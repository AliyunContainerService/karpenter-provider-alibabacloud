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

package drift

import (
	"os"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/labels"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	. "github.com/onsi/ginkgo/v2"
)

var _ = Describe("NodeClass Drift Reasons", func() {
	It("should detect security group drift", Label("security-group"), func() {
		nodeClaim, selector := launchDriftSubject("security-group")
		nodeClass.Spec.SecurityGroupSelectorTerms = append(nodeClass.Spec.SecurityGroupSelectorTerms, v1alpha1.SecurityGroupSelectorTerm{
			Tags: map[string]string{"drift/security-group": "true"},
		})
		env.ExpectUpdated(nodeClass)
		env.EventuallyExpectDrifted(nodeClaim)
		env.EventuallyExpectHealthyPodCount(selector, 1)
	})

	It("should detect vswitch subnet drift", Label("subnet"), Label("vswitch"), func() {
		nodeClaim, selector := launchDriftSubject("vswitch")
		nodeClass.Spec.VSwitchSelectorTerms = append(nodeClass.Spec.VSwitchSelectorTerms, v1alpha1.VSwitchSelectorTerm{
			Tags: map[string]string{"drift/vswitch": "true"},
		})
		env.ExpectUpdated(nodeClass)
		env.EventuallyExpectDrifted(nodeClaim)
		env.EventuallyExpectHealthyPodCount(selector, 1)
	})

	It("should detect RAM role instance profile drift", Label("role"), Label("instance-profile"), Label("ram"), func() {
		nodeClaim, selector := launchDriftSubject("ram-role")
		role := os.Getenv("TEST_RAM_ROLE")
		if role == "" {
			role = "karpenter-drift-e2e-role"
		}
		nodeClass.Spec.Role = lo.ToPtr(role + "-drift")
		env.ExpectUpdated(nodeClass)
		env.EventuallyExpectDrifted(nodeClaim)
		env.EventuallyExpectHealthyPodCount(selector, 1)
	})

	It("should detect block device system disk drift", Label("block-device"), Label("disk"), func() {
		nodeClaim, selector := launchDriftSubject("system-disk")
		nodeClass.Spec.SystemDisk.Size = lo.ToPtr(int32(80))
		env.ExpectUpdated(nodeClass)
		env.EventuallyExpectDrifted(nodeClaim)
		env.EventuallyExpectHealthyPodCount(selector, 1)
	})

	It("should detect data disk drift", Label("block-device"), Label("disk"), func() {
		nodeClaim, selector := launchDriftSubject("data-disk")
		nodeClass.Spec.DataDisks = append(nodeClass.Spec.DataDisks, v1alpha1.DataDiskSpec{
			Category:         "cloud_essd",
			Size:             120,
			PerformanceLevel: lo.ToPtr("PL0"),
		})
		env.ExpectUpdated(nodeClass)
		env.EventuallyExpectDrifted(nodeClaim)
		env.EventuallyExpectHealthyPodCount(selector, 1)
	})

	It("should detect hash drift when tags change", Label("hash"), func() {
		nodeClaim, selector := launchDriftSubject("hash")
		nodeClass.Spec.Tags["drift/hash"] = "changed"
		env.ExpectUpdated(nodeClass)
		env.EventuallyExpectDrifted(nodeClaim)
		env.EventuallyExpectHealthyPodCount(selector, 1)
	})

	It("should detect image drift from NodeClass hash changes", Label("image"), Label("ami"), func() {
		nodeClaim, selector := launchDriftSubject("image")
		nodeClass.Spec.ImageSelectorTerms = append(nodeClass.Spec.ImageSelectorTerms, v1alpha1.ImageSelectorTerm{
			ImageFamily: lo.ToPtr("acs:alibaba_cloud_linux_3_2104_lts_x64"),
		})
		env.ExpectUpdated(nodeClass)
		env.EventuallyExpectDrifted(nodeClaim)
		env.EventuallyExpectHealthyPodCount(selector, 1)
	})

	It("should exercise capacity reservation drift preconditions", Label("reservation"), Label("capacity-reservation"), func() {
		capacityReservationID := os.Getenv("TEST_CAPACITY_RESERVATION_ID")
		if capacityReservationID == "" {
			Skip("capacity reservation drift requires TEST_CAPACITY_RESERVATION_ID")
		}
		configureNodeClass("capacity-reservation", map[string]string{"drift/subject": "capacity-reservation"})
		configureNodePool("capacity-reservation")
		restrictNodePoolInstanceTypes(capacityReservationInstanceTypes()...)
		preference := "target"
		nodeClass.Spec.CapacityReservationPreference = &preference
		nodeClass.Spec.CapacityReservationSelectorTerms = []v1alpha1.CapacityReservationSelectorTerm{{ID: &capacityReservationID}}
		deployment := driftDeployment("capacity-reservation", 1)
		env.ExpectCreated(nodePool, nodeClass, deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectHealthyPodCount(selector, 1)
		nodeClaims := env.EventuallyExpectCreatedNodeClaimCount("==", 1)
		nodeClaim := nodeClaims[0]

		preference = "none"
		nodeClass.Spec.CapacityReservationPreference = &preference
		nodeClass.Spec.CapacityReservationSelectorTerms = nil
		env.ExpectUpdated(nodeClass)
		env.EventuallyExpectDrifted(nodeClaim)
		env.EventuallyExpectHealthyPodCount(selector, 1)
	})

	It("should detect kubelet hash drift", Label("hash"), func() {
		nodeClaim, selector := launchDriftSubject("kubelet-hash")
		maxPods := int32(30)
		nodeClass.Spec.Kubelet = &v1alpha1.KubeletConfiguration{MaxPods: &maxPods}
		env.ExpectUpdated(nodeClass)
		env.EventuallyExpectDrifted(nodeClaim)
		env.EventuallyExpectHealthyPodCount(selector, 1)
	})
})

func launchDriftSubject(name string) (*karpv1.NodeClaim, labels.Selector) {
	configureNodeClass(name, map[string]string{"drift/subject": name})
	configureNodePool(name)
	deployment := driftDeployment(name, 1)
	env.ExpectCreated(nodePool, nodeClass, deployment)
	selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
	env.EventuallyExpectHealthyPodCount(selector, 1)
	nodeClaims := env.EventuallyExpectCreatedNodeClaimCount("==", 1)
	return nodeClaims[0], selector
}
