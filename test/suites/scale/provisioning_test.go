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
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

var _ = Describe("Provisioning", func() {
	Context("Scale Up", func() {
		It("should scale up node-dense workload", Label("node-dense"), func() {
			By("creating a deployment with 5 pending pods requiring separate nodes")

			// Configure NodeClass
			nodeClass.Name = "scale-test-node-dense"
			nodeClass.Spec.Tags = map[string]string{
				"testing/cluster": env.ClusterName,
				"testing/type":    "node-dense",
			}

			// Configure NodePool with anti-affinity to force separate nodes
			nodePool.Name = "scale-test-pool-node-dense"
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

			// Create deployment with pod anti-affinity
			replicas := int32(5)
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "scale-test-node-dense-workload",
					Namespace: "default",
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "scale-test-node-dense",
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"app": "scale-test-node-dense",
							},
						},
						Spec: corev1.PodSpec{
							Affinity: &corev1.Affinity{
								PodAntiAffinity: &corev1.PodAntiAffinity{
									RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
										{
											LabelSelector: &metav1.LabelSelector{
												MatchLabels: map[string]string{
													"app": "scale-test-node-dense",
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

			By("creating deployment and waiting for pending pods")
			env.ExpectCreated(deployment)
			selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
			env.EventuallyExpectPendingPodCount(selector, int(replicas))

			By("creating NodePool and NodeClass to trigger provisioning")
			env.ExpectCreated(nodePool, nodeClass)

			By("waiting for nodes to be created")
			env.EventuallyExpectCreatedNodeCount("==", 5)
			env.EventuallyExpectInitializedNodeCount("==", 5)

			By("waiting for all pods to become healthy")
			env.EventuallyExpectHealthyPodCount(selector, int(replicas))

			By("verifying nodes are from Karpenter")
			nodes := env.Monitor.CreatedNodes()
			Expect(nodes).To(HaveLen(5))
			for _, node := range nodes {
				Expect(node.Labels).To(HaveKey(karpv1.NodePoolLabelKey))
				Expect(node.Labels[karpv1.NodePoolLabelKey]).To(Equal(nodePool.Name))
			}
		})

		It("should scale up pod-dense workload", Label("pod-dense"), func() {
			By("creating a deployment with 5 pods on a single node")

			// Configure NodeClass
			nodeClass.Name = "scale-test-pod-dense"
			nodeClass.Spec.Tags = map[string]string{
				"testing/cluster": env.ClusterName,
				"testing/type":    "pod-dense",
			}

			// Configure NodePool
			nodePool.Name = "scale-test-pool-pod-dense"
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

			// Create deployment with small resource requests to fit multiple pods
			replicas := int32(5)
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "scale-test-pod-dense-workload",
					Namespace: "default",
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "scale-test-pod-dense",
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"app": "scale-test-pod-dense",
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "pause",
									Image: "registry-cn-hangzhou.ack.aliyuncs.com/acs/pause:3.9",
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("10m"),
											corev1.ResourceMemory: resource.MustParse("64Mi"),
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

			By("creating deployment and waiting for pending pods")
			env.ExpectCreated(deployment)
			selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
			env.EventuallyExpectPendingPodCount(selector, int(replicas))

			By("creating NodePool and NodeClass to trigger provisioning")
			env.ExpectCreated(nodePool, nodeClass)

			By("waiting for node to be created")
			env.EventuallyExpectCreatedNodeCount(">=", 1)
			env.EventuallyExpectInitializedNodeCount(">=", 1)

			By("waiting for all pods to become healthy")
			env.EventuallyExpectHealthyPodCount(selector, int(replicas))

			By("verifying pods are co-located on few nodes")
			nodes := env.Monitor.CreatedNodes()
			Expect(len(nodes)).To(BeNumerically("<=", 2), "Expected pods to be co-located on few nodes") // Should fit on 1-2 nodes

			By("verifying pod density")
			podList := &corev1.PodList{}
			Expect(env.Client.List(env.Context, podList, &client.ListOptions{
				LabelSelector: selector,
			})).To(Succeed())

			nodePodsCount := make(map[string]int)
			for _, pod := range podList.Items {
				if pod.Spec.NodeName != "" {
					nodePodsCount[pod.Spec.NodeName]++
				}
			}

			// Verify at least one node has multiple pods
			maxPodsOnNode := 0
			for _, count := range nodePodsCount {
				if count > maxPodsOnNode {
					maxPodsOnNode = count
				}
			}
			Expect(maxPodsOnNode).To(BeNumerically(">=", 3), "Expected at least 3 pods on a single node")
		})

		It("should scale up GPU workload", Label("gpu"), func() {
			By("creating a deployment with 11 pods requiring GPU nodes")

			// Configure NodeClass
			nodeClass.Name = "scale-test-gpu"
			nodeClass.Spec.Tags = map[string]string{
				"testing/cluster": env.ClusterName,
				"testing/type":    "gpu",
			}

			// Configure NodePool
			nodePool.Name = "scale-test-pool-gpu"
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
						Values:   []string{"ecs.c6.large", "ecs.g6.large", "ecs.gn6v-c8g1.2xlarge"},
					},
				},
				{
					NodeSelectorRequirement: corev1.NodeSelectorRequirement{
						Key:      corev1.LabelTopologyZone,
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{"cn-hangzhou-i", "cn-hangzhou-j", "cn-hangzhou-k"},
					},
				},
			}

			// Create deployment with GPU requests
			replicas := int32(11)
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "scale-test-gpu-workload",
					Namespace: "default",
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "inflate",
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{
								"app": "inflate",
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "inflate",
									Image: "registry-cn-hangzhou.ack.aliyuncs.com/acs/pause:3.9",
									Resources: corev1.ResourceRequirements{
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse("1"),
											corev1.ResourceMemory: resource.MustParse("2Gi"),
											"nvidia.com/gpu":      resource.MustParse("1"),
										},
										Limits: corev1.ResourceList{
											"nvidia.com/gpu": resource.MustParse("1"),
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

			By("creating deployment and waiting for pending pods")
			env.ExpectCreated(deployment)
			selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
			env.EventuallyExpectPendingPodCount(selector, int(replicas))

			By("creating NodePool and NodeClass to trigger provisioning")
			env.ExpectCreated(nodePool, nodeClass)

			By("waiting for nodes to be created")
			env.EventuallyExpectCreatedNodeCount(">=", 1)
			env.EventuallyExpectInitializedNodeCount(">=", 1)

			By("waiting for all pods to become healthy")
			env.EventuallyExpectHealthyPodCount(selector, int(replicas))

			By("verifying nodes have GPU capacity")
			nodes := env.Monitor.CreatedNodes()
			for _, node := range nodes {
				if _, ok := node.Status.Capacity[corev1.ResourceName("nvidia.com/gpu")]; ok {
					return
				}
			}
			Fail("Expected at least one node to have GPU capacity")
		})
	})
})
