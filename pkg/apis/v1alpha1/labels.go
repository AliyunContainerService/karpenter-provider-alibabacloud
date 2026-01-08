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

import corev1 "k8s.io/api/core/v1"

const (
	// Group is the group name for the AlibabaCloud provider
	Group = "karpenter.sh"

	// AnnotationECSNodeClassHash is the annotation key for ECSNodeClass hash
	AnnotationECSNodeClassHash = Group + "/ecsnodeclass-hash"

	// AnnotationECSNodeClassHashVersion is the annotation key for hash version
	AnnotationECSNodeClassHashVersion = Group + "/ecsnodeclass-hash-version"

	// LabelNodeClass is the label key for node class name
	LabelNodeClass = Group + "/nodeclass"

	// LabelCapacityType is the label key for capacity type (on-demand/spot)
	LabelCapacityType = "karpenter.sh/capacity-type"

	// CapacityTypeOnDemand represents on-demand capacity type
	CapacityTypeOnDemand = "on-demand"

	// CapacityTypeSpot represents spot capacity type
	CapacityTypeSpot = "spot"

	// TagName is the tag key for instance name
	TagName = "Name"

	// TagNodePool is the tag key for nodepool name
	TagNodePool = Group + "/nodepool"

	// TagNodeClaim is the tag key for nodeclaim name
	TagNodeClaim = Group + "/nodeclaim"

	// TagManagedBy is the tag key indicating resource is managed by Karpenter
	TagManagedBy = Group + "/managed-by"

	// TagCluster is the tag key for cluster name
	TagCluster = "kubernetes.io/cluster"

	// TagClusterID is the tag key for ACK cluster ID
	TagClusterID = Group + "/cluster-id"

	// TagDiscovery is the tag key for resource discovery
	TagDiscovery = Group + "/discovery"

	// LabelInstanceFamily is the label key for instance family
	LabelInstanceFamily = "node.kubernetes.io/instance-family"

	// LabelInstanceSize is the label key for instance size
	LabelInstanceSize = "node.kubernetes.io/instance-size"

	// LabelInstanceType is the label key for instance type
	LabelInstanceType = "node.kubernetes.io/instance-type"

	// LabelZone is the label key for zone
	LabelZone = "topology.kubernetes.io/zone"

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
		LabelInstanceSize,
		LabelZone,
		LabelRegion,
		LabelArchitecture,
		LabelOS,
		LabelCapacityType,
	}
}
