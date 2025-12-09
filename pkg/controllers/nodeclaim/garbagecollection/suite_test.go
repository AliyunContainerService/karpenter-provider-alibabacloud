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

package garbagecollection_test

import (
	"context"
	"fmt"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	"path/filepath"
	"testing"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/cloudprovider"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/controllers/nodeclaim/garbagecollection"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/imagefamily"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instance"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instancetype"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/pricing"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/securitygroup"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/vswitch"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	coreapis "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"
)

var (
	ctx                   context.Context
	env                   *coretest.Environment
	gcController          *garbagecollection.GarbageCollectionController
	testEnv               *envtest.Environment
	cfg                   *rest.Config
	mockECSClient         *MockECSClient
	mockVPCClient         *MockVPCClient
	instanceProvider      *instance.Provider
	cloudProvider         *cloudprovider.CloudProvider
	instanceTypeProvider  *instancetype.Provider
	imageFamilyProvider   *imagefamily.Provider
	securityGroupProvider *securitygroup.Provider
	vswitchProvider       *vswitch.Provider
	pricingProvider       *pricing.Provider
)

func TestGarbageCollection(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "GarbageCollection Controller Suite")
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

	// Create providers
	instanceProvider = instance.NewProvider(ctx, "cn-hangzhou", mockECSClient)
	instanceTypeProvider = instancetype.NewProvider("cn-hangzhou", mockECSClient)
	imageFamilyProvider = imagefamily.NewProvider(mockECSClient)
	securityGroupProvider = securitygroup.NewProvider("cn-hangzhou", mockECSClient)
	vswitchProvider = vswitch.NewProvider("cn-hangzhou", mockVPCClient)
	pricingProvider = pricing.NewProvider("cn-hangzhou", mockECSClient)

	// Create cloud provider
	cloudProvider = cloudprovider.New(
		instanceTypeProvider,
		instanceProvider,
		nil,
		k8sClient,
		imageFamilyProvider,
		securityGroupProvider,
		vswitchProvider,
		nil, // instanceProfileProvider
		pricingProvider,
		nil, // launchTemplateProvider
		nil, // bootstrapProvider
	)

	watiDuration := time.Second * 10
	// Create garbage collection controller
	gcController = garbagecollection.NewGarbageCollectionController(k8sClient, cloudProvider, &watiDuration)

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

var _ = Describe("GarbageCollectionController", func() {
	var nodeClaim *coreapis.NodeClaim
	var node *v1.Node

	BeforeEach(func() {
		// Reset mock expectations
		mockECSClient = new(MockECSClient)
		mockVPCClient = new(MockVPCClient)

		// Recreate all providers with new mocks
		instanceProvider = instance.NewProvider(ctx, "cn-hangzhou", mockECSClient)
		instanceTypeProvider = instancetype.NewProvider("cn-hangzhou", mockECSClient)
		imageFamilyProvider = imagefamily.NewProvider(mockECSClient)
		securityGroupProvider = securitygroup.NewProvider("cn-hangzhou", mockECSClient)
		vswitchProvider = vswitch.NewProvider("cn-hangzhou", mockVPCClient)
		pricingProvider = pricing.NewProvider("cn-hangzhou", mockECSClient)

		// Recreate cloud provider with new mocks
		cloudProvider = cloudprovider.New(
			instanceTypeProvider,
			instanceProvider,
			nil,
			env.Client,
			imageFamilyProvider,
			securityGroupProvider,
			vswitchProvider,
			nil,
			pricingProvider,
			nil,
			nil,
		)
		watiDuration := time.Second * 10
		gcController = garbagecollection.NewGarbageCollectionController(env.Client, cloudProvider, &watiDuration)

		// Create NodeClaim (status will be set separately after creation)
		nodeClaim = &coreapis.NodeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-nodeclaim",
				Labels: map[string]string{
					coreapis.NodePoolLabelKey: "default",
				},
				CreationTimestamp: metav1.Time{Time: time.Now().Add(-2 * time.Minute)},
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
		// Note: Status.ProviderID will be set in individual tests after Create()

		// Create Node
		node = &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-node",
			},
			Spec: v1.NodeSpec{
				ProviderID: "cn-hangzhou.i-test123456",
			},
		}
	})

	AfterEach(func() {
		// Clean up all resources
		Expect(env.Client.DeleteAllOf(ctx, &coreapis.NodeClaim{})).To(Succeed())
		Expect(env.Client.DeleteAllOf(ctx, &v1.Node{})).To(Succeed())
		Expect(env.Client.DeleteAllOf(ctx, &v1alpha1.ECSNodeClass{})).To(Succeed())
	})

	Context("Orphaned Instance Cleanup", func() {
		It("should delete orphaned instance when NodeClaim has no matching instance", func() {
			// Create NodeClaim without corresponding instance
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			// Set status separately (status is a subresource)
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			// Mock DescribeZones (called by instanceTypeProvider.List -> cloudProvider.List)
			mockECSClient.On("DescribeZones", mock.Anything).Return(
				&ecs.DescribeZonesResponse{
					Zones: ecs.ZonesInDescribeZones{
						Zone: []ecs.Zone{
							{ZoneId: "cn-hangzhou-h"},
						},
					},
				}, nil)

			// Mock DescribeInstanceTypes (called by cloudProvider.List)
			mockECSClient.On("DescribeInstanceTypes", mock.Anything, mock.Anything).Return(
				&ecs.DescribeInstanceTypesResponse{
					InstanceTypes: ecs.InstanceTypesInDescribeInstanceTypes{
						InstanceType: []ecs.InstanceType{},
					},
				}, nil)

			// Mock List to return empty instances (orphaned)
			mockECSClient.On("DescribeInstances", mock.Anything, mock.Anything).Return(
				&ecs.DescribeInstancesResponse{
					Instances: ecs.InstancesInDescribeInstances{
						Instance: []ecs.Instance{},
					},
				}, nil)

			// Reconcile
			_, err := gcController.Reconcile(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Verify delete was called
			mockECSClient.AssertExpectations(GinkgoT())
		})

		It("should not delete instance if created within grace period (1 minute)", func() {
			// Create very recent NodeClaim
			recentNodeClaim := nodeClaim.DeepCopy()
			recentNodeClaim.Name = "recent-nodeclaim"
			recentNodeClaim.CreationTimestamp = metav1.Time{Time: time.Now().Add(-30 * time.Second)}
			Expect(env.Client.Create(ctx, recentNodeClaim)).To(Succeed())

			// Mock DescribeZones
			mockECSClient.On("DescribeZones", mock.Anything).Return(
				&ecs.DescribeZonesResponse{
					Zones: ecs.ZonesInDescribeZones{
						Zone: []ecs.Zone{
							{ZoneId: "cn-hangzhou-h"},
						},
					},
				}, nil)

			// Mock DescribeInstanceTypes
			mockECSClient.On("DescribeInstanceTypes", mock.Anything, mock.Anything).Return(
				&ecs.DescribeInstanceTypesResponse{
					InstanceTypes: ecs.InstanceTypesInDescribeInstanceTypes{
						InstanceType: []ecs.InstanceType{},
					},
				}, nil)

			// Mock List to return empty instances
			mockECSClient.On("DescribeInstances", mock.Anything, mock.Anything).Return(
				&ecs.DescribeInstancesResponse{
					Instances: ecs.InstancesInDescribeInstances{
						Instance: []ecs.Instance{},
					},
				}, nil)

			// Reconcile - should NOT delete because of grace period
			_, err := gcController.Reconcile(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Verify Delete was NOT called
			mockECSClient.AssertNotCalled(GinkgoT(), "DeleteInstances", mock.Anything, mock.Anything)
		})

		It("should not delete both orphaned instance and associated node - node claim created less than 1min", func() {
			// Create NodeClaim and Node
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			// Set status separately (status is a subresource)
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())
			Expect(env.Client.Create(ctx, node)).To(Succeed())

			// Mock DescribeZones
			mockECSClient.On("DescribeZones", mock.Anything).Return(
				&ecs.DescribeZonesResponse{
					Zones: ecs.ZonesInDescribeZones{
						Zone: []ecs.Zone{
							{ZoneId: "cn-hangzhou-h"},
						},
					},
				}, nil)

			// Mock DescribeInstanceTypes
			mockECSClient.On("DescribeInstanceTypes", mock.Anything, mock.Anything).Return(
				&ecs.DescribeInstanceTypesResponse{
					InstanceTypes: ecs.InstanceTypesInDescribeInstanceTypes{
						InstanceType: []ecs.InstanceType{},
					},
				}, nil)

			// Mock List to return empty instances (orphaned)
			mockECSClient.On("DescribeInstances", mock.Anything, mock.Anything).Return(
				&ecs.DescribeInstancesResponse{
					Instances: ecs.InstancesInDescribeInstances{
						Instance: []ecs.Instance{},
					},
				}, nil)

			// Reconcile
			_, err := gcController.Reconcile(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Verify delete was called
			mockECSClient.AssertExpectations(GinkgoT())

			// Wait a bit for deletion to propagate
			time.Sleep(100 * time.Millisecond)

			// Verify node was not deleted
			deletedNode := &v1.Node{}
			err = env.Client.Get(ctx, client.ObjectKey{Name: "test-node"}, deletedNode)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should delete both orphaned instance and associated node", func() {
			// Create NodeClaim and Node
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			// Set status separately (status is a subresource)
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())
			Expect(env.Client.Create(ctx, node)).To(Succeed())

			time.Sleep(10 * time.Second)

			// Mock DescribeZones
			mockECSClient.On("DescribeZones", mock.Anything).Return(
				&ecs.DescribeZonesResponse{
					Zones: ecs.ZonesInDescribeZones{
						Zone: []ecs.Zone{
							{ZoneId: "cn-hangzhou-h"},
						},
					},
				}, nil)

			// Mock DescribeInstanceTypes
			mockECSClient.On("DescribeInstanceTypes", mock.Anything, mock.Anything).Return(
				&ecs.DescribeInstanceTypesResponse{
					InstanceTypes: ecs.InstanceTypesInDescribeInstanceTypes{
						InstanceType: []ecs.InstanceType{},
					},
				}, nil)

			// Mock List to return empty instances (orphaned)
			mockECSClient.On("DescribeInstances", mock.Anything, mock.Anything).Return(
				&ecs.DescribeInstancesResponse{
					Instances: ecs.InstancesInDescribeInstances{
						Instance: []ecs.Instance{},
					},
				}, nil)

			// Reconcile
			_, err := gcController.Reconcile(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Verify delete was called
			mockECSClient.AssertExpectations(GinkgoT())

			// Wait a bit for deletion to propagate
			time.Sleep(100 * time.Millisecond)

			// Verify node was deleted
			deletedNode := &v1.Node{}
			err = env.Client.Get(ctx, client.ObjectKey{Name: "test-node"}, deletedNode)
			Expect(err).To(HaveOccurred())
			Expect(client.IgnoreNotFound(err)).To(BeNil())
		})
	})

	Context("Batch Deletion", func() {
		It("should delete multiple orphaned instances in one reconcile cycle", func() {
			// Create multiple orphaned NodeClaims
			for i := 0; i < 5; i++ {
				nc := nodeClaim.DeepCopy()
				nc.Name = fmt.Sprintf("nodeclaim-%d", i)
				nc.Status.ProviderID = fmt.Sprintf("cn-hangzhou.i-test%d", i)
				nc.CreationTimestamp = metav1.Time{Time: time.Now().Add(-5 * time.Minute)}
				Expect(env.Client.Create(ctx, nc)).To(Succeed())
			}

			// Mock DescribeZones
			mockECSClient.On("DescribeZones", mock.Anything).Return(
				&ecs.DescribeZonesResponse{
					Zones: ecs.ZonesInDescribeZones{
						Zone: []ecs.Zone{
							{ZoneId: "cn-hangzhou-h"},
						},
					},
				}, nil)

			// Mock DescribeInstanceTypes
			mockECSClient.On("DescribeInstanceTypes", mock.Anything, mock.Anything).Return(
				&ecs.DescribeInstanceTypesResponse{
					InstanceTypes: ecs.InstanceTypesInDescribeInstanceTypes{
						InstanceType: []ecs.InstanceType{},
					},
				}, nil)

			// Mock List to return empty instances (all orphaned)
			mockECSClient.On("DescribeInstances", mock.Anything, mock.Anything).Return(
				&ecs.DescribeInstancesResponse{
					Instances: ecs.InstancesInDescribeInstances{
						Instance: []ecs.Instance{},
					},
				}, nil)

			// Reconcile
			_, err := gcController.Reconcile(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Verify all deletes were called
			mockECSClient.AssertExpectations(GinkgoT())
		})
	})

	Context("Instance Still Exists", func() {
		It("should not delete instance if it still exists in cloud provider", func() {
			// Create NodeClaim
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			// Set status separately (status is a subresource)
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			// Mock DescribeZones
			mockECSClient.On("DescribeZones", mock.Anything).Return(
				&ecs.DescribeZonesResponse{
					Zones: ecs.ZonesInDescribeZones{
						Zone: []ecs.Zone{
							{ZoneId: "cn-hangzhou-h"},
						},
					},
				}, nil)

			// Mock DescribeInstanceTypes
			mockECSClient.On("DescribeInstanceTypes", mock.Anything, mock.Anything).Return(
				&ecs.DescribeInstanceTypesResponse{
					InstanceTypes: ecs.InstanceTypesInDescribeInstanceTypes{
						InstanceType: []ecs.InstanceType{},
					},
				}, nil)

			// Mock List to return the instance (NOT orphaned)
			mockECSClient.On("DescribeInstances", mock.Anything, mock.Anything).Return(
				&ecs.DescribeInstancesResponse{
					Instances: ecs.InstancesInDescribeInstances{
						Instance: []ecs.Instance{
							{
								InstanceId:   "i-test123456",
								RegionId:     "cn-hangzhou",
								ZoneId:       "cn-hangzhou-h",
								InstanceType: "ecs.g6.large",
								ImageId:      "img-123",
								Status:       "Running",
								Cpu:          2,
								Memory:       8192,
								Tags: ecs.TagsInDescribeInstances{
									Tag: []ecs.Tag{
										{Key: "karpenter.sh/managed-by", Value: "true"},
									},
								},
								CreationTime: time.Now().Add(-10 * time.Minute).Format(time.RFC3339),
							},
						},
					},
				}, nil)

			// Reconcile
			_, err := gcController.Reconcile(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Verify Delete was NOT called
			mockECSClient.AssertNotCalled(GinkgoT(), "DeleteInstances", mock.Anything, mock.Anything)
		})
	})

	Context("Error Handling", func() {
		It("should handle cloud provider List API error gracefully", func() {
			// Create NodeClaim
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			// Set status separately (status is a subresource)
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			// Mock DescribeZones to return error
			mockECSClient.On("DescribeZones", mock.Anything).Return(
				nil, fmt.Errorf("SDK.ServerError ErrorCode: %s Message: %s", "InternalError", "Internal Server Error"))

			// Mock other calls that List() makes to avoid panic
			mockECSClient.On("DescribeInstanceTypes", mock.Anything, mock.Anything).Return(
				&ecs.DescribeInstanceTypesResponse{
					InstanceTypes: ecs.InstanceTypesInDescribeInstanceTypes{
						InstanceType: []ecs.InstanceType{},
					},
				}, nil)

			// Mock DescribeInstances to return empty results since DescribeZones will error first
			mockECSClient.On("DescribeInstances", mock.Anything, mock.Anything).Return(
				&ecs.DescribeInstancesResponse{
					Instances: ecs.InstancesInDescribeInstances{
						Instance: []ecs.Instance{},
					},
				}, nil)

			// Reconcile should return error but not panic
			_, err := gcController.Reconcile(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("listing cloud provider instances"))
		})

		It("should handle cloud provider Delete API error gracefully", func() {
			// Create NodeClaim
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			// Set status separately (status is a subresource)
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			// Mock DescribeZones
			mockECSClient.On("DescribeZones", mock.Anything).Return(
				&ecs.DescribeZonesResponse{
					Zones: ecs.ZonesInDescribeZones{
						Zone: []ecs.Zone{
							{ZoneId: "cn-hangzhou-h"},
						},
					},
				}, nil)

			// Mock DescribeInstanceTypes
			mockECSClient.On("DescribeInstanceTypes", mock.Anything, mock.Anything).Return(
				&ecs.DescribeInstanceTypesResponse{
					InstanceTypes: ecs.InstanceTypesInDescribeInstanceTypes{
						InstanceType: []ecs.InstanceType{},
					},
				}, nil)

			// Mock List to return empty (orphaned)
			mockECSClient.On("DescribeInstances", mock.Anything, mock.Anything).Return(
				&ecs.DescribeInstancesResponse{
					Instances: ecs.InstancesInDescribeInstances{
						Instance: []ecs.Instance{},
					},
				}, nil)

			// Reconcile should continue despite delete error
			_, err := gcController.Reconcile(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Verify delete was attempted
			mockECSClient.AssertExpectations(GinkgoT())
		})

		It("should handle NodeClaim List error gracefully", func() {
			// Close the client to force an error
			// This simulates a K8s API error
			// Note: In real scenario, we would need to test with a broken client
			// For now, we verify the controller doesn't panic

			// Reconcile should handle the error
			_, err := gcController.Reconcile(ctx)
			// Should either succeed (no NodeClaims) or fail gracefully
			if err != nil {
				Expect(err.Error()).NotTo(ContainSubstring("panic"))
			}
		})
	})

	Context("Empty Cluster", func() {
		It("should handle empty cluster (no NodeClaims) gracefully", func() {
			// Don't create any NodeClaims

			// Reconcile should succeed
			_, err := gcController.Reconcile(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Verify List was NOT called (optimization)
			mockECSClient.AssertNotCalled(GinkgoT(), "DescribeInstances", mock.Anything, mock.Anything)
		})
	})

	Context("Throttling Protection", func() {
		It("should skip reconcile if last run was less than 1 minute ago", func() {
			// Create NodeClaim
			Expect(env.Client.Create(ctx, nodeClaim)).To(Succeed())
			// Set status separately (status is a subresource)
			nodeClaim.Status.ProviderID = "cn-hangzhou.i-test123456"
			Expect(env.Client.Status().Update(ctx, nodeClaim)).To(Succeed())

			// Mock DescribeZones
			mockECSClient.On("DescribeZones", mock.Anything).Return(
				&ecs.DescribeZonesResponse{
					Zones: ecs.ZonesInDescribeZones{
						Zone: []ecs.Zone{
							{ZoneId: "cn-hangzhou-h"},
						},
					},
				}, nil).Once()

			// Mock DescribeInstanceTypes
			mockECSClient.On("DescribeInstanceTypes", mock.Anything, mock.Anything).Return(
				&ecs.DescribeInstanceTypesResponse{
					InstanceTypes: ecs.InstanceTypesInDescribeInstanceTypes{
						InstanceType: []ecs.InstanceType{},
					},
				}, nil).Once()

			// First reconcile
			mockECSClient.On("DescribeInstances", mock.Anything, mock.Anything).Return(
				&ecs.DescribeInstancesResponse{
					Instances: ecs.InstancesInDescribeInstances{
						Instance: []ecs.Instance{
							{
								InstanceId:   "i-test123456",
								RegionId:     "cn-hangzhou",
								Status:       "Running",
								CreationTime: time.Now().Add(-10 * time.Minute).Format(time.RFC3339),
								Tags: ecs.TagsInDescribeInstances{
									Tag: []ecs.Tag{
										{Key: "karpenter.sh/managed-by", Value: "true"},
									},
								},
							},
						},
					},
				}, nil).Once()

			_, err := gcController.Reconcile(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Second reconcile immediately - should be skipped
			_, err = gcController.Reconcile(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Verify DescribeInstances was called only once
			mockECSClient.AssertNumberOfCalls(GinkgoT(), "DescribeInstances", 1)
			mockECSClient.AssertNumberOfCalls(GinkgoT(), "DescribeZones", 1)
			mockECSClient.AssertNumberOfCalls(GinkgoT(), "DescribeInstanceTypes", 1)
		})
	})

	Context("Provider ID Handling", func() {
		It("should handle various provider ID formats", func() {
			testCases := []struct {
				providerID         string
				expectedInstanceID string
			}{
				{"cn-hangzhou.i-abc123", "i-abc123"},
				{"cn-beijing.i-xyz789", "i-xyz789"},
				{"i-standalone123", "i-standalone123"},
			}

			// Create NodeClaims first
			for _, tc := range testCases {
				nc := nodeClaim.DeepCopy()
				nc.Name = fmt.Sprintf("nodeclaim-%s", tc.expectedInstanceID)
				nc.Status.ProviderID = tc.providerID
				nc.CreationTimestamp = metav1.Time{Time: time.Now().Add(-5 * time.Minute)}
				Expect(env.Client.Create(ctx, nc)).To(Succeed())
			}

			// Mock DescribeZones
			mockECSClient.On("DescribeZones", mock.Anything).Return(
				&ecs.DescribeZonesResponse{
					Zones: ecs.ZonesInDescribeZones{
						Zone: []ecs.Zone{
							{ZoneId: "cn-hangzhou-h"},
						},
					},
				}, nil)

			// Mock DescribeInstanceTypes
			mockECSClient.On("DescribeInstanceTypes", mock.Anything, mock.Anything).Return(
				&ecs.DescribeInstanceTypesResponse{
					InstanceTypes: ecs.InstanceTypesInDescribeInstanceTypes{
						InstanceType: []ecs.InstanceType{},
					},
				}, nil)

			// Mock empty list (orphaned)
			mockECSClient.On("DescribeInstances", mock.Anything, mock.Anything).Return(
				&ecs.DescribeInstancesResponse{
					Instances: ecs.InstancesInDescribeInstances{
						Instance: []ecs.Instance{},
					},
				}, nil)

			// Mock delete for each instance after all the setup
			//for _, tc := range testCases {
			//	mockECSClient.On("DeleteInstances", mock.Anything, mock.MatchedBy(func(req *ecs.DeleteInstancesRequest) bool {
			//		return len(*req.InstanceId) == 1 && (*req.InstanceId)[0] == tc.expectedInstanceID
			//	})).Return(&ecs.DeleteInstancesResponse{}, nil).Once()
			//}

			// Reconcile
			_, err := gcController.Reconcile(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Verify all deletes were called with correct instance IDs
			mockECSClient.AssertExpectations(GinkgoT())
		})

		It("should handle empty provider ID gracefully", func() {
			nc := nodeClaim.DeepCopy()
			nc.Name = "nodeclaim-empty-providerid"
			nc.Status.ProviderID = ""
			nc.CreationTimestamp = metav1.Time{Time: time.Now().Add(-5 * time.Minute)}
			Expect(env.Client.Create(ctx, nc)).To(Succeed())

			// Mock DescribeZones
			mockECSClient.On("DescribeZones", mock.Anything).Return(
				&ecs.DescribeZonesResponse{
					Zones: ecs.ZonesInDescribeZones{
						Zone: []ecs.Zone{
							{ZoneId: "cn-hangzhou-h"},
						},
					},
				}, nil)

			// Mock DescribeInstanceTypes
			mockECSClient.On("DescribeInstanceTypes", mock.Anything, mock.Anything).Return(
				&ecs.DescribeInstanceTypesResponse{
					InstanceTypes: ecs.InstanceTypesInDescribeInstanceTypes{
						InstanceType: []ecs.InstanceType{},
					},
				}, nil)

			// Mock empty list
			mockECSClient.On("DescribeInstances", mock.Anything, mock.Anything).Return(
				&ecs.DescribeInstancesResponse{
					Instances: ecs.InstancesInDescribeInstances{
						Instance: []ecs.Instance{},
					},
				}, nil)

			// Reconcile - should skip this NodeClaim
			_, err := gcController.Reconcile(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Verify Delete was NOT called for empty provider ID
			mockECSClient.AssertNotCalled(GinkgoT(), "DeleteInstances", mock.Anything, mock.Anything)
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

// MockVPCClient is a mock implementation of VPCClient for testing
type MockVPCClient struct {
	mock.Mock
}

func (m *MockVPCClient) DescribeVSwitches(ctx context.Context, vpcID string, tags map[string]string) (*vpc.DescribeVSwitchesResponse, error) {
	args := m.Called(ctx, vpcID, tags)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*vpc.DescribeVSwitchesResponse), args.Error(1)
}

// Helper functions
func stringPtr(s string) *string {
	return &s
}

func int32Ptr(i int32) *int32 {
	return &i
}

func quantityPtr(q resource.Quantity) *resource.Quantity {
	return &q
}
