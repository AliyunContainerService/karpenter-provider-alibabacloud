//go:build integration

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

package nodeclaim

import (
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"
)

// These integration tests pin the end-to-end capacity-type behaviour.
//
// Background: Alibaba Cloud spot instances keep InstanceChargeType=PostPaid and are
// distinguished ONLY by SpotStrategy. A previous bug classified PostPaid first and
// therefore mislabelled spot instances as on-demand. The unit tests in
// pkg/utils/ecs and pkg/providers/instance already pin the parsing (spot-first);
// these integration tests assert the real cluster behaviour in both directions:
//
//   - Test C1 (on-demand negative): request on-demand and assert the node is
//     labelled on-demand and NEVER spot. This guards against a regression where
//     spot capacity would leak into an on-demand request.
//
//   - Test C2 (spot positive, SpotWithPriceLimit): request spot with an explicit
//     price ceiling and assert the node is labelled spot. This exercises the
//     SpotStrategyForCapacityType path with a user-provided SpotWithPriceLimit
//     strategy (the interruption suite only covers the default SpotAsPriceGo).

func configureCapacityPool(capacityType string, instanceTypes []string) {
	nodePool.Spec.Template.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{
		{
			NodeSelectorRequirement: corev1.NodeSelectorRequirement{
				Key:      v1alpha1.LabelCapacityType,
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{capacityType},
			},
		},
		{
			NodeSelectorRequirement: corev1.NodeSelectorRequirement{
				Key:      corev1.LabelInstanceTypeStable,
				Operator: corev1.NodeSelectorOpIn,
				Values:   instanceTypes,
			},
		},
	}
}

func capacityTestPod() *corev1.Pod {
	return coretest.Pod(coretest.PodOptions{
		Image: "registry-cn-hangzhou.ack.aliyuncs.com/acs/pause:3.9",
		ResourceRequirements: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
		},
		NodeSelector: map[string]string{
			karpv1.NodePoolLabelKey: nodePool.Name,
		},
	})
}

var _ = Describe("CapacityType", Label("capacity-type"), func() {
	// Test C1: on-demand request must never yield a spot node.
	It("should provision an on-demand node and never label it spot", Label("on-demand"), func() {
		configureCapacityPool(v1alpha1.CapacityTypeOnDemand, []string{"ecs.g7.xlarge", "ecs.g7.2xlarge"})

		pod := capacityTestPod()

		By("creating NodeClass, NodePool and an on-demand-targeted pod")
		env.ExpectCreated(nodeClass, nodePool, pod)

		By("waiting for the pod to become healthy")
		env.EventuallyExpectHealthy(pod)

		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]

		By("verifying karpenter.sh/capacity-type == on-demand (and NOT spot)")
		Expect(node.Labels).To(HaveKeyWithValue(v1alpha1.LabelCapacityType, v1alpha1.CapacityTypeOnDemand),
			"capacity-type must be on-demand, got %q", node.Labels[v1alpha1.LabelCapacityType])
		Expect(node.Labels[v1alpha1.LabelCapacityType]).ToNot(Equal(v1alpha1.CapacityTypeSpot),
			"on-demand request must never produce a spot node (spot-misclassification regression)")
	})

	// Test C2: spot request with SpotWithPriceLimit must yield a spot node.
	It("should provision a spot node with SpotWithPriceLimit strategy", Label("spot"), func() {
		// Configure the NodeClass to use an explicit spot price ceiling.
		nodeClass.Spec.SpotStrategy = lo.ToPtr("SpotWithPriceLimit")
		nodeClass.Spec.SpotPriceLimit = lo.ToPtr(2.0) // generous ceiling ($/hr) to avoid no-capacity

		configureCapacityPool(v1alpha1.CapacityTypeSpot, []string{"ecs.g7.xlarge", "ecs.g7.2xlarge", "ecs.c7.xlarge"})

		pod := capacityTestPod()

		By("creating NodeClass (SpotWithPriceLimit), NodePool and a spot-targeted pod")
		env.ExpectCreated(nodeClass, nodePool, pod)

		By("waiting for the pod to become healthy on the new spot node")
		env.EventuallyExpectHealthy(pod)

		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]

		By("verifying karpenter.sh/capacity-type == spot")
		Expect(node.Labels).To(HaveKeyWithValue(v1alpha1.LabelCapacityType, v1alpha1.CapacityTypeSpot),
			"capacity-type must be spot, got %q", node.Labels[v1alpha1.LabelCapacityType])
	})
})
