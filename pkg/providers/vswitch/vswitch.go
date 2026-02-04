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

package vswitch

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Provider handles VSwitch operations for Alibaba Cloud
type Provider struct {
	region    string
	vpcClient clients.VPCClient
	cache     map[string]*CacheEntry
	cacheMu   sync.RWMutex
	cacheTTL  time.Duration
}

// CacheEntry represents a cached result with expiration
type CacheEntry struct {
	Value     interface{}
	ExpiresAt time.Time
}

// NewProvider creates a new VSwitch provider
func NewProvider(region string, vpcClient clients.VPCClient) *Provider {
	return &Provider{
		region:    region,
		vpcClient: vpcClient,
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

// Resolve resolves VSwitch selectors to actual VSwitches
func (p *Provider) Resolve(ctx context.Context, terms []v1alpha1.VSwitchSelectorTerm) ([]v1alpha1.VSwitch, error) {
	logger := log.FromContext(ctx)

	// If no selector terms, return empty list
	if len(terms) == 0 {
		return []v1alpha1.VSwitch{}, nil
	}

	// Create cache key from terms
	cacheKey := fmt.Sprintf("vswitches-%v", terms)

	// Check cache first
	if vsws, exists := p.getCachedValue(cacheKey); exists {
		logger.Info("Found VSwitches in cache")
		return vsws.([]v1alpha1.VSwitch), nil
	}

	var vswitches []v1alpha1.VSwitch

	// Process each selector term
	for _, term := range terms {
		if term.ID != nil {
			vsws, err := p.getByID(ctx, *term.ID)
			if err != nil {
				logger.Error(err, "failed to get VSwitch by ID", "id", *term.ID)
				return nil, fmt.Errorf("failed to get VSwitch by ID %s: %w", *term.ID, err)
			}
			// Direct ID reference
			vswitches = append(vswitches, *vsws)
		} else if term.Tags != nil {
			// Tag-based selection
			vsws, err := p.getByTags(ctx, term.Tags)
			if err != nil {
				logger.Error(err, "failed to get VSwitches by tags", "tags", term.Tags)
				return nil, fmt.Errorf("failed to get VSwitches by tags %v: %w", term.Tags, err)
			}
			vswitches = append(vswitches, vsws...)
		}
	}

	// Remove duplicates
	vswitches = removeDuplicateVSwitches(vswitches)

	// Cache the result
	p.setCachedValue(cacheKey, vswitches)

	return vswitches, nil
}

func (p *Provider) getByID(ctx context.Context, id string) (*v1alpha1.VSwitch, error) {
	logger := log.FromContext(ctx)

	// Execute request
	response, err := p.vpcClient.DescribeVSwitches(ctx, id, nil)
	if err != nil || response == nil || len(response.Body.VSwitches.VSwitch) == 0 {
		logger.Error(err, "failed to describe VSwitches by id %s", id)
		return nil, fmt.Errorf("failed to describe VSwitches: %w", err)
	}
	return &v1alpha1.VSwitch{
		ID:                      *response.Body.VSwitches.VSwitch[0].VSwitchId,
		Zone:                    *response.Body.VSwitches.VSwitch[0].ZoneId,
		ZoneID:                  *response.Body.VSwitches.VSwitch[0].ZoneId,
		AvailableIPAddressCount: int(*response.Body.VSwitches.VSwitch[0].AvailableIpAddressCount),
	}, nil
}

// getByTags gets VSwitches by tags
func (p *Provider) getByTags(ctx context.Context, tags map[string]string) ([]v1alpha1.VSwitch, error) {
	logger := log.FromContext(ctx)

	// Execute request
	response, err := p.vpcClient.DescribeVSwitches(ctx, "", tags)
	if err != nil || response == nil || len(response.Body.VSwitches.VSwitch) == 0 {
		logger.Error(err, "failed to describe VSwitches")
		return nil, fmt.Errorf("failed to describe VSwitches: %w", err)
	}

	// Convert to our VSwitch type
	var vswitches []v1alpha1.VSwitch
	for _, vsw := range response.Body.VSwitches.VSwitch {
		vswitches = append(vswitches, v1alpha1.VSwitch{
			ID:   *vsw.VSwitchId,
			Zone: *vsw.ZoneId,
		})
	}

	return vswitches, nil
}

// removeDuplicateVSwitches removes duplicate VSwitches from a slice
func removeDuplicateVSwitches(vsws []v1alpha1.VSwitch) []v1alpha1.VSwitch {
	seen := make(map[string]bool)
	var result []v1alpha1.VSwitch

	for _, vsw := range vsws {
		if !seen[vsw.ID] {
			seen[vsw.ID] = true
			result = append(result, vsw)
		}
	}

	return result
}

// ClearCache clears the VSwitch cache
func (p *Provider) ClearCache() {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	p.cache = make(map[string]*CacheEntry)
}
