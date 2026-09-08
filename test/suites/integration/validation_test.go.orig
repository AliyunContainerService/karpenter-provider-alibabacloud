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

package integration

import (
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	corev1 "k8s.io/api/core/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ECSNodeClass Validation", func() {
	It("should error when imageSelectorTerms are not defined", Label("validation"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.ImageSelectorTerms = nil

		Expect(nodeClass.Validate()).To(MatchError(ContainSubstring("imageSelectorTerms is required")))
	})

	It("should fail for poorly formatted image ids", Label("validation"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{{ID: ptr("must-start-with-m")}}

		Expect(nodeClass.Validate()).To(MatchError(ContainSubstring("not a valid image ID")))
	})

	It("should succeed when tags do not contain restricted keys", Label("validation"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.Tags = map[string]string{
			"karpenter.sh/custom-key":   "custom-value",
			"kubernetes.io/role/custom": "custom-value",
		}

		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should error when tags contain restricted keys", Label("validation"), func() {
		restrictedKeys := []string{
			v1alpha1.TagNodePool,
			v1alpha1.TagNodeClaim,
			v1alpha1.TagManagedBy,
			v1alpha1.TagClusterID,
			v1alpha1.TagKubeletMaxPods,
			v1alpha1.TagCluster,
		}
		for _, key := range restrictedKeys {
			nodeClass := validValidationNodeClass()
			nodeClass.Spec.Tags = map[string]string{key: "custom-value"}

			Expect(nodeClass.Validate()).To(MatchError(ContainSubstring("restricted key")))
		}
	})

	It("should fail when securityGroupSelectorTerms has id and other filters", Label("validation", "security-group"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.SecurityGroupSelectorTerms = []v1alpha1.SecurityGroupSelectorTerm{{
			ID:   ptr("sg-12345"),
			Tags: map[string]string{"karpenter.sh/discovery": "test"},
		}}

		Expect(nodeClass.Validate()).To(MatchError(ContainSubstring("cannot be combined")))
	})

	It("should fail when vSwitchSelectorTerms has id and other filters", Label("validation", "vswitch"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.VSwitchSelectorTerms = []v1alpha1.VSwitchSelectorTerm{{
			ID:     ptr("vsw-12345"),
			ZoneID: ptr("cn-test-a"),
		}}

		Expect(nodeClass.Validate()).To(MatchError(ContainSubstring("cannot be combined")))
	})

	It("should fail when imageSelectorTerms has id and other filters", Label("validation"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{{
			ID:   ptr("m-12345"),
			Tags: map[string]string{"karpenter.sh/discovery": "test"},
		}}

		Expect(nodeClass.Validate()).To(MatchError(ContainSubstring("cannot be combined")))
	})

	It("should validate block device and data disk configuration", Label("validation", "block-device", "data-disk"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.SystemDisk = &v1alpha1.SystemDiskSpec{Category: "cloud_bad"}
		Expect(nodeClass.Validate()).To(MatchError(ContainSubstring("systemDisk.category")))

		nodeClass = validValidationNodeClass()
		nodeClass.Spec.DataDisks = []v1alpha1.DataDiskSpec{{Category: "cloud_essd", Size: 10}}
		Expect(nodeClass.Validate()).To(MatchError(ContainSubstring("dataDisks[0].size")))
	})

	It("should validate metadata options and capacity reservation selectors", Label("validation", "metadata-options", "capacity-reservation"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.MetadataOptions = &v1alpha1.MetadataOptions{HttpTokens: "invalid"}
		Expect(nodeClass.Validate()).To(MatchError(ContainSubstring("metadataOptions.httpTokens")))

		nodeClass = validValidationNodeClass()
		nodeClass.Spec.CapacityReservationSelectorTerms = []v1alpha1.CapacityReservationSelectorTerm{{ID: ptr("cr-12345"), Tags: map[string]string{"env": "test"}}}
		Expect(nodeClass.Validate()).To(MatchError(ContainSubstring("cannot be combined")))
	})

	It("should error if imageGCHighThresholdPercent is less than imageGCLowThresholdPercent", Label("validation", "kubelet"), func() {
		nodeClass := validValidationNodeClass()
		high := int32(10)
		low := int32(60)
		nodeClass.Spec.Kubelet = &v1alpha1.KubeletConfiguration{
			ImageGCHighThresholdPercent: &high,
			ImageGCLowThresholdPercent:  &low,
		}

		Expect(nodeClass.Validate()).To(MatchError(ContainSubstring("imageGCHighThresholdPercent")))
	})

	It("should error if imageGCHighThresholdPercent or imageGCLowThresholdPercent is negative", Label("validation", "kubelet"), func() {
		negative := int32(-10)

		nodeClass := validValidationNodeClass()
		nodeClass.Spec.Kubelet = &v1alpha1.KubeletConfiguration{ImageGCHighThresholdPercent: &negative}
		Expect(nodeClass.Validate()).To(MatchError(ContainSubstring("imageGCHighThresholdPercent")))

		nodeClass = validValidationNodeClass()
		nodeClass.Spec.Kubelet = &v1alpha1.KubeletConfiguration{ImageGCLowThresholdPercent: &negative}
		Expect(nodeClass.Validate()).To(MatchError(ContainSubstring("imageGCLowThresholdPercent")))
	})

	It("should validate launch template selector exclusivity and version", Label("validation", "launch-template"), func() {
		version := int64(1)
		nodeClass := &v1alpha1.ECSNodeClass{
			Spec: v1alpha1.ECSNodeClassSpec{
				LaunchTemplateID:      ptr("lt-12345"),
				LaunchTemplateVersion: &version,
			},
		}
		Expect(nodeClass.Validate()).To(Succeed())

		nodeClass = validValidationNodeClass()
		nodeClass.Spec.LaunchTemplateID = ptr("lt-12345")
		Expect(nodeClass.Validate()).To(MatchError(ContainSubstring("launchTemplateID cannot be combined")))

		nodeClass = validValidationNodeClass()
		nodeClass.Spec.LaunchTemplateVersion = &version
		Expect(nodeClass.Validate()).To(MatchError(ContainSubstring("launchTemplateVersion requires launchTemplateID")))
	})
})

func validValidationNodeClass() *v1alpha1.ECSNodeClass {
	return &v1alpha1.ECSNodeClass{
		Spec: v1alpha1.ECSNodeClassSpec{
			VSwitchSelectorTerms: []v1alpha1.VSwitchSelectorTerm{{
				ID: ptr("vsw-12345"),
			}},
			SecurityGroupSelectorTerms: []v1alpha1.SecurityGroupSelectorTerm{{
				ID: ptr("sg-12345"),
			}},
			ImageSelectorTerms: []v1alpha1.ImageSelectorTerm{{
				ID: ptr("m-12345"),
			}},
			SystemDisk: &v1alpha1.SystemDiskSpec{
				Category: "cloud_essd",
			},
			Kubelet: &v1alpha1.KubeletConfiguration{
				SystemReserved: map[corev1.ResourceName]string{corev1.ResourceCPU: "100m"},
			},
		},
	}
}

func ptr[T any](v T) *T {
	return &v
}
