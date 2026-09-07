//go:build integration

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

package nodeclaim

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"
)

// TagBatching integration test for GitHub issue #14
//
// ECS API limits:
//   - RunInstances Tag parameter: max 20 tags per request
//   - TagResources Tag parameter: max 20 tags per request
//   - Per-instance tag quota: up to 50 tags
//
// When NodeClass.Spec.Tags contains >20 custom tags (combined with Karpenter
// managed tags), the tagging code must split tags into multiple batches and
// apply them via sequential TagResources calls after instance creation.
//
// This test exercises the end-to-end path:
//
// 1. NodeClass specifies 25 custom tags
// 2. Karpenter adds 4 managed tags (managed-by, cluster-id, nodepool, nodeclaim)
//    → total 29 tags
// 3. First 20 tags go into RunInstances request
// 4. Remaining 9 tags are applied post-creation via TagResources
// 5. All 29 tags should be present on the final ECS instance
//
// Without the batching fix (GH issue #14), step 3 or 4 would fail with
// NumberExceed.Tags error from ECS API.

const (
	// Number of custom tags to add (exceeds the 20-tag per-request limit)
	customTagCount = 25
)

// configureManyTags adds customTagCount custom tags to the NodeClass
func configureManyTags() {
	customTags := make(map[string]string)
	for i := 0; i < customTagCount; i++ {
		customTags[fmt.Sprintf("custom-tag-%02d", i)] = fmt.Sprintf("value-%02d", i)
	}
	// Merge with existing ownership tags
	for k, v := range nodeClass.Spec.Tags {
		customTags[k] = v
	}
	nodeClass.Spec.Tags = customTags
}

// configureTagBatchingPool sets up a NodePool for tag batching testing
func configureTagBatchingPool() {
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
				Key:      corev1.LabelArchStable,
				Operator: corev1.NodeSelectorOpIn,
				Values:   []string{v1alpha1.ArchitectureAmd64},
			},
		},
		{
			NodeSelectorRequirement: corev1.NodeSelectorRequirement{
				Key:      corev1.LabelInstanceTypeStable,
				Operator: corev1.NodeSelectorOpIn,
				Values: []string{
					"ecs.g7.large", "ecs.g7.xlarge",
					"ecs.c7.large", "ecs.c7.xlarge",
					"ecs.g6.large", "ecs.g6.xlarge",
				},
			},
		},
	}
}

func tagBatchingTestPod() *corev1.Pod {
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

// getInstanceIDFromProviderID extracts instance ID from provider ID
// Format: cn-hangzhou.i-xxxxx or alibabacloud://cn-hangzhou.i-xxxxx
func getInstanceIDFromProviderID(providerID string) string {
	parts := strings.Split(providerID, ".")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// getRegionFromProviderID extracts region from provider ID
func getRegionFromProviderID(providerID string) string {
	// Remove alibabacloud:// prefix if present
	providerID = strings.TrimPrefix(providerID, "alibabacloud://")
	parts := strings.Split(providerID, ".")
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

// describeInstanceTags queries ECS API for instance tags
func describeInstanceTags(ctx context.Context, instanceID string) (map[string]string, error) {
	request := &ecs.DescribeInstancesRequest{
		RegionId:    lo.ToPtr(env.Region),
		InstanceIds: lo.ToPtr(fmt.Sprintf("[\"%s\"]", instanceID)),
		PageSize:    lo.ToPtr(int32(100)),
	}

	response, err := env.ECSAPI.DescribeInstances(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("DescribeInstances failed: %w", err)
	}

	if response.Body == nil || response.Body.Instances == nil ||
		len(response.Body.Instances.Instance) == 0 {
		return nil, fmt.Errorf("instance %s not found", instanceID)
	}

	inst := response.Body.Instances.Instance[0]
	if inst.Tags == nil || inst.Tags.Tag == nil {
		return map[string]string{}, nil
	}

	tags := make(map[string]string)
	for _, tag := range inst.Tags.Tag {
		if tag.TagKey != nil && tag.TagValue != nil {
			tags[*tag.TagKey] = *tag.TagValue
		}
	}
	return tags, nil
}

var _ = Describe("TagBatching", Label("tag-batching"), func() {
	// Test: >20 tags batching end-to-end
	//
	// This test verifies the fix for GitHub issue #14:
	// ECS RunInstances and TagResources APIs limit tags to 20 per request.
	// When NodeClass.Spec.Tags has >20 tags, the code must split into batches.
	It("should successfully tag instance with >20 tags by splitting into batches", Label("many-tags"), func() {
		configureTagBatchingPool()
		configureManyTags()

		pod := tagBatchingTestPod()

		By("creating NodeClass with 25+ custom tags, NodePool and a pod")
		env.ExpectCreated(nodeClass, nodePool, pod)

		By("waiting for the pod to become healthy on the new node")
		env.EventuallyExpectHealthy(pod)

		node := env.ExpectCreatedNodeCount("==", 1)[0]

		By("extracting instance ID from node ProviderID")
		instanceID := getInstanceIDFromProviderID(node.Spec.ProviderID)
		Expect(instanceID).NotTo(BeEmpty(), "failed to extract instance ID from ProviderID: %s", node.Spec.ProviderID)

		By("querying ECS API for instance tags")
		var instanceTags map[string]string
		Eventually(func(g Gomega) {
			var err error
			instanceTags, err = describeInstanceTags(env.Context, instanceID)
			g.Expect(err).NotTo(HaveOccurred(), "failed to describe instance tags")
			g.Expect(instanceTags).NotTo(BeEmpty(), "instance should have tags")
		}).WithTimeout(2 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

		By("verifying all custom tags are present on the instance")
		for i := 0; i < customTagCount; i++ {
			key := fmt.Sprintf("custom-tag-%02d", i)
			value := fmt.Sprintf("value-%02d", i)
			Expect(instanceTags).To(HaveKeyWithValue(key, value),
				"custom tag %q should be present with value %q, got tags: %v", key, value, instanceTags)
		}

		By("verifying Karpenter managed tags are present")
		Expect(instanceTags).To(HaveKeyWithValue(v1alpha1.TagManagedBy, v1alpha1.TagManagedByValue),
			"managed-by tag should be present")
		Expect(instanceTags).To(HaveKey(v1alpha1.TagNodeClaim),
			"nodeclaim tag should be present")
		Expect(instanceTags).To(HaveKey(v1alpha1.TagNodePool),
			"nodepool tag should be present")

		By("verifying total tag count is at least customTagCount + 3 Karpenter tags")
		// Count tags that match our custom tags or Karpenter tags
		relevantTagCount := 0
		for key := range instanceTags {
			if strings.HasPrefix(key, "custom-tag-") ||
				strings.HasPrefix(key, "karpenter.sh/") ||
				strings.HasPrefix(key, "karpenter.alibabacloud.com/") {
				relevantTagCount++
			}
		}
		Expect(relevantTagCount).To(BeNumerically(">=", customTagCount+3),
			"expected at least %d relevant tags (25 custom + 3 karpenter), got %d: %v",
			customTagCount+3, relevantTagCount, instanceTags)
	})
})
