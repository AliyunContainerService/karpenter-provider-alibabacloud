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
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("IPv6 Networking", func() {
	It("should allocate IPv6 DNS service addresses", Label("ipv6"), Label("dns"), func() {
		Expect(strings.EqualFold(os.Getenv("TEST_IP_FAMILY"), "ipv6")).To(BeTrue(), "TEST_IP_FAMILY=ipv6 is required for the IPv6 DNS suite")
		configureNodeClassAndPool("ipv6-dns")

		deployment := ipv6Deployment("ipv6-dns", 1)
		service := ipv6Service("ipv6-dns")
		service.Name = "ipv6-test-dns"
		env.ExpectCreated(nodeClass, nodePool, deployment, service)
		env.EventuallyExpectHealthyPodCount(labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels), 1)

		Eventually(func(g Gomega) {
			current := &corev1.Service{}
			g.Expect(env.Client.Get(env.Context, client.ObjectKey{Namespace: service.Namespace, Name: service.Name}, current)).To(Succeed())
			g.Expect(current.Spec.ClusterIP).To(ContainSubstring(":"))
			g.Expect(current.Spec.IPFamilies).To(ContainElement(corev1.IPv6Protocol))
		}).WithTimeout(2 * time.Minute).Should(Succeed())
	})

	It("should assign primary IPv6 pod and node prefixes", Label("ipv6"), Label("prefix"), Label("primary"), func() {
		Expect(strings.EqualFold(os.Getenv("TEST_IP_FAMILY"), "ipv6")).To(BeTrue(), "TEST_IP_FAMILY=ipv6 is required for the IPv6 prefix suite")
		configureNodeClassAndPool("ipv6-prefix-primary")

		deployment := ipv6Deployment("ipv6-prefix-primary", 1)
		env.ExpectCreated(nodeClass, nodePool, deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectHealthyPodCount(selector, 1)

		Eventually(func(g Gomega) {
			pods := &corev1.PodList{}
			g.Expect(env.Client.List(env.Context, pods, &client.ListOptions{LabelSelector: selector})).To(Succeed())
			g.Expect(pods.Items).To(HaveLen(1))
			g.Expect(isIPv6(pods.Items[0].Status.PodIP)).To(BeTrue(), "primary pod IP should be IPv6")
			g.Expect(pods.Items[0].Status.PodIPs).ToNot(BeEmpty(), "pod IP prefix list should be populated")
		}).WithTimeout(2 * time.Minute).Should(Succeed())

		nodes := env.EventuallyExpectCreatedNodeCount("==", 1)
		Expect(hasIPv6NodeAddress(nodes[0])).To(BeTrue(), "primary node address should include IPv6")
	})
})
