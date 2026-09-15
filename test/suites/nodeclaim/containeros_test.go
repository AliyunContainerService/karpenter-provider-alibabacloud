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

// ContainerOS integration test for GitHub issue #13
//
// This test verifies that Karpenter can successfully provision nodes using
// ContainerOS (LifseaOS) images. ContainerOS images are hidden by default
// in the ECS DescribeImages API response unless ShowExpired=true is explicitly
// set. This test exercises the complete path:
//
// 1. NodeClass specifies a ContainerOS image by ID (lifsea_3_x64_5G_alibase_20260519.qcow2)
// 2. ImageFamilyProvider calls DescribeImages with ShowExpired=true
// 3. ECS API returns the ContainerOS image (Status=Available)
// 4. Karpenter provisions a node using the ContainerOS image
// 5. Node becomes Ready and the pod is scheduled
//
// Without the ShowExpired=true fix (GH issue #13), step 2 would return 0 images
// and the NodeClass would fail to resolve, causing node provisioning to fail.

const (
	// LifseaOS 3 ContainerOS image ID for x86_64 architecture
	// This is a system image that requires ShowExpired=true to query via ECS API
	containerOSImageID = "lifsea_3_x64_5G_alibase_20260519.qcow2"
)

// configureContainerOSImages points the NodeClass to use ContainerOS image by ID
func configureContainerOSImages() {
	nodeClass.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{
		{ID: lo.ToPtr(containerOSImageID)},
	}
}

// configureContainerOSPool sets up a NodePool for ContainerOS testing
// Uses standard x86_64 instance types (not ARM, not GPU)
func configureContainerOSPool() {
	nodePool.Spec.Template.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{
		{
			NodeSelectorRequirement: corev1.NodeSelectorRequirement{
				Key:      v1alpha1.LabelCapacityType,
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{v1alpha1.CapacityTypeOnDemand},
			},
		},
		{
			NodeSelectorRequirement: corev1.NodeSelectorRequirement{
				Key:      corev1.LabelArchStable,
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{v1alpha1.ArchitectureAmd64},
			},
		},
		{
			NodeSelectorRequirement: corev1.NodeSelectorRequirement{
				Key:      corev1.LabelInstanceTypeStable,
				Operator: corev1.NodeSelectorOpIn,
				// Use common x86_64 instance types available in cn-hangzhou
				Values: []string{
					"ecs.g7.large", "ecs.g7.xlarge",
					"ecs.c7.large", "ecs.c7.xlarge",
					"ecs.g6.large", "ecs.g6.xlarge",
				},
			},
		},
	}
}

func containerOSTestPod() *corev1.Pod {
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

var _ = Describe("ContainerOS", Label("containeros"), func() {
	// Test: ContainerOS (LifseaOS) end-to-end provisioning
	//
	// This test verifies the fix for GitHub issue #13:
	// ContainerOS images require ShowExpired=true in DescribeImages API
	It("should provision a node using ContainerOS (LifseaOS) image", Label("lifsea"), func() {
		configureContainerOSPool()
		configureContainerOSImages()

		pod := containerOSTestPod()

		By("creating NodeClass with ContainerOS image ID, NodePool and a pod")
		env.ExpectCreated(nodeClass, nodePool, pod)

		By("waiting for the pod to become healthy on the new ContainerOS node")
		env.EventuallyExpectHealthy(pod)

		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]

		By("verifying the node is x86_64 architecture (ContainerOS image arch)")
		Expect(node.Labels).To(HaveKeyWithValue(corev1.LabelArchStable, v1alpha1.ArchitectureAmd64),
			"node arch label must be amd64, got %q", node.Labels[corev1.LabelArchStable])

		By("verifying the instance-type is a standard x86_64 type")
		it := node.Labels[corev1.LabelInstanceTypeStable]
		Expect(it).To(SatisfyAny(
			HavePrefix("ecs.g7"), HavePrefix("ecs.c7"),
			HavePrefix("ecs.g6"),
		), "expected a standard x86_64 instance-type, got %q", it)

		By("verifying kubelet reports x86_64 in NodeInfo.Architecture")
		Expect(node.Status.NodeInfo.Architecture).To(Equal(v1alpha1.ArchitectureAmd64))

		By("verifying the node OS image contains Lifsea (ContainerOS)")
		// NodeInfo.OSImage typically contains "ContainerOS" or "Lifsea"
		// This is the key assertion that proves ContainerOS was successfully used
		Expect(node.Status.NodeInfo.OSImage).To(Or(
			ContainSubstring("Lifsea"),
			ContainSubstring("ContainerOS"),
			ContainSubstring("lifsea"),
		), "node OS image should contain Lifsea/ContainerOS, got %q", node.Status.NodeInfo.OSImage)
	})
})
