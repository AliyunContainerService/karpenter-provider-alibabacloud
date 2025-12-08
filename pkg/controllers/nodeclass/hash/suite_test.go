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

package hash_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/controllers/nodeclass/hash"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	coretest "sigs.k8s.io/karpenter/pkg/test"
)

var (
	ctx            context.Context
	env            *coretest.Environment
	hashController *hash.Controller
	testEnv        *envtest.Environment
	cfg            *rest.Config
)

func TestHash(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Hash Controller Suite")
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

	hashController = hash.NewController(k8sClient)

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

var _ = Describe("HashController", func() {
	var nodeClass *v1alpha1.ECSNodeClass

	BeforeEach(func() {
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
			},
		}
	})

	AfterEach(func() {
		// Clean up all ECSNodeClass resources
		Expect(env.Client.DeleteAllOf(ctx, &v1alpha1.ECSNodeClass{})).To(Succeed())
	})

	Context("Hash Computation", func() {
		It("should add hash annotation when ECSNodeClass is created", func() {
			// Create the ECSNodeClass
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())

			// Reconcile the controller
			_, err := hashController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClass),
			})
			Expect(err).ToNot(HaveOccurred())

			// Get the updated nodeClass
			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())

			// Verify hash annotation exists
			Expect(updated.Annotations).To(HaveKey(v1alpha1.AnnotationECSNodeClassHash))
			Expect(updated.Annotations[v1alpha1.AnnotationECSNodeClassHash]).ToNot(BeEmpty())
		})

		It("should compute same hash for identical specs", func() {
			// Create first nodeClass
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			_, err := hashController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClass),
			})
			Expect(err).ToNot(HaveOccurred())

			// Get the hash
			updated1 := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated1)).To(Succeed())
			hash1 := updated1.Annotations[v1alpha1.AnnotationECSNodeClassHash]

			// Create second identical nodeClass
			nodeClass2 := &v1alpha1.ECSNodeClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-nodeclass-2",
				},
				Spec: nodeClass.Spec,
			}
			Expect(env.Client.Create(ctx, nodeClass2)).To(Succeed())
			_, err = hashController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClass2),
			})
			Expect(err).ToNot(HaveOccurred())

			// Get the hash
			updated2 := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass2), updated2)).To(Succeed())
			hash2 := updated2.Annotations[v1alpha1.AnnotationECSNodeClassHash]

			// Hashes should be identical
			Expect(hash1).To(Equal(hash2))
		})

		It("should update hash when reconciling existing ECSNodeClass", func() {
			// Create with initial annotations
			nodeClass.Annotations = map[string]string{
				"custom-annotation": "custom-value",
			}
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())

			// First reconcile
			_, err := hashController.Reconcile(ctx, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(nodeClass),
			})
			Expect(err).ToNot(HaveOccurred())

			// Verify hash was added
			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())
			Expect(updated.Annotations).To(HaveKey(v1alpha1.AnnotationECSNodeClassHash))
			Expect(updated.Annotations).To(HaveKey("custom-annotation"))
		})
	})

	Context("Static Field Changes", func() {
		It("should update hash when VSwitchSelectorTerms change", func() {
			// Create and get initial hash
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			_, err := hashController.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(nodeClass)})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())
			originalHash := updated.Annotations[v1alpha1.AnnotationECSNodeClassHash]

			// Update VSwitchSelectorTerms
			updated.Spec.VSwitchSelectorTerms = []v1alpha1.VSwitchSelectorTerm{
				{
					ID: stringPtr("vsw-new-456"),
				},
			}
			Expect(env.Client.Update(ctx, updated)).To(Succeed())

			// Reconcile again
			_, err = hashController.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(updated)})
			Expect(err).ToNot(HaveOccurred())

			// Get new hash
			final := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), final)).To(Succeed())
			newHash := final.Annotations[v1alpha1.AnnotationECSNodeClassHash]

			// Hash should be different
			Expect(newHash).ToNot(Equal(originalHash))
		})

		It("should update hash when SecurityGroupSelectorTerms change", func() {
			// Create and get initial hash
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			_, err := hashController.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(nodeClass)})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())
			originalHash := updated.Annotations[v1alpha1.AnnotationECSNodeClassHash]

			// Update SecurityGroupSelectorTerms
			updated.Spec.SecurityGroupSelectorTerms = []v1alpha1.SecurityGroupSelectorTerm{
				{
					ID: stringPtr("sg-new-456"),
				},
			}
			Expect(env.Client.Update(ctx, updated)).To(Succeed())

			// Reconcile again
			_, err = hashController.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(updated)})
			Expect(err).ToNot(HaveOccurred())

			// Get new hash
			final := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), final)).To(Succeed())
			newHash := final.Annotations[v1alpha1.AnnotationECSNodeClassHash]

			// Hash should be different
			Expect(newHash).ToNot(Equal(originalHash))
		})

		It("should update hash when ImageSelectorTerms change", func() {
			// Create and get initial hash
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			_, err := hashController.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(nodeClass)})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())
			originalHash := updated.Annotations[v1alpha1.AnnotationECSNodeClassHash]

			// Update ImageSelectorTerms
			updated.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{
				{
					ID: stringPtr("aliyun_3_x64_20G_alibase_20241231.vhd"),
				},
			}
			Expect(env.Client.Update(ctx, updated)).To(Succeed())

			// Reconcile again
			_, err = hashController.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(updated)})
			Expect(err).ToNot(HaveOccurred())

			// Get new hash
			final := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), final)).To(Succeed())
			newHash := final.Annotations[v1alpha1.AnnotationECSNodeClassHash]

			// Hash should be different
			Expect(newHash).ToNot(Equal(originalHash))
		})

		It("should update hash when Role changes", func() {
			// Create and get initial hash
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			_, err := hashController.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(nodeClass)})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())
			originalHash := updated.Annotations[v1alpha1.AnnotationECSNodeClassHash]

			// Update Role
			updated.Spec.Role = stringPtr("KarpenterNodeRole")
			Expect(env.Client.Update(ctx, updated)).To(Succeed())

			// Reconcile again
			_, err = hashController.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(updated)})
			Expect(err).ToNot(HaveOccurred())

			// Get new hash
			final := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), final)).To(Succeed())
			newHash := final.Annotations[v1alpha1.AnnotationECSNodeClassHash]

			// Hash should be different
			Expect(newHash).ToNot(Equal(originalHash))
		})

		It("should handle multiple selector terms in VSwitchSelectorTerms", func() {
			// Create with multiple VSwitch terms
			nodeClass.Spec.VSwitchSelectorTerms = []v1alpha1.VSwitchSelectorTerm{
				{ID: stringPtr("vsw-1")},
				{ID: stringPtr("vsw-2")},
				{ID: stringPtr("vsw-3")},
			}
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			_, err := hashController.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(nodeClass)})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())
			originalHash := updated.Annotations[v1alpha1.AnnotationECSNodeClassHash]

			// Change order (hash should still be consistent due to sorting)
			updated.Spec.VSwitchSelectorTerms = []v1alpha1.VSwitchSelectorTerm{
				{ID: stringPtr("vsw-3")},
				{ID: stringPtr("vsw-1")},
				{ID: stringPtr("vsw-2")},
			}
			Expect(env.Client.Update(ctx, updated)).To(Succeed())
			_, err = hashController.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(updated)})
			Expect(err).ToNot(HaveOccurred())

			final := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), final)).To(Succeed())
			newHash := final.Annotations[v1alpha1.AnnotationECSNodeClassHash]

			// Hash should be same (sorted internally)
			Expect(newHash).To(Equal(originalHash))
		})
	})

	Context("Dynamic Field Changes", func() {
		It("should NOT update hash when Tags change", func() {
			// Create and get initial hash
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			_, err := hashController.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(nodeClass)})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())
			originalHash := updated.Annotations[v1alpha1.AnnotationECSNodeClassHash]

			// Update Tags (not included in hash)
			updated.Spec.Tags = map[string]string{
				"new-tag": "new-value",
			}
			Expect(env.Client.Update(ctx, updated)).To(Succeed())

			// Reconcile again
			_, err = hashController.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(updated)})
			Expect(err).ToNot(HaveOccurred())

			// Get new hash
			final := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), final)).To(Succeed())
			newHash := final.Annotations[v1alpha1.AnnotationECSNodeClassHash]

			// Hash should be same (Tags not in hash calculation)
			Expect(newHash).To(Equal(originalHash))
		})

		It("should NOT update hash when UserData changes", func() {
			// Create and get initial hash
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			_, err := hashController.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(nodeClass)})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())
			originalHash := updated.Annotations[v1alpha1.AnnotationECSNodeClassHash]

			// Update UserData (not included in current hash implementation)
			updated.Spec.UserData = stringPtr("#!/bin/bash\necho 'new userdata'")
			Expect(env.Client.Update(ctx, updated)).To(Succeed())

			// Reconcile again
			_, err = hashController.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(updated)})
			Expect(err).ToNot(HaveOccurred())

			// Get new hash
			final := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), final)).To(Succeed())
			newHash := final.Annotations[v1alpha1.AnnotationECSNodeClassHash]

			// Hash should be same
			Expect(newHash).To(Equal(originalHash))
		})

		It("should NOT update hash when SystemDisk changes", func() {
			// Create and get initial hash
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			_, err := hashController.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(nodeClass)})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())
			originalHash := updated.Annotations[v1alpha1.AnnotationECSNodeClassHash]

			// Update SystemDisk
			updated.Spec.SystemDisk = &v1alpha1.SystemDiskSpec{
				Category: "cloud_essd",
				Size:     int32Ptr(100),
			}
			Expect(env.Client.Update(ctx, updated)).To(Succeed())

			// Reconcile again
			_, err = hashController.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(updated)})
			Expect(err).ToNot(HaveOccurred())

			// Get new hash
			final := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), final)).To(Succeed())
			newHash := final.Annotations[v1alpha1.AnnotationECSNodeClassHash]

			// Hash should be same
			Expect(newHash).To(Equal(originalHash))
		})

		It("should NOT update hash when ClusterName changes", func() {
			// Create and get initial hash
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			_, err := hashController.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(nodeClass)})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())
			originalHash := updated.Annotations[v1alpha1.AnnotationECSNodeClassHash]

			// Update ClusterName
			updated.Spec.ClusterName = "new-cluster-name"
			Expect(env.Client.Update(ctx, updated)).To(Succeed())

			// Reconcile again
			_, err = hashController.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(updated)})
			Expect(err).ToNot(HaveOccurred())

			// Get new hash
			final := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), final)).To(Succeed())
			newHash := final.Annotations[v1alpha1.AnnotationECSNodeClassHash]

			// Hash should be same
			Expect(newHash).To(Equal(originalHash))
		})
	})

	Context("Edge Cases", func() {
		It("should handle ECSNodeClass without any selector terms", func() {
			// Create minimal nodeClass
			minimalNodeClass := &v1alpha1.ECSNodeClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "minimal-nodeclass",
				},
				Spec: v1alpha1.ECSNodeClassSpec{
					VSwitchSelectorTerms: []v1alpha1.VSwitchSelectorTerm{
						{Tags: map[string]string{"key": "value"}},
					},
					SecurityGroupSelectorTerms: []v1alpha1.SecurityGroupSelectorTerm{
						{Tags: map[string]string{"key": "value"}},
					},
					ImageSelectorTerms: []v1alpha1.ImageSelectorTerm{
						{Tags: map[string]string{"key": "value"}},
					},
				},
			}

			Expect(env.Client.Create(ctx, minimalNodeClass)).To(Succeed())
			_, err := hashController.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(minimalNodeClass)})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(minimalNodeClass), updated)).To(Succeed())
			Expect(updated.Annotations).To(HaveKey(v1alpha1.AnnotationECSNodeClassHash))
		})

		It("should handle reconciliation of non-existent ECSNodeClass", func() {
			nonExistent := &v1alpha1.ECSNodeClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "non-existent",
					Namespace: "",
				},
			}

			// Should not error, just ignore not found
			_, err := hashController.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(nonExistent)})
			Expect(err).ToNot(HaveOccurred())
		})

		It("should preserve existing annotations when adding hash", func() {
			// Create with existing annotations
			nodeClass.Annotations = map[string]string{
				"custom-annotation-1": "value-1",
				"custom-annotation-2": "value-2",
			}
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())

			// Reconcile
			_, err := hashController.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(nodeClass)})
			Expect(err).ToNot(HaveOccurred())

			// Verify all annotations preserved
			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())
			Expect(updated.Annotations).To(HaveKey(v1alpha1.AnnotationECSNodeClassHash))
			Expect(updated.Annotations).To(HaveKeyWithValue("custom-annotation-1", "value-1"))
			Expect(updated.Annotations).To(HaveKeyWithValue("custom-annotation-2", "value-2"))
		})

		It("should handle concurrent reconciliations", func() {
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())

			// Simulate concurrent reconciliations
			done := make(chan bool, 3)
			for i := 0; i < 3; i++ {
				go func() {
					_, err := hashController.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(nodeClass)})
					Expect(err).ToNot(HaveOccurred())
					done <- true
				}()
			}

			// Wait for all to complete
			for i := 0; i < 3; i++ {
				<-done
			}

			// Verify hash exists
			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())
			Expect(updated.Annotations).To(HaveKey(v1alpha1.AnnotationECSNodeClassHash))
		})

		It("should handle nil selector term IDs", func() {
			nodeClass.Spec.VSwitchSelectorTerms = []v1alpha1.VSwitchSelectorTerm{
				{ID: nil, Tags: map[string]string{"env": "test"}},
			}
			nodeClass.Spec.SecurityGroupSelectorTerms = []v1alpha1.SecurityGroupSelectorTerm{
				{ID: nil, Tags: map[string]string{"env": "test"}},
			}
			nodeClass.Spec.ImageSelectorTerms = []v1alpha1.ImageSelectorTerm{
				{ID: nil, Name: stringPtr("aliyun-*")},
			}

			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			_, err := hashController.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(nodeClass)})
			Expect(err).ToNot(HaveOccurred())

			updated := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated)).To(Succeed())
			Expect(updated.Annotations).To(HaveKey(v1alpha1.AnnotationECSNodeClassHash))
		})
	})

	Context("Hash Consistency", func() {
		It("should produce consistent hashes across multiple reconciliations", func() {
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())

			// First reconciliation
			_, err := hashController.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(nodeClass)})
			Expect(err).ToNot(HaveOccurred())

			updated1 := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated1)).To(Succeed())
			hash1 := updated1.Annotations[v1alpha1.AnnotationECSNodeClassHash]

			// Second reconciliation without changes
			_, err = hashController.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(nodeClass)})
			Expect(err).ToNot(HaveOccurred())

			updated2 := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated2)).To(Succeed())
			hash2 := updated2.Annotations[v1alpha1.AnnotationECSNodeClassHash]

			// Hashes should be identical
			Expect(hash1).To(Equal(hash2))
		})

		It("should produce different hashes for different configurations", func() {
			// Create first configuration
			Expect(env.Client.Create(ctx, nodeClass)).To(Succeed())
			_, err := hashController.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(nodeClass)})
			Expect(err).ToNot(HaveOccurred())

			updated1 := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass), updated1)).To(Succeed())
			hash1 := updated1.Annotations[v1alpha1.AnnotationECSNodeClassHash]

			// Create different configuration
			nodeClass2 := &v1alpha1.ECSNodeClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "different-nodeclass",
				},
				Spec: v1alpha1.ECSNodeClassSpec{
					VSwitchSelectorTerms: []v1alpha1.VSwitchSelectorTerm{
						{ID: stringPtr("vsw-different-999")},
					},
					SecurityGroupSelectorTerms: []v1alpha1.SecurityGroupSelectorTerm{
						{ID: stringPtr("sg-different-999")},
					},
					ImageSelectorTerms: []v1alpha1.ImageSelectorTerm{
						{ID: stringPtr("different-image-999")},
					},
				},
			}
			Expect(env.Client.Create(ctx, nodeClass2)).To(Succeed())
			_, err = hashController.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(nodeClass2)})
			Expect(err).ToNot(HaveOccurred())

			updated2 := &v1alpha1.ECSNodeClass{}
			Expect(env.Client.Get(ctx, client.ObjectKeyFromObject(nodeClass2), updated2)).To(Succeed())
			hash2 := updated2.Annotations[v1alpha1.AnnotationECSNodeClassHash]

			// Hashes should be different
			Expect(hash1).ToNot(Equal(hash2))
		})
	})
})

// Helper functions
func stringPtr(s string) *string {
	return &s
}

func int32Ptr(i int32) *int32 {
	return &i
}
