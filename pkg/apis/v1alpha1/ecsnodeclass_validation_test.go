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

package v1alpha1

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/yaml"
)

func TestECSNodeClassValidateRejectsRestrictedTags(t *testing.T) {
	for _, key := range []string{
		TagManagedBy,
		TagClusterID,
		TagNodePool,
		TagNodeClaim,
		TagKubeletMaxPods,
		TagCluster,
	} {
		nodeClass := validValidationNodeClassForUnit()
		nodeClass.Spec.Tags = map[string]string{key: "value"}

		require.ErrorContains(t, nodeClass.Validate(), "restricted key")
	}
}

func TestNormalizedLabelsIncludesAlibabaCloudDiskCSITopologyZone(t *testing.T) {
	require.Equal(t, corev1.LabelTopologyZone, karpv1.NormalizedLabels["topology.diskplugin.csi.alibabacloud.com/zone"])
}

func TestECSNodeClassValidateRejectsIDWithAdditionalSelectorFilters(t *testing.T) {
	t.Run("vswitch", func(t *testing.T) {
		nodeClass := validValidationNodeClassForUnit()
		nodeClass.Spec.VSwitchSelectorTerms = []VSwitchSelectorTerm{{
			ID:     ptrForUnit("vsw-12345"),
			ZoneID: ptrForUnit("cn-test-a"),
		}}

		require.ErrorContains(t, nodeClass.Validate(), "mutually exclusive")
	})

	t.Run("security group", func(t *testing.T) {
		nodeClass := validValidationNodeClassForUnit()
		nodeClass.Spec.SecurityGroupSelectorTerms = []SecurityGroupSelectorTerm{{
			ID:   ptrForUnit("sg-12345"),
			Tags: map[string]string{"env": "test"},
		}}

		require.ErrorContains(t, nodeClass.Validate(), "mutually exclusive")
	})

	t.Run("image", func(t *testing.T) {
		nodeClass := validValidationNodeClassForUnit()
		nodeClass.Spec.ImageSelectorTerms = []ImageSelectorTerm{{
			ID:   ptrForUnit("m-12345"),
			Tags: map[string]string{"env": "test"},
		}}

		require.ErrorContains(t, nodeClass.Validate(), "mutually exclusive")
	})

	t.Run("capacity reservation", func(t *testing.T) {
		nodeClass := validValidationNodeClassForUnit()
		nodeClass.Spec.CapacityReservationSelectorTerms = []CapacityReservationSelectorTerm{{
			ID:   ptrForUnit("cr-12345"),
			Tags: map[string]string{"env": "test"},
		}}

		require.ErrorContains(t, nodeClass.Validate(), "cannot be combined")
	})
}

func TestECSNodeClassValidateAllowsImageFamilySelector(t *testing.T) {
	nodeClass := validValidationNodeClassForUnit()
	nodeClass.Spec.ImageSelectorTerms = []ImageSelectorTerm{{
		ImageFamily: ptrForUnit("acs:alibaba_cloud_linux_3_2104_lts_x64"),
	}}

	require.NoError(t, nodeClass.Validate())
}

func TestECSNodeClassValidateCapacityReservationIDPrefix(t *testing.T) {
	nodeClass := validValidationNodeClassForUnit()
	nodeClass.Spec.CapacityReservationSelectorTerms = []CapacityReservationSelectorTerm{{ID: ptrForUnit("crp-12345")}}
	require.NoError(t, nodeClass.Validate())

	nodeClass = validValidationNodeClassForUnit()
	nodeClass.Spec.CapacityReservationSelectorTerms = []CapacityReservationSelectorTerm{{ID: ptrForUnit("cr-12345")}}
	require.ErrorContains(t, nodeClass.Validate(), "capacityReservationSelectorTerms[0].id is not a valid capacity reservation ID")
}

func TestECSNodeClassValidateKubeletImageGCThresholds(t *testing.T) {
	high := int32(10)
	low := int32(60)
	nodeClass := validValidationNodeClassForUnit()
	nodeClass.Spec.Kubelet = &KubeletConfiguration{
		ImageGCHighThresholdPercent: &high,
		ImageGCLowThresholdPercent:  &low,
	}

	require.ErrorContains(t, nodeClass.Validate(), "imageGCHighThresholdPercent")

	negative := int32(-1)
	nodeClass = validValidationNodeClassForUnit()
	nodeClass.Spec.Kubelet = &KubeletConfiguration{ImageGCLowThresholdPercent: &negative}

	require.ErrorContains(t, nodeClass.Validate(), "imageGCLowThresholdPercent")
}

func TestECSNodeClassValidateLaunchTemplate(t *testing.T) {
	t.Run("allows launch template without selectors", func(t *testing.T) {
		version := int64(1)
		nodeClass := &ECSNodeClass{
			Spec: ECSNodeClassSpec{
				LaunchTemplateID:      ptrForUnit("lt-12345"),
				LaunchTemplateVersion: &version,
			},
		}

		require.NoError(t, nodeClass.Validate())
	})

	t.Run("rejects selectors and explicit fields that conflict with launch template", func(t *testing.T) {
		nodeClass := validValidationNodeClassForUnit()
		nodeClass.Spec.LaunchTemplateID = ptrForUnit("lt-12345")

		require.ErrorContains(t, nodeClass.Validate(), "launchTemplateID cannot be combined")
	})

	t.Run("rejects version without launch template id", func(t *testing.T) {
		version := int64(1)
		nodeClass := validValidationNodeClassForUnit()
		nodeClass.Spec.LaunchTemplateVersion = &version

		require.ErrorContains(t, nodeClass.Validate(), "launchTemplateVersion requires launchTemplateID")
	})

	t.Run("rejects non-positive version", func(t *testing.T) {
		version := int64(0)
		nodeClass := &ECSNodeClass{
			Spec: ECSNodeClassSpec{
				LaunchTemplateID:      ptrForUnit("lt-12345"),
				LaunchTemplateVersion: &version,
			},
		}

		require.ErrorContains(t, nodeClass.Validate(), "launchTemplateVersion must be greater than 0")
	})
}

func TestECSNodeClassCRDConditionallyRequiresSelectorTerms(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "crds", "karpenter.alibabacloud.com_ecsnodeclasses.yaml"))
	require.NoError(t, err)

	var crd map[string]interface{}
	require.NoError(t, yaml.Unmarshal(contents, &crd))

	specSchema := crd["spec"].(map[string]interface{})["versions"].([]interface{})[0].(map[string]interface{})["schema"].(map[string]interface{})["openAPIV3Schema"].(map[string]interface{})["properties"].(map[string]interface{})["spec"].(map[string]interface{})
	validations, ok := specSchema["x-kubernetes-validations"].([]interface{})
	require.True(t, ok, "ECSNodeClass spec schema must define CEL validation rules")

	var rules []string
	for _, validation := range validations {
		rules = append(rules, validation.(map[string]interface{})["rule"].(string))
	}
	require.Contains(t, rules, "has(self.launchTemplateID) || has(self.vSwitchSelectorTerms)")
	require.Contains(t, rules, "has(self.launchTemplateID) || has(self.securityGroupSelectorTerms)")
	require.Contains(t, rules, "has(self.launchTemplateID) || has(self.imageSelectorTerms)")
}

func TestECSNodeClassValidateRejectsLaunchTemplateConflictByField(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ECSNodeClass)
	}{
		{name: "vswitch selectors", mutate: func(nc *ECSNodeClass) {
			nc.Spec.VSwitchSelectorTerms = []VSwitchSelectorTerm{{ID: ptrForUnit("vsw-12345")}}
		}},
		{name: "security group selectors", mutate: func(nc *ECSNodeClass) {
			nc.Spec.SecurityGroupSelectorTerms = []SecurityGroupSelectorTerm{{ID: ptrForUnit("sg-12345")}}
		}},
		{name: "image selectors", mutate: func(nc *ECSNodeClass) { nc.Spec.ImageSelectorTerms = []ImageSelectorTerm{{ID: ptrForUnit("m-12345")}} }},
		{name: "system disk", mutate: func(nc *ECSNodeClass) { nc.Spec.SystemDisk = &SystemDiskSpec{Category: "cloud_essd"} }},
		{name: "data disks", mutate: func(nc *ECSNodeClass) { nc.Spec.DataDisks = []DataDiskSpec{{Category: "cloud_essd", Size: 20}} }},
		{name: "user data", mutate: func(nc *ECSNodeClass) { nc.Spec.UserData = ptrForUnit("#!/bin/bash") }},
		{name: "spot strategy", mutate: func(nc *ECSNodeClass) { nc.Spec.SpotStrategy = ptrForUnit("SpotAsPriceGo") }},
		{name: "spot price", mutate: func(nc *ECSNodeClass) { nc.Spec.SpotPriceLimit = ptrForUnit(0.5) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeClass := &ECSNodeClass{
				Spec: ECSNodeClassSpec{
					LaunchTemplateID: ptrForUnit("lt-12345"),
				},
			}
			tt.mutate(nodeClass)

			require.ErrorContains(t, nodeClass.Validate(), "launchTemplateID cannot be combined")
		})
	}
}

func TestECSNodeClassValidateRole(t *testing.T) {
	tests := []struct {
		name        string
		role        *string
		expectError bool
	}{
		{
			name: "nil role",
			role: nil,
		},
		{
			name: "valid role",
			role: ptrForUnit("KarpenterNodeRole_1.2-3"),
		},
		{
			name:        "empty role",
			role:        ptrForUnit(""),
			expectError: true,
		},
		{
			name:        "whitespace role",
			role:        ptrForUnit(" KarpenterNodeRole"),
			expectError: true,
		},
		{
			name:        "arn role",
			role:        ptrForUnit("acs:ram::1234567890123456:role/KarpenterNodeRole"),
			expectError: true,
		},
		{
			name:        "malformed role",
			role:        ptrForUnit("Karpenter/NodeRole"),
			expectError: true,
		},
		{
			name:        "over length role",
			role:        ptrForUnit(strings.Repeat("a", 65)),
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeClass := validValidationNodeClassForUnit()
			nodeClass.Spec.Role = tt.role

			err := nodeClass.Validate()
			if tt.expectError && err == nil {
				t.Fatal("expected error")
			}
			if !tt.expectError && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func validValidationNodeClassForUnit() *ECSNodeClass {
	return &ECSNodeClass{
		Spec: ECSNodeClassSpec{
			VSwitchSelectorTerms: []VSwitchSelectorTerm{{
				ID: ptrForUnit("vsw-12345"),
			}},
			SecurityGroupSelectorTerms: []SecurityGroupSelectorTerm{{
				ID: ptrForUnit("sg-12345"),
			}},
			ImageSelectorTerms: []ImageSelectorTerm{{
				ID: ptrForUnit("m-12345"),
			}},
			SystemDisk: &SystemDiskSpec{
				Category: "cloud_essd",
			},
		},
	}
}

func ptrForUnit[T any](v T) *T {
	return &v
}

func TestECSNodeClassValidateMetadataOptions(t *testing.T) {
	tests := []struct {
		name        string
		metadata    *MetadataOptions
		expectError bool
	}{
		{
			name: "valid endpoint disabled",
			metadata: &MetadataOptions{
				HttpEndpoint: ptrForUnit("disabled"),
				HttpTokens:   "optional",
			},
		},
		{
			name: "invalid endpoint",
			metadata: &MetadataOptions{
				HttpEndpoint: ptrForUnit("not-enabled"),
				HttpTokens:   "optional",
			},
			expectError: true,
		},
		{
			name: "invalid tokens",
			metadata: &MetadataOptions{
				HttpTokens: "sometimes",
			},
			expectError: true,
		},
		{
			name: "invalid hop limit",
			metadata: &MetadataOptions{
				HttpTokens:              "optional",
				HttpPutResponseHopLimit: ptrForUnit(int32(65)),
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeClass := validValidationNodeClassForUnit()
			nodeClass.Spec.MetadataOptions = tt.metadata

			err := nodeClass.Validate()
			if tt.expectError && err == nil {
				t.Fatal("expected error")
			}
			if !tt.expectError && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestECSNodeClassValidateSelectorSemantics(t *testing.T) {
	tests := []struct {
		name        string
		mut         func(*ECSNodeClass)
		expectError string
	}{
		{
			name: "rejects empty vswitch term",
			mut: func(nodeClass *ECSNodeClass) {
				nodeClass.Spec.VSwitchSelectorTerms = []VSwitchSelectorTerm{{}}
			},
			expectError: "vSwitchSelectorTerms[0] must specify at least one of: id, tags, or zoneID",
		},
		{
			name: "rejects vswitch id with tags",
			mut: func(nodeClass *ECSNodeClass) {
				nodeClass.Spec.VSwitchSelectorTerms = []VSwitchSelectorTerm{{ID: ptrForUnit("vsw-123"), Tags: map[string]string{"env": "prod"}}}
			},
			expectError: "vSwitchSelectorTerms[0].id is mutually exclusive",
		},
		{
			name: "rejects vswitch empty tag key",
			mut: func(nodeClass *ECSNodeClass) {
				nodeClass.Spec.VSwitchSelectorTerms = []VSwitchSelectorTerm{{Tags: map[string]string{"": "prod"}}}
			},
			expectError: "vSwitchSelectorTerms[0].tags key must be non-empty",
		},
		{
			name: "accepts vswitch zone",
			mut: func(nodeClass *ECSNodeClass) {
				nodeClass.Spec.VSwitchSelectorTerms = []VSwitchSelectorTerm{{ZoneID: ptrForUnit("cn-hangzhou-h")}}
			},
		},
		{
			name: "rejects security group id with name",
			mut: func(nodeClass *ECSNodeClass) {
				nodeClass.Spec.SecurityGroupSelectorTerms = []SecurityGroupSelectorTerm{{ID: ptrForUnit("sg-123"), Name: ptrForUnit("app")}}
			},
			expectError: "securityGroupSelectorTerms[0].id is mutually exclusive",
		},
		{
			name: "rejects security group name with tags",
			mut: func(nodeClass *ECSNodeClass) {
				nodeClass.Spec.SecurityGroupSelectorTerms = []SecurityGroupSelectorTerm{{Name: ptrForUnit("app"), Tags: map[string]string{"env": "prod"}}}
			},
			expectError: "securityGroupSelectorTerms[0].name is mutually exclusive",
		},
		{
			name: "allows more than five security group selector terms",
			mut: func(nodeClass *ECSNodeClass) {
				nodeClass.Spec.SecurityGroupSelectorTerms = []SecurityGroupSelectorTerm{
					{ID: ptrForUnit("sg-1")}, {ID: ptrForUnit("sg-2")}, {ID: ptrForUnit("sg-3")},
					{ID: ptrForUnit("sg-4")}, {ID: ptrForUnit("sg-5")}, {ID: ptrForUnit("sg-6")},
				}
			},
		},
		{
			name: "rejects image owner ID as only selector",
			mut: func(nodeClass *ECSNodeClass) {
				nodeClass.Spec.ImageSelectorTerms = []ImageSelectorTerm{{ImageOwnerID: ptrForUnit("1234567890123456")}}
			},
			expectError: "imageSelectorTerms[0].imageOwnerID cannot be the only selector",
		},
		{
			name: "rejects invalid image owner ID",
			mut: func(nodeClass *ECSNodeClass) {
				nodeClass.Spec.ImageSelectorTerms = []ImageSelectorTerm{{Name: ptrForUnit("alinux"), ImageOwnerID: ptrForUnit("owner-123")}}
			},
			expectError: "imageSelectorTerms[0].imageOwnerID must match",
		},
		{
			name: "rejects image id with family",
			mut: func(nodeClass *ECSNodeClass) {
				nodeClass.Spec.ImageSelectorTerms = []ImageSelectorTerm{{ID: ptrForUnit("m-123"), ImageFamily: ptrForUnit("aliyun_3")}}
			},
			expectError: "imageSelectorTerms[0].id is mutually exclusive",
		},
		{
			name: "rejects invalid image owner alias",
			mut: func(nodeClass *ECSNodeClass) {
				nodeClass.Spec.ImageSelectorTerms = []ImageSelectorTerm{{ImageOwnerAlias: ptrForUnit("public")}}
			},
			expectError: "imageSelectorTerms[0].imageOwnerAlias must be one of: system, self, others, marketplace",
		},
		{
			name: "accepts image family with owner ID",
			mut: func(nodeClass *ECSNodeClass) {
				nodeClass.Spec.ImageSelectorTerms = []ImageSelectorTerm{{ImageFamily: ptrForUnit("aliyun_3"), ImageOwnerID: ptrForUnit("1234567890123456")}}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeClass := validValidationNodeClassForUnit()
			tt.mut(nodeClass)

			err := nodeClass.Validate()
			if tt.expectError != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", tt.expectError)
				}
				if !strings.Contains(err.Error(), tt.expectError) {
					t.Fatalf("expected error containing %q, got %q", tt.expectError, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestECSNodeClassValidateDiskOptions(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*ECSNodeClass)
	}{
		{
			name: "rejects non ESSD system disk performance level",
			mut: func(nodeClass *ECSNodeClass) {
				nodeClass.Spec.SystemDisk = &SystemDiskSpec{Category: "cloud_ssd", Size: ptrForUnit(int32(40)), PerformanceLevel: ptrForUnit("PL1")}
			},
		},
		{
			name: "rejects system disk kms without encryption",
			mut: func(nodeClass *ECSNodeClass) {
				nodeClass.Spec.SystemDisk = &SystemDiskSpec{Category: "cloud_essd", Size: ptrForUnit(int32(40)), KMSKeyID: ptrForUnit("kms-1")}
			},
		},
		{
			name: "rejects non ESSD data disk performance level",
			mut: func(nodeClass *ECSNodeClass) {
				nodeClass.Spec.DataDisks = []DataDiskSpec{{Category: "cloud_ssd", Size: 40, PerformanceLevel: ptrForUnit("PL1")}}
			},
		},
		{
			name: "rejects data disk kms without encryption",
			mut: func(nodeClass *ECSNodeClass) {
				nodeClass.Spec.DataDisks = []DataDiskSpec{{Category: "cloud_essd", Size: 40, KMSKeyID: ptrForUnit("kms-1")}}
			},
		},
		{
			name: "rejects invalid instance store policy",
			mut: func(nodeClass *ECSNodeClass) {
				nodeClass.Spec.InstanceStorePolicy = ptrForUnit("None")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeClass := validValidationNodeClassForUnit()
			tt.mut(nodeClass)

			if err := nodeClass.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestECSNodeClassDefaultMetadataOptions(t *testing.T) {
	t.Run("omitted parent remains nil", func(t *testing.T) {
		nodeClass := validValidationNodeClassForUnit()

		if err := nodeClass.Default(context.Background(), nodeClass); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if nodeClass.Spec.MetadataOptions != nil {
			t.Fatalf("expected metadataOptions to remain nil, got %#v", nodeClass.Spec.MetadataOptions)
		}
	})

	t.Run("provided parent defaults existing child defaults without defaulting endpoint", func(t *testing.T) {
		nodeClass := validValidationNodeClassForUnit()
		nodeClass.Spec.MetadataOptions = &MetadataOptions{}

		if err := nodeClass.Default(context.Background(), nodeClass); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if nodeClass.Spec.MetadataOptions.HttpTokens != "optional" {
			t.Fatalf("expected HttpTokens optional, got %q", nodeClass.Spec.MetadataOptions.HttpTokens)
		}
		if nodeClass.Spec.MetadataOptions.HttpPutResponseHopLimit == nil || *nodeClass.Spec.MetadataOptions.HttpPutResponseHopLimit != 1 {
			t.Fatalf("expected HttpPutResponseHopLimit 1, got %#v", nodeClass.Spec.MetadataOptions.HttpPutResponseHopLimit)
		}
		if nodeClass.Spec.MetadataOptions.HttpEndpoint != nil {
			t.Fatalf("expected HttpEndpoint to remain nil, got %q", *nodeClass.Spec.MetadataOptions.HttpEndpoint)
		}
	})
}
