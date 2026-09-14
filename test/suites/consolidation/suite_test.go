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

package consolidation

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	environmentcs "github.com/AliyunContainerService/karpenter-provider-alibabacloud/test/pkg/cs"
	"github.com/samber/lo"
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

func TestConsolidation(t *testing.T) {
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
	RunSpecs(t, "Consolidation")
}

var _ = BeforeEach(func() {
	env.BeforeEach()
	nodeClass = env.DefaultECSNodeClass()
	nodePool = env.DefaultNodePool(nodeClass)
})

var _ = AfterEach(func() {
	env.AfterEach()
})

var _ = Describe("Consolidation", func() {
	It("should delete empty nodes after workload is removed", Label("consolidation-empty"), func() {
		configureNodeClass("consolidation-empty", "consolidation-empty")
		configureNodePool("consolidation-empty")
		nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmptyOrUnderutilized
		nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")

		replicas := int32(5)
		deployment := consolidationDeployment("consolidation-empty", replicas, true)

		env.ExpectCreated(nodePool, nodeClass, deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectHealthyPodCount(selector, int(replicas))
		Expect(env.Monitor.CreatedNodes()).To(HaveLen(5))

		env.ExpectDeleted(deployment)

		Eventually(func(g Gomega) {
			g.Expect(env.Monitor.CreatedNodes()).To(HaveLen(0), "expected all empty nodes to be consolidated")
		}).WithTimeout(10 * time.Minute).Should(Succeed())
	})

	It("should consolidate underutilized nodes", Label("consolidation-underutilized"), func() {
		configureNodeClass("consolidation-underutilized", "consolidation-underutilized")
		configureNodePool("consolidation-underutilized")
		nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmptyOrUnderutilized
		nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")

		replicas := int32(5)
		deployment := consolidationDeployment("consolidation-underutilized", replicas, true)

		env.ExpectCreated(nodePool, nodeClass, deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectHealthyPodCount(selector, int(replicas))
		Expect(env.Monitor.CreatedNodes()).To(HaveLen(5))

		deployment.Spec.Replicas = lo.ToPtr(int32(1))
		env.ExpectUpdated(deployment)
		env.EventuallyExpectHealthyPodCount(selector, 1)

		Eventually(func(g Gomega) {
			g.Expect(len(env.Monitor.CreatedNodes())).To(BeNumerically("<=", 2), "expected nodes to be consolidated from five nodes")
		}).WithTimeout(10 * time.Minute).Should(Succeed())
	})

	It("should delete nodes when they become empty", Label("emptiness"), func() {
		configureNodeClass("emptiness", "emptiness")
		configureNodePool("emptiness")
		nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmpty
		nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")

		replicas := int32(3)
		deployment := consolidationDeployment("emptiness", replicas, false)

		env.ExpectCreated(nodePool, nodeClass, deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectHealthyPodCount(selector, int(replicas))
		Expect(env.Monitor.CreatedNodes()).ToNot(BeEmpty())

		env.ExpectDeleted(deployment)

		Eventually(func(g Gomega) {
			g.Expect(env.Monitor.CreatedNodes()).To(HaveLen(0), "expected all empty nodes to be deleted")
		}).WithTimeout(5 * time.Minute).Should(Succeed())
	})
})

func configureNodeClass(name, testType string) {
	nodeClass.Name = "consolidation-test-" + name
	nodeClass.Spec.Tags = env.TestTags(testType)
}

func configureNodePool(name string) {
	nodePool.Name = "consolidation-test-pool-" + name
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

func consolidationDeployment(name string, replicas int32, antiAffinity bool) *appsv1.Deployment {
	labels := map[string]string{"app": "consolidation-test-" + name}
	spec := corev1.PodSpec{
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
			Name:      "consolidation-test-" + name,
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
