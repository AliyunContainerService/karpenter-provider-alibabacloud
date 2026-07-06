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
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	environmentcs "github.com/AliyunContainerService/karpenter-provider-alibabacloud/test/pkg/cs"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var env *environmentcs.Environment
var nodeClass *v1alpha1.ECSNodeClass
var nodePool *karpv1.NodePool

func TestIntegration(t *testing.T) {
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
	RunSpecs(t, "Integration")
}

var _ = BeforeEach(func() {
	env.BeforeEach()
	nodeClass = env.DefaultECSNodeClass()
	nodePool = env.DefaultNodePool(nodeClass)
})

var _ = AfterEach(func() {
	env.AfterEach()
})

var _ = Describe("ECSNodeClass Integration", func() {
	It("should provision using explicit VSwitch, security group, and image selectors", Label("nodeclass-selectors"), func() {
		vSwitchIDs := requiredEnvList("TEST_VSWITCH_IDS")
		securityGroupIDs := requiredEnvList("TEST_SECURITY_GROUP_IDS")
		imageID := strings.TrimSpace(os.Getenv("TEST_IMAGE_ID"))
		Expect(imageID).ToNot(BeEmpty(), "TEST_IMAGE_ID must be discovered during ACK setup")

		configureNodeClassAndPool("selectors")
		nodeClass.Spec.VSwitchSelectorTerms = []v1alpha1.VSwitchSelectorTerm{{ID: lo.ToPtr(vSwitchIDs[0])}}
		nodeClass.Spec.SecurityGroupSelectorTerms = []v1alpha1.SecurityGroupSelectorTerm{{ID: lo.ToPtr(securityGroupIDs[0])}}
		nodeClass.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{{ID: lo.ToPtr(imageID)}}

		pod := integrationPod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		nodeClaims := env.EventuallyExpectCreatedNodeClaimCount("==", 1)
		Expect(env.ExpectExists(nodeClaims[0]).(*karpv1.NodeClaim).Status.ImageID).To(Equal(imageID))
	})

	It("should apply ECSNodeClass tags to launched ECS instances", Label("tags"), func() {
		configureNodeClassAndPool("tags")
		nodeClass.Spec.Tags["integration-test-tag"] = "tag-value"

		pod := integrationPod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]
		instance := describeInstanceByProviderID(node.Spec.ProviderID)

		Expect(instanceTags(instance)).To(HaveKeyWithValue("integration-test-tag", "tag-value"))
	})

	It("should launch nodes with kubelet configuration overrides", Label("kubelet-config"), func() {
		configureNodeClassAndPool("kubelet")
		maxPods := int32(20)
		nodeClass.Spec.Kubelet = &v1alpha1.KubeletConfiguration{
			MaxPods: &maxPods,
		}

		pod := integrationPod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]
		nodeClaims := env.EventuallyExpectCreatedNodeClaimCount("==", 1)

		Expect(env.ExpectExists(nodeClaims[0]).(*karpv1.NodeClaim).Status.Capacity.Pods().Value()).To(BeNumerically("<=", int64(maxPods)))
		Expect(node.Status.Capacity.Pods().Value()).To(BeNumerically(">", 0))
	})

	It("should launch nodes with metadata options configured", Label("metadata-options"), func() {
		configureNodeClassAndPool("metadata")
		hopLimit := int32(2)
		nodeClass.Spec.MetadataOptions = &v1alpha1.MetadataOptions{
			HttpTokens:              "optional",
			HttpPutResponseHopLimit: &hopLimit,
		}

		pod := integrationPod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		env.EventuallyExpectCreatedNodeCount("==", 1)
	})
})

func configureNodeClassAndPool(name string) {
	nodeClass.Name = "integration-test-" + name
	nodeClass.Spec.Tags = env.TestTags(name)
	nodePool.Name = "integration-test-pool-" + name
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

func integrationPod() *corev1.Pod {
	return coretest.Pod(coretest.PodOptions{
		Image: "registry-cn-hangzhou.ack.aliyuncs.com/acs/pause:3.9",
		ResourceRequirements: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
		},
		NodeSelector: map[string]string{
			karpv1.NodePoolLabelKey: nodePool.Name,
		},
	})
}

func describeInstanceByProviderID(providerID string) *ecs.DescribeInstancesResponseBodyInstancesInstance {
	parts := strings.Split(providerID, ".")
	Expect(parts).To(HaveLen(2), "expected providerID format <region>.<instance-id>")
	resp, err := env.ECSAPI.DescribeInstances(context.Background(), &ecs.DescribeInstancesRequest{
		RegionId:    tea.String(parts[0]),
		InstanceIds: tea.String("[\"" + parts[1] + "\"]"),
	})
	Expect(err).ToNot(HaveOccurred())
	Expect(resp).ToNot(BeNil())
	Expect(resp.Body).ToNot(BeNil())
	Expect(resp.Body.Instances).ToNot(BeNil())
	Expect(resp.Body.Instances.Instance).To(HaveLen(1))
	return resp.Body.Instances.Instance[0]
}

func instanceTags(instance *ecs.DescribeInstancesResponseBodyInstancesInstance) map[string]string {
	tags := map[string]string{}
	if instance == nil || instance.Tags == nil {
		return tags
	}
	for _, tag := range instance.Tags.Tag {
		if tag == nil || tag.TagKey == nil || tag.TagValue == nil {
			continue
		}
		tags[*tag.TagKey] = *tag.TagValue
	}
	return tags
}

func requiredEnvList(key string) []string {
	values := envList(key)
	Expect(values).ToNot(BeEmpty(), "%s must be discovered during ACK setup", key)
	return values
}

func testInstanceTypes() []string {
	if values := envList("TEST_INSTANCE_TYPES"); len(values) > 0 {
		return values
	}
	return []string{"ecs.c9i.large", "ecs.c9i.xlarge"}
}

func testGPUInstanceTypes() []string {
	return envList("TEST_GPU_INSTANCE_TYPES")
}

func testGPUZones() []string {
	return envList("TEST_GPU_ZONES")
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
