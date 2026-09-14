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

// These integration tests pin the end-to-end architecture-resolution behaviour
// that the unit tests in pkg/providers/instancetype and pkg/cloudprovider can only
// partially cover (the UT use mocked ECS responses; here we assert against a real
// ACK cluster and real ECS instances).
//
//   - Test A (arm64): request kubernetes.io/arch=arm64 pinned to a Yitian 710
//     (g8y) family and assert the provisioned node really comes up as arm64. This
//     exercises the authoritative path: instancetype.convertECSInstanceType reads
//     CpuArchitecture from DescribeInstanceTypes -> ecsutil.KubeArchitecture, and
//     cloudprovider.resolveArchitecture backfills the node label from the matched
//     instance type.
//
//   - Test B (GPU regression): request a GPU family (ecs.gn*) and assert the node
//     is amd64, NOT arm64. A previous name-prefix heuristic wrongly mapped
//     ecs.gn*/ecs.cu* to arm64; resolveArchitecture must now use the authoritative
//     instance-type architecture and classify GPU instances as amd64.

// arm64ImageFamily is the Alibaba Cloud Linux 3 container-optimized image family
// for aarch64. The default NodeClass only resolves the x86_64 family, so arm64
// tests must point the NodeClass at an arm64 image family; otherwise ECS rejects
// the RunInstances call with InvalidInstanceType.NotSupported (x86 image on an
// arm64 instance type).
const arm64ImageFamily = "acs:alibaba_cloud_linux_3_2104_arm64_container_optimized"

// configureArchImages points the NodeClass at an image family matching arch.
// For arm64 it selects the arm64 container-optimized family; for amd64 it leaves
// the default (x86_64) image selector terms untouched.
func configureArchImages(arch string) {
	if arch == v1alpha1.ArchitectureArm64 {
		nodeClass.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{
			{ImageFamily: lo.ToPtr(arm64ImageFamily)},
		}
	}
}

// configureArchPool rewrites the default NodePool so it selects a specific arch
// and a specific set of instance-type families, replacing the default
// on-demand/ecs.g7.xlarge requirements.
func configureArchPool(arch string, instanceTypes []string) {
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
				Values:   []string{arch},
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

func archTestPod() *corev1.Pod {
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

var _ = Describe("Architecture", Label("arch"), func() {
	// Test A: arm64 end-to-end on a Yitian 710 (g8y) family instance.
	It("should provision an arm64 node when arch=arm64 is requested (Yitian g8y)", Label("arm64"), func() {
		// ecs.g8yi.large / ecs.g8y.large are Yitian 710 (aarch64). We offer a few
		// sizes/families so the scheduler has capacity headroom in the target zone.
		// Offer a broad set of arm64 families spanning the cluster's zones. The
		// Ampere Altra families (g6r/c6r) are sellable in more Hangzhou zones than
		// the Yitian 710 families (g8y/c8y/r8y), which keeps this test resilient to
		// per-zone stock/on-sale differences; the vSwitch/zone fallback then lands
		// the instance in whichever zone actually has capacity.
		configureArchPool(v1alpha1.ArchitectureArm64, []string{
			"ecs.g6r.large", "ecs.g6r.xlarge",
			"ecs.c6r.large", "ecs.c6r.xlarge",
			"ecs.g8y.large", "ecs.g8y.xlarge",
			"ecs.c8y.large", "ecs.c8y.xlarge",
			"ecs.r8y.large", "ecs.r8y.xlarge",
		})
		configureArchImages(v1alpha1.ArchitectureArm64)

		pod := archTestPod()

		By("creating NodeClass, NodePool and an arm64-targeted pod")
		env.ExpectCreated(nodeClass, nodePool, pod)

		By("waiting for the pod to become healthy on the new arm64 node")
		env.EventuallyExpectHealthy(pod)

		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]

		By("verifying kubernetes.io/arch == arm64 (authoritative CpuArchitecture path)")
		Expect(node.Labels).To(HaveKeyWithValue(corev1.LabelArchStable, v1alpha1.ArchitectureArm64),
			"node arch label must be arm64, got %q", node.Labels[corev1.LabelArchStable])

		By("verifying the instance-type actually belongs to an ARM (Yitian/Ampere) family")
		it := node.Labels[corev1.LabelInstanceTypeStable]
		Expect(it).To(SatisfyAny(
			HavePrefix("ecs.g6r"), HavePrefix("ecs.c6r"),
			HavePrefix("ecs.g8y"), HavePrefix("ecs.c8y"), HavePrefix("ecs.r8y"),
		), "expected an arm64 family instance-type, got %q", it)

		By("verifying kubelet reports arm64 in NodeInfo.Architecture")
		Expect(node.Status.NodeInfo.Architecture).To(Equal(v1alpha1.ArchitectureArm64))
	})

	// Test B: GPU instances must be classified amd64, not arm64 (regression guard).
	It("should classify a GPU (ecs.gn*) node as amd64, not arm64", Label("gpu-arch-regression"), func() {
		// gn6i (T4) and gn7i (A10) are x86_64 GPU families. Under the old name-prefix
		// heuristic these were wrongly mapped to arm64; the authoritative path must
		// return amd64.
		configureArchPool(v1alpha1.ArchitectureAmd64, []string{
			"ecs.gn6i-c4g1.xlarge", "ecs.gn7i-c8g1.2xlarge",
		})

		pod := archTestPod()

		By("creating NodeClass, NodePool and a pod pinned to GPU families")
		env.ExpectCreated(nodeClass, nodePool, pod)

		By("waiting for the pod to become healthy on the new GPU node")
		env.EventuallyExpectHealthy(pod)

		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]

		By("verifying GPU instance-type is classified as amd64 (NOT arm64)")
		Expect(node.Labels).To(HaveKeyWithValue(corev1.LabelArchStable, v1alpha1.ArchitectureAmd64),
			"GPU node arch label must be amd64, got %q", node.Labels[corev1.LabelArchStable])
		Expect(node.Labels[corev1.LabelArchStable]).ToNot(Equal(v1alpha1.ArchitectureArm64),
			"GPU node must never be labelled arm64 (name-prefix heuristic regression)")

		By("verifying the instance-type actually belongs to a GPU (ecs.gn*) family")
		it := node.Labels[corev1.LabelInstanceTypeStable]
		Expect(it).To(HavePrefix("ecs.gn"), "expected a GPU family instance-type, got %q", it)
	})

	// Test C: NodePool required amd64 overrides Pod preferred arm64.
	// This pins the fix for GitHub issue #4: a Pod with
	// preferredDuringSchedulingIgnoredDuringExecution affinity for arm64
	// must not prevent provisioning when the NodePool requires amd64.
	// Under PreferencePolicyRespect (default), Karpenter core first treats
	// the preferred term as hard, fails, then relaxes it and retries — the
	// NodePool's required amd64 constraint wins. With PreferencePolicyIgnore,
	// the preferred term is dropped outright. Either way, the node must
	// come up as amd64.
	It("should provision an amd64 node when NodePool requires amd64 even if Pod prefers arm64", Label("preferred-arch-override"), func() {
		configureArchPool(v1alpha1.ArchitectureAmd64, []string{
			"ecs.g7.large", "ecs.g7.xlarge",
			"ecs.c7.large", "ecs.c7.xlarge",
			"ecs.g6.large", "ecs.g6.xlarge",
		})

		pod := coretest.Pod(coretest.PodOptions{
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
			NodePreferences: []corev1.NodeSelectorRequirement{
				{
					Key:      corev1.LabelArchStable,
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{v1alpha1.ArchitectureArm64},
				},
			},
		})

		By("creating NodeClass with required amd64 and a Pod with preferred arm64 affinity")
		env.ExpectCreated(nodeClass, nodePool, pod)

		By("waiting for the pod to become healthy on the new node")
		env.EventuallyExpectHealthy(pod)

		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]

		By("verifying kubernetes.io/arch == amd64 (NodePool required, preferred arm64 ignored)")
		Expect(node.Labels).To(HaveKeyWithValue(corev1.LabelArchStable, v1alpha1.ArchitectureAmd64),
			"node arch must be amd64 per NodePool requirement, got %q", node.Labels[corev1.LabelArchStable])

		By("verifying the instance-type is an x86_64 family")
		it := node.Labels[corev1.LabelInstanceTypeStable]
		Expect(it).To(SatisfyAny(
			HavePrefix("ecs.g7"), HavePrefix("ecs.c7"),
			HavePrefix("ecs.g6"), HavePrefix("ecs.c6"),
		), "expected an x86_64 family instance-type, got %q", it)
	})

})
