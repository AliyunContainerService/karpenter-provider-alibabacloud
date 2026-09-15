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

package ipv6

import (
	"net/netip"
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
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var env *environmentcs.Environment
var nodeClass *v1alpha1.ECSNodeClass
var nodePool *karpv1.NodePool

func TestIPv6(t *testing.T) {
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
	RunSpecs(t, "IPv6")
}

var _ = BeforeEach(func() {
	env.BeforeEach()
	nodeClass = env.DefaultECSNodeClass()
	nodePool = env.DefaultNodePool(nodeClass)
})

var _ = AfterEach(func() {
	env.AfterEach()
})

var _ = Describe("IPv6", func() {
	It("should run workload behind an IPv6 SingleStack service", Label("ipv6-service"), func() {
		Expect(strings.EqualFold(os.Getenv("TEST_IP_FAMILY"), "ipv6")).To(BeTrue(), "TEST_IP_FAMILY=ipv6 is required for the IPv6 suite")
		configureNodeClassAndPool("ipv6-service")

		replicas := int32(1)
		deployment := ipv6Deployment("ipv6-service", replicas)
		service := ipv6Service("ipv6-service")
		env.ExpectCreated(nodeClass, nodePool, deployment, service)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectHealthyPodCount(selector, int(replicas))

		Eventually(func(g Gomega) {
			current := &corev1.Service{}
			g.Expect(env.Client.Get(env.Context, clientKey(service), current)).To(Succeed())
			g.Expect(current.Spec.IPFamilies).To(ContainElement(corev1.IPv6Protocol))
			g.Expect(current.Spec.ClusterIP).To(ContainSubstring(":"))
		}).WithTimeout(2 * time.Minute).Should(Succeed())

		Eventually(func(g Gomega) {
			pods := &corev1.PodList{}
			g.Expect(env.Client.List(env.Context, pods, &client.ListOptions{LabelSelector: selector})).To(Succeed())
			g.Expect(pods.Items).ToNot(BeEmpty())
			for _, pod := range pods.Items {
				g.Expect(hasIPv6PodIP(pod)).To(BeTrue(), "expected pod %s/%s to have an IPv6 PodIP", pod.Namespace, pod.Name)
			}
		}).WithTimeout(2 * time.Minute).Should(Succeed())

		Eventually(func(g Gomega) {
			nodes := env.Monitor.CreatedNodes()
			g.Expect(nodes).ToNot(BeEmpty())
			for _, node := range nodes {
				g.Expect(hasIPv6NodeAddress(node)).To(BeTrue(), "expected node %s to have an IPv6 node address", node.Name)
			}
		}).WithTimeout(5 * time.Minute).Should(Succeed())
	})
})

func configureNodeClassAndPool(name string) {
	nodeClass.Name = "ipv6-test-" + name
	nodeClass.Spec.Tags = env.TestTags(name)
	nodePool.Name = "ipv6-test-pool-" + name
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

func ipv6Deployment(name string, replicas int32) *appsv1.Deployment {
	labels := map[string]string{"app": "ipv6-test-" + name}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "ipv6-test-" + name, Namespace: "default"},
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
							Ports: []corev1.ContainerPort{{ContainerPort: 8080}},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("100m"),
									corev1.ResourceMemory: resource.MustParse("128Mi"),
								},
							},
						},
					},
					NodeSelector: map[string]string{karpv1.NodePoolLabelKey: nodePool.Name},
				},
			},
		},
	}
}

func ipv6Service(name string) *corev1.Service {
	policy := corev1.IPFamilyPolicySingleStack
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "ipv6-test-" + name, Namespace: "default"},
		Spec: corev1.ServiceSpec{
			Selector:       map[string]string{"app": "ipv6-test-" + name},
			IPFamilyPolicy: &policy,
			IPFamilies:     []corev1.IPFamily{corev1.IPv6Protocol},
			Ports: []corev1.ServicePort{
				{
					Port:       80,
					TargetPort: intstr.FromInt32(8080),
				},
			},
		},
	}
}

func clientKey(obj metav1.Object) client.ObjectKey {
	return client.ObjectKey{Namespace: obj.GetNamespace(), Name: obj.GetName()}
}

func hasIPv6PodIP(pod corev1.Pod) bool {
	for _, podIP := range pod.Status.PodIPs {
		if isIPv6(podIP.IP) {
			return true
		}
	}
	return isIPv6(pod.Status.PodIP)
}

func hasIPv6NodeAddress(node *corev1.Node) bool {
	if node == nil {
		return false
	}
	for _, address := range node.Status.Addresses {
		if address.Type == corev1.NodeInternalIP || address.Type == corev1.NodeExternalIP {
			if isIPv6(address.Address) {
				return true
			}
		}
	}
	return false
}

func isIPv6(value string) bool {
	addr, err := netip.ParseAddr(strings.TrimSpace(value))
	return err == nil && addr.Is6()
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
