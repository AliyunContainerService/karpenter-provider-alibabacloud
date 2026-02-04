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

package capacityreservation

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

func (m *MockECSClient) DescribeSecurityGroups(ctx context.Context, tags map[string]string) (*ecs.DescribeSecurityGroupsResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (m *MockECSClient) DescribePrice(ctx context.Context, instanceType string) (*ecs.DescribePriceResponse, error) {
	//TODO implement me
	panic("implement me")
}

func (m *MockECSClient) DescribeCapacityReservations(ctx context.Context, id string, tags map[string]string) (*ecs.DescribeCapacityReservationsResponse, error) {
	args := m.Called(ctx, id, tags)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ecs.DescribeCapacityReservationsResponse), args.Error(1)
}

func stringPtr(s string) *string {
	return &s
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name          string
		selectorTerms []v1alpha1.CapacityReservationSelectorTerm
		mockSetup     func(*MockECSClient)
		expected      []v1alpha1.CapacityReservation
		expectError   bool
	}{
		{
			name:          "empty selector terms",
			selectorTerms: []v1alpha1.CapacityReservationSelectorTerm{},
			expected:      []v1alpha1.CapacityReservation{},
		},
		{
			name: "resolve by ID - success",
			selectorTerms: []v1alpha1.CapacityReservationSelectorTerm{
				{
					ID: stringPtr("cr-12345"),
				},
			},
			mockSetup: func(m *MockECSClient) {
				id := "cr-12345"
				name := "cn-hangzhou-h"
				response := &ecs.DescribeCapacityReservationsResponse{
					Body: &ecs.DescribeCapacityReservationsResponseBody{
						CapacityReservationSet: &ecs.DescribeCapacityReservationsResponseBodyCapacityReservationSet{
							CapacityReservationItem: []*ecs.DescribeCapacityReservationsResponseBodyCapacityReservationSetCapacityReservationItem{
								{
									PrivatePoolOptionsId:   &id,
									PrivatePoolOptionsName: &name,
								},
							},
						},
					},
				}
				m.On("DescribeCapacityReservations", mock.Anything, "cr-12345", mock.Anything).Return(response, nil)
			},
			expected: []v1alpha1.CapacityReservation{
				{
					ID:   "cr-12345",
					Name: "cn-hangzhou-h",
				},
			},
		},
		{
			name: "resolve by ID - not found",
			selectorTerms: []v1alpha1.CapacityReservationSelectorTerm{
				{
					ID: stringPtr("cr-notfound"),
				},
			},
			mockSetup: func(m *MockECSClient) {
				response := &ecs.DescribeCapacityReservationsResponse{
					Body: &ecs.DescribeCapacityReservationsResponseBody{
						CapacityReservationSet: &ecs.DescribeCapacityReservationsResponseBodyCapacityReservationSet{
							CapacityReservationItem: []*ecs.DescribeCapacityReservationsResponseBodyCapacityReservationSetCapacityReservationItem{},
						},
					},
				}
				m.On("DescribeCapacityReservations", mock.Anything, "cr-notfound", mock.Anything).Return(response, nil)
			},
			expectError: true,
		},
		{
			name: "resolve by tags - success",
			selectorTerms: []v1alpha1.CapacityReservationSelectorTerm{
				{
					Tags: map[string]string{
						"env": "prod",
					},
				},
			},
			mockSetup: func(m *MockECSClient) {
				id1 := "cr-tag-1"
				name1 := "cn-hangzhou-h"
				id2 := "cr-tag-2"
				name2 := "cn-hangzhou-i"
				response := &ecs.DescribeCapacityReservationsResponse{
					Body: &ecs.DescribeCapacityReservationsResponseBody{
						CapacityReservationSet: &ecs.DescribeCapacityReservationsResponseBodyCapacityReservationSet{
							CapacityReservationItem: []*ecs.DescribeCapacityReservationsResponseBodyCapacityReservationSetCapacityReservationItem{
								{
									PrivatePoolOptionsId:   &id1,
									PrivatePoolOptionsName: &name1,
								},
								{
									PrivatePoolOptionsId:   &id2,
									PrivatePoolOptionsName: &name2,
								},
							},
						},
					},
				}
				m.On("DescribeCapacityReservations", mock.Anything, "", map[string]string{"env": "prod"}).Return(response, nil)
			},
			expected: []v1alpha1.CapacityReservation{
				{
					ID:   "cr-tag-1",
					Name: "cn-hangzhou-h",
				},
				{
					ID:   "cr-tag-2",
					Name: "cn-hangzhou-i",
				},
			},
		},
		{
			name: "resolve by ID - API error",
			selectorTerms: []v1alpha1.CapacityReservationSelectorTerm{
				{
					ID: stringPtr("cr-error"),
				},
			},
			mockSetup: func(m *MockECSClient) {
				m.On("DescribeCapacityReservations", mock.Anything, "cr-error", mock.Anything).Return(nil, errors.New("API error"))
			},
			expectError: true,
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

			mockClient.AssertExpectations(t)
		})
	}
}

func TestGetByID(t *testing.T) {
	tests := []struct {
		name        string
		id          string
		mockSetup   func(*MockECSClient)
		expected    v1alpha1.CapacityReservation
		expectError bool
	}{
		{
			name: "successful retrieval",
			id:   "cr-12345",
			mockSetup: func(m *MockECSClient) {
				id := "cr-12345"
				name := "cn-hangzhou-h"
				response := &ecs.DescribeCapacityReservationsResponse{
					Body: &ecs.DescribeCapacityReservationsResponseBody{
						CapacityReservationSet: &ecs.DescribeCapacityReservationsResponseBodyCapacityReservationSet{
							CapacityReservationItem: []*ecs.DescribeCapacityReservationsResponseBodyCapacityReservationSetCapacityReservationItem{
								{
									PrivatePoolOptionsId:   &id,
									PrivatePoolOptionsName: &name,
								},
							},
						},
					},
				}
				m.On("DescribeCapacityReservations", mock.Anything, "cr-12345", mock.Anything).Return(response, nil)
			},
			expected: v1alpha1.CapacityReservation{
				ID:   "cr-12345",
				Name: "cn-hangzhou-h",
			},
		},
		{
			name: "not found",
			id:   "cr-notfound",
			mockSetup: func(m *MockECSClient) {
				response := &ecs.DescribeCapacityReservationsResponse{
					Body: &ecs.DescribeCapacityReservationsResponseBody{
						CapacityReservationSet: &ecs.DescribeCapacityReservationsResponseBodyCapacityReservationSet{
							CapacityReservationItem: []*ecs.DescribeCapacityReservationsResponseBodyCapacityReservationSetCapacityReservationItem{},
						},
					},
				}
				m.On("DescribeCapacityReservations", mock.Anything, "cr-notfound", mock.Anything).Return(response, nil)
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockECSClient)
			tt.mockSetup(mockClient)

			provider := NewProvider("cn-hangzhou", mockClient)
			result, err := provider.getByID(context.Background(), tt.id)

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

func TestGetByTags(t *testing.T) {
	tests := []struct {
		name        string
		tags        map[string]string
		mockSetup   func(*MockECSClient)
		expected    []v1alpha1.CapacityReservation
		expectError bool
	}{
		{
			name: "successful retrieval with multiple results",
			tags: map[string]string{"env": "prod"},
			mockSetup: func(m *MockECSClient) {
				id1 := "cr-1"
				name1 := "cn-hangzhou-h"
				id2 := "cr-2"
				name2 := "cn-hangzhou-i"
				response := &ecs.DescribeCapacityReservationsResponse{
					Body: &ecs.DescribeCapacityReservationsResponseBody{
						CapacityReservationSet: &ecs.DescribeCapacityReservationsResponseBodyCapacityReservationSet{
							CapacityReservationItem: []*ecs.DescribeCapacityReservationsResponseBodyCapacityReservationSetCapacityReservationItem{
								{
									PrivatePoolOptionsId:   &id1,
									PrivatePoolOptionsName: &name1,
								},
								{
									PrivatePoolOptionsId:   &id2,
									PrivatePoolOptionsName: &name2,
								},
							},
						},
					},
				}
				m.On("DescribeCapacityReservations", mock.Anything, "", map[string]string{"env": "prod"}).Return(response, nil)
			},
			expected: []v1alpha1.CapacityReservation{
				{
					ID:   "cr-1",
					Name: "cn-hangzhou-h",
				},
				{
					ID:   "cr-2",
					Name: "cn-hangzhou-i",
				},
			},
		},
		{
			name: "API error",
			tags: map[string]string{"env": "test"},
			mockSetup: func(m *MockECSClient) {
				m.On("DescribeCapacityReservations", mock.Anything, "", map[string]string{"env": "test"}).Return(nil, errors.New("API error"))
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

func TestNewProvider(t *testing.T) {
	provider := NewProvider("cn-hangzhou", nil)

	assert.NotNil(t, provider)
	assert.Equal(t, "cn-hangzhou", provider.region)
}

// Note: The following tests are based on the assumption that the Provider type has methods
// that work with CapacityReservation from the API package. However, the current implementation
// doesn't seem to have these methods. These tests might need to be adjusted based on the actual implementation.

/*
func TestBuildPrivatePoolOptions(t *testing.T) {
	provider := NewProvider("cn-hangzhou", nil)

	tests := []struct {
		name         string
		preference   *string
		reservations []v1alpha1.CapacityReservation
		expectedKey  string
		expectedVal  string
	}{
		{
			name:         "open preference",
			preference:   lo.ToPtr("open"),
			reservations: nil,
			expectedKey:  "PrivatePoolOptions.MatchCriteria",
			expectedVal:  "Open",
		},
		{
			name:         "none preference",
			preference:   lo.ToPtr("none"),
			reservations: nil,
			expectedKey:  "PrivatePoolOptions.MatchCriteria",
			expectedVal:  "None",
		},
		{
			name:       "target reservation",
			preference: nil,
			reservations: []v1alpha1.CapacityReservation{
				{ID: "cr-test123"},
			},
			expectedKey: "PrivatePoolOptions.MatchCriteria",
			expectedVal: "Target",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Note: This method doesn't exist in the current implementation
			// options := provider.BuildPrivatePoolOptions(tt.preference, tt.reservations)
			// assert.NotNil(t, options)
			// assert.Equal(t, tt.expectedVal, options[tt.expectedKey])
		})
	}
}

func TestSelectBestReservation(t *testing.T) {
	provider := NewProvider("cn-hangzhou", nil)

	// Note: This test is based on methods that don't exist in the current implementation
	// These would need to be adjusted based on the actual implementation

	// reservations := []v1alpha1.CapacityReservation{
	// 	{
	// 		ID:                     "cr-1",
	// 		InstanceType:           "ecs.g6.large",
	// 		Name:                   "cn-hangzhou-a",
	// 		Status:                 "Active",
	// 		AvailableInstanceCount: 5,
	// 	},
	// 	// ... other reservations
	// }
	//
	// // Should select cr-2 (most available in Name-a for g6.large)
	// best := provider.SelectBestReservation(reservations, "ecs.g6.large", "cn-hangzhou-a")
	// assert.NotNil(t, best)
	// assert.Equal(t, "cr-2", best.ID)
	// assert.Equal(t, 10, best.AvailableInstanceCount)
}

func TestSelectBestReservation_NoMatch(t *testing.T) {
	provider := NewProvider("cn-hangzhou", nil)

	// Similar to above, these tests would need to be adjusted based on actual implementation
}

func TestSelectBestReservation_InactiveReservation(t *testing.T) {
	provider := NewProvider("cn-hangzhou", nil)

	// Similar to above, these tests would need to be adjusted based on actual implementation
}

func TestIsReservationAvailable(t *testing.T) {
	provider := NewProvider("cn-hangzhou", nil)

	// Similar to above, these tests would need to be adjusted based on actual implementation
}

func BenchmarkSelectBestReservation(b *testing.B) {
	provider := NewProvider("cn-hangzhou", nil)

	// Similar to above, these tests would need to be adjusted based on actual implementation
}
*/
