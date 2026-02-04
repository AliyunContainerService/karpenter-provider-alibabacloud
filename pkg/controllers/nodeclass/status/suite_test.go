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

package status_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	ram "github.com/alibabacloud-go/ram-20150501/v2/client"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/controllers/nodeclass/status"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/imagefamily"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/ramrole"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/securitygroup"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/vswitch"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	vpc "github.com/alibabacloud-go/vpc-20160428/v7/client"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	coretest "sigs.k8s.io/karpenter/pkg/test"
)

var (
	ctx                   context.Context
	env                   *coretest.Environment
	statusController      *status.Controller
	testEnv               *envtest.Environment
	cfg                   *rest.Config
	mockECSClient         *MockECSClient
	mockVPCClient         *MockVPCClient
	mockRAMClient         *MockRAMClient
	vswitchProvider       *vswitch.Provider
	securityGroupProvider *securitygroup.Provider
	imageFamilyProvider   *imagefamily.Provider
	ramProvider           *ramrole.Provider
)

func TestStatus(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Status Controller Suite")
}

var _ = BeforeSuite(func() {
	ctx = context.Background()

	// Setup envtest
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "..", "charts", "karpenter", "crds")},
		ErrorIfCRDPathMissing: false,
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	// Register schemes
	err = v1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	// Create client
	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	// Setup mock clients
	mockECSClient = new(MockECSClient)
	mockVPCClient = new(MockVPCClient)
	mockRAMClient = new(MockRAMClient)

	// Create providers with mock clients
	vswitchProvider = vswitch.NewProvider("cn-hangzhou", mockVPCClient)
	securityGroupProvider = securitygroup.NewProvider("cn-hangzhou", mockECSClient)
	imageFamilyProvider = imagefamily.NewProvider(mockECSClient)
	ramProvider = ramrole.NewProvider(mockRAMClient, "cn-hangzhou")

	// Create status controller
	statusController = status.NewController(
		k8sClient,
		vswitchProvider,
		securityGroupProvider,
		imageFamilyProvider,
		ramProvider,
	)

	// Use a simple environment for compatibility
	env = &coretest.Environment{
		Client: k8sClient,
	}
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

var _ = Describe("StatusController", func() {
	var nodeClass *v1alpha1.ECSNodeClass

	BeforeEach(func() {
		// Reset mock expectations
		mockECSClient = new(MockECSClient)
		mockVPCClient = new(MockVPCClient)
		mockRAMClient = new(MockRAMClient)

		// Recreate providers
		vswitchProvider = vswitch.NewProvider("cn-hangzhou", mockVPCClient)
		securityGroupProvider = securitygroup.NewProvider("cn-hangzhou", mockECSClient)
		imageFamilyProvider = imagefamily.NewProvider(mockECSClient)
		ramProvider = ramrole.NewProvider(mockRAMClient, "cn-hangzhou")

		// Recreate controller
		statusController = status.NewController(
			env.Client,
			vswitchProvider,
			securityGroupProvider,
			imageFamilyProvider,
			ramProvider,
		)

		// Create ECSNodeClass
		nodeClass = &v1alpha1.ECSNodeClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-nodeclass",
			},
			Spec: v1alpha1.ECSNodeClassSpec{
				VSwitchSelectorTerms: []v1alpha1.VSwitchSelectorTerm{
					{
						ID: stringPtr("vsw-test-123"),
					},
				},
				SecurityGroupSelectorTerms: []v1alpha1.SecurityGroupSelectorTerm{
					{
						ID: stringPtr("sg-test-123"),
					},
				},
				ImageSelectorTerms: []v1alpha1.ImageSelectorTerm{
					{
						ID: stringPtr("aliyun_3_x64_20G_alibase_20231221.vhd"),
					},
				},
				Role: stringPtr("KarpenterNodeRole"),
			},
		}
	})

	AfterEach(func() {
		// Clean up all resources
		Expect(env.Client.DeleteAllOf(ctx, &v1alpha1.ECSNodeClass{})).To(Succeed())
	})

	Context("VSwitch Resolution", func() {
		It("should resolve VSwitches successfully and set status", func() {
			// Setup mock VPC client
			vswitchId := "vsw-test-123"
			zoneId := "cn-hangzhou-h"
			availableIps := int64(100)
			mockVPCClient.On("DescribeVSwitches", mock.Anything, "vsw-test-123", mock.Anything).Return(&vpc.DescribeVSwitchesResponse{
				Body: &vpc.DescribeVSwitchesResponseBody{
					VSwitches: &vpc.DescribeVSwitchesResponseBodyVSwitches{
						VSwitch: []*vpc.DescribeVSwitchesResponseBodyVSwitchesVSwitch{
							{
								VSwitchId:               &vswitchId,
								ZoneId:                  &zoneId,
								AvailableIpAddressCount: &availableIps,
							},
						},
					},
				},
			}, nil)

			// Setup other mocks
			sgId := "sg-test-123"
			sgName := "test-sg"
			mockECSClient.On("DescribeSecurityGroups", mock.Anything, mock.Anything).Return(&ecs.DescribeSecurityGroupsResponse{
				Body: &ecs.DescribeSecurityGroupsResponseBody{
					SecurityGroups: &ecs.DescribeSecurityGroupsResponseBodySecurityGroups{
						SecurityGroup: []*ecs.DescribeSecurityGroupsResponseBodySecurityGroupsSecurityGroup{
							{SecurityGroupId: &sgId, SecurityGroupName: &sgName},
						},
					},
				},
			}, nil)

			imageId := "aliyun_3_x64_20G_alibase_20231221.vhd"
			imageName := "Alibaba Cloud Linux 3"
			arch := "x86_64"
			mockECSClient.On("DescribeImages", mock.Anything, []string{"aliyun_3_x64_20G_alibase_20231221.vhd"}, mock.Anything).Return([]ecs.DescribeImagesResponseBodyImagesImage{
				{ImageId: &imageId, ImageName: &imageName, Architecture: &arch},
			}, nil)

			roleName := "KarpenterNodeRole"
			assumeRolePolicy := `{"Statement":[{"Effect":"Allow","Principal":{"Service":["ecs.aliyuncs.com"]},"Action":"sts:AssumeRole"}]}`
			mockRAMClient.On("GetRole", mock.Anything, "KarpenterNodeRole").Return(&ram.GetRoleResponse{
				Body: &ram.GetRoleResponseBody{
					Role: &ram.GetRoleResponseBodyRole{
						RoleName:                 &roleName,
						AssumeRolePolicyDocument: &assumeRolePolicy,
					},
				},
			}, nil)

			// Create and reconcile
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			_, err := statusController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClass),
			})
			Expect(err).ToNot(HaveOccurred())

			// Verify status
			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())

			Expect(updated.Status.VSwitches).To(HaveLen(1))
			Expect(updated.Status.VSwitches[0].ID).To(Equal("vsw-test-123"))
			Expect(updated.Status.VSwitches[0].Zone).To(Equal("cn-hangzhou-h"))

			// Verify condition
			condition := getCondition(updated, v1alpha1.ConditionTypeVSwitchResolved)
			Expect(condition).ToNot(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionTrue))
			Expect(condition.Reason).To(Equal("VSwitchResolved"))
		})

		It("should set error condition when VSwitch resolution fails", func() {
			// Setup mock to return error
			mockVPCClient.On("DescribeVSwitches", mock.Anything, "vsw-test-123", mock.Anything).Return(nil, fmt.Errorf("VPC API error"))

			sgId := "sg-test-123"
			mockECSClient.On("DescribeSecurityGroups", mock.Anything, mock.Anything).Return(&ecs.DescribeSecurityGroupsResponse{
				Body: &ecs.DescribeSecurityGroupsResponseBody{
					SecurityGroups: &ecs.DescribeSecurityGroupsResponseBodySecurityGroups{
						SecurityGroup: []*ecs.DescribeSecurityGroupsResponseBodySecurityGroupsSecurityGroup{{SecurityGroupId: &sgId}},
					},
				},
			}, nil)

			imageId := "aliyun_3_x64_20G_alibase_20231221.vhd"
			mockECSClient.On("DescribeImages", mock.Anything, mock.Anything, mock.Anything).Return([]ecs.DescribeImagesResponseBodyImagesImage{
				{ImageId: &imageId},
			}, nil)

			roleName := "KarpenterNodeRole"
			assumeRolePolicy := `{"Statement":[{"Effect":"Allow","Principal":{"Service":["ecs.aliyuncs.com"]},"Action":"sts:AssumeRole"}]}`
			mockRAMClient.On("GetRole", mock.Anything, "KarpenterNodeRole").Return(&ram.GetRoleResponse{
				Body: &ram.GetRoleResponseBody{
					Role: &ram.GetRoleResponseBodyRole{
						RoleName:                 &roleName,
						AssumeRolePolicyDocument: &assumeRolePolicy,
					},
				},
			}, nil)

			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			_, err := statusController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClass),
			})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())

			// Verify error condition
			condition := getCondition(updated, v1alpha1.ConditionTypeVSwitchResolved)
			Expect(condition).ToNot(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionFalse))
			Expect(condition.Reason).To(Equal("VSwitchResolutionFailed"))

			// Ready should be false
			readyCondition := getCondition(updated, v1alpha1.ConditionTypeReady)
			Expect(readyCondition).ToNot(BeNil())
			Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
		})

		It("should resolve multiple VSwitches", func() {
			nodeClass.Spec.VSwitchSelectorTerms = []v1alpha1.VSwitchSelectorTerm{
				{Tags: map[string]string{"env": "prod"}},
			}

			vsw1 := "vsw-1"
			vsw2 := "vsw-2"
			vsw3 := "vsw-3"
			zoneH := "cn-hangzhou-h"
			zoneI := "cn-hangzhou-i"
			zoneJ := "cn-hangzhou-j"
			mockVPCClient.On("DescribeVSwitches", mock.Anything, "", map[string]string{"env": "prod"}).Return(&vpc.DescribeVSwitchesResponse{
				Body: &vpc.DescribeVSwitchesResponseBody{
					VSwitches: &vpc.DescribeVSwitchesResponseBodyVSwitches{
						VSwitch: []*vpc.DescribeVSwitchesResponseBodyVSwitchesVSwitch{
							{VSwitchId: &vsw1, ZoneId: &zoneH},
							{VSwitchId: &vsw2, ZoneId: &zoneI},
							{VSwitchId: &vsw3, ZoneId: &zoneJ},
						},
					},
				},
			}, nil)

			sgId := "sg-test-123"
			mockECSClient.On("DescribeSecurityGroups", mock.Anything, mock.Anything).Return(&ecs.DescribeSecurityGroupsResponse{
				Body: &ecs.DescribeSecurityGroupsResponseBody{
					SecurityGroups: &ecs.DescribeSecurityGroupsResponseBodySecurityGroups{
						SecurityGroup: []*ecs.DescribeSecurityGroupsResponseBodySecurityGroupsSecurityGroup{{SecurityGroupId: &sgId}},
					},
				},
			}, nil)

			imageId := "aliyun_3_x64_20G_alibase_20231221.vhd"
			mockECSClient.On("DescribeImages", mock.Anything, mock.Anything, mock.Anything).Return([]ecs.DescribeImagesResponseBodyImagesImage{
				{ImageId: &imageId},
			}, nil)

			roleName := "KarpenterNodeRole"
			assumeRolePolicy := `{"Statement":[{"Effect":"Allow","Principal":{"Service":["ecs.aliyuncs.com"]},"Action":"sts:AssumeRole"}]}`
			mockRAMClient.On("GetRole", mock.Anything, "KarpenterNodeRole").Return(&ram.GetRoleResponse{
				Body: &ram.GetRoleResponseBody{
					Role: &ram.GetRoleResponseBodyRole{
						RoleName:                 &roleName,
						AssumeRolePolicyDocument: &assumeRolePolicy,
					},
				},
			}, nil)

			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			_, err := statusController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClass),
			})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())

			Expect(updated.Status.VSwitches).To(HaveLen(3))
			condition := getCondition(updated, v1alpha1.ConditionTypeVSwitchResolved)
			Expect(condition.Message).To(ContainSubstring("Resolved 3 VSwitches"))
		})
	})

	Context("SecurityGroup Resolution", func() {
		It("should resolve SecurityGroups successfully and set status", func() {
			// 使用 Tags 选择器来测试 API 调用和 Name 字段
			nodeClass.Spec.SecurityGroupSelectorTerms = []v1alpha1.SecurityGroupSelectorTerm{
				{Tags: map[string]string{"test": "true"}},
			}

			vswitchId := "vsw-test-123"
			zoneId := "cn-hangzhou-h"
			mockVPCClient.On("DescribeVSwitches", mock.Anything, "vsw-test-123", mock.Anything).Return(&vpc.DescribeVSwitchesResponse{
				Body: &vpc.DescribeVSwitchesResponseBody{
					VSwitches: &vpc.DescribeVSwitchesResponseBodyVSwitches{
						VSwitch: []*vpc.DescribeVSwitchesResponseBodyVSwitchesVSwitch{{VSwitchId: &vswitchId, ZoneId: &zoneId}},
					},
				},
			}, nil)

			sgId := "sg-test-123"
			sgName := "test-security-group"
			mockECSClient.On("DescribeSecurityGroups", mock.Anything, map[string]string{"test": "true"}).Return(&ecs.DescribeSecurityGroupsResponse{
				Body: &ecs.DescribeSecurityGroupsResponseBody{
					SecurityGroups: &ecs.DescribeSecurityGroupsResponseBodySecurityGroups{
						SecurityGroup: []*ecs.DescribeSecurityGroupsResponseBodySecurityGroupsSecurityGroup{
							{
								SecurityGroupId:   &sgId,
								SecurityGroupName: &sgName,
							},
						},
					},
				},
			}, nil)

			imageId := "aliyun_3_x64_20G_alibase_20231221.vhd"
			mockECSClient.On("DescribeImages", mock.Anything, mock.Anything, mock.Anything).Return([]ecs.DescribeImagesResponseBodyImagesImage{
				{ImageId: &imageId},
			}, nil)

			roleName := "KarpenterNodeRole"
			assumeRolePolicy := `{"Statement":[{"Effect":"Allow","Principal":{"Service":["ecs.aliyuncs.com"]},"Action":"sts:AssumeRole"}]}`
			mockRAMClient.On("GetRole", mock.Anything, "KarpenterNodeRole").Return(&ram.GetRoleResponse{
				Body: &ram.GetRoleResponseBody{
					Role: &ram.GetRoleResponseBodyRole{
						RoleName:                 &roleName,
						AssumeRolePolicyDocument: &assumeRolePolicy,
					},
				},
			}, nil)

			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			_, err := statusController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClass),
			})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())

			Expect(updated.Status.SecurityGroups).To(HaveLen(1))
			Expect(updated.Status.SecurityGroups[0].ID).To(Equal("sg-test-123"))
			// 注意: 通过 Tags 获取时，Provider 只设置 ID，不设置 Name
			// 这是因为 getByTags 方法只返回 ID (securitygroup.go:134-136)

			condition := getCondition(updated, v1alpha1.ConditionTypeSecurityGroupResolved)
			Expect(condition).ToNot(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionTrue))
		})

		It("should set error condition when SecurityGroup resolution fails", func() {
			// 使用 Tags 选择器来测试 API 调用失败的情况
			// 因为通过 ID 选择不会调用 API，无法触发错误
			nodeClass.Spec.SecurityGroupSelectorTerms = []v1alpha1.SecurityGroupSelectorTerm{
				{Tags: map[string]string{"test": "error"}},
			}

			vswitchId := "vsw-test-123"
			zoneId := "cn-hangzhou-h"
			mockVPCClient.On("DescribeVSwitches", mock.Anything, mock.Anything, mock.Anything).Return(&vpc.DescribeVSwitchesResponse{
				Body: &vpc.DescribeVSwitchesResponseBody{
					VSwitches: &vpc.DescribeVSwitchesResponseBodyVSwitches{
						VSwitch: []*vpc.DescribeVSwitchesResponseBodyVSwitchesVSwitch{{VSwitchId: &vswitchId, ZoneId: &zoneId}},
					},
				},
			}, nil)

			mockECSClient.On("DescribeSecurityGroups", mock.Anything, map[string]string{"test": "error"}).Return(nil, fmt.Errorf("ECS API error"))

			imageId := "aliyun_3_x64_20G_alibase_20231221.vhd"
			mockECSClient.On("DescribeImages", mock.Anything, mock.Anything, mock.Anything).Return([]ecs.DescribeImagesResponseBodyImagesImage{
				{ImageId: &imageId},
			}, nil)

			roleName := "KarpenterNodeRole"
			assumeRolePolicy := `{"Statement":[{"Effect":"Allow","Principal":{"Service":["ecs.aliyuncs.com"]},"Action":"sts:AssumeRole"}]}`
			mockRAMClient.On("GetRole", mock.Anything, "KarpenterNodeRole").Return(&ram.GetRoleResponse{
				Body: &ram.GetRoleResponseBody{
					Role: &ram.GetRoleResponseBodyRole{
						RoleName:                 &roleName,
						AssumeRolePolicyDocument: &assumeRolePolicy,
					},
				},
			}, nil)

			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			_, err := statusController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClass),
			})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())

			condition := getCondition(updated, v1alpha1.ConditionTypeSecurityGroupResolved)
			Expect(condition).ToNot(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionFalse))
			Expect(condition.Reason).To(Equal("SecurityGroupResolutionFailed"))
		})

		It("should resolve multiple SecurityGroups by tags", func() {
			nodeClass.Spec.SecurityGroupSelectorTerms = []v1alpha1.SecurityGroupSelectorTerm{
				{Tags: map[string]string{"env": "prod"}},
			}

			vswitchId := "vsw-test-123"
			zoneId := "cn-hangzhou-h"
			mockVPCClient.On("DescribeVSwitches", mock.Anything, mock.Anything, mock.Anything).Return(&vpc.DescribeVSwitchesResponse{
				Body: &vpc.DescribeVSwitchesResponseBody{
					VSwitches: &vpc.DescribeVSwitchesResponseBodyVSwitches{
						VSwitch: []*vpc.DescribeVSwitchesResponseBodyVSwitchesVSwitch{{VSwitchId: &vswitchId, ZoneId: &zoneId}},
					},
				},
			}, nil)

			sg1 := "sg-1"
			sg2 := "sg-2"
			sgName1 := "sg-prod-1"
			sgName2 := "sg-prod-2"
			mockECSClient.On("DescribeSecurityGroups", mock.Anything, map[string]string{"env": "prod"}).Return(&ecs.DescribeSecurityGroupsResponse{
				Body: &ecs.DescribeSecurityGroupsResponseBody{
					SecurityGroups: &ecs.DescribeSecurityGroupsResponseBodySecurityGroups{
						SecurityGroup: []*ecs.DescribeSecurityGroupsResponseBodySecurityGroupsSecurityGroup{
							{SecurityGroupId: &sg1, SecurityGroupName: &sgName1},
							{SecurityGroupId: &sg2, SecurityGroupName: &sgName2},
						},
					},
				},
			}, nil)

			imageId := "aliyun_3_x64_20G_alibase_20231221.vhd"
			mockECSClient.On("DescribeImages", mock.Anything, mock.Anything, mock.Anything).Return([]ecs.DescribeImagesResponseBodyImagesImage{
				{ImageId: &imageId},
			}, nil)

			roleName := "KarpenterNodeRole"
			assumeRolePolicy := `{"Statement":[{"Effect":"Allow","Principal":{"Service":["ecs.aliyuncs.com"]},"Action":"sts:AssumeRole"}]}`
			mockRAMClient.On("GetRole", mock.Anything, "KarpenterNodeRole").Return(&ram.GetRoleResponse{
				Body: &ram.GetRoleResponseBody{
					Role: &ram.GetRoleResponseBodyRole{
						RoleName:                 &roleName,
						AssumeRolePolicyDocument: &assumeRolePolicy,
					},
				},
			}, nil)

			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			_, err := statusController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClass),
			})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())

			Expect(updated.Status.SecurityGroups).To(HaveLen(2))
			condition := getCondition(updated, v1alpha1.ConditionTypeSecurityGroupResolved)
			Expect(condition.Message).To(ContainSubstring("Resolved 2 SecurityGroups"))
		})
	})

	Context("Image Resolution", func() {
		It("should resolve Images successfully and set status", func() {
			vswitchId := "vsw-test-123"
			zoneId := "cn-hangzhou-h"
			mockVPCClient.On("DescribeVSwitches", mock.Anything, mock.Anything, mock.Anything).Return(&vpc.DescribeVSwitchesResponse{
				Body: &vpc.DescribeVSwitchesResponseBody{
					VSwitches: &vpc.DescribeVSwitchesResponseBodyVSwitches{
						VSwitch: []*vpc.DescribeVSwitchesResponseBodyVSwitchesVSwitch{{VSwitchId: &vswitchId, ZoneId: &zoneId}},
					},
				},
			}, nil)

			sgId := "sg-test-123"
			mockECSClient.On("DescribeSecurityGroups", mock.Anything, mock.Anything).Return(&ecs.DescribeSecurityGroupsResponse{
				Body: &ecs.DescribeSecurityGroupsResponseBody{
					SecurityGroups: &ecs.DescribeSecurityGroupsResponseBodySecurityGroups{
						SecurityGroup: []*ecs.DescribeSecurityGroupsResponseBodySecurityGroupsSecurityGroup{{SecurityGroupId: &sgId}},
					},
				},
			}, nil)

			imageId := "aliyun_3_x64_20G_alibase_20231221.vhd"
			imageName := "Alibaba Cloud Linux 3"
			arch := "x86_64"
			mockECSClient.On("DescribeImages", mock.Anything, []string{"aliyun_3_x64_20G_alibase_20231221.vhd"}, mock.Anything).Return([]ecs.DescribeImagesResponseBodyImagesImage{
				{
					ImageId:      &imageId,
					ImageName:    &imageName,
					Architecture: &arch,
				},
			}, nil)

			roleName := "KarpenterNodeRole"
			assumeRolePolicy := `{"Statement":[{"Effect":"Allow","Principal":{"Service":["ecs.aliyuncs.com"]},"Action":"sts:AssumeRole"}]}`
			mockRAMClient.On("GetRole", mock.Anything, "KarpenterNodeRole").Return(&ram.GetRoleResponse{
				Body: &ram.GetRoleResponseBody{
					Role: &ram.GetRoleResponseBodyRole{
						RoleName:                 &roleName,
						AssumeRolePolicyDocument: &assumeRolePolicy,
					},
				},
			}, nil)

			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			_, err := statusController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClass),
			})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())

			Expect(updated.Status.Images).To(HaveLen(1))
			Expect(updated.Status.Images[0].ID).To(Equal("aliyun_3_x64_20G_alibase_20231221.vhd"))
			Expect(updated.Status.Images[0].Name).To(Equal("Alibaba Cloud Linux 3"))
			Expect(updated.Status.Images[0].Architecture).To(Equal("x86_64"))

			condition := getCondition(updated, v1alpha1.ConditionTypeImageResolved)
			Expect(condition).ToNot(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionTrue))
		})

		It("should set error condition when Image resolution fails", func() {
			vswitchId := "vsw-test-123"
			zoneId := "cn-hangzhou-h"
			mockVPCClient.On("DescribeVSwitches", mock.Anything, mock.Anything, mock.Anything).Return(&vpc.DescribeVSwitchesResponse{
				Body: &vpc.DescribeVSwitchesResponseBody{
					VSwitches: &vpc.DescribeVSwitchesResponseBodyVSwitches{
						VSwitch: []*vpc.DescribeVSwitchesResponseBodyVSwitchesVSwitch{{VSwitchId: &vswitchId, ZoneId: &zoneId}},
					},
				},
			}, nil)

			sgId := "sg-test-123"
			mockECSClient.On("DescribeSecurityGroups", mock.Anything, mock.Anything).Return(&ecs.DescribeSecurityGroupsResponse{
				Body: &ecs.DescribeSecurityGroupsResponseBody{
					SecurityGroups: &ecs.DescribeSecurityGroupsResponseBodySecurityGroups{
						SecurityGroup: []*ecs.DescribeSecurityGroupsResponseBodySecurityGroupsSecurityGroup{{SecurityGroupId: &sgId}},
					},
				},
			}, nil)

			mockECSClient.On("DescribeImages", mock.Anything, mock.Anything, mock.Anything).Return(nil, fmt.Errorf("Image not found"))

			roleName := "KarpenterNodeRole"
			assumeRolePolicy := `{"Statement":[{"Effect":"Allow","Principal":{"Service":["ecs.aliyuncs.com"]},"Action":"sts:AssumeRole"}]}`
			mockRAMClient.On("GetRole", mock.Anything, "KarpenterNodeRole").Return(&ram.GetRoleResponse{
				Body: &ram.GetRoleResponseBody{
					Role: &ram.GetRoleResponseBodyRole{
						RoleName:                 &roleName,
						AssumeRolePolicyDocument: &assumeRolePolicy,
					},
				},
			}, nil)

			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			_, err := statusController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClass),
			})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())

			condition := getCondition(updated, v1alpha1.ConditionTypeImageResolved)
			Expect(condition).ToNot(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionFalse))
			Expect(condition.Reason).To(Equal("ImageResolutionFailed"))
		})
	})

	Context("RAM Role Validation", func() {
		It("should validate RAM role successfully and set status", func() {
			vswitchId := "vsw-test-123"
			zoneId := "cn-hangzhou-h"
			mockVPCClient.On("DescribeVSwitches", mock.Anything, mock.Anything, mock.Anything).Return(&vpc.DescribeVSwitchesResponse{
				Body: &vpc.DescribeVSwitchesResponseBody{
					VSwitches: &vpc.DescribeVSwitchesResponseBodyVSwitches{
						VSwitch: []*vpc.DescribeVSwitchesResponseBodyVSwitchesVSwitch{{VSwitchId: &vswitchId, ZoneId: &zoneId}},
					},
				},
			}, nil)

			sgId := "sg-test-123"
			mockECSClient.On("DescribeSecurityGroups", mock.Anything, mock.Anything).Return(&ecs.DescribeSecurityGroupsResponse{
				Body: &ecs.DescribeSecurityGroupsResponseBody{
					SecurityGroups: &ecs.DescribeSecurityGroupsResponseBodySecurityGroups{
						SecurityGroup: []*ecs.DescribeSecurityGroupsResponseBodySecurityGroupsSecurityGroup{{SecurityGroupId: &sgId}},
					},
				},
			}, nil)

			imageId := "aliyun_3_x64_20G_alibase_20231221.vhd"
			mockECSClient.On("DescribeImages", mock.Anything, mock.Anything, mock.Anything).Return([]ecs.DescribeImagesResponseBodyImagesImage{
				{ImageId: &imageId},
			}, nil)

			roleName := "KarpenterNodeRole"
			assumeRolePolicy := `{"Statement":[{"Effect":"Allow","Principal":{"Service":["ecs.aliyuncs.com"]},"Action":"sts:AssumeRole"}]}`
			mockRAMClient.On("GetRole", mock.Anything, "KarpenterNodeRole").Return(&ram.GetRoleResponse{
				Body: &ram.GetRoleResponseBody{
					Role: &ram.GetRoleResponseBodyRole{
						RoleName:                 &roleName,
						AssumeRolePolicyDocument: &assumeRolePolicy,
					},
				},
			}, nil)

			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			_, err := statusController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClass),
			})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())

			Expect(updated.Status.RAMRole).ToNot(BeNil())
			Expect(*updated.Status.RAMRole).To(Equal("KarpenterNodeRole"))

			condition := getCondition(updated, v1alpha1.ConditionTypeRAMRoleResolved)
			Expect(condition).ToNot(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionTrue))
			Expect(condition.Reason).To(Equal("RAMRoleResolved"))
		})

		It("should set error condition when RAM role validation fails", func() {
			vswitchId := "vsw-test-123"
			zoneId := "cn-hangzhou-h"
			mockVPCClient.On("DescribeVSwitches", mock.Anything, mock.Anything, mock.Anything).Return(&vpc.DescribeVSwitchesResponse{
				Body: &vpc.DescribeVSwitchesResponseBody{
					VSwitches: &vpc.DescribeVSwitchesResponseBodyVSwitches{
						VSwitch: []*vpc.DescribeVSwitchesResponseBodyVSwitchesVSwitch{{VSwitchId: &vswitchId, ZoneId: &zoneId}},
					},
				},
			}, nil)

			sgId := "sg-test-123"
			mockECSClient.On("DescribeSecurityGroups", mock.Anything, mock.Anything).Return(&ecs.DescribeSecurityGroupsResponse{
				Body: &ecs.DescribeSecurityGroupsResponseBody{
					SecurityGroups: &ecs.DescribeSecurityGroupsResponseBodySecurityGroups{
						SecurityGroup: []*ecs.DescribeSecurityGroupsResponseBodySecurityGroupsSecurityGroup{{SecurityGroupId: &sgId}},
					},
				},
			}, nil)

			imageId := "aliyun_3_x64_20G_alibase_20231221.vhd"
			mockECSClient.On("DescribeImages", mock.Anything, mock.Anything, mock.Anything).Return([]ecs.DescribeImagesResponseBodyImagesImage{
				{ImageId: &imageId},
			}, nil)

			mockRAMClient.On("GetRole", mock.Anything, "KarpenterNodeRole").Return(nil, fmt.Errorf("role not found"))

			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			_, err := statusController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClass),
			})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())

			condition := getCondition(updated, v1alpha1.ConditionTypeRAMRoleResolved)
			Expect(condition).ToNot(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		})

		It("should skip RAM role validation when Role is not specified", func() {
			nodeClass.Spec.Role = nil

			vswitchId := "vsw-test-123"
			zoneId := "cn-hangzhou-h"
			mockVPCClient.On("DescribeVSwitches", mock.Anything, mock.Anything, mock.Anything).Return(&vpc.DescribeVSwitchesResponse{
				Body: &vpc.DescribeVSwitchesResponseBody{
					VSwitches: &vpc.DescribeVSwitchesResponseBodyVSwitches{
						VSwitch: []*vpc.DescribeVSwitchesResponseBodyVSwitchesVSwitch{{VSwitchId: &vswitchId, ZoneId: &zoneId}},
					},
				},
			}, nil)

			sgId := "sg-test-123"
			mockECSClient.On("DescribeSecurityGroups", mock.Anything, mock.Anything).Return(&ecs.DescribeSecurityGroupsResponse{
				Body: &ecs.DescribeSecurityGroupsResponseBody{
					SecurityGroups: &ecs.DescribeSecurityGroupsResponseBodySecurityGroups{
						SecurityGroup: []*ecs.DescribeSecurityGroupsResponseBodySecurityGroupsSecurityGroup{{SecurityGroupId: &sgId}},
					},
				},
			}, nil)

			imageId := "aliyun_3_x64_20G_alibase_20231221.vhd"
			mockECSClient.On("DescribeImages", mock.Anything, mock.Anything, mock.Anything).Return([]ecs.DescribeImagesResponseBodyImagesImage{
				{ImageId: &imageId},
			}, nil)

			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			_, err := statusController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClass),
			})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())

			// RAM role condition should not be set when Role is nil
			Expect(updated.Status.RAMRole).To(BeNil())
		})
	})

	Context("Ready Condition", func() {
		It("should set Ready to true when all resources are resolved", func() {
			vswitchId := "vsw-test-123"
			zoneId := "cn-hangzhou-h"
			mockVPCClient.On("DescribeVSwitches", mock.Anything, mock.Anything, mock.Anything).Return(&vpc.DescribeVSwitchesResponse{
				Body: &vpc.DescribeVSwitchesResponseBody{
					VSwitches: &vpc.DescribeVSwitchesResponseBodyVSwitches{
						VSwitch: []*vpc.DescribeVSwitchesResponseBodyVSwitchesVSwitch{{VSwitchId: &vswitchId, ZoneId: &zoneId}},
					},
				},
			}, nil)

			sgId := "sg-test-123"
			mockECSClient.On("DescribeSecurityGroups", mock.Anything, mock.Anything).Return(&ecs.DescribeSecurityGroupsResponse{
				Body: &ecs.DescribeSecurityGroupsResponseBody{
					SecurityGroups: &ecs.DescribeSecurityGroupsResponseBodySecurityGroups{
						SecurityGroup: []*ecs.DescribeSecurityGroupsResponseBodySecurityGroupsSecurityGroup{{SecurityGroupId: &sgId}},
					},
				},
			}, nil)

			imageId := "aliyun_3_x64_20G_alibase_20231221.vhd"
			mockECSClient.On("DescribeImages", mock.Anything, mock.Anything, mock.Anything).Return([]ecs.DescribeImagesResponseBodyImagesImage{
				{ImageId: &imageId},
			}, nil)

			roleName := "KarpenterNodeRole"
			assumeRolePolicy := `{"Statement":[{"Effect":"Allow","Principal":{"Service":["ecs.aliyuncs.com"]},"Action":"sts:AssumeRole"}]}`
			mockRAMClient.On("GetRole", mock.Anything, "KarpenterNodeRole").Return(&ram.GetRoleResponse{
				Body: &ram.GetRoleResponseBody{
					Role: &ram.GetRoleResponseBodyRole{
						RoleName:                 &roleName,
						AssumeRolePolicyDocument: &assumeRolePolicy,
					},
				},
			}, nil)

			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			_, err := statusController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClass),
			})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())

			readyCondition := getCondition(updated, v1alpha1.ConditionTypeReady)
			Expect(readyCondition).ToNot(BeNil())
			Expect(readyCondition.Status).To(Equal(metav1.ConditionTrue))
			Expect(readyCondition.Reason).To(Equal("Ready"))
			Expect(readyCondition.Message).To(Equal("ECSNodeClass is ready"))
		})

		It("should set Ready to false when any resource resolution fails", func() {
			mockVPCClient.On("DescribeVSwitches", mock.Anything, mock.Anything, mock.Anything).Return(nil, fmt.Errorf("VPC error"))

			sgId := "sg-test-123"
			mockECSClient.On("DescribeSecurityGroups", mock.Anything, mock.Anything).Return(&ecs.DescribeSecurityGroupsResponse{
				Body: &ecs.DescribeSecurityGroupsResponseBody{
					SecurityGroups: &ecs.DescribeSecurityGroupsResponseBodySecurityGroups{
						SecurityGroup: []*ecs.DescribeSecurityGroupsResponseBodySecurityGroupsSecurityGroup{{SecurityGroupId: &sgId}},
					},
				},
			}, nil)

			imageId := "aliyun_3_x64_20G_alibase_20231221.vhd"
			mockECSClient.On("DescribeImages", mock.Anything, mock.Anything, mock.Anything).Return([]ecs.DescribeImagesResponseBodyImagesImage{
				{ImageId: &imageId},
			}, nil)

			roleName := "KarpenterNodeRole"
			assumeRolePolicy := `{"Statement":[{"Effect":"Allow","Principal":{"Service":["ecs.aliyuncs.com"]},"Action":"sts:AssumeRole"}]}`
			mockRAMClient.On("GetRole", mock.Anything, "KarpenterNodeRole").Return(&ram.GetRoleResponse{
				Body: &ram.GetRoleResponseBody{
					Role: &ram.GetRoleResponseBodyRole{
						RoleName:                 &roleName,
						AssumeRolePolicyDocument: &assumeRolePolicy,
					},
				},
			}, nil)

			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			_, err := statusController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClass),
			})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())

			readyCondition := getCondition(updated, v1alpha1.ConditionTypeReady)
			Expect(readyCondition).ToNot(BeNil())
			Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
		})
	})

	Context("Edge Cases", func() {
		It("should handle non-existent ECSNodeClass gracefully", func() {
			nonExistent := &v1alpha1.ECSNodeClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "non-existent",
				},
			}

			// Should not error, just ignore not found
			_, err := statusController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nonExistent),
			})
			Expect(err).ToNot(HaveOccurred())
		})

		It("should update status on subsequent reconciliations", func() {
			vswitchId := "vsw-test-123"
			zoneId := "cn-hangzhou-h"
			mockVPCClient.On("DescribeVSwitches", mock.Anything, mock.Anything, mock.Anything).Return(&vpc.DescribeVSwitchesResponse{
				Body: &vpc.DescribeVSwitchesResponseBody{
					VSwitches: &vpc.DescribeVSwitchesResponseBodyVSwitches{
						VSwitch: []*vpc.DescribeVSwitchesResponseBodyVSwitchesVSwitch{{VSwitchId: &vswitchId, ZoneId: &zoneId}},
					},
				},
			}, nil)

			sgId := "sg-test-123"
			mockECSClient.On("DescribeSecurityGroups", mock.Anything, mock.Anything).Return(&ecs.DescribeSecurityGroupsResponse{
				Body: &ecs.DescribeSecurityGroupsResponseBody{
					SecurityGroups: &ecs.DescribeSecurityGroupsResponseBodySecurityGroups{
						SecurityGroup: []*ecs.DescribeSecurityGroupsResponseBodySecurityGroupsSecurityGroup{{SecurityGroupId: &sgId}},
					},
				},
			}, nil)

			imageId := "aliyun_3_x64_20G_alibase_20231221.vhd"
			mockECSClient.On("DescribeImages", mock.Anything, mock.Anything, mock.Anything).Return([]ecs.DescribeImagesResponseBodyImagesImage{
				{ImageId: &imageId},
			}, nil)

			roleName := "KarpenterNodeRole"
			assumeRolePolicy := `{"Statement":[{"Effect":"Allow","Principal":{"Service":["ecs.aliyuncs.com"]},"Action":"sts:AssumeRole"}]}`
			mockRAMClient.On("GetRole", mock.Anything, "KarpenterNodeRole").Return(&ram.GetRoleResponse{
				Body: &ram.GetRoleResponseBody{
					Role: &ram.GetRoleResponseBodyRole{
						RoleName:                 &roleName,
						AssumeRolePolicyDocument: &assumeRolePolicy,
					},
				},
			}, nil)

			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())

			// First reconciliation
			_, err := statusController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClass),
			})
			Expect(err).ToNot(HaveOccurred())

			// Second reconciliation - should still work
			_, err = statusController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClass),
			})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())
			Expect(updated.Status.VSwitches).To(HaveLen(1))
		})
	})
})

// Helper functions
func getCondition(nodeClass *v1alpha1.ECSNodeClass, conditionType string) *metav1.Condition {
	for i := range nodeClass.Status.Conditions {
		if nodeClass.Status.Conditions[i].Type == conditionType {
			return &nodeClass.Status.Conditions[i]
		}
	}
	return nil
}

func stringPtr(s string) *string {
	return &s
}

// MockECSClient is a mock implementation of ECSClient for testing
type MockECSClient struct {
	mock.Mock
}

func (m *MockECSClient) CreateLaunchTemplate(ctx context.Context, request *ecs.CreateLaunchTemplateRequest) (*ecs.CreateLaunchTemplateResponse, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ecs.CreateLaunchTemplateResponse), args.Error(1)
}

func (m *MockECSClient) DescribeLaunchTemplates(ctx context.Context, request *ecs.DescribeLaunchTemplatesRequest) (*ecs.DescribeLaunchTemplatesResponse, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ecs.DescribeLaunchTemplatesResponse), args.Error(1)
}

func (m *MockECSClient) DeleteLaunchTemplate(ctx context.Context, request *ecs.DeleteLaunchTemplateRequest) (*ecs.DeleteLaunchTemplateResponse, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ecs.DeleteLaunchTemplateResponse), args.Error(1)
}

func (m *MockECSClient) DescribeInstanceTypes(ctx context.Context, instanceTypes []string) (*ecs.DescribeInstanceTypesResponse, error) {
	args := m.Called(ctx, instanceTypes)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ecs.DescribeInstanceTypesResponse), args.Error(1)
}

func (m *MockECSClient) DescribeZones(ctx context.Context) (*ecs.DescribeZonesResponse, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ecs.DescribeZonesResponse), args.Error(1)
}

func (m *MockECSClient) DescribeImages(ctx context.Context, imageIDs []string, filters map[string]string) ([]ecs.DescribeImagesResponseBodyImagesImage, error) {
	args := m.Called(ctx, imageIDs, filters)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]ecs.DescribeImagesResponseBodyImagesImage), args.Error(1)
}

func (m *MockECSClient) DescribeSecurityGroups(ctx context.Context, tags map[string]string) (*ecs.DescribeSecurityGroupsResponse, error) {
	args := m.Called(ctx, tags)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ecs.DescribeSecurityGroupsResponse), args.Error(1)
}

func (m *MockECSClient) DescribeCapacityReservations(ctx context.Context, id string, tags map[string]string) (*ecs.DescribeCapacityReservationsResponse, error) {
	args := m.Called(ctx, id, tags)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ecs.DescribeCapacityReservationsResponse), args.Error(1)
}

func (m *MockECSClient) DescribePrice(ctx context.Context, instanceType string) (*ecs.DescribePriceResponse, error) {
	args := m.Called(ctx, instanceType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ecs.DescribePriceResponse), args.Error(1)
}

func (m *MockECSClient) RunInstances(ctx context.Context, request *ecs.RunInstancesRequest) (*ecs.RunInstancesResponse, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ecs.RunInstancesResponse), args.Error(1)
}

func (m *MockECSClient) DescribeInstances(ctx context.Context, request *ecs.DescribeInstancesRequest) (*ecs.DescribeInstancesResponse, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ecs.DescribeInstancesResponse), args.Error(1)
}

func (m *MockECSClient) DeleteInstances(ctx context.Context, request *ecs.DeleteInstancesRequest) (*ecs.DeleteInstancesResponse, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ecs.DeleteInstancesResponse), args.Error(1)
}

func (m *MockECSClient) TagResources(ctx context.Context, request *ecs.TagResourcesRequest) (*ecs.TagResourcesResponse, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ecs.TagResourcesResponse), args.Error(1)
}

// MockVPCClient is a mock implementation of VPCClient for testing
type MockVPCClient struct {
	mock.Mock
}

func (m *MockVPCClient) DescribeVSwitches(ctx context.Context, vSwitchID string, tags map[string]string) (*vpc.DescribeVSwitchesResponse, error) {
	args := m.Called(ctx, vSwitchID, tags)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*vpc.DescribeVSwitchesResponse), args.Error(1)
}

// MockRAMClient is a mock implementation of RAMClient for testing
type MockRAMClient struct {
	mock.Mock
}

func (m *MockRAMClient) GetRole(ctx context.Context, roleName string) (*ram.GetRoleResponse, error) {
	args := m.Called(ctx, roleName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*ram.GetRoleResponse), args.Error(1)
}
