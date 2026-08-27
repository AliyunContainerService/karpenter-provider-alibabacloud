package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	vpc "github.com/alibabacloud-go/vpc-20160428/v7/client"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	alb "github.com/aliyun/alibaba-cloud-sdk-go/services/alb"
	nlb "github.com/aliyun/alibaba-cloud-sdk-go/services/nlb"
	slb "github.com/aliyun/alibaba-cloud-sdk-go/services/slb"
)

const (
	sweepPollInterval = 15 * time.Second
	sweepPollTimeout  = 15 * time.Minute
)

type SweepResource struct {
	Type   string
	ID     string
	Status string
}

type SweepReport struct {
	Resources []SweepResource
}

func (r *SweepReport) Append(resourceType, id, status string) {
	resourceType = strings.TrimSpace(resourceType)
	id = strings.TrimSpace(id)
	status = strings.TrimSpace(status)
	if resourceType == "" || id == "" {
		return
	}
	for _, existing := range r.Resources {
		if existing.Type == resourceType && existing.ID == id {
			return
		}
	}
	r.Resources = append(r.Resources, SweepResource{Type: resourceType, ID: id, Status: status})
}

func (r SweepReport) HasResidue() bool {
	return len(r.Resources) > 0
}

func (r SweepReport) ResidueError() error {
	if !r.HasResidue() {
		return nil
	}
	parts := make([]string, 0, len(r.Resources))
	for _, resource := range r.Resources {
		name := resource.Type + "/" + resource.ID
		if resource.Status != "" {
			name += "(" + resource.Status + ")"
		}
		parts = append(parts, name)
	}
	return fmt.Errorf("owned Alibaba Cloud resources remain after cleanup: %s", strings.Join(parts, ", "))
}

func sweepTagSelectors(manifest *Manifest) []map[string]string {
	if manifest == nil {
		return nil
	}
	cluster := strings.TrimSpace(manifest.OwnershipTags["testing/cluster"])
	if cluster == "" {
		return nil
	}
	selectors := []map[string]string{{
		"testing/cluster": cluster,
	}}
	if discovery := strings.TrimSpace(manifest.OwnershipTags["karpenter.sh/discovery"]); discovery != "" {
		selectors[0]["karpenter.sh/discovery"] = discovery
	}
	selectors = append(selectors, map[string]string{
		"testing/cluster":         cluster,
		"karpenter.sh/managed-by": v1alpha1.TagManagedByValue,
	})
	selectors = append(selectors, map[string]string{
		"testing/cluster":         cluster,
		"karpenter.sh/managed-by": "karpenter",
	})
	return selectors
}

func SweepOwnedResources(ctx context.Context, cfg *Config, manifest *Manifest, deleteResources bool) (SweepReport, error) {
	selectors := sweepTagSelectors(manifest)
	if len(selectors) == 0 {
		return SweepReport{}, fmt.Errorf("manifest has no ownership tags for sweep")
	}
	return SweepResourcesBySelectors(ctx, cfg, selectors, deleteResources)
}

func SweepResourcesBySelectors(ctx context.Context, cfg *Config, selectors []map[string]string, deleteResources bool) (SweepReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, sweepPollTimeout)
	defer cancel()

	for {
		report, err := describeOwnedResources(ctx, cfg, selectors)
		if err != nil {
			return report, err
		}
		if !deleteResources || !report.HasResidue() {
			return report, nil
		}
		if err := deleteOwnedResources(ctx, cfg, report); err != nil {
			return report, err
		}
		select {
		case <-ctx.Done():
			return report, nil
		case <-time.After(sweepPollInterval):
		}
	}
}

func describeOwnedResources(ctx context.Context, cfg *Config, selectors []map[string]string) (SweepReport, error) {
	_ = ctx
	if len(selectors) == 0 {
		return SweepReport{}, fmt.Errorf("at least one sweep selector is required")
	}
	ecsClient, err := newECSClient(cfg)
	if err != nil {
		return SweepReport{}, err
	}
	vpcClient, err := newVPCClient(cfg)
	if err != nil {
		return SweepReport{}, err
	}
	slbClient, err := newSLBClient(cfg)
	if err != nil {
		return SweepReport{}, err
	}
	albClient, err := newALBClient(cfg)
	if err != nil {
		return SweepReport{}, err
	}
	nlbClient, err := newNLBClient(cfg)
	if err != nil {
		return SweepReport{}, err
	}
	var report SweepReport
	for _, selector := range selectors {
		if err := describeOwnedInstances(ecsClient, cfg, selector, &report); err != nil {
			return report, err
		}
		if err := describeOwnedDisks(ecsClient, cfg, selector, &report); err != nil {
			return report, err
		}
		if err := describeOwnedNetworkInterfaces(ecsClient, cfg, selector, &report); err != nil {
			return report, err
		}
		if err := describeOwnedSecurityGroups(ecsClient, cfg, selector, &report); err != nil {
			return report, err
		}
		if err := describeOwnedLaunchTemplates(ecsClient, cfg, selector, &report); err != nil {
			return report, err
		}
		if err := describeOwnedCapacityReservations(ecsClient, cfg, selector, &report); err != nil {
			return report, err
		}
		if err := describeOwnedNatGateways(vpcClient, cfg, selector, &report); err != nil {
			return report, err
		}
		if err := describeOwnedEIPs(vpcClient, cfg, selector, &report); err != nil {
			return report, err
		}
		if err := describeOwnedSLBs(slbClient, selector, &report); err != nil {
			return report, err
		}
		if err := describeOwnedALBs(albClient, selector, &report); err != nil {
			return report, err
		}
		if err := describeOwnedNLBs(nlbClient, selector, &report); err != nil {
			return report, err
		}
		if err := describeOwnedVSwitches(vpcClient, cfg, selector, &report); err != nil {
			return report, err
		}
		if err := describeOwnedVPCs(vpcClient, cfg, selector, &report); err != nil {
			return report, err
		}
	}
	return report, nil
}

func parseSweepSelectorFlags(values []string) ([]map[string]string, error) {
	selector := map[string]string{}
	for _, raw := range values {
		key, value, ok := strings.Cut(raw, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if !ok || key == "" || value == "" {
			return nil, fmt.Errorf("selector %q must be in key=value form", raw)
		}
		selector[key] = value
	}
	if len(selector) == 0 {
		return nil, fmt.Errorf("at least one selector is required")
	}
	return []map[string]string{selector}, nil
}

func deleteOwnedResources(ctx context.Context, cfg *Config, report SweepReport) error {
	_ = ctx
	ecsClient, err := newECSClient(cfg)
	if err != nil {
		return err
	}
	vpcClient, err := newVPCClient(cfg)
	if err != nil {
		return err
	}
	slbClient, err := newSLBClient(cfg)
	if err != nil {
		return err
	}
	albClient, err := newALBClient(cfg)
	if err != nil {
		return err
	}
	nlbClient, err := newNLBClient(cfg)
	if err != nil {
		return err
	}
	var deleteErrs []error
	for _, resourceType := range []string{"ecs-instance", "slb", "alb", "nlb", "disk", "network-interface", "security-group", "launch-template", "capacity-reservation", "nat-gateway", "eip", "vswitch", "vpc"} {
		for _, resource := range report.Resources {
			if resource.Type != resourceType {
				continue
			}
			switch resource.Type {
			case "ecs-instance":
				if err := disableInstanceDeletionProtection(ecsClient, resource.ID); err != nil && !isNotFoundOrDependencyError(err) {
					deleteErrs = append(deleteErrs, fmt.Errorf("disable ECS deletion protection %s: %w", resource.ID, err))
					continue
				}
				if _, err := ecsClient.DeleteInstances(&ecs.DeleteInstancesRequest{
					RegionId:              tea.String(cfg.AlibabaCloud.RegionID),
					InstanceId:            []*string{tea.String(resource.ID)},
					Force:                 tea.Bool(true),
					TerminateSubscription: tea.Bool(true),
				}); err != nil && !isNotFoundOrDependencyError(err) {
					deleteErrs = append(deleteErrs, fmt.Errorf("delete ECS instance %s: %w", resource.ID, err))
				}
			case "slb":
				if err := disableSLBProtection(slbClient, resource.ID); err != nil && !isNotFoundOrDependencyError(err) {
					deleteErrs = append(deleteErrs, fmt.Errorf("disable SLB protection %s: %w", resource.ID, err))
					continue
				}
				req := slb.CreateDeleteLoadBalancerRequest()
				req.LoadBalancerId = resource.ID
				if _, err := slbClient.DeleteLoadBalancer(req); err != nil && !isNotFoundOrDependencyError(err) {
					deleteErrs = append(deleteErrs, fmt.Errorf("delete SLB %s: %w", resource.ID, err))
				}
			case "alb":
				req := alb.CreateDeleteLoadBalancerRequest()
				req.LoadBalancerId = resource.ID
				if _, err := albClient.DeleteLoadBalancer(req); err != nil && !isNotFoundOrDependencyError(err) {
					deleteErrs = append(deleteErrs, fmt.Errorf("delete ALB %s: %w", resource.ID, err))
				}
			case "nlb":
				req := nlb.CreateDeleteLoadBalancerRequest()
				req.LoadBalancerId = resource.ID
				if _, err := nlbClient.DeleteLoadBalancer(req); err != nil && !isNotFoundOrDependencyError(err) {
					deleteErrs = append(deleteErrs, fmt.Errorf("delete NLB %s: %w", resource.ID, err))
				}
			case "disk":
				if !strings.EqualFold(resource.Status, "Available") {
					continue
				}
				if _, err := ecsClient.DeleteDisk(&ecs.DeleteDiskRequest{DiskId: tea.String(resource.ID)}); err != nil && !isNotFoundOrDependencyError(err) {
					deleteErrs = append(deleteErrs, fmt.Errorf("delete disk %s: %w", resource.ID, err))
				}
			case "network-interface":
				if !strings.EqualFold(resource.Status, "Available") {
					continue
				}
				if _, err := ecsClient.DeleteNetworkInterface(&ecs.DeleteNetworkInterfaceRequest{
					RegionId:           tea.String(cfg.AlibabaCloud.RegionID),
					NetworkInterfaceId: tea.String(resource.ID),
				}); err != nil && !isNotFoundOrDependencyError(err) {
					deleteErrs = append(deleteErrs, fmt.Errorf("delete ENI %s: %w", resource.ID, err))
				}
			case "security-group":
				if _, err := ecsClient.DeleteSecurityGroup(&ecs.DeleteSecurityGroupRequest{
					RegionId:        tea.String(cfg.AlibabaCloud.RegionID),
					SecurityGroupId: tea.String(resource.ID),
				}); err != nil && !isNotFoundOrDependencyError(err) {
					deleteErrs = append(deleteErrs, fmt.Errorf("delete security group %s: %w", resource.ID, err))
				}
			case "launch-template":
				if _, err := ecsClient.DeleteLaunchTemplate(&ecs.DeleteLaunchTemplateRequest{
					RegionId:         tea.String(cfg.AlibabaCloud.RegionID),
					LaunchTemplateId: tea.String(resource.ID),
				}); err != nil && !isNotFoundOrDependencyError(err) {
					deleteErrs = append(deleteErrs, fmt.Errorf("delete launch template %s: %w", resource.ID, err))
				}
			case "capacity-reservation":
				if err := releaseCapacityReservation(ecsClient, cfg, resource.ID); err != nil && !isNotFoundOrDependencyError(err) {
					deleteErrs = append(deleteErrs, fmt.Errorf("release capacity reservation %s: %w", resource.ID, err))
				}
			case "nat-gateway":
				if err := disableVPCDeletionProtection(vpcClient, cfg, resource.ID, "NATGW"); err != nil && !isNotFoundOrDependencyError(err) {
					deleteErrs = append(deleteErrs, fmt.Errorf("disable NAT gateway deletion protection %s: %w", resource.ID, err))
					continue
				}
				if _, err := vpcClient.DeleteNatGateway(&vpc.DeleteNatGatewayRequest{
					RegionId:     tea.String(cfg.AlibabaCloud.RegionID),
					NatGatewayId: tea.String(resource.ID),
					Force:        tea.Bool(true),
				}); err != nil && !isNotFoundOrDependencyError(err) {
					deleteErrs = append(deleteErrs, fmt.Errorf("delete NAT gateway %s: %w", resource.ID, err))
				}
			case "eip":
				if !strings.EqualFold(resource.Status, "Available") {
					continue
				}
				if err := disableVPCDeletionProtection(vpcClient, cfg, resource.ID, "EIP"); err != nil && !isNotFoundOrDependencyError(err) {
					deleteErrs = append(deleteErrs, fmt.Errorf("disable EIP deletion protection %s: %w", resource.ID, err))
					continue
				}
				if _, err := vpcClient.ReleaseEipAddress(&vpc.ReleaseEipAddressRequest{
					RegionId:     tea.String(cfg.AlibabaCloud.RegionID),
					AllocationId: tea.String(resource.ID),
				}); err != nil && !isNotFoundOrDependencyError(err) {
					deleteErrs = append(deleteErrs, fmt.Errorf("release EIP %s: %w", resource.ID, err))
				}
			case "vswitch":
				if _, err := vpcClient.DeleteVSwitch(&vpc.DeleteVSwitchRequest{
					RegionId:  tea.String(cfg.AlibabaCloud.RegionID),
					VSwitchId: tea.String(resource.ID),
				}); err != nil && !isNotFoundOrDependencyError(err) {
					deleteErrs = append(deleteErrs, fmt.Errorf("delete vswitch %s: %w", resource.ID, err))
				}
			case "vpc":
				if _, err := vpcClient.DeleteVpc(&vpc.DeleteVpcRequest{
					RegionId: tea.String(cfg.AlibabaCloud.RegionID),
					VpcId:    tea.String(resource.ID),
				}); err != nil && !isNotFoundOrDependencyError(err) {
					deleteErrs = append(deleteErrs, fmt.Errorf("delete VPC %s: %w", resource.ID, err))
				}
			}
		}
	}
	return errors.Join(deleteErrs...)
}

func disableInstanceDeletionProtection(client *ecs.Client, instanceID string) error {
	_, err := client.ModifyInstanceAttribute(&ecs.ModifyInstanceAttributeRequest{
		InstanceId:          tea.String(instanceID),
		DeletionProtection:  tea.Bool(false),
	})
	return err
}

func disableSLBProtection(client *slb.Client, loadBalancerID string) error {
	deleteProtection := slb.CreateSetLoadBalancerDeleteProtectionRequest()
	deleteProtection.LoadBalancerId = loadBalancerID
	deleteProtection.DeleteProtection = "off"
	if _, err := client.SetLoadBalancerDeleteProtection(deleteProtection); err != nil {
		return err
	}
	modificationProtection := slb.CreateSetLoadBalancerModificationProtectionRequest()
	modificationProtection.LoadBalancerId = loadBalancerID
	modificationProtection.ModificationProtectionStatus = "NonProtection"
	if _, err := client.SetLoadBalancerModificationProtection(modificationProtection); err != nil {
		return err
	}
	return nil
}

func disableVPCDeletionProtection(client *vpc.Client, cfg *Config, instanceID, instanceType string) error {
	_, err := client.DeletionProtection(&vpc.DeletionProtectionRequest{
		RegionId:         tea.String(cfg.AlibabaCloud.RegionID),
		InstanceId:       tea.String(instanceID),
		Type:             tea.String(instanceType),
		ProtectionEnable: tea.Bool(false),
	})
	return err
}

func describeOwnedInstances(client *ecs.Client, cfg *Config, selector map[string]string, report *SweepReport) error {
	var nextToken *string
	for {
		resp, err := client.DescribeInstances(&ecs.DescribeInstancesRequest{
			RegionId:   tea.String(cfg.AlibabaCloud.RegionID),
			MaxResults: tea.Int32(100),
			NextToken:  nextToken,
			Tag:        ecsInstanceTags(selector),
		})
		if err != nil {
			return fmt.Errorf("describe ECS instances: %w", err)
		}
		if resp != nil && resp.Body != nil && resp.Body.Instances != nil {
			for _, instance := range resp.Body.Instances.Instance {
				if instance == nil {
					continue
				}
				report.Append("ecs-instance", tea.StringValue(instance.InstanceId), tea.StringValue(instance.Status))
			}
			nextToken = resp.Body.NextToken
		} else {
			nextToken = nil
		}
		if tea.StringValue(nextToken) == "" {
			return nil
		}
	}
}

func describeOwnedLaunchTemplates(client *ecs.Client, cfg *Config, selector map[string]string, report *SweepReport) error {
	page := int32(1)
	for {
		resp, err := client.DescribeLaunchTemplates(&ecs.DescribeLaunchTemplatesRequest{
			RegionId:    tea.String(cfg.AlibabaCloud.RegionID),
			PageNumber:  tea.Int32(page),
			PageSize:    tea.Int32(100),
			TemplateTag: ecsLaunchTemplateTags(selector),
		})
		if err != nil {
			return fmt.Errorf("describe launch templates: %w", err)
		}
		count := 0
		if resp != nil && resp.Body != nil && resp.Body.LaunchTemplateSets != nil {
			for _, launchTemplate := range resp.Body.LaunchTemplateSets.LaunchTemplateSet {
				if launchTemplate == nil {
					continue
				}
				count++
				report.Append("launch-template", tea.StringValue(launchTemplate.LaunchTemplateId), tea.StringValue(launchTemplate.LaunchTemplateName))
			}
		}
		if count == 0 || resp == nil || resp.Body == nil || resp.Body.TotalCount == nil || int32(count) < tea.Int32Value(resp.Body.PageSize) {
			return nil
		}
		if page*tea.Int32Value(resp.Body.PageSize) >= tea.Int32Value(resp.Body.TotalCount) {
			return nil
		}
		page++
	}
}

func describeOwnedSLBs(client *slb.Client, selector map[string]string, report *SweepReport) error {
	page := 1
	for {
		tags := slbLoadBalancerTags(selector)
		req := slb.CreateDescribeLoadBalancersRequest()
		req.PageNumber = requests.NewInteger(page)
		req.PageSize = requests.NewInteger(50)
		req.Tag = &tags
		resp, err := client.DescribeLoadBalancers(req)
		if err != nil {
			return fmt.Errorf("describe SLBs: %w", err)
		}
		count := 0
		if resp != nil {
			for _, lb := range resp.LoadBalancers.LoadBalancer {
				count++
				report.Append("slb", lb.LoadBalancerId, lb.LoadBalancerStatus)
			}
		}
		if count < 50 {
			return nil
		}
		page++
	}
}

func describeOwnedALBs(client *alb.Client, selector map[string]string, report *SweepReport) error {
	nextToken := ""
	for {
		tags := albLoadBalancerTags(selector)
		req := alb.CreateListLoadBalancersRequest()
		req.MaxResults = requests.NewInteger(100)
		req.NextToken = nextToken
		req.Tag = &tags
		resp, err := client.ListLoadBalancers(req)
		if err != nil {
			return fmt.Errorf("describe ALBs: %w", err)
		}
		if resp != nil {
			for _, lb := range resp.LoadBalancers {
				report.Append("alb", lb.LoadBalancerId, lb.LoadBalancerStatus)
			}
			nextToken = resp.NextToken
		} else {
			nextToken = ""
		}
		if strings.TrimSpace(nextToken) == "" {
			return nil
		}
	}
}

func describeOwnedNLBs(client *nlb.Client, selector map[string]string, report *SweepReport) error {
	nextToken := ""
	for {
		tags := nlbLoadBalancerTags(selector)
		req := nlb.CreateListLoadBalancersRequest()
		req.MaxResults = requests.NewInteger(100)
		req.NextToken = nextToken
		req.Tag = &tags
		resp, err := client.ListLoadBalancers(req)
		if err != nil {
			return fmt.Errorf("describe NLBs: %w", err)
		}
		if resp != nil {
			for _, lb := range resp.LoadBalancers {
				report.Append("nlb", lb.LoadBalancerId, lb.LoadBalancerStatus)
			}
			nextToken = resp.NextToken
		} else {
			nextToken = ""
		}
		if strings.TrimSpace(nextToken) == "" {
			return nil
		}
	}
}

func describeOwnedDisks(client *ecs.Client, cfg *Config, selector map[string]string, report *SweepReport) error {
	var nextToken *string
	for {
		resp, err := client.DescribeDisks(&ecs.DescribeDisksRequest{
			RegionId:   tea.String(cfg.AlibabaCloud.RegionID),
			MaxResults: tea.Int32(100),
			NextToken:  nextToken,
			Tag:        ecsDiskTags(selector),
		})
		if err != nil {
			return fmt.Errorf("describe disks: %w", err)
		}
		if resp != nil && resp.Body != nil && resp.Body.Disks != nil {
			for _, disk := range resp.Body.Disks.Disk {
				if disk == nil {
					continue
				}
				report.Append("disk", tea.StringValue(disk.DiskId), tea.StringValue(disk.Status))
			}
			nextToken = resp.Body.NextToken
		} else {
			nextToken = nil
		}
		if tea.StringValue(nextToken) == "" {
			return nil
		}
	}
}

func describeOwnedNetworkInterfaces(client *ecs.Client, cfg *Config, selector map[string]string, report *SweepReport) error {
	var nextToken *string
	for {
		resp, err := client.DescribeNetworkInterfaces(&ecs.DescribeNetworkInterfacesRequest{
			RegionId:   tea.String(cfg.AlibabaCloud.RegionID),
			MaxResults: tea.Int32(100),
			NextToken:  nextToken,
			Tag:        ecsNetworkInterfaceTags(selector),
		})
		if err != nil {
			return fmt.Errorf("describe network interfaces: %w", err)
		}
		if resp != nil && resp.Body != nil && resp.Body.NetworkInterfaceSets != nil {
			for _, eni := range resp.Body.NetworkInterfaceSets.NetworkInterfaceSet {
				if eni == nil {
					continue
				}
				report.Append("network-interface", tea.StringValue(eni.NetworkInterfaceId), tea.StringValue(eni.Status))
			}
			nextToken = resp.Body.NextToken
		} else {
			nextToken = nil
		}
		if tea.StringValue(nextToken) == "" {
			return nil
		}
	}
}

func describeOwnedSecurityGroups(client *ecs.Client, cfg *Config, selector map[string]string, report *SweepReport) error {
	var nextToken *string
	for {
		resp, err := client.DescribeSecurityGroups(&ecs.DescribeSecurityGroupsRequest{
			RegionId:   tea.String(cfg.AlibabaCloud.RegionID),
			MaxResults: tea.Int32(100),
			NextToken:  nextToken,
			Tag:        ecsSecurityGroupTags(selector),
		})
		if err != nil {
			return fmt.Errorf("describe security groups: %w", err)
		}
		if resp != nil && resp.Body != nil && resp.Body.SecurityGroups != nil {
			for _, sg := range resp.Body.SecurityGroups.SecurityGroup {
				if sg == nil {
					continue
				}
				report.Append("security-group", tea.StringValue(sg.SecurityGroupId), tea.StringValue(sg.SecurityGroupType))
			}
			nextToken = resp.Body.NextToken
		} else {
			nextToken = nil
		}
		if tea.StringValue(nextToken) == "" {
			return nil
		}
	}
}

func describeOwnedVSwitches(client *vpc.Client, cfg *Config, selector map[string]string, report *SweepReport) error {
	page := int32(1)
	for {
		resp, err := client.DescribeVSwitches(&vpc.DescribeVSwitchesRequest{
			RegionId:   tea.String(cfg.AlibabaCloud.RegionID),
			PageNumber: tea.Int32(page),
			PageSize:   tea.Int32(50),
			Tag:        vpcVSwitchTags(selector),
		})
		if err != nil {
			return fmt.Errorf("describe vswitches: %w", err)
		}
		count := 0
		if resp != nil && resp.Body != nil && resp.Body.VSwitches != nil {
			for _, vsw := range resp.Body.VSwitches.VSwitch {
				if vsw == nil {
					continue
				}
				count++
				report.Append("vswitch", tea.StringValue(vsw.VSwitchId), tea.StringValue(vsw.Status))
			}
		}
		if count < 50 {
			return nil
		}
		page++
	}
}

func describeOwnedCapacityReservations(client *ecs.Client, cfg *Config, selector map[string]string, report *SweepReport) error {
	var nextToken *string
	for {
		resp, err := client.DescribeCapacityReservations(&ecs.DescribeCapacityReservationsRequest{
			RegionId:   tea.String(cfg.AlibabaCloud.RegionID),
			MaxResults: tea.Int32(100),
			NextToken:  nextToken,
			Tag:        ecsCapacityReservationTags(selector),
		})
		if err != nil {
			return fmt.Errorf("describe capacity reservations: %w", err)
		}
		if resp != nil && resp.Body != nil && resp.Body.CapacityReservationSet != nil {
			for _, reservation := range resp.Body.CapacityReservationSet.CapacityReservationItem {
				if reservation == nil {
					continue
				}
				report.Append("capacity-reservation", tea.StringValue(reservation.PrivatePoolOptionsId), tea.StringValue(reservation.Status))
			}
			nextToken = resp.Body.NextToken
		} else {
			nextToken = nil
		}
		if strings.TrimSpace(tea.StringValue(nextToken)) == "" {
			return nil
		}
	}
}

func describeOwnedNatGateways(client *vpc.Client, cfg *Config, selector map[string]string, report *SweepReport) error {
	page := int32(1)
	for {
		resp, err := client.DescribeNatGateways(&vpc.DescribeNatGatewaysRequest{
			RegionId:   tea.String(cfg.AlibabaCloud.RegionID),
			PageNumber: tea.Int32(page),
			PageSize:   tea.Int32(50),
			Tag:        vpcNatGatewayTags(selector),
		})
		if err != nil {
			return fmt.Errorf("describe NAT gateways: %w", err)
		}
		count := 0
		if resp != nil && resp.Body != nil && resp.Body.NatGateways != nil {
			for _, nat := range resp.Body.NatGateways.NatGateway {
				if nat == nil {
					continue
				}
				count++
				report.Append("nat-gateway", tea.StringValue(nat.NatGatewayId), tea.StringValue(nat.Status))
			}
		}
		if count < 50 {
			return nil
		}
		page++
	}
}

func describeOwnedEIPs(client *vpc.Client, cfg *Config, selector map[string]string, report *SweepReport) error {
	page := int32(1)
	for {
		resp, err := client.DescribeEipAddresses(&vpc.DescribeEipAddressesRequest{
			RegionId:   tea.String(cfg.AlibabaCloud.RegionID),
			PageNumber: tea.Int32(page),
			PageSize:   tea.Int32(100),
			Tag:        vpcEIPTags(selector),
		})
		if err != nil {
			return fmt.Errorf("describe EIPs: %w", err)
		}
		count := 0
		if resp != nil && resp.Body != nil && resp.Body.EipAddresses != nil {
			for _, eip := range resp.Body.EipAddresses.EipAddress {
				if eip == nil {
					continue
				}
				count++
				report.Append("eip", tea.StringValue(eip.AllocationId), tea.StringValue(eip.Status))
			}
		}
		if count < 100 {
			return nil
		}
		page++
	}
}

func describeOwnedVPCs(client *vpc.Client, cfg *Config, selector map[string]string, report *SweepReport) error {
	page := int32(1)
	for {
		resp, err := client.DescribeVpcs(&vpc.DescribeVpcsRequest{
			RegionId:   tea.String(cfg.AlibabaCloud.RegionID),
			PageNumber: tea.Int32(page),
			PageSize:   tea.Int32(50),
			Tag:        vpcTags(selector),
		})
		if err != nil {
			return fmt.Errorf("describe VPCs: %w", err)
		}
		count := 0
		if resp != nil && resp.Body != nil && resp.Body.Vpcs != nil {
			for _, ownedVPC := range resp.Body.Vpcs.Vpc {
				if ownedVPC == nil {
					continue
				}
				count++
				report.Append("vpc", tea.StringValue(ownedVPC.VpcId), tea.StringValue(ownedVPC.Status))
			}
		}
		if count < 50 {
			return nil
		}
		page++
	}
}

func ecsInstanceTags(selector map[string]string) []*ecs.DescribeInstancesRequestTag {
	tags := make([]*ecs.DescribeInstancesRequestTag, 0, len(selector))
	for key, value := range selector {
		tags = append(tags, &ecs.DescribeInstancesRequestTag{Key: tea.String(key), Value: tea.String(value)})
	}
	return tags
}

func ecsDiskTags(selector map[string]string) []*ecs.DescribeDisksRequestTag {
	tags := make([]*ecs.DescribeDisksRequestTag, 0, len(selector))
	for key, value := range selector {
		tags = append(tags, &ecs.DescribeDisksRequestTag{Key: tea.String(key), Value: tea.String(value)})
	}
	return tags
}

func ecsNetworkInterfaceTags(selector map[string]string) []*ecs.DescribeNetworkInterfacesRequestTag {
	tags := make([]*ecs.DescribeNetworkInterfacesRequestTag, 0, len(selector))
	for key, value := range selector {
		tags = append(tags, &ecs.DescribeNetworkInterfacesRequestTag{Key: tea.String(key), Value: tea.String(value)})
	}
	return tags
}

func ecsSecurityGroupTags(selector map[string]string) []*ecs.DescribeSecurityGroupsRequestTag {
	tags := make([]*ecs.DescribeSecurityGroupsRequestTag, 0, len(selector))
	for key, value := range selector {
		tags = append(tags, &ecs.DescribeSecurityGroupsRequestTag{Key: tea.String(key), Value: tea.String(value)})
	}
	return tags
}

func ecsLaunchTemplateTags(selector map[string]string) []*ecs.DescribeLaunchTemplatesRequestTemplateTag {
	tags := make([]*ecs.DescribeLaunchTemplatesRequestTemplateTag, 0, len(selector))
	for _, key := range sortedSelectorKeys(selector) {
		tags = append(tags, &ecs.DescribeLaunchTemplatesRequestTemplateTag{Key: tea.String(key), Value: tea.String(selector[key])})
	}
	return tags
}

func ecsCapacityReservationTags(selector map[string]string) []*ecs.DescribeCapacityReservationsRequestTag {
	tags := make([]*ecs.DescribeCapacityReservationsRequestTag, 0, len(selector))
	for _, key := range sortedSelectorKeys(selector) {
		tags = append(tags, &ecs.DescribeCapacityReservationsRequestTag{Key: tea.String(key), Value: tea.String(selector[key])})
	}
	return tags
}

func capacityReservationIDs(ids ...string) *string {
	values := uniqueNonEmpty(ids)
	if len(values) == 0 {
		return nil
	}
	data, err := json.Marshal(values)
	if err != nil {
		return nil
	}
	return tea.String(string(data))
}

func sortedSelectorKeys(selector map[string]string) []string {
	keys := make([]string, 0, len(selector))
	for key := range selector {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i] == "testing/cluster" {
			return true
		}
		if keys[j] == "testing/cluster" {
			return false
		}
		return keys[i] < keys[j]
	})
	return keys
}

func vpcVSwitchTags(selector map[string]string) []*vpc.DescribeVSwitchesRequestTag {
	tags := make([]*vpc.DescribeVSwitchesRequestTag, 0, len(selector))
	for key, value := range selector {
		tags = append(tags, &vpc.DescribeVSwitchesRequestTag{Key: tea.String(key), Value: tea.String(value)})
	}
	return tags
}

func vpcNatGatewayTags(selector map[string]string) []*vpc.DescribeNatGatewaysRequestTag {
	tags := make([]*vpc.DescribeNatGatewaysRequestTag, 0, len(selector))
	for key, value := range selector {
		tags = append(tags, &vpc.DescribeNatGatewaysRequestTag{Key: tea.String(key), Value: tea.String(value)})
	}
	return tags
}

func vpcEIPTags(selector map[string]string) []*vpc.DescribeEipAddressesRequestTag {
	tags := make([]*vpc.DescribeEipAddressesRequestTag, 0, len(selector))
	for key, value := range selector {
		tags = append(tags, &vpc.DescribeEipAddressesRequestTag{Key: tea.String(key), Value: tea.String(value)})
	}
	return tags
}

func vpcTags(selector map[string]string) []*vpc.DescribeVpcsRequestTag {
	tags := make([]*vpc.DescribeVpcsRequestTag, 0, len(selector))
	for key, value := range selector {
		tags = append(tags, &vpc.DescribeVpcsRequestTag{Key: tea.String(key), Value: tea.String(value)})
	}
	return tags
}

func slbLoadBalancerTags(selector map[string]string) []slb.DescribeLoadBalancersTag {
	tags := make([]slb.DescribeLoadBalancersTag, 0, len(selector))
	for key, value := range selector {
		tags = append(tags, slb.DescribeLoadBalancersTag{Key: key, Value: value})
	}
	return tags
}

func albLoadBalancerTags(selector map[string]string) []alb.ListLoadBalancersTag {
	tags := make([]alb.ListLoadBalancersTag, 0, len(selector))
	for key, value := range selector {
		tags = append(tags, alb.ListLoadBalancersTag{Key: key, Value: value})
	}
	return tags
}

func nlbLoadBalancerTags(selector map[string]string) []nlb.ListLoadBalancersTag {
	tags := make([]nlb.ListLoadBalancersTag, 0, len(selector))
	for key, value := range selector {
		tags = append(tags, nlb.ListLoadBalancersTag{Key: key, Value: value})
	}
	return tags
}

func newSLBClient(cfg *Config) (*slb.Client, error) {
	client, err := slb.NewClientWithAccessKey(cfg.AlibabaCloud.RegionID, cfg.AlibabaCloud.AccessKeyID, cfg.AlibabaCloud.AccessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("create SLB client: %w", err)
	}
	return client, nil
}

func newALBClient(cfg *Config) (*alb.Client, error) {
	client, err := alb.NewClientWithAccessKey(cfg.AlibabaCloud.RegionID, cfg.AlibabaCloud.AccessKeyID, cfg.AlibabaCloud.AccessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("create ALB client: %w", err)
	}
	return client, nil
}

func newNLBClient(cfg *Config) (*nlb.Client, error) {
	client, err := nlb.NewClientWithAccessKey(cfg.AlibabaCloud.RegionID, cfg.AlibabaCloud.AccessKeyID, cfg.AlibabaCloud.AccessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("create NLB client: %w", err)
	}
	return client, nil
}

func isNotFoundOrDependencyError(err error) bool {
	if err == nil || isClusterNotFound(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "dependency") ||
		strings.Contains(msg, "dependence") ||
		strings.Contains(msg, "in use") ||
		strings.Contains(msg, "inuse") ||
		strings.Contains(msg, "incorrectstatus")
}
