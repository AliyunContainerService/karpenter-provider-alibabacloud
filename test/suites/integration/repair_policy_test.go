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
	"time"

	"github.com/aws/karpenter-provider-aws/test/pkg/environment/common"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	. "github.com/onsi/ginkgo/v2"
	"github.com/samber/lo"
)

var _ = Describe("Repair Policy", func() {
	var selector labels.Selector
	var deployment *appsv1.Deployment

	BeforeEach(func() {
		deployment = coretest.Deployment(coretest.DeploymentOptions{
			Replicas: 1,
			PodOptions: coretest.PodOptions{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "repair"},
					Annotations: map[string]string{
						karpenterv1.DoNotDisruptAnnotationKey: "true",
					},
				},
				TerminationGracePeriodSeconds: lo.ToPtr[int64](0),
			},
		})
		selector = labels.SelectorFromSet(deployment.Spec.Selector.MatchLabels)
	})

	It("should repair a node with stale NodeReady=False condition", Label("repair"), func() {
		configureNodeClassAndPool("repair")

		env.ExpectCreated(nodeClass, nodePool, deployment)
		pod := env.EventuallyExpectHealthyPodCount(selector, 1)[0]
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]
		env.EventuallyExpectInitializedNodeCount("==", 1)

		node = common.ReplaceNodeConditions(node, corev1.NodeCondition{
			Type:               corev1.NodeReady,
			Status:             corev1.ConditionFalse,
			LastTransitionTime: metav1.Time{Time: time.Now().Add(-31 * time.Minute)},
		})
		env.ExpectStatusUpdated(node)

		env.EventuallyExpectNotFound(pod, node)
		env.EventuallyExpectHealthyPodCount(selector, 1)
	})
})
