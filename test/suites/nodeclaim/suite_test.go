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
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	environmentcs "github.com/AliyunContainerService/karpenter-provider-alibabacloud/test/pkg/cs"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"

	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var env *environmentcs.Environment
var nodeClass *v1alpha1.ECSNodeClass
var nodePool *karpv1.NodePool

func TestNodeClaim(t *testing.T) {
	RegisterFailHandler(Fail)
	BeforeSuite(func() {
		env = environmentcs.NewEnvironment(t)
	})
	AfterSuite(func() {
		env.Stop()
	})
	RunSpecs(t, "GarbageCollection")
}

var _ = BeforeEach(func() {
	// 删除已有的 ECSNodeClass 和 NodePool 对象，确保测试环境干净
	nodeClassList := &v1alpha1.ECSNodeClassList{}
	if err := env.Client.List(context.Background(), nodeClassList); err == nil {
		for i := range nodeClassList.Items {
			_ = env.Client.Delete(context.Background(), &nodeClassList.Items[i], &client.DeleteOptions{})
		}
	}
	nodePoolList := &karpv1.NodePoolList{}
	if err := env.Client.List(context.Background(), nodePoolList); err == nil {
		for i := range nodePoolList.Items {
			_ = env.Client.Delete(context.Background(), &nodePoolList.Items[i], &client.DeleteOptions{})
		}
	}

	// 给已有节点打上 taint，防止测试 Pod 调度到现有节点
	nodeList := &corev1.NodeList{}
	Expect(env.Client.List(context.Background(), nodeList)).To(Succeed())
	for i := range nodeList.Items {
		node := &nodeList.Items[i]
		// 检查节点是否已有该 taint
		hasTaint := false
		for _, taint := range node.Spec.Taints {
			if taint.Key == "karpenter-test" {
				hasTaint = true
				break
			}
		}
		if !hasTaint {
			node.Spec.Taints = append(node.Spec.Taints, corev1.Taint{
				Key:    "karpenter-test",
				Value:  "true",
				Effect: corev1.TaintEffectNoSchedule,
			})
			Expect(env.Client.Update(context.Background(), node, &client.UpdateOptions{})).To(Succeed())
		}
	}

	env.ValidateCleanEnvironment()
	nodeClass = env.DefaultECSNodeClass()
	nodePool = env.DefaultNodePool(nodeClass)
})
var _ = AfterEach(func() {
	env.CleanupObjects(environmentcs.CleanableObjects...)
	env.AfterEach()
})

var _ = Describe("GarbageCollection", func() {
	It("should succeed to garbage collect an Instance that was deleted without the cluster's knowledge", func() {
		fmt.Println("=== It block is executing ===")
		podOptions := coretest.PodOptions{
			Image: "registry-cn-hangzhou.ack.aliyuncs.com/acs/pause:3.9",
			ResourceRequirements: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
			},
		}
		pod := coretest.Pod(podOptions)
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		node := env.ExpectCreatedNodeCount("==", 1)[0]

		strs := strings.Split(node.Spec.ProviderID, ".")
		_, err := env.ECSAPI.DeleteInstances(context.Background(), &ecs.DeleteInstancesRequest{
			InstanceId: &[]string{strs[1]},
		})
		Expect(err).ToNot(HaveOccurred())
		env.EventuallyExpectNotFound(node)
	})
})
