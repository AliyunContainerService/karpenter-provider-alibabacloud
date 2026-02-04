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

	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
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
	DescribeImages(ctx context.Context, imageIDs []string, filters map[string]string) ([]ecs.DescribeImagesResponseBodyImagesImage, error)

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
	request := &ecs.DescribeInstanceTypesRequest{}

	if len(instanceTypes) > 0 {
		// Convert []string to []*string
		var instanceTypePointers []*string
		for _, it := range instanceTypes {
			instanceTypePointers = append(instanceTypePointers, tea.String(it))
		}
		request.InstanceTypes = instanceTypePointers
	}

	response, err := c.client.DescribeInstanceTypes(request)
	if err != nil {
		return nil, err
	}

	return response, nil
}

// DescribeZones implements ECSClient interface
func (c *DefaultECSClient) DescribeZones(ctx context.Context) (*ecs.DescribeZonesResponse, error) {
	request := &ecs.DescribeZonesRequest{
		RegionId: tea.String(c.region),
	}

	response, err := c.client.DescribeZones(request)
	if err != nil {
		return nil, err
	}

	return response, nil
}

// DescribeImages implements ECSClient interface
func (c *DefaultECSClient) DescribeImages(ctx context.Context, imageIDs []string, filters map[string]string) ([]ecs.DescribeImagesResponseBodyImagesImage, error) {
	request := &ecs.DescribeImagesRequest{
		RegionId: tea.String(c.region),
	}

	// Set image IDs if provided
	if len(imageIDs) > 0 {
		request.ImageId = tea.String(strings.Join(imageIDs, ","))
	}

	// Apply filters
	if imageFamily, ok := filters["ImageFamily"]; ok {
		request.ImageFamily = tea.String(imageFamily)
	}

	if imageName, ok := filters["ImageName"]; ok {
		request.ImageName = tea.String(imageName)
	}

	// Set page size for better performance
	const maxPageSize = 100
	request.PageSize = tea.Int32(maxPageSize)

	var allImages []ecs.DescribeImagesResponseBodyImagesImage
	pageNumber := 1

	for {
		request.PageNumber = tea.Int32(int32(pageNumber))

		response, err := c.client.DescribeImages(request)
		if err != nil {
			return nil, fmt.Errorf("failed to describe images (page %d): %w", pageNumber, err)
		}

		// Check if response body and images are valid
		if response == nil || response.Body == nil || response.Body.Images == nil || response.Body.Images.Image == nil {
			break
		}

		// Convert response images to our format
		for _, img := range response.Body.Images.Image {
			// Skip nil images
			if img == nil {
				continue
			}

			// Only include available images
			if img.Status != nil && *img.Status != "Available" {
				continue
			}

			allImages = append(allImages, *img)
		}

		// Check if there are more pages
		totalCount := 0
		if response.Body.TotalCount != nil {
			totalCount = int(*response.Body.TotalCount)
		}

		// Prevent overflow and check bounds
		if totalCount <= 0 {
			break
		}
		if pageNumber >= (totalCount+maxPageSize-1)/maxPageSize {
			break
		}
		pageNumber++
	}

	return allImages, nil
}

// DescribeSecurityGroups implements ECSClient interface
func (c *DefaultECSClient) DescribeSecurityGroups(ctx context.Context, tags map[string]string) (*ecs.DescribeSecurityGroupsResponse, error) {
	request := &ecs.DescribeSecurityGroupsRequest{
		RegionId: tea.String(c.region),
	}

	// Set tag filters
	if len(tags) > 0 {
		var ecsTags []*ecs.DescribeSecurityGroupsRequestTag
		for k, v := range tags {
			// Skip empty keys or values
			if k == "" || v == "" {
				continue
			}
			// Create local copies to avoid pointer reuse
			key := k
			value := v
			ecsTags = append(ecsTags, &ecs.DescribeSecurityGroupsRequestTag{
				Key:   tea.String(key),
				Value: tea.String(value),
			})
		}
		request.Tag = ecsTags
	}

	response, err := c.client.DescribeSecurityGroups(request)
	if err != nil {
		return nil, err
	}

	return response, nil
}

// DescribeCapacityReservations implements ECSClient interface
func (c *DefaultECSClient) DescribeCapacityReservations(ctx context.Context, id string, tags map[string]string) (*ecs.DescribeCapacityReservationsResponse, error) {
	request := &ecs.DescribeCapacityReservationsRequest{
		RegionId: tea.String(c.region),
	}

	// Set capacity reservation ID if provided
	if id != "" {
		request.PrivatePoolOptions = &ecs.DescribeCapacityReservationsRequestPrivatePoolOptions{
			Ids: tea.String("[\"" + id + "\"]"),
		}
	}

	// Note: The new SDK uses a different structure for tags
	// Tags are not directly supported in DescribeCapacityReservations request
	// If needed, we would need to filter results after fetching

	response, err := c.client.DescribeCapacityReservations(request)
	if err != nil {
		return nil, err
	}

	return response, nil
}

// DescribePrice implements ECSClient interface
func (c *DefaultECSClient) DescribePrice(ctx context.Context, instanceType string) (*ecs.DescribePriceResponse, error) {
	request := &ecs.DescribePriceRequest{
		RegionId:     tea.String(c.region),
		ResourceType: tea.String("instance"),
		InstanceType: tea.String(instanceType),
	}

	response, err := c.client.DescribePrice(request)
	if err != nil {
		return nil, err
	}

	return response, nil
}
