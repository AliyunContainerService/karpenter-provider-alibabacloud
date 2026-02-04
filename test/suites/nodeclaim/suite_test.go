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
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
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
	env.BeforeEach()
	nodeClass = env.DefaultECSNodeClass()
	nodePool = env.DefaultNodePool(nodeClass)
})
var _ = AfterEach(func() {
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
		// Create delete instance request
		request := &ecs.DeleteInstancesRequest{
			RegionId:   &strs[0],
			InstanceId: []*string{&strs[1]},
		}
		force := true
		request.Force = &force
		terminateSubscription := true
		request.TerminateSubscription = &terminateSubscription
		_, err := env.ECSAPI.DeleteInstances(context.Background(), request)
		Expect(err).ToNot(HaveOccurred())
		env.EventuallyExpectNotFound(node)
	})
})
