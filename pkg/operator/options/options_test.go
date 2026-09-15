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
	"flag"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	coreoptions "sigs.k8s.io/karpenter/pkg/operator/options"
)

func testFlagSet() *coreoptions.FlagSet {
	return &coreoptions.FlagSet{FlagSet: flag.NewFlagSet("karpenter-provider-alibabacloud", flag.ContinueOnError)}
}

func parseOptions(t *testing.T, args ...string) (*Options, error) {
	t.Helper()
	t.Setenv("ALIBABA_CLOUD_ACCESS_KEY_ID", "access-key-id")
	t.Setenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET", "access-key-secret")
	opts := New()
	fs := testFlagSet()
	opts.AddFlags(fs)
	args = append([]string{"--cluster-id=test-cluster-id"}, args...)
	err := opts.Parse(fs, args...)
	return opts, err
}

func TestOptionsDefaults(t *testing.T) {
	opts, err := parseOptions(t, "--cluster-name=test-cluster", "--cluster-endpoint=https://example.com")
	require.NoError(t, err)

	require.Equal(t, "test-cluster", opts.ClusterName)
	require.Equal(t, "https://example.com", opts.ClusterEndpoint)
	require.Equal(t, "cn-hangzhou", opts.Region)
	require.Equal(t, "", opts.InterruptionQueue)
	require.Equal(t, "50Mi", opts.VMMemoryOverhead)
	require.Equal(t, 0.05, opts.VMMemoryOverheadPercent)
	require.True(t, opts.PricingRefreshEnabled)
	require.Equal(t, 12*time.Hour, opts.PricingRefreshInterval)
	require.Equal(t, 8, opts.PricingRefreshConcurrency)
	require.False(t, opts.PricingStaticDefaultRegionFallbackEnabled)
	require.Equal(t, 5*time.Minute, opts.UnavailableOfferingCacheTTL)
	require.Equal(t, 20, opts.MaxCreateCandidateAttempts)
	require.Equal(t, PodDensityModeKubelet, opts.PodDensityMode)
	require.Equal(t, 1, opts.TerwayReservedENIs)
	require.Equal(t, 1, opts.TerwayReservedIPsPerENI)
	require.Equal(t, 0, opts.TerwayMaxPodsCap)
	require.Equal(t, "", opts.FeatureGates)
	require.NotNil(t, opts.GatesParsed)
	require.True(t, opts.GatesParsed.Enabled(FeatureGatePricingRefresh))
}

func TestOptionsParseValidFlags(t *testing.T) {
	opts, err := parseOptions(t,
		"--cluster-name=test-cluster",
		"--cluster-endpoint=https://example.com",
		"--region=cn-beijing",
		"--pricing-refresh-enabled=false",
		"--pricing-refresh-interval=6h",
		"--pricing-refresh-concurrency=16",
		"--pricing-static-default-region-fallback-enabled=true",
		"--unavailable-offering-cache-ttl=10m",
		"--max-create-candidate-attempts=40",
		"--pod-density-mode=terway-eni",
		"--terway-reserved-enis=2",
		"--terway-reserved-ips-per-eni=3",
		"--terway-max-pods-cap=110",
		"--feature-gates=TerwayPodDensity=true,PricingRefresh=false",
	)
	require.NoError(t, err)

	require.Equal(t, "cn-beijing", opts.Region)
	require.False(t, opts.PricingRefreshEnabled)
	require.Equal(t, 6*time.Hour, opts.PricingRefreshInterval)
	require.Equal(t, 16, opts.PricingRefreshConcurrency)
	require.True(t, opts.PricingStaticDefaultRegionFallbackEnabled)
	require.Equal(t, 10*time.Minute, opts.UnavailableOfferingCacheTTL)
	require.Equal(t, 40, opts.MaxCreateCandidateAttempts)
	require.Equal(t, PodDensityModeTerwayENI, opts.PodDensityMode)
	require.Equal(t, 2, opts.TerwayReservedENIs)
	require.Equal(t, 3, opts.TerwayReservedIPsPerENI)
	require.Equal(t, 110, opts.TerwayMaxPodsCap)
	require.True(t, opts.GatesParsed.Enabled(FeatureGateTerwayPodDensity))
	require.False(t, opts.GatesParsed.Enabled(FeatureGatePricingRefresh))
}

func TestOptionsFeatureGatesReusesCoreFlagWhenPresent(t *testing.T) {
	t.Setenv("ALIBABA_CLOUD_ACCESS_KEY_ID", "access-key-id")
	t.Setenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET", "access-key-secret")

	coreOpts := &coreoptions.Options{}
	opts := New()
	fs := testFlagSet()
	coreOpts.AddFlags(fs)

	require.NotPanics(t, func() {
		opts.AddFlags(fs)
	})
	err := opts.Parse(fs,
		"--cluster-name=test-cluster",
		"--cluster-id=test-cluster-id",
		"--cluster-endpoint=https://example.com",
		"--feature-gates=DeploymentSet=true",
	)
	require.NoError(t, err)
	require.Equal(t, "DeploymentSet=true", opts.FeatureGates)
	require.True(t, opts.GatesParsed.Enabled(FeatureGateDeploymentSet))
}

func TestOptionsEnvironmentOverridesOnlyDeploymentFields(t *testing.T) {
	t.Setenv("ALIBABA_CLOUD_ACCESS_KEY_ID", "access-key-id")
	t.Setenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET", "access-key-secret")
	t.Setenv("ALIBABA_CLOUD_REGION", "cn-shanghai")
	t.Setenv("ALIBABA_CLOUD_CLUSTER_NAME", "cluster-from-env")
	t.Setenv("CLUSTER_ID", "cluster-id-from-env")
	t.Setenv("ALIBABA_CLOUD_CLUSTER_ENDPOINT", "https://env.example.com")
	t.Setenv("FEATURE_GATES", "DeploymentSet=true")

	opts := New()
	fs := testFlagSet()
	opts.AddFlags(fs)
	err := opts.Parse(fs)
	require.NoError(t, err)
	require.Equal(t, "cluster-from-env", opts.ClusterName)
	require.Equal(t, "https://env.example.com", opts.ClusterEndpoint)
	require.Equal(t, "cn-shanghai", opts.Region)
	require.False(t, opts.GatesParsed.Enabled(FeatureGateDeploymentSet))

	opts = New()
	fs = testFlagSet()
	opts.AddFlags(fs)
	err = opts.Parse(fs,
		"--cluster-name=cluster-from-cli",
		"--cluster-id=test-cluster-id",
		"--cluster-endpoint=https://cli.example.com",
		"--region=cn-beijing",
	)
	require.NoError(t, err)
	require.Equal(t, "cluster-from-cli", opts.ClusterName)
	require.Equal(t, "https://cli.example.com", opts.ClusterEndpoint)
	require.Equal(t, "cn-beijing", opts.Region)
}

func TestOptionsValidationRejectsInvalidRanges(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "pricing interval too low", args: []string{"--pricing-refresh-interval=4m59s"}, want: "pricing-refresh-interval"},
		{name: "pricing interval too high", args: []string{"--pricing-refresh-interval=169h"}, want: "pricing-refresh-interval"},
		{name: "pricing concurrency too low", args: []string{"--pricing-refresh-concurrency=0"}, want: "pricing-refresh-concurrency"},
		{name: "pricing concurrency too high", args: []string{"--pricing-refresh-concurrency=65"}, want: "pricing-refresh-concurrency"},
		{name: "unavailable ttl too low", args: []string{"--unavailable-offering-cache-ttl=29s"}, want: "unavailable-offering-cache-ttl"},
		{name: "unavailable ttl too high", args: []string{"--unavailable-offering-cache-ttl=61m"}, want: "unavailable-offering-cache-ttl"},
		{name: "candidate attempts too low", args: []string{"--max-create-candidate-attempts=0"}, want: "max-create-candidate-attempts"},
		{name: "candidate attempts too high", args: []string{"--max-create-candidate-attempts=101"}, want: "max-create-candidate-attempts"},
		{name: "pod density mode unknown", args: []string{"--pod-density-mode=unknown"}, want: "pod-density-mode"},
		{name: "reserved enis negative", args: []string{"--terway-reserved-enis=-1"}, want: "terway-reserved-enis"},
		{name: "reserved ips negative", args: []string{"--terway-reserved-ips-per-eni=-1"}, want: "terway-reserved-ips-per-eni"},
		{name: "max pods cap negative", args: []string{"--terway-max-pods-cap=-1"}, want: "terway-max-pods-cap"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"--cluster-name=test-cluster", "--cluster-id=test-cluster-id", "--cluster-endpoint=https://example.com"}, tt.args...)
			_, err := parseOptions(t, args...)
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestOptionsTerwayValuesRequireGateAndNonKubeletMode(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "non default enis without gate",
			args: []string{"--pod-density-mode=terway-eni", "--terway-reserved-enis=2"},
			want: "TerwayPodDensity",
		},
		{
			name: "non default ips with gate and kubelet mode",
			args: []string{"--feature-gates=TerwayPodDensity=true", "--terway-reserved-ips-per-eni=2"},
			want: "pod-density-mode",
		},
		{
			name: "non default max pods cap without gate",
			args: []string{"--terway-max-pods-cap=1"},
			want: "TerwayPodDensity",
		},
		{
			name: "non kubelet mode without gate",
			args: []string{"--pod-density-mode=terway-eni"},
			want: "TerwayPodDensity",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"--cluster-name=test-cluster", "--cluster-id=test-cluster-id", "--cluster-endpoint=https://example.com"}, tt.args...)
			_, err := parseOptions(t, args...)
			require.ErrorContains(t, err, tt.want)
		})
	}

	_, err := parseOptions(t,
		"--cluster-name=test-cluster",
		"--cluster-endpoint=https://example.com",
		"--feature-gates=TerwayPodDensity=true",
		"--pod-density-mode=terway-eni-multi-ip",
		"--terway-reserved-enis=2",
		"--terway-reserved-ips-per-eni=2",
		"--terway-max-pods-cap=120",
	)
	require.NoError(t, err)
}

func TestOptionsInterruptionQueueRequiresGate(t *testing.T) {
	_, err := parseOptions(t,
		"--cluster-name=test-cluster",
		"--cluster-endpoint=https://example.com",
		"--interruption-queue=queue-name",
	)
	require.ErrorContains(t, err, FeatureGateInterruptionHandling)

	opts, err := parseOptions(t,
		"--cluster-name=test-cluster",
		"--cluster-endpoint=https://example.com",
		"--interruption-queue=queue-name",
		"--feature-gates=InterruptionHandling=true",
	)
	require.NoError(t, err)
	require.Equal(t, "queue-name", opts.InterruptionQueue)
	require.True(t, opts.GatesParsed.Enabled(FeatureGateInterruptionHandling))
}
