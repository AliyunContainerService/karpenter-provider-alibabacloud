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
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instance"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
)

const nodeFSAvailableSignal = "nodefs.available"

// ephemeralStorageResources estimates storage on the system disk used by nodefs.
// ECS SystemDisk.Size is in GiB. Filesystem metadata, kubelet/system usage and
// eviction headroom make the raw disk size larger than what pods can use.
func ephemeralStorageResources(nodeClass *v1alpha1.ECSNodeClass) (resource.Quantity, *cloudprovider.InstanceTypeOverhead, error) {
	if nodeClass == nil {
		return resource.Quantity{}, nil, fmt.Errorf("nodeclass is required to calculate ephemeral storage")
	}
	spec := nodeClass.Spec
	disks, err := v1alpha1.NormalizeDisks(spec)
	if err != nil {
		return resource.Quantity{}, nil, fmt.Errorf("normalizing system disk: %w", err)
	}
	capacity := *resource.NewQuantity(int64(disks.SystemDisk.Size)<<30, resource.BinarySI)
	baseReserve := resource.MustParse("1Gi")
	kubeReserved := baseReserve.DeepCopy()
	systemReserved := baseReserve.DeepCopy()
	// The default kubelet hard eviction threshold for nodefs is 10%. Keep
	// this minimum even if the image or NodeClass config uses a lower value.
	eviction := *resource.NewQuantity(capacity.Value()/10, resource.BinarySI)

	if spec.Kubelet != nil {
		if raw := spec.Kubelet.KubeReserved[corev1.ResourceEphemeralStorage]; raw != "" {
			kubeReserved, err = largerStorageReservation(kubeReserved, raw)
			if err != nil {
				return resource.Quantity{}, nil, fmt.Errorf("kubeReserved.ephemeral-storage: %w", err)
			}
		}
		if raw := spec.Kubelet.SystemReserved[corev1.ResourceEphemeralStorage]; raw != "" {
			systemReserved, err = largerStorageReservation(systemReserved, raw)
			if err != nil {
				return resource.Quantity{}, nil, fmt.Errorf("systemReserved.ephemeral-storage: %w", err)
			}
		}
		for _, threshold := range []struct {
			name   string
			values map[string]string
		}{
			{name: "evictionHard", values: spec.Kubelet.EvictionHard},
			{name: "evictionSoft", values: spec.Kubelet.EvictionSoft},
		} {
			if raw := threshold.values[nodeFSAvailableSignal]; raw != "" {
				configured, err := parseNodeFSEviction(raw, capacity)
				if err != nil {
					return resource.Quantity{}, nil, fmt.Errorf("%s.%s: %w", threshold.name, nodeFSAvailableSignal, err)
				}
				if configured.Cmp(eviction) > 0 {
					eviction = configured
				}
			}
		}
	}

	total := kubeReserved.DeepCopy()
	total.Add(systemReserved)
	total.Add(eviction)
	if total.Cmp(capacity) >= 0 {
		// An unusable disk must advertise zero allocatable, never a negative
		// quantity to the scheduler.
		return capacity, &cloudprovider.InstanceTypeOverhead{
			EvictionThreshold: corev1.ResourceList{corev1.ResourceEphemeralStorage: capacity},
		}, nil
	}
	return capacity, &cloudprovider.InstanceTypeOverhead{
		KubeReserved:      corev1.ResourceList{corev1.ResourceEphemeralStorage: kubeReserved},
		SystemReserved:    corev1.ResourceList{corev1.ResourceEphemeralStorage: systemReserved},
		EvictionThreshold: corev1.ResourceList{corev1.ResourceEphemeralStorage: eviction},
	}, nil
}

func largerStorageReservation(minimum resource.Quantity, raw string) (resource.Quantity, error) {
	configured, err := resource.ParseQuantity(raw)
	if err != nil {
		return resource.Quantity{}, err
	}
	if configured.Sign() < 0 {
		return resource.Quantity{}, fmt.Errorf("reservation must not be negative")
	}
	if configured.Cmp(minimum) > 0 {
		return configured, nil
	}
	return minimum, nil
}

func parseNodeFSEviction(raw string, capacity resource.Quantity) (resource.Quantity, error) {
	if strings.HasSuffix(raw, "%") {
		percent, err := strconv.ParseFloat(strings.TrimSuffix(raw, "%"), 64)
		if err != nil || math.IsNaN(percent) || percent < 0 || percent > 100 {
			return resource.Quantity{}, fmt.Errorf("invalid percentage %q", raw)
		}
		return *resource.NewQuantity(int64(math.Ceil(float64(capacity.Value())*percent/100)), resource.BinarySI), nil
	}
	quantity, err := resource.ParseQuantity(raw)
	if err != nil || quantity.Sign() < 0 {
		return resource.Quantity{}, fmt.Errorf("invalid quantity %q", raw)
	}
	return quantity, nil
}

// DescribeInstances does not include the system disk size. The values tagged at
// launch keep Get and List stable when the NodeClass changes or is deleted.
// Instances created before the tags existed retain the previous behavior.
func applyEphemeralStorageTags(inst *instance.Instance, capacity, allocatable corev1.ResourceList) {
	if capacity == nil || allocatable == nil {
		return
	}
	storageCapacity, err := resource.ParseQuantity(inst.Tags[v1alpha1.TagEphemeralStorageCapacity])
	if err != nil || storageCapacity.Sign() <= 0 {
		return
	}
	storageAllocatable, err := resource.ParseQuantity(inst.Tags[v1alpha1.TagEphemeralStorageAllocatable])
	if err != nil || storageAllocatable.Sign() < 0 || storageAllocatable.Cmp(storageCapacity) > 0 {
		return
	}
	capacity[corev1.ResourceEphemeralStorage] = storageCapacity
	allocatable[corev1.ResourceEphemeralStorage] = storageAllocatable
}
