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

package interruption

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	environmentcs "github.com/AliyunContainerService/karpenter-provider-alibabacloud/test/pkg/cs"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var env *environmentcs.Environment
var nodeClass *v1alpha1.ECSNodeClass
var nodePool *karpv1.NodePool

func TestInterruption(t *testing.T) {
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
	RunSpecs(t, "Interruption")
}

var _ = BeforeEach(func() {
	env.BeforeEach()
	nodeClass = env.DefaultECSNodeClass()
	nodePool = env.DefaultNodePool(nodeClass)
})

var _ = AfterEach(func() {
	env.AfterEach()
})

var _ = Describe("Interruption", func() {
	It("should recover workload when the backing ECS instance is deleted", Label("instance-terminated"), func() {
		configureNodeClassAndPool("instance-terminated")

		replicas := int32(1)
		deployment := interruptionDeployment("instance-terminated", replicas)
		env.ExpectCreated(nodeClass, nodePool, deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectHealthyPodCount(selector, int(replicas))
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]

		region, instanceID := parseProviderID(node.Spec.ProviderID)
		_, err := env.ECSAPI.DeleteInstances(context.Background(), &ecs.DeleteInstancesRequest{
			RegionId:              tea.String(region),
			InstanceId:            []*string{tea.String(instanceID)},
			Force:                 tea.Bool(true),
			TerminateSubscription: tea.Bool(true),
		})
		Expect(err).ToNot(HaveOccurred())

		Eventually(func(g Gomega) {
			current := &corev1.Node{}
			err := env.Client.Get(env.Context, client.ObjectKeyFromObject(node), current)
			g.Expect(err).To(HaveOccurred())
		}).WithTimeout(10 * time.Minute).Should(Succeed())
		env.EventuallyExpectHealthyPodCount(selector, int(replicas))
	})

	It("should launch interruptible spot capacity when requested", Label("spot"), func() {
		configureNodeClassAndPool("spot")
		spotStrategy := "SpotAsPriceGo"
		nodeClass.Spec.SpotStrategy = &spotStrategy
		nodePool.Spec.Template.Spec.Requirements[0].Values = []string{v1alpha1.CapacityTypeSpot}

		replicas := int32(1)
		deployment := interruptionDeployment("spot", replicas)
		env.ExpectCreated(nodeClass, nodePool, deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectHealthyPodCount(selector, int(replicas))
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]

		Expect(node.Labels).To(HaveKeyWithValue(v1alpha1.LabelCapacityType, v1alpha1.CapacityTypeSpot))
	})
})

func configureNodeClassAndPool(name string) {
	nodeClass.Name = "interruption-test-" + name
	nodeClass.Spec.Tags = env.TestTags(name)
	nodePool.Name = "interruption-test-pool-" + name
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

func interruptionDeployment(name string, replicas int32) *appsv1.Deployment {
	labels := map[string]string{"app": "interruption-test-" + name}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "interruption-test-" + name,
			Namespace: "default",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					TerminationGracePeriodSeconds: tea.Int64(0),
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

func parseProviderID(providerID string) (string, string) {
	parts := strings.Split(providerID, ".")
	Expect(parts).To(HaveLen(2), "expected providerID format <region>.<instance-id>")
	return parts[0], parts[1]
}

func testInstanceTypes() []string {
	if values := envList("TEST_INSTANCE_TYPES"); len(values) > 0 {
		return values
	}
	return []string{"ecs.g7.large", "ecs.g7.xlarge"}
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
