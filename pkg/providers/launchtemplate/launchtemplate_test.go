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

package launchtemplate

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

func (m *MockECSClient) CreateLaunchTemplate(ctx context.Context, request *ecs.CreateLaunchTemplateRequest) (*ecs.CreateLaunchTemplateResponse, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ecs.CreateLaunchTemplateResponse), args.Error(1)
}

func (m *MockECSClient) DescribeLaunchTemplates(ctx context.Context, request *ecs.DescribeLaunchTemplatesRequest) (*ecs.DescribeLaunchTemplatesResponse, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ecs.DescribeLaunchTemplatesResponse), args.Error(1)
}

func (m *MockECSClient) DeleteLaunchTemplate(ctx context.Context, request *ecs.DeleteLaunchTemplateRequest) (*ecs.DeleteLaunchTemplateResponse, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ecs.DeleteLaunchTemplateResponse), args.Error(1)
}

func TestCreate(t *testing.T) {
	size := int32(40)
	spotStrategy := "SpotWithPriceLimit"

	tests := []struct {
		name        string
		nodeClass   *v1alpha1.ECSNodeClass
		userData    string
		mockSetup   func(*MockECSClient)
		expectError bool
	}{
		{
			name: "successful creation with all fields",
			nodeClass: &v1alpha1.ECSNodeClass{
				Status: v1alpha1.ECSNodeClassStatus{
					Images: []v1alpha1.Image{
						{ID: "img-123", Name: "test-image"},
					},
					SecurityGroups: []v1alpha1.SecurityGroup{
						{ID: "sg-123"},
						{ID: "sg-456"},
					},
					VSwitches: []v1alpha1.VSwitch{
						{ID: "vsw-123", Zone: "cn-hangzhou-h"},
					},
				},
				Spec: v1alpha1.ECSNodeClassSpec{
					SpotStrategy: &spotStrategy,
					SystemDisk: &v1alpha1.SystemDiskSpec{
						Category: "cloud_essd",
						Size:     &size,
					},
				},
			},
			userData: "#!/bin/bash\\necho hello",
			mockSetup: func(m *MockECSClient) {
				ltID := "lt-123"
				response := &ecs.CreateLaunchTemplateResponse{
					Body: &ecs.CreateLaunchTemplateResponseBody{
						LaunchTemplateId: &ltID,
					},
				}
				m.On("CreateLaunchTemplate", mock.Anything, mock.Anything).Return(response, nil)
			},
		},
		{
			name: "creation with minimal fields",
			nodeClass: &v1alpha1.ECSNodeClass{
				Status: v1alpha1.ECSNodeClassStatus{
					Images: []v1alpha1.Image{
						{ID: "img-123"},
					},
				},
			},
			userData: "",
			mockSetup: func(m *MockECSClient) {
				ltID := "lt-456"
				response := &ecs.CreateLaunchTemplateResponse{
					Body: &ecs.CreateLaunchTemplateResponseBody{
						LaunchTemplateId: &ltID,
					},
				}
				m.On("CreateLaunchTemplate", mock.Anything, mock.Anything).Return(response, nil)
			},
		},
		{
			name: "API error",
			nodeClass: &v1alpha1.ECSNodeClass{
				Status: v1alpha1.ECSNodeClassStatus{
					Images: []v1alpha1.Image{
						{ID: "img-123"},
					},
				},
			},
			userData: "",
			mockSetup: func(m *MockECSClient) {
				m.On("CreateLaunchTemplate", mock.Anything, mock.Anything).Return(nil, errors.New("API error"))
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockECSClient)
			tt.mockSetup(mockClient)

			provider := NewProvider("cn-hangzhou", mockClient)
			result, err := provider.Create(context.Background(), tt.nodeClass, tt.userData)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				assert.NotEmpty(t, result.ID)
				assert.NotEmpty(t, result.Name)
				assert.NotEmpty(t, result.Version)
			}

			mockClient.AssertExpectations(t)
		})
	}
}

func TestGet(t *testing.T) {
	tests := []struct {
		name        string
		id          string
		mockSetup   func(*MockECSClient)
		expected    *LaunchTemplate
		expectError bool
	}{
		{
			name: "successful get",
			id:   "lt-123",
			mockSetup: func(m *MockECSClient) {
				ltID := "lt-123"
				ltName := "test-template"
				var version int64 = 1
				response := &ecs.DescribeLaunchTemplatesResponse{
					Body: &ecs.DescribeLaunchTemplatesResponseBody{
						LaunchTemplateSets: &ecs.DescribeLaunchTemplatesResponseBodyLaunchTemplateSets{
							LaunchTemplateSet: []*ecs.DescribeLaunchTemplatesResponseBodyLaunchTemplateSetsLaunchTemplateSet{
								{
									LaunchTemplateId:     &ltID,
									LaunchTemplateName:   &ltName,
									DefaultVersionNumber: &version,
								},
							},
						},
					},
				}
				m.On("DescribeLaunchTemplates", mock.Anything, mock.Anything).Return(response, nil)
			},
			expected: &LaunchTemplate{
				ID:      "lt-123",
				Name:    "test-template",
				Version: "1",
			},
		},
		{
			name: "template not found",
			id:   "lt-notfound",
			mockSetup: func(m *MockECSClient) {
				response := &ecs.DescribeLaunchTemplatesResponse{
					Body: &ecs.DescribeLaunchTemplatesResponseBody{
						LaunchTemplateSets: &ecs.DescribeLaunchTemplatesResponseBodyLaunchTemplateSets{
							LaunchTemplateSet: []*ecs.DescribeLaunchTemplatesResponseBodyLaunchTemplateSetsLaunchTemplateSet{},
						},
					},
				}
				m.On("DescribeLaunchTemplates", mock.Anything, mock.Anything).Return(response, nil)
			},
			expectError: true,
		},
		{
			name: "API error",
			id:   "lt-error",
			mockSetup: func(m *MockECSClient) {
				m.On("DescribeLaunchTemplates", mock.Anything, mock.Anything).Return(nil, errors.New("API error"))
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockECSClient)
			tt.mockSetup(mockClient)

			provider := NewProvider("cn-hangzhou", mockClient)
			result, err := provider.Get(context.Background(), tt.id)

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

func TestDelete(t *testing.T) {
	tests := []struct {
		name        string
		id          string
		mockSetup   func(*MockECSClient)
		expectError bool
	}{
		{
			name: "successful deletion",
			id:   "lt-123",
			mockSetup: func(m *MockECSClient) {
				response := &ecs.DeleteLaunchTemplateResponse{}
				m.On("DeleteLaunchTemplate", mock.Anything, mock.Anything).Return(response, nil)
			},
		},
		{
			name: "API error",
			id:   "lt-error",
			mockSetup: func(m *MockECSClient) {
				m.On("DeleteLaunchTemplate", mock.Anything, mock.Anything).Return(nil, errors.New("API error"))
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockECSClient)
			tt.mockSetup(mockClient)

			provider := NewProvider("cn-hangzhou", mockClient)
			err := provider.Delete(context.Background(), tt.id)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			mockClient.AssertExpectations(t)
		})
	}
}

func TestResolve(t *testing.T) {
	ltID := "lt-existing"

	tests := []struct {
		name        string
		nodeClass   *v1alpha1.ECSNodeClass
		mockSetup   func(*MockECSClient)
		expectError bool
	}{
		{
			name: "resolve with existing launch template ID",
			nodeClass: &v1alpha1.ECSNodeClass{
				Spec: v1alpha1.ECSNodeClassSpec{
					LaunchTemplateID: &ltID,
				},
			},
			mockSetup: func(m *MockECSClient) {
				existingID := "lt-existing"
				existingName := "existing-template"
				var version int64 = 1
				response := &ecs.DescribeLaunchTemplatesResponse{
					Body: &ecs.DescribeLaunchTemplatesResponseBody{
						LaunchTemplateSets: &ecs.DescribeLaunchTemplatesResponseBodyLaunchTemplateSets{
							LaunchTemplateSet: []*ecs.DescribeLaunchTemplatesResponseBodyLaunchTemplateSetsLaunchTemplateSet{
								{
									LaunchTemplateId:     &existingID,
									LaunchTemplateName:   &existingName,
									DefaultVersionNumber: &version,
								},
							},
						},
					},
				}
				m.On("DescribeLaunchTemplates", mock.Anything, mock.Anything).Return(response, nil)
			},
		},
		{
			name: "resolve by creating new template",
			nodeClass: &v1alpha1.ECSNodeClass{
				Status: v1alpha1.ECSNodeClassStatus{
					Images: []v1alpha1.Image{
						{ID: "img-123"},
					},
				},
			},
			mockSetup: func(m *MockECSClient) {
				ltID := "lt-new"
				response := &ecs.CreateLaunchTemplateResponse{
					Body: &ecs.CreateLaunchTemplateResponseBody{
						LaunchTemplateId: &ltID,
					},
				}
				m.On("CreateLaunchTemplate", mock.Anything, mock.Anything).Return(response, nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockECSClient)
			tt.mockSetup(mockClient)

			provider := NewProvider("cn-hangzhou", mockClient)
			result, err := provider.Resolve(context.Background(), tt.nodeClass)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
			}

			mockClient.AssertExpectations(t)
		})
	}
}

func TestCreateWithSecurityGroupsAndVSwitches(t *testing.T) {
	tests := []struct {
		name      string
		nodeClass *v1alpha1.ECSNodeClass
		mockSetup func(*MockECSClient)
	}{
		{
			name: "with multiple security groups",
			nodeClass: &v1alpha1.ECSNodeClass{
				Status: v1alpha1.ECSNodeClassStatus{
					Images: []v1alpha1.Image{
						{ID: "img-123"},
					},
					SecurityGroups: []v1alpha1.SecurityGroup{
						{ID: "sg-1"},
						{ID: "sg-2"},
						{ID: "sg-3"},
					},
				},
			},
			mockSetup: func(m *MockECSClient) {
				ltID := "lt-sg-test"
				response := &ecs.CreateLaunchTemplateResponse{
					Body: &ecs.CreateLaunchTemplateResponseBody{
						LaunchTemplateId: &ltID,
					},
				}
				m.On("CreateLaunchTemplate", mock.Anything, mock.MatchedBy(func(req *ecs.CreateLaunchTemplateRequest) bool {
					return req.SecurityGroupIds != nil && len(req.SecurityGroupIds) == 3
				})).Return(response, nil)
			},
		},
		{
			name: "with vswitch",
			nodeClass: &v1alpha1.ECSNodeClass{
				Status: v1alpha1.ECSNodeClassStatus{
					Images: []v1alpha1.Image{
						{ID: "img-123"},
					},
					VSwitches: []v1alpha1.VSwitch{
						{ID: "vsw-123", Zone: "cn-hangzhou-h"},
						{ID: "vsw-456", Zone: "cn-hangzhou-i"},
					},
				},
			},
			mockSetup: func(m *MockECSClient) {
				ltID := "lt-vsw-test"
				response := &ecs.CreateLaunchTemplateResponse{
					Body: &ecs.CreateLaunchTemplateResponseBody{
						LaunchTemplateId: &ltID,
					},
				}
				m.On("CreateLaunchTemplate", mock.Anything, mock.MatchedBy(func(req *ecs.CreateLaunchTemplateRequest) bool {
					return req.VSwitchId != nil && *req.VSwitchId == "vsw-123"
				})).Return(response, nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockECSClient)
			tt.mockSetup(mockClient)

			provider := NewProvider("cn-hangzhou", mockClient)
			result, err := provider.Create(context.Background(), tt.nodeClass, "")

			assert.NoError(t, err)
			assert.NotNil(t, result)

			mockClient.AssertExpectations(t)
		})
	}
}
