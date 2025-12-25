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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/operator/options"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/bootstrap"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/imagefamily"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instance"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instanceprofile"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instancetype"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/launchtemplate"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/pricing"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/securitygroup"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/vswitch"
	"github.com/awslabs/operatorpkg/status"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/karpenter/pkg/events"

	"sigs.k8s.io/controller-runtime/pkg/client"
	coreapis "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
)

// CloudProvider implements the Karpenter CloudProvider interface for Alibaba Cloud
// This is the main integration point between Karpenter Core and Alibaba Cloud
type CloudProvider struct {
	// Kubernetes client for accessing cluster resources
	kubeClient client.Client

	// Providers for Alibaba Cloud resources
	instanceProvider        *instance.Provider
	instanceTypeProvider    *instancetype.Provider
	imageFamilyProvider     *imagefamily.Provider
	vswitchProvider         *vswitch.Provider
	securityGroupProvider   *securitygroup.Provider
	pricingProvider         *pricing.Provider
	launchTemplateProvider  *launchtemplate.Provider
	bootstrapProvider       *bootstrap.Provider
	instanceProfileProvider *instanceprofile.Provider
}

// New creates a new CloudProvider with injected dependencies
func New(
	instanceTypeProvider *instancetype.Provider,
	instanceProvider *instance.Provider,
	recorder events.Recorder,
	kubeClient client.Client,
	imageFamilyProvider *imagefamily.Provider,
	securityGroupProvider *securitygroup.Provider,
	vswitchProvider *vswitch.Provider,
	instanceProfileProvider *instanceprofile.Provider,
	pricingProvider *pricing.Provider,
	launchTemplateProvider *launchtemplate.Provider,
	bootstrapProvider *bootstrap.Provider,
) *CloudProvider {
	return &CloudProvider{
		kubeClient:              kubeClient,
		instanceProvider:        instanceProvider,
		instanceTypeProvider:    instanceTypeProvider,
		imageFamilyProvider:     imageFamilyProvider,
		vswitchProvider:         vswitchProvider,
		securityGroupProvider:   securityGroupProvider,
		pricingProvider:         pricingProvider,
		launchTemplateProvider:  launchTemplateProvider,
		bootstrapProvider:       bootstrapProvider,
		instanceProfileProvider: instanceProfileProvider,
	}
}

const (
	ImageDrift         cloudprovider.DriftReason = "ImageDrift"
	VSwitchDrift       cloudprovider.DriftReason = "VSwitchDrift"
	SecurityGroupDrift cloudprovider.DriftReason = "SecurityGroupDrift"
	NodeClassDrift     cloudprovider.DriftReason = "NodeClassDrift"
)

// Create creates a new instance for the given NodeClaim
// This implements the cloudprovider.CloudProvider interface
func (c *CloudProvider) Create(ctx context.Context, nodeClaim *coreapis.NodeClaim) (*coreapis.NodeClaim, error) {
	// 1. Get ECSNodeClass from NodeClassRef
	nodeClass := &v1alpha1.ECSNodeClass{}
	if err := c.kubeClient.Get(ctx, client.ObjectKey{
		Name: nodeClaim.Spec.NodeClassRef.Name,
	}, nodeClass); err != nil {
		return nil, fmt.Errorf("failed to get nodeclass %s: %w", nodeClaim.Spec.NodeClassRef.Name, err)
	}

	// 2. Verify NodeClass is ready
	nodeClassReady := nodeClass.StatusConditions().Get(status.ConditionReady)
	if nodeClassReady.IsFalse() {
		return nil, cloudprovider.NewNodeClassNotReadyError(fmt.Errorf("node class is not ready: %s", nodeClassReady.Message))
	}
	if nodeClassReady.IsUnknown() {
		return nil, cloudprovider.NewCreateError(fmt.Errorf("resolving nodeclass readiness, nodeclass is in Ready=Unknown: %s", nodeClassReady.Message), "NodeClassReadinessUnknown", "NodeClass is in Ready=Unknown")
	}

	// 3. Get instance types that match requirements
	instanceTypes, err := c.instanceTypeProvider.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list instance types: %w", err)
	}

	// Filter instance types by NodeClaim requirements
	filteredTypes := c.filterInstanceTypesByRequirements(ctx, instanceTypes, nodeClaim.Spec.Requirements)
	if len(filteredTypes) == 0 {
		return nil, fmt.Errorf("no instance types match the requirements")
	}

	// 4. Resolve images from NodeClass
	images := nodeClass.Status.Images
	if len(images) == 0 {
		return nil, fmt.Errorf("no images available in nodeclass status")
	}

	// 5. Get VSwitches from NodeClass status
	vswitches := nodeClass.Status.VSwitches
	if len(vswitches) == 0 {
		return nil, fmt.Errorf("no vswitches available in nodeclass status")
	}

	// 6. Get SecurityGroups from NodeClass status
	securityGroups := nodeClass.Status.SecurityGroups
	if len(securityGroups) == 0 {
		return nil, fmt.Errorf("no security groups available in nodeclass status")
	}

	// 7. Generate UserData using bootstrap provider
	bootstrapOpts := bootstrap.BootstrapOptions{
		ClusterType:     determineClusterType(nodeClass),
		ClusterID:       nodeClass.Spec.ClusterID,
		ClusterName:     nodeClass.Spec.ClusterName,
		ClusterEndpoint: nodeClass.Spec.ClusterEndpoint,
		NodeName:        nodeClaim.Name,
		KubeletConfig:   convertKubeletConfigToBootstrap(nodeClass.Spec.Kubelet),
		Labels:          nodeClaim.Labels,
		Taints:          convertToCoreTaints(nodeClaim.Spec.Taints),
		CustomUserData:  lo.ToPtr(getCustomUserData(nodeClass)),
	}

	userData, err := c.bootstrapProvider.GenerateUserData(bootstrapOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to generate user data: %w", err)
	}

	// 8. Build tags for the instance
	tags := buildInstanceTags(nodeClaim, nodeClass)

	// 9. Create instance using instance provider
	instanceID, err := c.createInstanceWithRetry(ctx, nodeClass, filteredTypes, images, vswitches, securityGroups, userData, tags)
	if err != nil {
		return nil, fmt.Errorf("failed to create instance: %w", err)
	}

	// 10. Get the created instance details
	inst, err := c.instanceProvider.Get(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get created instance: %w", err)
	}

	// 11. Convert instance to NodeClaim
	createdNodeClaim := convertInstanceToNodeClaim(ctx, inst, nodeClaim, filteredTypes)

	// 12. Set NodeClass hash annotation for drift detection
	hash := calculateNodeClassHash(nodeClass)
	setNodeClassHashAnnotation(createdNodeClaim, hash)

	return createdNodeClaim, nil
}

// Delete deletes the instance for the given NodeClaim
func (c *CloudProvider) Delete(ctx context.Context, nodeClaim *coreapis.NodeClaim) error {
	log.FromContext(ctx).Info("CloudProvider.Delete called", "nodeClaimName", nodeClaim.Name, "providerID", nodeClaim.Status.ProviderID)

	// 提取 instanceID 从 ProviderID
	// ProviderID 格式: cn-hangzhou.i-xxxxx
	instanceID := fromProviderID(nodeClaim.Status.ProviderID)
	if instanceID == "" {
		// 如果无法从ProviderID提取instanceID，记录警告日志并返回nil（视为成功删除）
		log.FromContext(ctx).Info("Invalid providerID, treating as successful deletion", "providerID", nodeClaim.Status.ProviderID)
		return nil
	}

	log.FromContext(ctx).Info("Extracted instanceID from providerID", "instanceID", instanceID, "providerID", nodeClaim.Status.ProviderID)

	// 调用已实现的 instanceProvider.Delete()
	err := c.instanceProvider.Delete(ctx, instanceID)
	if err != nil {
		return err
	}

	log.FromContext(ctx).Info("Successfully deleted instance", "instanceID", instanceID)
	return nil
}

// Get retrieves the current state of the instance
func (c *CloudProvider) Get(ctx context.Context, providerID string) (*coreapis.NodeClaim, error) {
	// Extract instanceID from ProviderID
	instanceID := fromProviderID(providerID)
	if instanceID == "" {
		return nil, fmt.Errorf("invalid providerID: %s", providerID)
	}

	// Get instance from provider with caching
	inst, err := c.instanceProvider.Get(ctx, instanceID)
	if err != nil {
		return nil, err
	}

	// Get all instance types to look up capacity
	instanceTypes, err := c.instanceTypeProvider.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list instance types: %w", err)
	}

	// Find matching instance type for capacity information
	var capacity corev1.ResourceList
	var allocatable corev1.ResourceList
	for _, it := range instanceTypes {
		if it.Name == inst.InstanceType {
			var err error
			capacity, allocatable, err = calculateCapacityAndAllocatable(ctx, it)
			if err != nil {
				return nil, err
			}
			break
		}
	}

	// Parse launch time
	launchTime := parseLaunchTime(ctx, inst.InstanceID, inst.CreationTime)

	// Create NodeClaim from instance
	nodeClaim := &coreapis.NodeClaim{}
	nodeClaim.Name = inst.InstanceID
	nodeClaim.Labels = inst.Tags
	nodeClaim.CreationTimestamp = metav1.Time{Time: launchTime}

	// Set Status
	nodeClaim.Status.ProviderID = fmt.Sprintf("%s.%s", inst.Region, inst.InstanceID)
	nodeClaim.Status.Capacity = capacity
	nodeClaim.Status.Allocatable = allocatable
	nodeClaim.Status.ImageID = inst.ImageID

	return nodeClaim, nil
}

// List retrieves all instances managed by Karpenter
func (c *CloudProvider) List(ctx context.Context) ([]*coreapis.NodeClaim, error) {
	// List all Karpenter-managed instances
	tags := map[string]string{
		v1alpha1.TagManagedBy: "karpenter",
	}

	instances, err := c.instanceProvider.List(ctx, tags)
	if err != nil {
		return nil, err
	}

	// Pre-load instance types for capacity lookup
	instanceTypes, err := c.instanceTypeProvider.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list instance types: %w", err)
	}

	// Build instance type map for faster lookup
	instanceTypeMap := make(map[string]*instancetype.InstanceType)
	for _, it := range instanceTypes {
		instanceTypeMap[it.Name] = it
	}

	// Convert all instances to NodeClaims
	nodeClaims := make([]*coreapis.NodeClaim, 0, len(instances))
	for _, inst := range instances {
		// Find instance type capacity
		var capacity corev1.ResourceList
		var allocatable corev1.ResourceList
		if it, ok := instanceTypeMap[inst.InstanceType]; ok {
			// Ignore error here, just skip if calculation fails
			capacity, allocatable, _ = calculateCapacityAndAllocatable(ctx, it)
		}

		// Parse launch time
		launchTime := parseLaunchTime(ctx, inst.InstanceID, inst.CreationTime)

		// Create NodeClaim
		nodeClaim := &coreapis.NodeClaim{}
		nodeClaim.Name = inst.InstanceID
		nodeClaim.Labels = inst.Tags
		nodeClaim.CreationTimestamp = metav1.Time{Time: launchTime}

		// Set Status
		nodeClaim.Status.ProviderID = fmt.Sprintf("%s.%s", inst.Region, inst.InstanceID)
		nodeClaim.Status.Capacity = capacity
		nodeClaim.Status.Allocatable = allocatable
		nodeClaim.Status.ImageID = inst.ImageID

		nodeClaims = append(nodeClaims, nodeClaim)
	}

	return nodeClaims, nil
}

// GetInstanceTypes returns available instance types for the NodePool
func (c *CloudProvider) GetInstanceTypes(ctx context.Context, nodePool *coreapis.NodePool) ([]*cloudprovider.InstanceType, error) {
	logger := log.FromContext(ctx)

	// 1. Get NodeClass from NodePool
	nodeClass := &v1alpha1.ECSNodeClass{}
	if err := c.kubeClient.Get(ctx, client.ObjectKey{
		Name: nodePool.Spec.Template.Spec.NodeClassRef.Name,
	}, nodeClass); err != nil {
		return nil, fmt.Errorf("failed to get nodeclass: %w", err)
	}

	// 2. Verify NodeClass is ready
	nodeClassReady := nodeClass.StatusConditions().Get(status.ConditionReady)
	if nodeClassReady.IsFalse() {
		return nil, cloudprovider.NewNodeClassNotReadyError(fmt.Errorf("node class is not ready: %s", nodeClassReady.Message))
	}
	if nodeClassReady.IsUnknown() {
		return nil, cloudprovider.NewCreateError(fmt.Errorf("resolving nodeclass readiness, nodeclass is in Ready=Unknown: %s", nodeClassReady.Message), "NodeClassReadinessUnknown", "NodeClass is in Ready=Unknown")
	}

	// 3. Get all instance types
	instanceTypes, err := c.instanceTypeProvider.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list instance types: %w", err)
	}

	// Log total instance types found (only at debug level)
	logger.V(1).Info("Found instance types", "count", len(instanceTypes))

	// 4. Filter by NodePool requirements
	filteredTypes := c.filterInstanceTypesByRequirements(
		ctx,
		instanceTypes,
		nodePool.Spec.Template.Spec.Requirements,
	)

	// Log filtered instance types (only at debug level)
	logger.V(1).Info("Filtered instance types", "count", len(filteredTypes))

	// 5. Filter by availability zones (from VSwitches)
	availableZones := make(map[string]bool)
	for _, vs := range nodeClass.Status.VSwitches {
		// Support both Zone and ZoneID fields
		zoneID := vs.ZoneID
		if zoneID == "" {
			zoneID = vs.Zone
		}
		availableZones[zoneID] = true
	}

	// Log available zones (only at debug level)
	logger.V(1).Info("Available zones", "zones", availableZones)

	// 6. Update pricing information (if methods exist)
	// Note: Pricing provider may not have these exact methods, skip for now

	// 7. Convert to core InstanceType format
	coreInstanceTypes := make([]*cloudprovider.InstanceType, 0, len(filteredTypes))
	for _, it := range filteredTypes {
		// Validate instance type has required fields
		if it.CPU == nil || it.Memory == nil {
			logger.V(1).Info("Skipping instance type with nil CPU or Memory", "instanceType", it.Name)
			continue
		}

		// Create capacity ResourceList from CPU and Memory
		capacity := corev1.ResourceList{
			corev1.ResourceCPU:    *it.CPU,
			corev1.ResourceMemory: *it.Memory,
			// 添加pods资源到总容量中
			corev1.ResourcePods: *resource.NewQuantity(110, resource.DecimalSI), // 默认110个pods
		}

		// Create offerings from zones
		offerings := make(cloudprovider.Offerings, 0)
		for zoneID, zoneInfo := range it.Zones {
			if availableZones[zoneID] && zoneInfo.Available {
				offering := &cloudprovider.Offering{
					Price:     0.0, // Pricing not implemented yet
					Available: zoneInfo.Available,
				}
				offerings = append(offerings, offering)
			}
		}
		// Skip instance types with no available offerings
		if len(offerings) == 0 {
			// Log the instance type that has no available offerings (only at debug level)
			logger.V(1).Info("Instance type has no available offerings in specified zones",
				"instanceType", it.Name,
				"instanceZones", it.Zones,
				"availableZones", availableZones)
			continue
		}

		// Log instance type with available offerings (only at debug level)
		logger.V(1).Info("Instance type with available offerings",
			"instanceType", it.Name,
			"offerings", len(offerings))

		// Calculate overhead once and distribute appropriately
		totalOverhead := calculateOverhead(ctx, it)
		// Split overhead among different components
		// This is a simplified approach - in production, each should be configurable
		evictionThreshold := corev1.ResourceList{
			corev1.ResourceMemory: divideQuantity(totalOverhead[corev1.ResourceMemory], 3),
			corev1.ResourceCPU:    divideQuantity(totalOverhead[corev1.ResourceCPU], 3),
		}
		kubeReserved := corev1.ResourceList{
			corev1.ResourceMemory: divideQuantity(totalOverhead[corev1.ResourceMemory], 3),
			corev1.ResourceCPU:    divideQuantity(totalOverhead[corev1.ResourceCPU], 3),
		}
		systemReserved := corev1.ResourceList{
			corev1.ResourceMemory: divideQuantity(totalOverhead[corev1.ResourceMemory], 3),
			corev1.ResourceCPU:    divideQuantity(totalOverhead[corev1.ResourceCPU], 3),
		}

		// Create core InstanceType
		coreIT := &cloudprovider.InstanceType{
			Name:      it.Name,
			Capacity:  capacity,
			Offerings: offerings,
			Overhead: &cloudprovider.InstanceTypeOverhead{
				EvictionThreshold: evictionThreshold,
				KubeReserved:      kubeReserved,
				SystemReserved:    systemReserved,
			},
		}

		coreInstanceTypes = append(coreInstanceTypes, coreIT)
	}

	if len(coreInstanceTypes) == 0 {
		// Log detailed information when no instance types are available (only at debug level)
		logger.V(1).Info("No instance types available after filtering",
			"totalInstanceTypes", len(instanceTypes),
			"filteredInstanceTypes", len(filteredTypes),
			"availableZones", availableZones)
		return nil, fmt.Errorf("no instance types available matching requirements")
	}

	// Log final result (only at debug level)
	logger.V(1).Info("Returning instance types", "count", len(coreInstanceTypes))

	return coreInstanceTypes, nil
}

// RepairPolicies returns the repair policies for Alibaba Cloud nodes
func (c *CloudProvider) RepairPolicies() []cloudprovider.RepairPolicy {
	// For Alibaba Cloud, we don't have specific repair policies yet
	// Return an empty slice for now
	return []cloudprovider.RepairPolicy{}
}

// GetSupportedNodeClasses returns the CloudProvider NodeClass that implements status.Object
func (c *CloudProvider) GetSupportedNodeClasses() []status.Object {
	return []status.Object{&v1alpha1.ECSNodeClass{}}
}

// Name returns the CloudProvider implementation name.
func (c *CloudProvider) Name() string {
	return "alibabacloud"
}

// IsDrifted checks if the node has drifted from its desired configuration
func (c *CloudProvider) IsDrifted(ctx context.Context, nodeClaim *coreapis.NodeClaim) (cloudprovider.DriftReason, error) {
	// 1. Get NodeClass
	nodeClass := &v1alpha1.ECSNodeClass{}
	if err := c.kubeClient.Get(ctx, client.ObjectKey{
		Name: nodeClaim.Spec.NodeClassRef.Name,
	}, nodeClass); err != nil {
		return "", fmt.Errorf("failed to get nodeclass: %w", err)
	}

	// 2. Get instance details
	instanceID := fromProviderID(nodeClaim.Status.ProviderID)
	if instanceID == "" {
		return "", fmt.Errorf("invalid providerID: %s", nodeClaim.Status.ProviderID)
	}

	inst, err := c.instanceProvider.Get(ctx, instanceID)
	if err != nil {
		return "", fmt.Errorf("failed to get instance: %w", err)
	}

	// 3. Check NodeClass hash (most important drift detection)
	currentHash := calculateNodeClassHash(nodeClass)
	annotatedHash := ""
	if nodeClaim.Annotations != nil {
		annotatedHash = nodeClaim.Annotations[v1alpha1.AnnotationECSNodeClassHash]
	}

	if currentHash != annotatedHash {
		return NodeClassDrift, nil
	}

	// 4. Check if image is still in allowed list
	if !isImageAllowed(inst.ImageID, nodeClass.Status.Images) {
		return ImageDrift, nil
	}

	if !isVSwitchAllowed(inst.VSwitchID, nodeClass.Status.VSwitches) {
		return VSwitchDrift, nil
	}

	if !isSecurityGroupAllowed(inst.SecurityGroupIDs, nodeClass.Status.SecurityGroups) {
		return SecurityGroupDrift, nil
	}

	return "", nil
}

// fromProviderID extracts the instance ID from a provider ID
// Provider ID format: region.instance-id
// Example: cn-hangzhou.i-bp1abc123
func fromProviderID(providerID string) string {
	if providerID == "" {
		return ""
	}

	// Split by '.'
	parts := strings.Split(providerID, ".")

	// Check if we have enough parts
	// Expected format: region.instance-id, at lease 2 parts
	if len(parts) < 2 {
		return ""
	}

	// Return the last part which is the instance ID
	instanceID := parts[len(parts)-1]
	return instanceID
}

// Helper functions for Create

func determineClusterType(nodeClass *v1alpha1.ECSNodeClass) bootstrap.ClusterType {
	if nodeClass.Spec.ClusterID != "" {
		return bootstrap.ACKClusterType
	}
	return bootstrap.KubeadmClusterType
}

func convertToCoreTaints(taints []corev1.Taint) []corev1.Taint {
	return taints
}

func getCustomUserData(nodeClass *v1alpha1.ECSNodeClass) string {
	if nodeClass.Spec.UserData != nil {
		return *nodeClass.Spec.UserData
	}
	return ""
}

func buildInstanceTags(nodeClaim *coreapis.NodeClaim, nodeClass *v1alpha1.ECSNodeClass) map[string]string {
	tags := make(map[string]string)

	// Add Karpenter management tag
	tags[v1alpha1.TagManagedBy] = "true"
	// NodePool name will be extracted from labels if needed

	// Add NodeClaim labels as tags
	for k, v := range nodeClaim.Labels {
		tags[k] = v
	}

	return tags
}

// filterInstanceTypesByRequirements converts Karpenter requirements to instancetype requirements and filters
func (c *CloudProvider) filterInstanceTypesByRequirements(ctx context.Context, instanceTypes []*instancetype.InstanceType, requirements []coreapis.NodeSelectorRequirementWithMinValues) []*instancetype.InstanceType {
	// Convert Karpenter requirements to instancetype requirements
	var itRequirements []instancetype.InstanceTypeRequirement
	for _, req := range requirements {
		// Convert operator
		var operator instancetype.InstanceTypeOperator
		switch req.Operator {
		case corev1.NodeSelectorOpIn:
			operator = instancetype.InstanceTypeOperatorIn
		case corev1.NodeSelectorOpNotIn:
			operator = instancetype.InstanceTypeOperatorNotIn
		case corev1.NodeSelectorOpExists:
			operator = instancetype.InstanceTypeOperatorExists
		case corev1.NodeSelectorOpDoesNotExist:
			operator = instancetype.InstanceTypeOperatorDoesNotExist
		default:
			// Unknown operator, skip this requirement
			continue
		}

		itRequirements = append(itRequirements, instancetype.InstanceTypeRequirement{
			Key:      req.Key,
			Operator: operator,
			Values:   req.Values,
		})
	}

	// Use instancetype.Provider.Filter for filtering
	return c.instanceTypeProvider.Filter(ctx, instanceTypes, itRequirements)
}

func (c *CloudProvider) createInstanceWithRetry(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass, instanceTypes []*instancetype.InstanceType, images []v1alpha1.Image, vswitches []v1alpha1.VSwitch, securityGroups []v1alpha1.SecurityGroup, userData string, tags map[string]string) (string, error) {
	// This is a simplified implementation that delegates to the instance provider
	// In a production implementation, this should implement retry logic and capacity fallback

	if len(instanceTypes) == 0 || len(images) == 0 || len(vswitches) == 0 || len(securityGroups) == 0 {
		return "", fmt.Errorf("missing required parameters for instance creation")
	}

	// Select the first instance type, image, vswitch, and security group for simplicity
	// In a real implementation, this should implement proper selection logic
	instanceType := instanceTypes[0]
	image := images[0]
	vswitch := vswitches[0]
	securityGroup := securityGroups[0]

	// Build CreateOptions
	opts := instance.CreateOptions{
		InstanceType: instanceType.Name,
		ImageID:      image.ID,
		VSwitchID:    vswitch.ID,
		SecurityGroupIDs: []string{
			securityGroup.ID,
		},
		UserData: userData,
		Tags:     tags,
		SystemDisk: instance.SystemDisk{
			Category:         "cloud_essd", // Default category
			Size:             40,           // Default size in GB
			PerformanceLevel: "PL0",
		},
	}
	if nodeClass.Spec.SystemDisk != nil {
		opts.SystemDisk.Category = nodeClass.Spec.SystemDisk.Category
		if nodeClass.Spec.SystemDisk.Size != nil {
			opts.SystemDisk.Size = *nodeClass.Spec.SystemDisk.Size
		}
		if nodeClass.Spec.SystemDisk.PerformanceLevel != nil {
			opts.SystemDisk.PerformanceLevel = *nodeClass.Spec.SystemDisk.PerformanceLevel
		}
	}
	if nodeClass.Spec.DataDisks != nil {
		opts.DataDisks = []instance.DataDisk{}
		for _, disk := range nodeClass.Spec.DataDisks {
			dataDisk := instance.DataDisk{
				Category: disk.Category,
				Size:     disk.Size,
			}
			if disk.PerformanceLevel != nil {
				dataDisk.PerformanceLevel = *disk.PerformanceLevel
			}
			opts.DataDisks = append(opts.DataDisks, dataDisk)
		}
	}
	if nodeClass.Spec.Tags != nil {
		opts.Tags = nodeClass.Spec.Tags
	}
	if nodeClass.Spec.SpotStrategy != nil {
		opts.SpotStrategy = *nodeClass.Spec.SpotStrategy
	}
	if nodeClass.Spec.SpotPriceLimit != nil {
		opts.SpotPriceLimit = *nodeClass.Spec.SpotPriceLimit
	}

	// Call the instance provider's Create method
	instanceID, err := c.instanceProvider.Create(ctx, opts)
	if err != nil {
		return "", fmt.Errorf("failed to create instance: %w", err)
	}

	return instanceID, nil
}

func convertInstanceToNodeClaim(ctx context.Context, inst *instance.Instance, original *coreapis.NodeClaim, instanceTypes []*instancetype.InstanceType) *coreapis.NodeClaim {
	labels := make(map[string]string)
	// Find instance type info
	var capacity corev1.ResourceList
	var allocatable corev1.ResourceList

	for _, it := range instanceTypes {
		if it.Name == inst.InstanceType {
			capacity, allocatable, _ = calculateCapacityAndAllocatable(ctx, it)
			break
		}
	}

	// Copy original NodeClaim and update status
	nodeClaim := original.DeepCopy()
	nodeClaim.Status.ProviderID = fmt.Sprintf("%s.%s", inst.Region, inst.InstanceID)
	nodeClaim.Status.Capacity = capacity
	nodeClaim.Status.Allocatable = allocatable

	labels[corev1.LabelTopologyZone] = inst.Zone
	labels[v1alpha1.LabelCapacityType] = inst.CapacityType
	labels[v1alpha1.LabelInstanceType] = inst.InstanceType
	if v, ok := inst.Tags[coreapis.NodePoolLabelKey]; ok {
		labels[coreapis.NodePoolLabelKey] = v
	}
	nodeClaim.Labels = labels
	return nodeClaim
}

func setNodeClassHashAnnotation(nodeClaim *coreapis.NodeClaim, hash string) {
	if nodeClaim.Annotations == nil {
		nodeClaim.Annotations = make(map[string]string)
	}
	nodeClaim.Annotations[v1alpha1.AnnotationECSNodeClassHash] = hash
	nodeClaim.Annotations[v1alpha1.AnnotationECSNodeClassHashVersion] = "v1"
}

// convertKubeletConfigToBootstrap converts v1alpha1.KubeletConfiguration to bootstrap.KubeletConfiguration
func convertKubeletConfigToBootstrap(config *v1alpha1.KubeletConfiguration) *bootstrap.KubeletConfiguration {
	if config == nil {
		return nil
	}

	return &bootstrap.KubeletConfiguration{
		MaxPods:                     config.MaxPods,
		PodsPerCore:                 config.PodsPerCore,
		CPUCFSQuota:                 config.CPUCFSQuota,
		SystemReserved:              config.SystemReserved,
		KubeReserved:                config.KubeReserved,
		EvictionHard:                config.EvictionHard,
		EvictionSoft:                config.EvictionSoft,
		EvictionSoftGracePeriod:     config.EvictionSoftGracePeriod,
		EvictionMaxPodGracePeriod:   config.EvictionMaxPodGracePeriod,
		ImageGCHighThresholdPercent: config.ImageGCHighThresholdPercent,
		ImageGCLowThresholdPercent:  config.ImageGCLowThresholdPercent,
		ClusterDNS:                  config.ClusterDNS,
	}
}

// Drift detection helper functions (from drift.go)

func isImageAllowed(imageID string, allowedImages []v1alpha1.Image) bool {
	for _, img := range allowedImages {
		if img.ID == imageID {
			return true
		}
	}
	return false
}

func isVSwitchAllowed(vswitchID string, allowedVSwitches []v1alpha1.VSwitch) bool {
	for _, vs := range allowedVSwitches {
		if vs.ID == vswitchID {
			return true
		}
	}
	return false
}

func isSecurityGroupAllowed(instanceSGs []string, allowedSGs []v1alpha1.SecurityGroup) bool {
	// Build allowed IDs map
	allowedIDs := make(map[string]bool)
	for _, sg := range allowedSGs {
		allowedIDs[sg.ID] = true
	}

	// Check if all instance security groups are in the allowed list
	for _, sgID := range instanceSGs {
		if !allowedIDs[sgID] {
			return false
		}
	}

	return true
}

// calculateNodeClassHash calculates a hash of the ECSNodeClass configuration
// This hash is used for drift detection - when the NodeClass config changes,
// the hash will change, triggering node replacement
func calculateNodeClassHash(nodeClass *v1alpha1.ECSNodeClass) string {
	// NodeClassHashInput contains the fields that should trigger drift when changed
	type NodeClassHashInput struct {
		VSwitchSelectorTerms       []v1alpha1.VSwitchSelectorTerm       `json:"vSwitchSelectorTerms"`
		SecurityGroupSelectorTerms []v1alpha1.SecurityGroupSelectorTerm `json:"securityGroupSelectorTerms"`
		ImageSelectorTerms         []v1alpha1.ImageSelectorTerm         `json:"imageSelectorTerms"`
		ImageFamily                *string                              `json:"imageFamily,omitempty"`
		UserData                   *string                              `json:"userData,omitempty"`
		Kubelet                    *v1alpha1.KubeletConfiguration       `json:"kubelet,omitempty"`
		Tags                       map[string]string                    `json:"tags,omitempty"`
		Role                       *string                              `json:"role,omitempty"`
	}

	hashInput := NodeClassHashInput{
		VSwitchSelectorTerms:       nodeClass.Spec.VSwitchSelectorTerms,
		SecurityGroupSelectorTerms: nodeClass.Spec.SecurityGroupSelectorTerms,
		ImageSelectorTerms:         nodeClass.Spec.ImageSelectorTerms,
		UserData:                   nodeClass.Spec.UserData,
		Kubelet:                    nodeClass.Spec.Kubelet,
		Tags:                       nodeClass.Spec.Tags,
		Role:                       nodeClass.Spec.Role,
	}

	// Serialize to JSON with deterministic ordering
	jsonData, err := json.Marshal(hashInput)
	if err != nil {
		// This should never happen with valid structs
		// Return a fallback hash based on object generation
		return fmt.Sprintf("error-%d", nodeClass.Generation)
	}

	// Calculate SHA-256 hash
	hash := sha256.Sum256(jsonData)

	// Return first 16 characters (64 bits) as hex string
	return hex.EncodeToString(hash[:8])
}

// calculateOverhead calculates the overhead for a given instance type based on its capacity
// Following AWS Karpenter's approach with vmMemoryOverheadPercent
func calculateOverhead(ctx context.Context, instanceType *instancetype.InstanceType) corev1.ResourceList {
	// Get the options
	opts := options.FromContext(ctx)

	// Calculate memory overhead based on percentage
	memoryOverhead := resource.Quantity{}
	if instanceType.Memory != nil {
		// Calculate overhead as a percentage of total memory
		overheadBytes := int64(math.Ceil(float64(instanceType.Memory.Value()) * opts.VMMemoryOverheadPercent))
		memoryOverhead = *resource.NewQuantity(overheadBytes, resource.BinarySI)

		// Ensure we have at least the fixed overhead as minimum
		fixedOverhead, err := resource.ParseQuantity(opts.VMMemoryOverhead)
		if err != nil {
			// Log error and use a safe default
			log.FromContext(ctx).V(1).Info("Failed to parse VMMemoryOverhead, using default",
				"value", opts.VMMemoryOverhead,
				"error", err)
			fixedOverhead = resource.MustParse("100Mi")
		}
		if memoryOverhead.Cmp(fixedOverhead) < 0 {
			memoryOverhead = fixedOverhead
		}
	}

	// For CPU, we keep a fixed overhead similar to AWS approach
	cpuOverhead := resource.MustParse("100m")

	// For pods, use a reasonable default
	podsOverhead := resource.MustParse("10") // 保留10个pods用于系统使用

	return corev1.ResourceList{
		corev1.ResourceCPU:    cpuOverhead,
		corev1.ResourceMemory: memoryOverhead,
		corev1.ResourcePods:   podsOverhead,
	}
}

// subtractQuantity subtracts b from a, returning zero if result is negative
func subtractQuantity(a, b resource.Quantity) resource.Quantity {
	result := a.DeepCopy()
	result.Sub(b)
	if result.Sign() < 0 {
		return *resource.NewQuantity(0, result.Format)
	}
	return result
}

// divideQuantity divides quantity by divisor
func divideQuantity(q resource.Quantity, divisor int64) resource.Quantity {
	if divisor == 0 {
		return *resource.NewQuantity(0, q.Format)
	}
	value := q.Value() / divisor
	return *resource.NewQuantity(value, q.Format)
}

// calculateCapacityAndAllocatable calculates capacity and allocatable resources for an instance type
func calculateCapacityAndAllocatable(ctx context.Context, it *instancetype.InstanceType) (capacity, allocatable corev1.ResourceList, err error) {
	if it.CPU == nil || it.Memory == nil {
		return nil, nil, fmt.Errorf("instance type %s has nil CPU or Memory", it.Name)
	}

	// Create capacity ResourceList
	capacity = corev1.ResourceList{
		corev1.ResourceCPU:    *it.CPU,
		corev1.ResourceMemory: *it.Memory,
		corev1.ResourcePods:   *resource.NewQuantity(110, resource.DecimalSI),
	}

	// Calculate allocatable by subtracting overhead
	overhead := calculateOverhead(ctx, it)
	allocatable = corev1.ResourceList{
		corev1.ResourceCPU:    subtractQuantity(*it.CPU, overhead[corev1.ResourceCPU]),
		corev1.ResourceMemory: subtractQuantity(*it.Memory, overhead[corev1.ResourceMemory]),
		corev1.ResourcePods:   subtractQuantity(*resource.NewQuantity(110, resource.DecimalSI), overhead[corev1.ResourcePods]),
	}

	return capacity, allocatable, nil
}

// parseLaunchTime parses instance creation time with fallback to current time
func parseLaunchTime(ctx context.Context, instanceID, creationTime string) time.Time {
	launchTime, err := time.Parse(time.RFC3339, creationTime)
	if err != nil {
		// Log warning and use current time as fallback
		log.FromContext(ctx).V(1).Info("Failed to parse instance creation time, using current time",
			"instanceID", instanceID,
			"creationTime", creationTime,
			"error", err)
		return time.Now()
	}
	return launchTime
}
