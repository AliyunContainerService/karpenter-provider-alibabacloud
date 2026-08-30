package main

import (
	"fmt"
	"testing"

	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	vpc "github.com/alibabacloud-go/vpc-20160428/v7/client"
	"github.com/stretchr/testify/require"
)

func TestExtractClusterResources(t *testing.T) {
	resources := ExtractClusterResources(&cs.DescribeClusterDetailResponseBody{
		SecurityGroupId:   tea.String("sg-1"),
		WorkerRamRoleName: tea.String("KubernetesWorkerRole-test"),
		VswitchIds: []*string{
			tea.String("vsw-1"),
			tea.String(""),
			tea.String("vsw-2"),
		},
	})

	require.Equal(t, []string{"vsw-1", "vsw-2"}, resources.VSwitchIDs)
	require.Equal(t, []string{"sg-1"}, resources.SecurityGroupIDs)
	require.Equal(t, "KubernetesWorkerRole-test", resources.RAMRole)
}

func TestClusterResourcesCarriesWorkerImageID(t *testing.T) {
	resources := ClusterResources{
		WorkerImageID: "aliyun_3_x64_20G_container_optimized_alibase_20260513.vhd",
	}

	require.Equal(t, "aliyun_3_x64_20G_container_optimized_alibase_20260513.vhd", resources.WorkerImageID)
}

func TestClusterResourcesCarriesCapacityReservationID(t *testing.T) {
	resources := ClusterResources{
		CapacityReservationID: "crp-123456",
	}

	require.Equal(t, "crp-123456", resources.CapacityReservationID)
}

func TestExistingManifestCapacityReservationID(t *testing.T) {
	require.Equal(t, "crp-123456", existingManifestCapacityReservationID(&Manifest{
		Resources: []Resource{
			{Type: "capacity-reservation", ID: "crp-123456", State: ResourceStatePending},
		},
	}))
	require.Empty(t, existingManifestCapacityReservationID(&Manifest{
		Resources: []Resource{
			{Type: "capacity-reservation", ID: "crp-123456", State: ResourceStateDeleted},
		},
	}))
}

func TestBuildCapacityReservationRequestUsesOnDemandInstanceTypeAndOwnershipTags(t *testing.T) {
	manifest := &Manifest{
		ClusterName: "ack-e2e",
		OwnershipTags: map[string]string{
			"testing/type":           "e2e",
			"testing/cluster":        "ack-e2e",
			"karpenter.sh/discovery": "ack-e2e",
		},
	}
	cfg := &Config{
		AlibabaCloud: CloudConfig{RegionID: "cn-qingdao"},
		Cluster: ClusterConfig{
			NodePool: NodePoolConfig{
				InstanceTypes: []string{"ecs.gn6v-c8g1.2xlarge"},
			},
		},
	}
	resources := ClusterResources{Zones: []string{"cn-qingdao-c"}}

	req, err := BuildCapacityReservationRequest(cfg, manifest, resources)

	require.NoError(t, err)
	require.Equal(t, "cn-qingdao", tea.StringValue(req.RegionId))
	require.Equal(t, "cn-qingdao-c", tea.StringValue(req.ZoneId[0]))
	require.Equal(t, "ecs.c9i.large", tea.StringValue(req.InstanceType), "capacity reservation should use default on-demand E2E instance types, not GPU bootstrap type")
	require.Equal(t, int32(1), tea.Int32Value(req.InstanceAmount))
	require.Equal(t, "Limited", tea.StringValue(req.EndTimeType))
	require.NotEmpty(t, tea.StringValue(req.EndTime))
	require.Contains(t, tea.StringValue(req.ClientToken), "ecs-c9i-large")
	require.Contains(t, tea.StringValue(req.ClientToken), "cn-qingdao-c")
	require.Equal(t, "Target", tea.StringValue(req.PrivatePoolOptions.MatchCriteria))
	require.Equal(t, "ack-e2e-capacity-reservation", tea.StringValue(req.PrivatePoolOptions.Name))
	tags := map[string]string{}
	for _, tag := range req.Tag {
		tags[tea.StringValue(tag.Key)] = tea.StringValue(tag.Value)
	}
	require.Equal(t, "e2e", tags["testing/type"])
	require.Equal(t, "ack-e2e", tags["testing/cluster"])
}

func TestBuildCapacityReservationRequestsUseZoneAndInstanceFallbacks(t *testing.T) {
	t.Setenv("TEST_CAPACITY_RESERVATION_INSTANCE_TYPES", "ecs.c9i.large,ecs.g6.xlarge")
	manifest := &Manifest{ClusterName: "ack-e2e", OwnershipTags: map[string]string{"testing/cluster": "ack-e2e"}}
	cfg := &Config{AlibabaCloud: CloudConfig{RegionID: "cn-hangzhou"}}
	resources := ClusterResources{
		Zones:    []string{"cn-hangzhou-h", "cn-hangzhou-i"},
		GPUZones: []string{"cn-hangzhou-k"},
	}

	requests, err := BuildCapacityReservationRequests(cfg, manifest, resources)

	require.NoError(t, err)
	require.Len(t, requests, 6)
	require.Equal(t, "cn-hangzhou-h", tea.StringValue(requests[0].ZoneId[0]))
	require.Equal(t, "ecs.c9i.large", tea.StringValue(requests[0].InstanceType))
	require.Equal(t, "cn-hangzhou-h", tea.StringValue(requests[1].ZoneId[0]))
	require.Equal(t, "ecs.g6.xlarge", tea.StringValue(requests[1].InstanceType))
	require.Equal(t, "cn-hangzhou-k", tea.StringValue(requests[5].ZoneId[0]))
	require.Equal(t, "ecs.g6.xlarge", tea.StringValue(requests[5].InstanceType))
}

func TestBuildCapacityReservationRequestIgnoresBootstrapInstanceTypes(t *testing.T) {
	manifest := &Manifest{ClusterName: "ack-e2e", OwnershipTags: map[string]string{"testing/cluster": "ack-e2e"}}
	cfg := &Config{
		AlibabaCloud: CloudConfig{RegionID: "cn-qingdao"},
		Cluster: ClusterConfig{
			NodePool: NodePoolConfig{InstanceTypes: []string{"ecs.c9i.xlarge"}},
		},
	}
	resources := ClusterResources{Zones: []string{"cn-qingdao-c"}}

	req, err := BuildCapacityReservationRequest(cfg, manifest, resources)

	require.NoError(t, err)
	require.Equal(t, "ecs.c9i.large", tea.StringValue(req.InstanceType))
}

func TestBuildCapacityReservationRequestUsesEnvInstanceTypeOverride(t *testing.T) {
	t.Setenv("TEST_CAPACITY_RESERVATION_INSTANCE_TYPE", "ecs.c9i.xlarge")
	manifest := &Manifest{ClusterName: "ack-e2e", OwnershipTags: map[string]string{"testing/cluster": "ack-e2e"}}
	cfg := &Config{AlibabaCloud: CloudConfig{RegionID: "cn-qingdao"}}
	resources := ClusterResources{Zones: []string{"cn-qingdao-c"}}

	req, err := BuildCapacityReservationRequest(cfg, manifest, resources)

	require.NoError(t, err)
	require.Equal(t, "ecs.c9i.xlarge", tea.StringValue(req.InstanceType))
}

func TestIsRetryableCapacityReservationCandidateError(t *testing.T) {
	require.True(t, isRetryableCapacityReservationCandidateError(fmt.Errorf("Code: InvalidResourceType.NotSupported")))
	require.True(t, isRetryableCapacityReservationCandidateError(fmt.Errorf("Code: OperationDenied.NoStock")))
	require.True(t, isRetryableCapacityReservationCandidateError(fmt.Errorf("Code: Zone.NotOnSale")))
	require.True(t, isRetryableCapacityReservationCandidateError(fmt.Errorf("Code: Throttling")))
	require.False(t, isRetryableCapacityReservationCandidateError(fmt.Errorf("Code: InvalidAccessKeyId.NotFound")))
}

func TestBuildCapacityReservationRequestUsesUniqueClientToken(t *testing.T) {
	manifest := &Manifest{ClusterName: "ack-e2e", OwnershipTags: map[string]string{"testing/cluster": "ack-e2e"}}
	cfg := &Config{AlibabaCloud: CloudConfig{RegionID: "cn-qingdao"}}
	resources := ClusterResources{Zones: []string{"cn-qingdao-c"}}

	first, err := BuildCapacityReservationRequest(cfg, manifest, resources)
	require.NoError(t, err)
	second, err := BuildCapacityReservationRequest(cfg, manifest, resources)
	require.NoError(t, err)

	require.NotEqual(t, tea.StringValue(first.ClientToken), tea.StringValue(second.ClientToken))
}

func TestMarkManifestCapacityReservationDeleted(t *testing.T) {
	manifest := &Manifest{Resources: []Resource{
		{Type: "capacity-reservation", ID: "crp-stale", State: ResourceStatePending},
		{Type: "capacity-reservation", ID: "crp-active", State: ResourceStatePending},
	}}

	markManifestCapacityReservationDeleted(manifest, "crp-stale")

	require.Equal(t, ResourceStateDeleted, manifest.Resources[0].State)
	require.Equal(t, ResourceStatePending, manifest.Resources[1].State)
}

func TestBuildCapacityReservationRequestRequiresZone(t *testing.T) {
	_, err := BuildCapacityReservationRequest(&Config{}, &Manifest{ClusterName: "ack-e2e"}, ClusterResources{})

	require.ErrorContains(t, err, "zone")
}

func TestExtractWorkerImageID(t *testing.T) {
	imageID := ExtractWorkerImageID(&ecs.DescribeInstancesResponse{
		Body: &ecs.DescribeInstancesResponseBody{
			Instances: &ecs.DescribeInstancesResponseBodyInstances{
				Instance: []*ecs.DescribeInstancesResponseBodyInstancesInstance{
					{ImageId: tea.String("aliyun_3_x64_20G_container_optimized_alibase_20260513.vhd")},
				},
			},
		},
	})

	require.Equal(t, "aliyun_3_x64_20G_container_optimized_alibase_20260513.vhd", imageID)
}

func TestExtractImageIDFromImageNameLookup(t *testing.T) {
	imageID := ExtractImageIDFromImageNameLookup(&ecs.DescribeImagesResponse{
		Body: &ecs.DescribeImagesResponseBody{
			Images: &ecs.DescribeImagesResponseBodyImages{
				Image: []*ecs.DescribeImagesResponseBodyImagesImage{
					{
						ImageId:   tea.String("m-bp1234567890"),
						ImageName: tea.String("aliyun_3_x64_20G_container_optimized_alibase_20260513.vhd"),
						Status:    tea.String("Available"),
					},
				},
			},
		},
	})

	require.Equal(t, "m-bp1234567890", imageID)
}

func TestExtractImageIDFromImageNameLookupAllowsVHDImageID(t *testing.T) {
	imageID := ExtractImageIDFromImageNameLookup(&ecs.DescribeImagesResponse{
		Body: &ecs.DescribeImagesResponseBody{
			Images: &ecs.DescribeImagesResponseBodyImages{
				Image: []*ecs.DescribeImagesResponseBodyImagesImage{
					{
						ImageId: tea.String("aliyun_3_x64_20G_container_optimized_alibase_20260625.vhd"),
						Status:  tea.String("Available"),
					},
				},
			},
		},
	})

	require.Equal(t, "aliyun_3_x64_20G_container_optimized_alibase_20260625.vhd", imageID)
}

func TestAppendVSwitchZones(t *testing.T) {
	zones := appendVSwitchZones(nil, &vpc.DescribeVSwitchesResponse{
		Body: &vpc.DescribeVSwitchesResponseBody{
			VSwitches: &vpc.DescribeVSwitchesResponseBodyVSwitches{
				VSwitch: []*vpc.DescribeVSwitchesResponseBodyVSwitchesVSwitch{
					{ZoneId: tea.String("cn-hangzhou-i")},
					{ZoneId: tea.String("cn-hangzhou-i")},
					{ZoneId: tea.String("cn-hangzhou-k")},
				},
			},
		},
	})

	require.Equal(t, []string{"cn-hangzhou-i", "cn-hangzhou-k"}, zones)
}

func TestClusterResourcesCarriesZones(t *testing.T) {
	resources := ClusterResources{Zones: []string{"cn-hangzhou-i"}}

	require.Equal(t, []string{"cn-hangzhou-i"}, resources.Zones)
}

func TestExtractAvailableGPUResourcesRequiresStock(t *testing.T) {
	candidates := ExtractAvailableGPUResources(&ecs.DescribeAvailableResourceResponse{
		Body: &ecs.DescribeAvailableResourceResponseBody{
			AvailableZones: &ecs.DescribeAvailableResourceResponseBodyAvailableZones{
				AvailableZone: []*ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZone{
					{
						RegionId:       tea.String("cn-hangzhou"),
						ZoneId:         tea.String("cn-hangzhou-i"),
						Status:         tea.String("Available"),
						StatusCategory: tea.String("WithStock"),
						AvailableResources: &ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResources{
							AvailableResource: []*ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResourcesAvailableResource{
								{
									Type: tea.String("InstanceType"),
									SupportedResources: &ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResourcesAvailableResourceSupportedResources{
										SupportedResource: []*ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResourcesAvailableResourceSupportedResourcesSupportedResource{
											{
												Value:          tea.String("ecs.gn6v-c8g1.2xlarge"),
												Status:         tea.String("Available"),
												StatusCategory: tea.String("WithStock"),
											},
											{
												Value:          tea.String("ecs.gn6i-c4g1.xlarge"),
												Status:         tea.String("SoldOut"),
												StatusCategory: tea.String("WithoutStock"),
											},
										},
									},
								},
							},
						},
					},
					{
						RegionId:       tea.String("cn-shanghai"),
						ZoneId:         tea.String("cn-shanghai-m"),
						Status:         tea.String("SoldOut"),
						StatusCategory: tea.String("WithoutStock"),
						AvailableResources: &ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResources{
							AvailableResource: []*ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResourcesAvailableResource{
								{
									Type: tea.String("InstanceType"),
									SupportedResources: &ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResourcesAvailableResourceSupportedResources{
										SupportedResource: []*ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResourcesAvailableResourceSupportedResourcesSupportedResource{
											{
												Value:          tea.String("ecs.gn6v-c8g1.2xlarge"),
												Status:         tea.String("Available"),
												StatusCategory: tea.String("WithStock"),
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}, nil)

	require.Equal(t, []GPUResourceCandidate{{
		RegionID:     "cn-hangzhou",
		ZoneID:       "cn-hangzhou-i",
		InstanceType: "ecs.gn6v-c8g1.2xlarge",
	}}, candidates)
}

func TestExtractAvailableGPUResourcesFiltersInstanceTypes(t *testing.T) {
	candidates := ExtractAvailableGPUResources(&ecs.DescribeAvailableResourceResponse{
		Body: &ecs.DescribeAvailableResourceResponseBody{
			AvailableZones: &ecs.DescribeAvailableResourceResponseBodyAvailableZones{
				AvailableZone: []*ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZone{
					{
						RegionId:       tea.String("cn-hangzhou"),
						ZoneId:         tea.String("cn-hangzhou-i"),
						Status:         tea.String("Available"),
						StatusCategory: tea.String("WithStock"),
						AvailableResources: &ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResources{
							AvailableResource: []*ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResourcesAvailableResource{
								{
									Type: tea.String("InstanceType"),
									SupportedResources: &ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResourcesAvailableResourceSupportedResources{
										SupportedResource: []*ecs.DescribeAvailableResourceResponseBodyAvailableZonesAvailableZoneAvailableResourcesAvailableResourceSupportedResourcesSupportedResource{
											{
												Value:          tea.String("ecs.gn6v-c8g1.2xlarge"),
												Status:         tea.String("Available"),
												StatusCategory: tea.String("WithStock"),
											},
											{
												Value:          tea.String("ecs.gn7i-c8g1.2xlarge"),
												Status:         tea.String("Available"),
												StatusCategory: tea.String("WithStock"),
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}, []string{"ecs.gn7i-c8g1.2xlarge"})

	require.Equal(t, []GPUResourceCandidate{{
		RegionID:     "cn-hangzhou",
		ZoneID:       "cn-hangzhou-i",
		InstanceType: "ecs.gn7i-c8g1.2xlarge",
	}}, candidates)
}

func TestApplyGPUDiscoverySelectsFirstRegionAndAllMatchingZones(t *testing.T) {
	cfg := &Config{
		AlibabaCloud: CloudConfig{RegionID: "cn-shanghai"},
	}
	resources := &ClusterResources{}

	err := applyGPUDiscovery(cfg, resources, []GPUResourceCandidate{
		{RegionID: "cn-hangzhou", ZoneID: "cn-hangzhou-i", InstanceType: "ecs.gn6v-c8g1.2xlarge"},
		{RegionID: "cn-hangzhou", ZoneID: "cn-hangzhou-k", InstanceType: "ecs.gn6v-c8g1.2xlarge"},
		{RegionID: "cn-beijing", ZoneID: "cn-beijing-h", InstanceType: "ecs.gn7i-c8g1.2xlarge"},
	})

	require.NoError(t, err)
	require.Equal(t, "cn-hangzhou", cfg.AlibabaCloud.RegionID)
	require.Equal(t, []string{"ecs.gn6v-c8g1.2xlarge"}, resources.GPUInstanceTypes)
	require.Equal(t, []string{"cn-hangzhou-i", "cn-hangzhou-k"}, resources.GPUZones)
	require.Equal(t, []string{"cn-hangzhou-i", "cn-hangzhou-k"}, cfg.Cluster.ZoneIDs)
	require.Equal(t, []string{"ecs.gn6v-c8g1.2xlarge"}, cfg.Cluster.NodePool.InstanceTypes)
}

func TestApplyGPUDiscoveryPreservesPinnedNetworkZones(t *testing.T) {
	cfg := &Config{
		AlibabaCloud: CloudConfig{RegionID: "cn-hangzhou"},
		Cluster: ClusterConfig{
			VPCID:      "vpc-1",
			VSwitchIDs: []string{"vsw-1"},
			ZoneIDs:    []string{"cn-hangzhou-j"},
		},
	}
	resources := &ClusterResources{}

	err := applyGPUDiscovery(cfg, resources, []GPUResourceCandidate{
		{RegionID: "cn-hangzhou", ZoneID: "cn-hangzhou-i", InstanceType: "ecs.gn6v-c8g1.2xlarge"},
	})

	require.NoError(t, err)
	require.Equal(t, []string{"cn-hangzhou-j"}, cfg.Cluster.ZoneIDs)
}

func TestValidateGPUDiscoveryPinnedNetworkRequiresExplicitVSwitches(t *testing.T) {
	cfg := &Config{
		AlibabaCloud: CloudConfig{RegionID: "cn-hangzhou"},
		Cluster:      ClusterConfig{VPCID: "vpc-1"},
	}

	err := validateGPUDiscoveryPinnedNetworkConfig(cfg, &ClusterResources{
		GPUZones: []string{"cn-hangzhou-i"},
	})

	require.ErrorContains(t, err, "explicit VSwitch IDs")
}

func TestPinnedVSwitchIDsIncludesAllClusterVSwitches(t *testing.T) {
	ids := pinnedVSwitchIDs(ClusterConfig{
		VSwitchIDs:    []string{"vsw-worker"},
		PodVSwitchIDs: []string{"vsw-pod"},
		Master:        MasterConfig{VSwitchIDs: []string{"vsw-master"}},
	})

	require.Equal(t, []string{"vsw-worker", "vsw-pod", "vsw-master"}, ids)
}

func TestApplyGPUDiscoveryRejectsPinnedNetworkInDifferentRegion(t *testing.T) {
	tests := []struct {
		name    string
		cluster ClusterConfig
		message string
	}{
		{
			name:    "vpc",
			cluster: ClusterConfig{VPCID: "vpc-1"},
			message: "pins VPC",
		},
		{
			name:    "worker vswitch",
			cluster: ClusterConfig{VSwitchIDs: []string{"vsw-1"}},
			message: "pins VSwitch",
		},
		{
			name:    "pod vswitch",
			cluster: ClusterConfig{PodVSwitchIDs: []string{"vsw-1"}},
			message: "pins VSwitch",
		},
		{
			name: "master vswitch",
			cluster: ClusterConfig{
				Master: MasterConfig{VSwitchIDs: []string{"vsw-1"}},
			},
			message: "pins VSwitch",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				AlibabaCloud: CloudConfig{RegionID: "cn-shanghai"},
				Cluster:      tt.cluster,
			}
			resources := &ClusterResources{}

			err := applyGPUDiscovery(cfg, resources, []GPUResourceCandidate{
				{RegionID: "cn-hangzhou", ZoneID: "cn-hangzhou-i", InstanceType: "ecs.gn6v-c8g1.2xlarge"},
			})

			require.ErrorContains(t, err, tt.message)
		})
	}
}

func TestIntersectGPUZonesWithClusterZones(t *testing.T) {
	resources := ClusterResources{
		Zones:            []string{"cn-hangzhou-i", "cn-hangzhou-j"},
		GPUInstanceTypes: []string{"ecs.gn6v-c8g1.2xlarge"},
		GPUZones:         []string{"cn-hangzhou-i", "cn-hangzhou-k"},
	}

	filtered, err := IntersectGPUZonesWithClusterZones(resources)

	require.NoError(t, err)
	require.Equal(t, []string{"cn-hangzhou-i"}, filtered.GPUZones)
}

func TestIntersectGPUZonesWithClusterZonesFailsWithoutOverlap(t *testing.T) {
	resources := ClusterResources{
		Zones:            []string{"cn-hangzhou-j"},
		GPUInstanceTypes: []string{"ecs.gn6v-c8g1.2xlarge"},
		GPUZones:         []string{"cn-hangzhou-i"},
	}

	_, err := IntersectGPUZonesWithClusterZones(resources)

	require.ErrorContains(t, err, "no GPU zones overlap")
}

func TestExtractClusterResourcesFallsBackToDeprecatedVswitchID(t *testing.T) {
	resources := ExtractClusterResources(&cs.DescribeClusterDetailResponseBody{
		VswitchId: tea.String("vsw-1, vsw-2"),
	})

	require.Equal(t, []string{"vsw-1", "vsw-2"}, resources.VSwitchIDs)
}
