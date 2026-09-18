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

package cloudprovider

import (
	"context"
	"testing"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instance"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instancetype"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	karpentercloudprovider "sigs.k8s.io/karpenter/pkg/cloudprovider"
	karpenterresources "sigs.k8s.io/karpenter/pkg/utils/resources"
)

func TestEphemeralStorageFollowsNodeClassDiskSize(t *testing.T) {
	for _, tt := range []struct {
		name            string
		sizeGiB         int32
		wantAllocatable string
	}{
		{name: "default disk", sizeGiB: 40, wantAllocatable: "34Gi"},
		{name: "spark disk", sizeGiB: 100, wantAllocatable: "88Gi"},
		{name: "larger disk", sizeGiB: 200, wantAllocatable: "178Gi"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			nodeClass := &v1alpha1.ECSNodeClass{Spec: v1alpha1.ECSNodeClassSpec{
				SystemDisk: &v1alpha1.SystemDiskSpec{Size: &tt.sizeGiB},
			}}
			capacity, overhead, err := ephemeralStorageResources(nodeClass)
			if err != nil {
				t.Fatal(err)
			}
			if want := resource.NewQuantity(int64(tt.sizeGiB)<<30, resource.BinarySI); capacity.Cmp(*want) != 0 {
				t.Fatalf("capacity = %s, want %s", capacity.String(), want.String())
			}
			instanceType := &karpentercloudprovider.InstanceType{
				Capacity: corev1.ResourceList{corev1.ResourceEphemeralStorage: capacity},
				Overhead: overhead,
			}
			allocatable := instanceType.Allocatable()[corev1.ResourceEphemeralStorage]
			if want := resource.MustParse(tt.wantAllocatable); allocatable.Cmp(want) != 0 {
				t.Fatalf("allocatable = %s, want %s", allocatable.String(), want.String())
			}
			if tt.sizeGiB == 100 {
				if !karpenterresources.Fits(corev1.ResourceList{corev1.ResourceEphemeralStorage: resource.MustParse("20Gi")}, instanceType.Allocatable()) {
					t.Fatal("a 20Gi request must fit on a 100Gi disk")
				}
				if karpenterresources.Fits(corev1.ResourceList{corev1.ResourceEphemeralStorage: resource.MustParse("89Gi")}, instanceType.Allocatable()) {
					t.Fatal("an 89Gi request must not fit on a 100Gi disk")
				}
			}
		})
	}
}

func TestEphemeralStorageHonorsLargerKubeletReservations(t *testing.T) {
	size := int32(100)
	nodeClass := &v1alpha1.ECSNodeClass{Spec: v1alpha1.ECSNodeClassSpec{
		SystemDisk: &v1alpha1.SystemDiskSpec{Size: &size},
		Kubelet: &v1alpha1.KubeletConfiguration{
			KubeReserved:   map[corev1.ResourceName]string{corev1.ResourceEphemeralStorage: "3Gi"},
			SystemReserved: map[corev1.ResourceName]string{corev1.ResourceEphemeralStorage: "4Gi"},
			EvictionHard:   map[string]string{nodeFSAvailableSignal: "15%"},
		},
	}}
	it := &instancetype.InstanceType{
		Name:   "ecs.g7.xlarge",
		CPU:    resource.NewQuantity(4, resource.DecimalSI),
		Memory: resource.NewQuantity(16<<30, resource.BinarySI),
	}
	capacity, allocatable, err := (&CloudProvider{}).calculateCapacityAndAllocatable(context.Background(), it, nodeClass)
	if err != nil {
		t.Fatal(err)
	}
	storageCapacity := capacity[corev1.ResourceEphemeralStorage]
	storageAllocatable := allocatable[corev1.ResourceEphemeralStorage]
	if storageCapacity.Cmp(resource.MustParse("100Gi")) != 0 || storageAllocatable.Cmp(resource.MustParse("78Gi")) != 0 {
		t.Fatalf("capacity/allocatable = %s/%s, want 100Gi/78Gi", storageCapacity.String(), storageAllocatable.String())
	}
}

func TestEphemeralStorageHonorsSoftNodeFSEviction(t *testing.T) {
	size := int32(100)
	nodeClass := &v1alpha1.ECSNodeClass{Spec: v1alpha1.ECSNodeClassSpec{
		SystemDisk: &v1alpha1.SystemDiskSpec{Size: &size},
		Kubelet: &v1alpha1.KubeletConfiguration{
			EvictionSoft: map[string]string{nodeFSAvailableSignal: "20%"},
		},
	}}
	capacity, overhead, err := ephemeralStorageResources(nodeClass)
	if err != nil {
		t.Fatal(err)
	}
	instanceType := &karpentercloudprovider.InstanceType{
		Capacity: corev1.ResourceList{corev1.ResourceEphemeralStorage: capacity},
		Overhead: overhead,
	}
	allocatable := instanceType.Allocatable()[corev1.ResourceEphemeralStorage]
	if want := resource.MustParse("78Gi"); allocatable.Cmp(want) != 0 {
		t.Fatalf("allocatable = %s, want %s", allocatable.String(), want.String())
	}
}

func TestEphemeralStorageDoesNotAdvertiseUnknownDisk(t *testing.T) {
	it := &instancetype.InstanceType{
		Name:   "ecs.g7.xlarge",
		CPU:    resource.NewQuantity(4, resource.DecimalSI),
		Memory: resource.NewQuantity(16<<30, resource.BinarySI),
	}
	capacity, allocatable, err := (&CloudProvider{}).calculateCapacityAndAllocatable(context.Background(), it, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := capacity[corev1.ResourceEphemeralStorage]; exists {
		t.Fatal("unknown system disk must not advertise ephemeral capacity")
	}
	if _, exists := allocatable[corev1.ResourceEphemeralStorage]; exists {
		t.Fatal("unknown system disk must not advertise ephemeral allocatable")
	}
}

func TestEphemeralStorageRequiresNodeClass(t *testing.T) {
	if _, _, err := ephemeralStorageResources(nil); err == nil {
		t.Fatal("calculating storage without a NodeClass must fail")
	}
}

func TestEphemeralStorageCapsOverheadAtDiskSize(t *testing.T) {
	size := int32(20)
	nodeClass := &v1alpha1.ECSNodeClass{Spec: v1alpha1.ECSNodeClassSpec{
		SystemDisk: &v1alpha1.SystemDiskSpec{Size: &size},
		Kubelet: &v1alpha1.KubeletConfiguration{
			KubeReserved: map[corev1.ResourceName]string{corev1.ResourceEphemeralStorage: "20Gi"},
		},
	}}
	capacity, overhead, err := ephemeralStorageResources(nodeClass)
	if err != nil {
		t.Fatal(err)
	}
	instanceType := &karpentercloudprovider.InstanceType{
		Capacity: corev1.ResourceList{corev1.ResourceEphemeralStorage: capacity},
		Overhead: overhead,
	}
	allocatable := instanceType.Allocatable()[corev1.ResourceEphemeralStorage]
	if allocatable.Sign() != 0 {
		t.Fatalf("allocatable = %s, want zero", allocatable.String())
	}
}

func TestEphemeralStorageTagsPreserveLaunchTimeEstimate(t *testing.T) {
	inst := &instance.Instance{Tags: map[string]string{
		v1alpha1.TagEphemeralStorageCapacity:    "100Gi",
		v1alpha1.TagEphemeralStorageAllocatable: "88Gi",
	}}
	capacity := corev1.ResourceList{}
	allocatable := corev1.ResourceList{}
	applyEphemeralStorageTags(inst, capacity, allocatable)
	storageCapacity := capacity[corev1.ResourceEphemeralStorage]
	storageAllocatable := allocatable[corev1.ResourceEphemeralStorage]
	if storageCapacity.Cmp(resource.MustParse("100Gi")) != 0 || storageAllocatable.Cmp(resource.MustParse("88Gi")) != 0 {
		t.Fatalf("tagged capacity/allocatable = %s/%s, want 100Gi/88Gi", storageCapacity.String(), storageAllocatable.String())
	}

	inst.Tags[v1alpha1.TagEphemeralStorageAllocatable] = "101Gi"
	capacity = corev1.ResourceList{}
	allocatable = corev1.ResourceList{}
	applyEphemeralStorageTags(inst, capacity, allocatable)
	if _, exists := capacity[corev1.ResourceEphemeralStorage]; exists {
		t.Fatal("invalid tagged allocatable must not advertise storage")
	}
}
