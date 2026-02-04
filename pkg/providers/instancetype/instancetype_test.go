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

package instancetype

import (
	"context"
	"errors"
	"testing"

	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"k8s.io/apimachinery/pkg/api/resource"
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

func (m *MockECSClient) DescribeInstanceTypes(ctx context.Context, instanceTypes []string) (*ecs.DescribeInstanceTypesResponse, error) {
	args := m.Called(ctx, instanceTypes)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ecs.DescribeInstanceTypesResponse), args.Error(1)
}

func (m *MockECSClient) DescribeZones(ctx context.Context) (*ecs.DescribeZonesResponse, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ecs.DescribeZonesResponse), args.Error(1)
}

func TestList(t *testing.T) {
	tests := []struct {
		name        string
		mockSetup   func(*MockECSClient)
		expectedLen int
		expectError bool
	}{
		{
			name: "successful list with multiple instance types",
			mockSetup: func(m *MockECSClient) {
				zoneId1 := "cn-hangzhou-h"
				zoneId2 := "cn-hangzhou-i"
				zonesResponse := &ecs.DescribeZonesResponse{
					Body: &ecs.DescribeZonesResponseBody{
						Zones: &ecs.DescribeZonesResponseBodyZones{
							Zone: []*ecs.DescribeZonesResponseBodyZonesZone{
								{ZoneId: &zoneId1},
								{ZoneId: &zoneId2},
							},
						},
					},
				}
				m.On("DescribeZones", mock.Anything).Return(zonesResponse, nil)

				instanceTypeId1 := "ecs.g6.large"
				cpuCoreCount1 := int32(2)
				memorySize1 := float32(8.0)
				instanceTypeId2 := "ecs.c6.xlarge"
				cpuCoreCount2 := int32(4)
				memorySize2 := float32(8.0)
				instanceTypesResponse := &ecs.DescribeInstanceTypesResponse{
					Body: &ecs.DescribeInstanceTypesResponseBody{
						InstanceTypes: &ecs.DescribeInstanceTypesResponseBodyInstanceTypes{
							InstanceType: []*ecs.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType{
								{
									InstanceTypeId: &instanceTypeId1,
									CpuCoreCount:   &cpuCoreCount1,
									MemorySize:     &memorySize1,
								},
								{
									InstanceTypeId: &instanceTypeId2,
									CpuCoreCount:   &cpuCoreCount2,
									MemorySize:     &memorySize2,
								},
							},
						},
					},
				}
				m.On("DescribeInstanceTypes", mock.Anything, mock.Anything).Return(instanceTypesResponse, nil)
			},
			expectedLen: 2,
		},
		{
			name: "zones API error",
			mockSetup: func(m *MockECSClient) {
				m.On("DescribeZones", mock.Anything).Return((*ecs.DescribeZonesResponse)(nil), errors.New("API error"))
			},
			expectError: true,
		},
		{
			name: "instance types API error",
			mockSetup: func(m *MockECSClient) {
				zoneId := "cn-hangzhou-h"
				zonesResponse := &ecs.DescribeZonesResponse{
					Body: &ecs.DescribeZonesResponseBody{
						Zones: &ecs.DescribeZonesResponseBodyZones{
							Zone: []*ecs.DescribeZonesResponseBodyZonesZone{
								{ZoneId: &zoneId},
							},
						},
					},
				}
				m.On("DescribeZones", mock.Anything).Return(zonesResponse, nil)
				m.On("DescribeInstanceTypes", mock.Anything, mock.Anything).Return((*ecs.DescribeInstanceTypesResponse)(nil), errors.New("API error"))
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockECSClient)
			tt.mockSetup(mockClient)

			provider := NewProvider("cn-hangzhou", mockClient)
			result, err := provider.List(context.Background())

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

func TestGet(t *testing.T) {
	tests := []struct {
		name             string
		instanceTypeName string
		mockSetup        func(*MockECSClient)
		expectedName     string
		expectError      bool
	}{
		{
			name:             "successful get",
			instanceTypeName: "ecs.g6.large",
			mockSetup: func(m *MockECSClient) {
				zoneId := "cn-hangzhou-h"
				zonesResponse := &ecs.DescribeZonesResponse{
					Body: &ecs.DescribeZonesResponseBody{
						Zones: &ecs.DescribeZonesResponseBodyZones{
							Zone: []*ecs.DescribeZonesResponseBodyZonesZone{
								{ZoneId: &zoneId},
							},
						},
					},
				}
				m.On("DescribeZones", mock.Anything).Return(zonesResponse, nil)

				instanceTypeId := "ecs.g6.large"
				cpuCoreCount := int32(2)
				memorySize := float32(8.0)
				instanceTypesResponse := &ecs.DescribeInstanceTypesResponse{
					Body: &ecs.DescribeInstanceTypesResponseBody{
						InstanceTypes: &ecs.DescribeInstanceTypesResponseBodyInstanceTypes{
							InstanceType: []*ecs.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType{
								{
									InstanceTypeId: &instanceTypeId,
									CpuCoreCount:   &cpuCoreCount,
									MemorySize:     &memorySize,
								},
							},
						},
					},
				}
				m.On("DescribeInstanceTypes", mock.Anything, []string{"ecs.g6.large"}).Return(instanceTypesResponse, nil)
			},
			expectedName: "ecs.g6.large",
		},
		{
			name:             "instance type not found",
			instanceTypeName: "ecs.notfound.large",
			mockSetup: func(m *MockECSClient) {
				zoneId := "cn-hangzhou-h"
				zonesResponse := &ecs.DescribeZonesResponse{
					Body: &ecs.DescribeZonesResponseBody{
						Zones: &ecs.DescribeZonesResponseBodyZones{
							Zone: []*ecs.DescribeZonesResponseBodyZonesZone{
								{ZoneId: &zoneId},
							},
						},
					},
				}
				m.On("DescribeZones", mock.Anything).Return(zonesResponse, nil)

				instanceTypesResponse := &ecs.DescribeInstanceTypesResponse{
					Body: &ecs.DescribeInstanceTypesResponseBody{
						InstanceTypes: &ecs.DescribeInstanceTypesResponseBodyInstanceTypes{
							InstanceType: []*ecs.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType{},
						},
					},
				}
				m.On("DescribeInstanceTypes", mock.Anything, []string{"ecs.notfound.large"}).Return(instanceTypesResponse, nil)
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockECSClient)
			tt.mockSetup(mockClient)

			provider := NewProvider("cn-hangzhou", mockClient)
			result, err := provider.Get(context.Background(), tt.instanceTypeName)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedName, result.Name)
			}

			mockClient.AssertExpectations(t)
		})
	}
}

func TestFilter(t *testing.T) {
	instanceTypes := []*InstanceType{
		{
			Name:         "ecs.g6.large",
			Architecture: "X86",
			CPU:          resource.NewQuantity(2, resource.DecimalSI),
			Memory:       resource.NewQuantity(8*1024*1024*1024, resource.BinarySI),
			Zones: map[string]ZoneInfo{
				"cn-hangzhou-h": {Available: true},
			},
		},
		{
			Name:         "ecs.c6.xlarge",
			Architecture: "X86",
			CPU:          resource.NewQuantity(4, resource.DecimalSI),
			Memory:       resource.NewQuantity(8*1024*1024*1024, resource.BinarySI),
			Zones: map[string]ZoneInfo{
				"cn-hangzhou-i": {Available: true},
			},
		},
	}

	tests := []struct {
		name         string
		requirements []InstanceTypeRequirement
		expectedLen  int
	}{
		{
			name: "filter by instance type",
			requirements: []InstanceTypeRequirement{
				{
					Key:      "node.kubernetes.io/instance-type",
					Operator: InstanceTypeOperatorIn,
					Values:   []string{"ecs.g6.large"},
				},
			},
			expectedLen: 1,
		},
		{
			name: "filter by zone",
			requirements: []InstanceTypeRequirement{
				{
					Key:      "topology.kubernetes.io/zone",
					Operator: InstanceTypeOperatorIn,
					Values:   []string{"cn-hangzhou-h"},
				},
			},
			expectedLen: 1,
		},
		{
			name:         "no filter",
			requirements: []InstanceTypeRequirement{},
			expectedLen:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := NewProvider("cn-hangzhou", new(MockECSClient))
			result := provider.Filter(context.Background(), instanceTypes, tt.requirements)
			assert.Len(t, result, tt.expectedLen)
		})
	}
}

func TestSort(t *testing.T) {
	instanceTypes := []*InstanceType{
		{
			Name:   "ecs.c6.xlarge",
			CPU:    resource.NewQuantity(4, resource.DecimalSI),
			Memory: resource.NewQuantity(8*1024*1024*1024, resource.BinarySI),
		},
		{
			Name:   "ecs.g6.large",
			CPU:    resource.NewQuantity(2, resource.DecimalSI),
			Memory: resource.NewQuantity(8*1024*1024*1024, resource.BinarySI),
		},
	}

	provider := NewProvider("cn-hangzhou", new(MockECSClient))
	result := provider.Sort(instanceTypes)

	assert.Len(t, result, 2)
	assert.Equal(t, "ecs.g6.large", result[0].Name)
	assert.Equal(t, "ecs.c6.xlarge", result[1].Name)
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

func TestCalculateGPUMemory(t *testing.T) {
	tests := []struct {
		name            string
		ecsInstanceType *ecs.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType
		expectedMemory  int64 // in GiB
		expectNil       bool
	}{
		{
			name: "no GPU",
			ecsInstanceType: func() *ecs.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType {
				gpuAmount := int32(0)
				return &ecs.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType{
					GPUAmount: &gpuAmount,
				}
			}(),
			expectNil: true,
		},
		{
			name: "GPU instance from GPUInstanceTypes map (gn7e with single H100)",
			ecsInstanceType: func() *ecs.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType {
				instanceTypeId := "ecs.gn7e-c16g1.4xlarge"
				instanceTypeFamily := "ecs.gn7e"
				gpuAmount := int32(1)
				gpuSpec := "H100"
				return &ecs.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType{
					InstanceTypeId:     &instanceTypeId,
					InstanceTypeFamily: &instanceTypeFamily,
					GPUAmount:          &gpuAmount,
					GPUSpec:            &gpuSpec,
				}
			}(),
			// 81251 MiB per GPU -> floor(81251/1024) = 79 GiB
			expectedMemory: 79,
		},
		{
			name: "GPU instance from GPUInstanceTypeFamily map (gn6i with T4)",
			ecsInstanceType: func() *ecs.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType {
				instanceTypeId := "ecs.gn6i-c4g1.xlarge"
				instanceTypeFamily := "ecs.gn6i"
				gpuAmount := int32(1)
				gpuSpec := "T4"
				return &ecs.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType{
					InstanceTypeId:     &instanceTypeId,
					InstanceTypeFamily: &instanceTypeFamily,
					GPUAmount:          &gpuAmount,
					GPUSpec:            &gpuSpec,
				}
			}(),
			// 15109 MiB per GPU * 1 GPU -> floor(15109/1024) = 14 GiB
			expectedMemory: 14,
		},
		{
			name: "GPU instance with multiple GPUs from GPUInstanceTypeFamily (gn7 with 4x A100)",
			ecsInstanceType: func() *ecs.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType {
				instanceTypeId := "ecs.gn7-c13g1.13xlarge"
				instanceTypeFamily := "ecs.gn7"
				gpuAmount := int32(4)
				gpuSpec := "A100"
				return &ecs.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType{
					InstanceTypeId:     &instanceTypeId,
					InstanceTypeFamily: &instanceTypeFamily,
					GPUAmount:          &gpuAmount,
					GPUSpec:            &gpuSpec,
				}
			}(),
			// 40537 MiB per GPU * 4 GPUs = 162148 MiB -> floor(162148/1024) = 158 GiB
			expectedMemory: 158,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateGPUMemory(tt.ecsInstanceType)

			if tt.expectNil {
				assert.Nil(t, result)
			} else {
				assert.NotNil(t, result)
				assert.Equal(t, tt.expectedMemory, result.Value())
			}
		})
	}
}

func TestConvertECSInstanceTypeWithGPU(t *testing.T) {
	tests := []struct {
		name            string
		ecsInstanceType *ecs.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType
		expectGPU       bool
		expectedGPUMem  int64 // in GiB
	}{
		{
			name: "instance without GPU",
			ecsInstanceType: func() *ecs.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType {
				instanceTypeId := "ecs.g6.large"
				cpuCoreCount := int32(2)
				memorySize := float32(8.0)
				gpuAmount := int32(0)
				return &ecs.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType{
					InstanceTypeId: &instanceTypeId,
					CpuCoreCount:   &cpuCoreCount,
					MemorySize:     &memorySize,
					GPUAmount:      &gpuAmount,
				}
			}(),
			expectGPU: false,
		},
		{
			name: "GPU instance with gn6i (T4)",
			ecsInstanceType: func() *ecs.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType {
				instanceTypeId := "ecs.gn6i-c4g1.xlarge"
				instanceTypeFamily := "ecs.gn6i"
				cpuCoreCount := int32(4)
				memorySize := float32(16.0)
				gpuAmount := int32(1)
				gpuSpec := "Tesla T4"
				return &ecs.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType{
					InstanceTypeId:     &instanceTypeId,
					InstanceTypeFamily: &instanceTypeFamily,
					CpuCoreCount:       &cpuCoreCount,
					MemorySize:         &memorySize,
					GPUAmount:          &gpuAmount,
					GPUSpec:            &gpuSpec,
				}
			}(),
			expectGPU:      true,
			expectedGPUMem: 14, // 15109 MiB -> floor(15109/1024) = 14 GiB
		},
		{
			name: "GPU instance with multiple A100s (gn7)",
			ecsInstanceType: func() *ecs.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType {
				instanceTypeId := "ecs.gn7-c13g1.13xlarge"
				instanceTypeFamily := "ecs.gn7"
				cpuCoreCount := int32(48)
				memorySize := float32(192.0)
				gpuAmount := int32(4)
				gpuSpec := "Tesla A100"
				return &ecs.DescribeInstanceTypesResponseBodyInstanceTypesInstanceType{
					InstanceTypeId:     &instanceTypeId,
					InstanceTypeFamily: &instanceTypeFamily,
					CpuCoreCount:       &cpuCoreCount,
					MemorySize:         &memorySize,
					GPUAmount:          &gpuAmount,
					GPUSpec:            &gpuSpec,
				}
			}(),
			expectGPU:      true,
			expectedGPUMem: 158, // 40537 * 4 = 162148 MiB -> floor(162148/1024) = 158 GiB
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := NewProvider("cn-hangzhou", new(MockECSClient))
			zones := map[string]ZoneInfo{
				"cn-hangzhou-h": {Available: true},
			}

			result := provider.convertECSInstanceType(tt.ecsInstanceType, zones)

			assert.Equal(t, *tt.ecsInstanceType.InstanceTypeId, result.Name)
			assert.NotNil(t, result.CPU)
			assert.NotNil(t, result.Memory)

			if tt.expectGPU {
				assert.NotNil(t, result.GPU)
				assert.NotNil(t, result.GPU.Memory)
				assert.Equal(t, int64(*tt.ecsInstanceType.GPUAmount), result.GPU.Count.Value())
				assert.Equal(t, tt.expectedGPUMem, result.GPU.Memory.Value())
				assert.Equal(t, *tt.ecsInstanceType.GPUSpec, result.GPU.Model)
			} else {
				assert.Nil(t, result.GPU)
			}
		})
	}
}
