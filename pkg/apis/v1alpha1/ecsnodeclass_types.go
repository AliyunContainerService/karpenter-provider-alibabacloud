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

// +kubebuilder:object:generate=true
// +groupName=karpenter.alibabacloud.com
package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ECSNodeClass is the Schema for the ECSNodeClass API
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=ecsnodeclasses,scope=Cluster,categories=karpenter
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type ECSNodeClass struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ECSNodeClassSpec   `json:"spec,omitempty"`
	Status ECSNodeClassStatus `json:"status,omitempty"`
}

// ECSNodeClassList contains a list of ECSNodeClass
// +kubebuilder:object:root=true
type ECSNodeClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ECSNodeClass `json:"items"`
}

// ECSNodeClassSpec defines the desired state of ECSNodeClass
// +kubebuilder:object:generate=true
// +kubebuilder:object:root=false
type ECSNodeClassSpec struct {
	// ClusterID is the ACK cluster ID
	// +optional
	ClusterID string `json:"clusterID,omitempty"`

	// ClusterName is the ACK cluster name
	// +optional
	ClusterName string `json:"clusterName,omitempty"`

	// ClusterEndpoint is the Kubernetes API server endpoint
	// +optional
	ClusterEndpoint string `json:"clusterEndpoint,omitempty"`

	// VSwitchSelectorTerms is a list of VSwitch selector requirements
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	VSwitchSelectorTerms []VSwitchSelectorTerm `json:"vSwitchSelectorTerms"`

	// SecurityGroupSelectorTerms is a list of security group selector requirements
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	SecurityGroupSelectorTerms []SecurityGroupSelectorTerm `json:"securityGroupSelectorTerms"`

	// ImageSelectorTerms is a list of image selector requirements
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	ImageSelectorTerms []ImageSelectorTerm `json:"imageSelectorTerms"`

	// Role is the name of the RAM role to use for the instance
	// +optional
	Role *string `json:"role,omitempty"`

	// SystemDisk specifies the system disk configuration
	// +optional
	SystemDisk *SystemDiskSpec `json:"systemDisk,omitempty"`

	// DataDisks specifies the data disk configuration
	// +optional
	DataDisks []DataDiskSpec `json:"dataDisks,omitempty"`

	// SpotStrategy specifies the spot instance strategy (SpotAsPriceGo or SpotWithPriceLimit)
	// +optional
	SpotStrategy *string `json:"spotStrategy,omitempty"`

	// SpotPriceLimit specifies the maximum hourly price for spot instances
	// +optional
	SpotPriceLimit *float64 `json:"spotPriceLimit,omitempty"`

	// UserData specifies custom user data script
	// +optional
	UserData *string `json:"userData,omitempty"`

	// Tags are instance tags
	// +optional
	Tags map[string]string `json:"tags,omitempty"`

	// Kubelet defines Kubelet configuration overrides
	// +optional
	Kubelet *KubeletConfiguration `json:"kubelet,omitempty"`

	// CapacityReservationSelectorTerms is a list of capacity reservation selector requirements
	// +optional
	CapacityReservationSelectorTerms []CapacityReservationSelectorTerm `json:"capacityReservationSelectorTerms,omitempty"`

	// CapacityReservationPreference specifies the preference for capacity reservations (open or target or none)
	// +optional
	CapacityReservationPreference *string `json:"capacityReservationPreference,omitempty"`

	// MetadataOptions specifies metadata service options
	// +optional
	MetadataOptions *MetadataOptions `json:"metadataOptions,omitempty"`

	// LaunchTemplateID specifies the launch template ID to use
	// +optional
	LaunchTemplateID *string `json:"launchTemplateID,omitempty"`
}

// VSwitchSelectorTerm defines selection logic for VSwitch
// +kubebuilder:object:generate=true
// +kubebuilder:object:root=false
type VSwitchSelectorTerm struct {
	// Tags is a map of tags to match
	// +optional
	Tags map[string]string `json:"tags,omitempty"`

	// ID is the VSwitch ID
	// +optional
	ID *string `json:"id,omitempty"`

	// ZoneID is the zone ID
	// +optional
	ZoneID *string `json:"zoneID,omitempty"`
}

// SecurityGroupSelectorTerm defines selection logic for Security Group
// +kubebuilder:object:generate=true
// +kubebuilder:object:root=false
type SecurityGroupSelectorTerm struct {
	// Tags is a map of tags to match
	// +optional
	Tags map[string]string `json:"tags,omitempty"`

	// ID is the security group ID
	// +optional
	ID *string `json:"id,omitempty"`

	// Name is the security group name
	// +optional
	Name *string `json:"name,omitempty"`
}

// ImageSelectorTerm defines selection logic for Image
// +kubebuilder:object:generate=true
// +kubebuilder:object:root=false
type ImageSelectorTerm struct {
	// Alias is the image alias (system, self, others, marketplace)
	// +optional
	ImageOwnerAlias *string `json:"imageOwnerAlias,omitempty"`

	// Tags is a map of tags to match
	// +optional
	Tags map[string]string `json:"tags,omitempty"`

	// ID is the image ID
	// +optional
	ID *string `json:"id,omitempty"`

	// Name is the image name pattern
	// +optional
	Name *string `json:"name,omitempty"`

	// Owner is the image owner
	// +optional
	ImageOwnerID *string `json:"imageOwnerID,omitempty"`

	// Family is the image family (e.g. acs:alibaba_cloud_linux_3_2104_lts_x64)
	// +optional
	ImageFamily *string `json:"imageFamily,omitempty"`
}

// CapacityReservation represents a resolved capacity reservation
// +kubebuilder:object:generate=true
// +kubebuilder:object:root=false
type CapacityReservation struct {
	// ID is the capacity reservation ID
	ID string `json:"id"`

	// Name is the capacity reservation name
	Name string `json:"name,omitempty"`
}

// CapacityReservationSelectorTerm defines selection logic for Capacity Reservation
// +kubebuilder:object:generate=true
// +kubebuilder:object:root=false
type CapacityReservationSelectorTerm struct {
	// Tags is a map of tags to match
	// +optional
	Tags map[string]string `json:"tags,omitempty"`

	// ID is the capacity reservation ID
	// +optional
	ID *string `json:"id,omitempty"`
}

// SystemDiskSpec defines the system disk configuration
// +kubebuilder:object:generate=true
// +kubebuilder:object:root=false
type SystemDiskSpec struct {
	// Category is the disk type (cloud_efficiency, cloud_ssd, cloud_essd)
	// +kubebuilder:default="cloud_essd"
	Category string `json:"category,omitempty"`

	// Size is the disk size in GB
	// +kubebuilder:default=40
	// +kubebuilder:validation:Minimum=20
	// +kubebuilder:validation:Maximum=500
	Size *int32 `json:"size,omitempty"`

	// PerformanceLevel is the ESSD performance level (PL0, PL1, PL2, PL3)
	// +kubebuilder:default="PL0"
	// +optional
	PerformanceLevel *string `json:"performanceLevel,omitempty"`

	// Encrypted specifies whether the disk is encrypted
	// +optional
	Encrypted *bool `json:"encrypted,omitempty"`

	// KMSKeyID is the KMS key ID for encryption
	// +optional
	KMSKeyID *string `json:"kmsKeyID,omitempty"`
}

// DataDiskSpec defines the data disk configuration
// +kubebuilder:object:generate=true
// +kubebuilder:object:root=false
type DataDiskSpec struct {
	// Category is the disk type
	Category string `json:"category"`

	// Size is the disk size in GB
	Size int32 `json:"size"`

	// Device is the device name
	// +optional
	Device *string `json:"device,omitempty"`

	// PerformanceLevel is the ESSD performance level
	// +optional
	PerformanceLevel *string `json:"performanceLevel,omitempty"`

	// Encrypted specifies whether the disk is encrypted
	// +optional
	Encrypted *bool `json:"encrypted,omitempty"`

	// SnapshotID is the snapshot ID to create from
	// +optional
	SnapshotID *string `json:"snapshotID,omitempty"`

	// DeleteWithInstance specifies whether to delete with instance
	// +kubebuilder:default=true
	DeleteWithInstance *bool `json:"deleteWithInstance,omitempty"`
}

// KubeletConfiguration defines Kubelet configuration overrides
// +kubebuilder:object:generate=true
// +kubebuilder:object:root=false
type KubeletConfiguration struct {
	// ClusterDNS is a list of DNS server IP addresses
	// +optional
	ClusterDNS []string `json:"clusterDNS,omitempty"`

	// MaxPods is the maximum number of pods per node
	// +optional
	MaxPods *int32 `json:"maxPods,omitempty"`

	// SystemReserved contains resources reserved for system daemons
	// +optional
	SystemReserved map[corev1.ResourceName]string `json:"systemReserved,omitempty"`

	// KubeReserved contains resources reserved for Kubernetes components
	// +optional
	KubeReserved map[corev1.ResourceName]string `json:"kubeReserved,omitempty"`

	// EvictionHard contains eviction thresholds
	// +optional
	EvictionHard map[string]string `json:"evictionHard,omitempty"`

	// PodsPerCore enables pods per core scheduling
	// The maximum number of pods per node will be calculated as podsPerCore * number of cores
	// +optional
	PodsPerCore *int32 `json:"podsPerCore,omitempty"`

	// EvictionSoft contains soft eviction thresholds
	// +optional
	EvictionSoft map[string]string `json:"evictionSoft,omitempty"`

	// EvictionSoftGracePeriod contains grace periods for soft eviction thresholds
	// +optional
	EvictionSoftGracePeriod map[string]string `json:"evictionSoftGracePeriod,omitempty"`

	// EvictionMaxPodGracePeriod is the maximum grace period for pod termination during eviction
	// +optional
	EvictionMaxPodGracePeriod *int32 `json:"evictionMaxPodGracePeriod,omitempty"`

	// ImageGCHighThresholdPercent is the disk usage threshold for high image garbage collection
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +optional
	ImageGCHighThresholdPercent *int32 `json:"imageGCHighThresholdPercent,omitempty"`

	// ImageGCLowThresholdPercent is the disk usage threshold for low image garbage collection
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +optional
	ImageGCLowThresholdPercent *int32 `json:"imageGCLowThresholdPercent,omitempty"`

	// CPUCFSQuota enables CPU CFS quota enforcement
	// +optional
	CPUCFSQuota *bool `json:"cpuCFSQuota,omitempty"`
}

// MetadataOptions defines instance metadata service configuration
// +kubebuilder:object:generate=true
// +kubebuilder:object:root=false
type MetadataOptions struct {
	// HttpTokens specifies whether to require IMDSv2 (optional or required)
	// +kubebuilder:default="optional"
	// +kubebuilder:validation:Enum=optional;required
	HttpTokens string `json:"httpTokens,omitempty"`

	// HttpPutResponseHopLimit specifies the hop limit for instance metadata requests
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=64
	HttpPutResponseHopLimit *int32 `json:"httpPutResponseHopLimit,omitempty"`

	// HttpEndpoint enables or disables the metadata service
	// +kubebuilder:default="enabled"
	// +kubebuilder:validation:Enum=enabled;disabled
	// +optional
	HttpEndpoint *string `json:"httpEndpoint,omitempty"`
}

// ECSNodeClassStatus defines the observed state of ECSNodeClass
// +kubebuilder:object:generate=true
// +kubebuilder:object:root=false
type ECSNodeClassStatus struct {
	// Ready indicates whether the ECSNodeClass is ready to provision instances
	// +optional
	Ready bool `json:"ready,omitempty"`

	// VSwitches contains the resolved VSwitch list
	// +optional
	VSwitches []VSwitch `json:"vSwitches,omitempty"`

	// SecurityGroups contains the resolved security group list
	// +optional
	SecurityGroups []SecurityGroup `json:"securityGroups,omitempty"`

	// Images contains the resolved image list
	// +optional
	Images []Image `json:"images,omitempty"`

	// RAMRole is the resolved RAM role
	// +optional
	RAMRole *string `json:"ramRole,omitempty"`

	// Conditions contains the current conditions of the ECSNodeClass
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// VSwitch represents a resolved VSwitch
// +kubebuilder:object:generate=true
// +kubebuilder:object:root=false
type VSwitch struct {
	// ID is the VSwitch ID
	ID string `json:"id"`

	// Zone is the availability zone
	Zone string `json:"zone"`

	// ZoneID is the availability zone ID (alias for Zone)
	ZoneID string `json:"zoneID,omitempty"`

	// AvailableIPAddressCount is the available IP address count
	AvailableIPAddressCount int `json:"availableIPAddressCount,omitempty"`
}

// SecurityGroup represents a resolved security group
// +kubebuilder:object:generate=true
// +kubebuilder:object:root=false
type SecurityGroup struct {
	// ID is the security group ID
	ID string `json:"id"`

	// Name is the security group name
	Name string `json:"name,omitempty"`
}

// Image represents a resolved image
// +kubebuilder:object:generate=true
// +kubebuilder:object:root=false
type Image struct {
	// ID is the image ID
	ID string `json:"id"`

	// Name is the image name
	Name string `json:"name,omitempty"`

	// Architecture is the image architecture
	Architecture string `json:"architecture,omitempty"`
}

// Condition types for ECSNodeClass
const (
	ConditionTypeReady                 = "Ready"
	ConditionTypeVSwitchResolved       = "VSwitchResolved"
	ConditionTypeSecurityGroupResolved = "SecurityGroupResolved"
	ConditionTypeImageResolved         = "ImageResolved"
	ConditionTypeRAMRoleResolved       = "RAMRoleResolved"
)
