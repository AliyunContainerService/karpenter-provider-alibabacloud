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
	"time"

	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Interruption Recovery", func() {
	It("should launch replacement capacity when a NodeClaim is interrupted", Label("replacement"), Label("recover"), func() {
		configureNodeClassAndPool("nodeclaim-replacement")
		deployment := interruptionDeployment("nodeclaim-replacement", 1)
		env.ExpectCreated(nodeClass, nodePool, deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectHealthyPodCount(selector, 1)
		nodeClaim := env.EventuallyExpectCreatedNodeClaimCount("==", 1)[0]
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]

		env.Monitor.Reset()
		env.ExpectDeleted(nodeClaim)
		env.EventuallyExpectNotFound(nodeClaim, node)
		env.EventuallyExpectHealthyPodCount(selector, 1)
		env.EventuallyExpectCreatedNodeClaimCount("==", 1)
	})

	It("should recover from scheduled health event disruption", Label("scheduled-change"), Label("health-event"), Label("event"), func() {
		configureNodeClassAndPool("scheduled-health-event")
		deployment := interruptionDeployment("scheduled-health-event", 1)
		env.ExpectCreated(nodeClass, nodePool, deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectHealthyPodCount(selector, 1)
		nodeClaim := env.EventuallyExpectCreatedNodeClaimCount("==", 1)[0]

		nodeClaim.Annotations = loAssign(nodeClaim.Annotations, map[string]string{
			"karpenter.alibabacloud.com/scheduled-change": "health-event",
		})
		env.ExpectUpdated(nodeClaim)
		env.ExpectDeleted(nodeClaim)
		Eventually(func(g Gomega) {
			claims := &karpv1.NodeClaimList{}
			g.Expect(env.Client.List(env.Context, claims, &client.ListOptions{})).To(Succeed())
			g.Expect(claims.Items).ToNot(BeEmpty())
		}).WithTimeout(10 * time.Minute).Should(Succeed())
		env.EventuallyExpectHealthyPodCount(selector, 1)
	})
})

func loAssign(base map[string]string, extra map[string]string) map[string]string {
	if base == nil {
		base = map[string]string{}
	}
	for k, v := range extra {
		base[k] = v
	}
	return base
}
