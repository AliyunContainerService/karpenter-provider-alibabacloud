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

package options

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
)

type optionsKey struct{}

const (
	PodDensityModeKubelet          = "kubelet"
	PodDensityModeTerwayENI        = "terway-eni"
	PodDensityModeTerwayENIMultiIP = "terway-eni-multi-ip"
	PodDensityModeExclusiveENI     = "exclusive-eni"
)

// Options for running Karpenter on Alibaba Cloud
type Options struct {
	// ClusterName is the name of the Kubernetes cluster
	ClusterName string

	// ClusterID is the ACK cluster ID (e.g., c9d5c9e900adc4a7481624c18a4067b84)
	ClusterID string

	// ClusterEndpoint is the endpoint of the Kubernetes API server
	ClusterEndpoint string

	// Region is the Alibaba Cloud region
	Region string

	// AccessKeyID is the Alibaba Cloud Access Key ID
	AccessKeyID string

	// AccessKeySecret is the Alibaba Cloud Access Key Secret
	AccessKeySecret string

	// RRSA (RAM Roles for Service Accounts) configuration
	// RoleARN is the RAM role ARN for RRSA authentication
	RoleARN string

	// OIDCProviderARN is the OIDC provider ARN for RRSA authentication
	OIDCProviderARN string

	// OIDCTokenFile is the path to the OIDC token file for RRSA authentication
	OIDCTokenFile string

	// InterruptionQueue is the SLS queue name for spot interruption events (optional)
	InterruptionQueue string

	// AssumeRoleARN is the RAM role ARN to assume (optional)
	AssumeRoleARN string

	// VMMemoryOverhead is the memory overhead for VM (default: 100Mi)
	VMMemoryOverhead string

	// VMMemoryOverheadPercent is the VM memory overhead as a percent (default: 0.075)
	VMMemoryOverheadPercent float64

	PricingRefreshEnabled                     bool
	PricingRefreshInterval                    time.Duration
	PricingRefreshConcurrency                 int
	PricingStaticDefaultRegionFallbackEnabled bool
	UnavailableOfferingCacheTTL               time.Duration
	MaxCreateCandidateAttempts                int
	PodDensityMode                            string
	TerwayReservedENIs                        int
	TerwayReservedIPsPerENI                   int
	TerwayMaxPodsCap                          int

	// FeatureGates is the raw CLI input. GatesParsed contains the parsed gate state after Parse.
	FeatureGates string
	GatesParsed  *FeatureGates
}

// New creates a new Options instance with default values
func New() *Options {
	return &Options{
		Region:                                    "cn-hangzhou",
		VMMemoryOverhead:                          "50Mi", // 降低默认内存开销
		VMMemoryOverheadPercent:                   0.05,   // 降低到5%以避免过度预留
		PricingRefreshEnabled:                     true,
		PricingRefreshInterval:                    12 * time.Hour,
		PricingRefreshConcurrency:                 8,
		PricingStaticDefaultRegionFallbackEnabled: false,
		UnavailableOfferingCacheTTL:               5 * time.Minute,
		MaxCreateCandidateAttempts:                20,
		PodDensityMode:                            PodDensityModeKubelet,
		TerwayReservedENIs:                        1,
		TerwayReservedIPsPerENI:                   1,
		TerwayMaxPodsCap:                          0,
		FeatureGates:                              "",
		GatesParsed:                               DefaultFeatureGates(),
	}
}

// AddFlags adds flags to the FlagSet
func (o *Options) AddFlags(fs *coreoptions.FlagSet) {
	if value := os.Getenv("ALIBABA_CLOUD_CLUSTER_NAME"); value != "" {
		o.ClusterName = value
	}
	if value := os.Getenv("CLUSTER_ID"); value != "" {
		o.ClusterID = value
	}
	if value := os.Getenv("ALIBABA_CLOUD_CLUSTER_ENDPOINT"); value != "" {
		o.ClusterEndpoint = value
	}
	if value := os.Getenv("ALIBABA_CLOUD_REGION"); value != "" {
		o.Region = value
	}

	fs.StringVar(&o.ClusterName, "cluster-name", o.ClusterName, "The name of the Kubernetes cluster")
	fs.StringVar(&o.ClusterID, "cluster-id", o.ClusterID, "The ACK cluster ID (e.g., c9d5c9e900adc4a7481624c18a4067b84)")
	fs.StringVar(&o.ClusterEndpoint, "cluster-endpoint", o.ClusterEndpoint, "The endpoint of the Kubernetes API server")
	fs.StringVar(&o.Region, "region", o.Region, "The Alibaba Cloud region")
	fs.StringVar(&o.InterruptionQueue, "interruption-queue", o.InterruptionQueue, "The SLS queue name for spot interruption events")
	fs.StringVar(&o.AssumeRoleARN, "assume-role-arn", o.AssumeRoleARN, "The RAM role ARN to assume")
	fs.StringVar(&o.VMMemoryOverhead, "vm-memory-overhead", o.VMMemoryOverhead, "The memory overhead for VM (e.g., 100Mi)")
	fs.Float64Var(&o.VMMemoryOverheadPercent, "vm-memory-overhead-percent", o.VMMemoryOverheadPercent, "The VM memory overhead as a percent (default: 0.075)")
	fs.BoolVar(&o.PricingRefreshEnabled, "pricing-refresh-enabled", o.PricingRefreshEnabled, "Enable background ECS pricing refresh")
	fs.DurationVar(&o.PricingRefreshInterval, "pricing-refresh-interval", o.PricingRefreshInterval, "Pricing refresh interval")
	fs.IntVar(&o.PricingRefreshConcurrency, "pricing-refresh-concurrency", o.PricingRefreshConcurrency, "Max concurrent pricing calls")
	fs.BoolVar(&o.PricingStaticDefaultRegionFallbackEnabled, "pricing-static-default-region-fallback-enabled", o.PricingStaticDefaultRegionFallbackEnabled, "Allow static price fallback to cn-hangzhou")
	fs.DurationVar(&o.UnavailableOfferingCacheTTL, "unavailable-offering-cache-ttl", o.UnavailableOfferingCacheTTL, "TTL for capacity failure cache")
	fs.IntVar(&o.MaxCreateCandidateAttempts, "max-create-candidate-attempts", o.MaxCreateCandidateAttempts, "Bound create fallback attempts")
	fs.StringVar(&o.PodDensityMode, "pod-density-mode", o.PodDensityMode, "Pod density mode: kubelet, terway-eni, terway-eni-multi-ip, exclusive-eni")
	fs.IntVar(&o.TerwayReservedENIs, "terway-reserved-enis", o.TerwayReservedENIs, "Reserved ENIs for host/system networking")
	fs.IntVar(&o.TerwayReservedIPsPerENI, "terway-reserved-ips-per-eni", o.TerwayReservedIPsPerENI, "Reserved IPs per ENI")
	fs.IntVar(&o.TerwayMaxPodsCap, "terway-max-pods-cap", o.TerwayMaxPodsCap, "Optional upper bound for Terway calculated maxPods; 0 disables")
	if fs.Lookup("feature-gates") == nil {
		fs.StringVar(&o.FeatureGates, "feature-gates", o.FeatureGates, "Comma-separated provider feature gates")
	}
}

// RRSA environment variable names
const (
	EnvRoleARN         = "ALIBABA_CLOUD_ROLE_ARN"
	EnvOIDCProviderARN = "ALIBABA_CLOUD_OIDC_PROVIDER_ARN"
	EnvOIDCTokenFile   = "ALIBABA_CLOUD_OIDC_TOKEN_FILE"
)

// IsRRSAEnabled checks if RRSA is configured in options
func (o *Options) IsRRSAEnabled() bool {
	return o.RoleARN != "" && o.OIDCProviderARN != "" && o.OIDCTokenFile != ""
}

// Parse validates and populates options from environment variables
func (o *Options) Parse(fs *coreoptions.FlagSet, args ...string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		return fmt.Errorf("parsing flags, %w", err)
	}
	if featureGatesFlag := fs.Lookup("feature-gates"); featureGatesFlag != nil && wasFlagSet(fs, "feature-gates") {
		o.FeatureGates = featureGatesFlag.Value.String()
	}

	gates, err := ParseFeatureGates(o.FeatureGates)
	if err != nil {
		return fmt.Errorf("parsing feature gates, %w", err)
	}
	o.GatesParsed = gates

	// Required fields validation
	if o.ClusterName == "" {
		return fmt.Errorf("cluster-name is required")
	}
	if o.ClusterID == "" {
		return fmt.Errorf("cluster-id is required")
	}
	if o.ClusterEndpoint == "" {
		return fmt.Errorf("cluster-endpoint is required")
	}

	// Get AK/SK credentials from environment variables (optional if RRSA is enabled)
	o.AccessKeyID = os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_ID")
	o.AccessKeySecret = os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET")

	// Get RRSA credentials from environment variables
	o.RoleARN = os.Getenv(EnvRoleARN)
	o.OIDCProviderARN = os.Getenv(EnvOIDCProviderARN)
	o.OIDCTokenFile = os.Getenv(EnvOIDCTokenFile)

	// Check authentication: either RRSA or AK/SK must be configured
	hasAKSK := o.AccessKeyID != "" && o.AccessKeySecret != ""
	hasRRSA := o.IsRRSAEnabled()

	if !hasRRSA && !hasAKSK {
		return fmt.Errorf("authentication required: either configure RRSA (ALIBABA_CLOUD_ROLE_ARN, ALIBABA_CLOUD_OIDC_PROVIDER_ARN, ALIBABA_CLOUD_OIDC_TOKEN_FILE) or AK/SK (ALIBABA_CLOUD_ACCESS_KEY_ID, ALIBABA_CLOUD_ACCESS_KEY_SECRET)")
	}

	if err := o.validateProviderOptions(); err != nil {
		return err
	}

	return nil
}

func wasFlagSet(fs *coreoptions.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func (o *Options) validateProviderOptions() error {
	if o.PricingRefreshInterval < 5*time.Minute || o.PricingRefreshInterval > 168*time.Hour {
		return fmt.Errorf("pricing-refresh-interval must be between 5m and 168h")
	}
	if o.PricingRefreshConcurrency < 1 || o.PricingRefreshConcurrency > 64 {
		return fmt.Errorf("pricing-refresh-concurrency must be between 1 and 64")
	}
	if o.UnavailableOfferingCacheTTL < 30*time.Second || o.UnavailableOfferingCacheTTL > time.Hour {
		return fmt.Errorf("unavailable-offering-cache-ttl must be between 30s and 1h")
	}
	if o.MaxCreateCandidateAttempts < 1 || o.MaxCreateCandidateAttempts > 100 {
		return fmt.Errorf("max-create-candidate-attempts must be between 1 and 100")
	}
	switch o.PodDensityMode {
	case PodDensityModeKubelet, PodDensityModeTerwayENI, PodDensityModeTerwayENIMultiIP, PodDensityModeExclusiveENI:
	default:
		return fmt.Errorf("pod-density-mode must be one of kubelet, terway-eni, terway-eni-multi-ip, exclusive-eni")
	}
	if o.TerwayReservedENIs < 0 {
		return fmt.Errorf("terway-reserved-enis must be >= 0")
	}
	if o.TerwayReservedIPsPerENI < 0 {
		return fmt.Errorf("terway-reserved-ips-per-eni must be >= 0")
	}
	if o.TerwayMaxPodsCap < 0 {
		return fmt.Errorf("terway-max-pods-cap must be >= 0")
	}

	defaults := New()
	terwayValuesChanged := o.TerwayReservedENIs != defaults.TerwayReservedENIs ||
		o.TerwayReservedIPsPerENI != defaults.TerwayReservedIPsPerENI ||
		o.TerwayMaxPodsCap != defaults.TerwayMaxPodsCap
	if o.PodDensityMode != PodDensityModeKubelet && !o.GatesParsed.Enabled(FeatureGateTerwayPodDensity) {
		return fmt.Errorf("non-kubelet pod-density-mode requires %s=true", FeatureGateTerwayPodDensity)
	}
	if terwayValuesChanged && !o.GatesParsed.Enabled(FeatureGateTerwayPodDensity) {
		return fmt.Errorf("non-default Terway pod density options require %s=true", FeatureGateTerwayPodDensity)
	}
	if terwayValuesChanged && o.PodDensityMode == PodDensityModeKubelet {
		return fmt.Errorf("non-default Terway pod density options require pod-density-mode other than kubelet")
	}
	if o.InterruptionQueue != "" && !o.GatesParsed.Enabled(FeatureGateInterruptionHandling) {
		return fmt.Errorf("interruption-queue requires %s=true", FeatureGateInterruptionHandling)
	}
	return nil
}

// ToContext returns a context with the options
func (o *Options) ToContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, optionsKey{}, o)
}

// FromContext returns the options from the context
func FromContext(ctx context.Context) *Options {
	retval := ctx.Value(optionsKey{})
	if retval == nil {
		return New()
	}
	return retval.(*Options)
}
