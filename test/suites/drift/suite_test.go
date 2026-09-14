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

package drift

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	environmentcs "github.com/AliyunContainerService/karpenter-provider-alibabacloud/test/pkg/cs"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var env *environmentcs.Environment
var nodeClass *v1alpha1.ECSNodeClass
var nodePool *karpv1.NodePool

func TestDrift(t *testing.T) {
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
	RunSpecs(t, "Drift")
}

var _ = BeforeEach(func() {
	env.BeforeEach()
	nodeClass = env.DefaultECSNodeClass()
	nodePool = env.DefaultNodePool(nodeClass)
})

var _ = AfterEach(func() {
	env.AfterEach()
})

var _ = Describe("Drift", func() {
	It("should replace nodes when NodeClass configuration drifts", Label("drift"), func() {
		configureNodeClass("nodeclass", map[string]string{
			"version": "v1",
		})
		configureNodePool("nodeclass")

		replicas := int32(2)
		deployment := driftDeployment("nodeclass", replicas)
		env.ExpectCreated(nodePool, nodeClass, deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectHealthyPodCount(selector, int(replicas))
		initialNodes := env.Monitor.CreatedNodes()
		Expect(initialNodes).ToNot(BeEmpty())

		nodeClass.Spec.Tags["version"] = "v2"
		nodeClass.Spec.Tags["updated"] = "true"
		env.ExpectUpdated(nodeClass)

		Eventually(func(g Gomega) {
			currentNodes := env.Monitor.CreatedNodes()
			g.Expect(hasReplacementNode(initialNodes, currentNodes)).To(BeTrue(), "expected a replacement node after NodeClass drift")
		}).WithTimeout(10 * time.Minute).Should(Succeed())

		env.EventuallyExpectHealthyPodCount(selector, int(replicas))
	})

	It("should replace nodes when they expire", Label("expiration"), func() {
		configureNodeClass("expiration", nil)
		configureNodePool("expiration")
		nodePool.Spec.Template.Spec.ExpireAfter = karpv1.MustParseNillableDuration("5m")

		replicas := int32(2)
		deployment := driftDeployment("expiration", replicas)
		env.ExpectCreated(nodePool, nodeClass, deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectHealthyPodCount(selector, int(replicas))
		initialNodes := env.Monitor.CreatedNodes()
		Expect(initialNodes).ToNot(BeEmpty())

		Eventually(func(g Gomega) {
			currentNodes := env.Monitor.CreatedNodes()
			g.Expect(hasReplacementNode(initialNodes, currentNodes)).To(BeTrue(), "expected at least one replacement node after expiration")
			g.Expect(currentNodes).ToNot(BeEmpty())
		}).WithTimeout(10 * time.Minute).Should(Succeed())

		env.EventuallyExpectHealthyPodCount(selector, int(replicas))
	})
})

func configureNodeClass(name string, extraTags map[string]string) {
	nodeClass.Name = "drift-test-" + name
	nodeClass.Spec.Tags = env.TestTags(name)
	for key, value := range extraTags {
		nodeClass.Spec.Tags[key] = value
	}
}

func configureNodePool(name string) {
	nodePool.Name = "drift-test-pool-" + name
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

func driftDeployment(name string, replicas int32) *appsv1.Deployment {
	labels := map[string]string{"app": "drift-test-" + name}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "drift-test-" + name,
			Namespace: "default",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
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
}

func hasReplacementNode(initialNodes, currentNodes []*corev1.Node) bool {
	for _, currentNode := range currentNodes {
		found := false
		for _, initialNode := range initialNodes {
			if currentNode.Name == initialNode.Name {
				found = true
				break
			}
		}
		if !found {
			return true
		}
	}
	return false
}

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

func restrictNodePoolInstanceTypes(instanceTypes ...string) {
	for i := range nodePool.Spec.Template.Spec.Requirements {
		if nodePool.Spec.Template.Spec.Requirements[i].Key == v1alpha1.LabelInstanceType {
			nodePool.Spec.Template.Spec.Requirements[i].Values = instanceTypes
			return
		}
	}
}

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
