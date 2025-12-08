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

	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
)

// CSClient is an interface for Container Service client operations
type CSClient interface {
	DescribeClusterAttachScripts(ctx context.Context, clusterID string, request *cs.DescribeClusterAttachScriptsRequest) (string, error)
}

// DefaultCSClient implements CSClient using Alibaba Cloud SDK
type DefaultCSClient struct {
	client *cs.Client
}

// NewDefaultCSClient creates a new default CS client adapter
func NewDefaultCSClient(client *cs.Client) CSClient {
	return &DefaultCSClient{
		client: client,
	}
}

// DescribeClusterAttachScripts implements CSClient interface
func (c *DefaultCSClient) DescribeClusterAttachScripts(ctx context.Context, clusterID string, request *cs.DescribeClusterAttachScriptsRequest) (string, error) {
	response, err := c.client.DescribeClusterAttachScripts(&clusterID, request)
	if err != nil {
		return "", err
	}

	if response.Body == nil {
		return "", nil
	}

	return *response.Body, nil
}
