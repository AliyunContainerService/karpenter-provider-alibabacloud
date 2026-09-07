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

package storage

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	environmentcs "github.com/AliyunContainerService/karpenter-provider-alibabacloud/test/pkg/cs"
	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	coretest "sigs.k8s.io/karpenter/pkg/test"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var env *environmentcs.Environment
var nodeClass *v1alpha1.ECSNodeClass
var nodePool *karpv1.NodePool

func TestStorage(t *testing.T) {
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
	RunSpecs(t, "Storage")
}

var _ = BeforeEach(func() {
	env.BeforeEach()
	nodeClass = env.DefaultECSNodeClass()
	nodePool = env.DefaultNodePool(nodeClass)
})

var _ = AfterEach(func() {
	env.AfterEach()
})

var _ = Describe("Persistent Volumes", func() {
	It("should run a pod with a pre-bound persistent volume and empty storage class", Label("static-pv"), func() {
		configureNodeClassAndPool("static-pv")
		pvc := coretest.PersistentVolumeClaim(coretest.PersistentVolumeClaimOptions{
			VolumeName:       "storage-test-volume",
			StorageClassName: lo.ToPtr(""),
		})
		pv := coretest.PersistentVolume(coretest.PersistentVolumeOptions{
			ObjectMeta: metav1.ObjectMeta{Name: pvc.Spec.VolumeName},
		})
		pod := storageTestPod("static-pv", coretest.PodOptions{
			PersistentVolumeClaims: []string{pvc.Name},
		})

		env.ExpectCreated(nodeClass, nodePool, pv, pvc, pod)
		env.EventuallyExpectHealthy(pod)
		env.ExpectCreatedNodeCount("==", 1)
	})

	It("should run a pod with a pre-bound persistent volume and explicit storage class", Label("static-pv-storage-class"), func() {
		configureNodeClassAndPool("static-pv-storage-class")
		pvc := coretest.PersistentVolumeClaim(coretest.PersistentVolumeClaimOptions{
			VolumeName:       "storage-test-volume-class",
			StorageClassName: lo.ToPtr("non-existent-storage-class"),
		})
		pv := coretest.PersistentVolume(coretest.PersistentVolumeOptions{
			ObjectMeta:       metav1.ObjectMeta{Name: pvc.Spec.VolumeName},
			StorageClassName: "non-existent-storage-class",
		})
		pod := storageTestPod("static-pv-storage-class", coretest.PodOptions{
			PersistentVolumeClaims: []string{pvc.Name},
		})

		env.ExpectCreated(nodeClass, nodePool, pv, pvc, pod)
		env.EventuallyExpectHealthy(pod)
		env.ExpectCreatedNodeCount("==", 1)
	})

	It("should launch a node with ECS data disk configuration", Label("ecs-data-disk"), func() {
		configureNodeClassAndPool("ecs-data-disk")
		nodeClass.Spec.DataDisks = []v1alpha1.DataDiskSpec{
			{
				Category:         "cloud_essd",
				Size:             120,
				PerformanceLevel: lo.ToPtr("PL0"),
			},
		}
		pod := storageTestPod("ecs-data-disk")

		env.ExpectCreated(nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		env.EventuallyExpectCreatedNodeClaimCount("==", 1)
	})

	It("should dynamically provision a disk through ACK CSI", Label("dynamic-csi-pvc"), func() {
		configureNodeClassAndPool("dynamic-csi-pvc")
		storageClassName := dynamicStorageClassName()
		storageClass := dynamicDiskStorageClass(storageClassName)
		pvc := coretest.PersistentVolumeClaim(coretest.PersistentVolumeClaimOptions{
			ObjectMeta:       metav1.ObjectMeta{Name: "storage-test-dynamic-csi-pvc", Namespace: "default"},
			StorageClassName: lo.ToPtr(storageClassName),
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("20Gi")},
			},
		})
		pod := storageTestPod("dynamic-csi-pvc", coretest.PodOptions{
			PersistentVolumeClaims: []string{pvc.Name},
		})

		env.ExpectCreated(storageClass, nodeClass, nodePool, pvc, pod)
		env.EventuallyExpectHealthy(pod)

		Eventually(func(g Gomega) {
			currentPVC := &corev1.PersistentVolumeClaim{}
			g.Expect(env.Client.Get(env.Context, client.ObjectKey{Namespace: pvc.Namespace, Name: pvc.Name}, currentPVC)).To(Succeed())
			g.Expect(currentPVC.Status.Phase).To(Equal(corev1.ClaimBound))
			g.Expect(currentPVC.Spec.VolumeName).ToNot(BeEmpty())

			pv := &corev1.PersistentVolume{}
			g.Expect(env.Client.Get(env.Context, client.ObjectKey{Name: currentPVC.Spec.VolumeName}, pv)).To(Succeed())
			g.Expect(pv.Spec.CSI).ToNot(BeNil())
			g.Expect(pv.Spec.CSI.Driver).To(Equal("diskplugin.csi.alibabacloud.com"))
		}).WithTimeout(10 * time.Minute).Should(Succeed())
	})

	It("should run a pod with a pre-bound persistent volume while respecting topology constraints", Label("topology"), func() {
		zones := envList("TEST_ZONES")
		if len(zones) == 0 {
			Skip("storage topology test requires TEST_ZONES from ackctl setup")
		}
		configureNodeClassAndPool("static-topology")
		pvc := coretest.PersistentVolumeClaim(coretest.PersistentVolumeClaimOptions{
			VolumeName:       "storage-test-topology-volume",
			StorageClassName: lo.ToPtr("non-existent-storage-class"),
		})
		pv := coretest.PersistentVolume(coretest.PersistentVolumeOptions{
			ObjectMeta:       metav1.ObjectMeta{Name: pvc.Spec.VolumeName},
			StorageClassName: "non-existent-storage-class",
			Zones:            []string{zones[0]},
		})
		pod := storageTestPod("static-topology", coretest.PodOptions{
			PersistentVolumeClaims: []string{pvc.Name},
		})

		env.ExpectCreated(nodeClass, nodePool, pv, pvc, pod)
		env.EventuallyExpectHealthy(pod)
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]
		Expect(node.Labels).To(HaveKeyWithValue(corev1.LabelTopologyZone, zones[0]))
	})

	It("should run a pod with a generic ephemeral volume", Label("generic-ephemeral"), func() {
		configureNodeClassAndPool("generic-ephemeral")
		storageClassName := dynamicStorageClassName() + "-ephemeral"
		storageClass := dynamicDiskStorageClass(storageClassName)
		pod := storageTestPod("generic-ephemeral", coretest.PodOptions{
			EphemeralVolumeTemplates: []coretest.EphemeralVolumeTemplateOptions{{
				StorageClassName: lo.ToPtr(storageClassName),
			}},
		})

		env.ExpectCreated(storageClass, nodeClass, nodePool, pod)
		env.EventuallyExpectHealthy(pod)
		env.ExpectCreatedNodeCount("==", 1)
	})

	It("should run pods with dynamic persistent volumes while respecting volume limits", Label("volume-limits"), func() {
		configureNodeClassAndPool("volume-limits")
		storageClassName := dynamicStorageClassName() + "-limits"
		storageClass := dynamicDiskStorageClass(storageClassName)
		pvcs := lo.Times(2, func(i int) *corev1.PersistentVolumeClaim {
			return coretest.PersistentVolumeClaim(coretest.PersistentVolumeClaimOptions{
				ObjectMeta:       metav1.ObjectMeta{Name: "storage-test-volume-limits-" + string(rune('a'+i)), Namespace: "default"},
				StorageClassName: lo.ToPtr(storageClassName),
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("20Gi")},
				},
			})
		})
		podLabels := map[string]string{"app": "storage-test-volume-limits"}
		pods := lo.Map(pvcs, func(pvc *corev1.PersistentVolumeClaim, i int) *corev1.Pod {
			return storageTestPod("volume-limits-"+string(rune('a'+i)), coretest.PodOptions{
				ObjectMeta:             metav1.ObjectMeta{Name: "storage-test-volume-limits-" + string(rune('a'+i)), Labels: podLabels},
				PersistentVolumeClaims: []string{pvc.Name},
				PodAntiRequirements: []corev1.PodAffinityTerm{{
					TopologyKey:   corev1.LabelHostname,
					LabelSelector: &metav1.LabelSelector{MatchLabels: podLabels},
				}},
			})
		})

		env.ExpectCreated(storageClass, nodeClass, nodePool, pvcs[0], pods[0])
		env.EventuallyExpectHealthy(pods[0])
		env.ExpectCreated(pvcs[1], pods[1])
		env.EventuallyExpectHealthy(pods[1])
		env.EventuallyExpectCreatedNodeCount("==", 2)
	})

	It("should bind dynamic storage after a disrupted node is replaced", Label("dynamic"), Label("disrupted"), Label("node-deletion"), func() {
		configureNodeClassAndPool("dynamic-replacement")
		storageClassName := dynamicStorageClassName() + "-replacement"
		storageClass := dynamicDiskStorageClass(storageClassName)
		pvc := coretest.PersistentVolumeClaim(coretest.PersistentVolumeClaimOptions{
			ObjectMeta:       metav1.ObjectMeta{Name: "storage-test-dynamic-replacement", Namespace: "default"},
			StorageClassName: lo.ToPtr(storageClassName),
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("20Gi")},
			},
		})
		pod := storageTestPod("dynamic-replacement", coretest.PodOptions{
			PersistentVolumeClaims: []string{pvc.Name},
		})

		env.ExpectCreated(storageClass, nodeClass, nodePool, pvc, pod)
		env.EventuallyExpectHealthy(pod)
		nodeClaim := env.EventuallyExpectCreatedNodeClaimCount("==", 1)[0]
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]

		env.Monitor.Reset()
		env.ExpectDeleted(nodeClaim)
		env.EventuallyExpectNotFound(nodeClaim, node)
		env.ExpectDeleted(pod)
		replacementPod := storageTestPod("dynamic-replacement-again", coretest.PodOptions{
			PersistentVolumeClaims: []string{pvc.Name},
		})
		env.ExpectCreated(replacementPod)
		env.EventuallyExpectHealthy(replacementPod)
		env.EventuallyExpectCreatedNodeCount("==", 1)
	})
})

var _ = Describe("Stateful workloads", func() {
	It("should run on a new node without long delays when disrupted by node deletion and drain", Label("disrupted"), Label("node-deletion"), Label("drain"), func() {
		configureNodeClassAndPool("stateful-disrupted")
		storageClassName := dynamicStorageClassName() + "-stateful"
		storageClass := dynamicDiskStorageClass(storageClassName)
		statefulSet := statefulStorageWorkload("stateful-disrupted", storageClassName)
		selector := labels.SelectorFromSet(statefulSet.Spec.Selector.MatchLabels)

		env.ExpectCreated(nodeClass, nodePool, storageClass, statefulSet)
		nodeClaim := env.EventuallyExpectCreatedNodeClaimCount("==", 1)[0]
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]
		env.EventuallyExpectHealthyPodCount(selector, 1)

		env.Monitor.Reset()
		env.ExpectDeleted(nodeClaim)
		env.EventuallyExpectNotFound(nodeClaim, node)
		env.EventuallyExpectCreatedNodeClaimCount("==", 1)
		env.EventuallyExpectBoundPodCount(selector, 1)
		env.EventuallyExpectHealthyPodCountWithTimeout(3*time.Minute, selector, 1)
	})

	It("should not block node deletion if stateful workload cannot be drained", Label("disrupted"), Label("node-deletion"), Label("drain"), func() {
		configureNodeClassAndPool("stateful-undrainable")
		storageClassName := dynamicStorageClassName() + "-undrainable"
		storageClass := dynamicDiskStorageClass(storageClassName)
		statefulSet := statefulStorageWorkload("stateful-undrainable", storageClassName)
		statefulSet.Spec.Template.Spec.Tolerations = []corev1.Toleration{{
			Key:      "karpenter.sh/disruption",
			Operator: corev1.TolerationOpEqual,
			Value:    "disrupting",
			Effect:   corev1.TaintEffectNoExecute,
		}}
		selector := labels.SelectorFromSet(statefulSet.Spec.Selector.MatchLabels)

		env.ExpectCreated(nodeClass, nodePool, storageClass, statefulSet)
		nodeClaim := env.EventuallyExpectCreatedNodeClaimCount("==", 1)[0]
		node := env.EventuallyExpectCreatedNodeCount("==", 1)[0]
		env.EventuallyExpectHealthyPodCount(selector, 1)
		env.ExpectDeleted(nodeClaim)
		env.EventuallyExpectNotFound(nodeClaim, node)
	})
})

func configureNodeClassAndPool(name string) {
	nodeClass.Name = "storage-test-" + name
	nodeClass.Spec.Tags = env.TestTags(name)
	nodePool.Name = "storage-test-pool-" + name
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

func storageTestPod(name string, overrides ...coretest.PodOptions) *corev1.Pod {
	options := []coretest.PodOptions{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "storage-test-" + name},
			Image:      "registry-cn-hangzhou.ack.aliyuncs.com/acs/pause:3.9",
			ResourceRequirements: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("128Mi"),
				},
			},
			NodeSelector: map[string]string{
				karpv1.NodePoolLabelKey: nodePool.Name,
			},
		},
	}
	options = append(options, overrides...)
	return coretest.Pod(options...)
}

func statefulStorageWorkload(name, storageClassName string) *appsv1.StatefulSet {
	pvc := coretest.PersistentVolumeClaim(coretest.PersistentVolumeClaimOptions{
		ObjectMeta:       metav1.ObjectMeta{Name: "storage-test-" + name},
		StorageClassName: lo.ToPtr(storageClassName),
		Resources: corev1.VolumeResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("20Gi")},
		},
	})
	sts := coretest.StatefulSet(coretest.StatefulSetOptions{
		ObjectMeta: metav1.ObjectMeta{Name: "storage-test-" + name},
		Replicas:   1,
		PodOptions: coretest.PodOptions{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "storage-test-" + name}},
			Image:      "registry-cn-hangzhou.ack.aliyuncs.com/acs/pause:3.9",
			NodeSelector: map[string]string{
				karpv1.NodePoolLabelKey: nodePool.Name,
			},
			ResourceRequirements: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("128Mi"),
				},
			},
		},
	})
	sts.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{*pvc}
	sts.Spec.Template.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{{
		Name:      pvc.Name,
		MountPath: "/data",
	}}
	return sts
}

func testInstanceTypes() []string {
	if values := envList("TEST_INSTANCE_TYPES"); len(values) > 0 {
		return values
	}
	return []string{"ecs.g7.large", "ecs.g7.xlarge"}
}

func dynamicStorageClassName() string {
	if value := strings.TrimSpace(os.Getenv("TEST_DYNAMIC_STORAGE_CLASS")); value != "" {
		return value
	}
	return "storage-test-dynamic-csi"
}

func dynamicDiskStorageClass(name string) *storagev1.StorageClass {
	bindingMode := storagev1.VolumeBindingWaitForFirstConsumer
	reclaimPolicy := corev1.PersistentVolumeReclaimDelete
	allowExpansion := true
	return &storagev1.StorageClass{
		ObjectMeta:  metav1.ObjectMeta{Name: name},
		Provisioner: "diskplugin.csi.alibabacloud.com",
		Parameters: map[string]string{
			"type":             "cloud_essd",
			"performanceLevel": "PL0",
		},
		VolumeBindingMode:    &bindingMode,
		ReclaimPolicy:        &reclaimPolicy,
		AllowVolumeExpansion: &allowExpansion,
	}
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
