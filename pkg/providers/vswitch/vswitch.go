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
	"sort"
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

type VSwitchQuery struct {
	ID     string
	Tags   map[string]string
	ZoneID string
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

	for i, term := range terms {
		query := vSwitchQueryFromTerm(term)
		vsws, err := p.getByQuery(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("resolve vSwitchSelectorTerms[%d]: %w", i, err)
		}
		vswitches = append(vswitches, vsws...)
	}

	// Remove duplicates
	vswitches = removeDuplicateVSwitches(vswitches)

	// Only cache non-empty results. Caching empty results would block future reconciliations
	// when the API temporarily returns no VSwitches (e.g., transient error, eventual consistency).
	// The status controller would get stuck for the full cache TTL (2 hours) with empty VSwitches,
	// causing GetInstanceTypes to produce 0 offerings and blocking node provisioning.
	if len(vswitches) > 0 {
		p.setCachedValue(cacheKey, vswitches)
	}

	return vswitches, nil
}

func vSwitchQueryFromTerm(term v1alpha1.VSwitchSelectorTerm) VSwitchQuery {
	query := VSwitchQuery{Tags: term.Tags}
	if term.ID != nil {
		query.ID = *term.ID
	}
	if term.ZoneID != nil {
		query.ZoneID = *term.ZoneID
	}
	return query
}

func (p *Provider) getByQuery(ctx context.Context, query VSwitchQuery) ([]v1alpha1.VSwitch, error) {
	response, err := p.vpcClient.DescribeVSwitches(ctx, query.ID, query.Tags, query.ZoneID)
	if err != nil {
		return nil, fmt.Errorf("failed to describe VSwitches: %w", err)
	}
	if response == nil || response.Body == nil || response.Body.VSwitches == nil || len(response.Body.VSwitches.VSwitch) == 0 {
		return []v1alpha1.VSwitch{}, nil
	}

	var vswitches []v1alpha1.VSwitch
	for _, vsw := range response.Body.VSwitches.VSwitch {
		if vsw == nil || vsw.VSwitchId == nil {
			continue
		}
		zoneID := ""
		if vsw.ZoneId != nil {
			zoneID = *vsw.ZoneId
		}
		availableIPCount := 0
		if vsw.AvailableIpAddressCount != nil {
			availableIPCount = int(*vsw.AvailableIpAddressCount)
		}
		vswitches = append(vswitches, v1alpha1.VSwitch{
			ID:                      *vsw.VSwitchId,
			Zone:                    zoneID,
			ZoneID:                  zoneID,
			AvailableIPAddressCount: availableIPCount,
		})
	}
	return vswitches, nil
}

func (p *Provider) getByID(ctx context.Context, id string) (*v1alpha1.VSwitch, error) {

	vswitches, err := p.getByQuery(ctx, VSwitchQuery{ID: id})
	if err != nil {
		return nil, err
	}
	if len(vswitches) == 0 {
		return nil, fmt.Errorf("VSwitch %s not found", id)
	}
	return &vswitches[0], nil
}

// getByTags gets VSwitches by tags
func (p *Provider) getByTags(ctx context.Context, tags map[string]string) ([]v1alpha1.VSwitch, error) {
	return p.getByQuery(ctx, VSwitchQuery{Tags: tags})
}

// removeDuplicateVSwitches removes duplicate VSwitches from a slice
func removeDuplicateVSwitches(vsws []v1alpha1.VSwitch) []v1alpha1.VSwitch {
	seen := make(map[string]bool)
	var result []v1alpha1.VSwitch

	for _, vsw := range vsws {
		if vsw.ID != "" && !seen[vsw.ID] {
			seen[vsw.ID] = true
			result = append(result, vsw)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})

	return result
}

// ClearCache clears the VSwitch cache
func (p *Provider) ClearCache() {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	p.cache = make(map[string]*CacheEntry)
}
