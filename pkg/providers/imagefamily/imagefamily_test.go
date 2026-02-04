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

package imagefamily

import (
	"context"
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

func (m *MockECSClient) DescribeImages(ctx context.Context, imageIDs []string, filters map[string]string) ([]ecs.DescribeImagesResponseBodyImagesImage, error) {
	args := m.Called(ctx, imageIDs, filters)
	return args.Get(0).([]ecs.DescribeImagesResponseBodyImagesImage), args.Error(1)
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name          string
		selectorTerms []v1alpha1.ImageSelectorTerm
		imageFamily   *string
		mockResponse  *ecs.DescribeImagesResponse
		mockError     error
		expected      []v1alpha1.Image
		expectError   bool
	}{
		{
			name:          "empty selector terms",
			selectorTerms: []v1alpha1.ImageSelectorTerm{},
			expected:      []v1alpha1.Image{},
		},
		{
			name: "resolve by ID",
			selectorTerms: []v1alpha1.ImageSelectorTerm{
				{
					ID: stringPtr("m-1234567890abcdef0"),
				},
			},
			mockResponse: &ecs.DescribeImagesResponse{
				Body: &ecs.DescribeImagesResponseBody{
					Images: &ecs.DescribeImagesResponseBodyImages{
						Image: []*ecs.DescribeImagesResponseBodyImagesImage{
							{
								ImageId:      stringPtr("m-1234567890abcdef0"),
								ImageName:    stringPtr("test-image"),
								Architecture: stringPtr("x86_64"),
							},
						},
					},
				},
			},
			expected: []v1alpha1.Image{
				{
					ID:           "m-1234567890abcdef0",
					Name:         "test-image",
					Architecture: "x86_64",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockECSClient)
			if tt.mockResponse != nil || tt.mockError != nil {
				// Convert ecs.DescribeImagesResponse to []DescribeImagesResponseBodyImagesImage
				var images []ecs.DescribeImagesResponseBodyImagesImage
				if tt.mockResponse != nil && tt.mockResponse.Body != nil && tt.mockResponse.Body.Images != nil {
					for _, img := range tt.mockResponse.Body.Images.Image {
						images = append(images, ecs.DescribeImagesResponseBodyImagesImage{
							ImageId:      img.ImageId,
							ImageName:    img.ImageName,
							Architecture: img.Architecture,
						})
					}
				}
				mockClient.On("DescribeImages", mock.Anything, mock.Anything, mock.Anything).Return(images, tt.mockError)
			}

			provider := NewProvider(mockClient)
			result, err := provider.Resolve(context.Background(), tt.selectorTerms)

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

func stringPtr(s string) *string {
	return &s
}
