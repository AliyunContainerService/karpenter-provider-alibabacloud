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
	"fmt"
	"path/filepath"
	"testing"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/controllers/nodeclaim/tagging"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instance"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
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
	instanceProvider = instance.NewProvider("cn-hangzhou", mockECSClient)

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

var _ = Describe("TaggingController", func() {
	var nodeClaim *coreapis.NodeClaim
	var nodeClass *v1alpha1.ECSNodeClass

	BeforeEach(func() {
		// Reset mock expectations
		mockECSClient = new(MockECSClient)
		instanceProvider = instance.NewProvider("cn-hangzhou", mockECSClient)
		taggingController = tagging.NewController(env.Client, instanceProvider)

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
			// Create resources
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			// Update status separately as it's a subresource
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			// Setup mock expectations
			mockECSClient.On("TagResources", mock.Anything, mock.MatchedBy(func(req *ecs.TagResourcesRequest) bool {
				// Verify instance ID
				if len(*req.ResourceId) != 1 || (*req.ResourceId)[0] != "i-test123456" {
					return false
				}
				// Verify resource type
				if req.ResourceType != "instance" {
					return false
				}
				// Verify tags exist
				if req.Tag == nil || len(*req.Tag) == 0 {
					return false
				}
				return true
			})).Return(&ecs.TagResourcesResponse{}, nil)

			// Reconcile
			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())

			// Verify mock was called
			mockECSClient.AssertExpectations(GinkgoT())
		})

		It("should include all required Karpenter tags", func() {
			// Create resources
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			var capturedTags map[string]string
			mockECSClient.On("TagResources", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				req := args.Get(1).(*ecs.TagResourcesRequest)
				capturedTags = make(map[string]string)
				for _, tag := range *req.Tag {
					capturedTags[tag.Key] = tag.Value
				}
			}).Return(&ecs.TagResourcesResponse{}, nil)

			// Reconcile
			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())

			// Verify required tags
			Expect(capturedTags).To(HaveKeyWithValue("karpenter.sh/nodeclaim", "test-nodeclaim"))
			Expect(capturedTags).To(HaveKeyWithValue("karpenter.sh/nodepool", "default"))
			Expect(capturedTags).To(HaveKeyWithValue("karpenter.sh/managed-by", "karpenter"))
		})

		It("should include custom tags from NodeClass", func() {
			// Create resources
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			var capturedTags map[string]string
			mockECSClient.On("TagResources", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				req := args.Get(1).(*ecs.TagResourcesRequest)
				capturedTags = make(map[string]string)
				for _, tag := range *req.Tag {
					capturedTags[tag.Key] = tag.Value
				}
			}).Return(&ecs.TagResourcesResponse{}, nil)

			// Reconcile
			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())

			// Verify custom tags from NodeClass
			Expect(capturedTags).To(HaveKeyWithValue("Environment", "test"))
			Expect(capturedTags).To(HaveKeyWithValue("Team", "platform"))
		})

		It("should include labels from NodeClaim", func() {
			// Create resources
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			var capturedTags map[string]string
			mockECSClient.On("TagResources", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				req := args.Get(1).(*ecs.TagResourcesRequest)
				capturedTags = make(map[string]string)
				for _, tag := range *req.Tag {
					capturedTags[tag.Key] = tag.Value
				}
			}).Return(&ecs.TagResourcesResponse{}, nil)

			// Reconcile
			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())

			// Verify labels from NodeClaim
			Expect(capturedTags).To(HaveKeyWithValue("workload-type", "batch"))
			Expect(capturedTags).To(HaveKeyWithValue(coreapis.NodePoolLabelKey, "default"))
		})
	})

	Context("Tag Updates", func() {
		It("should update tags when NodeClass tags change", func() {
			// Create initial resources
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			// First reconcile
			mockECSClient.On("TagResources", mock.Anything, mock.Anything).Return(&ecs.TagResourcesResponse{}, nil).Once()
			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())

			// Update NodeClass tags
			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())
			updated.Spec.Tags = map[string]string{
				"Environment": "production",
				"Team":        "devops",
				"Project":     "karpenter",
			}
			Expect(env.Client.Update(ctx, updated)).To(Succeed())

			// Second reconcile with updated tags
			var capturedTags map[string]string
			mockECSClient.On("TagResources", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				req := args.Get(1).(*ecs.TagResourcesRequest)
				capturedTags = make(map[string]string)
				for _, tag := range *req.Tag {
					capturedTags[tag.Key] = tag.Value
				}
			}).Return(&ecs.TagResourcesResponse{}, nil).Once()

			_, err = taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())

			// Verify updated tags
			Expect(capturedTags).To(HaveKeyWithValue("Environment", "production"))
			Expect(capturedTags).To(HaveKeyWithValue("Team", "devops"))
			Expect(capturedTags).To(HaveKeyWithValue("Project", "karpenter"))
		})

		It("should update tags when NodeClaim labels change", func() {
			// Create initial resources
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			// First reconcile
			mockECSClient.On("TagResources", mock.Anything, mock.Anything).Return(&ecs.TagResourcesResponse{}, nil).Once()
			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())

			// Update NodeClaim labels
			updated := &coreapis.NodeClaim{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClaim), updated)).To(Succeed())
			updated.Labels["new-label"] = "new-value"
			updated.Labels["workload-type"] = "realtime"
			Expect(env.Client.Update(ctx, updated)).To(Succeed())

			// Second reconcile with updated labels
			var capturedTags map[string]string
			mockECSClient.On("TagResources", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				req := args.Get(1).(*ecs.TagResourcesRequest)
				capturedTags = make(map[string]string)
				for _, tag := range *req.Tag {
					capturedTags[tag.Key] = tag.Value
				}
			}).Return(&ecs.TagResourcesResponse{}, nil).Once()

			_, err = taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())

			// Verify updated labels
			Expect(capturedTags).To(HaveKeyWithValue("new-label", "new-value"))
			Expect(capturedTags).To(HaveKeyWithValue("workload-type", "realtime"))
		})
	})

	Context("Provider ID Handling", func() {
		It("should extract instance ID from provider ID with region prefix", func() {
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-abc123xyz"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			mockECSClient.On("TagResources", mock.Anything, mock.MatchedBy(func(req *ecs.TagResourcesRequest) bool {
				return len(*req.ResourceId) == 1 && (*req.ResourceId)[0] == "i-abc123xyz"
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

			mockECSClient.On("TagResources", mock.Anything, mock.MatchedBy(func(req *ecs.TagResourcesRequest) bool {
				return len(*req.ResourceId) == 1 && (*req.ResourceId)[0] == "i-xyz789"
			})).Return(&ecs.TagResourcesResponse{}, nil)

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())
			mockECSClient.AssertExpectations(GinkgoT())
		})

		It("should fail when provider ID is empty", func() {
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = ""
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to extract instance ID"))
		})

		It("should handle provider ID without dots", func() {
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "i-standalone123"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			mockECSClient.On("TagResources", mock.Anything, mock.MatchedBy(func(req *ecs.TagResourcesRequest) bool {
				return len(*req.ResourceId) == 1 && (*req.ResourceId)[0] == "i-standalone123"
			})).Return(&ecs.TagResourcesResponse{}, nil)

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())
			mockECSClient.AssertExpectations(GinkgoT())
		})
	})

	Context("Error Handling", func() {
		It("should return error when NodeClaim does not exist", func() {
			nonExistent := &coreapis.NodeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name: "non-existent",
				},
			}

			// Should not error, just ignore not found
			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nonExistent),
			})
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return error when NodeClass does not exist", func() {
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())

			// Should not error, just ignore not found
			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return error when ECS API fails", func() {
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			mockECSClient.On("TagResources", mock.Anything, mock.Anything).
				Return(nil, fmt.Errorf("SDK.ServerError ErrorCode: %s Message: %s", "InternalError", "Internal Server Error"))

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to tag instance"))
		})

		It("should handle throttling errors gracefully", func() {
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			mockECSClient.On("TagResources", mock.Anything, mock.Anything).
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

			var capturedTags map[string]string
			mockECSClient.On("TagResources", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				req := args.Get(1).(*ecs.TagResourcesRequest)
				capturedTags = make(map[string]string)
				for _, tag := range *req.Tag {
					capturedTags[tag.Key] = tag.Value
				}
			}).Return(&ecs.TagResourcesResponse{}, nil)

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())

			// Should still have Karpenter tags
			Expect(capturedTags).To(HaveKey("karpenter.sh/nodeclaim"))
			Expect(capturedTags).To(HaveKey("karpenter.sh/managed-by"))
		})

		It("should handle NodeClaim without labels", func() {
			nodeClaim.Labels = nil
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			var capturedTags map[string]string
			mockECSClient.On("TagResources", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				req := args.Get(1).(*ecs.TagResourcesRequest)
				capturedTags = make(map[string]string)
				for _, tag := range *req.Tag {
					capturedTags[tag.Key] = tag.Value
				}
			}).Return(&ecs.TagResourcesResponse{}, nil)

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())

			// Should still have Karpenter tags
			Expect(capturedTags).To(HaveKey("karpenter.sh/nodeclaim"))
			Expect(capturedTags).To(HaveKey("karpenter.sh/managed-by"))
		})

		It("should handle empty NodeClass tags", func() {
			nodeClass.Spec.Tags = map[string]string{}
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			mockECSClient.On("TagResources", mock.Anything, mock.Anything).
				Return(&ecs.TagResourcesResponse{}, nil)

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())
			mockECSClient.AssertExpectations(GinkgoT())
		})

		It("should handle tag key conflicts between NodeClass and NodeClaim", func() {
			// Both have "Team" tag with different values
			nodeClass.Spec.Tags = map[string]string{
				"Team": "platform",
			}
			nodeClaim.Labels = map[string]string{
				"Team":                    "devops", // This should override
				coreapis.NodePoolLabelKey: "default",
			}

			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			var capturedTags map[string]string
			mockECSClient.On("TagResources", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				req := args.Get(1).(*ecs.TagResourcesRequest)
				capturedTags = make(map[string]string)
				for _, tag := range *req.Tag {
					capturedTags[tag.Key] = tag.Value
				}
			}).Return(&ecs.TagResourcesResponse{}, nil)

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim),
			})
			Expect(err).ToNot(HaveOccurred())

			// NodeClaim labels should override NodeClass tags
			Expect(capturedTags).To(HaveKeyWithValue("Team", "devops"))
		})
	})

	Context("Multiple NodeClaims", func() {
		It("should tag different instances for different NodeClaims", func() {
			// Create first NodeClaim
			nodeClaim1 := nodeClaim.DeepCopy()
			nodeClaim1.Name = "nodeclaim-1"

			// Create second NodeClaim
			nodeClaim2 := nodeClaim.DeepCopy()
			nodeClaim2.Name = "nodeclaim-2"

			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			Expect(env.Client.Create(ctx, nodeClaim1)).To(Succeed())
			nodeClaim1.Status.ProviderID = "cn-hangzhou.i-instance001"
			Expect(env.Client.Status().Update(ctx, nodeClaim1)).To(Succeed())

			Expect(env.Client.Create(ctx, nodeClaim2)).To(Succeed())
			nodeClaim2.Status.ProviderID = "cn-hangzhou.i-instance002"
			Expect(env.Client.Status().Update(ctx, nodeClaim2)).To(Succeed())

			// First reconcile
			mockECSClient.On("TagResources", mock.Anything, mock.MatchedBy(func(req *ecs.TagResourcesRequest) bool {
				return len(*req.ResourceId) == 1 && (*req.ResourceId)[0] == "i-instance001"
			})).Return(&ecs.TagResourcesResponse{}, nil).Once()

			_, err := taggingController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClaim1),
			})
			Expect(err).ToNot(HaveOccurred())

			// Second reconcile
			mockECSClient.On("TagResources", mock.Anything, mock.MatchedBy(func(req *ecs.TagResourcesRequest) bool {
				return len(*req.ResourceId) == 1 && (*req.ResourceId)[0] == "i-instance002"
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

func (m *MockECSClient) DescribeImages(ctx context.Context, imageIDs []string, filters map[string]string) ([]ecs.Image, error) {
	args := m.Called(ctx, imageIDs, filters)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]ecs.Image), args.Error(1)
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

// Helper functions
func stringPtr(s string) *string {
	return &s
}
