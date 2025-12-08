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
	"fmt"
	"os"

	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
)

type optionsKey struct{}

// Options for running Karpenter on Alibaba Cloud
type Options struct {
	// ClusterName is the name of the Kubernetes cluster
	ClusterName string

	// ClusterEndpoint is the endpoint of the Kubernetes API server
	ClusterEndpoint string

	// Region is the Alibaba Cloud region
	Region string

	// AccessKeyID is the Alibaba Cloud Access Key ID
	AccessKeyID string

	// AccessKeySecret is the Alibaba Cloud Access Key Secret
	AccessKeySecret string

	// InterruptionQueue is the SLS queue name for spot interruption events (optional)
	InterruptionQueue string

	// AssumeRoleARN is the RAM role ARN to assume (optional)
	AssumeRoleARN string

	// VMMemoryOverhead is the memory overhead for VM (default: 100Mi)
	VMMemoryOverhead string

	// VMMemoryOverheadPercent is the VM memory overhead as a percent (default: 0.075)
	VMMemoryOverheadPercent float64
}

// New creates a new Options instance with default values
func New() *Options {
	return &Options{
		Region:                  "cn-hangzhou",
		VMMemoryOverhead:        "50Mi", // 降低默认内存开销
		VMMemoryOverheadPercent: 0.05,   // 降低到5%以避免过度预留
	}
}

// AddFlags adds flags to the FlagSet
func (o *Options) AddFlags(fs *coreoptions.FlagSet) {
	fs.StringVar(&o.ClusterName, "cluster-name", o.ClusterName, "The name of the Kubernetes cluster")
	fs.StringVar(&o.ClusterEndpoint, "cluster-endpoint", o.ClusterEndpoint, "The endpoint of the Kubernetes API server")
	fs.StringVar(&o.Region, "region", o.Region, "The Alibaba Cloud region")
	fs.StringVar(&o.InterruptionQueue, "interruption-queue", o.InterruptionQueue, "The SLS queue name for spot interruption events")
	fs.StringVar(&o.AssumeRoleARN, "assume-role-arn", o.AssumeRoleARN, "The RAM role ARN to assume")
	fs.StringVar(&o.VMMemoryOverhead, "vm-memory-overhead", o.VMMemoryOverhead, "The memory overhead for VM (e.g., 100Mi)")
	fs.Float64Var(&o.VMMemoryOverheadPercent, "vm-memory-overhead-percent", o.VMMemoryOverheadPercent, "The VM memory overhead as a percent (default: 0.075)")
}

// Parse validates and populates options from environment variables
func (o *Options) Parse(fs *coreoptions.FlagSet, args ...string) error {
	// Required fields validation
	if o.ClusterName == "" {
		return fmt.Errorf("cluster-name is required")
	}
	if o.ClusterEndpoint == "" {
		return fmt.Errorf("cluster-endpoint is required")
	}

	// Get credentials from environment variables
	o.AccessKeyID = os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_ID")
	if o.AccessKeyID == "" {
		return fmt.Errorf("ALIBABA_CLOUD_ACCESS_KEY_ID environment variable is required")
	}

	o.AccessKeySecret = os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET")
	if o.AccessKeySecret == "" {
		return fmt.Errorf("ALIBABA_CLOUD_ACCESS_KEY_SECRET environment variable is required")
	}

	// Optional region override from environment
	if region := os.Getenv("ALIBABA_CLOUD_REGION"); region != "" {
		o.Region = region
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
