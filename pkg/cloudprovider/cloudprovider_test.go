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

package cloudprovider

import (
	"context"
	"testing"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/capacityreservation"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instance"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instancetype"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	corecloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
)

func TestRepairPoliciesMatchKubeletAndNodeMonitoringConditions(t *testing.T) {
	cloudProvider := &CloudProvider{}

	policies := cloudProvider.RepairPolicies()

	require.Contains(t, policies, corecloudprovider.RepairPolicy{
		ConditionType:      corev1.NodeReady,
		ConditionStatus:    corev1.ConditionFalse,
		TolerationDuration: 30 * time.Minute,
	})
	require.Contains(t, policies, corecloudprovider.RepairPolicy{
		ConditionType:      corev1.NodeReady,
		ConditionStatus:    corev1.ConditionUnknown,
		TolerationDuration: 30 * time.Minute,
	})
	require.Contains(t, policies, corecloudprovider.RepairPolicy{
		ConditionType:      "AcceleratedHardwareReady",
		ConditionStatus:    corev1.ConditionFalse,
		TolerationDuration: 10 * time.Minute,
	})
	require.Contains(t, policies, corecloudprovider.RepairPolicy{
		ConditionType:      "StorageReady",
		ConditionStatus:    corev1.ConditionFalse,
		TolerationDuration: 30 * time.Minute,
	})
	require.Contains(t, policies, corecloudprovider.RepairPolicy{
		ConditionType:      "NetworkingReady",
		ConditionStatus:    corev1.ConditionFalse,
		TolerationDuration: 30 * time.Minute,
	})
	require.Contains(t, policies, corecloudprovider.RepairPolicy{
		ConditionType:      "KernelReady",
		ConditionStatus:    corev1.ConditionFalse,
		TolerationDuration: 30 * time.Minute,
	})
	require.Contains(t, policies, corecloudprovider.RepairPolicy{
		ConditionType:      "ContainerRuntimeReady",
		ConditionStatus:    corev1.ConditionFalse,
		TolerationDuration: 30 * time.Minute,
	})
}

func TestBuildInstanceTagsMergesUserTagsWithoutOverridingProtectedOwnership(t *testing.T) {
	t.Setenv("GIT_REF", "abc123")
	maxPods := int32(20)

	nodeClaim := &karpv1.NodeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: "nc-1",
			Labels: map[string]string{
				karpv1.NodePoolLabelKey: "np-1",
				"custom/nodeclaim":      "from-nodeclaim",
				v1alpha1.TagManagedBy:   "malicious-nodeclaim",
			},
		},
	}
	nodeClass := &v1alpha1.ECSNodeClass{
		Spec: v1alpha1.ECSNodeClassSpec{
			ClusterID:   "c-test",
			ClusterName: "ack-e2e",
			Tags: map[string]string{
				"custom/user":              "from-user",
				v1alpha1.TagManagedBy:      "malicious-user",
				v1alpha1.TagDiscovery:      "wrong-cluster",
				v1alpha1.TagNodePool:       "wrong-nodepool",
				v1alpha1.TagNodeClaim:      "wrong-nodeclaim",
				v1alpha1.TagClusterID:      "wrong-cluster-id",
				v1alpha1.TagKubeletMaxPods: "999",
				"testing/type":             "wrong-type",
				"testing/cluster":          "wrong-cluster",
				"test/git_ref":             "wrong-git-ref",
				v1alpha1.TagCluster:        "wrong-kubernetes-cluster-tag",
			},
			Kubelet: &v1alpha1.KubeletConfiguration{
				MaxPods: &maxPods,
			},
		},
	}

	tags := buildInstanceTags(nodeClaim, nodeClass)

	require.Equal(t, "true", tags[v1alpha1.TagManagedBy])
	require.Equal(t, "c-test", tags[v1alpha1.TagClusterID])
	require.Equal(t, "ack-e2e", tags[v1alpha1.TagDiscovery])
	require.Equal(t, "np-1", tags[v1alpha1.TagNodePool])
	require.Equal(t, "nc-1", tags[v1alpha1.TagNodeClaim])
	require.Equal(t, "ack-e2e", tags[v1alpha1.TagCluster])
	require.Equal(t, "20", tags[v1alpha1.TagKubeletMaxPods])

	require.Equal(t, "from-nodeclaim", tags["custom/nodeclaim"])
	require.Equal(t, "from-user", tags["custom/user"])
	require.Equal(t, "wrong-type", tags["testing/type"])
	require.Equal(t, "wrong-cluster", tags["testing/cluster"])
	require.Equal(t, "wrong-git-ref", tags["test/git_ref"])
}

func TestBuildInstanceTagsDoesNotAddTestTagsByDefault(t *testing.T) {
	t.Setenv("GIT_REF", "abc123")

	tags := buildInstanceTags(&karpv1.NodeClaim{}, &v1alpha1.ECSNodeClass{
		Spec: v1alpha1.ECSNodeClassSpec{
			ClusterID:   "c-test",
			ClusterName: "prod-cluster",
		},
	})

	require.NotContains(t, tags, "testing/type")
	require.NotContains(t, tags, "testing/cluster")
	require.NotContains(t, tags, "test/git_ref")
}

func TestManagedInstanceListTagsMatchLaunchedInstanceTags(t *testing.T) {
	launchedTags := buildInstanceTags(&karpv1.NodeClaim{}, &v1alpha1.ECSNodeClass{
		Spec: v1alpha1.ECSNodeClassSpec{
			ClusterID:   "c-test",
			ClusterName: "ack-e2e",
		},
	})

	require.Equal(t, launchedTags[v1alpha1.TagManagedBy], managedInstanceTags()[v1alpha1.TagManagedBy])
}

func TestConvertInstanceToNodeClaimIncludesImageID(t *testing.T) {
	cpu := resource.MustParse("4")
	memory := resource.MustParse("8Gi")
	cloudProvider := &CloudProvider{}
	original := &karpv1.NodeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: "nc-1",
			Labels: map[string]string{
				karpv1.NodePoolLabelKey: "np-1",
			},
		},
	}
	inst := &instance.Instance{
		InstanceID:   "i-test",
		Region:       "cn-shanghai",
		Zone:         "cn-shanghai-a",
		InstanceType: "ecs.c7.xlarge",
		ImageID:      "aliyun_3_x64_20G_container_optimized_alibase_20260513.vhd",
		CapacityType: v1alpha1.CapacityTypeOnDemand,
		Tags: map[string]string{
			karpv1.NodePoolLabelKey:    "np-1",
			v1alpha1.TagKubeletMaxPods: "20",
		},
	}
	nodeClaim := cloudProvider.convertInstanceToNodeClaim(context.Background(), inst, original, []*instancetype.InstanceType{
		{
			Name:   "ecs.c7.xlarge",
			CPU:    &cpu,
			Memory: &memory,
		},
	}, "c-test")

	require.Equal(t, inst.ImageID, nodeClaim.Status.ImageID)
	require.Equal(t, int64(20), nodeClaim.Status.Capacity.Pods().Value())
	require.Equal(t, int64(20), nodeClaim.Status.Allocatable.Pods().Value())
}

func TestConvertInstanceToNodeClaimPreservesAndAddsSchedulingLabels(t *testing.T) {
	cpu := resource.MustParse("4")
	memory := resource.MustParse("8Gi")
	cloudProvider := &CloudProvider{}
	original := &karpv1.NodeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: "nc-1",
			Labels: map[string]string{
				karpv1.NodePoolLabelKey:        "np-1",
				v1alpha1.LabelInstanceCategory: "c",
			},
		},
	}
	inst := &instance.Instance{
		InstanceID:   "i-test",
		Region:       "cn-shanghai",
		Zone:         "cn-shanghai-a",
		InstanceType: "ecs.c7.xlarge",
		ImageID:      "aliyun_3_x64_20G_container_optimized_alibase_20260513.vhd",
		CapacityType: v1alpha1.CapacityTypeOnDemand,
		Tags: map[string]string{
			karpv1.NodePoolLabelKey: "np-1",
		},
	}

	nodeClaim := cloudProvider.convertInstanceToNodeClaim(context.Background(), inst, original, []*instancetype.InstanceType{
		{
			Name:         "ecs.c7.xlarge",
			Architecture: "X86",
			CPU:          &cpu,
			Memory:       &memory,
			Zones: map[string]instancetype.ZoneInfo{
				"cn-shanghai-a": {Available: true},
			},
		},
	}, "c-test")

	require.Equal(t, "np-1", nodeClaim.Labels[karpv1.NodePoolLabelKey])
	require.Equal(t, "c", nodeClaim.Labels[v1alpha1.LabelInstanceCategory])
	require.Equal(t, "7", nodeClaim.Labels[v1alpha1.LabelInstanceGeneration])
	require.Equal(t, "c7", nodeClaim.Labels[v1alpha1.LabelInstanceFamily])
	require.Equal(t, "xlarge", nodeClaim.Labels[v1alpha1.LabelInstanceSize])
	require.Equal(t, "4", nodeClaim.Labels[v1alpha1.LabelInstanceCPU])
	require.Equal(t, "8192", nodeClaim.Labels[v1alpha1.LabelInstanceMemory])
	require.Equal(t, "amd64", nodeClaim.Labels[corev1.LabelArchStable])
	require.Equal(t, "linux", nodeClaim.Labels[corev1.LabelOSStable])
	require.Equal(t, "cn-shanghai", nodeClaim.Labels[corev1.LabelTopologyRegion])
}

func TestConvertInstanceToNodeClaimNormalizesGPUModelLabel(t *testing.T) {
	cpu := resource.MustParse("8")
	memory := resource.MustParse("32Gi")
	gpuCount := resource.MustParse("1")
	gpuMemory := resource.MustParse("16")
	cloudProvider := &CloudProvider{}
	original := &karpv1.NodeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: "nc-1",
			Labels: map[string]string{
				karpv1.NodePoolLabelKey: "np-1",
			},
		},
	}
	inst := &instance.Instance{
		InstanceID:   "i-test",
		Region:       "cn-qingdao",
		Zone:         "cn-qingdao-c",
		InstanceType: "ecs.gn6v-c8g1.2xlarge",
		ImageID:      "aliyun_3_x64_20G_container_optimized_alibase_20260513.vhd",
		CapacityType: v1alpha1.CapacityTypeOnDemand,
		Tags:         map[string]string{karpv1.NodePoolLabelKey: "np-1"},
	}

	nodeClaim := cloudProvider.convertInstanceToNodeClaim(context.Background(), inst, original, []*instancetype.InstanceType{
		{
			Name:         "ecs.gn6v-c8g1.2xlarge",
			Architecture: "X86",
			CPU:          &cpu,
			Memory:       &memory,
			GPU: &instancetype.GPU{
				Count:  &gpuCount,
				Model:  "NVIDIA V100",
				Memory: &gpuMemory,
			},
			Zones: map[string]instancetype.ZoneInfo{
				"cn-qingdao-c": {Available: true},
			},
		},
	}, "c-test")

	require.Equal(t, "nvidia-v100", nodeClaim.Labels[v1alpha1.LabelInstanceGPUName])
}

func TestComputeInstanceTypeRequirementsIncludesAlibabaCloudLabels(t *testing.T) {
	cpu := resource.MustParse("4")
	memory := resource.MustParse("8Gi")
	gpuCount := resource.MustParse("1")
	gpuMemory := resource.MustParse("14")

	requirements := computeInstanceTypeRequirements(&instancetype.InstanceType{
		Name:         "ecs.gn6i-c4g1.xlarge",
		Architecture: "X86",
		CPU:          &cpu,
		Memory:       &memory,
		GPU: &instancetype.GPU{
			Count:  &gpuCount,
			Model:  "T4",
			Memory: &gpuMemory,
		},
		Zones: map[string]instancetype.ZoneInfo{
			"cn-hangzhou-i": {Available: true},
			"cn-hangzhou-j": {Available: false},
		},
	}, "cn-hangzhou")

	require.Equal(t, "ecs.gn6i-c4g1.xlarge", requirements.Get(corev1.LabelInstanceTypeStable).Any())
	require.Equal(t, "cn-hangzhou-i", requirements.Get(corev1.LabelTopologyZone).Any())
	require.Equal(t, "cn-hangzhou", requirements.Get(corev1.LabelTopologyRegion).Any())
	require.Equal(t, "amd64", requirements.Get(corev1.LabelArchStable).Any())
	require.Equal(t, "linux", requirements.Get(corev1.LabelOSStable).Any())
	require.True(t, requirements.Get(v1alpha1.LabelCapacityType).Has(v1alpha1.CapacityTypeOnDemand))
	require.True(t, requirements.Get(v1alpha1.LabelCapacityType).Has(v1alpha1.CapacityTypeSpot))
	require.Equal(t, "gn", requirements.Get(v1alpha1.LabelInstanceCategory).Any())
	require.Equal(t, "6", requirements.Get(v1alpha1.LabelInstanceGeneration).Any())
	require.Equal(t, "gn6i-c4g1", requirements.Get(v1alpha1.LabelInstanceFamily).Any())
	require.Equal(t, "xlarge", requirements.Get(v1alpha1.LabelInstanceSize).Any())
	require.Equal(t, "4", requirements.Get(v1alpha1.LabelInstanceCPU).Any())
	require.Equal(t, "8192", requirements.Get(v1alpha1.LabelInstanceMemory).Any())
	require.Equal(t, "t4", requirements.Get(v1alpha1.LabelInstanceGPUName).Any())
	require.Equal(t, "nvidia", requirements.Get(v1alpha1.LabelInstanceGPUManufacturer).Any())
	require.Equal(t, "1", requirements.Get(v1alpha1.LabelInstanceGPUCount).Any())
	require.Equal(t, "14", requirements.Get(v1alpha1.LabelInstanceGPUMemory).Any())
}

func TestComputeInstanceTypeRequirementsNormalizesGPUModelLabelValue(t *testing.T) {
	cpu := resource.MustParse("8")
	memory := resource.MustParse("32Gi")
	gpuCount := resource.MustParse("1")
	gpuMemory := resource.MustParse("16")

	requirements := computeInstanceTypeRequirements(&instancetype.InstanceType{
		Name:         "ecs.gn6v-c8g1.2xlarge",
		Architecture: "X86",
		CPU:          &cpu,
		Memory:       &memory,
		GPU: &instancetype.GPU{
			Count:  &gpuCount,
			Model:  "NVIDIA V100",
			Memory: &gpuMemory,
		},
		Zones: map[string]instancetype.ZoneInfo{
			"cn-qingdao-c": {Available: true},
		},
	}, "cn-qingdao")

	require.Equal(t, "nvidia-v100", requirements.Get(v1alpha1.LabelInstanceGPUName).Any())
}

func TestCapPodCapacityFromInstanceTags(t *testing.T) {
	capacity := corev1.ResourceList{
		corev1.ResourcePods: *resource.NewQuantity(23, resource.DecimalSI),
	}
	allocatable := corev1.ResourceList{
		corev1.ResourcePods: *resource.NewQuantity(23, resource.DecimalSI),
	}

	cappedCapacity, cappedAllocatable := capPodCapacityFromInstanceTags(capacity, allocatable, map[string]string{
		v1alpha1.TagKubeletMaxPods: "20",
	})

	require.Equal(t, int64(20), cappedCapacity.Pods().Value())
	require.Equal(t, int64(20), cappedAllocatable.Pods().Value())
	require.Equal(t, int64(23), capacity.Pods().Value(), "input capacity must not be mutated")
	require.Equal(t, int64(23), allocatable.Pods().Value(), "input allocatable must not be mutated")
}

func TestCreateInstanceWithRetryPassesLaunchTemplateID(t *testing.T) {
	ecsClient := &capturingRunInstancesECSClient{}
	cloudProvider := &CloudProvider{
		instanceProvider: instance.NewProvider(context.Background(), "cn-hangzhou", ecsClient),
	}
	launchTemplateID := "lt-123456"
	launchTemplateVersion := int64(2)

	_, err := cloudProvider.createInstanceWithRetry(
		context.Background(),
		&v1alpha1.ECSNodeClass{
			Spec: v1alpha1.ECSNodeClassSpec{
				LaunchTemplateID:      &launchTemplateID,
				LaunchTemplateVersion: &launchTemplateVersion,
			},
		},
		[]*instancetype.InstanceType{{Name: "ecs.g6.large"}},
		[]v1alpha1.Image{{ID: "img-123"}},
		[]v1alpha1.VSwitch{{ID: "vsw-123"}},
		[]v1alpha1.SecurityGroup{{ID: "sg-123"}},
		"#!/bin/bash",
		map[string]string{"karpenter.sh/nodeclaim": "nc-1"},
	)

	require.NoError(t, err)
	require.NotNil(t, ecsClient.runInstancesRequest)
	require.NotNil(t, ecsClient.runInstancesRequest.LaunchTemplateId)
	require.Equal(t, launchTemplateID, *ecsClient.runInstancesRequest.LaunchTemplateId)
	require.NotNil(t, ecsClient.runInstancesRequest.LaunchTemplateVersion)
	require.Equal(t, launchTemplateVersion, *ecsClient.runInstancesRequest.LaunchTemplateVersion)
}

func TestCreateInstanceWithRetryResolvesTargetCapacityReservation(t *testing.T) {
	capacityReservationID := "crp-123456"
	capacityReservationName := "reserved-capacity"
	ecsClient := &capturingRunInstancesECSClient{
		capacityReservationResponse: &ecs.DescribeCapacityReservationsResponse{
			Body: &ecs.DescribeCapacityReservationsResponseBody{
				CapacityReservationSet: &ecs.DescribeCapacityReservationsResponseBodyCapacityReservationSet{
					CapacityReservationItem: []*ecs.DescribeCapacityReservationsResponseBodyCapacityReservationSetCapacityReservationItem{
						{
							PrivatePoolOptionsId:   &capacityReservationID,
							PrivatePoolOptionsName: &capacityReservationName,
						},
					},
				},
			},
		},
	}
	cloudProvider := &CloudProvider{
		instanceProvider:            instance.NewProvider(context.Background(), "cn-hangzhou", ecsClient),
		capacityReservationProvider: capacityreservation.NewProvider("cn-hangzhou", ecsClient),
	}
	preference := "target"

	_, err := cloudProvider.createInstanceWithRetry(
		context.Background(),
		&v1alpha1.ECSNodeClass{
			Spec: v1alpha1.ECSNodeClassSpec{
				CapacityReservationPreference: &preference,
				CapacityReservationSelectorTerms: []v1alpha1.CapacityReservationSelectorTerm{
					{ID: &capacityReservationID},
				},
			},
		},
		[]*instancetype.InstanceType{{Name: "ecs.g6.large"}},
		[]v1alpha1.Image{{ID: "img-123"}},
		[]v1alpha1.VSwitch{{ID: "vsw-123"}},
		[]v1alpha1.SecurityGroup{{ID: "sg-123"}},
		"#!/bin/bash",
		map[string]string{"karpenter.sh/nodeclaim": "nc-1"},
	)

	require.NoError(t, err)
	require.NotNil(t, ecsClient.runInstancesRequest)
	require.NotNil(t, ecsClient.runInstancesRequest.PrivatePoolOptions)
	require.Equal(t, "Target", *ecsClient.runInstancesRequest.PrivatePoolOptions.MatchCriteria)
	require.Equal(t, capacityReservationID, *ecsClient.runInstancesRequest.PrivatePoolOptions.Id)
}

type capturingRunInstancesECSClient struct {
	runInstancesRequest         *ecs.RunInstancesRequest
	capacityReservationResponse *ecs.DescribeCapacityReservationsResponse
}

func (c *capturingRunInstancesECSClient) RunInstances(ctx context.Context, request *ecs.RunInstancesRequest) (*ecs.RunInstancesResponse, error) {
	c.runInstancesRequest = request
	instanceID := "i-123456"
	return &ecs.RunInstancesResponse{
		Body: &ecs.RunInstancesResponseBody{
			InstanceIdSets: &ecs.RunInstancesResponseBodyInstanceIdSets{
				InstanceIdSet: []*string{&instanceID},
			},
		},
	}, nil
}

func (c *capturingRunInstancesECSClient) DescribeInstances(ctx context.Context, request *ecs.DescribeInstancesRequest) (*ecs.DescribeInstancesResponse, error) {
	return nil, nil
}

func (c *capturingRunInstancesECSClient) DeleteInstances(ctx context.Context, request *ecs.DeleteInstancesRequest) (*ecs.DeleteInstancesResponse, error) {
	return nil, nil
}

func (c *capturingRunInstancesECSClient) TagResources(ctx context.Context, request *ecs.TagResourcesRequest) (*ecs.TagResourcesResponse, error) {
	return nil, nil
}

func (c *capturingRunInstancesECSClient) CreateLaunchTemplate(ctx context.Context, request *ecs.CreateLaunchTemplateRequest) (*ecs.CreateLaunchTemplateResponse, error) {
	return nil, nil
}

func (c *capturingRunInstancesECSClient) DescribeLaunchTemplates(ctx context.Context, request *ecs.DescribeLaunchTemplatesRequest) (*ecs.DescribeLaunchTemplatesResponse, error) {
	return nil, nil
}

func (c *capturingRunInstancesECSClient) DeleteLaunchTemplate(ctx context.Context, request *ecs.DeleteLaunchTemplateRequest) (*ecs.DeleteLaunchTemplateResponse, error) {
	return nil, nil
}

func (c *capturingRunInstancesECSClient) DescribeInstanceTypes(ctx context.Context, instanceTypes []string) (*ecs.DescribeInstanceTypesResponse, error) {
	return nil, nil
}

func (c *capturingRunInstancesECSClient) DescribeZones(ctx context.Context) (*ecs.DescribeZonesResponse, error) {
	return nil, nil
}

func (c *capturingRunInstancesECSClient) DescribeImages(ctx context.Context, imageIDs []string, filters map[string]string) ([]ecs.DescribeImagesResponseBodyImagesImage, error) {
	return nil, nil
}

func (c *capturingRunInstancesECSClient) DescribeSecurityGroups(ctx context.Context, tags map[string]string) (*ecs.DescribeSecurityGroupsResponse, error) {
	return nil, nil
}

func (c *capturingRunInstancesECSClient) DescribeCapacityReservations(ctx context.Context, id string, tags map[string]string) (*ecs.DescribeCapacityReservationsResponse, error) {
	return c.capacityReservationResponse, nil
}

func (c *capturingRunInstancesECSClient) DescribePrice(ctx context.Context, instanceType string) (*ecs.DescribePriceResponse, error) {
	return nil, nil
}
