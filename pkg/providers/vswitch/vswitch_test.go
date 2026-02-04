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

package vswitch

import (
	"context"
	"errors"
	"testing"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	vpc "github.com/alibabacloud-go/vpc-20160428/v7/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockVPCClient is a mock implementation of VPCClient
type MockVPCClient struct {
	mock.Mock
}

func (m *MockVPCClient) DescribeVSwitches(ctx context.Context, vSwitchID string, tags map[string]string) (*vpc.DescribeVSwitchesResponse, error) {
	args := m.Called(ctx, vSwitchID, tags)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*vpc.DescribeVSwitchesResponse), args.Error(1)
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name          string
		selectorTerms []v1alpha1.VSwitchSelectorTerm
		mockSetup     func(*MockVPCClient)
		expected      []v1alpha1.VSwitch
		expectError   bool
	}{
		{
			name:          "empty selector terms",
			selectorTerms: []v1alpha1.VSwitchSelectorTerm{},
			expected:      []v1alpha1.VSwitch{},
		},
		{
			name: "resolve by ID - success",
			selectorTerms: []v1alpha1.VSwitchSelectorTerm{
				{
					ID: stringPtr("vsw-12345"),
				},
			},
			mockSetup: func(m *MockVPCClient) {
				vswID := "vsw-12345"
				zoneID := "cn-hangzhou-h"
				availIP := int64(100)
				response := &vpc.DescribeVSwitchesResponse{
					Body: &vpc.DescribeVSwitchesResponseBody{
						VSwitches: &vpc.DescribeVSwitchesResponseBodyVSwitches{
							VSwitch: []*vpc.DescribeVSwitchesResponseBodyVSwitchesVSwitch{
								{
									VSwitchId:               &vswID,
									ZoneId:                  &zoneID,
									AvailableIpAddressCount: &availIP,
								},
							},
						},
					},
				}
				m.On("DescribeVSwitches", mock.Anything, "vsw-12345", mock.Anything).Return(response, nil)
			},
			expected: []v1alpha1.VSwitch{
				{
					ID:                      "vsw-12345",
					Zone:                    "cn-hangzhou-h",
					ZoneID:                  "cn-hangzhou-h",
					AvailableIPAddressCount: 100,
				},
			},
		},
		{
			name: "resolve by tags - success",
			selectorTerms: []v1alpha1.VSwitchSelectorTerm{
				{
					Tags: map[string]string{
						"env": "prod",
					},
				},
			},
			mockSetup: func(m *MockVPCClient) {
				vswID1 := "vsw-tag-1"
				zoneID1 := "cn-hangzhou-h"
				vswID2 := "vsw-tag-2"
				zoneID2 := "cn-hangzhou-i"
				response := &vpc.DescribeVSwitchesResponse{
					Body: &vpc.DescribeVSwitchesResponseBody{
						VSwitches: &vpc.DescribeVSwitchesResponseBodyVSwitches{
							VSwitch: []*vpc.DescribeVSwitchesResponseBodyVSwitchesVSwitch{
								{
									VSwitchId: &vswID1,
									ZoneId:    &zoneID1,
								},
								{
									VSwitchId: &vswID2,
									ZoneId:    &zoneID2,
								},
							},
						},
					},
				}
				m.On("DescribeVSwitches", mock.Anything, "", map[string]string{"env": "prod"}).Return(response, nil)
			},
			expected: []v1alpha1.VSwitch{
				{
					ID:   "vsw-tag-1",
					Zone: "cn-hangzhou-h",
				},
				{
					ID:   "vsw-tag-2",
					Zone: "cn-hangzhou-i",
				},
			},
		},
		{
			name: "resolve by ID - API error",
			selectorTerms: []v1alpha1.VSwitchSelectorTerm{
				{
					ID: stringPtr("vsw-error"),
				},
			},
			mockSetup: func(m *MockVPCClient) {
				m.On("DescribeVSwitches", mock.Anything, "vsw-error", mock.Anything).Return(nil, errors.New("API error"))
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockVPCClient)
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
		mockSetup   func(*MockVPCClient)
		expected    *v1alpha1.VSwitch
		expectError bool
	}{
		{
			name: "successful retrieval",
			id:   "vsw-12345",
			mockSetup: func(m *MockVPCClient) {
				vswID := "vsw-12345"
				zoneID := "cn-hangzhou-h"
				availIP := int64(100)
				response := &vpc.DescribeVSwitchesResponse{
					Body: &vpc.DescribeVSwitchesResponseBody{
						VSwitches: &vpc.DescribeVSwitchesResponseBodyVSwitches{
							VSwitch: []*vpc.DescribeVSwitchesResponseBodyVSwitchesVSwitch{
								{
									VSwitchId:               &vswID,
									ZoneId:                  &zoneID,
									AvailableIpAddressCount: &availIP,
								},
							},
						},
					},
				}
				m.On("DescribeVSwitches", mock.Anything, "vsw-12345", mock.Anything).Return(response, nil)
			},
			expected: &v1alpha1.VSwitch{
				ID:                      "vsw-12345",
				Zone:                    "cn-hangzhou-h",
				ZoneID:                  "cn-hangzhou-h",
				AvailableIPAddressCount: 100,
			},
		},
		{
			name: "API error",
			id:   "vsw-error",
			mockSetup: func(m *MockVPCClient) {
				m.On("DescribeVSwitches", mock.Anything, "vsw-error", mock.Anything).Return(nil, errors.New("API error"))
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockVPCClient)
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
		mockSetup   func(*MockVPCClient)
		expected    []v1alpha1.VSwitch
		expectError bool
	}{
		{
			name: "successful retrieval with multiple results",
			tags: map[string]string{"env": "prod"},
			mockSetup: func(m *MockVPCClient) {
				vswID1 := "vsw-1"
				zoneID1 := "cn-hangzhou-h"
				vswID2 := "vsw-2"
				zoneID2 := "cn-hangzhou-i"
				response := &vpc.DescribeVSwitchesResponse{
					Body: &vpc.DescribeVSwitchesResponseBody{
						VSwitches: &vpc.DescribeVSwitchesResponseBodyVSwitches{
							VSwitch: []*vpc.DescribeVSwitchesResponseBodyVSwitchesVSwitch{
								{
									VSwitchId: &vswID1,
									ZoneId:    &zoneID1,
								},
								{
									VSwitchId: &vswID2,
									ZoneId:    &zoneID2,
								},
							},
						},
					},
				}
				m.On("DescribeVSwitches", mock.Anything, "", map[string]string{"env": "prod"}).Return(response, nil)
			},
			expected: []v1alpha1.VSwitch{
				{
					ID:   "vsw-1",
					Zone: "cn-hangzhou-h",
				},
				{
					ID:   "vsw-2",
					Zone: "cn-hangzhou-i",
				},
			},
		},
		{
			name: "API error",
			tags: map[string]string{"env": "test"},
			mockSetup: func(m *MockVPCClient) {
				m.On("DescribeVSwitches", mock.Anything, "", map[string]string{"env": "test"}).Return(nil, errors.New("API error"))
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockVPCClient)
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

func TestRemoveDuplicateVSwitches(t *testing.T) {
	vswitches := []v1alpha1.VSwitch{
		{ID: "vsw-1", Zone: "cn-hangzhou-h"},
		{ID: "vsw-2", Zone: "cn-hangzhou-i"},
		{ID: "vsw-1", Zone: "cn-hangzhou-h"}, // duplicate
	}

	result := removeDuplicateVSwitches(vswitches)
	assert.Len(t, result, 2)
}

func TestClearCache(t *testing.T) {
	provider := NewProvider("cn-hangzhou", new(MockVPCClient))

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
