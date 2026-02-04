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

package clients

import (
	"context"

	ram "github.com/alibabacloud-go/ram-20150501/v2/client"
)

// RAMClient is an interface for RAM client operations
type RAMClient interface {
	GetRole(ctx context.Context, roleName string) (*ram.GetRoleResponse, error)
}

// DefaultRAMClient implements RAMClient using Alibaba Cloud SDK
type DefaultRAMClient struct {
	client *ram.Client
	region string
}

// NewDefaultRAMClient creates a new default RAM client
func NewDefaultRAMClient(client *ram.Client, region string) *DefaultRAMClient {
	return &DefaultRAMClient{
		client: client,
		region: region,
	}
}

// GetRole implements RAMClient interface
func (c *DefaultRAMClient) GetRole(ctx context.Context, roleName string) (*ram.GetRoleResponse, error) {
	request := &ram.GetRoleRequest{}
	request.RoleName = &roleName

	return c.client.GetRole(request)
}
