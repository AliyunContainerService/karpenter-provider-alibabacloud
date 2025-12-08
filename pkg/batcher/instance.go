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

package batcher

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/samber/lo"
)

// RunInstancesRequest represents a request to create an instance
type RunInstancesRequest struct {
	// ECS RunInstances request
	Request *ecs.RunInstancesRequest

	// Additional metadata for batching
	BatchKey string
}

// RunInstancesResult represents the result of creating an instance
type RunInstancesResult struct {
	InstanceID string
	LaunchTime time.Time
}

// ECSClientInterface defines the minimal interface needed for ECS operations
type ECSClientInterface interface {
	RunInstances(req *ecs.RunInstancesRequest) (*ecs.RunInstancesResponse, error)
}

// RunInstancesExecutor executes batch instance creation
type RunInstancesExecutor struct {
	ecsClient ECSClientInterface
	region    string
}

// NewRunInstancesExecutor creates a new RunInstances batch executor
func NewRunInstancesExecutor(ecsClient ECSClientInterface, region string) *RunInstancesExecutor {
	return &RunInstancesExecutor{
		ecsClient: ecsClient,
		region:    region,
	}
}

// Execute executes a batch of RunInstances requests
// This implements the BatchExecutor interface
func (e *RunInstancesExecutor) Execute(ctx context.Context, items []*BatchItem) error {
	if len(items) == 0 {
		return nil
	}

	// All items in the same batch should have compatible configurations
	// We use the first request as the template and set Amount to len(items)
	firstRequest := items[0].Request.(*RunInstancesRequest)

	// Build batch request
	req := e.cloneRequest(firstRequest.Request)
	req.Amount = requests.NewInteger(len(items))

	// Execute batch creation
	startTime := time.Now()
	resp, err := e.ecsClient.RunInstances(req)

	if err != nil {
		// Batch failed, notify all waiters
		for _, item := range items {
			item.ResultCh <- &BatchResult{
				Error: fmt.Errorf("batch RunInstances failed: %w", err),
			}
		}
		return err
	}

	// Check if we got enough instances
	instanceIDs := resp.InstanceIdSets.InstanceIdSet
	if len(instanceIDs) < len(items) {
		err := fmt.Errorf("insufficient instances returned: requested %d, got %d", len(items), len(instanceIDs))

		// Distribute what we got, then error for the rest
		for i, item := range items {
			if i < len(instanceIDs) {
				item.ResultCh <- &BatchResult{
					Value: &RunInstancesResult{
						InstanceID: instanceIDs[i],
						LaunchTime: startTime,
					},
				}
			} else {
				item.ResultCh <- &BatchResult{
					Error: err,
				}
			}
		}
		return err
	}

	// Success! Distribute results to all waiters
	for i, item := range items {
		item.ResultCh <- &BatchResult{
			Value: &RunInstancesResult{
				InstanceID: instanceIDs[i],
				LaunchTime: startTime,
			},
		}
	}

	return nil
}

// cloneRequest creates a copy of the RunInstances request
func (e *RunInstancesExecutor) cloneRequest(req *ecs.RunInstancesRequest) *ecs.RunInstancesRequest {
	clone := ecs.CreateRunInstancesRequest()

	// Copy all fields from original request
	clone.RegionId = req.RegionId
	clone.InstanceType = req.InstanceType
	clone.ImageId = req.ImageId
	clone.VSwitchId = req.VSwitchId
	clone.SecurityGroupId = req.SecurityGroupId
	clone.SecurityGroupIds = req.SecurityGroupIds
	clone.InstanceName = req.InstanceName
	clone.InternetMaxBandwidthOut = req.InternetMaxBandwidthOut
	clone.UserData = req.UserData
	clone.ClientToken = req.ClientToken
	clone.SpotStrategy = req.SpotStrategy
	clone.SpotPriceLimit = req.SpotPriceLimit
	clone.RamRoleName = req.RamRoleName
	clone.Tag = req.Tag
	clone.SystemDiskCategory = req.SystemDiskCategory
	clone.SystemDiskSize = req.SystemDiskSize
	clone.SystemDiskPerformanceLevel = req.SystemDiskPerformanceLevel
	clone.DataDisk = req.DataDisk

	// Note: Amount will be set by the caller

	return clone
}

// ComputeRunInstancesBatchKey computes a batch key for RunInstances request
// Requests with the same key can be batched together
func ComputeRunInstancesBatchKey(req *ecs.RunInstancesRequest) string {
	// Create a deterministic key from request parameters that affect batching
	// Only requests with identical configurations can be batched

	h := sha256.New()

	// Core instance configuration
	h.Write([]byte(req.RegionId))
	h.Write([]byte(req.InstanceType))
	h.Write([]byte(req.ImageId))
	h.Write([]byte(req.VSwitchId))
	h.Write([]byte(req.SecurityGroupId))

	// Security groups (if multiple)
	if req.SecurityGroupIds != nil {
		for _, sg := range *req.SecurityGroupIds {
			h.Write([]byte(sg))
		}
	}

	// Spot configuration
	h.Write([]byte(req.SpotStrategy))
	h.Write([]byte(req.SpotPriceLimit))

	// RAM role
	h.Write([]byte(req.RamRoleName))

	// System disk
	h.Write([]byte(req.SystemDiskCategory))
	h.Write([]byte(req.SystemDiskSize))
	h.Write([]byte(req.SystemDiskPerformanceLevel))

	// Data disks (hash the configuration)
	if req.DataDisk != nil {
		for _, disk := range *req.DataDisk {
			h.Write([]byte(disk.Category))
			h.Write([]byte(disk.Size))
			h.Write([]byte(disk.PerformanceLevel))
		}
	}

	// UserData
	h.Write([]byte(req.UserData))

	// Tags (sorted for consistency)
	if req.Tag != nil {
		tags := *req.Tag
		sortedTags := lo.Map(tags, func(t ecs.RunInstancesTag, _ int) string {
			return t.Key + "=" + t.Value
		})
		for _, tag := range sortedTags {
			h.Write([]byte(tag))
		}
	}

	return fmt.Sprintf("run-instances-%x", h.Sum(nil))
}

// InstanceBatcher provides batch instance creation with windowed batching
type InstanceBatcher struct {
	windowedBatcher *WindowedBatcher
	ecsClient       ECSClientInterface
	region          string
}

// NewInstanceBatcher creates a new instance batcher
// Uses 1ms idle / 10ms max windows aligned with optimized AWS Provider settings for faster response
func NewInstanceBatcher(ecsClient ECSClientInterface, region string) *InstanceBatcher {
	executorFactory := func(key string) BatchExecutor {
		return NewRunInstancesExecutor(ecsClient, region)
	}

	return &InstanceBatcher{
		windowedBatcher: NewWindowedBatcher(
			1*time.Millisecond,  // idle duration - 缩短空闲时间以提高响应速度
			10*time.Millisecond, // max duration - 缩短最大等待时间以加快批处理
			executorFactory,
		),
		ecsClient: ecsClient,
		region:    region,
	}
}

// CreateInstance creates an instance with batching
// Multiple concurrent calls with the same configuration will be batched together
func (b *InstanceBatcher) CreateInstance(
	ctx context.Context,
	req *ecs.RunInstancesRequest,
) (*RunInstancesResult, error) {
	// Compute batch key
	batchKey := ComputeRunInstancesBatchKey(req)

	// Create result channel
	resultCh := make(chan *BatchResult, 1)

	// Add to batch window
	request := &RunInstancesRequest{
		Request:  req,
		BatchKey: batchKey,
	}

	err := b.windowedBatcher.Batch(batchKey, request, resultCh)
	if err != nil {
		return nil, fmt.Errorf("failed to add to batch: %w", err)
	}

	// Wait for result
	select {
	case result := <-resultCh:
		if result.Error != nil {
			return nil, result.Error
		}
		return result.Value.(*RunInstancesResult), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close closes the batcher and releases resources
func (b *InstanceBatcher) Close() error {
	if b.windowedBatcher != nil {
		// Close the windowed batcher which will flush any pending items
		b.windowedBatcher.Close()
	}
	return nil
}

// Stats returns batcher statistics
func (b *InstanceBatcher) Stats() map[string]interface{} {
	if b.windowedBatcher != nil {
		return b.windowedBatcher.Stats()
	}
	return map[string]interface{}{}
}
