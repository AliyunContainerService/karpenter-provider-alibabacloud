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

package launchtemplate

import (
	"context"
	"fmt"
	"time"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Provider handles launch template operations for Alibaba Cloud
type Provider struct {
	region    string
	ecsClient clients.ECSClient
}

// LaunchTemplate represents an ECS launch template
type LaunchTemplate struct {
	ID      string
	Name    string
	Version string
}

// LaunchTemplateData contains the instance-affecting fields from a launch template version.
type LaunchTemplateData struct {
	ImageID          string
	InstanceType     string
	VSwitchID        string
	ZoneID           string
	SecurityGroupIDs []string
}

type launchTemplateVersionDescriber interface {
	DescribeLaunchTemplateVersions(context.Context, *ecs.DescribeLaunchTemplateVersionsRequest) (*ecs.DescribeLaunchTemplateVersionsResponse, error)
}

// NewProvider creates a new launch template provider
func NewProvider(region string, ecsClient clients.ECSClient) *Provider {
	return &Provider{
		region:    region,
		ecsClient: ecsClient,
	}
}

// Create creates a new launch template
func (p *Provider) Create(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass, userData string) (*LaunchTemplate, error) {
	logger := log.FromContext(ctx)

	// Generate a unique name for the launch template
	name := fmt.Sprintf("karpenter-%s-%d", nodeClass.Name, time.Now().Unix())

	// Create launch template request
	request := &ecs.CreateLaunchTemplateRequest{
		LaunchTemplateName: tea.String(name),
		RegionId:           tea.String(p.region),
	}

	// Set launch template parameters from nodeClass
	if len(nodeClass.Status.Images) > 0 {
		image := nodeClass.Status.Images[0]
		request.ImageId = tea.String(image.ID)
	}

	// Set security groups if specified
	if len(nodeClass.Status.SecurityGroups) > 0 {
		// Extract security group IDs from resolved security groups
		var sgIDs []*string
		for _, sg := range nodeClass.Status.SecurityGroups {
			sgIDs = append(sgIDs, tea.String(sg.ID))
		}
		request.SecurityGroupIds = sgIDs
	}

	// Set VSwitch ID if specified
	if nodeClass.Status.VSwitches != nil && len(nodeClass.Status.VSwitches) > 0 {
		// Use the first available VSwitch
		// In production, you might want to implement zone-aware selection
		request.VSwitchId = tea.String(nodeClass.Status.VSwitches[0].ID)
	}

	// Set user data
	if userData != "" {
		request.UserData = tea.String(userData)
	}

	// Set instance charge type (spot or on-demand)
	if nodeClass.Spec.SpotStrategy != nil {
		request.SpotStrategy = tea.String(string(*nodeClass.Spec.SpotStrategy))
	}

	// Set system disk configuration if specified
	if nodeClass.Spec.SystemDisk != nil {
		systemDisk := &ecs.CreateLaunchTemplateRequestSystemDisk{}
		if nodeClass.Spec.SystemDisk.Category != "" {
			systemDisk.Category = tea.String(nodeClass.Spec.SystemDisk.Category)
		}
		if nodeClass.Spec.SystemDisk.Size != nil {
			systemDisk.Size = tea.Int32(int32(*nodeClass.Spec.SystemDisk.Size))
		}
		request.SystemDisk = systemDisk
	}

	// Execute request
	response, err := p.ecsClient.CreateLaunchTemplate(ctx, request)
	if err != nil {
		logger.Error(err, "failed to create launch template")
		return nil, fmt.Errorf("failed to create launch template: %w", err)
	}

	logger.Info("created launch template", "id", *response.Body.LaunchTemplateId, "name", name)

	return &LaunchTemplate{
		ID:      *response.Body.LaunchTemplateId,
		Name:    name,
		Version: fmt.Sprintf("%d", *response.Body.LaunchTemplateVersionNumber), // Default version for new templates
	}, nil
}

// Get gets a launch template by ID
func (p *Provider) Get(ctx context.Context, id string) (*LaunchTemplate, error) {
	logger := log.FromContext(ctx)

	// Create describe launch templates request
	request := &ecs.DescribeLaunchTemplatesRequest{
		LaunchTemplateId: []*string{tea.String(id)},
	}

	// Execute request
	response, err := p.ecsClient.DescribeLaunchTemplates(ctx, request)
	if err != nil {
		logger.Error(err, "failed to describe launch template", "id", id)
		return nil, fmt.Errorf("failed to describe launch template %s: %w", id, err)
	}

	// Check if launch template exists
	if len(response.Body.LaunchTemplateSets.LaunchTemplateSet) == 0 {
		return nil, fmt.Errorf("launch template %s not found", id)
	}

	launchTemplateSet := response.Body.LaunchTemplateSets.LaunchTemplateSet[0]

	return &LaunchTemplate{
		ID:      *launchTemplateSet.LaunchTemplateId,
		Name:    *launchTemplateSet.LaunchTemplateName,
		Version: fmt.Sprintf("%d", *launchTemplateSet.DefaultVersionNumber),
	}, nil
}

// GetData resolves the concrete configuration from a launch template version.
func (p *Provider) GetData(ctx context.Context, id string, version *int64) (*LaunchTemplateData, error) {
	describer, ok := p.ecsClient.(launchTemplateVersionDescriber)
	if !ok {
		return nil, fmt.Errorf("ECS client does not support DescribeLaunchTemplateVersions")
	}

	request := &ecs.DescribeLaunchTemplateVersionsRequest{
		RegionId:         tea.String(p.region),
		LaunchTemplateId: tea.String(id),
		DetailFlag:       tea.Bool(true),
		PageSize:         tea.Int32(1),
	}
	if version != nil {
		request.LaunchTemplateVersion = []*int64{tea.Int64(*version)}
	} else {
		request.DefaultVersion = tea.Bool(true)
	}

	response, err := describer.DescribeLaunchTemplateVersions(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("describe launch template versions: %w", err)
	}
	if response == nil || response.Body == nil || response.Body.LaunchTemplateVersionSets == nil ||
		len(response.Body.LaunchTemplateVersionSets.LaunchTemplateVersionSet) == 0 ||
		response.Body.LaunchTemplateVersionSets.LaunchTemplateVersionSet[0].LaunchTemplateData == nil {
		return nil, fmt.Errorf("launch template version not found")
	}

	data := response.Body.LaunchTemplateVersionSets.LaunchTemplateVersionSet[0].LaunchTemplateData
	result := &LaunchTemplateData{}
	if data.ImageId != nil {
		result.ImageID = *data.ImageId
	}
	if data.InstanceType != nil {
		result.InstanceType = *data.InstanceType
	}
	if data.VSwitchId != nil {
		result.VSwitchID = *data.VSwitchId
	}
	if data.ZoneId != nil {
		result.ZoneID = *data.ZoneId
	}
	if data.SecurityGroupId != nil {
		result.SecurityGroupIDs = append(result.SecurityGroupIDs, *data.SecurityGroupId)
	}
	if data.SecurityGroupIds != nil {
		for _, id := range data.SecurityGroupIds.SecurityGroupId {
			if id != nil {
				result.SecurityGroupIDs = append(result.SecurityGroupIDs, *id)
			}
		}
	}
	if data.NetworkInterfaces != nil && len(data.NetworkInterfaces.NetworkInterface) > 0 {
		networkInterface := data.NetworkInterfaces.NetworkInterface[0]
		if result.VSwitchID == "" && networkInterface.VSwitchId != nil {
			result.VSwitchID = *networkInterface.VSwitchId
		}
		if networkInterface.SecurityGroupId != nil {
			result.SecurityGroupIDs = append(result.SecurityGroupIDs, *networkInterface.SecurityGroupId)
		}
		if networkInterface.SecurityGroupIds != nil {
			for _, id := range networkInterface.SecurityGroupIds.SecurityGroupId {
				if id != nil {
					result.SecurityGroupIDs = append(result.SecurityGroupIDs, *id)
				}
			}
		}
	}
	return result, nil
}

// Delete deletes a launch template by ID
func (p *Provider) Delete(ctx context.Context, id string) error {
	logger := log.FromContext(ctx)

	// Create delete launch template request
	request := &ecs.DeleteLaunchTemplateRequest{
		LaunchTemplateId: tea.String(id),
	}

	// Execute request
	_, err := p.ecsClient.DeleteLaunchTemplate(ctx, request)
	if err != nil {
		logger.Error(err, "failed to delete launch template", "id", id)
		return fmt.Errorf("failed to delete launch template %s: %w", id, err)
	}

	logger.Info("deleted launch template", "id", id)
	return nil
}

// Resolve resolves launch template selectors to actual launch templates
func (p *Provider) Resolve(ctx context.Context, nodeClass *v1alpha1.ECSNodeClass) (*LaunchTemplate, error) {
	// If launch template is specified in node class, use it
	if nodeClass.Spec.LaunchTemplateID != nil {
		return p.Get(ctx, *nodeClass.Spec.LaunchTemplateID)
	}

	// For now, we'll create a new one
	userData := "" // In a real implementation, you would generate user data
	return p.Create(ctx, nodeClass, userData)
}
