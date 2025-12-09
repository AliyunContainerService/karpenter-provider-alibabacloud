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

package instancetype

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Provider handles instance type operations for Alibaba Cloud
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

// InstanceType represents an ECS instance type
type InstanceType struct {
	Name         string
	Architecture string
	CPU          *resource.Quantity
	Memory       *resource.Quantity
	Storage      *resource.Quantity
	GPU          *GPU
	Zones        map[string]ZoneInfo
}

// ZoneInfo represents information about an instance type in a specific zone
type ZoneInfo struct {
	Available bool
}

// GPU represents GPU information
type GPU struct {
	Count  *resource.Quantity
	Model  string
	Memory *resource.Quantity
}

// InstanceTypeRequirement represents a requirement for instance types
type InstanceTypeRequirement struct {
	// Key is the requirement key
	Key string
	// Operator is the requirement operator
	Operator InstanceTypeOperator
	// Values are the requirement values
	Values []string
}

// InstanceTypeOperator is the operator for instance type requirements
type InstanceTypeOperator string

const (
	// InstanceTypeOperatorIn is the "In" operator
	InstanceTypeOperatorIn InstanceTypeOperator = "In"
	// InstanceTypeOperatorNotIn is the "NotIn" operator
	InstanceTypeOperatorNotIn InstanceTypeOperator = "NotIn"
	// InstanceTypeOperatorExists is the "Exists" operator
	InstanceTypeOperatorExists InstanceTypeOperator = "Exists"
	// InstanceTypeOperatorDoesNotExist is the "DoesNotExist" operator
	InstanceTypeOperatorDoesNotExist InstanceTypeOperator = "DoesNotExist"
)

// NewProvider creates a new instance type provider
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
	entry, exists := p.cache[key]
	if !exists {
		p.cacheMu.RUnlock()
		return nil, false
	}

	// Check if cache entry is expired
	if time.Now().After(entry.ExpiresAt) {
		p.cacheMu.RUnlock()
		// Remove expired entry
		p.cacheMu.Lock()
		delete(p.cache, key)
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

// convertECSInstanceType converts an ECS instance type to our InstanceType structure
func (p *Provider) convertECSInstanceType(ecsInstanceType ecs.InstanceType, zones map[string]ZoneInfo) *InstanceType {
	cpu := resource.NewQuantity(int64(ecsInstanceType.CpuCoreCount), resource.DecimalSI)
	// 修复内存大小转换：阿里云返回的是GiB单位，需要正确转换为字节
	memoryGiB := ecsInstanceType.MemorySize
	// Prevent overflow: max memory is ~16 EiB (int64 max / 1024^3)
	if memoryGiB > float64(1<<53) {
		memoryGiB = float64(1 << 53)
	}
	memoryBytes := int64(memoryGiB * 1024 * 1024 * 1024) // GiB to bytes
	memory := resource.NewQuantity(memoryBytes, resource.BinarySI)
	storage := resource.NewQuantity(0, resource.DecimalSI) // Storage is not directly available

	// Get architecture from ECS API, fallback to amd64 if empty
	architecture := ecsInstanceType.CpuArchitecture

	// Get GPU information if available
	var gpu *GPU
	if ecsInstanceType.GPUAmount > 0 {
		gpuCount := resource.NewQuantity(int64(ecsInstanceType.GPUAmount), resource.DecimalSI)
		gpu = &GPU{
			Count: gpuCount,
			Model: ecsInstanceType.GPUSpec,
		}
	}

	return &InstanceType{
		Name:         ecsInstanceType.InstanceTypeId,
		Architecture: architecture,
		CPU:          cpu,
		Memory:       memory,
		Storage:      storage,
		GPU:          gpu,
		Zones:        zones,
	}
}

// getAvailableZones fetches available zones from Alibaba Cloud API
func (p *Provider) getAvailableZones(ctx context.Context) (map[string]ZoneInfo, error) {
	logger := log.FromContext(ctx)

	// Check cache first
	cacheKey := "zones"
	if cachedValue, exists := p.getCachedValue(cacheKey); exists {
		if zones, ok := cachedValue.(map[string]ZoneInfo); ok {
			logger.V(1).Info("Found zones in cache")
			return zones, nil
		}
		logger.V(1).Info("Invalid cache entry type for zones, refetching")
	}

	// Execute request
	response, err := p.ecsClient.DescribeZones(ctx)
	if err != nil {
		logger.Error(err, "failed to describe zones")
		return nil, fmt.Errorf("failed to describe zones: %w", err)
	}

	// Convert to our zone information
	zones := make(map[string]ZoneInfo)
	for _, zone := range response.Zones.Zone {
		// Mark all zones as available by default
		zones[zone.ZoneId] = ZoneInfo{Available: true}
	}

	// Cache the result
	p.setCachedValue(cacheKey, zones)

	return zones, nil
}

// List lists all available instance types
func (p *Provider) List(ctx context.Context) ([]*InstanceType, error) {
	logger := log.FromContext(ctx)

	// Check cache first
	cacheKey := "instance-types"
	if cachedValue, exists := p.getCachedValue(cacheKey); exists {
		if instanceTypes, ok := cachedValue.([]*InstanceType); ok {
			logger.V(1).Info("Found instance types in cache")
			return instanceTypes, nil
		}
		logger.V(1).Info("Invalid cache entry type for instance types, refetching")
	}

	// Get available zones
	zones, err := p.getAvailableZones(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get available zones: %w", err)
	}

	// Execute request
	response, err := p.ecsClient.DescribeInstanceTypes(ctx, nil)
	if err != nil {
		logger.Error(err, "failed to describe instance types")
		return nil, fmt.Errorf("failed to describe instance types: %w", err)
	}

	// Convert to our InstanceType type
	var instanceTypes []*InstanceType
	for _, it := range response.InstanceTypes.InstanceType {
		instanceType := p.convertECSInstanceType(it, zones)

		// Log instance type information for debugging (only at debug level)
		logger.V(1).Info("Found instance type",
			"name", it.InstanceTypeId,
			"cpu", it.CpuCoreCount,
			"memoryGiB", it.MemorySize,
			"zones", zones)

		instanceTypes = append(instanceTypes, instanceType)
	}

	// Cache the result - both the full list and individual instance types
	p.setCachedValue(cacheKey, instanceTypes)
	// Also cache each instance type individually for Get() method
	for _, it := range instanceTypes {
		individualKey := fmt.Sprintf("instance-type-%s", it.Name)
		p.setCachedValue(individualKey, it)
	}

	return instanceTypes, nil
}

// Get gets a specific instance type by name
func (p *Provider) Get(ctx context.Context, instanceTypeName string) (*InstanceType, error) {
	logger := log.FromContext(ctx)

	// Check cache first
	cacheKey := fmt.Sprintf("instance-type-%s", instanceTypeName)
	if cachedValue, exists := p.getCachedValue(cacheKey); exists {
		if instanceType, ok := cachedValue.(*InstanceType); ok {
			logger.V(1).Info("Found instance type in cache", "name", instanceTypeName)
			return instanceType, nil
		}
		logger.V(1).Info("Invalid cache entry type for instance type, refetching", "name", instanceTypeName)
	}

	// Get available zones
	zones, err := p.getAvailableZones(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get available zones: %w", err)
	}

	// Execute request
	instanceTypesList := []string{instanceTypeName}
	response, err := p.ecsClient.DescribeInstanceTypes(ctx, instanceTypesList)
	if err != nil {
		logger.Error(err, "failed to describe instance type", "instanceType", instanceTypeName)
		return nil, fmt.Errorf("failed to describe instance type %s: %w", instanceTypeName, err)
	}

	if len(response.InstanceTypes.InstanceType) == 0 {
		return nil, fmt.Errorf("instance type %s not found", instanceTypeName)
	}

	it := response.InstanceTypes.InstanceType[0]
	instanceType := p.convertECSInstanceType(it, zones)

	// Cache the result
	p.setCachedValue(cacheKey, instanceType)

	return instanceType, nil
}

// Filter filters instance types by requirements
func (p *Provider) Filter(ctx context.Context, instanceTypes []*InstanceType, requirements []InstanceTypeRequirement) []*InstanceType {
	var filtered []*InstanceType

	for _, it := range instanceTypes {
		matches := true

		for _, req := range requirements {
			if !p.matchesRequirement(it, req) {
				matches = false
				break
			}
		}

		if matches {
			filtered = append(filtered, it)
		}
	}

	return filtered
}

// matchesRequirement checks if an instance type matches a single requirement
func (p *Provider) matchesRequirement(it *InstanceType, req InstanceTypeRequirement) bool {
	switch req.Key {
	case "node.kubernetes.io/instance-type":
		return p.matchesStringRequirement(it.Name, req.Operator, req.Values)
	case "kubernetes.io/arch":
		return p.matchesStringRequirement(it.Architecture, req.Operator, req.Values)
	case "topology.kubernetes.io/zone":
		return p.matchesZoneRequirement(it, req.Operator, req.Values)
	default:
		// Unknown requirement key, assume it matches
		return true
	}
}

// matchesStringRequirement checks if a string value matches the requirement
func (p *Provider) matchesStringRequirement(value string, operator InstanceTypeOperator, values []string) bool {
	switch operator {
	case InstanceTypeOperatorIn:
		for _, v := range values {
			if value == v {
				return true
			}
		}
		return false
	case InstanceTypeOperatorNotIn:
		for _, v := range values {
			if value == v {
				return false
			}
		}
		return true
	case InstanceTypeOperatorExists:
		return value != ""
	case InstanceTypeOperatorDoesNotExist:
		return value == ""
	default:
		return true
	}
}

// matchesZoneRequirement checks if an instance type is available in the required zones
func (p *Provider) matchesZoneRequirement(it *InstanceType, operator InstanceTypeOperator, values []string) bool {
	switch operator {
	case InstanceTypeOperatorIn:
		// Must be available in at least one of the specified zones
		for _, value := range values {
			if zoneInfo, exists := it.Zones[value]; exists && zoneInfo.Available {
				return true
			}
		}
		return false
	case InstanceTypeOperatorNotIn:
		// Must not be available in any of the specified zones
		for _, value := range values {
			if zoneInfo, exists := it.Zones[value]; exists && zoneInfo.Available {
				return false
			}
		}
		return true
	case InstanceTypeOperatorExists:
		// Must have at least one available zone
		for _, zoneInfo := range it.Zones {
			if zoneInfo.Available {
				return true
			}
		}
		return false
	case InstanceTypeOperatorDoesNotExist:
		// Must have no available zones
		for _, zoneInfo := range it.Zones {
			if zoneInfo.Available {
				return false
			}
		}
		return true
	default:
		return true
	}
}

// Sort sorts instance types by CPU and memory
func (p *Provider) Sort(instanceTypes []*InstanceType) []*InstanceType {
	sort.Slice(instanceTypes, func(i, j int) bool {
		// Sort by CPU first
		if instanceTypes[i].CPU != nil && instanceTypes[j].CPU != nil {
			cpuCmp := instanceTypes[i].CPU.Cmp(*instanceTypes[j].CPU)
			if cpuCmp != 0 {
				return cpuCmp < 0
			}
		}

		// Then by memory
		if instanceTypes[i].Memory != nil && instanceTypes[j].Memory != nil {
			return instanceTypes[i].Memory.Cmp(*instanceTypes[j].Memory) < 0
		}

		// If one or both are nil, compare names to maintain consistent ordering
		return instanceTypes[i].Name < instanceTypes[j].Name
	})

	return instanceTypes
}

// ClearCache clears the instance type cache
func (p *Provider) ClearCache() {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	p.cache = make(map[string]*CacheEntry)
}

func (p *Provider) GetImageSupportInstanceTypes() []string {
	return []string{}
}
