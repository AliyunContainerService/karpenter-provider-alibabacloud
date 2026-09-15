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

package scale

import (
	"context"
	"strings"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Scale Acceptance Extensions", func() {
	It("should scale back up after an interrupting ECS termination", Label("interrupt"), func() {
		configureScaleSubject("interrupt")
		deployment := scaleDeployment("interrupt", 2)
		env.ExpectCreated(nodeClass, nodePool, deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectHealthyPodCount(selector, 2)
		node := env.EventuallyExpectCreatedNodeCount(">=", 1)[0]
		region, instanceID := parseScaleProviderID(node.Spec.ProviderID)
		_, err := env.ECSAPI.DeleteInstances(context.Background(), &ecs.DeleteInstancesRequest{
			RegionId:              tea.String(region),
			InstanceId:            []*string{tea.String(instanceID)},
			Force:                 tea.Bool(true),
			TerminateSubscription: tea.Bool(true),
		})
		Expect(err).ToNot(HaveOccurred())
		env.EventuallyExpectHealthyPodCount(selector, 2)
	})

	It("should scale from zero to one node", Label("node-dense"), func() {
		configureScaleSubject("zero-to-one")
		deployment := scaleDeployment("zero-to-one", 1)
		env.ExpectCreated(deployment)
		env.EventuallyExpectPendingPodCount(labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels), 1)
		env.ExpectCreated(nodeClass, nodePool)
		env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels), 1)
		env.EventuallyExpectCreatedNodeCount("==", 1)
	})

	It("should scale replicas up on an existing NodePool", Label("pod-dense"), func() {
		configureScaleSubject("replica-up")
		deployment := scaleDeployment("replica-up", 1)
		env.ExpectCreated(nodeClass, nodePool, deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectHealthyPodCount(selector, 1)
		deployment.Spec.Replicas = tea.Int32(4)
		env.ExpectUpdated(deployment)
		env.EventuallyExpectHealthyPodCount(selector, 4)
	})

	It("should scale nodes down after replica reduction", Label("consolidation"), Label("empty"), func() {
		configureScaleSubject("replica-down")
		nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmptyOrUnderutilized
		nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")
		deployment := scaleDeployment("replica-down", 3)
		env.ExpectCreated(nodeClass, nodePool, deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectHealthyPodCount(selector, 3)
		deployment.Spec.Replicas = tea.Int32(1)
		env.ExpectUpdated(deployment)
		env.EventuallyExpectHealthyPodCount(selector, 1)
	})
})

func configureScaleSubject(name string) {
	nodeClass.Name = "scale-test-" + name
	nodeClass.Spec.Tags = env.TestTags(name)
	nodePool.Name = "scale-test-pool-" + name
	nodePool.Spec.Template.Spec.NodeClassRef = &karpv1.NodeClassReference{
		Group: "karpenter.alibabacloud.com",
		Kind:  "ECSNodeClass",
		Name:  nodeClass.Name,
	}
	nodePool.Spec.Template.Spec.Requirements = []karpv1.NodeSelectorRequirementWithMinValues{
		{NodeSelectorRequirement: corev1.NodeSelectorRequirement{
			Key:      v1alpha1.LabelCapacityType,
			Operator: corev1.NodeSelectorOpIn,
			Values:   []string{v1alpha1.CapacityTypeOnDemand},
		}},
		{NodeSelectorRequirement: corev1.NodeSelectorRequirement{
			Key:      v1alpha1.LabelInstanceType,
			Operator: corev1.NodeSelectorOpIn,
			Values:   testInstanceTypes(),
		}},
	}
}

func scaleDeployment(name string, replicas int32) *appsv1.Deployment {
	podLabels := map[string]string{"app": "scale-test-" + name}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "scale-test-" + name, Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: podLabels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: podLabels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "pause",
						Image: "registry-cn-hangzhou.ack.aliyuncs.com/acs/pause:3.9",
						Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("50m"),
							corev1.ResourceMemory: resource.MustParse("64Mi"),
						}},
					}},
					NodeSelector: map[string]string{karpv1.NodePoolLabelKey: nodePool.Name},
				},
			},
		},
	}
}

func parseScaleProviderID(providerID string) (string, string) {
	parts := strings.Split(providerID, ".")
	Expect(parts).To(HaveLen(2), "expected providerID format <region>.<instance-id>")
	return parts[0], parts[1]
}
