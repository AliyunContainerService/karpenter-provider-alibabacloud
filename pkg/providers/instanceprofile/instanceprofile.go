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

package instanceprofile

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Provider handles instance profile operations for Alibaba Cloud
type Provider struct {
	ramClient clients.RAMClient
	region    string
}

// NewProvider creates a new instance profile provider
func NewProvider(ramClient clients.RAMClient, region string) *Provider {
	return &Provider{
		ramClient: ramClient,
		region:    region,
	}
}

// Resolve resolves instance profile selectors to actual instance profiles
func (p *Provider) Resolve(ctx context.Context, role *string, instanceProfile *string) (string, error) {
	// If instance profile is specified, use it directly
	if instanceProfile != nil {
		return *instanceProfile, nil
	}

	// If role is specified, resolve to instance profile
	if role != nil {
		logger := log.FromContext(ctx)
		profile, err := p.getByRole(ctx, *role)
		if err != nil {
			logger.Error(err, "failed to get instance profile by role", "role", *role)
			return "", fmt.Errorf("failed to get instance profile for role %s: %w", *role, err)
		}
		return profile, nil
	}

	// No instance profile or role specified
	return "", nil
}

// getByRole gets instance profile by role name
func (p *Provider) getByRole(ctx context.Context, roleName string) (string, error) {
	// Execute request
	response, err := p.ramClient.GetRole(ctx, roleName)
	if err != nil {
		return "", fmt.Errorf("failed to get role %s: %w", roleName, err)
	}

	// Return role ARN as instance profile
	return *response.Body.Role.Arn, nil
}

// Validate validates that the instance profile exists and is valid
func (p *Provider) Validate(ctx context.Context, instanceProfile string) error {
	// If no instance profile, nothing to validate
	if instanceProfile == "" {
		return nil
	}

	logger := log.FromContext(ctx)

	// Parse the instance profile ARN to get role name
	// ARN format: acs:ramrole::account-id:role/role-name
	roleName, err := parseRoleNameFromARN(instanceProfile)
	if err != nil {
		logger.Error(err, "failed to parse role name from ARN", "arn", instanceProfile)
		return fmt.Errorf("invalid instance profile ARN %s: %w", instanceProfile, err)
	}

	// Validate the role exists
	if err := p.validateRole(ctx, roleName); err != nil {
		logger.Error(err, "failed to validate role", "role", roleName)
		return fmt.Errorf("invalid role %s: %w", roleName, err)
	}

	return nil
}

// validateRole validates that the role exists and has the correct trust policy
func (p *Provider) validateRole(ctx context.Context, roleName string) error {
	// Execute request
	response, err := p.ramClient.GetRole(ctx, roleName)
	if err != nil {
		return fmt.Errorf("role %s does not exist: %w", roleName, err)
	}

	// Validate trust policy to ensure it allows ECS service to assume the role
	if err := p.validateTrustPolicy(ctx, *response.Body.Role.AssumeRolePolicyDocument); err != nil {
		return fmt.Errorf("role %s has invalid trust policy: %w", roleName, err)
	}

	return nil
}

// validateTrustPolicy validates that the trust policy allows ECS service to assume the role
func (p *Provider) validateTrustPolicy(ctx context.Context, policyDocument string) error {
	logger := log.FromContext(ctx)

	// Parse the policy document
	var policy PolicyDocument
	if err := json.Unmarshal([]byte(policyDocument), &policy); err != nil {
		logger.Error(err, "failed to parse trust policy document")
		return fmt.Errorf("failed to parse trust policy document: %w", err)
	}

	// Check if any statement allows ECS service to assume the role
	for _, statement := range policy.Statement {
		// Check if the action is "sts:AssumeRole"
		if statement.Action == "sts:AssumeRole" || (isArrayWithItem(statement.Action, "sts:AssumeRole")) {
			// Check if the principal includes ECS service
			if principalContainsECSService(statement.Principal) {
				// Trust policy is valid
				return nil
			}
		}
	}

	return fmt.Errorf("trust policy does not allow ECS service to assume the role")
}

// PolicyDocument represents a RAM policy document
type PolicyDocument struct {
	Statement []Statement `json:"Statement"`
}

// Statement represents a statement in a policy document
type Statement struct {
	Action    interface{} `json:"Action"`
	Principal Principal   `json:"Principal"`
}

// Principal represents the principal in a statement
type Principal struct {
	Service interface{} `json:"Service"`
}

// isArrayWithItem checks if the interface is an array and contains the specified item
func isArrayWithItem(arr interface{}, item string) bool {
	if arr == nil {
		return false
	}

	switch v := arr.(type) {
	case []interface{}:
		for _, elem := range v {
			if str, ok := elem.(string); ok && str == item {
				return true
			}
		}
	case []string:
		for _, elem := range v {
			if elem == item {
				return true
			}
		}
	}
	return false
}

// principalContainsECSService checks if the principal includes ECS service
func principalContainsECSService(principal Principal) bool {
	// Check if the service is "ecs.aliyuncs.com" or includes it in an array
	switch v := principal.Service.(type) {
	case string:
		return v == "ecs.aliyuncs.com"
	case []interface{}:
		for _, elem := range v {
			if str, ok := elem.(string); ok && str == "ecs.aliyuncs.com" {
				return true
			}
		}
	case []string:
		for _, elem := range v {
			if elem == "ecs.aliyuncs.com" {
				return true
			}
		}
	}
	return false
}

// parseRoleNameFromARN parses role name from ARN
func parseRoleNameFromARN(arn string) (string, error) {
	// ARN format: acs:ramrole::account-id:role/role-name
	// We need to extract role-name from the end

	// Find the last occurrence of "/"
	lastSlash := -1
	for i := len(arn) - 1; i >= 0; i-- {
		if arn[i] == '/' {
			lastSlash = i
			break
		}
	}

	if lastSlash == -1 || lastSlash == len(arn)-1 {
		return "", fmt.Errorf("invalid ARN format: %s", arn)
	}

	// Check if the ARN has the correct prefix
	if !strings.HasPrefix(arn, "acs:ramrole::") || !strings.Contains(arn, ":role/") {
		return "", fmt.Errorf("invalid RAM role ARN format: %s", arn)
	}

	return arn[lastSlash+1:], nil
}
