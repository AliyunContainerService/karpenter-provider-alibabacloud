package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	"k8s.io/klog/v2"
)

type NetworkConfig struct {
	// ClusterNetwork is the network plugin name (e.g., terway, terway-eniip, kube-flannel-ds, kube-flannel-ds-vxlan)
	ClusterNetwork string
	// NodeCidrMask is the CIDR mask for node network
	NodeCidrMask int
	// MaxPods is the maximum number of pods per node, calculated from NodeCidrMask
	MaxPods int64
	// TrunkEniEnabled indicates if trunk ENI is enabled for terway-eniip
	TrunkEniEnabled bool
	// ExclusiveEniEnabled indicates if exclusive ENI mode is enabled (managed terway only)
	ExclusiveEniEnabled bool
}

type ClusterEniConfig struct {
	ENITrunking bool `json:"ENITrunking"`
}

// CalculateMaxPods calculates the maximum pods per node based on the CIDR mask
func (nc *NetworkConfig) CalculateMaxPods() int64 {
	if nc.NodeCidrMask > 0 && nc.NodeCidrMask < 32 {
		return int64(math.Pow(2, 32-float64(nc.NodeCidrMask)))
	}
	return 0 // Default max pods
}

// getClusterNodeCidrMask retrieves the node CIDR mask from cluster detail
func getClusterNodeCidrMask(csClient clients.CSClient, clusterID string) (int, error) {
	detail, err := csClient.DescribeClusterDetail(context.Background(), clusterID)
	if err != nil {
		return 0, fmt.Errorf("failed to describe cluster detail: %w", err)
	}
	if detail == nil || detail.Body == nil || detail.Body.NodeCidrMask == nil {
		return 24, nil // Default mask
	}

	nodeCidrMask := 24 // Default
	_, err = strconv.Atoi(*detail.Body.NodeCidrMask)
	if err != nil {
		klog.Warningf("failed to parse NodeCidrMask %s for cluster %s, using default 24: %v", *detail.Body.NodeCidrMask, clusterID, err)
		return 24, nil
	}
	nodeCidrMask, _ = strconv.Atoi(*detail.Body.NodeCidrMask)
	return nodeCidrMask, nil
}

// getClusterNetworkAddon retrieves network addon information from cluster
func getClusterNetworkAddon(csClient clients.CSClient, clusterID string) (addonName string, addonVersion string, addonConfig *string, err error) {
	addons := []string{"terway-eniip", "terway", "kube-flannel-ds", "kube-flannel-ds-vxlan"}
	for _, addon := range addons {
		resp, err := csClient.GetClusterAddonInstance(context.Background(), clusterID, addon)
		if err == nil && resp.Body != nil && resp.Body.Name != nil {
			version := ""
			if resp.Body.Version != nil {
				version = *resp.Body.Version
			}
			return *resp.Body.Name, version, resp.Body.Config, nil
		}
	}
	return "", "", nil, fmt.Errorf("no network addon found in cluster %s", clusterID)
}

// InitializeClusterNetworkConfig initializes cluster network configuration
// Implements the same logic as goatscaler's setClusterNetWorkConfig
func InitializeClusterNetworkConfig(csClient clients.CSClient, clusterID string) (*NetworkConfig, error) {
	if clusterID == "" {
		return nil, fmt.Errorf("clusterID is empty")
	}

	// Get network addon information
	networkAddon, _, addonConfig, err := getClusterNetworkAddon(csClient, clusterID)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster network addon: %w", err)
	}

	// Get node CIDR mask
	nodeCidrMask, err := getClusterNodeCidrMask(csClient, clusterID)
	if err != nil {
		klog.Warningf("failed to get cluster node cidr mask for cluster %s: %v", clusterID, err)
		nodeCidrMask = 24 // Use default
	}

	// Initialize network config with basic info
	nc := &NetworkConfig{
		ClusterNetwork:      networkAddon,
		NodeCidrMask:        nodeCidrMask,
		TrunkEniEnabled:     false, // Default value
		ExclusiveEniEnabled: false, // Default value
		MaxPods:             0,
	}

	// Handle special cases based on network addon type
	switch networkAddon {
	case "terway-eniip":
		// Parse terway-eniip configuration
		if addonConfig != nil {
			if err := parseTerwayEniipConfig(addonConfig, nc); err != nil {
				klog.Warningf("failed to parse terway-eniip config for cluster %s: %v", clusterID, err)
			} else {
				klog.Infof("cluster %s has terway-eniip, trunk ENI enabled: %v", clusterID, nc.TrunkEniEnabled)
			}
		}

	case "kube-flannel-ds", "kube-flannel-ds-vxlan":
		klog.Infof("cluster %s uses flannel/terway network addon: %s", clusterID, networkAddon)
		nc.MaxPods = nc.CalculateMaxPods()
	}

	return nc, nil
}

// parseTerwayEniipConfig parses the terway-eniip configuration from addon config string
func parseTerwayEniipConfig(configStr *string, nc *NetworkConfig) error {
	if configStr == nil {
		return fmt.Errorf("config string is nil")
	}

	eniConfig := ClusterEniConfig{}

	if err := json.Unmarshal([]byte(*configStr), &eniConfig); err != nil {
		return fmt.Errorf("failed to unmarshal terway-eniip config: %w", err)
	}

	nc.TrunkEniEnabled = eniConfig.ENITrunking
	return nil
}
