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
	"os"
	"strings"

	"github.com/alibabacloud-go/darabonba-openapi/v2/client"
	"k8s.io/klog/v2"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/cluster"
	"github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/aliyun/credentials-go/credentials"

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
	ecs "github.com/alibabacloud-go/ecs-20140526/v5/client"
	ram "github.com/alibabacloud-go/ram-20150501/v2/client"
	vpc "github.com/alibabacloud-go/vpc-20160428/v7/client"
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
	EnvSTSEndpoint = "STS_ENDPOINT"

	// RRSA environment variable names
	EnvRoleARN          = "ALIBABA_CLOUD_ROLE_ARN"
	EnvOIDCProviderARN  = "ALIBABA_CLOUD_OIDC_PROVIDER_ARN"
	EnvOIDCTokenFile    = "ALIBABA_CLOUD_OIDC_TOKEN_FILE"
	RRSASessionName     = "karpenter-controller"
	RRSASessionDuration = 3600
)

// Operator is injected into the Alibaba Cloud CloudProvider's factories
type Operator struct {
	*coreoperator.Operator

	// Alibaba Cloud specific configuration
	Region               string
	ClusterName          string
	InterruptionQueue    string
	ClusterNetworkConfig *cluster.NetworkConfig

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
	var errs []error

	// Close the instance provider batcher
	if o.InstanceProvider != nil && o.InstanceProvider.Batcher != nil {
		o.InstanceProvider.Batcher.Close()
	}

	// Note: Alibaba Cloud SDK clients use standard HTTP transport with connection pooling.
	// The Go HTTP client automatically manages connections and doesn't require explicit cleanup.
	// However, if we need to force close connections, we can implement custom cleanup here.

	if len(errs) > 0 {
		return fmt.Errorf("errors during operator close: %v", errs)
	}
	return nil
}

// isRRSAEnabled checks if RRSA environment variables are configured and valid
func isRRSAEnabled() bool {
	roleArn := os.Getenv(EnvRoleARN)
	providerArn := os.Getenv(EnvOIDCProviderARN)
	tokenFile := os.Getenv(EnvOIDCTokenFile)

	// Check if all required environment variables are set
	if roleArn == "" || providerArn == "" || tokenFile == "" {
		return false
	}

	// Validate Role ARN format (acs:ram::account-id:role/role-name)
	if !strings.HasPrefix(roleArn, "acs:ram::") || !strings.Contains(roleArn, ":role/") {
		return false
	}

	// Validate OIDC Provider ARN format
	if !strings.HasPrefix(providerArn, "acs:ram::") || !strings.Contains(providerArn, ":oidc-provider/") {
		return false
	}

	// Validate token file path exists and is readable
	if _, err := os.Stat(tokenFile); err != nil {
		return false
	}

	// Validate token file is in the expected directory to prevent path traversal
	expectedPrefix := "/var/run/secrets/ack.alibabacloud.com/"
	if !strings.HasPrefix(tokenFile, expectedPrefix) {
		return false
	}

	return true
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
// Supports RRSA and explicit credentials (AK/SK)
func InitECSClient(region, accessKeyID, accessKeySecret string) (clients.ECSClient, error) {
	// Priority 1: RRSA (highest priority)
	if isRRSAEnabled() {
		config := &credentials.Config{
			Type:                  tea.String("oidc_role_arn"),
			OIDCProviderArn:       tea.String(os.Getenv(EnvOIDCProviderARN)),
			OIDCTokenFilePath:     tea.String(os.Getenv(EnvOIDCTokenFile)),
			RoleArn:               tea.String(os.Getenv(EnvRoleARN)),
			RoleSessionName:       tea.String(RRSASessionName),
			RoleSessionExpiration: tea.Int(RRSASessionDuration),
		}

		// Set STS endpoint if configured
		if stsEndpoint := os.Getenv(EnvSTSEndpoint); stsEndpoint != "" {
			config.STSEndpoint = tea.String(stsEndpoint)
		}

		cred, err := credentials.NewCredential(config)
		if err != nil {
			klog.ErrorS(err, "Failed to create RRSA credential for ECS",
				"roleArn", os.Getenv(EnvRoleARN),
				"oidcProvider", os.Getenv(EnvOIDCProviderARN),
				"tokenFile", os.Getenv(EnvOIDCTokenFile))
			return nil, fmt.Errorf("failed to create RRSA credential for ECS: %w", err)
		}

		ecsClient, err := ecs.NewClient(&client.Config{
			Credential:     cred,
			RegionId:       tea.String(region),
			ConnectTimeout: tea.Int(30),
			MaxIdleConns:   tea.Int(100),
		})
		if err != nil {
			klog.ErrorS(err, "Failed to create ECS client with RRSA", "region", region)
			return nil, fmt.Errorf("failed to create ECS client with RRSA: %w", err)
		}

		// Configure custom endpoint if specified
		if os.Getenv(EnvECSEndpoint) != "" {
			ecsClient.Endpoint = tea.String(os.Getenv(EnvECSEndpoint))
		}

		klog.InfoS("ECS client initialized with RRSA", "region", region)
		return clients.NewDefaultECSClient(ecsClient, region), nil
	}

	// Priority 2: Explicit credentials
	if accessKeyID != "" && accessKeySecret != "" {
		ecsClient, err := ecs.NewClient(&client.Config{
			AccessKeyId:     tea.String(accessKeyID),
			AccessKeySecret: tea.String(accessKeySecret),
			RegionId:        tea.String(region),
			ConnectTimeout:  tea.Int(30),
			MaxIdleConns:    tea.Int(100),
		})
		if err != nil {
			klog.ErrorS(err, "Failed to create ECS client with access key", "region", region)
			return nil, fmt.Errorf("failed to create ECS client with access key: %w", err)
		}

		// Configure custom endpoint if specified
		if os.Getenv(EnvECSEndpoint) != "" {
			ecsClient.Endpoint = tea.String(os.Getenv(EnvECSEndpoint))
		}

		klog.InfoS("ECS client initialized with AK/SK", "region", region)
		return clients.NewDefaultECSClient(ecsClient, region), nil
	}

	return nil, fmt.Errorf("failed to create ECS client: no valid credentials provided (RRSA or AK/SK required)")
}

// InitVPCClient initializes the Alibaba Cloud VPC client
// Supports RRSA and explicit credentials (AK/SK)
func InitVPCClient(region, accessKeyID, accessKeySecret string) (clients.VPCClient, error) {
	// Priority 1: RRSA (highest priority)
	if isRRSAEnabled() {
		config := &credentials.Config{
			Type:                  tea.String("oidc_role_arn"),
			OIDCProviderArn:       tea.String(os.Getenv(EnvOIDCProviderARN)),
			OIDCTokenFilePath:     tea.String(os.Getenv(EnvOIDCTokenFile)),
			RoleArn:               tea.String(os.Getenv(EnvRoleARN)),
			RoleSessionName:       tea.String(RRSASessionName),
			RoleSessionExpiration: tea.Int(RRSASessionDuration),
		}

		// Set STS endpoint if configured
		if stsEndpoint := os.Getenv(EnvSTSEndpoint); stsEndpoint != "" {
			config.STSEndpoint = tea.String(stsEndpoint)
		}

		cred, err := credentials.NewCredential(config)
		if err != nil {
			klog.ErrorS(err, "Failed to create RRSA credential for VPC",
				"roleArn", os.Getenv(EnvRoleARN))
			return nil, fmt.Errorf("failed to create RRSA credential for VPC: %w", err)
		}

		vpcClient, err := vpc.NewClient(&utils.Config{
			Credential:     cred,
			RegionId:       tea.String(region),
			ConnectTimeout: tea.Int(30),
			MaxIdleConns:   tea.Int(100),
		})
		if err != nil {
			klog.ErrorS(err, "Failed to create VPC client with RRSA", "region", region)
			return nil, fmt.Errorf("failed to create VPC client with RRSA: %w", err)
		}

		// Configure custom endpoint if specified
		if os.Getenv(EnvVPCEndpoint) != "" {
			vpcClient.Endpoint = tea.String(os.Getenv(EnvVPCEndpoint))
		}

		klog.InfoS("VPC client initialized with RRSA", "region", region)
		return clients.NewDefaultVPCClient(vpcClient, region), nil
	}

	// Priority 2: Explicit credentials
	if accessKeyID != "" && accessKeySecret != "" {
		vpcClient, err := vpc.NewClient(&utils.Config{
			AccessKeyId:     tea.String(accessKeyID),
			AccessKeySecret: tea.String(accessKeySecret),
			RegionId:        tea.String(region),
			ConnectTimeout:  tea.Int(30),
			MaxIdleConns:    tea.Int(100),
		})
		if err != nil {
			klog.ErrorS(err, "Failed to create VPC client with access key", "region", region)
			return nil, fmt.Errorf("failed to create VPC client with access key: %w", err)
		}

		// Configure custom endpoint if specified
		if os.Getenv(EnvVPCEndpoint) != "" {
			vpcClient.Endpoint = tea.String(os.Getenv(EnvVPCEndpoint))
		}

		klog.InfoS("VPC client initialized with AK/SK", "region", region)
		return clients.NewDefaultVPCClient(vpcClient, region), nil
	}

	return nil, fmt.Errorf("failed to create VPC client: no valid credentials provided (RRSA or AK/SK required)")
}

// InitRAMClient initializes the Alibaba Cloud RAM client
// Supports RRSA and explicit credentials (AK/SK)
func InitRAMClient(region, accessKeyID, accessKeySecret string) (clients.RAMClient, error) {
	// Priority 1: RRSA (highest priority)
	if isRRSAEnabled() {
		config := &credentials.Config{
			Type:                  tea.String("oidc_role_arn"),
			OIDCProviderArn:       tea.String(os.Getenv(EnvOIDCProviderARN)),
			OIDCTokenFilePath:     tea.String(os.Getenv(EnvOIDCTokenFile)),
			RoleArn:               tea.String(os.Getenv(EnvRoleARN)),
			RoleSessionName:       tea.String(RRSASessionName),
			RoleSessionExpiration: tea.Int(RRSASessionDuration),
		}

		// Set STS endpoint if configured
		if stsEndpoint := os.Getenv(EnvSTSEndpoint); stsEndpoint != "" {
			config.STSEndpoint = tea.String(stsEndpoint)
		}

		cred, err := credentials.NewCredential(config)
		if err != nil {
			klog.ErrorS(err, "Failed to create RRSA credential for RAM",
				"roleArn", os.Getenv(EnvRoleARN))
			return nil, fmt.Errorf("failed to create RRSA credential for RAM: %w", err)
		}

		ramClient, err := ram.NewClient(&utils.Config{
			Credential:     cred,
			RegionId:       tea.String(region),
			ConnectTimeout: tea.Int(30),
			MaxIdleConns:   tea.Int(100),
		})
		if err != nil {
			klog.ErrorS(err, "Failed to create RAM client with RRSA", "region", region)
			return nil, fmt.Errorf("failed to create RAM client with RRSA: %w", err)
		}

		// Configure custom endpoint if specified
		if os.Getenv(EnvRAMEndpoint) != "" {
			ramClient.Endpoint = tea.String(os.Getenv(EnvRAMEndpoint))
		}

		klog.InfoS("RAM client initialized with RRSA", "region", region)
		return clients.NewDefaultRAMClient(ramClient, region), nil
	}

	// Priority 2: Explicit credentials
	if accessKeyID != "" && accessKeySecret != "" {
		ramClient, err := ram.NewClient(&utils.Config{
			AccessKeyId:     tea.String(accessKeyID),
			AccessKeySecret: tea.String(accessKeySecret),
			RegionId:        tea.String(region),
			ConnectTimeout:  tea.Int(30),
			MaxIdleConns:    tea.Int(100),
		})
		if err != nil {
			klog.ErrorS(err, "Failed to create RAM client with access key", "region", region)
			return nil, fmt.Errorf("failed to create RAM client with access key: %w", err)
		}

		// Configure custom endpoint if specified
		if os.Getenv(EnvRAMEndpoint) != "" {
			ramClient.Endpoint = tea.String(os.Getenv(EnvRAMEndpoint))
		}

		klog.InfoS("RAM client initialized with AK/SK", "region", region)
		return clients.NewDefaultRAMClient(ramClient, region), nil
	}

	return nil, fmt.Errorf("failed to create RAM client: no valid credentials provided (RRSA or AK/SK required)")
}

// InitCSClient initializes the Alibaba Cloud CS (Container Service) client
// Supports RRSA and explicit credentials
func InitCSClient(region, accessKeyID, accessKeySecret string) (clients.CSClient, error) {
	// Priority 1: RRSA (highest priority)
	if isRRSAEnabled() {
		config := &credentials.Config{
			Type:                  tea.String("oidc_role_arn"),
			OIDCProviderArn:       tea.String(os.Getenv(EnvOIDCProviderARN)),
			OIDCTokenFilePath:     tea.String(os.Getenv(EnvOIDCTokenFile)),
			RoleArn:               tea.String(os.Getenv(EnvRoleARN)),
			RoleSessionName:       tea.String(RRSASessionName),
			RoleSessionExpiration: tea.Int(RRSASessionDuration),
		}

		// Set STS endpoint if configured
		if stsEndpoint := os.Getenv(EnvSTSEndpoint); stsEndpoint != "" {
			config.STSEndpoint = tea.String(stsEndpoint)
		}

		cred, err := credentials.NewCredential(config)
		if err != nil {
			klog.ErrorS(err, "Failed to create RRSA credential for CS",
				"roleArn", os.Getenv(EnvRoleARN))
			return nil, fmt.Errorf("failed to create RRSA credential for CS: %w", err)
		}

		csClient, err := cs.NewClient(&utils.Config{
			Credential:     cred,
			RegionId:       tea.String(region),
			ConnectTimeout: tea.Int(30),
			MaxIdleConns:   tea.Int(100),
		})
		if err != nil {
			klog.ErrorS(err, "Failed to create CS client with RRSA", "region", region)
			return nil, fmt.Errorf("failed to create CS client with RRSA: %w", err)
		}

		// Configure custom endpoint if specified
		if os.Getenv(EnvCSEndpoint) != "" {
			csClient.Endpoint = tea.String(os.Getenv(EnvCSEndpoint))
		}

		klog.InfoS("CS client initialized with RRSA", "region", region)
		return clients.NewDefaultCSClient(csClient), nil
	}

	// Priority 2: Explicit credentials
	if accessKeyID != "" && accessKeySecret != "" {
		csClient, err := cs.NewClient(&utils.Config{
			AccessKeyId:     tea.String(accessKeyID),
			AccessKeySecret: tea.String(accessKeySecret),
			RegionId:        tea.String(region),
			ConnectTimeout:  tea.Int(30),
			MaxIdleConns:    tea.Int(100),
		})
		if err != nil {
			klog.ErrorS(err, "Failed to create CS client with access key", "region", region)
			return nil, fmt.Errorf("failed to create CS client with access key: %w", err)
		}

		// Configure custom endpoint if specified
		if os.Getenv(EnvCSEndpoint) != "" {
			csClient.Endpoint = tea.String(os.Getenv(EnvCSEndpoint))
		}

		klog.InfoS("CS client initialized with AK/SK", "region", region)
		return clients.NewDefaultCSClient(csClient), nil
	}

	return nil, fmt.Errorf("failed to create CS client: no valid credentials provided (RRSA or AK/SK required)")
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

	// Initialize cluster network config
	networkConfig, err := cluster.InitializeClusterNetworkConfig(csClient, opts.ClusterName)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize cluster network config: %w", err)
	}
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
		CSClient:                    csClient,
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
		ClusterNetworkConfig:        networkConfig,
	}

	return result, nil
}
