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

package bootstrap

import (
	"context"
	"errors"
	"testing"
	"time"

	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
)

// MockCSClient is a mock implementation of CSClient
type MockCSClient struct {
	mock.Mock
}

func (m *MockCSClient) GetClusterAddonInstance(ctx context.Context, clusterID, addonName string) (*cs.GetClusterAddonInstanceResponse, error) {
	args := m.Called(ctx, clusterID, addonName)
	return args.Get(0).(*cs.GetClusterAddonInstanceResponse), args.Error(1)
}

func (m *MockCSClient) DescribeClusterDetail(ctx context.Context, clusterID string) (*cs.DescribeClusterDetailResponse, error) {
	args := m.Called(ctx, clusterID)
	return args.Get(0).(*cs.DescribeClusterDetailResponse), args.Error(1)
}

func (m *MockCSClient) DescribeClusterAttachScripts(ctx context.Context, clusterID string, request *cs.DescribeClusterAttachScriptsRequest) (string, error) {
	args := m.Called(ctx, clusterID, request)
	return args.String(0), args.Error(1)
}

func TestGenerateUserData(t *testing.T) {
	customData := "#!/bin/bash\necho custom"

	tests := []struct {
		name        string
		opts        BootstrapOptions
		mockSetup   func(*MockCSClient)
		expectError bool
		contains    []string
	}{
		{
			name: "custom user data",
			opts: BootstrapOptions{
				ClusterType:    ACKClusterType,
				CustomUserData: &customData,
			},
			contains: []string{"#!/bin/bash", "echo custom"},
		},
		{
			name: "ACK cluster type",
			opts: BootstrapOptions{
				ClusterType: ACKClusterType,
				ClusterID:   "c-test123",
			},
			mockSetup: func(m *MockCSClient) {
				script := "curl -sSL http://aliacs-k8s.oss.aliyuncs.com/public/pkg/run/attach/1.12.6-aliyunedge.1/attach.sh | bash"
				m.On("DescribeClusterAttachScripts", mock.Anything, "c-test123", mock.Anything).Return(script, nil)
			},
			contains: []string{"#!/bin/bash", "set -ex", "attach.sh"},
		},
		{
			name: "self-managed cluster type",
			opts: BootstrapOptions{
				ClusterType:     SelfManagedClusterType,
				ClusterEndpoint: "https://192.168.1.1:6443",
				NodeName:        "test-node",
			},
			contains: []string{"#!/bin/bash", "set -ex", "kubeadm join"},
		},
		{
			name: "ACK cluster - missing cluster ID",
			opts: BootstrapOptions{
				ClusterType: ACKClusterType,
			},
			expectError: true,
		},
		{
			name: "self-managed cluster - missing endpoint",
			opts: BootstrapOptions{
				ClusterType: SelfManagedClusterType,
			},
			expectError: true,
		},
		{
			name: "unsupported cluster type",
			opts: BootstrapOptions{
				ClusterType: ClusterType("unsupported"),
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockCSClient)
			if tt.mockSetup != nil {
				tt.mockSetup(mockClient)
			}

			provider := NewProvider("cn-hangzhou", mockClient)
			result, err := provider.GenerateUserData(tt.opts)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				for _, substr := range tt.contains {
					assert.Contains(t, result, substr)
				}
			}

			if tt.mockSetup != nil {
				mockClient.AssertExpectations(t)
			}
		})
	}
}

func TestGetACKBootstrapScriptByCluster(t *testing.T) {
	tests := []struct {
		name        string
		opts        BootstrapOptions
		mockSetup   func(*MockCSClient)
		expectError bool
		expected    string
	}{
		{
			name: "successful retrieval",
			opts: BootstrapOptions{
				ClusterID: "c-abc123",
			},
			mockSetup: func(m *MockCSClient) {
				script := "curl -sSL http://test.sh | bash"
				m.On("DescribeClusterAttachScripts", mock.Anything, "c-abc123", mock.Anything).Return(script, nil)
			},
			expected: "curl -sSL http://test.sh | bash",
		},
		{
			name: "missing cluster ID",
			opts: BootstrapOptions{
				ClusterID: "",
			},
			expectError: true,
		},
		{
			name: "API error",
			opts: BootstrapOptions{
				ClusterID: "c-error",
			},
			mockSetup: func(m *MockCSClient) {
				m.On("DescribeClusterAttachScripts", mock.Anything, "c-error", mock.Anything).Return("", errors.New("API error"))
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockCSClient)
			if tt.mockSetup != nil {
				tt.mockSetup(mockClient)
			}

			provider := NewProvider("cn-hangzhou", mockClient)
			result, err := provider.getACKBootstrapScriptByCluster(tt.opts)

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

func TestGenerateACKBootstrapScript(t *testing.T) {
	tests := []struct {
		name        string
		opts        BootstrapOptions
		mockSetup   func(*MockCSClient)
		expectError bool
		contains    []string
	}{
		{
			name: "successful generation",
			opts: BootstrapOptions{
				ClusterID: "c-abc123",
			},
			mockSetup: func(m *MockCSClient) {
				script := "echo test"
				m.On("DescribeClusterAttachScripts", mock.Anything, "c-abc123", mock.Anything).Return(script, nil)
			},
			contains: []string{"#!/bin/bash", "set -ex", "echo test", "--taints"},
		},
		{
			name: "script with existing header",
			opts: BootstrapOptions{
				ClusterID: "c-abc123",
			},
			mockSetup: func(m *MockCSClient) {
				script := "#!/bin/bash\necho test"
				m.On("DescribeClusterAttachScripts", mock.Anything, "c-abc123", mock.Anything).Return(script, nil)
			},
			contains: []string{"#!/bin/bash", "set -ex", "echo test"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockCSClient)
			if tt.mockSetup != nil {
				tt.mockSetup(mockClient)
			}

			provider := NewProvider("cn-hangzhou", mockClient)
			result, err := provider.generateACKBootstrapScript(tt.opts)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				for _, substr := range tt.contains {
					assert.Contains(t, result, substr)
				}
			}

			if tt.mockSetup != nil {
				mockClient.AssertExpectations(t)
			}
		})
	}
}

func TestGenerateSelfManagedBootstrapScript(t *testing.T) {
	maxPods := int32(110)

	tests := []struct {
		name        string
		opts        BootstrapOptions
		expectError bool
		contains    []string
	}{
		{
			name: "basic self-managed",
			opts: BootstrapOptions{
				ClusterEndpoint: "https://192.168.1.1:6443",
				NodeName:        "test-node",
			},
			contains: []string{"#!/bin/bash", "kubeadm join", "192.168.1.1:6443", "test-node"},
		},
		{
			name: "with kubelet config",
			opts: BootstrapOptions{
				ClusterEndpoint: "https://192.168.1.1:6443",
				NodeName:        "test-node",
				KubeletConfig: &KubeletConfiguration{
					MaxPods: &maxPods,
				},
			},
			contains: []string{"maxPods"},
		},
		{
			name: "with labels and taints",
			opts: BootstrapOptions{
				ClusterEndpoint: "https://192.168.1.1:6443",
				NodeName:        "test-node",
				Labels: map[string]string{
					"env": "prod",
				},
				Taints: []corev1.Taint{
					{Key: "test", Value: "true", Effect: corev1.TaintEffectNoSchedule},
				},
			},
			contains: []string{"node-labels", "register-with-taints"},
		},
		{
			name: "missing cluster endpoint",
			opts: BootstrapOptions{
				ClusterEndpoint: "",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockCSClient)
			provider := NewProvider("cn-hangzhou", mockClient)
			result, err := provider.generateSelfManagedBootstrapScript(tt.opts)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				for _, substr := range tt.contains {
					assert.Contains(t, result, substr)
				}
			}
		})
	}
}

func TestEnsureScriptHeader(t *testing.T) {
	tests := []struct {
		name     string
		script   string
		contains []string
	}{
		{
			name:     "script without header",
			script:   "echo test",
			contains: []string{"#!/bin/bash", "set -ex", "echo test", "--taints"},
		},
		{
			name:     "script with header but no set -ex",
			script:   "#!/bin/bash\necho test",
			contains: []string{"#!/bin/bash", "set -ex", "echo test"},
		},
		{
			name:     "script with header and set -ex",
			script:   "#!/bin/bash\nset -ex\necho test",
			contains: []string{"#!/bin/bash", "set -ex", "echo test"},
		},
		{
			name:     "empty script",
			script:   "",
			contains: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ensureScriptHeader(tt.script)
			for _, substr := range tt.contains {
				assert.Contains(t, result, substr)
			}
		})
	}
}

func TestConvertTaints2String(t *testing.T) {
	tests := []struct {
		name     string
		taints   []corev1.Taint
		expected string
	}{
		{
			name:     "empty taints",
			taints:   []corev1.Taint{},
			expected: "",
		},
		{
			name: "single taint",
			taints: []corev1.Taint{
				{Key: "key1", Value: "value1", Effect: corev1.TaintEffectNoSchedule},
			},
			expected: "key1=value1:NoSchedule",
		},
		{
			name: "multiple taints",
			taints: []corev1.Taint{
				{Key: "key1", Value: "value1", Effect: corev1.TaintEffectNoSchedule},
				{Key: "key2", Value: "value2", Effect: corev1.TaintEffectNoExecute},
			},
			expected: "key1=value1:NoSchedule,key2=value2:NoExecute",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertTaints2String(tt.taints)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildNodeLabels(t *testing.T) {
	tests := []struct {
		name     string
		opts     BootstrapOptions
		expected string
	}{
		{
			name:     "no labels",
			opts:     BootstrapOptions{},
			expected: "",
		},
		{
			name: "single label",
			opts: BootstrapOptions{
				Labels: map[string]string{"env": "prod"},
			},
			expected: "env=prod",
		},
		{
			name: "multiple labels",
			opts: BootstrapOptions{
				Labels: map[string]string{
					"env":  "prod",
					"tier": "frontend",
				},
			},
			expected: "env=prod,tier=frontend",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockCSClient)
			provider := NewProvider("cn-hangzhou", mockClient)
			result := provider.buildNodeLabels(tt.opts)

			if tt.expected == "" {
				assert.Empty(t, result)
			} else if len(tt.opts.Labels) == 1 {
				assert.Equal(t, tt.expected, result)
			} else {
				// For multiple labels, just check it contains all labels
				for k, v := range tt.opts.Labels {
					assert.Contains(t, result, k+"="+v)
				}
			}
		})
	}
}

func TestBuildNodeTaints(t *testing.T) {
	tests := []struct {
		name     string
		opts     BootstrapOptions
		expected string
	}{
		{
			name:     "no taints",
			opts:     BootstrapOptions{},
			expected: "",
		},
		{
			name: "single taint",
			opts: BootstrapOptions{
				Taints: []corev1.Taint{
					{Key: "key1", Value: "value1", Effect: corev1.TaintEffectNoSchedule},
				},
			},
			expected: "key1=value1:NoSchedule",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockCSClient)
			provider := NewProvider("cn-hangzhou", mockClient)
			result := provider.buildNodeTaints(tt.opts)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCacheOperations(t *testing.T) {
	mockClient := new(MockCSClient)
	provider := NewProvider("cn-hangzhou", mockClient)

	// Test setCachedValue and getCachedValue
	provider.setCachedValue("test-key", "test-value")
	value, exists := provider.getCachedValue("test-key")
	assert.True(t, exists)
	assert.Equal(t, "test-value", value)

	// Test cache expiration
	provider.SetCacheTTL(1 * time.Millisecond)
	provider.setCachedValue("expiring-key", "expiring-value")
	time.Sleep(10 * time.Millisecond)
	_, exists = provider.getCachedValue("expiring-key")
	assert.False(t, exists)

	// Test ClearCache
	provider.setCachedValue("clear-test", "value")
	provider.ClearCache()
	_, exists = provider.getCachedValue("clear-test")
	assert.False(t, exists)
}

func TestFormatLabels(t *testing.T) {
	tests := []struct {
		name     string
		labels   map[string]string
		expected string
	}{
		{
			name:     "empty labels",
			labels:   map[string]string{},
			expected: "",
		},
		{
			name:     "single label",
			labels:   map[string]string{"env": "prod"},
			expected: "env=prod",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatLabels(tt.labels)
			if tt.expected == "" {
				assert.Empty(t, result)
			} else {
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestFormatTaints(t *testing.T) {
	tests := []struct {
		name     string
		taints   []corev1.Taint
		expected string
	}{
		{
			name:     "empty taints",
			taints:   []corev1.Taint{},
			expected: "",
		},
		{
			name: "single taint",
			taints: []corev1.Taint{
				{Key: "key1", Value: "value1", Effect: corev1.TaintEffectNoSchedule},
			},
			expected: "key1=value1:NoSchedule",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatTaints(tt.taints)
			assert.Equal(t, tt.expected, result)
		})
	}
}
