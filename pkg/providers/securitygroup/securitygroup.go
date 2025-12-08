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

package securitygroup

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Provider handles security group operations for Alibaba Cloud
type Provider struct {
	region    string
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

// NewProvider creates a new security group provider
func NewProvider(region string, ecsClient clients.ECSClient) *Provider {
	return &Provider{
		region:    region,
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

// Resolve resolves security group selectors to actual security groups
func (p *Provider) Resolve(ctx context.Context, terms []v1alpha1.SecurityGroupSelectorTerm) ([]v1alpha1.SecurityGroup, error) {
	logger := log.FromContext(ctx)

	// If no selector terms, return empty list
	if len(terms) == 0 {
		return []v1alpha1.SecurityGroup{}, nil
	}

	// Create cache key from terms
	cacheKey := fmt.Sprintf("security-groups-%v", terms)

	// Check cache first
	if sgs, exists := p.getCachedValue(cacheKey); exists {
		logger.Info("Found security groups in cache")
		return sgs.([]v1alpha1.SecurityGroup), nil
	}

	var securityGroups []v1alpha1.SecurityGroup

	// Process each selector term
	for _, term := range terms {
		if term.ID != nil {
			// Direct ID reference
			securityGroups = append(securityGroups, v1alpha1.SecurityGroup{
				ID: *term.ID,
			})
		} else if term.Tags != nil {
			// Tag-based selection
			sgs, err := p.getByTags(ctx, term.Tags)
			if err != nil {
				logger.Error(err, "failed to get security groups by tags", "tags", term.Tags)
				return nil, fmt.Errorf("failed to get security groups by tags %v: %w", term.Tags, err)
			}
			securityGroups = append(securityGroups, sgs...)
		}
	}

	// Remove duplicates
	securityGroups = removeDuplicateSecurityGroups(securityGroups)

	// Cache the result
	p.setCachedValue(cacheKey, securityGroups)

	return securityGroups, nil
}

// getByTags gets security groups by tags
func (p *Provider) getByTags(ctx context.Context, tags map[string]string) ([]v1alpha1.SecurityGroup, error) {
	logger := log.FromContext(ctx)

	// Execute request
	response, err := p.ecsClient.DescribeSecurityGroups(ctx, tags)
	if err != nil {
		logger.Error(err, "failed to describe security groups")
		return nil, fmt.Errorf("failed to describe security groups: %w", err)
	}

	// Convert to our SecurityGroup type
	var securityGroups []v1alpha1.SecurityGroup
	for _, sg := range response.SecurityGroups.SecurityGroup {
		securityGroups = append(securityGroups, v1alpha1.SecurityGroup{
			ID: sg.SecurityGroupId,
		})
	}

	return securityGroups, nil
}

// removeDuplicateSecurityGroups removes duplicate security groups from a slice
func removeDuplicateSecurityGroups(sgs []v1alpha1.SecurityGroup) []v1alpha1.SecurityGroup {
	seen := make(map[string]bool)
	var result []v1alpha1.SecurityGroup

	for _, sg := range sgs {
		if !seen[sg.ID] {
			seen[sg.ID] = true
			result = append(result, sg)
		}
	}

	return result
}

// ClearCache clears the security group cache
func (p *Provider) ClearCache() {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	p.cache = make(map[string]*CacheEntry)
}
