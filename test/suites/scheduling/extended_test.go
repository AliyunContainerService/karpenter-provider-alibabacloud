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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	. "github.com/onsi/ginkgo/v2"
)

var _ = Describe("Scheduling Additional Contracts", func() {
	It("should provision a node for naked pods", Label("naked-pods"), func() {
		configureNodeClass("naked-pods", "naked-pods")
		configureNodePool("naked-pods")
		pod := schedulingPod("naked-pods", map[string]string{karpv1.NodePoolLabelKey: nodePool.Name}, smallRequests())
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		env.EventuallyExpectCreatedNodeCount("==", 1)
	})

	It("should provision a node for a deployment", Label("deployment"), func() {
		configureNodeClass("deployment", "deployment")
		configureNodePool("deployment")
		deployment := schedulingDeployment("deployment", 3, "50m", "64Mi", false)
		env.ExpectCreated(nodeClass, nodePool, deployment)
		env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels), 3)
	})

	It("should provision nodes for topology spread", Label("topology-spread"), func() {
		configureNodeClass("topology-spread", "topology-spread")
		configureNodePool("topology-spread")
		podLabels := map[string]string{"app": "scheduling-test-topology-spread"}
		deployment := schedulingDeployment("topology-spread", 2, "100m", "128Mi", false)
		deployment.Spec.Template.Labels = podLabels
		deployment.Spec.Selector.MatchLabels = podLabels
		deployment.Spec.Template.Spec.TopologySpreadConstraints = []corev1.TopologySpreadConstraint{{
			MaxSkew:           1,
			TopologyKey:       corev1.LabelHostname,
			WhenUnsatisfiable: corev1.DoNotSchedule,
			LabelSelector:     &metav1.LabelSelector{MatchLabels: podLabels},
		}}
		env.ExpectCreated(nodeClass, nodePool, deployment)
		env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(podLabels), 2)
	})

	It("should provision pods with self affinity", Label("affinity"), func() {
		configureNodeClass("self-affinity", "self-affinity")
		configureNodePool("self-affinity")
		podLabels := map[string]string{"app": "scheduling-test-self-affinity"}
		deployment := schedulingDeployment("self-affinity", 2, "100m", "128Mi", false)
		deployment.Spec.Template.Labels = podLabels
		deployment.Spec.Selector.MatchLabels = podLabels
		deployment.Spec.Template.Spec.Affinity = &corev1.Affinity{PodAffinity: &corev1.PodAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{{
				LabelSelector: &metav1.LabelSelector{MatchLabels: podLabels},
				TopologyKey:   corev1.LabelHostname,
			}},
		}}
		env.ExpectCreated(nodeClass, nodePool, deployment)
		env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(podLabels), 2)
	})

	It("should respect node selector custom labels", Label("well-known-labels"), func() {
		configureNodeClass("custom-labels", "custom-labels")
		configureNodePool("custom-labels")
		nodePool.Spec.Template.Labels = map[string]string{"team": "karpenter-e2e"}
		pod := schedulingPod("custom-labels", map[string]string{
			karpv1.NodePoolLabelKey: nodePool.Name,
			"team":                  "karpenter-e2e",
		}, smallRequests())
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
	})

	It("should schedule by stable architecture label", Label("well-known-labels"), func() {
		configureNodeClass("arch", "arch")
		configureNodePool("arch")
		pod := schedulingPod("arch", map[string]string{
			karpv1.NodePoolLabelKey: nodePool.Name,
			corev1.LabelArchStable:  "amd64",
		}, smallRequests())
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
	})

	It("should schedule by stable operating system label", Label("well-known-labels"), func() {
		configureNodeClass("os", "os")
		configureNodePool("os")
		pod := schedulingPod("os", map[string]string{
			karpv1.NodePoolLabelKey: nodePool.Name,
			corev1.LabelOSStable:    "linux",
		}, smallRequests())
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
	})

	It("should schedule when minvalues can be satisfied", Label("minvalues"), func() {
		configureNodeClass("minvalues", "minvalues")
		configureNodePool("minvalues")
		min := 1
		nodePool.Spec.Template.Spec.Requirements[1].MinValues = &min
		pod := schedulingPod("minvalues", map[string]string{karpv1.NodePoolLabelKey: nodePool.Name}, smallRequests())
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
	})

	It("should keep pods pending when NodePool selector does not match", Label("nodepool-selector"), func() {
		configureNodeClass("selector-mismatch", "selector-mismatch")
		configureNodePool("selector-mismatch")
		pod := schedulingPod("selector-mismatch", map[string]string{karpv1.NodePoolLabelKey: "does-not-exist"}, smallRequests())
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectPendingPodCount(labels.SelectorFromSet(pod.Labels), 1)
		env.ConsistentlyExpectNodeCount("==", 0, time.Minute)
	})

	It("should schedule pods with larger memory requests", Label("deployment"), func() {
		configureNodeClass("memory", "memory")
		configureNodePool("memory")
		pod := schedulingPod("memory", map[string]string{karpv1.NodePoolLabelKey: nodePool.Name}, corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("1Gi"),
		})
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
	})

	It("should schedule pods with larger cpu requests", Label("deployment"), func() {
		configureNodeClass("cpu", "cpu")
		configureNodePool("cpu")
		pod := schedulingPod("cpu", map[string]string{karpv1.NodePoolLabelKey: nodePool.Name}, corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("1500m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		})
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
	})

	It("should support multiple compatible instance types", Label("instance-type"), func() {
		configureNodeClass("multi-instance-type", "multi-instance-type")
		configureNodePool("multi-instance-type")
		nodePool.Spec.Template.Spec.Requirements = withInstanceTypeRequirements(testInstanceTypes()...)
		pod := schedulingPod("multi-instance-type", map[string]string{karpv1.NodePoolLabelKey: nodePool.Name}, smallRequests())
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
	})

	It("should schedule to a node with NodePool template taints tolerated", Label("deployment"), func() {
		configureNodeClass("taints", "taints")
		configureNodePool("taints")
		nodePool.Spec.Template.Spec.Taints = []corev1.Taint{{Key: "dedicated", Value: "scheduling", Effect: corev1.TaintEffectNoSchedule}}
		pod := schedulingPod("taints", map[string]string{karpv1.NodePoolLabelKey: nodePool.Name}, smallRequests())
		pod.Spec.Tolerations = []corev1.Toleration{{Key: "dedicated", Value: "scheduling", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule}}
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
	})

	It("should schedule pods after NodePool requirements are narrowed", Label("instance-type"), func() {
		configureNodeClass("narrowed", "narrowed")
		configureNodePool("narrowed")
		nodePool.Spec.Template.Spec.Requirements = withInstanceTypeRequirements(testInstanceTypes()[0])
		pod := schedulingPod("narrowed", map[string]string{karpv1.NodePoolLabelKey: nodePool.Name}, smallRequests())
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
	})

	It("should keep workloads healthy across repeated scheduling batches", Label("deployment"), func() {
		configureNodeClass("batch", "batch")
		configureNodePool("batch")
		deployment := schedulingDeployment("batch", 2, "50m", "64Mi", false)
		env.ExpectCreated(nodeClass, nodePool, deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectHealthyPodCount(selector, 2)
		deployment.Spec.Replicas = int32Ptr(4)
		env.ExpectUpdated(deployment)
		env.EventuallyExpectHealthyPodCount(selector, 4)
	})
})

func smallRequests() corev1.ResourceList {
	return corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("100m"),
		corev1.ResourceMemory: resource.MustParse("128Mi"),
	}
}
