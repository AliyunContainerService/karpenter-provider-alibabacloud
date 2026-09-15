package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadConfigUsesEnvironmentCredentialsAndRedactsSecrets(t *testing.T) {
	t.Setenv("ALIBABA_CLOUD_ACCESS_KEY_ID", "env-ak")
	t.Setenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET", "env-sk")

	path := filepath.Join(t.TempDir(), "deploy-config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
kubeconfig: /tmp/kubeconfig
alibaba_cloud:
  access_key_id: file-ak
  access_key_secret: file-sk
  region_id: cn-shanghai
cluster:
  name: e2e
  cluster_type: ManagedKubernetes
  cluster_spec: ack.pro.small
  profile: Default
  endpoint_public_access: true
  deletion_protection: false
  service_cidr: 172.21.0.0/20
  proxy_mode: ipvs
  snat_entry: true
  addons:
    - name: terway-eniip
  node_pool:
    name: default-nodepool
    instance_types: ["ecs.g6.xlarge"]
    desired_size: 2
    system_disk_category: cloud_essd
    system_disk_size: 120
    instance_charge_type: PostPaid
    image_type: AliyunLinux3ContainerOptimized
    runtime: containerd
autoscaling:
  enabled: true
  scaler_type: ack-goatscaler
  cool_down_duration: 1m
  scan_interval: 15s
  expander: least-waste
  scale_down_enabled: true
  unneeded_duration: 1m
  utilization_threshold: "0.5"
  gpu_utilization_threshold: "0.5"
`), 0600))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.Equal(t, "env-ak", cfg.AlibabaCloud.AccessKeyID)
	require.Equal(t, "env-sk", cfg.AlibabaCloud.AccessKeySecret)
	require.Equal(t, "cn-shanghai", cfg.AlibabaCloud.RegionID)
	require.Equal(t, "e2e", cfg.Cluster.Name)

	redacted := cfg.RedactedString()
	require.NotContains(t, redacted, "env-ak")
	require.NotContains(t, redacted, "env-sk")
	require.NotContains(t, redacted, "file-ak")
	require.NotContains(t, redacted, "file-sk")
	require.Contains(t, redacted, "<redacted>")
}

func TestWriteEnvFileEscapesShellValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ack.env")

	require.NoError(t, WriteEnvFile(path, map[string]string{
		"KUBECONFIG":            "/tmp/kube config",
		"TEST_CLUSTER_NAME":     "ack'e2e",
		"TEST_CLUSTER_ID":       "c-123",
		"TEST_REGION":           "cn-shanghai",
		"TEST_CLUSTER_ENDPOINT": "https://example.com",
	}))

	data := string(requireReadFile(t, path))
	require.Contains(t, data, "export KUBECONFIG='/tmp/kube config'")
	require.Contains(t, data, "export TEST_CLUSTER_NAME='ack'\"'\"'e2e'")
	require.False(t, strings.Contains(data, "ALIBABA_CLOUD_ACCESS_KEY_SECRET"))
}

func TestLoadConfigExpandsHomeDirectoryInKubeconfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(t.TempDir(), "deploy-config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
kubeconfig: ~/.kube/ack-e2e
alibaba_cloud:
  access_key_id: ak
  access_key_secret: sk
  region_id: cn-shanghai
cluster:
  name: e2e
`), 0600))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(home, ".kube", "ack-e2e"), cfg.Kubeconfig)
}

func TestSaveConfigOverrideWritesRegionClusterNameAndKubeconfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deploy-config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
kubeconfig: /tmp/old-kubeconfig
alibaba_cloud:
  access_key_id: ak
  access_key_secret: sk
  region_id: cn-shanghai
cluster:
  name: old-name
`), 0600))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	cfg.AlibabaCloud.RegionID = "cn-hangzhou"
	cfg.Cluster.Name = "new-name"
	cfg.Kubeconfig = "/tmp/new-kubeconfig"

	out := filepath.Join(t.TempDir(), "rendered.yaml")
	require.NoError(t, SaveConfig(out, cfg))

	rendered, err := LoadConfig(out)
	require.NoError(t, err)
	require.Equal(t, "cn-hangzhou", rendered.AlibabaCloud.RegionID)
	require.Equal(t, "new-name", rendered.Cluster.Name)
	require.Equal(t, "/tmp/new-kubeconfig", rendered.Kubeconfig)
	require.Equal(t, "ak", rendered.AlibabaCloud.AccessKeyID)
	require.Equal(t, "sk", rendered.AlibabaCloud.AccessKeySecret)
}

func TestApplyRuntimeConfigRejectsRegionOverrideWithPinnedNetwork(t *testing.T) {
	tests := []struct {
		name    string
		cluster ClusterConfig
	}{
		{name: "vpc", cluster: ClusterConfig{VPCID: "vpc-1"}},
		{name: "worker vswitch", cluster: ClusterConfig{VSwitchIDs: []string{"vsw-1"}}},
		{name: "pod vswitch", cluster: ClusterConfig{PodVSwitchIDs: []string{"vsw-1"}}},
		{name: "master vswitch", cluster: ClusterConfig{Master: MasterConfig{VSwitchIDs: []string{"vsw-1"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				AlibabaCloud: CloudConfig{RegionID: "cn-shanghai"},
				Cluster:      tt.cluster,
			}

			err := applyRuntimeConfig(cfg, RuntimeConfigOverrides{Region: "cn-hangzhou"})

			require.ErrorContains(t, err, "region override")
		})
	}
}

func TestApplyRuntimeConfigAppliesKubernetesVersion(t *testing.T) {
	cfg := &Config{
		AlibabaCloud: CloudConfig{RegionID: "cn-shanghai"},
	}

	err := applyRuntimeConfig(cfg, RuntimeConfigOverrides{
		Region:            "cn-shanghai",
		KubernetesVersion: "1.32",
	})

	require.NoError(t, err)
	require.Equal(t, "1.32", cfg.Cluster.KubernetesVersion)
}

func TestApplyRuntimeConfigAppliesIPStack(t *testing.T) {
	cfg := &Config{}

	err := applyRuntimeConfig(cfg, RuntimeConfigOverrides{IPStack: "ipv6"})

	require.NoError(t, err)
	require.Equal(t, "ipv6", cfg.Cluster.IPStack)
	require.Equal(t, defaultIPv6ServiceCIDR(), cfg.Cluster.ServiceCIDR)
}

func TestApplyRuntimeConfigPreservesExplicitIPv6ServiceCIDR(t *testing.T) {
	cfg := &Config{Cluster: ClusterConfig{ServiceCIDR: "fd00:10:96::/112"}}

	err := applyRuntimeConfig(cfg, RuntimeConfigOverrides{IPStack: "ipv6"})

	require.NoError(t, err)
	require.Equal(t, "fd00:10:96::/112", cfg.Cluster.ServiceCIDR)
}

func requireReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}
