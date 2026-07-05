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

// Package nodeclaim contains integration tests that require a live ACK cluster.
//
// Test coverage: issues #2 #4 #5
//
//   - #4 (arch/OS/instance-type labels): Karpenter must write kubernetes.io/arch,
//     kubernetes.io/os and node.kubernetes.io/instance-type on every provisioned node.
//     The fix is in instancetype.Provider.convertECSInstanceType (nil-safe ARM64 mapping)
//     and cloudprovider.convertInstanceToNodeClaim.
//
//   - #5 (no ECS tag leak): Before the fix, CloudProvider.List/Get set NodeClaim.Labels
//     directly from inst.Tags (the raw ECS tag map), which could contain characters
//     illegal in K8s label values (colons, @-signs, slashes). After the fix,
//     instanceLabelsFromInstance() copies only the five controlled labels and restores
//     the NodePool name from the karpenter-managed tag.
//
//   - #2 (runtime version, partial): The bootstrap script now calls
//     DescribeKubernetesVersionMetadata to patch --runtime-version at generation time.
//     We cannot inspect the userdata blob after the fact, but we can assert that the
//     node joins with a non-empty ContainerRuntimeVersion that names containerd.
//
// Required environment variables (via NewEnvironment):
//
//	ALIBABA_CLOUD_ACCESS_KEY_ID
//	ALIBABA_CLOUD_ACCESS_KEY_SECRET
//	TEST_REGION            — e.g. cn-hangzhou
//	TEST_CLUSTER_ID        — ACK cluster ID
//	TEST_CLUSTER_NAME      — ACK cluster name
//	TEST_CLUSTER_ENDPOINT  — API server endpoint
//
// Run with:
//
//	go test -v -tags integration ./test/suites/nodeclaim/... -run NodeLabels -timeout 30m
package nodeclaim

import (
	"strings"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"
)

var _ = Describe("NodeLabels", Label("node-labels"), func() {
	// Single It block: provision once, verify all label properties (#2, #4, #5).
	// Splitting into multiple It blocks would require separate node provisioning per
	// assertion — expensive and unnecessary since all properties come from the same ECS
	// DescribeInstances / RunInstances response.
	It("should provision node with correct Kubernetes labels and runtime version", func() {
		// Set a tag whose key survives K8s label-key validation (prefix + name).
		// Under the old code (#5), inst.Tags was assigned directly to NodeClaim.Labels,
		// so this tag would have appeared on the resulting Node object.
		// After the fix, instanceLabelsFromInstance() copies only controlled labels;
		// this key must be ABSENT from node.Labels.
		nodeClass.Spec.Tags["testing/raw-ecs-tag"] = "raw-value"

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
		})

		By("creating NodeClass, NodePool, and test pod")
		env.ExpectCreated(nodeClass, nodePool, pod)

		By("waiting for pod to become healthy (node must join and pass readiness)")
		env.EventuallyExpectHealthy(pod)

		By("fetching the provisioned node")
		nodes := env.EventuallyExpectCreatedNodeCount("==", 1)
		Expect(nodes).To(HaveLen(1), "expected exactly one Karpenter-provisioned node")
		node := nodes[0]

		// ── Issue #4: arch/OS/instance-type labels ────────────────────────────
		//
		// ECS "X86" maps to "amd64"; "ARM" / "ARM64" maps to "arm64".
		// The fix adds nil-safety for CpuArchitecture and normalises all ARM* variants
		// via strings.HasPrefix.

		By("verifying kubernetes.io/arch is amd64 or arm64 (#4)")
		arch := node.Labels[corev1.LabelArchStable]
		Expect(arch).To(BeElementOf("amd64", "arm64"),
			"kubernetes.io/arch must be amd64 or arm64, got %q", arch)

		By("verifying kubernetes.io/os is linux (#4)")
		Expect(node.Labels[corev1.LabelOSStable]).To(Equal("linux"))

		By("verifying node.kubernetes.io/instance-type has ecs. prefix (#4)")
		instanceType := node.Labels[corev1.LabelInstanceTypeStable]
		Expect(instanceType).To(HavePrefix("ecs."),
			"node.kubernetes.io/instance-type must start with ecs., got %q", instanceType)

		// ── Issue #5: no ECS tag leakage ────────────────────────────────────
		//
		// The raw ECS tag we injected must NOT appear as a K8s label.
		// Also check that the NodePool label is present (restored from the managed
		// karpenter.alibabacloud.com/nodepool tag written during CreateInstance).

		By("verifying the injected ECS tag is absent from node labels (#5)")
		Expect(node.Labels).NotTo(HaveKey("testing/raw-ecs-tag"),
			"ECS tag 'testing/raw-ecs-tag' leaked as a Kubernetes node label (fix #5 regression)")

		// Belt-and-suspenders: also scan all label keys.
		for k := range node.Labels {
			Expect(strings.HasSuffix(k, "/raw-ecs-tag")).To(BeFalse(),
				"found a leaked ECS tag key in node labels: %q", k)
		}

		By("verifying karpenter.sh/nodepool label is set to the correct pool name (#5)")
		Expect(node.Labels[karpv1.NodePoolLabelKey]).To(Equal(nodePool.Name),
			"expected karpenter.sh/nodepool=%s, got %q", nodePool.Name, node.Labels[karpv1.NodePoolLabelKey])

		By("verifying karpenter.sh/capacity-type label is present (#5)")
		capacityType := node.Labels[v1alpha1.LabelCapacityType]
		Expect(capacityType).To(BeElementOf(v1alpha1.CapacityTypeOnDemand, v1alpha1.CapacityTypeSpot),
			"karpenter.sh/capacity-type must be on-demand or spot, got %q", capacityType)

		// ── Issue #2: container runtime version (partial) ───────────────────
		//
		// The bootstrap script now injects --runtime-version via
		// DescribeKubernetesVersionMetadata.  We cannot inspect the userdata blob
		// after the fact, but we can verify that the node joined with a valid
		// containerd version string, which means the bootstrap args were accepted.

		By("verifying ContainerRuntimeVersion contains containerd:// (#2)")
		rv := node.Status.NodeInfo.ContainerRuntimeVersion
		Expect(rv).To(ContainSubstring("containerd://"),
			"expected ContainerRuntimeVersion to contain containerd://, got %q", rv)

		parts := strings.SplitN(rv, "://", 2)
		Expect(parts).To(HaveLen(2))
		Expect(parts[1]).NotTo(BeEmpty(), "containerd version string after :// must not be empty")
	})
})
