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
	"fmt"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	coreapis "sigs.k8s.io/karpenter/pkg/apis/v1"
	"strings"
	"sync"
	"text/template"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// ClusterType represents the type of Kubernetes cluster
type ClusterType string

const (
	// ACKClusterType represents Alibaba Cloud ACK cluster
	ACKClusterType ClusterType = "cs"
	// SelfManagedClusterType represents self-managed Kubernetes cluster
	SelfManagedClusterType ClusterType = "self-managed"
	// KubeadmClusterType represents self-managed Kubernetes cluster bootstrapped with kubeadm
	KubeadmClusterType ClusterType = "kubeadm"
)

// BootstrapOptions contains options for generating bootstrap scripts
type BootstrapOptions struct {
	// Cluster type (ACK or self-managed)
	ClusterType ClusterType
	// ACK cluster ID (required for ACK clusters)
	ClusterID string
	// ACK cluster name (required for ACK clusters)
	ClusterName string
	// Kubernetes API server endpoint
	ClusterEndpoint string
	// Node name
	NodeName string
	// Kubelet configuration overrides
	KubeletConfig *KubeletConfiguration
	// Node labels
	Labels map[string]string
	// Node taints
	Taints []corev1.Taint
	// Custom user data script
	CustomUserData *string
	// Container runtime to use (containerd or docker)
	ContainerRuntime string
}

// KubeletConfiguration represents kubelet configuration
type KubeletConfiguration struct {
	// MaxPods is the maximum number of pods that can run on this Kubelet
	MaxPods *int32
	// PodsPerCore is the maximum number of pods per core
	PodsPerCore *int32
	// CPUCFSQuota enables CPU CFS quota enforcement for containers that specify CPU limits
	CPUCFSQuota *bool
	// SystemReserved is the reserved resources for system
	SystemReserved map[corev1.ResourceName]string
	// KubeReserved is the reserved resources for Kubernetes
	KubeReserved map[corev1.ResourceName]string
	// EvictionHard is the map of signal names to quantities that define hard eviction thresholds
	EvictionHard map[string]string
	// EvictionSoft is the map of signal names to quantities that define soft eviction thresholds
	EvictionSoft map[string]string
	// EvictionSoftGracePeriod is the map of signal names to quantities that define grace periods for soft eviction thresholds
	EvictionSoftGracePeriod map[string]string
	// EvictionMaxPodGracePeriod is the maximum allowed grace period (in seconds) to use when terminating pods in response to a soft eviction threshold being met
	EvictionMaxPodGracePeriod *int32
	// ImageGCHighThresholdPercent is the percent of disk usage after which image garbage collection is always run
	ImageGCHighThresholdPercent *int32
	// ImageGCLowThresholdPercent is the percent of disk usage before which image garbage collection is never run
	ImageGCLowThresholdPercent *int32
	// ClusterDNS is the IP address for a cluster DNS server
	ClusterDNS []string
}

// Provider handles bootstrap script generation for Alibaba Cloud instances
type Provider struct {
	region   string
	csClient clients.CSClient
	cache    map[string]*CacheEntry
	cacheMu  sync.RWMutex
	cacheTTL time.Duration
}

// 添加构造函数初始化缓存
func NewProvider(region string, csClient clients.CSClient) *Provider {
	return &Provider{
		region:   region,
		csClient: csClient,
		cache:    make(map[string]*CacheEntry),
		cacheTTL: 5 * time.Minute, // Cache for 5 minutes by default
	}
}

// 复用 vswitch.go 中的 CacheEntry 定义
// CacheEntry represents a cached result with expiration
type CacheEntry struct {
	Value     interface{}
	ExpiresAt time.Time
}

// getCachedValue retrieves a value from cache if it exists and is not expired
func (p *Provider) getCachedValue(key string) (interface{}, bool) {
	p.cacheMu.RLock()
	entry, exists := p.cache[key]
	if !exists {
		p.cacheMu.RUnlock()
		return nil, false
	}

	// Check if cache entry is expired
	if time.Now().After(entry.ExpiresAt) {
		p.cacheMu.RUnlock()
		// Remove expired entry with write lock
		p.cacheMu.Lock()
		// Double-check to prevent concurrent deletions
		if entry2, exists := p.cache[key]; exists && time.Now().After(entry2.ExpiresAt) {
			delete(p.cache, key)
		}
		p.cacheMu.Unlock()
		return nil, false
	}

	value := entry.Value
	p.cacheMu.RUnlock()
	return value, true
}

// setCachedValue stores a value in cache with expiration
func (p *Provider) setCachedValue(key string, value interface{}) {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()

	p.cache[key] = &CacheEntry{
		Value:     value,
		ExpiresAt: time.Now().Add(p.cacheTTL),
	}
}

// SetCacheTTL sets the cache TTL duration
func (p *Provider) SetCacheTTL(ttl time.Duration) {
	p.cacheTTL = ttl
}

// ClearCache clears the bootstrap script cache
func (p *Provider) ClearCache() {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	p.cache = make(map[string]*CacheEntry)
}

// GenerateUserData generates user data script for bootstrapping nodes
func (p *Provider) GenerateUserData(opts BootstrapOptions) (string, error) {
	// If custom user data is provided, use it
	if opts.CustomUserData != nil && *opts.CustomUserData != "" {
		return *opts.CustomUserData, nil
	}

	// Generate bootstrap script based on cluster type
	switch opts.ClusterType {
	case ACKClusterType:
		return p.generateACKBootstrapScript(opts)
	case SelfManagedClusterType:
		return p.generateSelfManagedBootstrapScript(opts)
	case KubeadmClusterType:
		// For kubeadm clusters, use the dedicated kubeadm bootstrapper
		kubeadmBootstrapper := NewKubeadmBootstrapper()
		return kubeadmBootstrapper.GenerateUserData(opts)
	default:
		return "", fmt.Errorf("unsupported cluster type: %s", opts.ClusterType)
	}
}

func (p *Provider) getACKBootstrapScriptByCluster(opts BootstrapOptions) (string, error) {
	// Validate required fields for ACK clusters
	if opts.ClusterID == "" {
		return "", fmt.Errorf("cluster ID is required for ACK clusters")
	}

	// Create cache key from cluster ID
	cacheKey := fmt.Sprintf("cs-bootstrap-script-%s", opts.ClusterID)

	// Check cache first
	if script, exists := p.getCachedValue(cacheKey); exists {
		return script.(string), nil
	}

	// Create request to get cluster attach scripts
	request := &cs.DescribeClusterAttachScriptsRequest{
		Expired: tea.Int64(1825228800),
	}

	// Execute request
	script, err := p.csClient.DescribeClusterAttachScripts(context.Background(), opts.ClusterID, request)
	if err != nil {
		return "", fmt.Errorf("failed to describe cluster attach scripts: %w", err)
	}

	// Cache the result
	p.setCachedValue(cacheKey, script)

	return script, nil
}

// generateACKBootstrapScript generates bootstrap script for ACK clusters
func (p *Provider) generateACKBootstrapScript(opts BootstrapOptions) (string, error) {
	// Validate required fields for ACK clusters
	if opts.ClusterID == "" {
		return "", fmt.Errorf("cluster ID is required for ACK clusters")
	}

	// Get ACK bootstrap script
	script, err := p.getACKBootstrapScriptByCluster(opts)
	if err != nil {
		return "", fmt.Errorf("failed to get ACK bootstrap script: %w", err)
	}

	// 如果获取到的脚本不为空，则在开头添加 #!/bin/bash 和 set -ex
	if script != "" {
		script = ensureScriptHeader(script)
	}

	return script, nil
}

// 辅助函数 - 提取自generateACKBootstrapScript中的脚本处理逻辑
func ensureScriptHeader(script string) string {
	if script != "" {
		// remove \r
		script = strings.ReplaceAll(script, "\r", "")
		if !strings.HasPrefix(script, "#!/bin/bash") {
			script = "#!/bin/bash\nset -ex\n" + script
		} else if !strings.Contains(script, "set -ex") {
			lines := strings.Split(script, "\n")
			if len(lines) > 1 {
				lines[1] = "set -ex\n" + lines[1]
				script = strings.Join(lines, "\n")
			}
		}
		// add unregister taint
		script += fmt.Sprintf(" --taints %s", convertTaints2String([]corev1.Taint{coreapis.UnregisteredNoExecuteTaint}))
	}
	return script
}

func convertTaints2String(taints []corev1.Taint) string {
	taintsStr := []string{}
	for _, taint := range taints {
		pair := fmt.Sprintf("%s=%s", taint.Key, taint.Value)
		if taint.Effect != "" {
			pair = fmt.Sprintf("%s:%s", pair, taint.Effect)
		}
		taintsStr = append(taintsStr, pair)
	}
	return strings.Join(taintsStr, ",")
}

// generateSelfManagedBootstrapScript generates bootstrap script for self-managed clusters
func (p *Provider) generateSelfManagedBootstrapScript(opts BootstrapOptions) (string, error) {
	// Validate required fields for self-managed clusters
	if opts.ClusterEndpoint == "" {
		return "", fmt.Errorf("cluster endpoint is required for self-managed clusters")
	}

	// Build kubelet arguments
	kubeletArgs := p.buildKubeletArgs(opts)

	// Build node labels
	nodeLabels := p.buildNodeLabels(opts)

	// Build node taints
	nodeTaints := p.buildNodeTaints(opts)

	// Template for self-managed bootstrap script
	scriptTemplate := `#!/bin/bash
set -ex

# Install kubeadm, kubelet, and kubectl
curl -s https://packages.cloud.google.com/apt/doc/apt-key.gpg | apt-key add -
echo "deb https://apt.kubernetes.io/ kubernetes-xenial main" > /etc/apt/sources.list.d/kubernetes.list
apt-get update
apt-get install -y kubelet kubeadm kubectl

# Configure kubelet
cat > /var/lib/kubelet/config.yaml << 'EOF'
apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
{{- range .KubeletArgs}}
{{.Key}}: {{.Value}}
{{- end}}
EOF

# Join cluster
kubeadm join {{.ClusterEndpoint}} \
  --token {{.Token}} \
  --discovery-token-ca-cert-hash {{.CACertHash}} \
  --node-name {{.NodeName}} \
  {{- if .NodeLabels}}
  --node-labels={{.NodeLabels}} \
  {{- end}}
  {{- if .NodeTaints}}
  --register-with-taints={{.NodeTaints}} \
  {{- end}}
`

	// Parse template
	tmpl, err := template.New("self-managed-bootstrap").Parse(scriptTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse self-managed bootstrap template: %w", err)
	}

	// Template data (placeholder values)
	data := map[string]interface{}{
		"ClusterEndpoint": opts.ClusterEndpoint,
		"NodeName":        opts.NodeName,
		"Token":           "abc.def.ghi", // Placeholder
		"CACertHash":      "sha256:xyz",  // Placeholder
		"KubeletArgs":     kubeletArgs,
		"NodeLabels":      nodeLabels,
		"NodeTaints":      nodeTaints,
	}

	// Execute template
	var script strings.Builder
	if err := tmpl.Execute(&script, data); err != nil {
		return "", fmt.Errorf("failed to execute self-managed bootstrap template: %w", err)
	}

	return script.String(), nil
}

// buildKubeletArgs builds kubelet arguments from configuration
func (p *Provider) buildKubeletArgs(opts BootstrapOptions) []map[string]interface{} {
	var args []map[string]interface{}

	// Add kubelet configuration overrides
	if opts.KubeletConfig != nil {
		kc := opts.KubeletConfig

		// Add maxPods
		if kc.MaxPods != nil {
			args = append(args, map[string]interface{}{
				"Key":   "maxPods",
				"Value": fmt.Sprintf("%d", *kc.MaxPods),
			})
		}

		// Add podsPerCore
		if kc.PodsPerCore != nil {
			args = append(args, map[string]interface{}{
				"Key":   "podsPerCore",
				"Value": fmt.Sprintf("%d", *kc.PodsPerCore),
			})
		}

		// Add cpuCFSQuota
		if kc.CPUCFSQuota != nil {
			args = append(args, map[string]interface{}{
				"Key":   "cpuCFSQuota",
				"Value": fmt.Sprintf("%t", *kc.CPUCFSQuota),
			})
		}

		// Add systemReserved
		if kc.SystemReserved != nil {
			resources := make(map[string]string)
			for k, v := range kc.SystemReserved {
				resources[string(k)] = v
			}
			if len(resources) > 0 {
				args = append(args, map[string]interface{}{
					"Key":   "systemReserved",
					"Value": fmt.Sprintf("%q", resources),
				})
			}
		}

		// Add kubeReserved
		if kc.KubeReserved != nil {
			resources := make(map[string]string)
			for k, v := range kc.KubeReserved {
				resources[string(k)] = v
			}
			if len(resources) > 0 {
				args = append(args, map[string]interface{}{
					"Key":   "kubeReserved",
					"Value": fmt.Sprintf("%q", resources),
				})
			}
		}

		// Add evictionHard
		if kc.EvictionHard != nil {
			thresholds := make(map[string]string)
			for k, v := range kc.EvictionHard {
				thresholds[k] = v
			}
			if len(thresholds) > 0 {
				args = append(args, map[string]interface{}{
					"Key":   "evictionHard",
					"Value": fmt.Sprintf("%q", thresholds),
				})
			}
		}

		// Add evictionSoft
		if kc.EvictionSoft != nil {
			thresholds := make(map[string]string)
			for k, v := range kc.EvictionSoft {
				thresholds[k] = v
			}
			if len(thresholds) > 0 {
				args = append(args, map[string]interface{}{
					"Key":   "evictionSoft",
					"Value": fmt.Sprintf("%q", thresholds),
				})
			}
		}

		// Add evictionSoftGracePeriod
		if kc.EvictionSoftGracePeriod != nil {
			gracePeriods := make(map[string]string)
			for k, v := range kc.EvictionSoftGracePeriod {
				gracePeriods[k] = v
			}
			if len(gracePeriods) > 0 {
				args = append(args, map[string]interface{}{
					"Key":   "evictionSoftGracePeriod",
					"Value": fmt.Sprintf("%q", gracePeriods),
				})
			}
		}

		// Add evictionMaxPodGracePeriod
		if kc.EvictionMaxPodGracePeriod != nil {
			args = append(args, map[string]interface{}{
				"Key":   "evictionMaxPodGracePeriod",
				"Value": fmt.Sprintf("%d", *kc.EvictionMaxPodGracePeriod),
			})
		}

		// Add imageGCHighThresholdPercent
		if kc.ImageGCHighThresholdPercent != nil {
			args = append(args, map[string]interface{}{
				"Key":   "imageGCHighThresholdPercent",
				"Value": fmt.Sprintf("%d", *kc.ImageGCHighThresholdPercent),
			})
		}

		// Add imageGCLowThresholdPercent
		if kc.ImageGCLowThresholdPercent != nil {
			args = append(args, map[string]interface{}{
				"Key":   "imageGCLowThresholdPercent",
				"Value": fmt.Sprintf("%d", *kc.ImageGCLowThresholdPercent),
			})
		}

		// Add clusterDNS
		if kc.ClusterDNS != nil && len(kc.ClusterDNS) > 0 {
			dnsList := make([]string, len(kc.ClusterDNS))
			for i, dns := range kc.ClusterDNS {
				dnsList[i] = dns
			}
			args = append(args, map[string]interface{}{
				"Key":   "clusterDNS",
				"Value": fmt.Sprintf("%q", dnsList),
			})
		}
	}

	return args
}

// buildNodeLabels builds node labels string
func (p *Provider) buildNodeLabels(opts BootstrapOptions) string {
	if len(opts.Labels) == 0 {
		return ""
	}

	var labels []string
	for k, v := range opts.Labels {
		labels = append(labels, fmt.Sprintf("%s=%s", k, v))
	}

	return strings.Join(labels, ",")
}

// buildNodeTaints builds node taints string
func (p *Provider) buildNodeTaints(opts BootstrapOptions) string {
	if len(opts.Taints) == 0 {
		return ""
	}

	var taints []string
	for _, taint := range opts.Taints {
		taints = append(taints, fmt.Sprintf("%s=%s:%s", taint.Key, taint.Value, taint.Effect))
	}

	return strings.Join(taints, ",")
}

// formatLabels formats labels as a comma-separated string
func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}

	var labelList []string
	for k, v := range labels {
		labelList = append(labelList, fmt.Sprintf("%s=%s", k, v))
	}

	return strings.Join(labelList, ",")
}

// formatTaints formats taints as a comma-separated string
func formatTaints(taints []corev1.Taint) string {
	if len(taints) == 0 {
		return ""
	}

	var taintList []string
	for _, taint := range taints {
		taintList = append(taintList, fmt.Sprintf("%s=%s:%s", taint.Key, taint.Value, taint.Effect))
	}

	return strings.Join(taintList, ",")
}
