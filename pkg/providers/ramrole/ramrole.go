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

package ramrole

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Provider handles RAM role operations for Alibaba Cloud
type Provider struct {
	ramClient clients.RAMClient
	region    string
}

// NewProvider creates a new RAM provider
func NewProvider(ramClient clients.RAMClient, region string) *Provider {
	return &Provider{
		ramClient: ramClient,
		region:    region,
	}
}

// ValidateRoleResult contains the validation result
type ValidateRoleResult struct {
	Valid   bool
	Reason  string
	Message string
}

// ValidateRole validates that the RAM role exists and has the correct trust policy
func (p *Provider) ValidateRole(ctx context.Context, roleName string) *ValidateRoleResult {
	if roleName == "" {
		// Validation successful
		return &ValidateRoleResult{
			Valid:   true,
			Reason:  "RAMRoleResolved",
			Message: fmt.Sprintf("RAM role %s is valid", roleName),
		}
	}

	logger := log.FromContext(ctx)

	// Get the RAM role
	response, err := p.ramClient.GetRole(ctx, roleName)
	if err != nil {
		logger.Error(err, "failed to get RAM role", "role", roleName)
		return &ValidateRoleResult{
			Valid:   false,
			Reason:  "RoleNotFound",
			Message: fmt.Sprintf("RAM role %s does not exist", roleName),
		}
	}

	// Validate AssumeRolePolicy (must allow ECS service to assume the role)
	type AssumeRolePolicy struct {
		Statement []struct {
			Effect    string `json:"Effect"`
			Principal struct {
				Service []string `json:"Service"`
			} `json:"Principal"`
			Action string `json:"Action"`
		} `json:"Statement"`
	}

	var policy AssumeRolePolicy
	if err := json.Unmarshal([]byte(response.Role.AssumeRolePolicyDocument), &policy); err != nil {
		logger.Error(err, "failed to parse AssumeRolePolicy", "role", roleName)
		return &ValidateRoleResult{
			Valid:   false,
			Reason:  "InvalidPolicy",
			Message: "Failed to parse AssumeRolePolicy",
		}
	}

	// Check if the policy includes ecs.aliyuncs.com
	hasECSService := false
	for _, stmt := range policy.Statement {
		for _, svc := range stmt.Principal.Service {
			if svc == "ecs.aliyuncs.com" {
				hasECSService = true
				break
			}
		}
		if hasECSService {
			break
		}
	}

	if !hasECSService {
		return &ValidateRoleResult{
			Valid:   false,
			Reason:  "InvalidAssumePolicy",
			Message: "Role cannot be assumed by ECS service",
		}
	}

	// Validation successful
	return &ValidateRoleResult{
		Valid:   true,
		Reason:  "RAMRoleResolved",
		Message: fmt.Sprintf("RAM role %s is valid", roleName),
	}
}
