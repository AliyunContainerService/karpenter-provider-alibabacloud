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

var _ = Describe("ECSNodeClass Additional Integration Contracts", func() {
	It("should validate explicit security group selector IDs", Label("validation", "security-group"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.SecurityGroupSelectorTerms = []v1alpha1.SecurityGroupSelectorTerm{{ID: ptr("sg-12345")}}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate explicit vswitch selector IDs", Label("validation", "vswitch"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.VSwitchSelectorTerms = []v1alpha1.VSwitchSelectorTerm{{ID: ptr("vsw-12345")}}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate image selector IDs", Label("validation"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{{ID: ptr("m-12345")}}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate system disk ESSD configuration", Label("validation", "block-device", "disk"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.SystemDisk = &v1alpha1.SystemDiskSpec{Category: "cloud_essd", Size: ptr(int32(40)), PerformanceLevel: ptr("PL0")}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate data disk ESSD configuration", Label("validation", "block-device", "data-disk", "disk"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.DataDisks = []v1alpha1.DataDiskSpec{{Category: "cloud_essd", Size: 120, PerformanceLevel: ptr("PL0")}}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate metadata hop limit", Label("validation", "metadata"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.MetadataOptions = &v1alpha1.MetadataOptions{HttpTokens: "optional", HttpPutResponseHopLimit: ptr(int32(2))}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate required metadata tokens", Label("validation", "metadata"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.MetadataOptions = &v1alpha1.MetadataOptions{HttpTokens: "required"}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate kubelet maxPods", Label("validation", "kubelet"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.Kubelet.MaxPods = ptr(int32(20))
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate kubelet system reserved CPU", Label("validation", "kubelet"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.Kubelet.SystemReserved = map[corev1.ResourceName]string{corev1.ResourceCPU: "100m"}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate kubelet kube reserved memory", Label("validation", "kubelet"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.Kubelet.KubeReserved = map[corev1.ResourceName]string{corev1.ResourceMemory: "128Mi"}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate kubelet eviction hard", Label("validation", "kubelet"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.Kubelet.EvictionHard = map[string]string{"memory.available": "5%"}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate kubelet eviction soft", Label("validation", "kubelet"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.Kubelet.EvictionSoft = map[string]string{"memory.available": "10%"}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate kubelet eviction soft grace period", Label("validation", "kubelet"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.Kubelet.EvictionSoftGracePeriod = map[string]string{"memory.available": "1m"}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate image garbage collection thresholds", Label("validation", "kubelet"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.Kubelet.ImageGCLowThresholdPercent = ptr(int32(40))
		nodeClass.Spec.Kubelet.ImageGCHighThresholdPercent = ptr(int32(70))
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate nodeclass tags", Label("validation", "tags"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.Tags = map[string]string{"app": "karpenter-e2e"}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate launch template only nodeclasses", Label("validation", "launch-template"), func() {
		version := int64(1)
		nodeClass := &v1alpha1.ECSNodeClass{Spec: v1alpha1.ECSNodeClassSpec{LaunchTemplateID: ptr("lt-12345"), LaunchTemplateVersion: &version}}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate RAM role settings", Label("validation", "ram-role", "role"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.Role = ptr("KarpenterNodeRole")
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate capacity reservation selector IDs", Label("validation", "capacity-reservation"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.CapacityReservationSelectorTerms = []v1alpha1.CapacityReservationSelectorTerm{{ID: ptr("crp-12345")}}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate capacity reservation selector tags", Label("validation", "capacity-reservation"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.CapacityReservationSelectorTerms = []v1alpha1.CapacityReservationSelectorTerm{{Tags: map[string]string{"env": "e2e"}}}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate spot strategy", Label("validation", "spot"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.SpotStrategy = ptr("SpotAsPriceGo")
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate spot price limit", Label("validation", "spot"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.SpotPriceLimit = ptr(0.5)
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate security group tag selectors", Label("validation", "security-group"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.SecurityGroupSelectorTerms = []v1alpha1.SecurityGroupSelectorTerm{{Tags: map[string]string{"karpenter.sh/discovery": "e2e"}}}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate vswitch tag selectors", Label("validation", "vswitch"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.VSwitchSelectorTerms = []v1alpha1.VSwitchSelectorTerm{{Tags: map[string]string{"karpenter.sh/discovery": "e2e"}}}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate image tag selectors", Label("validation"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{{Tags: map[string]string{"image": "e2e"}}}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate image family selectors", Label("validation"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{{ImageFamily: ptr("acs:alibaba_cloud_linux_3_2104_lts_x64")}}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate cluster metadata fields", Label("validation", "nodeclass"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.ClusterID = "c-test"
		nodeClass.Spec.ClusterName = "karpenter-alibabacloud-e2e"
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate custom userdata", Label("validation", "userdata"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.UserData = ptr("#!/bin/bash\necho ok\n")
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate network interface metadata", Label("validation", "network-interface", "eni"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.Tags = map[string]string{"eni-test": "true"}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate nodeclass hash inputs", Label("validation", "hash"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.Tags = map[string]string{"hash-test": "true"}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate repair-ready nodeclasses", Label("validation", "repair"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.Tags = map[string]string{"repair-test": "true"}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate extended resource nodeclasses", Label("validation", "extended-resources", "gpu", "accelerator"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.Tags = map[string]string{"gpu-test": "true"}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate CNI maxpods nodeclasses", Label("validation", "cni", "terway", "maxpods"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.Kubelet.MaxPods = ptr(int32(30))
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate block device performance level", Label("validation", "block-device", "disk"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.SystemDisk.PerformanceLevel = ptr("PL1")
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate data disk delete with instance", Label("validation", "data-disk", "disk"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.DataDisks = []v1alpha1.DataDiskSpec{{Category: "cloud_essd", Size: 120, DeleteWithInstance: ptr(true)}}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate multiple security group terms", Label("validation", "security-group"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.SecurityGroupSelectorTerms = []v1alpha1.SecurityGroupSelectorTerm{{ID: ptr("sg-1")}, {ID: ptr("sg-2")}}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate multiple vswitch terms", Label("validation", "vswitch"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.VSwitchSelectorTerms = []v1alpha1.VSwitchSelectorTerm{{ID: ptr("vsw-1")}, {ID: ptr("vsw-2")}}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate multiple image terms", Label("validation"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{{ID: ptr("m-12345")}, {ImageFamily: ptr("acs:alibaba_cloud_linux_3_2104_lts_x64")}}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate nodeclass ownership tags", Label("validation", "tags", "nodeclass"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.Tags = map[string]string{"testing/cluster": "karpenter-alibabacloud-e2e"}
		Expect(nodeClass.Validate()).To(Succeed())
	})

	It("should validate empty optional data disks", Label("validation", "data-disk"), func() {
		nodeClass := validValidationNodeClass()
		nodeClass.Spec.DataDisks = nil
		Expect(nodeClass.Validate()).To(Succeed())
	})
})
