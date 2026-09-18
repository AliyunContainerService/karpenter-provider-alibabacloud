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

package tagging_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/controllers/nodeclaim/tagging"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instance"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	coreapis "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"
)

var (
	ctx               context.Context
	env               *coretest.Environment
	taggingController *tagging.Controller
	testEnv           *envtest.Environment
	cfg               *rest.Config
	mockECSClient     *MockECSClient
	instanceProvider  *instance.Provider
)

func TestTagging(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Tagging Controller Suite")
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

	// Setup mock ECS client
	mockECSClient = new(MockECSClient)

	// Create instance provider with mock client
	instanceProvider = instance.NewProvider(ctx, "cn-hangzhou", mockECSClient)

	// Create tagging controller
	taggingController = tagging.NewController(k8sClient, instanceProvider)

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

// mockDescribeInstancesWithTags sets up DescribeInstances mock to return an instance
// with the given tags. Used for diff-based tagging tests.
func mockDescribeInstancesWithTags(instanceID string, tags map[string]string) {
	var ecsTags []*ecs.DescribeInstancesResponseBodyInstancesInstanceTagsTag
	for k, v := range tags {
		ecsTags = append(ecsTags, &ecs.DescribeInstancesResponseBodyInstancesInstanceTagsTag{
			TagKey:   tea.String(k),
			TagValue: tea.String(v),
		})
	}

	mockECSClient.On("DescribeInstances", mock.Anything, mock.Anything).
		Return(&ecs.DescribeInstancesResponse{
			Body: &ecs.DescribeInstancesResponseBody{
				TotalCount: tea.Int32(1),
				PageNumber: tea.Int32(1),
				PageSize:   tea.Int32(100),
				Instances: &ecs.DescribeInstancesResponseBodyInstances{
					Instance: []*ecs.DescribeInstancesResponseBodyInstancesInstance{
						{
							InstanceId:         tea.String(instanceID),
							RegionId:           tea.String("cn-hangzhou"),
							ZoneId:             tea.String("cn-hangzhou-a"),
							InstanceType:       tea.String("ecs.g6.large"),
							ImageId:            tea.String("aliyun_3_x64_20G_alibase_20231221.vhd"),
							Status:             tea.String("Running"),
							CreationTime:       tea.String("2024-01-01T00:00:00Z"),
							Cpu:                tea.Int32(2),
							Memory:             tea.Int32(8192),
							InstanceChargeType: tea.String("PostPaid"),
							SpotStrategy:       tea.String("NoSpot"),
							SecurityGroupIds: &ecs.DescribeInstancesResponseBodyInstancesInstanceSecurityGroupIds{
								SecurityGroupId: []*string{tea.String("sg-test")},
							},
							VpcAttributes: &ecs.DescribeInstancesResponseBodyInstancesInstanceVpcAttributes{
								VSwitchId: tea.String("vsw-test"),
							},
							Tags: &ecs.DescribeInstancesResponseBodyInstancesInstanceTags{
								Tag: ecsTags,
							},
						},
					},
				},
			},
		}, nil).Maybe()
}

var _ = Describe("TaggingController", func() {
	var nodeClaim *coreapis.NodeClaim
	var nodeClass *v1alpha1.ECSNodeClass

	BeforeEach(func() {
		// Reset mock expectations
		mockECSClient = new(MockECSClient)
		instanceProvider = instance.NewProvider(ctx, "cn-hangzhou", mockECSClient)
		taggingController = tagging.NewController(env.Client, instanceProvider)

		// Create ECSNodeClass
		nodeClass = &v1alpha1.ECSNodeClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-nodeclass",
			},
			Spec: v1alpha1.ECSNodeClassSpec{
				ClusterID: "test-cluster-123",
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
				Tags: map[string]string{
					"Environment": "test",
					"Team":        "platform",
				},
			},
		}

		// Create NodeClaim
		nodeClaim = &coreapis.NodeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-nodeclaim",
				Labels: map[string]string{
					coreapis.NodePoolLabelKey: "default",
					"workload-type":           "batch",
				},
			},
			Spec: coreapis.NodeClaimSpec{
				NodeClassRef: &coreapis.NodeClassReference{
					Group: "karpenter.alibabacloud.com",
					Kind:  "ECSNodeClass",
					Name:  "test-nodeclass",
				},
				Requirements: []coreapis.NodeSelectorRequirementWithMinValues{
					{
						NodeSelectorRequirement: v1.NodeSelectorRequirement{
							Key:      v1.LabelArchStable,
							Operator: v1.NodeSelectorOpIn,
							Values:   []string{"amd64"},
						},
					},
				},
			},
		}
	})

	AfterEach(func() {
		// Clean up all resources
		Expect(env.Client.DeleteAllOf(ctx, &coreapis.NodeClaim{})).To(Succeed())
		Expect(env.Client.DeleteAllOf(ctx, &v1alpha1.ECSNodeClass{})).To(Succeed())
	})

	Context("Basic Tagging", func() {
		It("should tag instance with Karpenter metadata when NodeClaim is created", func() {
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			// Mock DescribeInstances to return instance with no tags
			mockDescribeInstancesWithTags("i-test123456", map[string]string{})

			// Setup mock expectations
			mockECSClient.On("TagResources", mock.Anything, mock.MatchedBy(func(req *ecs.TagResourcesRequest) bool {
				if len(req.ResourceId) != 1 || *req.ResourceId[0] != "i-test123456" {
					return false
				}
				if req.ResourceType == nil || *req.ResourceType != "instance" {
					return false
				}
				if req.Tag == nil || len(req.Tag) == 0 {
					return false
				}
				return true
			})).Return(&ecs.TagResourcesResponse{}, nil)

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())
			mockECSClient.AssertExpectations(GinkgoT())
		})

		It("should include all required Karpenter tags including cluster-id", func() {
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			// Mock DescribeInstances to return instance with no tags
			mockDescribeInstancesWithTags("i-test123456", map[string]string{})

			var capturedTags map[string]string
			mockECSClient.On("TagResources", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				req := args.Get(1).(*ecs.TagResourcesRequest)
				capturedTags = make(map[string]string)
				for _, tag := range req.Tag {
					capturedTags[*tag.Key] = *tag.Value
				}
			}).Return(&ecs.TagResourcesResponse{}, nil)

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())

			// Verify required tags
			Expect(capturedTags).To(HaveKeyWithValue(v1alpha1.TagNodeClaim, "test-nodeclaim"))
			Expect(capturedTags).To(HaveKeyWithValue(v1alpha1.TagNodePool, "default"))
			Expect(capturedTags).To(HaveKeyWithValue(v1alpha1.TagManagedBy, v1alpha1.TagManagedByValue))
			Expect(capturedTags).To(HaveKeyWithValue(v1alpha1.TagClusterID, "test-cluster-123"))
			// An old instance without launch-time storage tags must not be
			// assigned estimates from the current NodeClass during reconciliation.
			Expect(capturedTags).ToNot(HaveKey(v1alpha1.TagEphemeralStorageCapacity))
			Expect(capturedTags).ToNot(HaveKey(v1alpha1.TagEphemeralStorageAllocatable))
		})

		It("should include custom tags from NodeClass", func() {
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			mockDescribeInstancesWithTags("i-test123456", map[string]string{})

			var capturedTags map[string]string
			mockECSClient.On("TagResources", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				req := args.Get(1).(*ecs.TagResourcesRequest)
				capturedTags = make(map[string]string)
				for _, tag := range req.Tag {
					capturedTags[*tag.Key] = *tag.Value
				}
			}).Return(&ecs.TagResourcesResponse{}, nil)

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())

			Expect(capturedTags).To(HaveKeyWithValue("Environment", "test"))
			Expect(capturedTags).To(HaveKeyWithValue("Team", "platform"))
		})

		It("should not copy derived NodeClaim labels to ECS tags", func() {
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			mockDescribeInstancesWithTags("i-test123456", map[string]string{})

			var capturedTags map[string]string
			mockECSClient.On("TagResources", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				req := args.Get(1).(*ecs.TagResourcesRequest)
				capturedTags = make(map[string]string)
				for _, tag := range req.Tag {
					capturedTags[*tag.Key] = *tag.Value
				}
			}).Return(&ecs.TagResourcesResponse{}, nil)

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())

			Expect(capturedTags).ToNot(HaveKey("workload-type"))
			Expect(capturedTags).To(HaveKeyWithValue(v1alpha1.TagNodePool, "default"))
		})
	})

	Context("Annotation-Based Skip", func() {
		It("should skip tagging when annotation is already set", func() {
			nodeClaim.Annotations = map[string]string{
				v1alpha1.AnnotationInstanceTagged: "true",
			}
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			// No mock expectations - TagResources and DescribeInstances should NOT be called

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())

			// Verify TagResources was not called
			mockECSClient.AssertNotCalled(GinkgoT(), "TagResources", mock.Anything, mock.Anything)
		})

		It("should set annotation after successful tagging", func() {
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			mockDescribeInstancesWithTags("i-test123456", map[string]string{})
			mockECSClient.On("TagResources", mock.Anything, mock.Anything).
				Return(&ecs.TagResourcesResponse{}, nil)

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())

			// Verify annotation was set
			updated := &coreapis.NodeClaim{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClaim), updated)).To(Succeed())
			Expect(updated.Annotations).To(HaveKeyWithValue(v1alpha1.AnnotationInstanceTagged, "true"))
		})
	})

	Context("Diff-Based Tagging", func() {
		It("should only tag missing tags when some already exist", func() {
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			// Instance already has managed-by and nodeclaim tags
			mockDescribeInstancesWithTags("i-test123456", map[string]string{
				v1alpha1.TagManagedBy: v1alpha1.TagManagedByValue,
				v1alpha1.TagNodeClaim: "test-nodeclaim",
			})

			var capturedTags map[string]string
			mockECSClient.On("TagResources", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				req := args.Get(1).(*ecs.TagResourcesRequest)
				capturedTags = make(map[string]string)
				for _, tag := range req.Tag {
					capturedTags[*tag.Key] = *tag.Value
				}
			}).Return(&ecs.TagResourcesResponse{}, nil)

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())

			// Should NOT include already-present tags
			Expect(capturedTags).ToNot(HaveKey(v1alpha1.TagManagedBy))
			Expect(capturedTags).ToNot(HaveKey(v1alpha1.TagNodeClaim))
			// Should include missing tags
			Expect(capturedTags).To(HaveKeyWithValue(v1alpha1.TagNodePool, "default"))
			Expect(capturedTags).To(HaveKeyWithValue(v1alpha1.TagClusterID, "test-cluster-123"))
			Expect(capturedTags).To(HaveKeyWithValue("Environment", "test"))
			Expect(capturedTags).To(HaveKeyWithValue("Team", "platform"))
		})

		It("should not call TagResources when all tags already exist", func() {
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			// Instance already has ALL tags
			mockDescribeInstancesWithTags("i-test123456", map[string]string{
				v1alpha1.TagManagedBy: v1alpha1.TagManagedByValue,
				v1alpha1.TagNodeClaim: "test-nodeclaim",
				v1alpha1.TagNodePool:  "default",
				v1alpha1.TagClusterID: "test-cluster-123",
				"Environment":         "test",
				"Team":                "platform",
			})

			// TagResources should NOT be called
			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())

			mockECSClient.AssertNotCalled(GinkgoT(), "TagResources", mock.Anything, mock.Anything)
		})

		It("should update tag when value differs", func() {
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			// Instance has old value for Team tag
			mockDescribeInstancesWithTags("i-test123456", map[string]string{
				v1alpha1.TagManagedBy: v1alpha1.TagManagedByValue,
				v1alpha1.TagNodeClaim: "test-nodeclaim",
				v1alpha1.TagNodePool:  "default",
				v1alpha1.TagClusterID: "test-cluster-123",
				"Environment":         "test",
				"Team":                "old-team", // Different value
			})

			var capturedTags map[string]string
			mockECSClient.On("TagResources", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				req := args.Get(1).(*ecs.TagResourcesRequest)
				capturedTags = make(map[string]string)
				for _, tag := range req.Tag {
					capturedTags[*tag.Key] = *tag.Value
				}
			}).Return(&ecs.TagResourcesResponse{}, nil)

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())

			// Should include Team with new value
			Expect(capturedTags).To(HaveKeyWithValue("Team", "platform"))
			// Should NOT include unchanged tags
			Expect(capturedTags).ToNot(HaveKey(v1alpha1.TagManagedBy))
			Expect(capturedTags).ToNot(HaveKey("Environment"))
		})
	})

	Context("Provider ID Handling", func() {
		It("should skip when provider ID is empty", func() {
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = ""
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())
			mockECSClient.AssertNotCalled(GinkgoT(), "TagResources", mock.Anything, mock.Anything)
		})

		It("should extract instance ID from provider ID with region prefix", func() {
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-abc123xyz"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			mockDescribeInstancesWithTags("i-abc123xyz", map[string]string{})

			mockECSClient.On("TagResources", mock.Anything, mock.MatchedBy(func(req *ecs.TagResourcesRequest) bool {
				return len(req.ResourceId) == 1 && *req.ResourceId[0] == "i-abc123xyz"
			})).Return(&ecs.TagResourcesResponse{}, nil)

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())
			mockECSClient.AssertExpectations(GinkgoT())
		})

		It("should extract instance ID from provider ID with multiple dots", func() {
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "alibabacloud://cn-beijing.i-xyz789"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			mockDescribeInstancesWithTags("i-xyz789", map[string]string{})

			mockECSClient.On("TagResources", mock.Anything, mock.MatchedBy(func(req *ecs.TagResourcesRequest) bool {
				return len(req.ResourceId) == 1 && *req.ResourceId[0] == "i-xyz789"
			})).Return(&ecs.TagResourcesResponse{}, nil)

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())
			mockECSClient.AssertExpectations(GinkgoT())
		})

		It("should handle provider ID without dots", func() {
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "i-standalone123"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			mockDescribeInstancesWithTags("i-standalone123", map[string]string{})

			mockECSClient.On("TagResources", mock.Anything, mock.MatchedBy(func(req *ecs.TagResourcesRequest) bool {
				return len(req.ResourceId) == 1 && *req.ResourceId[0] == "i-standalone123"
			})).Return(&ecs.TagResourcesResponse{}, nil)

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())
			mockECSClient.AssertExpectations(GinkgoT())
		})
	})

	Context("Error Handling", func() {
		It("should not error when NodeClaim does not exist", func() {
			nonExistent := &coreapis.NodeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name: "non-existent",
				},
			}

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nonExistent),
			})
			Expect(err).ToNot(HaveOccurred())
		})

		It("should not error when NodeClass does not exist", func() {
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return error when ECS TagResources fails", func() {
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			mockDescribeInstancesWithTags("i-test123456", map[string]string{})

			mockECSClient.On("TagResources", mock.Anything, mock.Anything).
				Return(nil, fmt.Errorf("SDK.ServerError ErrorCode: %s Message: %s", "InternalError", "Internal Server Error"))

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to tag instance"))
		})

		It("should return error when DescribeInstances fails", func() {
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			mockECSClient.On("DescribeInstances", mock.Anything, mock.Anything).
				Return(nil, fmt.Errorf("SDK.ServerError ErrorCode: %s Message: %s", "Throttling", "Request was denied due to request throttling"))

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to tag instance"))
		})
	})

	Context("Edge Cases", func() {
		It("should handle NodeClass without custom tags", func() {
			nodeClass.Spec.Tags = nil
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			mockDescribeInstancesWithTags("i-test123456", map[string]string{})

			var capturedTags map[string]string
			mockECSClient.On("TagResources", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				req := args.Get(1).(*ecs.TagResourcesRequest)
				capturedTags = make(map[string]string)
				for _, tag := range req.Tag {
					capturedTags[*tag.Key] = *tag.Value
				}
			}).Return(&ecs.TagResourcesResponse{}, nil)

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())

			// Should still have Karpenter tags
			Expect(capturedTags).To(HaveKey(v1alpha1.TagNodeClaim))
			Expect(capturedTags).To(HaveKey(v1alpha1.TagManagedBy))
			Expect(capturedTags).To(HaveKey(v1alpha1.TagClusterID))
		})

		It("should handle NodeClaim without labels", func() {
			nodeClaim.Labels = nil
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			mockDescribeInstancesWithTags("i-test123456", map[string]string{})

			var capturedTags map[string]string
			mockECSClient.On("TagResources", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				req := args.Get(1).(*ecs.TagResourcesRequest)
				capturedTags = make(map[string]string)
				for _, tag := range req.Tag {
					capturedTags[*tag.Key] = *tag.Value
				}
			}).Return(&ecs.TagResourcesResponse{}, nil)

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())

			Expect(capturedTags).To(HaveKey(v1alpha1.TagNodeClaim))
			Expect(capturedTags).To(HaveKey(v1alpha1.TagManagedBy))
		})

		It("should handle empty NodeClass tags", func() {
			nodeClass.Spec.Tags = map[string]string{}
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			mockDescribeInstancesWithTags("i-test123456", map[string]string{})

			mockECSClient.On("TagResources", mock.Anything, mock.Anything).
				Return(&ecs.TagResourcesResponse{}, nil)

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())
			mockECSClient.AssertExpectations(GinkgoT())
		})

		It("should handle NodeClass without cluster-id", func() {
			nodeClass.Spec.ClusterID = ""
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			mockDescribeInstancesWithTags("i-test123456", map[string]string{})

			var capturedTags map[string]string
			mockECSClient.On("TagResources", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				req := args.Get(1).(*ecs.TagResourcesRequest)
				capturedTags = make(map[string]string)
				for _, tag := range req.Tag {
					capturedTags[*tag.Key] = *tag.Value
				}
			}).Return(&ecs.TagResourcesResponse{}, nil)

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())

			// Should NOT include cluster-id tag
			Expect(capturedTags).ToNot(HaveKey(v1alpha1.TagClusterID))
		})

		It("should handle tag key conflicts — NodeClass tags win", func() {
			nodeClass.Spec.Tags = map[string]string{
				"Team": "platform",
			}
			nodeClaim.Labels = map[string]string{
				"Team":                    "devops",
				coreapis.NodePoolLabelKey: "default",
			}

			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			mockDescribeInstancesWithTags("i-test123456", map[string]string{})

			var capturedTags map[string]string
			mockECSClient.On("TagResources", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				req := args.Get(1).(*ecs.TagResourcesRequest)
				capturedTags = make(map[string]string)
				for _, tag := range req.Tag {
					capturedTags[*tag.Key] = *tag.Value
				}
			}).Return(&ecs.TagResourcesResponse{}, nil)

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())

			// NodeClass tags have highest priority
			Expect(capturedTags).To(HaveKeyWithValue("Team", "platform"))
		})
	})

	Context("Tagging with >20 tags (batching)", func() {
		It("should handle more than 20 tags by splitting into batches", func() {
			// Create NodeClass with 25 custom tags
			customTags := make(map[string]string)
			for i := 0; i < 25; i++ {
				customTags[fmt.Sprintf("custom-tag-%d", i)] = fmt.Sprintf("value-%d", i)
			}
			nodeClass.Spec.Tags = customTags

			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			mockDescribeInstancesWithTags("i-test123456", map[string]string{})

			// Track all TagResources calls
			var allCapturedTags []map[string]string
			mockECSClient.On("TagResources", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				req := args.Get(1).(*ecs.TagResourcesRequest)
				batchTags := make(map[string]string)
				for _, tag := range req.Tag {
					batchTags[*tag.Key] = *tag.Value
				}
				allCapturedTags = append(allCapturedTags, batchTags)
			}).Return(&ecs.TagResourcesResponse{}, nil)

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())

			// Should have 2 batches: 20 + 9 (4 karpenter tags + 25 custom = 29 total)
			Expect(len(allCapturedTags)).To(Equal(2))
			Expect(len(allCapturedTags[0])).To(BeNumerically("<=", 20))
			Expect(len(allCapturedTags[1])).To(BeNumerically("<=", 20))

			// Merge all batches and verify all tags present
			merged := make(map[string]string)
			for _, batch := range allCapturedTags {
				for k, v := range batch {
					merged[k] = v
				}
			}
			// 4 karpenter tags + 25 custom = 29
			Expect(len(merged)).To(Equal(29))
			for i := 0; i < 25; i++ {
				Expect(merged).To(HaveKeyWithValue(fmt.Sprintf("custom-tag-%d", i), fmt.Sprintf("value-%d", i)))
			}
		})
	})

	Context("Multiple NodeClaims", func() {
		It("should tag different instances for different NodeClaims", func() {
			nodeClaim1 := nodeClaim.DeepCopy()
			nodeClaim1.Name = "nodeclaim-1"

			nodeClaim2 := nodeClaim.DeepCopy()
			nodeClaim2.Name = "nodeclaim-2"

			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim1)).To(Succeed())
			nodeClaim1.Status.ProviderID = "cn-hangzhou.i-instance001"
			Expect(env.Client.Status().Update(ctx, nodeClaim1)).To(Succeed())

			Expect(env.Client.Create(ctx, nodeClaim2)).To(Succeed())
			nodeClaim2.Status.ProviderID = "cn-hangzhou.i-instance002"
			Expect(env.Client.Status().Update(ctx, nodeClaim2)).To(Succeed())

			mockDescribeInstancesWithTags("i-instance001", map[string]string{})
			mockDescribeInstancesWithTags("i-instance002", map[string]string{})

			mockECSClient.On("TagResources", mock.Anything, mock.MatchedBy(func(req *ecs.TagResourcesRequest) bool {
				return len(req.ResourceId) == 1 && *req.ResourceId[0] == "i-instance001"
			})).Return(&ecs.TagResourcesResponse{}, nil).Once()

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim1),
			})
			Expect(err).ToNot(HaveOccurred())

			mockECSClient.On("TagResources", mock.Anything, mock.MatchedBy(func(req *ecs.TagResourcesRequest) bool {
				return len(req.ResourceId) == 1 && *req.ResourceId[0] == "i-instance002"
			})).Return(&ecs.TagResourcesResponse{}, nil).Once()

			_, err = taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim2),
			})
			Expect(err).ToNot(HaveOccurred())

			mockECSClient.AssertExpectations(GinkgoT())
		})
	})
})

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

func (m *MockECSClient) DescribeAvailableResource(ctx context.Context, request *ecs.DescribeAvailableResourceRequest) (*ecs.DescribeAvailableResourceResponse, error) {
	return nil, errors.New("not implemented")
}

func (m *MockECSClient) DescribeImages(ctx context.Context, imageIDs []string, filters map[string]string) ([]ecs.DescribeImagesResponseBodyImagesImage, error) {
	args := m.Called(ctx, imageIDs, filters)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]ecs.DescribeImagesResponseBodyImagesImage), args.Error(1)
}

func (m *MockECSClient) DescribeSecurityGroups(ctx context.Context, id string, name string, tags map[string]string) (*ecs.DescribeSecurityGroupsResponse, error) {
	args := m.Called(ctx, id, name, tags)
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

// Helper functions
func stringPtr(s string) *string {
	return &s
}
