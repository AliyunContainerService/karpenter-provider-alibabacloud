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

	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
)

// VPCClient is an interface for VPC client operations
type VPCClient interface {
	DescribeVSwitches(ctx context.Context, vSwitchID string, tags map[string]string) (*vpc.DescribeVSwitchesResponse, error)
}

// DefaultVPCClient implements VPCClient using Alibaba Cloud SDK
type DefaultVPCClient struct {
	client *vpc.Client
	region string
}

// NewDefaultVPCClient creates a new default VPC client adapter
func NewDefaultVPCClient(client *vpc.Client, region string) *DefaultVPCClient {
	return &DefaultVPCClient{
		client: client,
		region: region,
	}
}

// DescribeVSwitches implements VPCClient interface
func (c *DefaultVPCClient) DescribeVSwitches(ctx context.Context, vSwitchID string, tags map[string]string) (*vpc.DescribeVSwitchesResponse, error) {
	request := vpc.CreateDescribeVSwitchesRequest()
	request.Scheme = "https"
	request.RegionId = c.region

	// Set VSwitch ID if provided
	if vSwitchID != "" {
		request.VSwitchId = vSwitchID
	}

	// Set tag filters
	if len(tags) > 0 {
		var vpcTags []vpc.DescribeVSwitchesTag
		for key, value := range tags {
			vpcTags = append(vpcTags, vpc.DescribeVSwitchesTag{
				Key:   key,
				Value: value,
			})
		}
		request.Tag = &vpcTags
	}

	response, err := c.client.DescribeVSwitches(request)
	if err != nil {
		return nil, err
	}

	return response, nil
}
