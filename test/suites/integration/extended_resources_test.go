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

package integration

import (
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	. "github.com/onsi/ginkgo/v2"
)

var _ = Describe("Extended Resources", func() {
	It("should provision nodes for a deployment that requests nvidia.com/gpu", Label("extended-resources", "gpu"), func() {
		if len(testGPUInstanceTypes()) == 0 || len(testGPUZones()) == 0 {
			Skip("GPU integration test requires TEST_GPU_INSTANCE_TYPES and TEST_GPU_ZONES from e2e GPU discovery")
		}

		configureNodeClassAndPool("gpu")
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
					Values:   testGPUInstanceTypes(),
				},
			},
			{
				NodeSelectorRequirement: corev1.NodeSelectorRequirement{
					Key:      corev1.LabelTopologyZone,
					Operator: corev1.NodeSelectorOpIn,
					Values:   testGPUZones(),
				},
			},
		}

		replicas := int32(1)
		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "integration-gpu-workload", Namespace: "default"},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "integration-gpu"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "integration-gpu"}},
					Spec: corev1.PodSpec{
						NodeSelector: map[string]string{karpv1.NodePoolLabelKey: nodePool.Name},
						Containers: []corev1.Container{{
							Name:  "pause",
							Image: "registry-cn-hangzhou.ack.aliyuncs.com/acs/pause:3.9",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("1Gi"),
									v1alpha1.ResourceGPU:  resource.MustParse("1"),
								},
								Limits: corev1.ResourceList{
									v1alpha1.ResourceGPU: resource.MustParse("1"),
								},
							},
						}},
					},
				},
			},
		}

		env.ExpectCreated(deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectPendingPodCount(selector, int(replicas))
		env.ExpectCreated(nodeClass, nodePool)
		env.EventuallyExpectHealthyPodCount(selector, int(replicas))

		for _, node := range env.Monitor.CreatedNodes() {
			if _, ok := node.Status.Capacity[v1alpha1.ResourceGPU]; ok {
				return
			}
		}
		Fail("expected at least one created node to expose nvidia.com/gpu capacity")
	})
})
