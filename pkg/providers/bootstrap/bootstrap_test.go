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
	"fmt"
	"strings"
	"testing"
	"time"

	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	"github.com/alibabacloud-go/tea/tea"
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

func (m *MockCSClient) DescribeKubernetesVersionMetadata(ctx context.Context, k8sVersion string) ([]*cs.DescribeKubernetesVersionMetadataResponseBody, error) {
	args := m.Called(ctx, k8sVersion)
	return args.Get(0).([]*cs.DescribeKubernetesVersionMetadataResponseBody), args.Error(1)
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
				// patchRuntimeVersion is non-blocking; return error to exercise fallback path
				m.On("DescribeClusterDetail", mock.Anything, "c-test123").Maybe().Return(&cs.DescribeClusterDetailResponse{}, fmt.Errorf("skip patch"))
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
			result, err := provider.GenerateUserData(context.Background(), tt.opts)

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
				// patchRuntimeVersion is non-blocking; return error to exercise fallback path
				m.On("DescribeClusterDetail", mock.Anything, "c-abc123").Maybe().Return(&cs.DescribeClusterDetailResponse{}, fmt.Errorf("skip patch"))
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
			result, err := provider.getACKBootstrapScriptByCluster(context.Background(), tt.opts)

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
				m.On("DescribeClusterDetail", mock.Anything, "c-abc123").Maybe().Return(&cs.DescribeClusterDetailResponse{}, fmt.Errorf("skip patch"))
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
				m.On("DescribeClusterDetail", mock.Anything, "c-abc123").Maybe().Return(&cs.DescribeClusterDetailResponse{}, fmt.Errorf("skip patch"))
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
			result, err := provider.generateACKBootstrapScript(context.Background(), tt.opts)

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

func TestPatchRuntimeVersion(t *testing.T) {
	originalScript := "bash attach.sh --runtime containerd --runtime-version 1.5.10 --other-flag"

	t.Run("replaces runtime version from metadata", func(t *testing.T) {
		m := &MockCSClient{}
		name := "containerd"
		ver := "1.7.2"
		m.On("DescribeClusterDetail", mock.Anything, "c-test").Return(
			&cs.DescribeClusterDetailResponse{
				Body: &cs.DescribeClusterDetailResponseBody{
					CurrentVersion: tea.String("1.30.1-aliyun.1"),
				},
			}, nil)
		m.On("DescribeKubernetesVersionMetadata", mock.Anything, "1.30.1-aliyun.1").Return(
			[]*cs.DescribeKubernetesVersionMetadataResponseBody{
				{
					Runtimes: []*cs.Runtime{
						{Name: &name, Version: &ver},
					},
				},
			}, nil)

		p := NewProvider("cn-shanghai", m)
		result := p.patchRuntimeVersion(context.Background(), "c-test", originalScript)
		assert.Contains(t, result, "--runtime containerd --runtime-version 1.7.2")
		assert.NotContains(t, result, "1.5.10")
	})

	t.Run("returns original script on DescribeClusterDetail failure", func(t *testing.T) {
		m := &MockCSClient{}
		m.On("DescribeClusterDetail", mock.Anything, "c-test").Return(
			&cs.DescribeClusterDetailResponse{}, fmt.Errorf("API error"))

		p := NewProvider("cn-shanghai", m)
		result := p.patchRuntimeVersion(context.Background(), "c-test", originalScript)
		assert.Equal(t, originalScript, result)
	})

	t.Run("returns original script on empty runtimes", func(t *testing.T) {
		m := &MockCSClient{}
		m.On("DescribeClusterDetail", mock.Anything, "c-test").Return(
			&cs.DescribeClusterDetailResponse{
				Body: &cs.DescribeClusterDetailResponseBody{
					CurrentVersion: tea.String("1.30.1-aliyun.1"),
				},
			}, nil)
		m.On("DescribeKubernetesVersionMetadata", mock.Anything, "1.30.1-aliyun.1").Return(
			[]*cs.DescribeKubernetesVersionMetadataResponseBody{}, nil)

		p := NewProvider("cn-shanghai", m)
		result := p.patchRuntimeVersion(context.Background(), "c-test", originalScript)
		assert.Equal(t, originalScript, result)
	})
}

func TestSanitizeBootstrapLabels(t *testing.T) {
	tests := []struct {
		name        string
		script      string
		wantContain string
		wantAbsent  string
	}{
		{
			name:        "valid labels kept",
			script:      "attach.sh --labels env=prod,app=frontend --other",
			wantContain: "--labels env=prod,app=frontend",
		},
		{
			name:        "invalid key with multiple slashes dropped",
			script:      "attach.sh --labels a/b/c=test,env=prod",
			wantContain: "--labels env=prod",
			wantAbsent:  "a/b/c",
		},
		{
			name:        "invalid value with colon dropped",
			script:      "attach.sh --labels key=val:ue,good=ok",
			wantContain: "--labels good=ok",
			wantAbsent:  "val:ue",
		},
		{
			name:        "no labels flag passes through unchanged",
			script:      "attach.sh --other-flag",
			wantContain: "--other-flag",
		},
		{
			name:        "all labels invalid removes --labels entirely",
			script:      "attach.sh --labels a/b/c=test --other",
			wantAbsent:  "--labels",
			wantContain: "--other",
		},
		{
			name:        "valid prefixed key kept",
			script:      "attach.sh --labels kubernetes.io/hostname=mynode --other",
			wantContain: "--labels kubernetes.io/hostname=mynode",
		},
		{
			name:        "invalid prefix dropped",
			script:      "attach.sh --labels INVALID/name=val,good=ok",
			wantContain: "--labels good=ok",
			wantAbsent:  "INVALID",
		},
		{
			name:        "empty value is valid",
			script:      "attach.sh --labels env= --other",
			wantContain: "--labels env=",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeBootstrapLabels(context.Background(), tt.script)
			if tt.wantContain != "" {
				assert.Contains(t, result, tt.wantContain)
			}
			if tt.wantAbsent != "" {
				assert.NotContains(t, result, tt.wantAbsent)
			}
		})
	}
}

func TestLabelValidationHelpers(t *testing.T) {
	t.Run("isValidLabelName empty string", func(t *testing.T) {
		assert.False(t, isValidLabelName(""))
	})
	t.Run("isValidLabelName too long", func(t *testing.T) {
		longName := strings.Repeat("a", 64)
		assert.False(t, isValidLabelName(longName))
	})
	t.Run("isValidLabelName single char", func(t *testing.T) {
		assert.True(t, isValidLabelName("a"))
	})
	t.Run("isValidLabelName valid", func(t *testing.T) {
		assert.True(t, isValidLabelName("my-label.name_1"))
	})
	t.Run("isValidDNSSubdomain valid", func(t *testing.T) {
		assert.True(t, isValidDNSSubdomain("kubernetes.io"))
	})
	t.Run("isValidDNSSubdomain with uppercase invalid", func(t *testing.T) {
		assert.False(t, isValidDNSSubdomain("Kubernetes.io"))
	})
	t.Run("isValidDNSSubdomain empty invalid", func(t *testing.T) {
		assert.False(t, isValidDNSSubdomain(""))
	})
	t.Run("isValidLabelValue empty is valid", func(t *testing.T) {
		assert.True(t, isValidLabelValue(""))
	})
	t.Run("isValidLabelValue too long invalid", func(t *testing.T) {
		longVal := strings.Repeat("a", 64)
		assert.False(t, isValidLabelValue(longVal))
	})
	t.Run("isValidLabelKey with valid prefix", func(t *testing.T) {
		assert.True(t, isValidLabelKey("kubernetes.io/hostname"))
	})
	t.Run("isValidLabelKey with multiple slashes invalid", func(t *testing.T) {
		assert.False(t, isValidLabelKey("a/b/c"))
	})
}
