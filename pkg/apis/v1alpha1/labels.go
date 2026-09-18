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

package v1alpha1

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	karpv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

func init() {
	karpv1.WellKnownLabels = karpv1.WellKnownLabels.Insert(WellKnownLabels()...)
	karpv1.NormalizedLabels[LabelDiskCSITopologyZone] = corev1.LabelTopologyZone
	karpv1.WellKnownResources.Insert(ResourceGPU, ResourceGPUMemory)
}

const (
	// Group is the group name for the AlibabaCloud provider
	Group = "karpenter.sh"

	// LabelDomain is the domain for AlibabaCloud provider-owned scheduling labels.
	LabelDomain = "karpenter.alibabacloud.com"

	// AnnotationECSNodeClassHash is the annotation key for ECSNodeClass hash
	AnnotationECSNodeClassHash = Group + "/ecsnodeclass-hash"

	// AnnotationECSNodeClassHashVersion is the annotation key for hash version
	AnnotationECSNodeClassHashVersion = Group + "/ecsnodeclass-hash-version"

	// AnnotationInstanceTagged marks that the ECS instance has been successfully tagged.
	// Once set to "true", the tagging controller will skip subsequent reconciles to avoid
	// redundant API calls. Aligns with AWS Karpenter design.
	AnnotationInstanceTagged = Group + "/instance-tagged"

	// LabelNodeClass is the label key for node class name
	LabelNodeClass = Group + "/nodeclass"

	// LabelCapacityType is the label key for capacity type (on-demand/spot)
	LabelCapacityType = "karpenter.sh/capacity-type"

	// CapacityTypeOnDemand represents on-demand capacity type
	CapacityTypeOnDemand = "on-demand"

	// CapacityTypeSpot represents spot capacity type
	CapacityTypeSpot = "spot"

	// CapacityTypePrePaid represents subscription (PrePaid) capacity type
	CapacityTypePrePaid = "pre-paid"

	// TagName is the tag key for instance name
	TagName = "Name"

	// TagNodePool is the tag key for nodepool name
	TagNodePool = Group + "/nodepool"

	// TagNodeClaim is the tag key for nodeclaim name
	TagNodeClaim = Group + "/nodeclaim"

	// TagManagedBy is the tag key indicating resource is managed by Karpenter
	TagManagedBy = Group + "/managed-by"

	// TagManagedByValue is the value used to identify resources managed by Karpenter.
	// MUST stay "karpenter" to match opensource-main: instances created by the
	// upstream provider are tagged karpenter.sh/managed-by=karpenter, and List()
	// filters on this exact value. Changing it (e.g. to "true") is a breaking
	// change that orphans existing nodes and can cause GC to miss/mis-handle them.
	TagManagedByValue = "karpenter"

	// TagCluster is the tag key for cluster name
	TagCluster = "kubernetes.io/cluster"

	// TagClusterID is the tag key for ACK cluster ID
	TagClusterID = Group + "/cluster-id"

	// TagDiscovery is the tag key for resource discovery
	TagDiscovery = Group + "/discovery"

	// TagKubeletMaxPods records the ECSNodeClass kubelet maxPods setting on launched instances.
	TagKubeletMaxPods = Group + "/kubelet-max-pods"

	// These tags preserve the disk estimate used when an instance was launched.
	// Get and List can report the same storage after the NodeClass changes.
	TagEphemeralStorageCapacity    = LabelDomain + "/ephemeral-storage-capacity"
	TagEphemeralStorageAllocatable = LabelDomain + "/ephemeral-storage-allocatable"

	// LabelInstanceFamily is the legacy label key for instance family
	LabelInstanceFamily = "node.kubernetes.io/instance-family"

	// LabelInstanceFamilyCanonical is the provider-owned label key for instance family
	LabelInstanceFamilyCanonical = LabelDomain + "/instance-family"

	// LabelInstanceCategory is the label key for instance category
	LabelInstanceCategory = LabelDomain + "/instance-category"

	// LabelInstanceGeneration is the label key for instance generation
	LabelInstanceGeneration = LabelDomain + "/instance-generation"

	// LabelInstanceSize is the legacy label key for instance size
	LabelInstanceSize = "node.kubernetes.io/instance-size"

	// LabelInstanceSizeCanonical is the provider-owned label key for instance size
	LabelInstanceSizeCanonical = LabelDomain + "/instance-size"

	// LabelInstanceCPU is the label key for vCPU count
	LabelInstanceCPU = LabelDomain + "/instance-cpu"

	// LabelInstanceMemory is the label key for memory in MiB
	LabelInstanceMemory = LabelDomain + "/instance-memory"

	// LabelInstanceGPUName is the label key for GPU model
	LabelInstanceGPUName = LabelDomain + "/instance-gpu-name"

	// LabelInstanceGPUManufacturer is the label key for GPU manufacturer
	LabelInstanceGPUManufacturer = LabelDomain + "/instance-gpu-manufacturer"

	// LabelInstanceGPUCount is the label key for GPU count
	LabelInstanceGPUCount = LabelDomain + "/instance-gpu-count"

	// LabelInstanceGPUMemory is the label key for GPU memory in GiB
	LabelInstanceGPUMemory = LabelDomain + "/instance-gpu-memory"

	// LabelCapacityReservationID is the label key for reserved capacity ID
	LabelCapacityReservationID = LabelDomain + "/capacity-reservation-id"

	// LabelCapacityReservationType is the label key for reserved capacity type
	LabelCapacityReservationType = LabelDomain + "/capacity-reservation-type"

	// LabelInstanceType is the label key for instance type
	LabelInstanceType = "node.kubernetes.io/instance-type"

	// LabelZone is the label key for zone
	LabelZone = "topology.kubernetes.io/zone"

	// LabelDiskCSITopologyZone is the ACK disk CSI topology key for zonal volumes.
	LabelDiskCSITopologyZone = "topology.diskplugin.csi.alibabacloud.com/zone"

	// LabelRegion is the label key for region
	LabelRegion = "topology.kubernetes.io/region"

	// LabelArchitecture is the label key for CPU architecture
	LabelArchitecture = "kubernetes.io/arch"

	// LabelOS is the label key for operating system
	LabelOS = "kubernetes.io/os"

	// ArchitectureAmd64 represents x86_64 architecture
	ArchitectureAmd64 = "amd64"

	// ArchitectureArm64 represents ARM64 architecture
	ArchitectureArm64 = "arm64"

	// OSLinux represents Linux operating system
	OSLinux = "linux"

	// OSWindows represents Windows operating system
	OSWindows = "windows"

	ResourceGPU       corev1.ResourceName = "nvidia.com/gpu"
	ResourceGPUMemory corev1.ResourceName = "aliyun.com/gpu-mem"
)

// WellKnownLabels returns a set of well-known Kubernetes labels
func WellKnownLabels() []string {
	return []string{
		LabelInstanceType,
		LabelInstanceFamily,
		LabelInstanceFamilyCanonical,
		LabelInstanceCategory,
		LabelInstanceGeneration,
		LabelInstanceSize,
		LabelInstanceSizeCanonical,
		LabelInstanceCPU,
		LabelInstanceMemory,
		LabelInstanceGPUName,
		LabelInstanceGPUManufacturer,
		LabelInstanceGPUCount,
		LabelInstanceGPUMemory,
		LabelCapacityReservationID,
		LabelCapacityReservationType,
		LabelZone,
		LabelRegion,
		LabelArchitecture,
		LabelOS,
		LabelCapacityType,
	}
}

// NormalizeLabelValue converts provider-sourced values into Kubernetes label values.
func NormalizeLabelValue(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var normalized strings.Builder
	lastReplacement := false
	for _, r := range value {
		if isLabelValueChar(r) {
			normalized.WriteRune(r)
			lastReplacement = false
			continue
		}
		if !lastReplacement {
			normalized.WriteByte('-')
			lastReplacement = true
		}
	}
	result := strings.Trim(normalized.String(), "-_.")
	if len(result) > 63 {
		result = strings.Trim(result[:63], "-_.")
	}
	return result
}

func isLabelValueChar(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= '0' && r <= '9') ||
		r == '-' ||
		r == '_' ||
		r == '.'
}
