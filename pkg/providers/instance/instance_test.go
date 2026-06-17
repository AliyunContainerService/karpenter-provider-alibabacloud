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

func (m *MockECSClient) DescribeAvailableResource(ctx context.Context, request *ecs.DescribeAvailableResourceRequest) (*ecs.DescribeAvailableResourceResponse, error) {
	// Not used in instance-level tests; satisfies ECSClient interface
	return nil, errors.New("not implemented")
}

func (m *MockECSClient) DescribeImages(ctx context.Context, imageIDs []string, filters map[string]string) ([]ecs.DescribeImagesResponseBodyImagesImage, error) {
	//TODO implement me
	panic("implement me")
}

func (m *MockECSClient) DescribeSecurityGroups(ctx context.Context, id string, name string, tags map[string]string) (*ecs.DescribeSecurityGroupsResponse, error) {
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
			name: "sets RAM role name",
			opts: CreateOptions{
				InstanceType:     "ecs.g6.large",
				ImageID:          "img-123",
				VSwitchID:        "vsw-123",
				SecurityGroupIDs: []string{"sg-123"},
				RAMRoleName:      "KarpenterNodeRole",
				SystemDisk: SystemDisk{
					Category: "cloud_essd",
					Size:     40,
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
				m.On("RunInstances", mock.Anything, mock.MatchedBy(func(request *ecs.RunInstancesRequest) bool {
					return request.RamRoleName != nil && *request.RamRoleName == "KarpenterNodeRole"
				})).Return(response, nil)
			},
		},
		{
			name: "API error",
			opts: CreateOptions{
				InstanceType:     "ecs.g6.large",
				ImageID:          "img-123",
				VSwitchID:        "vsw-123",
				SecurityGroupIDs: []string{"sg-123"},
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

func TestCreateSecurityGroupIDs(t *testing.T) {
	tests := []struct {
		name               string
		securityGroupIDs   []string
		expectedRequestIDs []string
		expectError        string
		expectAPICall      bool
	}{
		{
			name:               "single security group uses repeated field",
			securityGroupIDs:   []string{"sg-1"},
			expectedRequestIDs: []string{"sg-1"},
			expectAPICall:      true,
		},
		{
			name:               "multiple security groups are normalized before request",
			securityGroupIDs:   []string{"sg-2", "sg-1", "sg-2"},
			expectedRequestIDs: []string{"sg-1", "sg-2"},
			expectAPICall:      true,
		},
		{
			name:          "zero security groups are rejected before ECS call",
			expectError:   "at least one security group ID is required",
			expectAPICall: false,
		},
		{
			name:             "empty security group ID is rejected before ECS call",
			securityGroupIDs: []string{"sg-1", ""},
			expectError:      "security group ID cannot be empty",
			expectAPICall:    false,
		},
		{
			name:               "more than five security groups are sent to ECS",
			securityGroupIDs:   []string{"sg-6", "sg-5", "sg-4", "sg-3", "sg-2", "sg-1"},
			expectedRequestIDs: []string{"sg-1", "sg-2", "sg-3", "sg-4", "sg-5", "sg-6"},
			expectAPICall:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockECSClient)
			if tt.expectAPICall {
				instanceID := "i-123456"
				response := &ecs.RunInstancesResponse{
					Body: &ecs.RunInstancesResponseBody{
						InstanceIdSets: &ecs.RunInstancesResponseBodyInstanceIdSets{
							InstanceIdSet: []*string{&instanceID},
						},
					},
				}
				mockClient.On("RunInstances", mock.Anything, mock.MatchedBy(func(request *ecs.RunInstancesRequest) bool {
					assert.Nil(t, request.SecurityGroupId)
					assert.Equal(t, tt.expectedRequestIDs, stringPointersToValues(request.SecurityGroupIds))
					return true
				})).Return(response, nil)
			}

			provider := NewProvider(context.Background(), "cn-hangzhou", mockClient)
			_, err := provider.Create(context.Background(), CreateOptions{
				InstanceType:     "ecs.g6.large",
				ImageID:          "img-123",
				VSwitchID:        "vsw-123",
				SecurityGroupIDs: tt.securityGroupIDs,
				SystemDisk: SystemDisk{
					Category: "cloud_essd",
					Size:     40,
				},
			})

			if tt.expectError != "" {
				assert.ErrorContains(t, err, tt.expectError)
			} else {
				assert.NoError(t, err)
			}
			mockClient.AssertExpectations(t)
		})
	}
}

func TestCreatePreservesECSSecurityGroupErrorDetails(t *testing.T) {
	mockClient := new(MockECSClient)
	mockClient.On("RunInstances", mock.Anything, mock.Anything).Return(nil, errors.New("InvalidSecurityGroupLimitExceeded: attach limit exceeded"))

	provider := NewProvider(context.Background(), "cn-hangzhou", mockClient)
	_, err := provider.Create(context.Background(), CreateOptions{
		InstanceType:     "ecs.g6.large",
		ImageID:          "img-123",
		VSwitchID:        "vsw-123",
		SecurityGroupIDs: []string{"sg-1", "sg-2", "sg-3", "sg-4", "sg-5", "sg-6"},
		SystemDisk: SystemDisk{
			Category: "cloud_essd",
			Size:     40,
		},
	})

	assert.ErrorContains(t, err, "failed to create instance")
	assert.ErrorContains(t, err, "InvalidSecurityGroupLimitExceeded")
	assert.ErrorContains(t, err, "attach limit exceeded")
	mockClient.AssertExpectations(t)
}

func TestCreateDiskOptionsOmitSendSemantics(t *testing.T) {
	encrypted := true
	deleteWithInstanceFalse := false
	mockClient := new(MockECSClient)
	instanceID := "i-123456"
	response := &ecs.RunInstancesResponse{
		Body: &ecs.RunInstancesResponseBody{
			InstanceIdSets: &ecs.RunInstancesResponseBodyInstanceIdSets{
				InstanceIdSet: []*string{&instanceID},
			},
		},
	}
	mockClient.On("RunInstances", mock.Anything, mock.MatchedBy(func(request *ecs.RunInstancesRequest) bool {
		assert.Equal(t, "cloud_essd", *request.SystemDisk.Category)
		assert.Equal(t, "40", *request.SystemDisk.Size)
		assert.Equal(t, "PL0", *request.SystemDisk.PerformanceLevel)
		assert.Equal(t, "true", *request.SystemDisk.Encrypted)
		assert.Equal(t, "kms-system", *request.SystemDisk.KMSKeyId)

		if assert.Len(t, request.DataDisk, 2) {
			essd := request.DataDisk[0]
			assert.Equal(t, "cloud_essd", *essd.Category)
			assert.Equal(t, int32(120), *essd.Size)
			assert.Equal(t, "/dev/xvdb", *essd.Device)
			assert.Equal(t, "PL1", *essd.PerformanceLevel)
			assert.Equal(t, "true", *essd.Encrypted)
			assert.Equal(t, "kms-data", *essd.KMSKeyId)
			assert.Equal(t, "s-123", *essd.SnapshotId)
			assert.Equal(t, false, *essd.DeleteWithInstance)

			ssd := request.DataDisk[1]
			assert.Equal(t, "cloud_ssd", *ssd.Category)
			assert.Equal(t, int32(80), *ssd.Size)
			assert.Nil(t, ssd.PerformanceLevel)
			assert.Nil(t, ssd.Encrypted)
			assert.Nil(t, ssd.KMSKeyId)
			assert.Nil(t, ssd.SnapshotId)
			assert.Nil(t, ssd.Device)
			assert.Nil(t, ssd.DeleteWithInstance)
		}
		return true
	})).Return(response, nil)

	provider := NewProvider(context.Background(), "cn-hangzhou", mockClient)
	_, err := provider.Create(context.Background(), CreateOptions{
		InstanceType:     "ecs.g6.large",
		ImageID:          "img-123",
		VSwitchID:        "vsw-123",
		SecurityGroupIDs: []string{"sg-123"},
		SystemDisk: SystemDisk{
			Category:         "cloud_essd",
			Size:             40,
			PerformanceLevel: "PL0",
			Encrypted:        &encrypted,
			KMSKeyID:         "kms-system",
		},
		DataDisks: []DataDisk{
			{
				Category:           "cloud_essd",
				Size:               120,
				Device:             "/dev/xvdb",
				PerformanceLevel:   "PL1",
				Encrypted:          &encrypted,
				KMSKeyID:           "kms-data",
				SnapshotID:         "s-123",
				DeleteWithInstance: &deleteWithInstanceFalse,
			},
			{
				Category: "cloud_ssd",
				Size:     80,
			},
		},
	})
	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func stringPointersToValues(values []*string) []string {
	if values == nil {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == nil {
			result = append(result, "")
			continue
		}
		result = append(result, *value)
	}
	return result
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
				deleteResponse := &ecs.DeleteInstancesResponse{
					Body: &ecs.DeleteInstancesResponseBody{
						RequestId: stringPtr("test-request-id"),
					},
				}
				m.On("DeleteInstances", mock.Anything, mock.Anything).Return(deleteResponse, nil)
				// Delete calls DescribeInstances after DeleteInstances to check termination status.
				// Return "Stopping" so Delete returns nil (still terminating, caller will retry).
				stoppingStatus := "Stopping"
				describeResponse := &ecs.DescribeInstancesResponse{
					Body: &ecs.DescribeInstancesResponseBody{
						Instances: &ecs.DescribeInstancesResponseBodyInstances{
							Instance: []*ecs.DescribeInstancesResponseBodyInstancesInstance{
								{Status: &stoppingStatus},
							},
						},
					},
				}
				m.On("DescribeInstances", mock.Anything, mock.Anything).Return(describeResponse, nil)
			},
		},
		{
			name:       "instance not found",
			instanceID: "i-notfound",
			mockSetup: func(m *MockECSClient) {
				m.On("DeleteInstances", mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("InvalidInstanceId.NotFound: instance i-notfound does not exist"))
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

func TestCreateMetadataOptions(t *testing.T) {
	tests := []struct {
		name   string
		opts   CreateOptions
		assert func(*testing.T, *ecs.RunInstancesRequest)
	}{
		{
			name: "nil metadata options omits request fields",
			opts: CreateOptions{
				InstanceType:     "ecs.g6.large",
				ImageID:          "img-123",
				VSwitchID:        "vsw-123",
				SecurityGroupIDs: []string{"sg-123"},
				SystemDisk: SystemDisk{
					Category: "cloud_essd",
					Size:     40,
				},
			},
			assert: func(t *testing.T, request *ecs.RunInstancesRequest) {
				assert.Nil(t, request.HttpEndpoint)
				assert.Nil(t, request.HttpTokens)
				assert.Nil(t, request.HttpPutResponseHopLimit)
			},
		},
		{
			name: "sets non-empty metadata options",
			opts: CreateOptions{
				InstanceType:     "ecs.g6.large",
				ImageID:          "img-123",
				VSwitchID:        "vsw-123",
				SecurityGroupIDs: []string{"sg-123"},
				SystemDisk: SystemDisk{
					Category: "cloud_essd",
					Size:     40,
				},
				MetadataOptions: &MetadataOptions{
					HttpEndpoint:            stringPtr("disabled"),
					HttpTokens:              stringPtr("required"),
					HttpPutResponseHopLimit: int32Ptr(2),
				},
			},
			assert: func(t *testing.T, request *ecs.RunInstancesRequest) {
				assert.Equal(t, "disabled", *request.HttpEndpoint)
				assert.Equal(t, "required", *request.HttpTokens)
				assert.Equal(t, int32(2), *request.HttpPutResponseHopLimit)
			},
		},
		{
			name: "empty metadata strings are omitted",
			opts: CreateOptions{
				InstanceType:     "ecs.g6.large",
				ImageID:          "img-123",
				VSwitchID:        "vsw-123",
				SecurityGroupIDs: []string{"sg-123"},
				SystemDisk: SystemDisk{
					Category: "cloud_essd",
					Size:     40,
				},
				MetadataOptions: &MetadataOptions{
					HttpEndpoint: stringPtr(""),
					HttpTokens:   stringPtr(""),
				},
			},
			assert: func(t *testing.T, request *ecs.RunInstancesRequest) {
				assert.Nil(t, request.HttpEndpoint)
				assert.Nil(t, request.HttpTokens)
				assert.Nil(t, request.HttpPutResponseHopLimit)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockECSClient)
			instanceID := "i-123456"
			response := &ecs.RunInstancesResponse{
				Body: &ecs.RunInstancesResponseBody{
					InstanceIdSets: &ecs.RunInstancesResponseBodyInstanceIdSets{
						InstanceIdSet: []*string{&instanceID},
					},
				},
			}
			mockClient.On("RunInstances", mock.Anything, mock.MatchedBy(func(request *ecs.RunInstancesRequest) bool {
				tt.assert(t, request)
				return true
			})).Return(response, nil)

			provider := NewProvider(context.Background(), "cn-hangzhou", mockClient)
			_, err := provider.Create(context.Background(), tt.opts)
			assert.NoError(t, err)
			mockClient.AssertExpectations(t)
		})
	}
}
