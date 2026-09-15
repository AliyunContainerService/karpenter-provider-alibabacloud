package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Kubeconfig   string             `yaml:"kubeconfig"`
	AlibabaCloud CloudConfig        `yaml:"alibaba_cloud"`
	Cluster      ClusterConfig      `yaml:"cluster"`
	GPUDiscovery GPUDiscoveryConfig `yaml:"gpu_discovery"`
	Autoscaling  AutoscalingConfig  `yaml:"autoscaling"`
}

type CloudConfig struct {
	AccessKeyID     string `yaml:"access_key_id"`
	AccessKeySecret string `yaml:"access_key_secret"`
	RegionID        string `yaml:"region_id"`
	ClusterID       string `yaml:"cluster_id"`
}

type ClusterConfig struct {
	Name                 string         `yaml:"name"`
	ClusterType          string         `yaml:"cluster_type"`
	ClusterSpec          string         `yaml:"cluster_spec"`
	Profile              string         `yaml:"profile"`
	KubernetesVersion    string         `yaml:"kubernetes_version"`
	VPCID                string         `yaml:"vpc_id"`
	VSwitchIDs           []string       `yaml:"vswitch_ids"`
	ServiceCIDR          string         `yaml:"service_cidr"`
	IPStack              string         `yaml:"ip_stack"`
	PodVSwitchIDs        []string       `yaml:"pod_vswitch_ids"`
	ContainerCIDR        string         `yaml:"container_cidr"`
	ProxyMode            string         `yaml:"proxy_mode"`
	SNATEntry            bool           `yaml:"snat_entry"`
	EndpointPublicAccess bool           `yaml:"endpoint_public_access"`
	AccessControlList    []string       `yaml:"access_control_list"`
	DeletionProtection   bool           `yaml:"deletion_protection"`
	Timezone             string         `yaml:"timezone"`
	ZoneIDs              []string       `yaml:"zone_ids"`
	Addons               []AddonConfig  `yaml:"addons"`
	NodePool             NodePoolConfig `yaml:"node_pool"`
	Master               MasterConfig   `yaml:"master"`
}

type AddonConfig struct {
	Name     string `yaml:"name"`
	Disabled bool   `yaml:"disabled"`
	Config   string `yaml:"config"`
}

type NodePoolConfig struct {
	Name               string   `yaml:"name"`
	InstanceTypes      []string `yaml:"instance_types"`
	DesiredSize        int64    `yaml:"desired_size"`
	SystemDiskCategory string   `yaml:"system_disk_category"`
	SystemDiskSize     int64    `yaml:"system_disk_size"`
	InstanceChargeType string   `yaml:"instance_charge_type"`
	ImageType          string   `yaml:"image_type"`
	KeyPair            string   `yaml:"key_pair"`
	LoginPassword      string   `yaml:"login_password"`
	Runtime            string   `yaml:"runtime"`
	RuntimeVersion     string   `yaml:"runtime_version"`
}

type MasterConfig struct {
	Count              int64    `yaml:"count"`
	InstanceTypes      []string `yaml:"instance_types"`
	VSwitchIDs         []string `yaml:"vswitch_ids"`
	SystemDiskCategory string   `yaml:"system_disk_category"`
	SystemDiskSize     int64    `yaml:"system_disk_size"`
	InstanceChargeType string   `yaml:"instance_charge_type"`
	KeyPair            string   `yaml:"key_pair"`
	LoginPassword      string   `yaml:"login_password"`
}

type AutoscalingConfig struct {
	Enabled                 bool   `yaml:"enabled"`
	ScalerType              string `yaml:"scaler_type"`
	CoolDownDuration        string `yaml:"cool_down_duration"`
	ScanInterval            string `yaml:"scan_interval"`
	Expander                string `yaml:"expander"`
	ScaleDownEnabled        bool   `yaml:"scale_down_enabled"`
	UnneededDuration        string `yaml:"unneeded_duration"`
	UtilizationThreshold    string `yaml:"utilization_threshold"`
	GPUUtilizationThreshold string `yaml:"gpu_utilization_threshold"`
}

type GPUDiscoveryConfig struct {
	Regions            []string `yaml:"regions"`
	InstanceTypes      []string `yaml:"instance_types"`
	InstanceChargeType string   `yaml:"instance_charge_type"`
	SystemDiskCategory string   `yaml:"system_disk_category"`
}

type RuntimeConfigOverrides struct {
	Region            string
	ClusterName       string
	Kubeconfig        string
	KubernetesVersion string
	IPStack           string
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if v := os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_ID"); v != "" {
		cfg.AlibabaCloud.AccessKeyID = v
	}
	if v := os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET"); v != "" {
		cfg.AlibabaCloud.AccessKeySecret = v
	}
	if cfg.AlibabaCloud.RegionID == "" {
		return nil, fmt.Errorf("alibaba_cloud.region_id is required")
	}
	if cfg.Cluster.Name == "" {
		return nil, fmt.Errorf("cluster.name is required")
	}
	cfg.Kubeconfig = expandHome(cfg.Kubeconfig)
	return cfg, nil
}

func applyRuntimeConfig(cfg *Config, overrides RuntimeConfigOverrides) error {
	if overrides.Region != "" && cfg.AlibabaCloud.RegionID != "" && overrides.Region != cfg.AlibabaCloud.RegionID {
		if cfg.Cluster.VPCID != "" || hasPinnedVSwitches(cfg.Cluster) {
			return fmt.Errorf("region override %s conflicts with pinned VPC/VSwitch network in %s", overrides.Region, cfg.AlibabaCloud.RegionID)
		}
	}
	if overrides.Region != "" {
		cfg.AlibabaCloud.RegionID = overrides.Region
	}
	if overrides.ClusterName != "" {
		cfg.Cluster.Name = overrides.ClusterName
	}
	if overrides.Kubeconfig != "" {
		cfg.Kubeconfig = overrides.Kubeconfig
	}
	if overrides.KubernetesVersion != "" {
		cfg.Cluster.KubernetesVersion = overrides.KubernetesVersion
	}
	if overrides.IPStack != "" {
		cfg.Cluster.IPStack = overrides.IPStack
	}
	if strings.EqualFold(cfg.Cluster.IPStack, "ipv6") && !strings.Contains(cfg.Cluster.ServiceCIDR, ":") {
		cfg.Cluster.ServiceCIDR = defaultIPv6ServiceCIDR()
	}
	return nil
}

func defaultIPv6ServiceCIDR() string {
	return "fd00:172:21::/112"
}

func SaveConfig(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func (c *Config) RedactedString() string {
	cp := *c
	cp.AlibabaCloud.AccessKeyID = "<redacted>"
	cp.AlibabaCloud.AccessKeySecret = "<redacted>"
	cp.Cluster.NodePool.LoginPassword = "<redacted>"
	cp.Cluster.Master.LoginPassword = "<redacted>"
	data, err := yaml.Marshal(cp)
	if err != nil {
		return "<redacted>"
	}
	return string(data)
}

func WriteEnvFile(path string, values map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf bytes.Buffer
	for _, k := range keys {
		buf.WriteString("export ")
		buf.WriteString(k)
		buf.WriteByte('=')
		buf.WriteString(shellQuote(values[k]))
		buf.WriteByte('\n')
	}
	return os.WriteFile(path, buf.Bytes(), 0600)
}

func shellQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "'\"'\"'") + "'"
}

func expandHome(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}
