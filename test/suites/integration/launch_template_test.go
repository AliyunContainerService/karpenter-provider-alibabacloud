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
	"fmt"
	"os"
	"strings"
	"time"

	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/samber/lo"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Launch Template", func() {
	It("should provision nodes with an ECS launch template", Label("launch-template"), func() {
		vSwitchIDs := requiredEnvList("TEST_VSWITCH_IDS")
		securityGroupIDs := requiredEnvList("TEST_SECURITY_GROUP_IDS")
		imageID := strings.TrimSpace(os.Getenv("TEST_IMAGE_ID"))
		Expect(imageID).ToNot(BeEmpty(), "TEST_IMAGE_ID must be discovered during ACK setup")

		launchTemplateID, launchTemplateVersion := createLaunchTemplate(imageID, testInstanceTypes()[0], vSwitchIDs[0], securityGroupIDs[0])
		defer deleteLaunchTemplate(launchTemplateID)

		configureNodeClassAndPool("launch-template")
		nodeClass.Spec.LaunchTemplateID = lo.ToPtr(launchTemplateID)
		nodeClass.Spec.LaunchTemplateVersion = lo.ToPtr(launchTemplateVersion)
		nodeClass.Spec.VSwitchSelectorTerms = nil
		nodeClass.Spec.SecurityGroupSelectorTerms = nil
		nodeClass.Spec.ImageSelectorTerms = nil
		nodeClass.Spec.SystemDisk = nil
		nodeClass.Spec.DataDisks = nil

		pod := integrationPod()
		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]
		instance := describeInstanceByProviderID(node.Spec.ProviderID)

		Expect(instance.ImageId).ToNot(BeNil())
		Expect(*instance.ImageId).To(Equal(imageID))
	})
})

func createLaunchTemplate(imageID, instanceType, vSwitchID, securityGroupID string) (string, int64) {
	name := fmt.Sprintf("karpenter-e2e-%d", time.Now().UnixNano())
	resp, err := env.ECSAPI.CreateLaunchTemplate(context.Background(), &ecs.CreateLaunchTemplateRequest{
		RegionId:           tea.String(env.Region),
		LaunchTemplateName: tea.String(name),
		ImageId:            tea.String(imageID),
		InstanceType:       tea.String(instanceType),
		VSwitchId:          tea.String(vSwitchID),
		SecurityGroupId:    tea.String(securityGroupID),
		SystemDisk: &ecs.CreateLaunchTemplateRequestSystemDisk{
			Category: tea.String("cloud_essd"),
			Size:     tea.Int32(40),
		},
		TemplateTag: []*ecs.CreateLaunchTemplateRequestTemplateTag{
			{Key: tea.String("testing/type"), Value: tea.String("e2e")},
			{Key: tea.String("testing/cluster"), Value: tea.String(env.ClusterName)},
		},
	})
	Expect(err).ToNot(HaveOccurred())
	Expect(resp).ToNot(BeNil())
	Expect(resp.Body).ToNot(BeNil())
	Expect(resp.Body.LaunchTemplateId).ToNot(BeNil())
	Expect(resp.Body.LaunchTemplateVersionNumber).ToNot(BeNil())
	return *resp.Body.LaunchTemplateId, *resp.Body.LaunchTemplateVersionNumber
}

func deleteLaunchTemplate(launchTemplateID string) {
	_, err := env.ECSAPI.DeleteLaunchTemplate(context.Background(), &ecs.DeleteLaunchTemplateRequest{
		RegionId:         tea.String(env.Region),
		LaunchTemplateId: tea.String(launchTemplateID),
	})
	Expect(err).ToNot(HaveOccurred())
}
