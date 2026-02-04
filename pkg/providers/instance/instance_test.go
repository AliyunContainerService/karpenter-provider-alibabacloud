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
	"errors"
	"fmt"
	"testing"
	"time"

	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"k8s.io/apimachinery/pkg/api/resource"
)

// MockECSClient is a mock implementation of ECSClient
type MockECSClient struct {
	mock.Mock
}

func (m *MockECSClient) CreateLaunchTemplate(ctx context.Context, request *ecs.CreateLaunchTemplateRequest) (*ecs.CreateLaunchTemplateResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (m *MockECSClient) DescribeLaunchTemplates(ctx context.Context, request *ecs.DescribeLaunchTemplatesRequest) (*ecs.DescribeLaunchTemplatesResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (m *MockECSClient) DeleteLaunchTemplate(ctx context.Context, request *ecs.DeleteLaunchTemplateRequest) (*ecs.DeleteLaunchTemplateResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (m *MockECSClient) DescribeInstanceTypes(ctx context.Context, instanceTypes []string) (*ecs.DescribeInstanceTypesResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (m *MockECSClient) DescribeZones(ctx context.Context) (*ecs.DescribeZonesResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (m *MockECSClient) DescribeImages(ctx context.Context, imageIDs []string, filters map[string]string) ([]ecs.DescribeImagesResponseBodyImagesImage, error) {
	//TODO implement me
	panic("implement me")
}

func (m *MockECSClient) DescribeSecurityGroups(ctx context.Context, tags map[string]string) (*ecs.DescribeSecurityGroupsResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (m *MockECSClient) DescribeCapacityReservations(ctx context.Context, id string, tags map[string]string) (*ecs.DescribeCapacityReservationsResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (m *MockECSClient) DescribePrice(ctx context.Context, instanceType string) (*ecs.DescribePriceResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (m *MockECSClient) RunInstances(ctx context.Context, request *ecs.RunInstancesRequest) (*ecs.RunInstancesResponse, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ecs.RunInstancesResponse), args.Error(1)
}

func (m *MockECSClient) DescribeInstances(ctx context.Context, request *ecs.DescribeInstancesRequest) (*ecs.DescribeInstancesResponse, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ecs.DescribeInstancesResponse), args.Error(1)
}

func (m *MockECSClient) DeleteInstances(ctx context.Context, request *ecs.DeleteInstancesRequest) (*ecs.DeleteInstancesResponse, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ecs.DeleteInstancesResponse), args.Error(1)
}

func (m *MockECSClient) TagResources(ctx context.Context, request *ecs.TagResourcesRequest) (*ecs.TagResourcesResponse, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ecs.TagResourcesResponse), args.Error(1)
}

func stringPtr(s string) *string {
	return &s
}

func int32Ptr(i int32) *int32 {
	return &i
}

func TestCreate(t *testing.T) {
	tests := []struct {
		name        string
		opts        CreateOptions
		mockSetup   func(*MockECSClient)
		expectError bool
	}{
		{
			name: "successful creation",
			opts: CreateOptions{
				InstanceType:     "ecs.g6.large",
				ImageID:          "img-123",
				VSwitchID:        "vsw-123",
				SecurityGroupIDs: []string{"sg-123"},
				SystemDisk: SystemDisk{
					Category: "cloud_essd",
					Size:     40,
				},
				Tags: map[string]string{
					"env": "test",
				},
			},
			mockSetup: func(m *MockECSClient) {
				instanceID := "i-123456"
				response := &ecs.RunInstancesResponse{
					Body: &ecs.RunInstancesResponseBody{
						InstanceIdSets: &ecs.RunInstancesResponseBodyInstanceIdSets{
							InstanceIdSet: []*string{&instanceID},
						},
					},
				}
				m.On("RunInstances", mock.Anything, mock.Anything).Return(response, nil)
			},
		},
		{
			name: "API error",
			opts: CreateOptions{
				InstanceType: "ecs.g6.large",
				ImageID:      "img-123",
			},
			mockSetup: func(m *MockECSClient) {
				m.On("RunInstances", mock.Anything, mock.Anything).Return(nil, errors.New("API error"))
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockECSClient)
			tt.mockSetup(mockClient)

			provider := NewProvider(context.Background(), "cn-hangzhou", mockClient)
			result, err := provider.Create(context.Background(), tt.opts)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotEmpty(t, result)
			}

			mockClient.AssertExpectations(t)
		})
	}
}

func TestList(t *testing.T) {
	tests := []struct {
		name        string
		tags        map[string]string
		mockSetup   func(*MockECSClient)
		expectedLen int
		expectError bool
	}{
		{
			name: "successful list",
			tags: map[string]string{"env": "test"},
			mockSetup: func(m *MockECSClient) {
				totalCount := int32(1)
				response := &ecs.DescribeInstancesResponse{
					Body: &ecs.DescribeInstancesResponseBody{
						TotalCount: &totalCount,
						Instances: &ecs.DescribeInstancesResponseBodyInstances{
							Instance: []*ecs.DescribeInstancesResponseBodyInstancesInstance{
								{
									InstanceId:         stringPtr("i-123"),
									RegionId:           stringPtr("cn-hangzhou"),
									ZoneId:             stringPtr("cn-hangzhou-h"),
									InstanceType:       stringPtr("ecs.g6.large"),
									ImageId:            stringPtr("img-123"),
									Cpu:                int32Ptr(2),
									Memory:             int32Ptr(8192),
									Status:             stringPtr("Running"),
									InstanceChargeType: stringPtr("PostPaid"),
									CreationTime:       stringPtr("2024-01-01T00:00:00Z"),
									Tags: &ecs.DescribeInstancesResponseBodyInstancesInstanceTags{
										Tag: []*ecs.DescribeInstancesResponseBodyInstancesInstanceTagsTag{},
									},
								},
							},
						},
					},
				}
				m.On("DescribeInstances", mock.Anything, mock.Anything).Return(response, nil)
			},
			expectedLen: 1,
		},
		{
			name: "API error",
			tags: map[string]string{"env": "test"},
			mockSetup: func(m *MockECSClient) {
				m.On("DescribeInstances", mock.Anything, mock.Anything).Return(nil, errors.New("API error"))
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockECSClient)
			tt.mockSetup(mockClient)

			provider := NewProvider(context.Background(), "cn-hangzhou", mockClient)
			result, err := provider.List(context.Background(), tt.tags)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Len(t, result, tt.expectedLen)
			}

			mockClient.AssertExpectations(t)
		})
	}
}

func TestListWithPagination(t *testing.T) {
	tests := []struct {
		name        string
		tags        map[string]string
		mockSetup   func(*MockECSClient)
		expectedLen int
		expectError bool
	}{
		{
			name: "single page result",
			tags: map[string]string{"env": "test"},
			mockSetup: func(m *MockECSClient) {
				response := &ecs.DescribeInstancesResponse{
					Body: &ecs.DescribeInstancesResponseBody{
						TotalCount: int32Ptr(1),
						PageNumber: int32Ptr(1),
						PageSize:   int32Ptr(100),
						Instances: &ecs.DescribeInstancesResponseBodyInstances{
							Instance: []*ecs.DescribeInstancesResponseBodyInstancesInstance{
								{
									InstanceId:         stringPtr("i-123"),
									RegionId:           stringPtr("cn-hangzhou"),
									ZoneId:             stringPtr("cn-hangzhou-h"),
									InstanceType:       stringPtr("ecs.g6.large"),
									ImageId:            stringPtr("img-123"),
									Cpu:                int32Ptr(2),
									Memory:             int32Ptr(8192),
									Status:             stringPtr("Running"),
									InstanceChargeType: stringPtr("PostPaid"),
									CreationTime:       stringPtr("2024-01-01T00:00:00Z"),
									Tags: &ecs.DescribeInstancesResponseBodyInstancesInstanceTags{
										Tag: []*ecs.DescribeInstancesResponseBodyInstancesInstanceTagsTag{},
									},
								},
							},
						},
					},
				}
				m.On("DescribeInstances", mock.Anything, mock.Anything).Return(response, nil)
			},
			expectedLen: 1,
		},
		{
			name: "multiple pages result",
			tags: map[string]string{"env": "test"},
			mockSetup: func(m *MockECSClient) {
				// First page
				firstPage := &ecs.DescribeInstancesResponse{
					Body: &ecs.DescribeInstancesResponseBody{
						TotalCount: int32Ptr(150), // Total 150 instances
						PageNumber: int32Ptr(1),
						PageSize:   int32Ptr(100),
						Instances: &ecs.DescribeInstancesResponseBodyInstances{
							Instance: make([]*ecs.DescribeInstancesResponseBodyInstancesInstance, 100), // First 100 instances
						},
					},
				}
				// Initialize first 100 instances
				for i := 0; i < 100; i++ {
					firstPage.Body.Instances.Instance[i] = &ecs.DescribeInstancesResponseBodyInstancesInstance{
						InstanceId:         stringPtr(fmt.Sprintf("i-%d", i)),
						RegionId:           stringPtr("cn-hangzhou"),
						ZoneId:             stringPtr("cn-hangzhou-h"),
						InstanceType:       stringPtr("ecs.g6.large"),
						ImageId:            stringPtr("img-123"),
						Cpu:                int32Ptr(2),
						Memory:             int32Ptr(8192),
						Status:             stringPtr("Running"),
						InstanceChargeType: stringPtr("PostPaid"),
					}
				}

				// Second page
				secondPage := &ecs.DescribeInstancesResponse{
					Body: &ecs.DescribeInstancesResponseBody{
						TotalCount: int32Ptr(150),
						PageNumber: int32Ptr(2),
						PageSize:   int32Ptr(100),
						Instances: &ecs.DescribeInstancesResponseBodyInstances{
							Instance: make([]*ecs.DescribeInstancesResponseBodyInstancesInstance, 50), // Remaining 50 instances
						},
					},
				}
				// Initialize remaining 50 instances
				for i := 0; i < 50; i++ {
					secondPage.Body.Instances.Instance[i] = &ecs.DescribeInstancesResponseBodyInstancesInstance{
						InstanceId:         stringPtr(fmt.Sprintf("i-%d", i+100)),
						RegionId:           stringPtr("cn-hangzhou"),
						ZoneId:             stringPtr("cn-hangzhou-h"),
						InstanceType:       stringPtr("ecs.g6.large"),
						ImageId:            stringPtr("img-123"),
						Cpu:                int32Ptr(2),
						Memory:             int32Ptr(8192),
						Status:             stringPtr("Running"),
						InstanceChargeType: stringPtr("PostPaid"),
					}
				}

				// Set up mock expectations - we'll use call count to simulate different responses
				m.On("DescribeInstances", mock.Anything, mock.Anything).Return(firstPage, nil).Once()
				m.On("DescribeInstances", mock.Anything, mock.Anything).Return(secondPage, nil).Once()
			},
			expectedLen: 150,
		},
		{
			name: "API error on first page",
			tags: map[string]string{"env": "test"},
			mockSetup: func(m *MockECSClient) {
				m.On("DescribeInstances", mock.Anything, mock.Anything).Return(nil, errors.New("API error"))
			},
			expectError: true,
		},
		{
			name: "API error on second page",
			tags: map[string]string{"env": "test"},
			mockSetup: func(m *MockECSClient) {
				// First page succeeds
				firstPage := &ecs.DescribeInstancesResponse{
					Body: &ecs.DescribeInstancesResponseBody{
						TotalCount: int32Ptr(150),
						PageNumber: int32Ptr(1),
						PageSize:   int32Ptr(100),
						Instances: &ecs.DescribeInstancesResponseBodyInstances{
							Instance: make([]*ecs.DescribeInstancesResponseBodyInstancesInstance, 100),
						},
					},
				}
				// Initialize first 100 instances
				for i := 0; i < 100; i++ {
					firstPage.Body.Instances.Instance[i] = &ecs.DescribeInstancesResponseBodyInstancesInstance{
						InstanceId:         stringPtr(fmt.Sprintf("i-%d", i)),
						RegionId:           stringPtr("cn-hangzhou"),
						ZoneId:             stringPtr("cn-hangzhou-h"),
						InstanceType:       stringPtr("ecs.g6.large"),
						ImageId:            stringPtr("img-123"),
						Cpu:                int32Ptr(2),
						Memory:             int32Ptr(8192),
						Status:             stringPtr("Running"),
						InstanceChargeType: stringPtr("PostPaid"),
					}
				}

				// Set up mock expectations
				m.On("DescribeInstances", mock.Anything, mock.Anything).Return(firstPage, nil).Once()
				m.On("DescribeInstances", mock.Anything, mock.Anything).Return(nil, errors.New("API error")).Once()
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockECSClient)
			tt.mockSetup(mockClient)

			provider := NewProvider(context.Background(), "cn-hangzhou", mockClient)
			result, err := provider.List(context.Background(), tt.tags)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Len(t, result, tt.expectedLen)
			}

			mockClient.AssertExpectations(t)
		})
	}
}

func TestDelete(t *testing.T) {
	tests := []struct {
		name        string
		instanceID  string
		mockSetup   func(*MockECSClient)
		expectError bool
	}{
		{
			name:       "successful deletion",
			instanceID: "i-123",
			mockSetup: func(m *MockECSClient) {
				// Mock Get call
				getResponse := &ecs.DescribeInstancesResponse{
					Body: &ecs.DescribeInstancesResponseBody{
						Instances: &ecs.DescribeInstancesResponseBodyInstances{
							Instance: []*ecs.DescribeInstancesResponseBodyInstancesInstance{
								{
									InstanceId:   stringPtr("i-123"),
									RegionId:     stringPtr("cn-hangzhou"),
									ZoneId:       stringPtr("cn-hangzhou-h"),
									InstanceType: stringPtr("ecs.g6.large"),
									ImageId:      stringPtr("img-123"),
									Cpu:          int32Ptr(2),
									Memory:       int32Ptr(8192),
									Status:       stringPtr("Running"),
								},
							},
						},
					},
				}
				m.On("DescribeInstances", mock.Anything, mock.Anything).Return(getResponse, nil)

				// Mock Delete call
				deleteResponse := &ecs.DeleteInstancesResponse{
					Body: &ecs.DeleteInstancesResponseBody{
						RequestId: stringPtr("test-request-id"),
					},
				}
				m.On("DeleteInstances", mock.Anything, mock.Anything).Return(deleteResponse, nil)
			},
		},
		{
			name:       "instance not found",
			instanceID: "i-notfound",
			mockSetup: func(m *MockECSClient) {
				getResponse := &ecs.DescribeInstancesResponse{
					Body: &ecs.DescribeInstancesResponseBody{
						Instances: &ecs.DescribeInstancesResponseBodyInstances{
							Instance: []*ecs.DescribeInstancesResponseBodyInstancesInstance{},
						},
					},
				}
				m.On("DescribeInstances", mock.Anything, mock.Anything).Return(getResponse, nil)
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockECSClient)
			tt.mockSetup(mockClient)

			provider := NewProvider(context.Background(), "cn-hangzhou", mockClient)
			err := provider.Delete(context.Background(), tt.instanceID)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			mockClient.AssertExpectations(t)
		})
	}
}

func TestTagInstance(t *testing.T) {
	tests := []struct {
		name        string
		instanceID  string
		tags        map[string]string
		mockSetup   func(*MockECSClient)
		expectError bool
	}{
		{
			name:       "successful tagging",
			instanceID: "i-123",
			tags:       map[string]string{"env": "prod"},
			mockSetup: func(m *MockECSClient) {
				response := &ecs.TagResourcesResponse{}
				m.On("TagResources", mock.Anything, mock.Anything).Return(response, nil)
			},
		},
		{
			name:       "API error",
			instanceID: "i-123",
			tags:       map[string]string{"env": "prod"},
			mockSetup: func(m *MockECSClient) {
				m.On("TagResources", mock.Anything, mock.Anything).Return(nil, errors.New("API error"))
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockECSClient)
			tt.mockSetup(mockClient)

			provider := NewProvider(context.Background(), "cn-hangzhou", mockClient)
			err := provider.TagInstance(context.Background(), tt.instanceID, tt.tags)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			mockClient.AssertExpectations(t)
		})
	}
}

func TestCacheOperations(t *testing.T) {
	provider := NewProvider(context.Background(), "cn-hangzhou", new(MockECSClient))

	// Test setCachedInstance and getCachedInstance
	instance := &Instance{
		InstanceID: "i-123",
		Region:     "cn-hangzhou",
		Zone:       "cn-hangzhou-h",
		CPU:        resource.MustParse("2"),
		Memory:     resource.MustParse("8Gi"),
		GPU:        resource.MustParse("0"),
		GPUSpec:    "",
	}

	provider.setCachedInstance("i-123", instance)
	cached, exists := provider.getCachedInstance("i-123")
	assert.True(t, exists)
	assert.Equal(t, instance, cached)

	// Test deleteCachedInstance
	provider.deleteCachedInstance("i-123")
	_, exists = provider.getCachedInstance("i-123")
	assert.False(t, exists)
}

func TestSetCacheTTL(t *testing.T) {
	provider := NewProvider(context.Background(), "cn-hangzhou", new(MockECSClient))

	newTTL := 1 * time.Minute
	provider.SetCacheTTL(newTTL)

	assert.Equal(t, newTTL, provider.cacheTTL)
}

func TestConvertTags(t *testing.T) {
	ecsTags := []*ecs.DescribeInstancesResponseBodyInstancesInstanceTagsTag{
		{TagKey: stringPtr("env"), TagValue: stringPtr("prod")},
		{TagKey: stringPtr("app"), TagValue: stringPtr("test")},
	}

	result := convertTags(&ecs.DescribeInstancesResponseBodyInstancesInstanceTags{Tag: ecsTags})

	assert.Len(t, result, 2)
	assert.Equal(t, "prod", result["env"])
	assert.Equal(t, "test", result["app"])
}

func TestNotFoundError(t *testing.T) {
	originalErr := errors.New("instance not found")
	notFoundErr := NewNotFoundError(originalErr)

	assert.Error(t, notFoundErr)
	assert.True(t, IsNotFoundError(notFoundErr))
	assert.Contains(t, notFoundErr.Error(), "instance not found")

	regularErr := errors.New("regular error")
	assert.False(t, IsNotFoundError(regularErr))
}
