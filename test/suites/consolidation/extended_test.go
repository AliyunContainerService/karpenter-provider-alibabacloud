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
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Consolidation Policies", func() {
	It("should respect a zero-node disruption budget", Label("budget"), func() {
		configureNodeClass("budget", "budget")
		configureNodePool("budget")
		nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmptyOrUnderutilized
		nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")
		nodePool.Spec.Disruption.Budgets = []karpv1.Budget{{Nodes: "0"}}

		deployment := consolidationDeployment("budget", 2, true)
		env.ExpectCreated(nodePool, nodeClass, deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectHealthyPodCount(selector, 2)
		env.ExpectDeleted(deployment)
		env.ConsistentlyExpectNodeCount(">=", 2, 2*time.Minute)
	})

	It("should allow empty-node consolidation when budget permits one disruption", Label("budget"), func() {
		configureNodeClass("budget-one", "budget-one")
		configureNodePool("budget-one")
		nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmpty
		nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")
		nodePool.Spec.Disruption.Budgets = []karpv1.Budget{{Nodes: "1"}}

		deployment := consolidationDeployment("budget-one", 1, false)
		env.ExpectCreated(nodePool, nodeClass, deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectHealthyPodCount(selector, 1)
		env.ExpectDeleted(deployment)
		Eventually(func(g Gomega) {
			g.Expect(env.Monitor.CreatedNodes()).To(HaveLen(0))
		}).WithTimeout(5 * time.Minute).Should(Succeed())
	})

	It("should replace a node when a cheaper compatible replacement exists", Label("replace"), func() {
		configureNodeClass("replace", "replace")
		configureNodePool("replace")
		nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmptyOrUnderutilized
		nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")

		deployment := consolidationDeployment("replace", 2, true)
		env.ExpectCreated(nodePool, nodeClass, deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectHealthyPodCount(selector, 2)
		initial := env.Monitor.CreatedNodes()
		Expect(initial).ToNot(BeEmpty())
		deployment.Spec.Replicas = lo.ToPtr(int32(1))
		env.ExpectUpdated(deployment)
		env.EventuallyExpectHealthyPodCount(selector, 1)
		Eventually(func(g Gomega) {
			g.Expect(len(env.Monitor.CreatedNodes())).To(BeNumerically("<=", 1))
		}).WithTimeout(10 * time.Minute).Should(Succeed())
	})

	It("should avoid replacement when consolidation is disabled by policy", Label("replace"), func() {
		configureNodeClass("replace-disabled", "replace-disabled")
		configureNodePool("replace-disabled")
		nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmpty
		nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("Never")

		deployment := consolidationDeployment("replace-disabled", 1, false)
		env.ExpectCreated(nodePool, nodeClass, deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectHealthyPodCount(selector, 1)
		env.ConsistentlyExpectNodeCount(">=", 1, 2*time.Minute)
	})

	It("should consolidate spot nodes when they become empty", Label("spot"), func() {
		configureNodeClass("spot-empty", "spot-empty")
		configureNodePool("spot-empty")
		spotStrategy := "SpotAsPriceGo"
		nodeClass.Spec.SpotStrategy = &spotStrategy
		nodePool.Spec.Template.Spec.Requirements[0].Values = []string{v1alpha1.CapacityTypeSpot}
		nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmpty
		nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")

		deployment := consolidationDeployment("spot-empty", 1, false)
		env.ExpectCreated(nodePool, nodeClass, deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectHealthyPodCount(selector, 1)
		env.ExpectDeleted(deployment)
		Eventually(func(g Gomega) {
			g.Expect(env.Monitor.CreatedNodes()).To(HaveLen(0))
		}).WithTimeout(5 * time.Minute).Should(Succeed())
	})

	It("should keep spot workloads healthy during underutilized consolidation", Label("spot"), func() {
		configureNodeClass("spot-underutilized", "spot-underutilized")
		configureNodePool("spot-underutilized")
		spotStrategy := "SpotAsPriceGo"
		nodeClass.Spec.SpotStrategy = &spotStrategy
		nodePool.Spec.Template.Spec.Requirements[0].Values = []string{v1alpha1.CapacityTypeSpot}
		nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmptyOrUnderutilized
		nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")

		deployment := consolidationDeployment("spot-underutilized", 2, true)
		env.ExpectCreated(nodePool, nodeClass, deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectHealthyPodCount(selector, 2)
		deployment.Spec.Replicas = lo.ToPtr(int32(1))
		env.ExpectUpdated(deployment)
		env.EventuallyExpectHealthyPodCount(selector, 1)
	})

	It("should exercise reserved capacity consolidation preconditions", Label("reserved"), Label("reservation"), Label("capacity-reservation"), func() {
		capacityReservationID := os.Getenv("TEST_CAPACITY_RESERVATION_ID")
		if capacityReservationID == "" {
			Skip("capacity reservation consolidation requires TEST_CAPACITY_RESERVATION_ID")
		}
		configureNodeClass("capacity-reservation", "capacity-reservation")
		configureNodePool("capacity-reservation")
		restrictNodePoolInstanceTypes(capacityReservationInstanceTypes()...)
		preference := "target"
		nodeClass.Spec.CapacityReservationPreference = &preference
		nodeClass.Spec.CapacityReservationSelectorTerms = []v1alpha1.CapacityReservationSelectorTerm{{ID: &capacityReservationID}}
		nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmpty
		nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")

		deployment := consolidationDeployment("capacity-reservation", 1, false)
		env.ExpectCreated(nodePool, nodeClass, deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectHealthyPodCount(selector, 1)
		env.ExpectDeleted(deployment)
		Eventually(func(g Gomega) {
			g.Expect(env.Monitor.CreatedNodes()).To(HaveLen(0))
		}).WithTimeout(5 * time.Minute).Should(Succeed())
	})

	It("should not consolidate nodes protected by do-not-disrupt annotations", Label("budget"), func() {
		configureNodeClass("do-not-disrupt", "do-not-disrupt")
		configureNodePool("do-not-disrupt")
		nodePool.Spec.Template.Annotations = map[string]string{karpv1.DoNotDisruptAnnotationKey: "true"}
		nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmptyOrUnderutilized
		nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")

		deployment := consolidationDeployment("do-not-disrupt", 1, false)
		env.ExpectCreated(nodePool, nodeClass, deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectHealthyPodCount(selector, 1)
		env.ConsistentlyExpectNodeCount(">=", 1, 2*time.Minute)
	})

	It("should honor scheduled disruption budgets", Label("budget"), func() {
		configureNodeClass("scheduled-budget", "scheduled-budget")
		configureNodePool("scheduled-budget")
		schedule := "@hourly"
		duration := metav1.Duration{Duration: time.Hour}
		nodePool.Spec.Disruption.Budgets = []karpv1.Budget{{Nodes: "0", Schedule: &schedule, Duration: &duration}}
		nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmpty

		deployment := consolidationDeployment("scheduled-budget", 1, false)
		env.ExpectCreated(nodePool, nodeClass, deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectHealthyPodCount(selector, 1)
	})

	It("should consolidate only empty nodes with WhenEmpty policy", Label("replace"), func() {
		configureNodeClass("when-empty-only", "when-empty-only")
		configureNodePool("when-empty-only")
		nodePool.Spec.Disruption.ConsolidationPolicy = karpv1.ConsolidationPolicyWhenEmpty
		nodePool.Spec.Disruption.ConsolidateAfter = karpv1.MustParseNillableDuration("0s")

		deployment := consolidationDeployment("when-empty-only", 2, true)
		env.ExpectCreated(nodePool, nodeClass, deployment)
		selector := labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
		env.EventuallyExpectHealthyPodCount(selector, 2)
		deployment.Spec.Replicas = lo.ToPtr(int32(1))
		env.ExpectUpdated(deployment)
		env.EventuallyExpectHealthyPodCount(selector, 1)
	})
})
