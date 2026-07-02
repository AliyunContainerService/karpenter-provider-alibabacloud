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

package instance

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"

	"sigs.k8s.io/karpenter/pkg/cloudprovider"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/batcher"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/errors"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Provider handles ECS instance operations for Alibaba Cloud
type Provider struct {
	region    string
	ecsClient clients.ECSClient
	Batcher   *batcher.Batcher
	cache     map[string]*CacheEntry
	cacheMu   sync.RWMutex
	cacheTTL  time.Duration
}

// CacheEntry represents a cached instance with expiration
type CacheEntry struct {
	Instance  *Instance
	ExpiresAt time.Time
}

// CreateOptions represents options for creating an instance
type CreateOptions struct {
	InstanceType        string
	ImageID             string
	VSwitchID           string
	SecurityGroupIDs    []string
	UserData            string
	Tags                map[string]string
	SystemDisk          SystemDisk
	DataDisks           []DataDisk
	SpotStrategy        string
	SpotPriceLimit      float64
	InstanceStorePolicy *string // Add instance store policy field
}

// SystemDisk represents system disk configuration
type SystemDisk struct {
	Category         string
	Size             int32
	PerformanceLevel string
}

// DataDisk represents data disk configuration
type DataDisk struct {
	Category         string
	Size             int32
	PerformanceLevel string
	Device           string
}

// Instance represents an ECS instance
type Instance struct {
	InstanceID       string
	Region           string
	Zone             string
	InstanceType     string
	ImageID          string
	CPU              resource.Quantity
	Memory           resource.Quantity
	Storage          resource.Quantity
	GPU              resource.Quantity
	GPUMem           resource.Quantity
	GPUSpec          string
	Architecture     string
	Status           string
	CreationTime     string
	Tags             map[string]string
	CapacityType     string
	SecurityGroupIDs []string
	VSwitchID        string
}

// NewProvider creates a new instance provider
// 参考AWS Karpenter的优化设置，使用更短的批处理时间间隔以提高响应速度
func NewProvider(ctx context.Context, region string, ecsClient clients.ECSClient) *Provider {
	return &Provider{
		region:    region,
		ecsClient: ecsClient,
		Batcher:   batcher.NewBatcher(ctx, 10*time.Millisecond, 1*time.Millisecond), // 初始化batcher，使用优化的时间间隔
		cache:     make(map[string]*CacheEntry),
		cacheTTL:  30 * time.Second, // 缩短缓存TTL以更快响应变化
	}
}

// SetCacheTTL sets the cache TTL duration
func (p *Provider) SetCacheTTL(ttl time.Duration) {
	p.cacheTTL = ttl
}

// getCachedInstance retrieves an instance from cache if it exists and is not expired
func (p *Provider) getCachedInstance(instanceID string) (*Instance, bool) {
	p.cacheMu.RLock()

	entry, exists := p.cache[instanceID]
	if !exists {
		p.cacheMu.RUnlock()
		return nil, false
	}

	// Check if cache entry is expired
	if time.Now().After(entry.ExpiresAt) {
		// Release read lock and acquire write lock to clean up expired entry
		p.cacheMu.RUnlock()
		p.cacheMu.Lock()
		// Double-check if entry is still expired after acquiring write lock
		if entry, exists := p.cache[instanceID]; exists && time.Now().After(entry.ExpiresAt) {
			delete(p.cache, instanceID)
		}
		p.cacheMu.Unlock()
		return nil, false
	}

	instance := entry.Instance
	p.cacheMu.RUnlock()
	return instance, true
}

// setCachedInstance stores an instance in cache with expiration
func (p *Provider) setCachedInstance(instanceID string, instance *Instance) {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()

	p.cache[instanceID] = &CacheEntry{
		Instance:  instance,
		ExpiresAt: time.Now().Add(p.cacheTTL),
	}
}

// deleteExpiredCacheEntries removes expired entries from cache
func (p *Provider) deleteExpiredCacheEntries() {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()

	now := time.Now()
	for instanceID, entry := range p.cache {
		if now.After(entry.ExpiresAt) {
			delete(p.cache, instanceID)
		}
	}
}

func (p *Provider) deleteCachedInstance(instanceID string) {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()

	delete(p.cache, instanceID)
}

// Create creates a new ECS instance
func (p *Provider) Create(ctx context.Context, opts CreateOptions) (string, error) {
	logger := log.FromContext(ctx)

	// Create instance request
	request := &ecs.RunInstancesRequest{
		RegionId:     tea.String(p.region),
		InstanceType: tea.String(opts.InstanceType),
		ImageId:      tea.String(opts.ImageID),
		VSwitchId:    tea.String(opts.VSwitchID),
	}

	// Set security group IDs
	if len(opts.SecurityGroupIDs) > 0 {
		request.SecurityGroupId = tea.String(opts.SecurityGroupIDs[0])
	}

	if opts.SpotStrategy != "" {
		request.SpotStrategy = tea.String(opts.SpotStrategy)
	}

	if opts.SpotPriceLimit > 0 {
		request.SpotPriceLimit = tea.Float32(float32(opts.SpotPriceLimit))
	}

	// Set system disk
	request.SystemDisk = &ecs.RunInstancesRequestSystemDisk{
		Category: tea.String(opts.SystemDisk.Category),
		Size:     tea.String(fmt.Sprintf("%d", opts.SystemDisk.Size)),
	}

	// Set data disks
	if len(opts.DataDisks) > 0 {
		var dataDisks []*ecs.RunInstancesRequestDataDisk
		for _, disk := range opts.DataDisks {
			dataDisk := &ecs.RunInstancesRequestDataDisk{
				Category: tea.String(disk.Category),
				Size:     tea.Int32(int32(disk.Size)),
			}
			if disk.Device != "" {
				dataDisk.Device = tea.String(disk.Device)
			}
			if disk.PerformanceLevel != "" {
				dataDisk.PerformanceLevel = tea.String(disk.PerformanceLevel)
			}
			dataDisks = append(dataDisks, dataDisk)
		}
		request.DataDisk = dataDisks
	}

	// Set user data
	if opts.UserData != "" {
		// Encode user data as base64 as required by Alibaba Cloud ECS API
		request.UserData = tea.String(base64.StdEncoding.EncodeToString([]byte(opts.UserData)))
	}

	// Set tags
	if len(opts.Tags) > 0 {
		var tags []*ecs.RunInstancesRequestTag
		for key, value := range opts.Tags {
			tags = append(tags, &ecs.RunInstancesRequestTag{
				Key:   tea.String(key),
				Value: tea.String(value),
			})
		}
		request.Tag = tags
	}

	// Handle instance store policy if specified
	if opts.InstanceStorePolicy != nil {
		policy := *opts.InstanceStorePolicy
		switch policy {
		case "RAID0":
			// For RAID0 policy, we would need to configure local disks
			// This is a placeholder implementation as the exact API usage
			// depends on the specific instance type and local disk configuration
			logger.Info("Applying RAID0 instance store policy")
			// In a real implementation, you would configure local disks here
			// based on the instance type's local disk specifications
		case "None":
			// No special handling needed for None policy
			logger.Info("Using None instance store policy")
		default:
			logger.Info("Unknown instance store policy, using default", "policy", policy)
		}
	}

	response, err := p.ecsClient.RunInstances(ctx, request)
	if err != nil {
		logger.Error(err, "failed to create instance")
		return "", fmt.Errorf("failed to create instance: %w", err)
	}

	// Safely check response chain for nil
	if response == nil || response.Body == nil || response.Body.InstanceIdSets == nil ||
		len(response.Body.InstanceIdSets.InstanceIdSet) == 0 {
		return "", fmt.Errorf("no instance ID returned from ECS API")
	}

	if response.Body.InstanceIdSets.InstanceIdSet[0] == nil {
		return "", fmt.Errorf("instance ID is nil in ECS API response")
	}
	instanceID := *response.Body.InstanceIdSets.InstanceIdSet[0]
	logger.Info("created instance", "instanceID", instanceID)

	return instanceID, nil
}

// Get retrieves an ECS instance by ID
func (p *Provider) Get(ctx context.Context, instanceID string) (*Instance, error) {
	logger := log.FromContext(ctx)

	// First check cache
	if instance, exists := p.getCachedInstance(instanceID); exists {
		logger.Info("Found instance in cache", "instanceID", instanceID)
		return instance, nil
	}

	// 使用批处理机制来减少API调用
	instanceIDs := []string{instanceID}
	results, err := p.Batcher.BatchDescribeInstances(ctx, instanceIDs, func(ids []string) (interface{}, error) {
		return p.describeInstances(ctx, ids)
	})
	if err != nil {
		return nil, fmt.Errorf("fail to describe instances: %w", err)
	}

	// 解析批处理结果
	batchResults, ok := results.([]interface{})
	if !ok || len(batchResults) == 0 {
		return nil, fmt.Errorf("invalid batch results for instance %s", instanceID)
	}

	response, ok := batchResults[0].(*ecs.DescribeInstancesResponse)
	if !ok {
		return nil, fmt.Errorf("invalid response type for instance %s", instanceID)
	}

	// Log response information for debugging (only at debug level)
	logger.V(1).Info("DescribeInstances response", "instanceCount", len(response.Body.Instances.Instance), "totalInstancesInRegion", *response.Body.TotalCount, "pageNumber", *response.Body.PageNumber, "pageSize", *response.Body.PageSize)

	if len(response.Body.Instances.Instance) == 0 {
		// Log additional information when instance is not found (only at debug level)
		logger.V(1).Info("Instance not found in response", "instanceID", instanceID, "region", p.region,
			"totalInstancesInRegion", *response.Body.TotalCount, "pageNumber", *response.Body.PageNumber, "pageSize", *response.Body.PageSize)

		// List instances in region for debugging purposes (only when instance is not found and at debug level)
		if *response.Body.TotalCount > 0 {
			logger.V(1).Info("List instances in region for debugging", "region", p.region, "totalInstances", *response.Body.TotalCount, "returnedInstances", len(response.Body.Instances.Instance))
			// Log sample instances for debugging
			maxSample := 5
			if len(response.Body.Instances.Instance) < maxSample {
				maxSample = len(response.Body.Instances.Instance)
			}
			for i := 0; i < maxSample; i++ {
				inst := response.Body.Instances.Instance[i]
				logger.V(1).Info("Sample instance in region", "sampleInstanceID", *inst.InstanceId, "status", *inst.Status)
			}
		}
		p.deleteCachedInstance(instanceID)
		return nil, cloudprovider.NewNodeClaimNotFoundError(fmt.Errorf("instance %s not found", instanceID))
	}

	inst := response.Body.Instances.Instance[0]

	// Log instance information for debugging (only at debug level)
	logger.V(1).Info("Found instance", "instanceID", *inst.InstanceId, "status", *inst.Status, "zone", *inst.ZoneId, "instanceType", *inst.InstanceType)

	// Convert to our Instance type
	cpu := resource.MustParse(fmt.Sprintf("%d", *inst.Cpu))
	memory := resource.MustParse(fmt.Sprintf("%dGi", *inst.Memory/1024)) // ECS returns memory in MiB
	gpuAmount := int32(0)
	if inst.GPUAmount != nil {
		gpuAmount = *inst.GPUAmount
	}
	gpu := resource.MustParse(fmt.Sprintf("%d", gpuAmount))
	storage := resource.MustParse("0") // Storage is not directly available in DescribeInstances response
	gpuMem := resource.MustParse("0")  // GPUMem is not directly available in DescribeInstances response

	// Get architecture from instance type
	architecture := "amd64"
	if strings.HasPrefix(*inst.InstanceType, "ecs.gn") || strings.HasPrefix(*inst.InstanceType, "ecs.cu") {
		architecture = "arm64"
	}

	var capacityType string
	if *inst.InstanceChargeType == "PostPaid" {
		capacityType = "on-demand"
	} else if *inst.InstanceChargeType == "PrePaid" {
		capacityType = "pre-paid"
	} else if inst.SpotStrategy != nil && *inst.SpotStrategy != "" && *inst.SpotStrategy != "NoSpot" {
		capacityType = "spot"
	}

	securityGroupIds := []string{}
	if inst.SecurityGroupIds != nil && inst.SecurityGroupIds.SecurityGroupId != nil {
		for _, sg := range inst.SecurityGroupIds.SecurityGroupId {
			if sg != nil {
				securityGroupIds = append(securityGroupIds, *sg)
			}
		}
	}

	vSwitchID := ""
	if inst.VpcAttributes != nil && inst.VpcAttributes.VSwitchId != nil {
		vSwitchID = *inst.VpcAttributes.VSwitchId
	}

	gpuSpec := ""
	if inst.GPUSpec != nil {
		gpuSpec = *inst.GPUSpec
	}

	instance := &Instance{
		InstanceID:       *inst.InstanceId,
		Region:           *inst.RegionId,
		Zone:             *inst.ZoneId,
		InstanceType:     *inst.InstanceType,
		ImageID:          *inst.ImageId,
		CPU:              cpu,
		Memory:           memory,
		Storage:          storage,
		Architecture:     architecture,
		Status:           *inst.Status,
		CreationTime:     *inst.CreationTime,
		Tags:             convertTags(inst.Tags),
		CapacityType:     capacityType,
		SecurityGroupIDs: securityGroupIds,
		VSwitchID:        vSwitchID,
		GPU:              gpu,
		GPUMem:           gpuMem,
		GPUSpec:          gpuSpec,
	}

	// Cache the instance
	p.setCachedInstance(instanceID, instance)

	return instance, nil
}

// describeInstances 是实际执行DescribeInstances API调用的方法
func (p *Provider) describeInstances(ctx context.Context, instanceIDs []string) (*ecs.DescribeInstancesResponse, error) {
	logger := log.FromContext(ctx)

	// Create describe instance request
	request := &ecs.DescribeInstancesRequest{
		RegionId: tea.String(p.region),
	}

	// Set instance IDs - 使用阿里云SDK推荐的方式
	if len(instanceIDs) > 0 {
		// 使用SDK的InstanceIds参数，直接传入字符串数组
		// 阿里云SDK会自动处理参数格式
		request.InstanceIds = tea.String(fmt.Sprintf("[\"%s\"]", strings.Join(instanceIDs, "\",\"")))
	}

	// Log the request for debugging (only at debug level)
	logger.V(1).Info("DescribeInstances request", "regionId", *request.RegionId, "instanceIds", *request.InstanceIds)

	response, err := p.ecsClient.DescribeInstances(ctx, request)
	if err != nil {
		if strings.Contains(err.Error(), "InvalidInstanceId.NotFound") ||
			strings.Contains(err.Error(), "InstanceNotFound") ||
			strings.Contains(err.Error(), "not found") {
			logger.V(1).Info("instance not found during describe operation", "instanceIDs", instanceIDs, "error", err.Error())
			return nil, NewNotFoundError(fmt.Errorf("instances %v not found: %w", instanceIDs, err))
		}
		logger.Error(err, "failed to describe instance", "instanceIDs", instanceIDs, "region", p.region)
		return nil, fmt.Errorf("failed to describe instances %v: %w", instanceIDs, err)
	}

	return response, nil
}

// Delete deletes an ECS instance by ID
func (p *Provider) Delete(ctx context.Context, instanceID string) error {
	logger := log.FromContext(ctx)

	// Log the deletion attempt for debugging (only at debug level)
	logger.Info("Attempting to delete instance", "instanceID", instanceID, "region", p.region)

	// Check instance status before attempting deletion using batcher
	logger.Info("Getting instance info before deletion", "instanceID", instanceID)
	inst, err := p.Get(ctx, instanceID)
	if err != nil {
		return err
	}

	logger.Info("Instance info retrieved", "instanceID", instanceID, "status", inst.Status)

	// Create delete instance request
	request := &ecs.DeleteInstancesRequest{
		RegionId:   tea.String(p.region),
		InstanceId: []*string{tea.String(instanceID)},
		Force:      tea.Bool(true),
	}

	// Log the delete request for debugging
	logger.Info("DeleteInstances request prepared", "regionId", *request.RegionId, "instanceIds", instanceID, "force", *request.Force)

	resp, deleteErr := p.ecsClient.DeleteInstances(ctx, request)
	if deleteErr == nil {
		requestId := ""
		if resp.Body != nil && resp.Body.RequestId != nil {
			requestId = *resp.Body.RequestId
		}
		logger.Info("Successfully deleted instance", "instanceID", instanceID, "requestId", requestId)
		p.deleteCachedInstance(instanceID)
	}
	if deleteErr != nil {
		if errors.IsNotFound(deleteErr) ||
			strings.Contains(deleteErr.Error(), "InvalidInstanceId.NotFound") ||
			strings.Contains(deleteErr.Error(), "InstanceNotFound") ||
			strings.Contains(deleteErr.Error(), "not found") {
			logger.Info("instance already deleted or not found, returning NodeClaimNotFoundError", "instanceID", instanceID, "error", deleteErr.Error())
			return cloudprovider.NewNodeClaimNotFoundError(fmt.Errorf("instance delete with err %w", deleteErr))
		}
		return fmt.Errorf("delete instance failed with err %w", deleteErr)
	}

	return nil
}

// List lists all ECS instances managed by Karpenter
func (p *Provider) List(ctx context.Context, tags map[string]string) ([]*Instance, error) {
	logger := log.FromContext(ctx)

	// Create describe instances request with Karpenter tag filter
	request := &ecs.DescribeInstancesRequest{
		RegionId: tea.String(p.region),
		PageSize: tea.Int32(100),
	}

	// Log the tags we're filtering by
	logger.Info("Listing instances with tags", "tags", tags)

	// Set tag filters
	var ecsTags []*ecs.DescribeInstancesRequestTag
	for key, value := range tags {
		ecsTags = append(ecsTags, &ecs.DescribeInstancesRequestTag{
			Key:   tea.String(key),
			Value: tea.String(value),
		})
	}
	request.Tag = ecsTags

	// Implement pagination
	var allInstances []*Instance
	pageNumber := int32(1)

	for {
		request.PageNumber = tea.Int32(pageNumber)

		response, err := p.ecsClient.DescribeInstances(ctx, request)
		if err != nil {
			logger.Error(err, "failed to list instances")
			return nil, fmt.Errorf("failed to list instances: %w", err)
		}

		// Safely check response chain for nil
		if response == nil || response.Body == nil || response.Body.Instances == nil {
			return nil, fmt.Errorf("invalid response from ECS API")
		}

		// Log response information
		totalCount := int32(0)
		if response.Body.TotalCount != nil {
			totalCount = *response.Body.TotalCount
		}
		logger.Info("DescribeInstances response", "totalCount", totalCount, "returnedCount", len(response.Body.Instances.Instance), "pageNumber", pageNumber)

		// Convert to our Instance type
		for _, inst := range response.Body.Instances.Instance {
			// Skip instance if essential fields are nil
			if inst.Cpu == nil || inst.Memory == nil {
				logger.Info("Skipping instance with missing CPU or Memory info")
				continue
			}
			cpu := resource.MustParse(fmt.Sprintf("%d", *inst.Cpu))
			memory := resource.MustParse(fmt.Sprintf("%dGi", *inst.Memory/1024)) // ECS returns memory in MiB
			storage := resource.MustParse("0")                                   // Storage is not directly available in DescribeInstances response
			gpuAmount := int32(0)
			if inst.GPUAmount != nil {
				gpuAmount = *inst.GPUAmount
			}
			gpu := resource.MustParse(fmt.Sprintf("%d", gpuAmount))
			gpuMem := resource.MustParse("0") // GPUMem is not directly available in DescribeInstances response

			// Get architecture from instance type
			architecture := "amd64"
			instType := derefString(inst.InstanceType)
			if strings.HasPrefix(instType, "ecs.gn") || strings.HasPrefix(instType, "ecs.cu") {
				architecture = "arm64"
			}

			// Determine capacity type
			var capacityType string
			chargeType := derefString(inst.InstanceChargeType)
			spotStrategy := derefString(inst.SpotStrategy)
			if chargeType == "PostPaid" {
				capacityType = "on-demand"
			} else if chargeType == "PrePaid" {
				capacityType = "pre-paid"
			} else if spotStrategy != "" && spotStrategy != "NoSpot" {
				capacityType = "spot"
			}

			securityGroupIds := []string{}
			if inst.SecurityGroupIds != nil && inst.SecurityGroupIds.SecurityGroupId != nil {
				for _, sg := range inst.SecurityGroupIds.SecurityGroupId {
					if sg != nil {
						securityGroupIds = append(securityGroupIds, *sg)
					}
				}
			}

			vSwitchID := ""
			if inst.VpcAttributes != nil && inst.VpcAttributes.VSwitchId != nil {
				vSwitchID = *inst.VpcAttributes.VSwitchId
			}

			gpuSpec := ""
			if inst.GPUSpec != nil {
				gpuSpec = *inst.GPUSpec
			}

			instance := &Instance{
				InstanceID:       derefString(inst.InstanceId),
				Region:           derefString(inst.RegionId),
				Zone:             derefString(inst.ZoneId),
				InstanceType:     derefString(inst.InstanceType),
				ImageID:          derefString(inst.ImageId),
				CPU:              cpu,
				Memory:           memory,
				Storage:          storage,
				Architecture:     architecture,
				Status:           derefString(inst.Status),
				CreationTime:     derefString(inst.CreationTime),
				Tags:             convertTags(inst.Tags),
				CapacityType:     capacityType,
				SecurityGroupIDs: securityGroupIds,
				VSwitchID:        vSwitchID,
				GPU:              gpu,
				GPUSpec:          gpuSpec,
				GPUMem:           gpuMem,
			}

			// Log each instance found
			logger.Info("Found instance", "instanceID", instance.InstanceID, "status", instance.Status, "tags", instance.Tags)

			allInstances = append(allInstances, instance)

			// Cache the instance
			if inst.InstanceId != nil {
				p.setCachedInstance(*inst.InstanceId, instance)
			}
		}

		// Check if there are more pages
		if int32(pageNumber*100) >= totalCount {
			break
		}
		pageNumber++
	}

	return allInstances, nil
}

// TagInstance tags an ECS instance with the given tags
func (p *Provider) TagInstance(ctx context.Context, instanceID string, tags map[string]string) error {
	logger := log.FromContext(ctx)

	// Create tag resources request
	request := &ecs.TagResourcesRequest{
		RegionId:     tea.String(p.region),
		ResourceType: tea.String("instance"),
		ResourceId:   []*string{tea.String(instanceID)},
	}

	// Convert tags to ECS format
	var ecsTags []*ecs.TagResourcesRequestTag
	for key, value := range tags {
		ecsTags = append(ecsTags, &ecs.TagResourcesRequestTag{
			Key:   tea.String(key),
			Value: tea.String(value),
		})
	}
	request.Tag = ecsTags

	// Execute request
	_, err := p.ecsClient.TagResources(ctx, request)
	if err != nil {
		logger.Error(err, "failed to tag instance", "instanceID", instanceID, "tags", tags)
		return fmt.Errorf("failed to tag instance %s: %w", instanceID, err)
	}

	logger.Info("tagged instance", "instanceID", instanceID, "tags", tags)
	return nil
}

// convertTags converts ECS tags to map
func convertTags(ecsTagsResp *ecs.DescribeInstancesResponseBodyInstancesInstanceTags) map[string]string {
	tags := make(map[string]string)
	if ecsTagsResp == nil {
		return tags
	}
	ecsTags := ecsTagsResp.Tag
	for _, tag := range ecsTags {
		if tag.TagKey != nil && tag.TagValue != nil {
			tags[*tag.TagKey] = *tag.TagValue
		}
	}
	return tags
}

// NotFoundError represents an error when an instance is not found
type NotFoundError struct {
	err error
}

// NewNotFoundError creates a new NotFoundError
func NewNotFoundError(err error) *NotFoundError {
	return &NotFoundError{err: err}
}

// Error returns the error message
func (e *NotFoundError) Error() string {
	return e.err.Error()
}

// IsNotFoundError checks if an error is a NotFoundError
func IsNotFoundError(err error) bool {
	_, ok := err.(*NotFoundError)
	return ok
}

// derefString safely dereferences a string pointer, returning empty string if nil
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
