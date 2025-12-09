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
	"math"
	"math/rand"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	"strings"
	"sync"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/batcher"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/errors"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
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
	request := ecs.CreateRunInstancesRequest()
	request.RegionId = p.region
	request.InstanceType = opts.InstanceType
	request.ImageId = opts.ImageID
	request.VSwitchId = opts.VSwitchID

	// Set security group IDs
	if len(opts.SecurityGroupIDs) > 0 {
		request.SecurityGroupId = opts.SecurityGroupIDs[0]
	}

	request.SpotStrategy = opts.SpotStrategy
	request.SpotPriceLimit = requests.NewFloat(opts.SpotPriceLimit)

	// Set system disk
	request.SystemDiskCategory = opts.SystemDisk.Category
	request.SystemDiskSize = fmt.Sprintf("%d", opts.SystemDisk.Size)

	// Set data disks
	if len(opts.DataDisks) > 0 {
		var dataDisks []ecs.RunInstancesDataDisk
		for _, disk := range opts.DataDisks {
			dataDisk := ecs.RunInstancesDataDisk{
				Category: disk.Category,
				Size:     fmt.Sprintf("%d", disk.Size),
			}
			if disk.PerformanceLevel != "" {
				dataDisk.PerformanceLevel = disk.PerformanceLevel
			}
			dataDisks = append(dataDisks, dataDisk)
		}
		request.DataDisk = &dataDisks
	}

	// Set user data
	if opts.UserData != "" {
		// Encode user data as base64 as required by Alibaba Cloud ECS API
		request.UserData = base64.StdEncoding.EncodeToString([]byte(opts.UserData))
	}

	// Set tags
	if len(opts.Tags) > 0 {
		var tags []ecs.RunInstancesTag
		for key, value := range opts.Tags {
			tags = append(tags, ecs.RunInstancesTag{
				Key:   key,
				Value: value,
			})
		}
		request.Tag = &tags
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

	// Implement retry mechanism for throttling errors
	var response *ecs.RunInstancesResponse
	var err error

	// Get retry strategy for throttling
	strategy := errors.GetRetryStrategy(fmt.Errorf("throttling"))
	if strategy == nil {
		// Default strategy if not found
		strategy = &errors.RetryStrategy{
			MaxAttempts:       5,
			InitialBackoff:    1000,  // 1s
			MaxBackoff:        16000, // 16s
			BackoffMultiplier: 2.0,
		}
	}

	for attempt := 0; attempt < strategy.MaxAttempts; attempt++ {
		// Execute request
		response, err = p.ecsClient.RunInstances(ctx, request)
		if err == nil {
			break // Success
		}

		// Check if it's a throttling error
		if errors.IsThrottlingError(err) {
			logger.Info("Throttling error encountered in Create, will retry", "attempt", attempt+1, "error", err.Error())

			// Calculate backoff with jitter
			backoff := time.Duration(strategy.InitialBackoff) * time.Millisecond
			if attempt > 0 {
				backoff = time.Duration(float64(backoff) * math.Pow(strategy.BackoffMultiplier, float64(attempt)))
			}

			// Cap at max backoff
			if backoff > time.Duration(strategy.MaxBackoff)*time.Millisecond {
				backoff = time.Duration(strategy.MaxBackoff) * time.Millisecond
			}

			// Add jitter (±50%)
			jitter := time.Duration(rand.Int63n(int64(backoff))) - backoff/2
			backoff += jitter

			// Ensure backoff is positive
			if backoff < 0 {
				backoff = time.Duration(strategy.InitialBackoff) * time.Millisecond
			}

			logger.Info("Waiting before retry in Create", "backoff", backoff.String(), "attempt", attempt+1)
			time.Sleep(backoff)
			continue
		}

		// For non-throttling errors, don't retry
		logger.Error(err, "failed to create instance")
		return "", fmt.Errorf("failed to create instance: %w", err)
	}

	if err != nil {
		logger.Error(err, "failed to create instance after retries")
		return "", fmt.Errorf("failed to create instance after %d attempts: %w", strategy.MaxAttempts, err)
	}

	if len(response.InstanceIdSets.InstanceIdSet) == 0 {
		return "", fmt.Errorf("no instance ID returned from ECS API")
	}

	instanceID := response.InstanceIdSets.InstanceIdSet[0]
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
	logger.V(1).Info("DescribeInstances response", "instanceCount", len(response.Instances.Instance), "totalInstancesInRegion", response.TotalCount, "pageNumber", response.PageNumber, "pageSize", response.PageSize)

	if len(response.Instances.Instance) == 0 {
		// Log additional information when instance is not found (only at debug level)
		logger.V(1).Info("Instance not found in response", "instanceID", instanceID, "region", p.region,
			"totalInstancesInRegion", response.TotalCount, "pageNumber", response.PageNumber, "pageSize", response.PageSize)

		// List instances in region for debugging purposes (only when instance is not found and at debug level)
		if response.TotalCount > 0 {
			logger.V(1).Info("List instances in region for debugging", "region", p.region, "totalInstances", response.TotalCount, "returnedInstances", len(response.Instances.Instance))
			// Log sample instances for debugging
			maxSample := 5
			if len(response.Instances.Instance) < maxSample {
				maxSample = len(response.Instances.Instance)
			}
			for i := 0; i < maxSample; i++ {
				inst := response.Instances.Instance[i]
				logger.V(1).Info("Sample instance in region", "sampleInstanceID", inst.InstanceId, "status", inst.Status)
			}
		}
		p.deleteCachedInstance(instanceID)
		return nil, cloudprovider.NewNodeClaimNotFoundError(fmt.Errorf("instance %s not found", instanceID))
	}

	inst := response.Instances.Instance[0]

	// Log instance information for debugging (only at debug level)
	logger.V(1).Info("Found instance", "instanceID", inst.InstanceId, "status", inst.Status, "zone", inst.ZoneId, "instanceType", inst.InstanceType)

	// Convert to our Instance type
	cpu := resource.MustParse(fmt.Sprintf("%d", inst.Cpu))
	memory := resource.MustParse(fmt.Sprintf("%dGi", inst.Memory/1024)) // ECS returns memory in MiB
	storage := resource.MustParse("0")                                  // Storage is not directly available in DescribeInstances response

	// Get architecture from instance type
	architecture := "amd64"
	if strings.HasPrefix(inst.InstanceType, "ecs.gn") || strings.HasPrefix(inst.InstanceType, "ecs.cu") {
		architecture = "arm64"
	}

	var capacityType string
	if inst.InstanceChargeType == "PostPaid" {
		capacityType = "on-demand"
	} else if inst.InstanceChargeType == "PrePaid" {
		capacityType = "pre-paid"
	} else if inst.SpotStrategy != "" && inst.SpotStrategy != "NoSpot" {
		capacityType = "spot"
	}

	instance := &Instance{
		InstanceID:       inst.InstanceId,
		Region:           inst.RegionId,
		Zone:             inst.ZoneId,
		InstanceType:     inst.InstanceType,
		ImageID:          inst.ImageId,
		CPU:              cpu,
		Memory:           memory,
		Storage:          storage,
		Architecture:     architecture,
		Status:           inst.Status,
		CreationTime:     inst.CreationTime,
		Tags:             convertTags(inst.Tags.Tag),
		CapacityType:     capacityType,
		SecurityGroupIDs: inst.SecurityGroupIds.SecurityGroupId,
		VSwitchID:        inst.VpcAttributes.VSwitchId,
	}

	// Cache the instance
	p.setCachedInstance(instanceID, instance)

	return instance, nil
}

// describeInstances 是实际执行DescribeInstances API调用的方法
func (p *Provider) describeInstances(ctx context.Context, instanceIDs []string) (*ecs.DescribeInstancesResponse, error) {
	logger := log.FromContext(ctx)

	// Create describe instance request
	request := ecs.CreateDescribeInstancesRequest()
	request.RegionId = p.region

	// Set instance IDs - 使用阿里云SDK推荐的方式
	if len(instanceIDs) > 0 {
		// 使用SDK的InstanceIds参数，直接传入字符串数组
		// 阿里云SDK会自动处理参数格式
		request.InstanceIds = fmt.Sprintf("[\"%s\"]", strings.Join(instanceIDs, "\",\""))
	}

	// Log the request for debugging (only at debug level)
	logger.V(1).Info("DescribeInstances request", "regionId", request.RegionId, "instanceIds", request.InstanceIds)

	// Implement retry mechanism for throttling errors
	var response *ecs.DescribeInstancesResponse
	var err error

	// Get retry strategy for throttling
	strategy := errors.GetRetryStrategy(fmt.Errorf("throttling"))
	if strategy == nil {
		// Default strategy if not found
		strategy = &errors.RetryStrategy{
			MaxAttempts:       5,
			InitialBackoff:    1000,  // 1s
			MaxBackoff:        16000, // 16s
			BackoffMultiplier: 2.0,
		}
	}

	for attempt := 0; attempt < strategy.MaxAttempts; attempt++ {
		// Execute request
		response, err = p.ecsClient.DescribeInstances(ctx, request)
		if err == nil {
			break // Success
		}

		// Check if it's a throttling error
		if errors.IsThrottlingError(err) {
			logger.Info("Throttling error encountered in Get, will retry", "attempt", attempt+1, "error", err.Error())

			// Calculate backoff with jitter
			backoff := time.Duration(strategy.InitialBackoff) * time.Millisecond
			if attempt > 0 {
				backoff = time.Duration(float64(backoff) * math.Pow(strategy.BackoffMultiplier, float64(attempt)))
			}

			// Cap at max backoff
			if backoff > time.Duration(strategy.MaxBackoff)*time.Millisecond {
				backoff = time.Duration(strategy.MaxBackoff) * time.Millisecond
			}

			// Add jitter (±50%)
			jitter := time.Duration(rand.Int63n(int64(backoff))) - backoff/2
			backoff += jitter

			// Ensure backoff is positive
			if backoff < 0 {
				backoff = time.Duration(strategy.InitialBackoff) * time.Millisecond
			}

			logger.Info("Waiting before retry in Get", "backoff", backoff.String(), "attempt", attempt+1)
			time.Sleep(backoff)
			continue
		}

		// Check if it's an instance not found error
		// For Alibaba Cloud, if instance is already deleted, the API might return a specific error
		if strings.Contains(err.Error(), "InvalidInstanceId.NotFound") ||
			strings.Contains(err.Error(), "InstanceNotFound") ||
			strings.Contains(err.Error(), "not found") {
			logger.V(1).Info("instance not found during describe operation", "instanceIDs", instanceIDs, "error", err.Error())
			return nil, NewNotFoundError(fmt.Errorf("instances %v not found: %w", instanceIDs, err))
		}

		// For non-throttling errors, don't retry
		logger.Error(err, "failed to describe instance", "instanceIDs", instanceIDs, "region", p.region)
		return nil, fmt.Errorf("failed to describe instances %v: %w", instanceIDs, err)
	}

	if err != nil {
		logger.Error(err, "failed to describe instances after retries", "instanceIDs", instanceIDs, "region", p.region)
		return nil, fmt.Errorf("failed to describe instances %v after %d attempts: %w", instanceIDs, strategy.MaxAttempts, err)
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
	request := ecs.CreateDeleteInstancesRequest()
	request.RegionId = p.region

	// Set instance IDs correctly for DeleteInstances
	request.InstanceId = &[]string{instanceID}
	request.Force = requests.NewBoolean(true)                 // Force deletion
	request.TerminateSubscription = requests.NewBoolean(true) // Terminate associated subscription resources

	// Log the delete request for debugging
	logger.Info("DeleteInstances request prepared", "regionId", request.RegionId, "instanceIds", *request.InstanceId, "force", request.Force, "terminateSubscription", request.TerminateSubscription)

	// Implement retry mechanism for throttling errors
	var deleteErr error

	// Get retry strategy for throttling
	strategy := errors.GetRetryStrategy(fmt.Errorf("throttling"))
	if strategy == nil {
		// Default strategy if not found
		strategy = &errors.RetryStrategy{
			MaxAttempts:       5,
			InitialBackoff:    1000,  // 1s
			MaxBackoff:        16000, // 16s
			BackoffMultiplier: 2.0,
		}
	}

	for attempt := 0; attempt < strategy.MaxAttempts; attempt++ {
		logger.Info("Attempting to delete instance", "attempt", attempt+1, "instanceID", instanceID)

		// Execute request
		response, err := p.ecsClient.DeleteInstances(ctx, request)
		if err == nil {
			// Check if the response indicates success
			if response != nil && response.BaseResponse != nil && !response.BaseResponse.IsSuccess() {
				// Response indicates failure via HTTP status code
				deleteErr = fmt.Errorf("delete instance API call succeeded but response indicates failure, requestID: %s, HTTPCode: %d",
					response.RequestId, response.BaseResponse.GetHttpStatus())
				logger.Error(deleteErr, "Delete instance API response indicates failure", "instanceID", instanceID)
			} else {
				// Success - either response indicates success or we don't have enough info to determine failure
				logger.Info("Successfully deleted instance", "instanceID", instanceID, "requestId", response.RequestId)
				p.deleteCachedInstance(instanceID)
				return nil
			}
		} else {
			deleteErr = err
			logger.Info("DeleteInstances API call failed", "attempt", attempt+1, "instanceID", instanceID, "error", err.Error())
		}

		// Check if it's a throttling error, if yes retry
		if errors.IsThrottlingError(deleteErr) {
			logger.Info("Throttling error encountered in Delete, will retry", "attempt", attempt+1, "error", deleteErr.Error())

			// Calculate backoff with jitter
			backoff := time.Duration(strategy.InitialBackoff) * time.Millisecond
			if attempt > 0 {
				backoff = time.Duration(float64(backoff) * math.Pow(strategy.BackoffMultiplier, float64(attempt)))
			}

			// Cap at max backoff
			if backoff > time.Duration(strategy.MaxBackoff)*time.Millisecond {
				backoff = time.Duration(strategy.MaxBackoff) * time.Millisecond
			}

			// Add jitter (±50%)
			jitter := time.Duration(rand.Int63n(int64(backoff))) - backoff/2
			backoff += jitter

			// Ensure backoff is positive
			if backoff < 0 {
				backoff = time.Duration(strategy.InitialBackoff) * time.Millisecond
			}

			logger.Info("Waiting before retry in Delete", "backoff", backoff.String(), "attempt", attempt+1)
			time.Sleep(backoff)
			continue
		}

		// Check if it's an instance not found error
		// For Alibaba Cloud, if instance is already deleted, the API might return a specific error
		if errors.IsNotFound(deleteErr) ||
			strings.Contains(deleteErr.Error(), "InvalidInstanceId.NotFound") ||
			strings.Contains(deleteErr.Error(), "InstanceNotFound") ||
			strings.Contains(deleteErr.Error(), "not found") {
			logger.Info("instance already deleted or not found, returning NodeClaimNotFoundError", "instanceID", instanceID, "error", deleteErr.Error())
			return cloudprovider.NewNodeClaimNotFoundError(fmt.Errorf("instance delete with err %w", deleteErr))
		}

		// if error is not throttling or not found, return the error
		return fmt.Errorf("delete instance failed with err %w", deleteErr)
	}

	// After all retries failed, return the last error
	if deleteErr != nil {
		return fmt.Errorf("delete instance failed with err %w after %d retries", deleteErr, strategy.MaxAttempts)
	}

	return nil
}

// List lists all ECS instances managed by Karpenter
func (p *Provider) List(ctx context.Context, tags map[string]string) ([]*Instance, error) {
	logger := log.FromContext(ctx)

	// Create describe instances request with Karpenter tag filter
	request := ecs.CreateDescribeInstancesRequest()
	request.RegionId = p.region

	// Set page size for better performance
	request.PageSize = requests.NewInteger(100)

	// Log the tags we're filtering by
	logger.Info("Listing instances with tags", "tags", tags)

	// Set tag filters
	var ecsTags []ecs.DescribeInstancesTag
	for key, value := range tags {
		ecsTags = append(ecsTags, ecs.DescribeInstancesTag{
			Key:   key,
			Value: value,
		})
	}
	request.Tag = &ecsTags

	// Implement pagination
	var allInstances []*Instance
	pageNumber := 1

	for {
		request.PageNumber = requests.NewInteger(pageNumber)

		// Implement retry mechanism for throttling errors
		var response *ecs.DescribeInstancesResponse
		var err error

		// Get retry strategy for throttling
		strategy := errors.GetRetryStrategy(fmt.Errorf("throttling"))
		if strategy == nil {
			// Default strategy if not found
			strategy = &errors.RetryStrategy{
				MaxAttempts:       5,
				InitialBackoff:    1000,  // 1s
				MaxBackoff:        16000, // 16s
				BackoffMultiplier: 2.0,
			}
		}

		for attempt := 0; attempt < strategy.MaxAttempts; attempt++ {
			// Execute request
			response, err = p.ecsClient.DescribeInstances(ctx, request)
			if err == nil {
				break // Success
			}

			// Check if it's a throttling error
			if errors.IsThrottlingError(err) {
				logger.Info("Throttling error encountered, will retry", "attempt", attempt+1, "error", err.Error())

				// Calculate backoff with jitter
				backoff := time.Duration(strategy.InitialBackoff) * time.Millisecond
				if attempt > 0 {
					backoff = time.Duration(float64(backoff) * math.Pow(strategy.BackoffMultiplier, float64(attempt)))
				}

				// Cap at max backoff
				if backoff > time.Duration(strategy.MaxBackoff)*time.Millisecond {
					backoff = time.Duration(strategy.MaxBackoff) * time.Millisecond
				}

				// Add jitter (±50%)
				jitter := time.Duration(rand.Int63n(int64(backoff))) - backoff/2
				backoff += jitter

				// Ensure backoff is positive
				if backoff < 0 {
					backoff = time.Duration(strategy.InitialBackoff) * time.Millisecond
				}

				logger.Info("Waiting before retry", "backoff", backoff.String(), "attempt", attempt+1)
				time.Sleep(backoff)
				continue
			}

			// For non-throttling errors, don't retry
			logger.Error(err, "failed to list instances")
			return nil, fmt.Errorf("failed to list instances: %w", err)
		}

		if err != nil {
			logger.Error(err, "failed to list instances after retries")
			return nil, fmt.Errorf("failed to list instances after %d attempts: %w", strategy.MaxAttempts, err)
		}

		// Log response information
		logger.Info("DescribeInstances response", "totalCount", response.TotalCount, "returnedCount", len(response.Instances.Instance), "pageNumber", pageNumber)

		// Convert to our Instance type
		for _, inst := range response.Instances.Instance {
			cpu := resource.MustParse(fmt.Sprintf("%d", inst.Cpu))
			memory := resource.MustParse(fmt.Sprintf("%dGi", inst.Memory/1024)) // ECS returns memory in MiB
			storage := resource.MustParse("0")                                  // Storage is not directly available in DescribeInstances response

			// Get architecture from instance type
			architecture := "amd64"
			if strings.HasPrefix(inst.InstanceType, "ecs.gn") || strings.HasPrefix(inst.InstanceType, "ecs.cu") {
				architecture = "arm64"
			}

			// Determine capacity type
			var capacityType string
			if inst.InstanceChargeType == "PostPaid" {
				capacityType = "on-demand"
			} else if inst.InstanceChargeType == "PrePaid" {
				capacityType = "pre-paid"
			} else if inst.SpotStrategy != "" && inst.SpotStrategy != "NoSpot" {
				capacityType = "spot"
			}

			instance := &Instance{
				InstanceID:       inst.InstanceId,
				Region:           inst.RegionId,
				Zone:             inst.ZoneId,
				InstanceType:     inst.InstanceType,
				ImageID:          inst.ImageId,
				CPU:              cpu,
				Memory:           memory,
				Storage:          storage,
				Architecture:     architecture,
				Status:           inst.Status,
				CreationTime:     inst.CreationTime,
				Tags:             convertTags(inst.Tags.Tag),
				CapacityType:     capacityType,
				SecurityGroupIDs: inst.SecurityGroupIds.SecurityGroupId,
				VSwitchID:        inst.VpcAttributes.VSwitchId,
			}

			// Log each instance found
			logger.Info("Found instance", "instanceID", instance.InstanceID, "status", instance.Status, "tags", instance.Tags)

			allInstances = append(allInstances, instance)

			// Cache the instance
			p.setCachedInstance(inst.InstanceId, instance)
		}

		// Check if there are more pages
		if pageNumber*100 >= response.TotalCount {
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
	request := ecs.CreateTagResourcesRequest()
	request.RegionId = p.region
	request.ResourceType = "instance"

	// Set resource IDs
	resourceIds := []string{instanceID}
	request.ResourceId = &resourceIds

	// Convert tags to ECS format
	var ecsTags []ecs.TagResourcesTag
	for key, value := range tags {
		ecsTags = append(ecsTags, ecs.TagResourcesTag{
			Key:   key,
			Value: value,
		})
	}
	request.Tag = &ecsTags

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
func convertTags(ecsTags []ecs.Tag) map[string]string {
	tags := make(map[string]string)
	for _, tag := range ecsTags {
		tags[tag.TagKey] = tag.TagValue
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
