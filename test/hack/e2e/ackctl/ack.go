package main

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	"github.com/alibabacloud-go/tea/tea"
)

func ResolveClusterName(prefix string) string {
	if prefix == "" {
		prefix = "karpenter-alibabacloud-e2e"
	}
	return fmt.Sprintf("%s-%s", prefix, time.Now().UTC().Format("20060102-150405"))
}

func BuildCreateClusterRequest(cfg *Config, clusterName, gitRef string) *cs.CreateClusterRequest {
	req := &cs.CreateClusterRequest{
		Name:                     tea.String(clusterName),
		RegionId:                 tea.String(cfg.AlibabaCloud.RegionID),
		ClusterType:              tea.String(defaultString(cfg.Cluster.ClusterType, "ManagedKubernetes")),
		ClusterSpec:              optionalString(cfg.Cluster.ClusterSpec),
		Profile:                  optionalString(cfg.Cluster.Profile),
		KubernetesVersion:        optionalString(cfg.Cluster.KubernetesVersion),
		Vpcid:                    optionalString(cfg.Cluster.VPCID),
		WorkerVswitchIds:         stringPtrs(cfg.Cluster.VSwitchIDs),
		PodVswitchIds:            stringPtrs(cfg.Cluster.PodVSwitchIDs),
		ServiceCidr:              optionalString(cfg.Cluster.ServiceCIDR),
		IpStack:                  optionalString(ackIPStack(cfg.Cluster.IPStack)),
		ContainerCidr:            optionalString(cfg.Cluster.ContainerCIDR),
		ProxyMode:                optionalString(cfg.Cluster.ProxyMode),
		SnatEntry:                tea.Bool(cfg.Cluster.SNATEntry),
		EndpointPublicAccess:     tea.Bool(cfg.Cluster.EndpointPublicAccess),
		AccessControlList:        publicEndpointAccessControlList(cfg.Cluster),
		DeletionProtection:       tea.Bool(cfg.Cluster.DeletionProtection),
		Timezone:                 optionalString(cfg.Cluster.Timezone),
		ZoneIds:                  stringPtrs(cfg.Cluster.ZoneIDs),
		NumOfNodes:               tea.Int64(nonZeroInt64(cfg.Cluster.NodePool.DesiredSize, 1)),
		WorkerInstanceTypes:      stringPtrs(cfg.Cluster.NodePool.InstanceTypes),
		WorkerInstanceChargeType: optionalString(defaultString(cfg.Cluster.NodePool.InstanceChargeType, "PostPaid")),
		WorkerSystemDiskCategory: optionalString(defaultString(cfg.Cluster.NodePool.SystemDiskCategory, "cloud_essd")),
		WorkerSystemDiskSize:     tea.Int64(nonZeroInt64(cfg.Cluster.NodePool.SystemDiskSize, 120)),
		LoginPassword:            optionalString(cfg.Cluster.NodePool.LoginPassword),
		KeyPair:                  optionalString(cfg.Cluster.NodePool.KeyPair),
		Runtime: &cs.Runtime{
			Name:    optionalString(defaultString(cfg.Cluster.NodePool.Runtime, "containerd")),
			Version: optionalString(cfg.Cluster.NodePool.RuntimeVersion),
		},
		Addons: buildAddons(cfg.Cluster.Addons),
		Tags:   buildOwnershipTags(clusterName, gitRef),
	}
	if cfg.Cluster.NodePool.ImageType != "" {
		req.ImageType = tea.String(cfg.Cluster.NodePool.ImageType)
	}
	if cfg.Cluster.ClusterType == "Kubernetes" {
		req.MasterCount = tea.Int64(cfg.Cluster.Master.Count)
		req.MasterInstanceTypes = stringPtrs(cfg.Cluster.Master.InstanceTypes)
		req.MasterVswitchIds = stringPtrs(cfg.Cluster.Master.VSwitchIDs)
		req.MasterInstanceChargeType = optionalString(defaultString(cfg.Cluster.Master.InstanceChargeType, "PostPaid"))
		req.MasterSystemDiskCategory = optionalString(defaultString(cfg.Cluster.Master.SystemDiskCategory, "cloud_essd"))
		req.MasterSystemDiskSize = tea.Int64(nonZeroInt64(cfg.Cluster.Master.SystemDiskSize, 120))
		if cfg.Cluster.Master.LoginPassword != "" {
			req.LoginPassword = tea.String(cfg.Cluster.Master.LoginPassword)
		}
		if cfg.Cluster.Master.KeyPair != "" {
			req.KeyPair = tea.String(cfg.Cluster.Master.KeyPair)
		}
	}
	return req
}

func ackIPStack(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), "ipv6") {
		return "dual"
	}
	return value
}

func publicEndpointAccessControlList(cluster ClusterConfig) []*string {
	if !cluster.EndpointPublicAccess {
		return nil
	}
	if len(cluster.AccessControlList) == 0 {
		return stringPtrs([]string{"0.0.0.0/0"})
	}
	return stringPtrs(cluster.AccessControlList)
}

func BuildBootstrapNodePoolRequest(cfg *Config, manifest *Manifest, resources ClusterResources) (*cs.CreateClusterNodePoolRequest, error) {
	desiredSize := nonZeroInt64(cfg.Cluster.NodePool.DesiredSize, 1)
	instanceTypes := cfg.Cluster.NodePool.InstanceTypes
	if len(instanceTypes) == 0 {
		return nil, fmt.Errorf("cluster.node_pool.instance_types is required to create bootstrap nodepool")
	}
	vswitchIDs := firstNonEmptyList(resources.VSwitchIDs, cfg.Cluster.VSwitchIDs)
	if len(vswitchIDs) == 0 {
		return nil, fmt.Errorf("bootstrap nodepool requires at least one VSwitch ID")
	}
	scalingGroup := &cs.CreateClusterNodePoolRequestScalingGroup{
		DesiredSize:            tea.Int64(desiredSize),
		InstanceTypes:          stringPtrs(instanceTypes),
		InstanceChargeType:     optionalString(defaultString(cfg.Cluster.NodePool.InstanceChargeType, "PostPaid")),
		SystemDiskCategory:     optionalString(defaultString(cfg.Cluster.NodePool.SystemDiskCategory, "cloud_essd")),
		SystemDiskSize:         tea.Int64(nonZeroInt64(cfg.Cluster.NodePool.SystemDiskSize, 120)),
		KeyPair:                optionalString(cfg.Cluster.NodePool.KeyPair),
		LoginPassword:          optionalString(cfg.Cluster.NodePool.LoginPassword),
		VswitchIds:             stringPtrs(vswitchIDs),
		SecurityGroupIds:       stringPtrs(resources.SecurityGroupIDs),
		RamRoleName:            optionalString(resources.RAMRole),
		Tags:                   nodePoolTags(manifest.OwnershipTags),
		CompensateWithOnDemand: tea.Bool(true),
	}
	if cfg.Cluster.NodePool.ImageType != "" {
		scalingGroup.ImageType = tea.String(cfg.Cluster.NodePool.ImageType)
	}
	return &cs.CreateClusterNodePoolRequest{
		NodepoolInfo: &cs.CreateClusterNodePoolRequestNodepoolInfo{
			Name: tea.String(defaultString(cfg.Cluster.NodePool.Name, "bootstrap")),
			Type: tea.String("ess"),
		},
		KubernetesConfig: &cs.CreateClusterNodePoolRequestKubernetesConfig{
			Runtime:        optionalString(defaultString(cfg.Cluster.NodePool.Runtime, "containerd")),
			RuntimeVersion: optionalString(cfg.Cluster.NodePool.RuntimeVersion),
		},
		ScalingGroup: scalingGroup,
	}, nil
}

func nodePoolTags(tags map[string]string) []*cs.CreateClusterNodePoolRequestScalingGroupTags {
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]*cs.CreateClusterNodePoolRequestScalingGroupTags, 0, len(keys))
	for _, key := range keys {
		out = append(out, &cs.CreateClusterNodePoolRequestScalingGroupTags{
			Key:   tea.String(key),
			Value: tea.String(tags[key]),
		})
	}
	return out
}

func EnsureNodeLoginCredential(cfg *Config) error {
	if cfg.Cluster.NodePool.KeyPair == "" && cfg.Cluster.NodePool.LoginPassword == "" {
		password, err := generatePassword(24)
		if err != nil {
			return err
		}
		cfg.Cluster.NodePool.LoginPassword = password
	}
	if cfg.Cluster.ClusterType == "Kubernetes" && cfg.Cluster.Master.KeyPair == "" && cfg.Cluster.Master.LoginPassword == "" {
		password, err := generatePassword(24)
		if err != nil {
			return err
		}
		cfg.Cluster.Master.LoginPassword = password
	}
	return nil
}

func generatePassword(length int) (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*"
	required := []byte{'a', 'A', '0', '!'}
	out := make([]byte, 0, length)
	out = append(out, required...)
	for len(out) < length {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		out = append(out, alphabet[n.Int64()])
	}
	for i := range out {
		j, err := rand.Int(rand.Reader, big.NewInt(int64(len(out))))
		if err != nil {
			return "", err
		}
		out[i], out[j.Int64()] = out[j.Int64()], out[i]
	}
	return string(out), nil
}

func buildAddons(addons []AddonConfig) []*cs.Addon {
	out := make([]*cs.Addon, 0, len(addons))
	for _, addon := range addons {
		if addon.Name == "" {
			continue
		}
		out = append(out, &cs.Addon{
			Name:     tea.String(addon.Name),
			Config:   optionalString(addon.Config),
			Disabled: tea.Bool(addon.Disabled),
		})
	}
	return out
}

func buildOwnershipTags(clusterName, gitRef string) []*cs.Tag {
	values := map[string]string{
		"testing/type":           "e2e",
		"testing/cluster":        clusterName,
		"karpenter.sh/discovery": clusterName,
	}
	if gitRef != "" {
		values["test/git_ref"] = gitRef
	}
	tags := make([]*cs.Tag, 0, len(values))
	for k, v := range values {
		tags = append(tags, &cs.Tag{Key: tea.String(k), Value: tea.String(v)})
	}
	return tags
}

func tagsToMap(tags []*cs.Tag) map[string]string {
	out := map[string]string{}
	for _, tag := range tags {
		if tag == nil || tag.Key == nil || tag.Value == nil {
			continue
		}
		out[*tag.Key] = *tag.Value
	}
	return out
}

func stringPtrs(values []string) []*string {
	out := make([]*string, 0, len(values))
	for _, v := range values {
		if v == "" {
			continue
		}
		out = append(out, tea.String(v))
	}
	return out
}

func firstNonEmptyList(lists ...[]string) []string {
	for _, list := range lists {
		values := uniqueNonEmpty(list)
		if len(values) > 0 {
			return values
		}
	}
	return nil
}

func optionalString(v string) *string {
	if v == "" {
		return nil
	}
	return tea.String(v)
}

func defaultString(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func nonZeroInt64(v, fallback int64) int64 {
	if v == 0 {
		return fallback
	}
	return v
}
