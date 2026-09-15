package main

import (
	"testing"

	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/require"
)

func TestBuildCreateClusterRequestMapsConfigAndOwnershipTags(t *testing.T) {
	cfg := &Config{
		AlibabaCloud: CloudConfig{RegionID: "cn-shanghai"},
		Cluster: ClusterConfig{
			Name:                 "ack-e2e",
			ClusterType:          "ManagedKubernetes",
			ClusterSpec:          "ack.pro.small",
			Profile:              "Default",
			KubernetesVersion:    "1.32",
			VPCID:                "vpc-1",
			VSwitchIDs:           []string{"vsw-1"},
			ServiceCIDR:          "172.21.0.0/20",
			IPStack:              "ipv6",
			ContainerCIDR:        "172.22.0.0/16",
			ProxyMode:            "ipvs",
			SNATEntry:            true,
			EndpointPublicAccess: true,
			DeletionProtection:   false,
			Timezone:             "Asia/Shanghai",
			ZoneIDs:              []string{"cn-shanghai-l"},
			Addons: []AddonConfig{
				{Name: "terway-eniip"},
				{Name: "nginx-ingress-controller", Disabled: true},
			},
			NodePool: NodePoolConfig{
				InstanceTypes:      []string{"ecs.g6.xlarge"},
				DesiredSize:        2,
				SystemDiskCategory: "cloud_essd",
				SystemDiskSize:     120,
				InstanceChargeType: "PostPaid",
				ImageType:          "AliyunLinux3ContainerOptimized",
				Runtime:            "containerd",
			},
		},
	}

	req := BuildCreateClusterRequest(cfg, "ack-e2e-123", "gitsha")

	require.Equal(t, "ack-e2e-123", tea.StringValue(req.Name))
	require.Equal(t, "cn-shanghai", tea.StringValue(req.RegionId))
	require.Equal(t, "ManagedKubernetes", tea.StringValue(req.ClusterType))
	require.Equal(t, "ack.pro.small", tea.StringValue(req.ClusterSpec))
	require.Equal(t, "Default", tea.StringValue(req.Profile))
	require.Equal(t, "1.32", tea.StringValue(req.KubernetesVersion))
	require.Equal(t, "dual", tea.StringValue(req.IpStack))
	require.Equal(t, "vpc-1", tea.StringValue(req.Vpcid))
	require.Equal(t, "vsw-1", tea.StringValue(req.WorkerVswitchIds[0]))
	require.Equal(t, "cn-shanghai-l", tea.StringValue(req.ZoneIds[0]))
	require.True(t, tea.BoolValue(req.EndpointPublicAccess))
	require.False(t, tea.BoolValue(req.DeletionProtection))
	require.Equal(t, int64(2), tea.Int64Value(req.NumOfNodes))
	require.Equal(t, "containerd", tea.StringValue(req.Runtime.Name))
	require.Len(t, req.Addons, 2)
	require.True(t, tea.BoolValue(req.Addons[1].Disabled))

	tags := tagsToMap(req.Tags)
	require.Equal(t, "e2e", tags["testing/type"])
	require.Equal(t, "ack-e2e-123", tags["testing/cluster"])
	require.Equal(t, "ack-e2e-123", tags["karpenter.sh/discovery"])
	require.Equal(t, "gitsha", tags["test/git_ref"])
}

func TestACKIPStackMapsIPv6TestConfigToDualStackACKAPI(t *testing.T) {
	require.Equal(t, "dual", ackIPStack("ipv6"))
	require.Equal(t, "dual", ackIPStack(" IPv6 "))
	require.Equal(t, "dual", ackIPStack("dual"))
	require.Empty(t, ackIPStack(""))
}

func TestBuildCreateClusterRequestDefaultsBootstrapWorkerWhenDesiredSizeOmitted(t *testing.T) {
	cfg := &Config{
		AlibabaCloud: CloudConfig{RegionID: "cn-qingdao"},
		Cluster: ClusterConfig{
			Name: "ack-e2e",
			NodePool: NodePoolConfig{
				InstanceTypes: []string{"ecs.g6.xlarge"},
			},
		},
	}

	req := BuildCreateClusterRequest(cfg, "ack-e2e-123", "gitsha")

	require.Equal(t, int64(1), tea.Int64Value(req.NumOfNodes))
}

func TestBuildCreateClusterRequestDefaultsPublicEndpointAccessControlList(t *testing.T) {
	cfg := &Config{
		AlibabaCloud: CloudConfig{RegionID: "cn-qingdao"},
		Cluster: ClusterConfig{
			Name:                 "ack-e2e",
			EndpointPublicAccess: true,
			NodePool: NodePoolConfig{
				InstanceTypes: []string{"ecs.g6.xlarge"},
			},
		},
	}

	req := BuildCreateClusterRequest(cfg, "ack-e2e-123", "gitsha")

	require.Len(t, req.AccessControlList, 1)
	require.Equal(t, "0.0.0.0/0", tea.StringValue(req.AccessControlList[0]))
}

func TestBuildCreateClusterRequestUsesConfiguredAccessControlList(t *testing.T) {
	cfg := &Config{
		AlibabaCloud: CloudConfig{RegionID: "cn-qingdao"},
		Cluster: ClusterConfig{
			Name:                 "ack-e2e",
			EndpointPublicAccess: true,
			AccessControlList:    []string{"203.0.113.10/32", "198.51.100.0/24"},
			NodePool: NodePoolConfig{
				InstanceTypes: []string{"ecs.g6.xlarge"},
			},
		},
	}

	req := BuildCreateClusterRequest(cfg, "ack-e2e-123", "gitsha")

	require.Len(t, req.AccessControlList, 2)
	require.Equal(t, "203.0.113.10/32", tea.StringValue(req.AccessControlList[0]))
	require.Equal(t, "198.51.100.0/24", tea.StringValue(req.AccessControlList[1]))
}

func TestBuildBootstrapNodePoolRequestUsesClusterResources(t *testing.T) {
	cfg := &Config{
		Cluster: ClusterConfig{
			NodePool: NodePoolConfig{
				Name:               "bootstrap",
				InstanceTypes:      []string{"ecs.g6.xlarge"},
				SystemDiskCategory: "cloud_essd",
				SystemDiskSize:     120,
				InstanceChargeType: "PostPaid",
				Runtime:            "containerd",
				RuntimeVersion:     "1.6.28",
			},
		},
	}
	manifest := &Manifest{
		ClusterName: "ack-e2e-123",
		OwnershipTags: map[string]string{
			"testing/type":    "e2e",
			"testing/cluster": "ack-e2e-123",
		},
	}
	resources := ClusterResources{
		VSwitchIDs:       []string{"vsw-1"},
		SecurityGroupIDs: []string{"sg-1"},
		RAMRole:          "KubernetesWorkerRole-123",
	}

	req, err := BuildBootstrapNodePoolRequest(cfg, manifest, resources)

	require.NoError(t, err)
	require.Nil(t, req.Count)
	require.Equal(t, int64(1), tea.Int64Value(req.ScalingGroup.DesiredSize))
	require.Equal(t, "bootstrap", tea.StringValue(req.NodepoolInfo.Name))
	require.Equal(t, "ess", tea.StringValue(req.NodepoolInfo.Type))
	require.Equal(t, "ecs.g6.xlarge", tea.StringValue(req.ScalingGroup.InstanceTypes[0]))
	require.Equal(t, "vsw-1", tea.StringValue(req.ScalingGroup.VswitchIds[0]))
	require.Equal(t, "sg-1", tea.StringValue(req.ScalingGroup.SecurityGroupIds[0]))
	require.Equal(t, "KubernetesWorkerRole-123", tea.StringValue(req.ScalingGroup.RamRoleName))
	require.Equal(t, "containerd", tea.StringValue(req.KubernetesConfig.Runtime))
	require.Equal(t, "1.6.28", tea.StringValue(req.KubernetesConfig.RuntimeVersion))
	require.Len(t, req.ScalingGroup.Tags, 2)
}

func TestEnsureNodeLoginCredentialGeneratesPasswordWhenMissing(t *testing.T) {
	cfg := &Config{}
	require.NoError(t, EnsureNodeLoginCredential(cfg))
	require.NotEmpty(t, cfg.Cluster.NodePool.LoginPassword)
	require.Len(t, cfg.Cluster.NodePool.LoginPassword, 24)
}
