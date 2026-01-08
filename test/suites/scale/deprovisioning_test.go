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

package scale

import (
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

var _ = Describe("Deprovisioning", func() {
	Context("Consolidation", func() {
		It("should delete empty nodes after workload is removed", Label("consolidation-empty"), func() {
			By("provisioning nodes with workload")

			// Configure NodeClass
			nodeClass.Name = "scale-test-consolidation-empty"
			nodeClass.Spec.Tags = map[string]string{
				"testing/cluster": env.ClusterName,
				"testing/type":    "consolidation-empty",
			}

			// Configure NodePool with consolidation enabled
			nodePool.Name = "scale-test-pool-consolidation-empty"
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
						Key:      v1alpha1.LabelInstanceType,
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{"ecs.c6.xlarge", "ecs.c7.xlarge"},
					},
				},
			}
			// Enable consolidation with immediate trigger
			nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmptyOrUnderutilized
			nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")

			// Create initial workload
			replicas := int32(5)
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "scale-test-consolidation-empty-workload",
					Namespace: "default",
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "scale-test-consolidation-empty",
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"app": "scale-test-consolidation-empty",
							},
						},
						Spec: corev1.PodSpec{
							Affinity: &corev1.Affinity{
								PodAntiAffinity: &corev1.PodAntiAffinity{
									RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
										{
											LabelSelector: &metav1.LabelSelector{
												MatchLabels: map[string]string{
													"app": "scale-test-consolidation-empty",
												},
											},
											TopologyKey: corev1.LabelHostname,
										},
									},
								},
							},
							Containers: []corev1.Container{
								{
									Name:  "pause",
									Image: "registry-cn-hangzhou.ack.aliyuncs.com/acs/pause:3.9",
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("100m"),
											corev1.ResourceMemory: resource.MustParse("128Mi"),
										},
									},
								},
							},
							NodeSelector: map[string]string{
								karpv1.NodePoolLabelKey: nodePool.Name,
							},
						},
					},
				},
			}

			By("creating workload and resources")
			env.ExpectCreated(nodePool, nodeClass, deployment)
			selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)

			By("waiting for nodes to be provisioned")
			env.EventuallyExpectHealthyPodCount(selector, int(replicas))
			initialNodeCount := len(env.Monitor.CreatedNodes())
			Expect(initialNodeCount).To(Equal(5))

			By("deleting the workload to make nodes empty")
			env.ExpectDeleted(deployment)

			By("waiting for empty nodes to be deleted via consolidation")
			Eventually(func(g Gomega) {
				nodes := env.Monitor.CreatedNodes()
				g.Expect(len(nodes)).To(Equal(0), "Expected all empty nodes to be deleted")
			}).WithTimeout(10 * time.Minute).Should(Succeed())
		})

		It("should consolidate underutilized nodes", Label("consolidation-underutilized"), func() {
			By("provisioning nodes with workload")

			// Configure NodeClass
			nodeClass.Name = "scale-test-consolidation-underutilized"
			nodeClass.Spec.Tags = map[string]string{
				"testing/cluster": env.ClusterName,
				"testing/type":    "consolidation-underutilized",
			}

			// Configure NodePool
			nodePool.Name = "scale-test-pool-consolidation-underutilized"
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
						Key:      v1alpha1.LabelInstanceType,
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{"ecs.c6.xlarge", "ecs.c7.xlarge"},
					},
				}}
			nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmptyOrUnderutilized
			nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")

			// Create initial workload with 5 pods
			replicas := int32(5)
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "scale-test-consolidation-underutilized-workload",
					Namespace: "default",
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "scale-test-consolidation-underutilized",
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"app": "scale-test-consolidation-underutilized",
							},
						},
						Spec: corev1.PodSpec{
							Affinity: &corev1.Affinity{
								PodAntiAffinity: &corev1.PodAntiAffinity{
									RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
										{
											LabelSelector: &metav1.LabelSelector{
												MatchLabels: map[string]string{
													"app": "scale-test-consolidation-underutilized",
												},
											},
											TopologyKey: corev1.LabelHostname,
										},
									},
								},
							},
							Containers: []corev1.Container{
								{
									Name:  "pause",
									Image: "registry-cn-hangzhou.ack.aliyuncs.com/acs/pause:3.9",
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("100m"),
											corev1.ResourceMemory: resource.MustParse("128Mi"),
										},
									},
								},
							},
							NodeSelector: map[string]string{
								karpv1.NodePoolLabelKey: nodePool.Name,
							},
						},
					},
				},
			}

			By("creating workload and resources")
			env.ExpectCreated(nodePool, nodeClass, deployment)
			selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)

			By("waiting for initial provisioning")
			env.EventuallyExpectHealthyPodCount(selector, int(replicas))
			initialNodeCount := len(env.Monitor.CreatedNodes())
			Expect(initialNodeCount).To(Equal(5))

			By("scaling down the workload to 1 replica")
			deployment.Spec.Replicas = lo.ToPtr(int32(1))
			env.ExpectUpdated(deployment)

			By("waiting for pods to be scaled down")
			env.EventuallyExpectHealthyPodCount(selector, 1)

			By("waiting for underutilized nodes to be consolidated")
			Eventually(func(g Gomega) {
				nodes := env.Monitor.CreatedNodes()
				g.Expect(len(nodes)).To(BeNumerically("<=", 2), "Expected nodes to be consolidated from 5 to 1-2")
			}).WithTimeout(10 * time.Minute).Should(Succeed())
		})
	})

	Context("Emptiness", func() {
		It("should delete nodes when they become empty", Label("emptiness"), func() {
			By("provisioning nodes with workload")

			// Configure NodeClass
			nodeClass.Name = "scale-test-emptiness"
			nodeClass.Spec.Tags = map[string]string{
				"testing/cluster": env.ClusterName,
				"testing/type":    "emptiness",
			}

			// Configure NodePool with TTLSecondsAfterEmpty
			nodePool.Name = "scale-test-pool-emptiness"
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
						Key:      v1alpha1.LabelInstanceType,
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{"ecs.c6.xlarge", "ecs.c7.xlarge"},
					},
				}}
			// Set TTLSecondsAfterEmpty to 0 for immediate deletion
			nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmpty
			nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")

			// Create workload
			replicas := int32(3)
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "scale-test-emptiness-workload",
					Namespace: "default",
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "scale-test-emptiness",
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"app": "scale-test-emptiness",
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "pause",
									Image: "registry-cn-hangzhou.ack.aliyuncs.com/acs/pause:3.9",
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("100m"),
											corev1.ResourceMemory: resource.MustParse("128Mi"),
										},
									},
								},
							},
							NodeSelector: map[string]string{
								karpv1.NodePoolLabelKey: nodePool.Name,
							},
						},
					},
				},
			}

			By("creating workload and resources")
			env.ExpectCreated(nodePool, nodeClass, deployment)
			selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)

			By("waiting for provisioning")
			env.EventuallyExpectHealthyPodCount(selector, int(replicas))
			initialNodeCount := len(env.Monitor.CreatedNodes())
			Expect(initialNodeCount).To(BeNumerically(">=", 1))

			By("deleting the workload")
			env.ExpectDeleted(deployment)

			By("waiting for empty nodes to be deleted")
			Eventually(func(g Gomega) {
				nodes := env.Monitor.CreatedNodes()
				g.Expect(len(nodes)).To(Equal(0), "Expected all empty nodes to be deleted")
			}).WithTimeout(5 * time.Minute).Should(Succeed())
		})
	})

	Context("Expiration", func() {
		It("should replace nodes when they expire", Label("expiration"), func() {
			By("provisioning nodes with short expiration time")

			// Configure NodeClass
			nodeClass.Name = "scale-test-expiration"
			nodeClass.Spec.Tags = map[string]string{
				"testing/cluster": env.ClusterName,
				"testing/type":    "expiration",
			}

			// Configure NodePool with short expiration
			nodePool.Name = "scale-test-pool-expiration"
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
						Key:      v1alpha1.LabelInstanceType,
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{"ecs.c6.xlarge", "ecs.c7.xlarge"},
					},
				}}
			// Set expiration to 5 minutes for testing
			nodePool.Spec.Template.Spec.ExpireAfter = karpv1.MustParseNillableDuration("5m")

			// Create workload
			replicas := int32(2)
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "scale-test-expiration-workload",
					Namespace: "default",
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "scale-test-expiration",
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"app": "scale-test-expiration",
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "pause",
									Image: "registry-cn-hangzhou.ack.aliyuncs.com/acs/pause:3.9",
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("100m"),
											corev1.ResourceMemory: resource.MustParse("128Mi"),
										},
									},
								},
							},
							NodeSelector: map[string]string{
								karpv1.NodePoolLabelKey: nodePool.Name,
							},
						},
					},
				},
			}

			By("creating workload and resources")
			env.ExpectCreated(nodePool, nodeClass, deployment)
			selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)

			By("waiting for initial provisioning")
			env.EventuallyExpectHealthyPodCount(selector, int(replicas))
			initialNodes := env.Monitor.CreatedNodes()
			initialNodeCount := len(initialNodes)
			Expect(initialNodeCount).To(BeNumerically(">=", 1))

			By("waiting for nodes to expire and be replaced")
			Eventually(func(g Gomega) {
				currentNodes := env.Monitor.CreatedNodes()
				// Check if any nodes have been replaced (different node names)
				replaced := false
				for _, currentNode := range currentNodes {
					found := false
					for _, initialNode := range initialNodes {
						if currentNode.Name == initialNode.Name {
							found = true
							break
						}
					}
					if !found {
						replaced = true
						break
					}
				}
				g.Expect(replaced).To(BeTrue(), "Expected at least one node to be replaced due to expiration")
				// Verify workload remains healthy
				g.Expect(len(currentNodes)).To(BeNumerically(">=", 1), "Expected nodes to remain after replacement")
			}).WithTimeout(10 * time.Minute).Should(Succeed())

			By("verifying all pods remain healthy after replacement")
			env.EventuallyExpectHealthyPodCount(selector, int(replicas))
		})
	})

	Context("Drift", func() {
		It("should replace nodes when NodeClass configuration drifts", Label("drift"), func() {
			By("provisioning nodes with initial configuration")

			// Configure initial NodeClass
			nodeClass.Name = "scale-test-drift"
			nodeClass.Spec.Tags = map[string]string{
				"testing/cluster": env.ClusterName,
				"testing/type":    "drift",
				"version":         "v1",
			}

			// Configure NodePool
			nodePool.Name = "scale-test-pool-drift"
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
						Key:      v1alpha1.LabelInstanceType,
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{"ecs.c6.xlarge", "ecs.c7.xlarge"},
					},
				}}

			// Create workload
			replicas := int32(2)
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "scale-test-drift-workload",
					Namespace: "default",
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "scale-test-drift",
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"app": "scale-test-drift",
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "pause",
									Image: "registry-cn-hangzhou.ack.aliyuncs.com/acs/pause:3.9",
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("100m"),
											corev1.ResourceMemory: resource.MustParse("128Mi"),
										},
									},
								},
							},
							NodeSelector: map[string]string{
								karpv1.NodePoolLabelKey: nodePool.Name,
							},
						},
					},
				},
			}

			By("creating workload and resources")
			env.ExpectCreated(nodePool, nodeClass, deployment)
			selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)

			By("waiting for initial provisioning")
			env.EventuallyExpectHealthyPodCount(selector, int(replicas))
			initialNodes := env.Monitor.CreatedNodes()
			initialNodeCount := len(initialNodes)
			Expect(initialNodeCount).To(BeNumerically(">=", 1))

			By("updating NodeClass configuration to trigger drift")
			nodeClass.Spec.Tags["version"] = "v2"
			nodeClass.Spec.Tags["updated"] = "true"
			env.ExpectUpdated(nodeClass)

			By("waiting for drifted nodes to be replaced")
			Eventually(func(g Gomega) {
				currentNodes := env.Monitor.CreatedNodes()
				// Verify nodes have been replaced
				replaced := false
				for _, currentNode := range currentNodes {
					found := false
					for _, initialNode := range initialNodes {
						if currentNode.Name == initialNode.Name {
							found = true
							break
						}
					}
					if !found {
						replaced = true
						break
					}
				}
				g.Expect(replaced).To(BeTrue(), "Expected nodes to be replaced due to drift")
			}).WithTimeout(10 * time.Minute).Should(Succeed())

			By("verifying workload remains healthy after drift replacement")
			env.EventuallyExpectHealthyPodCount(selector, int(replicas))
		})
	})
})
