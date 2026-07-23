package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

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
	// DualStack is true when cluster IpStack is "dual" (IPv6 dual-stack enabled)
	DualStack bool
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

// getClusterNetworkAddon retrieves network addon information from cluster
func getClusterNetworkAddon(csClient clients.CSClient, clusterID string) (addonName string, addonVersion string, addonConfig *string, err error) {
	addons := []string{"terway-eniip", "terway", "kube-flannel-ds", "kube-flannel-ds-vxlan"}
	for _, addon := range addons {
		resp, addonErr := csClient.GetClusterAddonInstance(context.Background(), clusterID, addon)
		if addonErr != nil {
			klog.Warningf("GetClusterAddonInstance(%s, %s) error: %v", clusterID, addon, addonErr)
			continue
		}
		if resp.Body == nil {
			klog.Warningf("GetClusterAddonInstance(%s, %s): response body is nil", clusterID, addon)
			continue
		}
		if resp.Body.Name == nil {
			klog.Warningf("GetClusterAddonInstance(%s, %s): response body.Name is nil, body=%+v", clusterID, addon, resp.Body)
			continue
		}
		version := ""
		if resp.Body.Version != nil {
			version = *resp.Body.Version
		}
		return *resp.Body.Name, version, resp.Body.Config, nil
	}
	return "", "", nil, fmt.Errorf("no network addon found in cluster %s", clusterID)
}

// InitializeClusterNetworkConfig initializes cluster network configuration
// Implements the same logic as goatscaler's setClusterNetWorkConfig
func InitializeClusterNetworkConfig(csClient clients.CSClient, clusterID string) (*NetworkConfig, error) {
	if clusterID == "" {
		return nil, fmt.Errorf("clusterID is empty")
	}

	networkAddon, _, addonConfig, err := getClusterNetworkAddon(csClient, clusterID)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster network addon: %w", err)
	}

	// Get node CIDR mask and dual-stack setting from a single DescribeClusterDetail call
	nodeCidrMask := 24 // default
	dualStack := false
	detail, err := csClient.DescribeClusterDetail(context.Background(), clusterID)
	if err != nil {
		klog.Warningf("failed to describe cluster detail for cluster %s: %v, using defaults", clusterID, err)
	} else if detail != nil && detail.Body != nil {
		if detail.Body.NodeCidrMask != nil {
			if mask, err := strconv.Atoi(*detail.Body.NodeCidrMask); err == nil {
				nodeCidrMask = mask
			} else {
				klog.Warningf("failed to parse NodeCidrMask %s for cluster %s, using default 24", *detail.Body.NodeCidrMask, clusterID)
			}
		}
		if detail.Body.IpStack != nil {
			// When a cluster has IPv6 dual-stack enabled, the IpStack field is
			// reported as "dual" (not "ipv6"). Only "dual" indicates dual-stack.
			if strings.EqualFold(strings.TrimSpace(*detail.Body.IpStack), "dual") {
				dualStack = true
			}
		}
	}

	// Initialize network config with basic info
	nc := &NetworkConfig{
		ClusterNetwork:      networkAddon,
		NodeCidrMask:        nodeCidrMask,
		DualStack:           dualStack,
		TrunkEniEnabled:     false,
		ExclusiveEniEnabled: false,
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
