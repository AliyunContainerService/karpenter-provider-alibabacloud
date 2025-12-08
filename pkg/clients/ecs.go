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

package clients

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
)

// ECSClient is a unified interface for all ECS client operations across different providers
type ECSClient interface {
	// Instance operations
	RunInstances(ctx context.Context, request *ecs.RunInstancesRequest) (*ecs.RunInstancesResponse, error)
	DescribeInstances(ctx context.Context, request *ecs.DescribeInstancesRequest) (*ecs.DescribeInstancesResponse, error)
	DeleteInstances(ctx context.Context, request *ecs.DeleteInstancesRequest) (*ecs.DeleteInstancesResponse, error)
	TagResources(ctx context.Context, request *ecs.TagResourcesRequest) (*ecs.TagResourcesResponse, error)

	// Launch template operations
	CreateLaunchTemplate(ctx context.Context, request *ecs.CreateLaunchTemplateRequest) (*ecs.CreateLaunchTemplateResponse, error)
	DescribeLaunchTemplates(ctx context.Context, request *ecs.DescribeLaunchTemplatesRequest) (*ecs.DescribeLaunchTemplatesResponse, error)
	DeleteLaunchTemplate(ctx context.Context, request *ecs.DeleteLaunchTemplateRequest) (*ecs.DeleteLaunchTemplateResponse, error)

	// Instance type operations
	DescribeInstanceTypes(ctx context.Context, instanceTypes []string) (*ecs.DescribeInstanceTypesResponse, error)
	DescribeZones(ctx context.Context) (*ecs.DescribeZonesResponse, error)

	// Image operations
	DescribeImages(ctx context.Context, imageIDs []string, filters map[string]string) ([]ecs.Image, error)

	// Security group operations
	DescribeSecurityGroups(ctx context.Context, tags map[string]string) (*ecs.DescribeSecurityGroupsResponse, error)

	// Capacity reservation operations
	DescribeCapacityReservations(ctx context.Context, id string, tags map[string]string) (*ecs.DescribeCapacityReservationsResponse, error)

	// Pricing operations
	DescribePrice(ctx context.Context, instanceType string) (*ecs.DescribePriceResponse, error)
}

// Image represents an ECS image
type Image struct {
	ID           string
	Name         string
	OSType       string
	Architecture string
	CreationTime time.Time
}

// DefaultECSClient implements ECSClient using Alibaba Cloud SDK
type DefaultECSClient struct {
	client *ecs.Client
	region string
}

// NewDefaultECSClient creates a new default ECS client
func NewDefaultECSClient(client *ecs.Client, region string) ECSClient {
	return &DefaultECSClient{
		client: client,
		region: region,
	}
}

// RunInstances implements ECSClient interface
func (c *DefaultECSClient) RunInstances(ctx context.Context, request *ecs.RunInstancesRequest) (*ecs.RunInstancesResponse, error) {
	return c.client.RunInstances(request)
}

// DescribeInstances implements ECSClient interface
func (c *DefaultECSClient) DescribeInstances(ctx context.Context, request *ecs.DescribeInstancesRequest) (*ecs.DescribeInstancesResponse, error) {
	return c.client.DescribeInstances(request)
}

// DeleteInstances implements ECSClient interface
func (c *DefaultECSClient) DeleteInstances(ctx context.Context, request *ecs.DeleteInstancesRequest) (*ecs.DeleteInstancesResponse, error) {
	return c.client.DeleteInstances(request)
}

// TagResources implements ECSClient interface
func (c *DefaultECSClient) TagResources(ctx context.Context, request *ecs.TagResourcesRequest) (*ecs.TagResourcesResponse, error) {
	return c.client.TagResources(request)
}

// CreateLaunchTemplate implements ECSClient interface
func (c *DefaultECSClient) CreateLaunchTemplate(ctx context.Context, request *ecs.CreateLaunchTemplateRequest) (*ecs.CreateLaunchTemplateResponse, error) {
	return c.client.CreateLaunchTemplate(request)
}

// DescribeLaunchTemplates implements ECSClient interface
func (c *DefaultECSClient) DescribeLaunchTemplates(ctx context.Context, request *ecs.DescribeLaunchTemplatesRequest) (*ecs.DescribeLaunchTemplatesResponse, error) {
	return c.client.DescribeLaunchTemplates(request)
}

// DeleteLaunchTemplate implements ECSClient interface
func (c *DefaultECSClient) DeleteLaunchTemplate(ctx context.Context, request *ecs.DeleteLaunchTemplateRequest) (*ecs.DeleteLaunchTemplateResponse, error) {
	return c.client.DeleteLaunchTemplate(request)
}

// DescribeInstanceTypes implements ECSClient interface
func (c *DefaultECSClient) DescribeInstanceTypes(ctx context.Context, instanceTypes []string) (*ecs.DescribeInstanceTypesResponse, error) {
	request := ecs.CreateDescribeInstanceTypesRequest()
	request.Scheme = "https"
	request.RegionId = c.region

	if len(instanceTypes) > 0 {
		request.InstanceTypes = &instanceTypes
	}

	response, err := c.client.DescribeInstanceTypes(request)
	if err != nil {
		return nil, err
	}

	return response, nil
}

// DescribeZones implements ECSClient interface
func (c *DefaultECSClient) DescribeZones(ctx context.Context) (*ecs.DescribeZonesResponse, error) {
	request := ecs.CreateDescribeZonesRequest()
	request.Scheme = "https"
	request.RegionId = c.region

	response, err := c.client.DescribeZones(request)
	if err != nil {
		return nil, err
	}

	return response, nil
}

// DescribeImages implements ECSClient interface
func (c *DefaultECSClient) DescribeImages(ctx context.Context, imageIDs []string, filters map[string]string) ([]ecs.Image, error) {
	request := ecs.CreateDescribeImagesRequest()
	request.Scheme = "https"
	request.RegionId = c.region

	// Set image IDs if provided
	if len(imageIDs) > 0 {
		request.ImageId = strings.Join(imageIDs, ",")
	}

	// Apply filters
	if owner, ok := filters["ImageFamily"]; ok {
		request.ImageFamily = owner
	}

	if imageName, ok := filters["ImageName"]; ok {
		request.ImageName = imageName
	}

	// Apply tag filters
	for k, v := range filters {
		if strings.HasPrefix(k, "Tag.") {
			tagKey := strings.TrimPrefix(k, "Tag.")
			// Note: Alibaba Cloud SDK might have different ways to set tag filters
			// This is a placeholder implementation
			_ = tagKey
			_ = v
		}
	}

	// Set page size for better performance
	request.PageSize = "100"

	var allImages []ecs.Image
	pageNumber := 1

	for {
		request.PageNumber = requests.NewInteger(pageNumber)

		response, err := c.client.DescribeImages(request)
		if err != nil {
			return nil, fmt.Errorf("failed to describe images (page %d): %w", pageNumber, err)
		}

		// Convert response images to our format
		for _, img := range response.Images.Image {
			// Only include available images
			if img.Status != "Available" {
				continue
			}

			// Parse creation time
			//creationTime, err := time.Parse(time.RFC3339, img.CreationTime)
			//if err != nil {
			//	// If parsing fails, use zero time
			//	creationTime = time.Time{}
			//}
			//
			//ecsImage := Image{
			//	ID:           img.ImageId,
			//	Name:         img.ImageName,
			//	OSType:       img.OSType,
			//	Architecture: img.Architecture,
			//	CreationTime: creationTime,
			//}
			allImages = append(allImages, img)
		}

		// Check if there are more pages
		if pageNumber*100 >= response.TotalCount {
			break
		}
		pageNumber++
	}

	return allImages, nil
}

// DescribeSecurityGroups implements ECSClient interface
func (c *DefaultECSClient) DescribeSecurityGroups(ctx context.Context, tags map[string]string) (*ecs.DescribeSecurityGroupsResponse, error) {
	request := ecs.CreateDescribeSecurityGroupsRequest()
	request.Scheme = "https"
	request.RegionId = c.region

	// Set tag filters
	if len(tags) > 0 {
		var ecsTags []ecs.DescribeSecurityGroupsTag
		for key, value := range tags {
			ecsTags = append(ecsTags, ecs.DescribeSecurityGroupsTag{
				Key:   key,
				Value: value,
			})
		}
		request.Tag = &ecsTags
	}

	response, err := c.client.DescribeSecurityGroups(request)
	if err != nil {
		return nil, err
	}

	return response, nil
}

// DescribeCapacityReservations implements ECSClient interface
func (c *DefaultECSClient) DescribeCapacityReservations(ctx context.Context, id string, tags map[string]string) (*ecs.DescribeCapacityReservationsResponse, error) {
	request := ecs.CreateDescribeCapacityReservationsRequest()
	request.Scheme = "https"
	request.RegionId = c.region

	// Set capacity reservation ID if provided
	if id != "" {
		request.PrivatePoolOptionsIds = "[\"" + id + "\"]"
	}

	// Apply tag filters
	if len(tags) > 0 {
		var ecsTags []ecs.DescribeCapacityReservationsTag
		for k, v := range tags {
			ecsTags = append(ecsTags, ecs.DescribeCapacityReservationsTag{
				Key:   k,
				Value: v,
			})
		}
		request.Tag = &ecsTags
	}

	response, err := c.client.DescribeCapacityReservations(request)
	if err != nil {
		return nil, err
	}

	return response, nil
}

// DescribePrice implements ECSClient interface
func (c *DefaultECSClient) DescribePrice(ctx context.Context, instanceType string) (*ecs.DescribePriceResponse, error) {
	request := ecs.CreateDescribePriceRequest()
	request.Scheme = "https"
	request.RegionId = c.region
	request.ResourceType = "instance"
	request.InstanceType = instanceType

	response, err := c.client.DescribePrice(request)
	if err != nil {
		return nil, err
	}

	return response, nil
}
