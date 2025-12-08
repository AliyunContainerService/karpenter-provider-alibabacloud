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
	"sync"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Provider handles image operations for Alibaba Cloud
type Provider struct {
	ecsClient clients.ECSClient
	cache     map[string]*CacheEntry
	cacheMu   sync.RWMutex
	cacheTTL  time.Duration
}

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
		cacheTTL:  5 * time.Minute, // Cache for 5 minutes by default
	}
}

// SetCacheTTL sets the cache TTL duration
func (p *Provider) SetCacheTTL(ttl time.Duration) {
	p.cacheTTL = ttl
}

// getCachedValue retrieves a value from cache if it exists and is not expired
func (p *Provider) getCachedValue(key string) (interface{}, bool) {
	p.cacheMu.RLock()
	defer p.cacheMu.RUnlock()

	entry, exists := p.cache[key]
	if !exists {
		return nil, false
	}

	// Check if cache entry is expired
	if time.Now().After(entry.ExpiresAt) {
		return nil, false
	}

	return entry.Value, true
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
	logger := log.FromContext(ctx)

	// If no selector terms, return empty list
	if len(terms) == 0 {
		return []v1alpha1.Image{}, nil
	}

	// Create cache key from terms
	cacheKey := fmt.Sprintf("images-%v", terms)

	// Check cache first
	if images, exists := p.getCachedValue(cacheKey); exists {
		logger.Info("Found images in cache")
		return images.([]v1alpha1.Image), nil
	}

	var result []v1alpha1.Image

	// Process each selector term
	for _, term := range terms {
		// If image family is specified, use it as a filter
		if term.ImageFamily != nil {
			filters := map[string]string{
				"ImageFamily": *term.ImageFamily,
			}
			images, err := p.ecsClient.DescribeImages(ctx, nil, filters)
			if err != nil {
				logger.Error(err, "failed to describe images by family", "family", *term.ImageFamily)
				return nil, fmt.Errorf("failed to describe images by family %s: %w", *term.ImageFamily, err)
			}

			for _, img := range images {
				result = append(result, v1alpha1.Image{
					ID:           img.ImageId,
					Name:         img.ImageName,
					Architecture: img.Architecture,
				})
			}
		} else if term.ID != nil {
			// Direct ID reference
			images, err := p.ecsClient.DescribeImages(ctx, []string{*term.ID}, nil)
			if err != nil {
				logger.Error(err, "failed to describe image by ID", "id", *term.ID)
				return nil, fmt.Errorf("failed to describe image by ID %s: %w", *term.ID, err)
			}

			for _, img := range images {
				result = append(result, v1alpha1.Image{
					ID:           img.ImageId,
					Name:         img.ImageName,
					Architecture: img.Architecture,
				})
			}
		} else if term.Name != nil {
			// Name-based selection would require a different API call
			// For now, we'll skip this as it's not commonly used
			logger.Info("Image name-based selection is not implemented", "name", *term.Name)
		}
	}

	// Remove duplicates
	result = removeDuplicateImages(result)

	// Cache the result
	p.setCachedValue(cacheKey, result)

	return result, nil
}

// removeDuplicateImages removes duplicate images from a slice
func removeDuplicateImages(images []v1alpha1.Image) []v1alpha1.Image {
	seen := make(map[string]bool)
	var result []v1alpha1.Image

	for _, img := range images {
		if !seen[img.ID] {
			seen[img.ID] = true
			result = append(result, img)
		}
	}

	return result
}

// ClearCache clears the image cache
func (p *Provider) ClearCache() {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	p.cache = make(map[string]*CacheEntry)
}
