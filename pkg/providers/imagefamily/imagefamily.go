/*
Copyright 2024 The Alibaba Cloud Karpenter Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package imagefamily

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
)

// Provider handles image operations for Alibaba Cloud
type Provider struct {
	ecsClient clients.ECSClient
	cache     map[string]*CacheEntry
	cacheMu   sync.RWMutex
	cacheTTL  time.Duration
}

type ImageQuery struct {
	ID              string
	ImageFamily     string
	Name            string
	ImageOwnerAlias string
	ImageOwnerID    string
	Tags            map[string]string
}

var imageOwnerIDRegex = regexp.MustCompile(`^[1-9][0-9]{5,19}$`)

// CacheEntry represents a cached result with expiration
type CacheEntry struct {
	Value     interface{}
	ExpiresAt time.Time
}

// Image represents an ECS image
type Image struct {
	ID           string
	Name         string
	OSType       string
	Architecture string
	CreationTime time.Time
}

// NewProvider creates a new image provider
func NewProvider(ecsClient clients.ECSClient) *Provider {
	return &Provider{
		ecsClient: ecsClient,
		cache:     make(map[string]*CacheEntry),
		cacheTTL:  2 * time.Hour, // Cache for 2 hours by default
	}
}

// SetCacheTTL sets the cache TTL duration
func (p *Provider) SetCacheTTL(ttl time.Duration) {
	p.cacheTTL = ttl
}

// getCachedValue retrieves a value from cache if it exists and is not expired
func (p *Provider) getCachedValue(key string) (interface{}, bool) {
	p.cacheMu.RLock()
	entry, exists := p.cache[key]
	if !exists {
		p.cacheMu.RUnlock()
		return nil, false
	}

	// Check if cache entry is expired
	if time.Now().After(entry.ExpiresAt) {
		p.cacheMu.RUnlock()
		// Remove expired entry with write lock
		p.cacheMu.Lock()
		// Double-check to prevent concurrent deletions
		if entry2, exists := p.cache[key]; exists && time.Now().After(entry2.ExpiresAt) {
			delete(p.cache, key)
		}
		p.cacheMu.Unlock()
		return nil, false
	}

	value := entry.Value
	p.cacheMu.RUnlock()
	return value, true
}

// setCachedValue stores a value in cache with expiration
func (p *Provider) setCachedValue(key string, value interface{}) {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()

	p.cache[key] = &CacheEntry{
		Value:     value,
		ExpiresAt: time.Now().Add(p.cacheTTL),
	}
}

// Resolve resolves image selectors to actual images
func (p *Provider) Resolve(ctx context.Context, terms []v1alpha1.ImageSelectorTerm) ([]v1alpha1.Image, error) {
	// If no selector terms, return empty list
	if len(terms) == 0 {
		return []v1alpha1.Image{}, nil
	}

	// Create cache key from terms
	cacheKey := fmt.Sprintf("images-%v", terms)

	// Check cache first
	if images, exists := p.getCachedValue(cacheKey); exists {
		return images.([]v1alpha1.Image), nil
	}

	var result []v1alpha1.Image

	for i, term := range terms {
		query := imageQueryFromTerm(term)
		if query.ImageOwnerID != "" && !imageOwnerIDRegex.MatchString(query.ImageOwnerID) {
			return nil, fmt.Errorf("resolve imageSelectorTerms[%d]: imageOwnerID %q is invalid", i, query.ImageOwnerID)
		}
		images, err := p.getByQuery(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("resolve imageSelectorTerms[%d]: %w", i, err)
		}
		result = append(result, images...)
	}

	// Remove duplicates
	result = removeDuplicateImages(result)

	// Cache the result
	p.setCachedValue(cacheKey, result)

	return result, nil
}

func imageQueryFromTerm(term v1alpha1.ImageSelectorTerm) ImageQuery {
	query := ImageQuery{Tags: term.Tags}
	if term.ID != nil {
		query.ID = *term.ID
	}
	if term.ImageFamily != nil {
		query.ImageFamily = *term.ImageFamily
	}
	if term.Name != nil {
		query.Name = *term.Name
	}
	if term.ImageOwnerAlias != nil {
		query.ImageOwnerAlias = *term.ImageOwnerAlias
	}
	if term.ImageOwnerID != nil {
		query.ImageOwnerID = *term.ImageOwnerID
	}
	return query
}

func (p *Provider) getByQuery(ctx context.Context, query ImageQuery) ([]v1alpha1.Image, error) {
	imageIDs := []string(nil)
	filters := map[string]string{}
	if query.ID != "" {
		imageIDs = []string{query.ID}
	}
	if query.ImageFamily != "" {
		filters["ImageFamily"] = query.ImageFamily
	}
	if query.Name != "" {
		filters["ImageName"] = query.Name
	}
	if query.ImageOwnerAlias != "" {
		filters["ImageOwnerAlias"] = query.ImageOwnerAlias
	}
	if query.ImageOwnerID != "" {
		filters["ImageOwnerID"] = query.ImageOwnerID
	}
	for k, v := range query.Tags {
		filters["tag:"+k] = v
	}
	if len(filters) == 0 {
		filters = nil
	}

	images, err := p.ecsClient.DescribeImages(ctx, imageIDs, filters)
	if err != nil {
		return nil, fmt.Errorf("failed to describe images: %w", err)
	}

	result := make([]v1alpha1.Image, 0, len(images))
	for _, img := range images {
		if img.ImageId == nil {
			continue
		}
		image := v1alpha1.Image{ID: *img.ImageId}
		if img.ImageName != nil {
			image.Name = *img.ImageName
		}
		if img.Architecture != nil {
			image.Architecture = *img.Architecture
		}
		result = append(result, image)
	}
	return result, nil
}

// removeDuplicateImages removes duplicate images from a slice
func removeDuplicateImages(images []v1alpha1.Image) []v1alpha1.Image {
	seen := make(map[string]bool)
	var result []v1alpha1.Image

	for _, img := range images {
		if img.ID != "" && !seen[img.ID] {
			seen[img.ID] = true
			result = append(result, img)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})

	return result
}

// ClearCache clears the image cache
func (p *Provider) ClearCache() {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	p.cache = make(map[string]*CacheEntry)
}
