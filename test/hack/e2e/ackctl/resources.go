package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	vpc "github.com/alibabacloud-go/vpc-20160428/v7/client"
)

type ClusterResources struct {
	VSwitchIDs                      []string
	Zones                           []string
	SecurityGroupIDs                []string
	RAMRole                         string
	WorkerImageID                   string
	GPUInstanceTypes                []string
	GPUZones                        []string
	CapacityReservationID           string
	CapacityReservationInstanceType string
}

type GPUResourceCandidate struct {
	RegionID     string
	ZoneID       string
	InstanceType string
}

type GPUDiscoveryOptions struct {
	Regions            []string
	InstanceTypes      []string
	InstanceChargeType string
	SystemDiskCategory string
}

func EnsureCapacityReservation(ctx context.Context, cfg *Config, manifest *Manifest, resources ClusterResources) (string, string, error) {
	client, err := newECSClient(cfg)
	if err != nil {
		return "", "", err
	}
	if id := strings.TrimSpace(firstNonEmpty(os.Getenv("TEST_CAPACITY_RESERVATION_ID"), resources.CapacityReservationID)); id != "" {
		active, status, err := capacityReservationActive(client, cfg, id)
		if err != nil {
			return "", "", err
		}
		if active {
			return id, firstNonEmpty(resources.CapacityReservationInstanceType, os.Getenv("TEST_CAPACITY_RESERVATION_INSTANCE_TYPE"), capacityReservationInstanceType(cfg)), nil
		}
		if strings.TrimSpace(os.Getenv("TEST_CAPACITY_RESERVATION_ID")) != "" {
			return "", "", fmt.Errorf("capacity reservation %s is not active, status=%s", id, status)
		}
	}
	if id := existingManifestCapacityReservationID(manifest); id != "" {
		active, status, err := capacityReservationActive(client, cfg, id)
		if err != nil {
			return "", "", err
		}
		if active {
			return id, firstNonEmpty(resources.CapacityReservationInstanceType, os.Getenv("TEST_CAPACITY_RESERVATION_INSTANCE_TYPE"), capacityReservationInstanceType(cfg)), nil
		}
		markManifestCapacityReservationDeleted(manifest, id)
		fmt.Fprintf(os.Stderr, "capacity reservation %s is not active, status=%s; creating a new reservation\n", id, status)
	}
	requests, err := BuildCapacityReservationRequests(cfg, manifest, resources)
	if err != nil {
		return "", "", err
	}
	var resp *ecs.CreateCapacityReservationResponse
	var request *ecs.CreateCapacityReservationRequest
	var createErrs []error
	for _, candidate := range requests {
		request = candidate
		resp, err = client.CreateCapacityReservation(candidate)
		if err == nil {
			break
		}
		createErrs = append(createErrs, fmt.Errorf("%s/%s: %w", tea.StringValue(candidate.ZoneId[0]), tea.StringValue(candidate.InstanceType), err))
		if !isRetryableCapacityReservationCandidateError(err) {
			return "", "", fmt.Errorf("create capacity reservation: %w", err)
		}
		resp = nil
	}
	if resp == nil {
		return "", "", fmt.Errorf("create capacity reservation failed for all candidates: %s", joinErrorMessages(createErrs))
	}
	if resp == nil || resp.Body == nil || strings.TrimSpace(tea.StringValue(resp.Body.PrivatePoolOptionsId)) == "" {
		return "", "", fmt.Errorf("create capacity reservation returned empty private pool id")
	}
	id := strings.TrimSpace(tea.StringValue(resp.Body.PrivatePoolOptionsId))
	upsertManifestResource(manifest, Resource{
		Type:         "capacity-reservation",
		ID:           id,
		Name:         tea.StringValue(request.PrivatePoolOptions.Name),
		SupportsTags: true,
		State:        ResourceStatePending,
	})
	if err := waitForActiveCapacityReservation(ctx, client, cfg, id, 5*time.Minute); err != nil {
		return "", "", err
	}
	return id, tea.StringValue(request.InstanceType), nil
}

func existingManifestCapacityReservationID(manifest *Manifest) string {
	if manifest == nil {
		return ""
	}
	for _, resource := range manifest.Resources {
		if resource.Type == "capacity-reservation" && resource.State != ResourceStateDeleted && strings.TrimSpace(resource.ID) != "" {
			return strings.TrimSpace(resource.ID)
		}
	}
	return ""
}

func markManifestCapacityReservationDeleted(manifest *Manifest, id string) {
	if manifest == nil {
		return
	}
	for i := range manifest.Resources {
		if manifest.Resources[i].Type == "capacity-reservation" && manifest.Resources[i].ID == id {
			manifest.Resources[i].State = ResourceStateDeleted
		}
	}
}

func BuildCapacityReservationRequest(cfg *Config, manifest *Manifest, resources ClusterResources) (*ecs.CreateCapacityReservationRequest, error) {
	requests, err := BuildCapacityReservationRequests(cfg, manifest, resources)
	if err != nil {
		return nil, err
	}
	return requests[0], nil
}

func BuildCapacityReservationRequests(cfg *Config, manifest *Manifest, resources ClusterResources) ([]*ecs.CreateCapacityReservationRequest, error) {
	zones := uniqueNonEmpty(append(append(append([]string{}, resources.Zones...), resources.GPUZones...), cfg.Cluster.ZoneIDs...))
	if len(zones) == 0 {
		return nil, fmt.Errorf("capacity reservation requires at least one zone")
	}
	instanceTypes := capacityReservationInstanceTypes(cfg)
	if len(instanceTypes) == 0 {
		return nil, fmt.Errorf("capacity reservation requires an instance type")
	}

	name := strings.TrimSpace(manifest.ClusterName)
	if name == "" {
		name = "ack-e2e"
	}
	name += "-capacity-reservation"
	endTime := time.Now().UTC().Add(6 * time.Hour).Format("2006-01-02T15:04:05Z")
	requests := make([]*ecs.CreateCapacityReservationRequest, 0, len(zones)*len(instanceTypes))
	for _, zone := range zones {
		for _, instanceType := range instanceTypes {
			tokenSeed := fmt.Sprintf("%s-%s-%s-%d", name, zone, instanceType, time.Now().UTC().UnixNano())
			clientToken := strings.NewReplacer(".", "-", "_", "-", "/", "-").Replace(tokenSeed)
			requests = append(requests, &ecs.CreateCapacityReservationRequest{
				RegionId:       tea.String(cfg.AlibabaCloud.RegionID),
				ZoneId:         []*string{tea.String(zone)},
				InstanceType:   tea.String(instanceType),
				InstanceAmount: tea.Int32(1),
				Platform:       tea.String("Linux"),
				EndTimeType:    tea.String("Limited"),
				EndTime:        tea.String(endTime),
				ClientToken:    tea.String(clientToken),
				Description:    tea.String(name),
				PrivatePoolOptions: &ecs.CreateCapacityReservationRequestPrivatePoolOptions{
					MatchCriteria: tea.String("Target"),
					Name:          tea.String(name),
				},
				Tag: capacityReservationTags(manifest),
			})
		}
	}
	return requests, nil
}

func isRetryableCapacityReservationCandidateError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	retryableCodes := []string{
		"InvalidResourceType.NotSupported",
		"OperationDenied.NoStock",
		"ResourceNotAvailable",
		"Zone.NotOnSale",
		"Throttling",
	}
	for _, code := range retryableCodes {
		if strings.Contains(message, code) {
			return true
		}
	}
	return false
}

func joinErrorMessages(errs []error) string {
	values := make([]string, 0, len(errs))
	for _, err := range errs {
		if err != nil {
			values = append(values, err.Error())
		}
	}
	return strings.Join(values, "; ")
}

func capacityReservationInstanceType(cfg *Config) string {
	values := capacityReservationInstanceTypes(cfg)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func capacityReservationInstanceTypes(cfg *Config) []string {
	if values := envList("TEST_CAPACITY_RESERVATION_INSTANCE_TYPES"); len(values) > 0 {
		return values
	}
	if instanceType := strings.TrimSpace(os.Getenv("TEST_CAPACITY_RESERVATION_INSTANCE_TYPE")); instanceType != "" {
		return []string{instanceType}
	}
	if values := envList("TEST_INSTANCE_TYPES"); len(values) > 0 {
		return values
	}
	return []string{"ecs.c9i.large"}
}

func capacityReservationTags(manifest *Manifest) []*ecs.CreateCapacityReservationRequestTag {
	keys := make([]string, 0, len(manifest.OwnershipTags))
	for key := range manifest.OwnershipTags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	tags := make([]*ecs.CreateCapacityReservationRequestTag, 0, len(keys))
	for _, key := range keys {
		tags = append(tags, &ecs.CreateCapacityReservationRequestTag{
			Key:   tea.String(key),
			Value: tea.String(manifest.OwnershipTags[key]),
		})
	}
	return tags
}

func waitForActiveCapacityReservation(ctx context.Context, client *ecs.Client, cfg *Config, id string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		active, status, err := capacityReservationActive(client, cfg, id)
		if err != nil {
			return err
		}
		if active {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("capacity reservation %s did not become active within %s, last status=%s", id, timeout, status)
		case <-ticker.C:
		}
	}
}

func capacityReservationActive(client *ecs.Client, cfg *Config, id string) (bool, string, error) {
	resp, err := client.DescribeCapacityReservations(&ecs.DescribeCapacityReservationsRequest{
		RegionId: tea.String(cfg.AlibabaCloud.RegionID),
		Status:   tea.String("All"),
		PrivatePoolOptions: &ecs.DescribeCapacityReservationsRequestPrivatePoolOptions{
			Ids: tea.String(fmt.Sprintf("[\"%s\"]", id)),
		},
	})
	if err != nil {
		return false, "", fmt.Errorf("describe capacity reservation %s: %w", id, err)
	}
	if resp == nil || resp.Body == nil || resp.Body.CapacityReservationSet == nil {
		return false, "NotFound", nil
	}
	for _, item := range resp.Body.CapacityReservationSet.CapacityReservationItem {
		if item == nil || tea.StringValue(item.PrivatePoolOptionsId) != id {
			continue
		}
		status := tea.StringValue(item.Status)
		return strings.EqualFold(status, "Active"), status, nil
	}
	return false, "NotFound", nil
}

func DiscoverClusterResources(ctx context.Context, cfg *Config, clusterID string) (ClusterResources, error) {
	_ = ctx
	client, err := newCSClient(cfg)
	if err != nil {
		return ClusterResources{}, err
	}
	resp, err := client.DescribeClusterDetail(tea.String(clusterID))
	if err != nil {
		return ClusterResources{}, fmt.Errorf("describe ACK cluster detail: %w", err)
	}
	if resp == nil || resp.Body == nil {
		return ClusterResources{}, fmt.Errorf("describe ACK cluster detail returned empty body")
	}
	resources := ExtractClusterResources(resp.Body)
	if len(resources.VSwitchIDs) == 0 {
		return ClusterResources{}, fmt.Errorf("ACK cluster %s has no discoverable vswitch IDs", clusterID)
	}
	if len(resources.SecurityGroupIDs) == 0 {
		return ClusterResources{}, fmt.Errorf("ACK cluster %s has no discoverable security group IDs", clusterID)
	}
	if imageID, err := DiscoverWorkerImageID(ctx, cfg, clusterID); err != nil {
		return ClusterResources{}, err
	} else {
		resources.WorkerImageID = imageID
	}
	if zones, err := DiscoverVSwitchZones(ctx, cfg, resources.VSwitchIDs); err != nil {
		return ClusterResources{}, err
	} else {
		resources.Zones = zones
	}
	return resources, nil
}

func DiscoverVSwitchZones(ctx context.Context, cfg *Config, vSwitchIDs []string) ([]string, error) {
	_ = ctx
	client, err := newVPCClient(cfg)
	if err != nil {
		return nil, err
	}
	var zones []string
	for _, id := range vSwitchIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		resp, err := client.DescribeVSwitches(&vpc.DescribeVSwitchesRequest{
			RegionId:  tea.String(cfg.AlibabaCloud.RegionID),
			VSwitchId: tea.String(id),
		})
		if err != nil {
			return nil, fmt.Errorf("describe vswitch %s: %w", id, err)
		}
		zones = appendVSwitchZones(zones, resp)
	}
	return zones, nil
}

func DiscoverWorkerImageID(ctx context.Context, cfg *Config, clusterID string) (string, error) {
	_ = ctx
	client, err := newECSClient(cfg)
	if err != nil {
		return "", err
	}
	resp, err := client.DescribeInstances(&ecs.DescribeInstancesRequest{
		RegionId: tea.String(cfg.AlibabaCloud.RegionID),
		Tag: []*ecs.DescribeInstancesRequestTag{
			{Key: tea.String("ack.aliyun.com"), Value: tea.String(clusterID)},
		},
	})
	if err != nil {
		return "", fmt.Errorf("describe ACK worker instances: %w", err)
	}
	imageID := ExtractWorkerImageID(resp)
	if imageID == "" || strings.HasPrefix(imageID, "m-") {
		return imageID, nil
	}
	images, err := client.DescribeImages(&ecs.DescribeImagesRequest{
		RegionId:  tea.String(cfg.AlibabaCloud.RegionID),
		ImageName: tea.String(imageID),
		Status:    tea.String("Available"),
		PageSize:  tea.Int32(10),
	})
	if err != nil {
		return "", fmt.Errorf("resolve ACK worker image name %s: %w", imageID, err)
	}
	if resolved := ExtractImageIDFromImageNameLookup(images); resolved != "" {
		return resolved, nil
	}
	images, err = client.DescribeImages(&ecs.DescribeImagesRequest{
		RegionId:    tea.String(cfg.AlibabaCloud.RegionID),
		ImageFamily: tea.String(defaultImageFamily()),
		Status:      tea.String("Available"),
		PageSize:    tea.Int32(10),
	})
	if err != nil {
		return "", fmt.Errorf("resolve default worker image family %s: %w", defaultImageFamily(), err)
	}
	if resolved := ExtractImageIDFromImageNameLookup(images); resolved != "" {
		return resolved, nil
	}
	return imageID, nil
}

func DiscoverGPUCapacity(ctx context.Context, cfg *Config, opts GPUDiscoveryOptions) ([]GPUResourceCandidate, error) {
	_ = ctx
	regions := opts.Regions
	if len(regions) == 0 {
		var err error
		regions, err = DiscoverECSRegions(ctx, cfg)
		if err != nil {
			return nil, err
		}
	}
	instanceTypes := opts.InstanceTypes
	if len(instanceTypes) == 0 {
		instanceTypes = defaultGPUDiscoveryInstanceTypes()
	}
	var candidates []GPUResourceCandidate
	for _, region := range uniqueNonEmpty(regions) {
		regionCfg := *cfg
		regionCfg.AlibabaCloud.RegionID = region
		client, err := newECSClient(&regionCfg)
		if err != nil {
			return nil, err
		}
		for _, instanceType := range uniqueNonEmpty(instanceTypes) {
			resp, err := client.DescribeAvailableResource(&ecs.DescribeAvailableResourceRequest{
				RegionId:            tea.String(region),
				DestinationResource: tea.String("InstanceType"),
				ResourceType:        tea.String("instance"),
				InstanceChargeType:  tea.String(defaultString(opts.InstanceChargeType, defaultString(cfg.Cluster.NodePool.InstanceChargeType, "PostPaid"))),
				SystemDiskCategory:  tea.String(defaultString(opts.SystemDiskCategory, defaultString(cfg.Cluster.NodePool.SystemDiskCategory, "cloud_essd"))),
				NetworkCategory:     tea.String("vpc"),
				InstanceType:        tea.String(instanceType),
			})
			if err != nil {
				return nil, fmt.Errorf("describe GPU capacity for %s in %s: %w", instanceType, region, err)
			}
			candidates = appendGPUCandidates(candidates, ExtractAvailableGPUResources(resp, []string{instanceType})...)
		}
	}
	return candidates, nil
}

func DiscoverECSRegions(ctx context.Context, cfg *Config) ([]string, error) {
	_ = ctx
	client, err := newECSClient(cfg)
	if err != nil {
		return nil, err
	}
	resp, err := client.DescribeRegions(&ecs.DescribeRegionsRequest{
		ResourceType:       tea.String("instance"),
		InstanceChargeType: tea.String(defaultString(cfg.Cluster.NodePool.InstanceChargeType, "PostPaid")),
	})
	if err != nil {
		return nil, fmt.Errorf("describe ECS regions: %w", err)
	}
	return ExtractAvailableRegionIDs(resp), nil
}

func ExtractAvailableRegionIDs(resp *ecs.DescribeRegionsResponse) []string {
	if resp == nil || resp.Body == nil || resp.Body.Regions == nil {
		return nil
	}
	var regions []string
	for _, region := range resp.Body.Regions.Region {
		if region == nil || region.RegionId == nil {
			continue
		}
		if region.Status != nil && !strings.EqualFold(strings.TrimSpace(*region.Status), "available") {
			continue
		}
		regions = appendIfNotEmpty(regions, *region.RegionId)
	}
	return regions
}

func ExtractAvailableGPUResources(resp *ecs.DescribeAvailableResourceResponse, allowedInstanceTypes []string) []GPUResourceCandidate {
	if resp == nil || resp.Body == nil || resp.Body.AvailableZones == nil {
		return nil
	}
	allowed := stringSet(allowedInstanceTypes)
	var candidates []GPUResourceCandidate
	for _, zone := range resp.Body.AvailableZones.AvailableZone {
		if zone == nil || zone.ZoneId == nil || zone.RegionId == nil {
			continue
		}
		if !hasStock(zone.Status, zone.StatusCategory) {
			continue
		}
		resources := zone.AvailableResources
		if resources == nil {
			continue
		}
		for _, resource := range resources.AvailableResource {
			if resource == nil || resource.Type == nil || !strings.EqualFold(*resource.Type, "InstanceType") || resource.SupportedResources == nil {
				continue
			}
			for _, supported := range resource.SupportedResources.SupportedResource {
				if supported == nil || supported.Value == nil {
					continue
				}
				instanceType := strings.TrimSpace(*supported.Value)
				if instanceType == "" || !isAllowedGPUInstanceType(instanceType, allowed) || !hasStock(supported.Status, supported.StatusCategory) {
					continue
				}
				candidates = appendGPUCandidates(candidates, GPUResourceCandidate{
					RegionID:     strings.TrimSpace(*zone.RegionId),
					ZoneID:       strings.TrimSpace(*zone.ZoneId),
					InstanceType: instanceType,
				})
			}
		}
	}
	return candidates
}

func defaultGPUDiscoveryInstanceTypes() []string {
	return []string{
		"ecs.gn6v-c8g1.2xlarge",
		"ecs.gn6i-c4g1.xlarge",
		"ecs.gn7i-c8g1.2xlarge",
		"ecs.gn7e-c16g1.4xlarge",
	}
}

func applyGPUDiscovery(cfg *Config, resources *ClusterResources, candidates []GPUResourceCandidate) error {
	if len(candidates) == 0 {
		return fmt.Errorf("no GPU capacity found for candidate instance types")
	}
	selected := candidates[0]
	if cfg.Cluster.VPCID != "" && cfg.AlibabaCloud.RegionID != "" && cfg.AlibabaCloud.RegionID != selected.RegionID {
		return fmt.Errorf("GPU capacity was found in %s, but config pins VPC %s in %s", selected.RegionID, cfg.Cluster.VPCID, cfg.AlibabaCloud.RegionID)
	}
	if hasPinnedVSwitches(cfg.Cluster) && cfg.AlibabaCloud.RegionID != "" && cfg.AlibabaCloud.RegionID != selected.RegionID {
		return fmt.Errorf("GPU capacity was found in %s, but config pins VSwitches in %s", selected.RegionID, cfg.AlibabaCloud.RegionID)
	}
	cfg.AlibabaCloud.RegionID = selected.RegionID
	resources.GPUInstanceTypes = []string{selected.InstanceType}
	cfg.Cluster.NodePool.InstanceTypes = []string{selected.InstanceType}
	for _, candidate := range candidates {
		if candidate.RegionID != selected.RegionID || candidate.InstanceType != selected.InstanceType {
			continue
		}
		resources.GPUZones = appendIfNotEmpty(resources.GPUZones, candidate.ZoneID)
	}
	if cfg.Cluster.VPCID == "" && !hasPinnedVSwitches(cfg.Cluster) {
		cfg.Cluster.ZoneIDs = append([]string(nil), resources.GPUZones...)
	}
	return nil
}

func ensureGPUDiscoveryNetworkCompatible(ctx context.Context, cfg *Config, resources *ClusterResources) error {
	if err := validateGPUDiscoveryPinnedNetworkConfig(cfg, resources); err != nil {
		return err
	}
	ids := pinnedVSwitchIDs(cfg.Cluster)
	if len(ids) == 0 || len(resources.GPUZones) == 0 {
		return nil
	}
	zones, err := DiscoverVSwitchZones(ctx, cfg, ids)
	if err != nil {
		return err
	}
	resources.Zones = zones
	filtered, err := IntersectGPUZonesWithClusterZones(ClusterResources{
		Zones:            zones,
		GPUInstanceTypes: resources.GPUInstanceTypes,
		GPUZones:         resources.GPUZones,
	})
	if err != nil {
		return err
	}
	resources.GPUZones = filtered.GPUZones
	return nil
}

func validateGPUDiscoveryPinnedNetworkConfig(cfg *Config, resources *ClusterResources) error {
	if cfg == nil || resources == nil || len(resources.GPUZones) == 0 {
		return nil
	}
	if cfg.Cluster.VPCID != "" && !hasPinnedVSwitches(cfg.Cluster) {
		return fmt.Errorf("GPU discovery with pinned VPC %s requires explicit VSwitch IDs in GPU-capable zones before ACK cluster creation", cfg.Cluster.VPCID)
	}
	return nil
}

func hasPinnedVSwitches(cluster ClusterConfig) bool {
	return len(uniqueNonEmpty(cluster.VSwitchIDs)) > 0 ||
		len(uniqueNonEmpty(cluster.PodVSwitchIDs)) > 0 ||
		len(uniqueNonEmpty(cluster.Master.VSwitchIDs)) > 0
}

func pinnedVSwitchIDs(cluster ClusterConfig) []string {
	var ids []string
	ids = append(ids, cluster.VSwitchIDs...)
	ids = append(ids, cluster.PodVSwitchIDs...)
	ids = append(ids, cluster.Master.VSwitchIDs...)
	return uniqueNonEmpty(ids)
}

func IntersectGPUZonesWithClusterZones(resources ClusterResources) (ClusterResources, error) {
	if len(resources.GPUZones) == 0 || len(resources.Zones) == 0 {
		return resources, nil
	}
	clusterZones := stringSet(resources.Zones)
	var filtered []string
	for _, zone := range resources.GPUZones {
		if _, ok := clusterZones[zone]; ok {
			filtered = appendIfNotEmpty(filtered, zone)
		}
	}
	if len(filtered) == 0 {
		return resources, fmt.Errorf("no GPU zones overlap cluster VSwitch zones: gpu=%s cluster=%s", strings.Join(resources.GPUZones, ","), strings.Join(resources.Zones, ","))
	}
	resources.GPUZones = filtered
	return resources, nil
}

func ExtractWorkerImageID(resp *ecs.DescribeInstancesResponse) string {
	if resp == nil || resp.Body == nil || resp.Body.Instances == nil {
		return ""
	}
	for _, instance := range resp.Body.Instances.Instance {
		if instance == nil || instance.ImageId == nil {
			continue
		}
		if imageID := strings.TrimSpace(*instance.ImageId); imageID != "" {
			return imageID
		}
	}
	return ""
}

func ExtractImageIDFromImageNameLookup(resp *ecs.DescribeImagesResponse) string {
	if resp == nil || resp.Body == nil || resp.Body.Images == nil {
		return ""
	}
	for _, image := range resp.Body.Images.Image {
		if image == nil || image.ImageId == nil {
			continue
		}
		if image.Status != nil && !strings.EqualFold(strings.TrimSpace(*image.Status), "Available") {
			continue
		}
		if imageID := strings.TrimSpace(*image.ImageId); imageID != "" {
			return imageID
		}
	}
	return ""
}

func appendVSwitchZones(zones []string, resp *vpc.DescribeVSwitchesResponse) []string {
	if resp == nil || resp.Body == nil || resp.Body.VSwitches == nil {
		return zones
	}
	for _, vsw := range resp.Body.VSwitches.VSwitch {
		if vsw == nil || vsw.ZoneId == nil {
			continue
		}
		zones = appendIfNotEmpty(zones, *vsw.ZoneId)
	}
	return zones
}

func ExtractClusterResources(detail *cs.DescribeClusterDetailResponseBody) ClusterResources {
	if detail == nil {
		return ClusterResources{}
	}
	resources := ClusterResources{}
	for _, id := range detail.VswitchIds {
		if id == nil {
			continue
		}
		resources.VSwitchIDs = appendIfNotEmpty(resources.VSwitchIDs, *id)
	}
	if len(resources.VSwitchIDs) == 0 && detail.VswitchId != nil {
		for _, id := range strings.Split(*detail.VswitchId, ",") {
			resources.VSwitchIDs = appendIfNotEmpty(resources.VSwitchIDs, id)
		}
	}
	if detail.SecurityGroupId != nil {
		resources.SecurityGroupIDs = appendIfNotEmpty(resources.SecurityGroupIDs, *detail.SecurityGroupId)
	}
	if detail.WorkerRamRoleName != nil {
		resources.RAMRole = strings.TrimSpace(*detail.WorkerRamRoleName)
	}
	return resources
}

func appendIfNotEmpty(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func hasStock(status, category *string) bool {
	if status != nil && !strings.EqualFold(strings.TrimSpace(*status), "Available") {
		return false
	}
	if category != nil && !strings.EqualFold(strings.TrimSpace(*category), "WithStock") {
		return false
	}
	return true
}

func isAllowedGPUInstanceType(instanceType string, allowed map[string]struct{}) bool {
	if len(allowed) > 0 {
		_, ok := allowed[instanceType]
		return ok
	}
	return strings.Contains(instanceType, ".gn") || strings.Contains(instanceType, ".vgn") || strings.Contains(instanceType, ".sgn")
}

func stringSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	out := map[string]struct{}{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out[value] = struct{}{}
		}
	}
	return out
}

func uniqueNonEmpty(values []string) []string {
	var out []string
	for _, value := range values {
		out = appendIfNotEmpty(out, value)
	}
	return out
}

func appendGPUCandidates(values []GPUResourceCandidate, candidates ...GPUResourceCandidate) []GPUResourceCandidate {
	for _, candidate := range candidates {
		if candidate.RegionID == "" || candidate.ZoneID == "" || candidate.InstanceType == "" {
			continue
		}
		duplicate := false
		for _, existing := range values {
			if existing == candidate {
				duplicate = true
				break
			}
		}
		if !duplicate {
			values = append(values, candidate)
		}
	}
	return values
}
