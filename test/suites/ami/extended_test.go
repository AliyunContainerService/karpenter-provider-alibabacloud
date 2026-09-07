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

package ami

import (
	"fmt"
	"strings"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	environmentcs "github.com/AliyunContainerService/karpenter-provider-alibabacloud/test/pkg/cs"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Image Resolution Contracts", func() {
	It("should merge custom userdata with the selected image", Label("userdata"), Label("user-data"), func() {
		configureImageNodeClass("userdata")
		nodeClass.Spec.UserData = lo.ToPtr("#!/bin/bash\nset -euxo pipefail\necho karpenter-userdata >/var/log/karpenter-userdata-e2e.log\n")
		pod := imageTestPod()

		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		env.EventuallyExpectCreatedNodeClaimCount("==", 1)
	})

	It("should mark NodeClaims not ready when an explicit image ID is not resolved", Label("not-ready"), Label("not-resolved"), Label("validation"), func() {
		configureImageNodeClass("not-resolved-image-id")
		nodeClass.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{{ID: lo.ToPtr("m-does-not-exist-for-e2e")}}
		pod := imageTestPod()

		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectPendingPodCount(labelsForPod(pod), 1)
		env.ConsistentlyExpectNodeCount("==", 0, time.Minute)
	})

	It("should reject an image family that cannot be resolved", Label("not-ready"), Label("not-resolved"), Label("validation"), func() {
		configureImageNodeClass("not-resolved-image-family")
		nodeClass.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{{ImageFamily: lo.ToPtr("acs:no-such-family")}}
		pod := imageTestPod()

		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectPendingPodCount(labelsForPod(pod), 1)
		env.ConsistentlyExpectNodeCount("==", 0, time.Minute)
	})

	It("should use the default AlibabaCloud Linux image family", Label("image-family"), func() {
		configureImageNodeClass("default-family")
		nodeClass.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{{ImageFamily: lo.ToPtr(environmentcs.DefaultImageFamily)}}
		pod := imageTestPod()

		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		env.EventuallyExpectCreatedNodeClaimCount("==", 1)
	})

	It("should resolve image status before launch", Label("status-image"), func() {
		configureImageNodeClass("status-image")
		env.ExpectCreated(nodeClass, nodePool)
		Eventually(func(g Gomega) {
			current := env.ExpectExists(nodeClass).(*v1alpha1.ECSNodeClass)
			g.Expect(current.Status.Images).ToNot(BeEmpty())
		}).WithTimeout(5 * time.Minute).Should(Succeed())
	})

	It("should keep image status stable across repeated resolutions", Label("status-image"), func() {
		configureImageNodeClass("status-stable")
		env.ExpectCreated(nodeClass, nodePool)
		var imageID string
		Eventually(func(g Gomega) {
			current := env.ExpectExists(nodeClass).(*v1alpha1.ECSNodeClass)
			g.Expect(current.Status.Images).ToNot(BeEmpty())
			imageID = current.Status.Images[0].ID
		}).WithTimeout(5 * time.Minute).Should(Succeed())
		Consistently(func(g Gomega) {
			current := env.ExpectExists(nodeClass).(*v1alpha1.ECSNodeClass)
			g.Expect(current.Status.Images).ToNot(BeEmpty())
			g.Expect(current.Status.Images[0].ID).To(Equal(imageID))
		}).WithTimeout(time.Minute).Should(Succeed())
	})

	It("should launch with an image selected by image family after NodePool creation", Label("image-family"), func() {
		configureImageNodeClass("late-nodepool")
		pod := imageTestPod()
		env.ExpectCreated(nodeClass, pod)
		env.EventuallyExpectPendingPodCount(labelsForPod(pod), 1)
		env.ExpectCreated(nodePool)
		env.EventuallyExpectHealthy(pod)
	})

	It("should launch multiple pods using the same resolved image", Label("status-image"), func() {
		configureImageNodeClass("multi-pod")
		podA := imageTestPod()
		podA.Name = "ami-test-multi-pod-a"
		podB := imageTestPod()
		podB.Name = "ami-test-multi-pod-b"
		env.ExpectCreated(nodeClass, nodePool, podA, podB)
		env.EventuallyExpectHealthy(podA)
		env.EventuallyExpectHealthy(podB)
		env.EventuallyExpectCreatedNodeClaimCount(">=", 1)
	})

	It("should preserve custom user data on image family launches", Label("userdata"), Label("user-data"), func() {
		configureImageNodeClass("userdata-family")
		nodeClass.Spec.UserData = lo.ToPtr("#!/bin/bash\nset -euxo pipefail\necho image-family-userdata >/var/log/karpenter-image-family-userdata.log\n")
		pod := imageTestPod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
	})

	It("should reject malformed image IDs during validation", Label("validation"), func() {
		nodeClass.Name = "ami-test-malformed-image-id"
		configureNodePool("malformed-image-id")
		nodeClass.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{{ID: lo.ToPtr("not-an-aliyun-image-id")}}
		pod := imageTestPod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectPendingPodCount(labelsForPod(pod), 1)
		env.ConsistentlyExpectNodeCount("==", 0, time.Minute)
	})

	It("should not resolve an empty image selector set", Label("not-ready"), Label("validation"), func() {
		nodeClass.Name = "ami-test-empty-selector"
		configureNodePool("empty-selector")
		nodeClass.Spec.ImageSelectorTerms = nil
		Expect(env.Client.Create(env.Context, nodeClass)).To(MatchError(ContainSubstring("spec.imageSelectorTerms")))
		env.ConsistentlyExpectNodeCount("==", 0, time.Minute)
	})

	It("should support explicit image family aliases", Label("image-family"), Label("alias"), func() {
		configureImageNodeClass("family-alias")
		imageFamily := strings.TrimSpace(environmentcs.DefaultImageFamily)
		nodeClass.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{{ImageFamily: lo.ToPtr(imageFamily)}}
		pod := imageTestPod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
	})

	It("should keep NodeClaims not ready when the NodeClass image is not ready", Label("not-ready"), Label("not-resolved"), func() {
		configureImageNodeClass("nodeclass-image-not-ready")
		nodeClass.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{{ImageFamily: lo.ToPtr("acs:not-ready")}}
		pod := imageTestPod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectPendingPodCount(labelsForPod(pod), 1)
		env.ExpectNodeClaimCount("==", 0)
	})

	It("should update status image when selector changes", Label("status-image"), func() {
		configureImageNodeClass("status-selector-change")
		env.ExpectCreated(nodeClass, nodePool)
		Eventually(func(g Gomega) {
			current := env.ExpectExists(nodeClass).(*v1alpha1.ECSNodeClass)
			g.Expect(current.Status.Images).ToNot(BeEmpty())
		}).WithTimeout(5 * time.Minute).Should(Succeed())
		nodeClass.Spec.Tags["image-selector-reconciled"] = "true"
		env.ExpectUpdated(nodeClass)
		Eventually(func(g Gomega) {
			current := env.ExpectExists(nodeClass).(*v1alpha1.ECSNodeClass)
			g.Expect(current.Status.Images).ToNot(BeEmpty())
		}).WithTimeout(5 * time.Minute).Should(Succeed())
	})

	It("should launch from most recent image family resolution", Label("most-recent"), Label("image-family"), func() {
		configureImageNodeClass("most-recent")
		pod := imageTestPod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		nodeClaims := env.EventuallyExpectCreatedNodeClaimCount("==", 1)
		Expect(nodeClaims[0].Status.ImageID).ToNot(BeEmpty())
	})
})

func configureImageNodeClass(name string) {
	// Add parallel process ID to avoid resource name conflicts in parallel tests
	procID := fmt.Sprintf("-p%d", GinkgoParallelProcess())
	nodeClass.Name = "ami-test-" + name + procID
	nodeClass.Spec.Tags = env.TestTags(name)
	nodeClass.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{{ImageFamily: lo.ToPtr(environmentcs.DefaultImageFamily)}}
	configureNodePool(name)
}

func labelsForPod(pod *corev1.Pod) labels.Selector {
	return labels.SelectorFromSet(pod.Labels)
}
