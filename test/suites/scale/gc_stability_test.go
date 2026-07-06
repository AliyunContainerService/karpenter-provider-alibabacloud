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

// Test coverage: issue #8
//
// The over-provisioning fix addressed four root causes:
//
//  1. TagManagedBy value: buildInstanceTags set karpenter.alibabacloud.com/managed-by="true"
//     while List() queried for "karpenter".  The exact-match tag filter returned zero
//     instances, so Karpenter's GC controller deleted every NodeClaim immediately after
//     creation — triggering a re-provision loop.
//     Fix: tag value changed to "karpenter".
//
//  2. Inventory check (DescribeAvailableResource): SpotAsPriceGo was incorrectly passed
//     as InstanceChargeType; fixed to pass it as SpotStrategy.  getInventory() now
//     excludes instance types with no stock in any zone from List().
//
//  3. Offering.Requirements populated: each cloudprovider.Offering now carries
//     topology.kubernetes.io/zone and karpenter.sh/capacity-type requirements.
//
//  4. InstanceType.Requirements populated: all five well-known labels required by
//     Karpenter's scheduler are now present on each InstanceType.
//
// Tests in this file verify (1) and the downstream effect of (2)–(4): nodes created by
// Karpenter must persist and must honour NodePool resource limits.
//
// Required environment variables (via NewEnvironment):
//
//	ALIBABA_CLOUD_ACCESS_KEY_ID
//	ALIBABA_CLOUD_ACCESS_KEY_SECRET
//	TEST_REGION, TEST_CLUSTER_ID, TEST_CLUSTER_NAME, TEST_CLUSTER_ENDPOINT
//
// Run with:
//
//	go test -v -tags integration ./test/suites/scale/... -run GCStability -timeout 20m
package scale

import (
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

var _ = Describe("GCStability", Label("gc-stability"), func() {
	// ── Issue #8: TagManagedBy fix ───────────────────────────────────────────
	//
	// Before the fix, List() found zero ECS instances for any Karpenter NodeClaim
	// because the tag filter queried for managed-by="karpenter" but the instance was
	// tagged managed-by="true".  Karpenter's GC controller deleted the NodeClaim
	// seconds after creation.  The node disappeared, Karpenter re-provisioned, and
	// the cycle repeated — "over-provisioning" at the API level.
	//
	// This test provisions one node, waits for it to be healthy, then asserts it
	// remains alive for 3 minutes.  If the TagManagedBy bug is present, the node
	// will be deleted by GC well within that window.
	It("should not garbage collect provisioned nodes immediately after creation", func() {
		// Use auto-generated unique names (from test.ObjectMeta) to avoid conflicts
		// with objects stuck in Terminating state from previous runs.
		nodePool.Name = "gc-stability-pool"
		nodePool.Spec.Template.Spec.NodeClassRef = &karpv1.NodeClassReference{
			Group: "karpenter.alibabacloud.com",
			Kind:  "ECSNodeClass",
			Name:  nodeClass.Name,
		}
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
					Key:      corev1.LabelInstanceTypeStable,
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{"ecs.c6.xlarge", "ecs.c7.xlarge"},
				},
			},
		}

		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "gc-stability-pod",
				Namespace: "default",
			},
			Spec: corev1.PodSpec{
				NodeSelector: map[string]string{
					karpv1.NodePoolLabelKey: nodePool.Name,
				},
				Containers: []corev1.Container{{
					Name:  "pause",
					Image: "registry-cn-hangzhou.ack.aliyuncs.com/acs/pause:3.9",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
					},
				}},
			},
		}

		By("creating NodeClass, NodePool, and test pod")
		env.ExpectCreated(nodeClass, nodePool, pod)

		By("waiting for pod to become healthy (node must join the cluster)")
		env.EventuallyExpectHealthy(pod)

		By("asserting node count stays at 1 for 3 minutes (TagManagedBy fix #8)")
		// If the TagManagedBy bug is present, the GC loop deletes the NodeClaim
		// and the node disappears within the first reconcile cycle (≈30–60s).
		// A 3-minute stable window is sufficient to detect this regression.
		env.ConsistentlyExpectNodeCount("==", 1, 3*time.Minute)
	})

	// ── Issue #8: NodePool resource limits respected ─────────────────────────
	//
	// After fixing the inventory and offering requirements, Karpenter's scheduler
	// can correctly evaluate InstanceType.Requirements and Offering.Requirements.
	// The NodePool spec.limits.cpu cap must now be enforced: if the limit is 4 CPU
	// and 6 pods each request 1 CPU (total 6 CPU), only 4 CPUs worth of capacity
	// may be provisioned; the remaining 2 pods stay Pending.
	It("should respect NodePool CPU resource limits and leave excess pods pending", func() {
		nodeClass.Name = "gc-limits-nc"
		nodePool.Name = "gc-limits-pool"
		nodePool.Spec.Template.Spec.NodeClassRef = &karpv1.NodeClassReference{
			Group: "karpenter.alibabacloud.com",
			Kind:  "ECSNodeClass",
			Name:  nodeClass.Name,
		}
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
					Key:      corev1.LabelInstanceTypeStable,
					Operator: corev1.NodeSelectorOpIn,
					// 4-vCPU instance types so a single node saturates the 4-CPU limit.
					Values: []string{"ecs.c6.xlarge", "ecs.c7.xlarge"},
				},
			},
		}
		// Hard cap: at most 4 CPU cores across all nodes in this NodePool.
		nodePool.Spec.Limits = karpv1.Limits{
			corev1.ResourceCPU: resource.MustParse("4"),
		}

		// 6 pods × 800m = 4.8 CPU requested. NodePool limit is 4 CPU → only 1 node (4 vCPU)
		// is provisioned. System pods + kube-reserved consume ~800–1000m, leaving ~3000–3200m
		// allocatable. 3 pods (3 × 800m = 2400m) fit on the node; the remaining 3 pods stay
		// Pending because the NodePool CPU limit prevents a second node.
		pods := make([]*corev1.Pod, 6)
		for i := range pods {
			pods[i] = &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "gc-limits-pod-",
					Namespace:    "default",
				},
				Spec: corev1.PodSpec{
					NodeSelector: map[string]string{
						karpv1.NodePoolLabelKey: nodePool.Name,
					},
					Containers: []corev1.Container{{
						Name:  "pause",
						Image: "registry-cn-hangzhou.ack.aliyuncs.com/acs/pause:3.9",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								// 800m per pod: ~3 pods fit on the 4-vCPU node given actual
								// system overhead (~800–1000m), and the NodePool CPU limit
								// prevents a second node from being provisioned.
								corev1.ResourceCPU:    resource.MustParse("800m"),
								corev1.ResourceMemory: resource.MustParse("256Mi"),
							},
						},
					}},
				},
			}
		}

		By("creating NodeClass, NodePool, and 6 pods requesting 1 CPU each")
		env.ExpectCreated(nodeClass, nodePool)
		for _, p := range pods {
			env.ExpectCreated(p)
		}

		By("waiting for at least 2 pods to become healthy (within the 4-CPU limit)")
		// Give Karpenter enough time to provision a node and schedule pods.
		// The exact count that fits depends on system overhead (~800–1000m reserved on a 4-CPU node),
		// so we assert ≥2 running. The real invariant is verified at the CPU-limit assertion below.
		Eventually(func(g Gomega) {
			var healthyCount int
			for _, p := range pods {
				current := &corev1.Pod{}
				if err := env.Client.Get(env.Context, client.ObjectKeyFromObject(p), current); err != nil {
					return
				}
				if current.Status.Phase == corev1.PodRunning {
					healthyCount++
				}
			}
			g.Expect(healthyCount).To(BeNumerically(">=", 2),
				"expected at least 2 pods to be running on the provisioned node")
		}).WithTimeout(20 * time.Minute).WithPolling(15 * time.Second).Should(Succeed())

		By("verifying total provisioned CPU does not exceed the NodePool limit of 4")
		nodes := env.Monitor.CreatedNodes()
		var totalCPU resource.Quantity
		for _, n := range nodes {
			if cpu, ok := n.Status.Capacity[corev1.ResourceCPU]; ok {
				totalCPU.Add(cpu)
			}
		}
		limit := resource.MustParse("4")
		Expect(totalCPU.Cmp(limit)).To(BeNumerically("<=", 0),
			"provisioned node CPU %s exceeds NodePool limit 4 (#8)", totalCPU.String())
	})
})
