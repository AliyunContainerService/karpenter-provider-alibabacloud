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
	"errors"
	"testing"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockECSClient is a mock implementation of ECSClient
type MockECSClient struct {
	mock.Mock
}

func (m *MockECSClient) RunInstances(ctx context.Context, request *ecs.RunInstancesRequest) (*ecs.RunInstancesResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (m *MockECSClient) DescribeInstances(ctx context.Context, request *ecs.DescribeInstancesRequest) (*ecs.DescribeInstancesResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (m *MockECSClient) DeleteInstances(ctx context.Context, request *ecs.DeleteInstancesRequest) (*ecs.DeleteInstancesResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (m *MockECSClient) TagResources(ctx context.Context, request *ecs.TagResourcesRequest) (*ecs.TagResourcesResponse, error) {
	//TODO implement me
	panic("implement me")
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

func (m *MockECSClient) DescribeCapacityReservations(ctx context.Context, id string, tags map[string]string) (*ecs.DescribeCapacityReservationsResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (m *MockECSClient) DescribePrice(ctx context.Context, instanceType string) (*ecs.DescribePriceResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (m *MockECSClient) DescribeSecurityGroups(ctx context.Context, tags map[string]string) (*ecs.DescribeSecurityGroupsResponse, error) {
	args := m.Called(ctx, tags)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ecs.DescribeSecurityGroupsResponse), args.Error(1)
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name          string
		selectorTerms []v1alpha1.SecurityGroupSelectorTerm
		mockSetup     func(*MockECSClient)
		expected      []v1alpha1.SecurityGroup
		expectError   bool
	}{
		{
			name:          "empty selector terms",
			selectorTerms: []v1alpha1.SecurityGroupSelectorTerm{},
			expected:      []v1alpha1.SecurityGroup{},
		},
		{
			name: "resolve by ID - success",
			selectorTerms: []v1alpha1.SecurityGroupSelectorTerm{
				{
					ID: stringPtr("sg-12345"),
				},
			},
			expected: []v1alpha1.SecurityGroup{
				{
					ID: "sg-12345",
				},
			},
		},
		{
			name: "resolve by tags - success",
			selectorTerms: []v1alpha1.SecurityGroupSelectorTerm{
				{
					Tags: map[string]string{
						"env": "prod",
					},
				},
			},
			mockSetup: func(m *MockECSClient) {
				sgID1 := "sg-tag-1"
				sgID2 := "sg-tag-2"
				response := &ecs.DescribeSecurityGroupsResponse{
					Body: &ecs.DescribeSecurityGroupsResponseBody{
						SecurityGroups: &ecs.DescribeSecurityGroupsResponseBodySecurityGroups{
							SecurityGroup: []*ecs.DescribeSecurityGroupsResponseBodySecurityGroupsSecurityGroup{
								{
									SecurityGroupId: &sgID1,
								},
								{
									SecurityGroupId: &sgID2,
								},
							},
						},
					},
				}
				m.On("DescribeSecurityGroups", mock.Anything, map[string]string{"env": "prod"}).Return(response, nil)
			},
			expected: []v1alpha1.SecurityGroup{
				{
					ID: "sg-tag-1",
				},
				{
					ID: "sg-tag-2",
				},
			},
		},
		{
			name: "resolve by tags - API error",
			selectorTerms: []v1alpha1.SecurityGroupSelectorTerm{
				{
					Tags: map[string]string{
						"env": "test",
					},
				},
			},
			mockSetup: func(m *MockECSClient) {
				m.On("DescribeSecurityGroups", mock.Anything, map[string]string{"env": "test"}).Return(nil, errors.New("API error"))
			},
			expectError: true,
		},
		{
			name: "multiple terms - ID and tags",
			selectorTerms: []v1alpha1.SecurityGroupSelectorTerm{
				{
					ID: stringPtr("sg-12345"),
				},
				{
					Tags: map[string]string{
						"env": "prod",
					},
				},
			},
			mockSetup: func(m *MockECSClient) {
				sgID := "sg-tag-1"
				response := &ecs.DescribeSecurityGroupsResponse{
					Body: &ecs.DescribeSecurityGroupsResponseBody{
						SecurityGroups: &ecs.DescribeSecurityGroupsResponseBodySecurityGroups{
							SecurityGroup: []*ecs.DescribeSecurityGroupsResponseBodySecurityGroupsSecurityGroup{
								{
									SecurityGroupId: &sgID,
								},
							},
						},
					},
				}
				m.On("DescribeSecurityGroups", mock.Anything, map[string]string{"env": "prod"}).Return(response, nil)
			},
			expected: []v1alpha1.SecurityGroup{
				{
					ID: "sg-12345",
				},
				{
					ID: "sg-tag-1",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockECSClient)
			if tt.mockSetup != nil {
				tt.mockSetup(mockClient)
			}

			provider := NewProvider("cn-hangzhou", mockClient)
			result, err := provider.Resolve(context.Background(), tt.selectorTerms)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}

			if tt.mockSetup != nil {
				mockClient.AssertExpectations(t)
			}
		})
	}
}

func TestGetByTags(t *testing.T) {
	tests := []struct {
		name        string
		tags        map[string]string
		mockSetup   func(*MockECSClient)
		expected    []v1alpha1.SecurityGroup
		expectError bool
	}{
		{
			name: "successful retrieval with multiple results",
			tags: map[string]string{"env": "prod"},
			mockSetup: func(m *MockECSClient) {
				sgID1 := "sg-1"
				sgID2 := "sg-2"
				response := &ecs.DescribeSecurityGroupsResponse{
					Body: &ecs.DescribeSecurityGroupsResponseBody{
						SecurityGroups: &ecs.DescribeSecurityGroupsResponseBodySecurityGroups{
							SecurityGroup: []*ecs.DescribeSecurityGroupsResponseBodySecurityGroupsSecurityGroup{
								{
									SecurityGroupId: &sgID1,
								},
								{
									SecurityGroupId: &sgID2,
								},
							},
						},
					},
				}
				m.On("DescribeSecurityGroups", mock.Anything, map[string]string{"env": "prod"}).Return(response, nil)
			},
			expected: []v1alpha1.SecurityGroup{
				{
					ID: "sg-1",
				},
				{
					ID: "sg-2",
				},
			},
		},
		{
			name: "API error",
			tags: map[string]string{"env": "test"},
			mockSetup: func(m *MockECSClient) {
				m.On("DescribeSecurityGroups", mock.Anything, map[string]string{"env": "test"}).Return(nil, errors.New("API error"))
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockECSClient)
			tt.mockSetup(mockClient)

			provider := NewProvider("cn-hangzhou", mockClient)
			result, err := provider.getByTags(context.Background(), tt.tags)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}

			mockClient.AssertExpectations(t)
		})
	}
}

func TestRemoveDuplicateSecurityGroups(t *testing.T) {
	sgs := []v1alpha1.SecurityGroup{
		{ID: "sg-1"},
		{ID: "sg-2"},
		{ID: "sg-1"}, // duplicate
	}

	result := removeDuplicateSecurityGroups(sgs)
	assert.Len(t, result, 2)
}

func TestClearCache(t *testing.T) {
	provider := NewProvider("cn-hangzhou", new(MockECSClient))

	// Add something to cache
	provider.setCachedValue("test-key", "test-value")

	// Verify it's in cache
	value, exists := provider.getCachedValue("test-key")
	assert.True(t, exists)
	assert.Equal(t, "test-value", value)

	// Clear cache
	provider.ClearCache()

	// Verify cache is cleared
	_, exists = provider.getCachedValue("test-key")
	assert.False(t, exists)
}

func stringPtr(s string) *string {
	return &s
}
