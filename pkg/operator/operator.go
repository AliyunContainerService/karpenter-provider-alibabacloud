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

package operator

import (
	"context"
	"fmt"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	"github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	"github.com/alibabacloud-go/tea/tea"
	"net/http"
	"os"
	"time"

	aliv1alpha1 "github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/cache"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/operator/options"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/bootstrap"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/capacityreservation"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/imagefamily"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instance"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instanceprofile"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instancetype"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/launchtemplate"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/pricing"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/ramrole"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/securitygroup"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/vswitch"
	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth/credentials/provider"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ecs"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/ram"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/vpc"
	"github.com/awslabs/operatorpkg/controller"
	"sigs.k8s.io/karpenter/pkg/cloudprovider"
	coreoperator "sigs.k8s.io/karpenter/pkg/operator"
)

// Environment variable names for custom endpoints
const (
	EnvECSEndpoint = "ECS_ENDPOINT"
	EnvVPCEndpoint = "VPC_ENDPOINT"
	EnvRAMEndpoint = "RAM_ENDPOINT"
	EnvCSEndpoint  = "CS_ENDPOINT"
)

var (
	transport = &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 50,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		// Explicitly enable HTTP/2 support
		ForceAttemptHTTP2: true,
	}
)

// Operator is injected into the Alibaba Cloud CloudProvider's factories
type Operator struct {
	*coreoperator.Operator

	// Alibaba Cloud specific configuration
	Region            string
	ClusterName       string
	InterruptionQueue string

	// Alibaba Cloud SDK clients
	ECSClient clients.ECSClient
	VPCClient clients.VPCClient
	RAMClient clients.RAMClient
	CSClient  clients.CSClient

	// Cache layers
	UnavailableOfferingsCache *cache.UnavailableOfferingsCache

	// Providers
	VSwitchProvider             *vswitch.Provider
	SecurityGroupProvider       *securitygroup.Provider
	ImageFamilyProvider         *imagefamily.Provider
	BootstrapProvider           *bootstrap.Provider
	PricingProvider             *pricing.Provider
	InstanceTypeProvider        *instancetype.Provider
	InstanceProvider            *instance.Provider
	InstanceProfileProvider     *instanceprofile.Provider
	RAMProvider                 *ramrole.Provider
	LaunchTemplateProvider      *launchtemplate.Provider
	CapacityReservationProvider *capacityreservation.Provider

	// CloudProvider
	CloudProvider cloudprovider.CloudProvider
}

// Close closes the operator and releases resources
func (o *Operator) Close() error {
	// Note: Most Alibaba Cloud SDK clients don't have explicit Close methods
	// The Go HTTP client handles connection pooling automatically
	if o.InstanceProvider != nil && o.InstanceProvider.Batcher != nil {
		o.InstanceProvider.Batcher.Close()
	}
	return nil
}

// WithControllers registers a set of Alibaba Cloud-specific controllers with the operator
func (o *Operator) WithControllers(ctx context.Context, controllers ...controller.Controller) *Operator {
	for _, c := range controllers {
		if err := c.Register(ctx, o.Manager); err != nil {
			panic(fmt.Sprintf("failed to register controller: %v", err))
		}
	}
	return o
}

// InitECSClient initializes the Alibaba Cloud ECS client
// Supports both environment variables and config file credentials through default credential chain
func InitECSClient(region, accessKeyID, accessKeySecret string) (clients.ECSClient, error) {
	if accessKeyID != "" && accessKeySecret != "" {
		// Use explicit credentials from environment variables
		client, err := ecs.NewClientWithAccessKey(region, accessKeyID, accessKeySecret)
		if err != nil {
			return nil, fmt.Errorf("failed to create ECS client with access key: %w", err)
		}
		// Configure HTTP client parameters to prevent connection leaks
		client.GetConfig().WithTimeout(30 * time.Second)
		client.GetConfig().WithGoRoutinePoolSize(10)
		if os.Getenv(EnvECSEndpoint) != "" {
			client.EndpointMap = map[string]string{
				region: os.Getenv(EnvECSEndpoint),
			}
		}
		client.GetConfig().WithHttpTransport(transport)

		return clients.NewDefaultECSClient(client, region), nil
	}

	// Use default credential chain which includes:
	// 1. Environment variables
	// 2. CLI config file (~/.aliyun/config.json)
	// 3. Profile config file
	// 4. ECS RAM role (if running on ECS)
	// 5. Credentials URI (if ALIBABA_CLOUD_CREDENTIALS_URI is set)
	client, err := ecs.NewClientWithProvider(region, provider.DefaultChain)
	if err != nil {
		return nil, fmt.Errorf("failed to create ECS client with default credentials: %w", err)
	}
	// Configure HTTP client parameters to prevent connection leaks
	client.GetConfig().WithTimeout(30 * time.Second)
	client.GetConfig().WithGoRoutinePoolSize(10)
	client.GetConfig().WithHttpTransport(transport)

	return clients.NewDefaultECSClient(client, region), nil
}

// InitVPCClient initializes the Alibaba Cloud VPC client
func InitVPCClient(region, accessKeyID, accessKeySecret string) (clients.VPCClient, error) {
	if accessKeyID != "" && accessKeySecret != "" {
		// Use explicit credentials from environment variables
		client, err := vpc.NewClientWithAccessKey(region, accessKeyID, accessKeySecret)
		if err != nil {
			return nil, fmt.Errorf("failed to create VPC client: %w", err)
		}
		// Configure HTTP client parameters to prevent connection leaks
		client.GetConfig().WithTimeout(30 * time.Second)
		client.GetConfig().WithGoRoutinePoolSize(10)
		if os.Getenv(EnvVPCEndpoint) != "" {
			client.EndpointMap = map[string]string{
				region: os.Getenv(EnvVPCEndpoint),
			}
		}
		client.GetConfig().WithHttpTransport(transport)

		return clients.NewDefaultVPCClient(client, region), nil
	}

	// Use default credential chain
	client, err := vpc.NewClientWithProvider(region, provider.DefaultChain)
	if err != nil {
		return nil, fmt.Errorf("failed to create VPC client with default credentials: %w", err)
	}
	// Configure HTTP client parameters to prevent connection leaks
	client.GetConfig().WithTimeout(30 * time.Second)
	client.GetConfig().WithGoRoutinePoolSize(10)

	// Configure HTTP transport with proper connection pooling
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 50,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		// Explicitly enable HTTP/2 support
		ForceAttemptHTTP2: true,
	}
	client.GetConfig().WithHttpTransport(transport)

	return clients.NewDefaultVPCClient(client, region), nil
}

// InitRAMClient initializes the Alibaba Cloud RAM client
func InitRAMClient(region, accessKeyID, accessKeySecret string) (clients.RAMClient, error) {
	if accessKeyID != "" && accessKeySecret != "" {
		// Use explicit credentials from environment variables
		client, err := ram.NewClientWithAccessKey(region, accessKeyID, accessKeySecret)
		if err != nil {
			return nil, fmt.Errorf("failed to create RAM client: %w", err)
		}
		// Configure HTTP client parameters to prevent connection leaks
		client.GetConfig().WithTimeout(30 * time.Second)
		client.GetConfig().WithGoRoutinePoolSize(10)
		if os.Getenv(EnvRAMEndpoint) != "" {
			client.EndpointMap = map[string]string{
				region: os.Getenv(EnvRAMEndpoint),
			}
		}
		client.GetConfig().WithHttpTransport(transport)

		return clients.NewDefaultRAMClient(client, region), nil
	}

	// Use default credential chain
	client, err := ram.NewClientWithProvider(region, provider.DefaultChain)
	if err != nil {
		return nil, fmt.Errorf("failed to create RAM client with default credentials: %w", err)
	}
	// Configure HTTP client parameters to prevent connection leaks
	client.GetConfig().WithTimeout(30 * time.Second)
	client.GetConfig().WithGoRoutinePoolSize(10)
	client.GetConfig().WithHttpTransport(transport)

	return clients.NewDefaultRAMClient(client, region), nil
}

func InitCSClient(region, accessKeyID, accessKeySecret string) (clients.CSClient, error) {
	if accessKeyID != "" && accessKeySecret != "" {
		csClient, err := cs.NewClient(&utils.Config{
			AccessKeyId:     tea.String(accessKeyID),
			AccessKeySecret: tea.String(accessKeySecret),
			RegionId:        tea.String(region),
			ConnectTimeout:  tea.Int(30),
			MaxIdleConns:    tea.Int(100),
		})
		if os.Getenv(EnvCSEndpoint) != "" {
			csClient.Endpoint = tea.String(os.Getenv(EnvCSEndpoint))
		}
		if err != nil {
			return nil, err
		}
		return clients.NewDefaultCSClient(csClient), nil
	}

	return nil, fmt.Errorf("failed to create CS client without ak,sk")
}

// NewOperator creates a new Alibaba Cloud Karpenter Operator
func NewOperator(ctx context.Context, coreOp *coreoperator.Operator) (*Operator, error) {
	// Get options from context
	opts := options.FromContext(ctx)

	// Add Alibaba Cloud API types to the scheme
	if err := aliv1alpha1.AddToScheme(coreOp.Manager.GetScheme()); err != nil {
		return nil, fmt.Errorf("failed to register Alibaba Cloud API types: %w", err)
	}

	// Initialize Alibaba Cloud SDK clients
	ecsClient, err := InitECSClient(opts.Region, opts.AccessKeyID, opts.AccessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize ECS client: %w", err)
	}

	vpcClient, err := InitVPCClient(opts.Region, opts.AccessKeyID, opts.AccessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize VPC client: %w", err)
	}

	ramClient, err := InitRAMClient(opts.Region, opts.AccessKeyID, opts.AccessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize RAM client: %w", err)
	}

	csClient, err := InitCSClient(opts.Region, opts.AccessKeyID, opts.AccessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize CS client: %w", err)
	}
	// Initialize cache
	unavailableOfferingsCache := cache.NewUnavailableOfferingsCache()

	// Initialize providers
	vswitchProvider := vswitch.NewProvider(opts.Region, vpcClient)
	securityGroupProvider := securitygroup.NewProvider(opts.Region, ecsClient)
	pricingProvider := pricing.NewProvider(opts.Region, ecsClient)
	bootstrapProvider := bootstrap.NewProvider(opts.Region, csClient)
	launchTemplateProvider := launchtemplate.NewProvider(opts.Region, ecsClient)
	instanceProfileProvider := instanceprofile.NewProvider(ramClient, opts.Region)
	ramProvider := ramrole.NewProvider(ramClient, opts.Region)
	instanceTypeProvider := instancetype.NewProvider(opts.Region, ecsClient)
	imageFamilyProvider := imagefamily.NewProvider(ecsClient)
	instanceProvider := instance.NewProvider(ctx, opts.Region, ecsClient)
	capacityReservationProvider := capacityreservation.NewProvider(opts.Region, ecsClient)

	// Remove the manual creation of coreOp since we're now receiving it as a parameter
	result := &Operator{
		Operator:                    coreOp,
		Region:                      opts.Region,
		ClusterName:                 opts.ClusterName,
		InterruptionQueue:           opts.InterruptionQueue,
		ECSClient:                   ecsClient,
		VPCClient:                   vpcClient,
		RAMClient:                   ramClient,
		UnavailableOfferingsCache:   unavailableOfferingsCache,
		VSwitchProvider:             vswitchProvider,
		SecurityGroupProvider:       securityGroupProvider,
		ImageFamilyProvider:         imageFamilyProvider,
		BootstrapProvider:           bootstrapProvider,
		PricingProvider:             pricingProvider,
		InstanceTypeProvider:        instanceTypeProvider,
		InstanceProvider:            instanceProvider,
		InstanceProfileProvider:     instanceProfileProvider,
		RAMProvider:                 ramProvider,
		LaunchTemplateProvider:      launchTemplateProvider,
		CapacityReservationProvider: capacityReservationProvider,
	}

	return result, nil
}
