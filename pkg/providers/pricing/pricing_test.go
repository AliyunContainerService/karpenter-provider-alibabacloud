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

package pricing

import (
	"context"
	"testing"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
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

func (m *MockECSClient) DescribeImages(ctx context.Context, imageIDs []string, filters map[string]string) ([]ecs.Image, error) {
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
	args := m.Called(ctx, instanceType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ecs.DescribePriceResponse), args.Error(1)
}

func TestGetPricing(t *testing.T) {
	tests := []struct {
		name        string
		cacheAge    time.Duration
		expectedLen int
	}{
		{
			name:        "returns cached pricing when cache is valid",
			cacheAge:    30 * time.Minute,
			expectedLen: 4,
		},
		{
			name:        "refreshes pricing when cache is expired",
			cacheAge:    2 * time.Hour,
			expectedLen: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockECSClient)
			provider := NewProvider("cn-hangzhou", mockClient)

			// Setup cache
			provider.cache = map[string]float64{
				"ecs.g6.large":  0.12,
				"ecs.g6.xlarge": 0.24,
				"ecs.c6.large":  0.09,
				"ecs.c6.xlarge": 0.18,
			}
			provider.lastUpdate = time.Now().Add(-tt.cacheAge)

			result, err := provider.GetPricing(context.Background())

			assert.NoError(t, err)
			assert.Len(t, result, tt.expectedLen)
		})
	}
}

func TestRefreshPricing(t *testing.T) {
	tests := []struct {
		name        string
		expectedLen int
	}{
		{
			name:        "refreshes pricing successfully",
			expectedLen: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockECSClient)
			provider := NewProvider("cn-hangzhou", mockClient)

			result, err := provider.refreshPricing(context.Background())

			assert.NoError(t, err)
			assert.Len(t, result, tt.expectedLen)
			assert.NotZero(t, provider.lastUpdate)
		})
	}
}

func TestGetInstanceTypePrice(t *testing.T) {
	tests := []struct {
		name         string
		instanceType string
		expected     float64
	}{
		{
			name:         "returns price for existing instance type",
			instanceType: "ecs.g6.large",
			expected:     0.12,
		},
		{
			name:         "returns 0 for non-existing instance type",
			instanceType: "ecs.unknown.type",
			expected:     0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockECSClient)
			provider := NewProvider("cn-hangzhou", mockClient)

			// Setup cache
			provider.cache = map[string]float64{
				"ecs.g6.large":  0.12,
				"ecs.g6.xlarge": 0.24,
				"ecs.c6.large":  0.09,
				"ecs.c6.xlarge": 0.18,
			}
			provider.lastUpdate = time.Now()

			result, err := provider.GetInstanceTypePrice(context.Background(), tt.instanceType)

			assert.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetSpotPricing(t *testing.T) {
	tests := []struct {
		name        string
		expectedLen int
	}{
		{
			name:        "returns spot pricing with discount",
			expectedLen: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockECSClient)
			provider := NewProvider("cn-hangzhou", mockClient)

			// Setup cache
			provider.cache = map[string]float64{
				"ecs.g6.large":  0.12,
				"ecs.g6.xlarge": 0.24,
				"ecs.c6.large":  0.09,
				"ecs.c6.xlarge": 0.18,
			}
			provider.lastUpdate = time.Now()

			result, err := provider.GetSpotPricing(context.Background())

			assert.NoError(t, err)
			assert.Len(t, result, tt.expectedLen)

			// Verify discount is applied (spot price should be 80% of regular price)
			for instanceType, spotPrice := range result {
				regularPrice := provider.cache[instanceType]
				assert.Equal(t, regularPrice*0.8, spotPrice)
			}
		})
	}
}
