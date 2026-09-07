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

package scheduling

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	environmentcs "github.com/AliyunContainerService/karpenter-provider-alibabacloud/test/pkg/cs"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	karpv1alpha1 "sigs.k8s.io/karpenter/pkg/apis/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var env *environmentcs.Environment
var nodeClass *v1alpha1.ECSNodeClass
var nodePool *karpv1.NodePool

func TestScheduling(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = environmentcs.NewEnvironment(t)
		SetDefaultEventuallyTimeout(time.Hour)
	})
	AfterSuite(func() {
		if env != nil {
			env.Stop()
		}
	})
	RunSpecs(t, "Scheduling")
}

var _ = BeforeEach(func() {
	env.BeforeEach()
	nodeClass = env.DefaultECSNodeClass()
	nodePool = env.DefaultNodePool(nodeClass)
})

var _ = AfterEach(func() {
	env.AfterEach()
})

var _ = Describe("Scheduling", func() {
	It("should provision nodes only after a matching NodePool exists", Label("nodepool-selector"), func() {
		configureNodeClass("nodepool-selector", "nodepool-selector")
		configureNodePool("nodepool-selector")

		replicas := int32(5)
		deployment := schedulingDeployment("nodepool-selector", replicas, "100m", "128Mi", true)
		env.ExpectCreated(deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectPendingPodCount(selector, int(replicas))

		env.ExpectCreated(nodePool, nodeClass)

		env.EventuallyExpectCreatedNodeCount("==", 5)
		env.EventuallyExpectInitializedNodeCount("==", 5)
		env.EventuallyExpectHealthyPodCount(selector, int(replicas))
		for _, node := range env.Monitor.CreatedNodes() {
			Expect(node.Labels).To(HaveKeyWithValue(karpv1.NodePoolLabelKey, nodePool.Name))
		}
	})

	It("should honor hostname anti-affinity by spreading pods across nodes", Label("hostname-anti-affinity"), func() {
		configureNodeClass("hostname-anti-affinity", "hostname-anti-affinity")
		configureNodePool("hostname-anti-affinity")

		replicas := int32(4)
		deployment := schedulingDeployment("hostname-anti-affinity", replicas, "100m", "128Mi", true)

		env.ExpectCreated(nodePool, nodeClass, deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectHealthyPodCount(selector, int(replicas))
		Expect(env.Monitor.CreatedNodes()).To(HaveLen(int(replicas)))
	})

	It("should pack pod-dense workloads onto fewer nodes", Label("pod-dense"), func() {
		configureNodeClass("pod-dense", "pod-dense")
		configureNodePool("pod-dense")

		replicas := int32(5)
		deployment := schedulingDeployment("pod-dense", replicas, "10m", "64Mi", false)

		env.ExpectCreated(deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectPendingPodCount(selector, int(replicas))
		env.ExpectCreated(nodePool, nodeClass)
		env.EventuallyExpectHealthyPodCount(selector, int(replicas))

		Expect(len(env.Monitor.CreatedNodes())).To(BeNumerically("<=", 2), "expected pods to be packed onto one or two nodes")

		podList := &corev1.PodList{}
		Expect(env.Client.List(env.Context, podList, &client.ListOptions{LabelSelector: selector})).To(Succeed())
		nodePodsCount := map[string]int{}
		for _, pod := range podList.Items {
			if pod.Spec.NodeName != "" {
				nodePodsCount[pod.Spec.NodeName]++
			}
		}
		maxPodsOnNode := 0
		for _, count := range nodePodsCount {
			if count > maxPodsOnNode {
				maxPodsOnNode = count
			}
		}
		Expect(maxPodsOnNode).To(BeNumerically(">=", 3), "expected pod density on a single node")
	})

	It("should apply annotations to the node", Label("annotations"), func() {
		configureNodeClass("annotations", "annotations")
		configureNodePool("annotations")
		nodePool.Spec.Template.Annotations = map[string]string{
			"karpenter.alibabacloud.com/e2e-annotation": "true",
			karpv1.DoNotDisruptAnnotationKey:            "true",
		}
		pod := schedulingPod("annotations", map[string]string{karpv1.NodePoolLabelKey: nodePool.Name}, corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		})

		env.ExpectCreated(nodePool, nodeClass, pod)
		env.EventuallyExpectHealthy(pod)
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]
		Expect(node.Annotations).To(HaveKeyWithValue("karpenter.alibabacloud.com/e2e-annotation", "true"))
		Expect(node.Annotations).To(HaveKeyWithValue(karpv1.DoNotDisruptAnnotationKey, "true"))
	})

	It("should support well-known labels for instance type selection", Label("well-known-labels"), Label("instance-type"), func() {
		configureNodeClass("well-known-labels", "well-known-labels")
		configureNodePool("well-known-labels")
		instanceType := testInstanceTypes()[0]
		nodePool.Spec.Template.Spec.Requirements = withInstanceTypeRequirements(instanceType)
		pod := schedulingPod("well-known-labels", map[string]string{
			karpv1.NodePoolLabelKey:          nodePool.Name,
			corev1.LabelInstanceTypeStable:   instanceType,
			v1alpha1.LabelInstanceFamily:     instanceFamily(instanceType),
			v1alpha1.LabelInstanceCategory:   instanceCategory(instanceType),
			v1alpha1.LabelInstanceGeneration: instanceGeneration(instanceType),
			v1alpha1.LabelInstanceSize:       instanceSize(instanceType),
			corev1.LabelOSStable:             v1alpha1.OSLinux,
			v1alpha1.LabelCapacityType:       v1alpha1.CapacityTypeOnDemand,
		}, corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		})

		env.ExpectCreated(nodePool, nodeClass, pod)
		env.EventuallyExpectHealthy(pod)
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]
		Expect(node.Labels).To(HaveKeyWithValue(corev1.LabelInstanceTypeStable, instanceType))
		Expect(node.Labels).To(HaveKeyWithValue(v1alpha1.LabelInstanceCategory, instanceCategory(instanceType)))
	})

	It("should provision nodes for pods with zone requirements in the correct zone", Label("zone"), func() {
		zones := envList("TEST_ZONES")
		if len(zones) == 0 {
			Skip("zone scheduling test requires TEST_ZONES from ackctl setup")
		}
		configureNodeClass("zone", "zone")
		configureNodePool("zone")
		selectedZone := zones[0]
		nodePool.Spec.Template.Spec.Requirements = append(withInstanceTypeRequirements(testInstanceTypes()...),
			karpv1.NodeSelectorRequirementWithMinValues{NodeSelectorRequirement: corev1.NodeSelectorRequirement{
				Key:      corev1.LabelTopologyZone,
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{selectedZone},
			}},
		)
		pod := schedulingPod("zone", map[string]string{
			karpv1.NodePoolLabelKey:    nodePool.Name,
			corev1.LabelTopologyZone:   selectedZone,
			corev1.LabelTopologyRegion: env.Region,
			v1alpha1.LabelCapacityType: v1alpha1.CapacityTypeOnDemand,
		}, corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		})

		env.ExpectCreated(nodePool, nodeClass, pod)
		env.EventuallyExpectHealthy(pod)
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]
		Expect(node.Labels).To(HaveKeyWithValue(corev1.LabelTopologyZone, selectedZone))
	})

	It("should provision a node using a NodePool with higher priority", Label("priority"), func() {
		instanceTypes := testInstanceTypes()
		if len(instanceTypes) < 2 {
			Skip("priority scheduling test requires at least two TEST_INSTANCE_TYPES")
		}
		configureNodeClass("priority", "priority")
		configureNodePool("priority")
		low := nodePool.DeepCopy()
		low.Name = "scheduling-test-pool-priority-low"
		low.Spec.Weight = int32Ptr(10)
		low.Spec.Template.Spec.Requirements = withInstanceTypeRequirements(instanceTypes[0])
		high := nodePool.DeepCopy()
		high.Name = "scheduling-test-pool-priority-high"
		high.Spec.Weight = int32Ptr(100)
		high.Spec.Template.Spec.Requirements = withInstanceTypeRequirements(instanceTypes[1])
		pod := schedulingPod("priority", nil, corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		})

		env.ExpectCreated(nodeClass, low, high, pod)
		env.EventuallyExpectHealthy(pod)
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]
		Expect(node.Labels).To(HaveKeyWithValue(karpv1.NodePoolLabelKey, high.Name))
		Expect(node.Labels).To(HaveKeyWithValue(corev1.LabelInstanceTypeStable, instanceTypes[1]))
	})

	It("should provision a right-sized node when a pod has initContainers", Label("initcontainers"), func() {
		configureNodeClass("initcontainers", "initcontainers")
		configureNodePool("initcontainers")
		pod := schedulingPod("initcontainers", map[string]string{karpv1.NodePoolLabelKey: nodePool.Name}, corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		}, corev1.Container{
			Name:    "init-capacity",
			Image:   "registry-cn-hangzhou.ack.aliyuncs.com/acs/pause:3.9",
			Command: []string{"/pause"},
			Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("1500m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			}},
		})

		env.ExpectCreated(nodePool, nodeClass, pod)
		env.EventuallyExpectInitializedNodeCount("==", 1)
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]
		Expect(node.Status.Capacity.Cpu().MilliValue()).To(BeNumerically(">=", 1500))
	})

	It("should support well-known labels for gpu accelerator scheduling", Label("gpu"), Label("accelerator"), func() {
		gpuTypes := envList("TEST_GPU_INSTANCE_TYPES")
		gpuZones := envList("TEST_GPU_ZONES")
		if len(gpuTypes) == 0 || len(gpuZones) == 0 {
			Skip("GPU scheduling test requires TEST_GPU_INSTANCE_TYPES and TEST_GPU_ZONES from e2e GPU discovery")
		}
		configureNodeClass("gpu", "gpu")
		configureNodePool("gpu")
		nodePool.Spec.Template.Spec.Requirements = append(withInstanceTypeRequirements(gpuTypes...),
			karpv1.NodeSelectorRequirementWithMinValues{NodeSelectorRequirement: corev1.NodeSelectorRequirement{
				Key:      corev1.LabelTopologyZone,
				Operator: corev1.NodeSelectorOpIn,
				Values:   gpuZones,
			}},
		)
		pod := schedulingPod("gpu", map[string]string{
			karpv1.NodePoolLabelKey:        nodePool.Name,
			v1alpha1.LabelInstanceGPUCount: "1",
		}, corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
			v1alpha1.ResourceGPU:  resource.MustParse("1"),
		})

		env.ExpectCreated(nodePool, nodeClass, pod)
		env.EventuallyExpectHealthy(pod)
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]
		Expect(node.Status.Capacity).To(HaveKey(v1alpha1.ResourceGPU))
	})

	It("should exercise capacity reservation scheduling preconditions", Label("capacity-reservation"), Label("reservation"), func() {
		capacityReservationID := os.Getenv("TEST_CAPACITY_RESERVATION_ID")
		if capacityReservationID == "" {
			Skip("capacity reservation scheduling requires TEST_CAPACITY_RESERVATION_ID for a real AlibabaCloud reserved-capacity acceptance run")
		}
		configureNodeClass("capacity-reservation", "capacity-reservation")
		configureNodePool("capacity-reservation")
		nodePool.Spec.Template.Spec.Requirements = withInstanceTypeRequirements(capacityReservationInstanceTypes()...)
		preference := "target"
		nodeClass.Spec.CapacityReservationPreference = &preference
		nodeClass.Spec.CapacityReservationSelectorTerms = []v1alpha1.CapacityReservationSelectorTerm{{ID: &capacityReservationID}}
		pod := schedulingPod("capacity-reservation", map[string]string{
			karpv1.NodePoolLabelKey:    nodePool.Name,
			v1alpha1.LabelCapacityType: v1alpha1.CapacityTypeOnDemand,
		}, corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		})

		env.ExpectCreated(nodePool, nodeClass, pod)
		env.EventuallyExpectHealthy(pod)
	})

	It("should provision a node that matches hugepages resource requests", Label("hugepages"), func() {
		if os.Getenv("FEATURE_GATES") == "" || !strings.Contains(os.Getenv("FEATURE_GATES"), "NodeOverlay=true") {
			Skip("hugepages scheduling requires FEATURE_GATES with NodeOverlay=true")
		}
		configureNodeClass("hugepages", "hugepages")
		configureNodePool("hugepages")
		instanceType := testInstanceTypes()[0]
		nodePool.Spec.Template.Spec.Requirements = withInstanceTypeRequirements(instanceType)
		nodePool.Spec.Limits = karpv1.Limits{
			corev1.ResourceCPU: resource.MustParse("2"),
		}
		nodeOverlay := &karpv1alpha1.NodeOverlay{
			ObjectMeta: metav1.ObjectMeta{Name: "scheduling-test-hugepages"},
			Spec: karpv1alpha1.NodeOverlaySpec{
				Requirements: []corev1.NodeSelectorRequirement{{
					Key:      corev1.LabelInstanceTypeStable,
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{instanceType},
				}},
				Capacity: corev1.ResourceList{
					corev1.ResourceName("hugepages-2Mi"): resource.MustParse("1Gi"),
				},
			},
		}
		nodeClass.Spec.UserData = stringPtr(hugePagesUserData)
		pod := schedulingPod("hugepages", map[string]string{karpv1.NodePoolLabelKey: nodePool.Name}, corev1.ResourceList{
			corev1.ResourceCPU:                   resource.MustParse("100m"),
			corev1.ResourceMemory:                resource.MustParse("128Mi"),
			corev1.ResourceName("hugepages-2Mi"): resource.MustParse("100Mi"),
		})
		pod.Spec.Containers[0].Resources.Limits = corev1.ResourceList{
			corev1.ResourceName("hugepages-2Mi"): resource.MustParse("100Mi"),
		}

		env.ExpectCreated(nodePool, nodeClass, nodeOverlay, pod)
		env.EventuallyExpectHealthy(pod)
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]
		Expect(node.Labels).To(HaveKeyWithValue(corev1.LabelInstanceTypeStable, instanceType))
	})
})

func configureNodeClass(name, testType string) {
	nodeClass.Name = "scheduling-test-" + name
	nodeClass.Spec.Tags = env.TestTags(testType)
}

func configureNodePool(name string) {
	nodePool.Name = "scheduling-test-pool-" + name
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
				Values:   testInstanceTypes(),
			},
		},
	}
}

func schedulingDeployment(name string, replicas int32, cpu, memory string, antiAffinity bool) *appsv1.Deployment {
	labels := map[string]string{"app": "scheduling-test-" + name}
	spec := corev1.PodSpec{
		Containers: []corev1.Container{
			{
				Name:  "pause",
				Image: "registry-cn-hangzhou.ack.aliyuncs.com/acs/pause:3.9",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse(cpu),
						corev1.ResourceMemory: resource.MustParse(memory),
					},
				},
			},
		},
		NodeSelector: map[string]string{
			karpv1.NodePoolLabelKey: nodePool.Name,
		},
	}
	if antiAffinity {
		spec.Affinity = &corev1.Affinity{
			PodAntiAffinity: &corev1.PodAntiAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{MatchLabels: labels},
						TopologyKey:   corev1.LabelHostname,
					},
				},
			},
		}
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "scheduling-test-" + name,
			Namespace: "default",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       spec,
			},
		},
	}
}

func schedulingPod(name string, nodeSelector map[string]string, requests corev1.ResourceList, initContainers ...corev1.Container) *corev1.Pod {
	limits := corev1.ResourceList{}
	if gpu, ok := requests[v1alpha1.ResourceGPU]; ok {
		limits[v1alpha1.ResourceGPU] = gpu
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "scheduling-test-" + name,
			Namespace: "default",
			Labels:    map[string]string{"app": "scheduling-test-" + name},
		},
		Spec: corev1.PodSpec{
			NodeSelector:      nodeSelector,
			InitContainers:    initContainers,
			RestartPolicy:     corev1.RestartPolicyNever,
			PriorityClassName: "scheduling-test-default",
			Containers: []corev1.Container{{
				Name:  "pause",
				Image: "registry-cn-hangzhou.ack.aliyuncs.com/acs/pause:3.9",
				Resources: corev1.ResourceRequirements{
					Requests: requests,
					Limits:   limits,
				},
			}},
		},
	}
}

var _ = BeforeEach(func() {
	env.ExpectCreatedOrUpdated(&schedulingv1.PriorityClass{
		ObjectMeta: metav1.ObjectMeta{Name: "scheduling-test-default"},
		Value:      1000,
	})
})

func testInstanceTypes() []string {
	if values := envList("TEST_INSTANCE_TYPES"); len(values) > 0 {
		return values
	}
	return []string{"ecs.g7.large", "ecs.g7.xlarge"}
}

func capacityReservationInstanceTypes() []string {
	if values := envList("TEST_CAPACITY_RESERVATION_INSTANCE_TYPE"); len(values) > 0 {
		return values
	}
	return []string{testInstanceTypes()[0]}
}

func withInstanceTypeRequirements(instanceTypes ...string) []karpv1.NodeSelectorRequirementWithMinValues {
	return []karpv1.NodeSelectorRequirementWithMinValues{
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
				Values:   instanceTypes,
			},
		},
	}
}

func int32Ptr(v int32) *int32 {
	return &v
}

func stringPtr(v string) *string {
	return &v
}

func instanceFamily(name string) string {
	parts := strings.Split(strings.TrimPrefix(name, "ecs."), ".")
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func instanceSize(name string) string {
	parts := strings.Split(strings.TrimPrefix(name, "ecs."), ".")
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

func instanceCategory(name string) string {
	family := instanceFamily(name)
	var b strings.Builder
	for _, r := range family {
		if r < 'a' || r > 'z' {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

func instanceGeneration(name string) string {
	family := instanceFamily(name)
	var b strings.Builder
	for _, r := range family {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
			continue
		}
		if b.Len() > 0 {
			break
		}
	}
	return b.String()
}

const hugePagesUserData = `#!/bin/bash
set -euxo pipefail
sysctl -w vm.nr_hugepages=512
grep -q '^vm.nr_hugepages=' /etc/sysctl.conf || echo 'vm.nr_hugepages=512' >> /etc/sysctl.conf
systemctl restart kubelet || true
`

func envList(key string) []string {
	raw := os.Getenv(key)
	if raw == "" {
		return nil
	}
	var values []string
	for _, part := range strings.Split(raw, ",") {
		if value := strings.TrimSpace(part); value != "" {
			values = append(values, value)
		}
	}
	return values
}
