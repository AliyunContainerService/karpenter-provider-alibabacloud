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
	"os"
	"strings"
	"testing"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	environmentcs "github.com/AliyunContainerService/karpenter-provider-alibabacloud/test/pkg/cs"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var env *environmentcs.Environment
var nodeClass *v1alpha1.ECSNodeClass
var nodePool *v1.NodePool

func TestAMI(t *testing.T) {
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
	RunSpecs(t, "AMI")
}

var _ = BeforeEach(func() {
	env.BeforeEach()
	nodeClass = env.DefaultECSNodeClass()
	nodePool = env.DefaultNodePool(nodeClass)
})

var _ = AfterEach(func() {
	env.AfterEach()
})

var _ = Describe("Image Selection", func() {
	It("should use the ECS image selected by ID", Label("image-id"), func() {
		imageID := strings.TrimSpace(os.Getenv("TEST_IMAGE_ID"))
		Expect(imageID).ToNot(BeEmpty(), "TEST_IMAGE_ID must be discovered during ACK setup for the image-id suite")

		nodeClass.Name = "ami-test-image-id"
		configureNodePool("image-id")
		nodeClass.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{{ID: lo.ToPtr(imageID)}}
		nodeClass.Spec.Tags = env.TestTags("image-id")

		pod := imageTestPod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		nodeClaims := env.EventuallyExpectCreatedNodeClaimCount("==", 1)

		Eventually(func(g Gomega) {
			nodeClaim := env.ExpectExists(nodeClaims[0]).(*v1.NodeClaim)
			g.Expect(nodeClaim.Status.ImageID).To(Equal(imageID))
		}).WithTimeout(2 * time.Minute).Should(Succeed())
	})

	It("should resolve an ECS image family and launch a node", Label("image-family"), func() {
		imageFamily := strings.TrimSpace(os.Getenv("TEST_IMAGE_FAMILY"))
		if imageFamily == "" {
			imageFamily = environmentcs.DefaultImageFamily
		}

		nodeClass.Name = "ami-test-image-family"
		configureNodePool("image-family")
		nodeClass.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{{ImageFamily: lo.ToPtr(imageFamily)}}
		nodeClass.Spec.Tags = env.TestTags("image-family")

		pod := imageTestPod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		nodeClaims := env.EventuallyExpectCreatedNodeClaimCount("==", 1)

		Eventually(func(g Gomega) {
			nodeClaim := env.ExpectExists(nodeClaims[0]).(*v1.NodeClaim)
			g.Expect(nodeClaim.Status.ImageID).ToNot(BeEmpty())
		}).WithTimeout(2 * time.Minute).Should(Succeed())
	})
})

func configureNodePool(name string) {
	nodePool.Name = "ami-test-pool-" + name
	nodePool.Spec.Template.Spec.NodeClassRef = &v1.NodeClassReference{
		Group: "karpenter.alibabacloud.com",
		Kind:  "ECSNodeClass",
		Name:  nodeClass.Name,
	}
	nodePool.Spec.Template.Spec.Requirements = []v1.NodeSelectorRequirementWithMinValues{
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

func imageTestPod() *corev1.Pod {
	return coretest.Pod(coretest.PodOptions{
		Image: "registry-cn-hangzhou.ack.aliyuncs.com/acs/pause:3.9",
		ResourceRequirements: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
		},
		NodeSelector: map[string]string{
			v1.NodePoolLabelKey: nodePool.Name,
		},
	})
}

func testInstanceTypes() []string {
	if values := envList("TEST_INSTANCE_TYPES"); len(values) > 0 {
		return values
	}
	return []string{"ecs.c9i.large", "ecs.c9i.xlarge"}
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
