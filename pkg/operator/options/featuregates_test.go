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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseFeatureGatesDefaults(t *testing.T) {
	gates, err := ParseFeatureGates("")
	require.NoError(t, err)

	require.False(t, gates.Enabled(FeatureGateLaunchTemplateID))
	require.False(t, gates.Enabled(FeatureGateMetadataOptions))
	require.False(t, gates.Enabled(FeatureGateCapacityReservation))
	require.False(t, gates.Enabled(FeatureGateDeploymentSet))
	require.False(t, gates.Enabled(FeatureGateInstanceStoreRAID0))
	require.False(t, gates.Enabled(FeatureGateInterruptionHandling))
	require.False(t, gates.Enabled(FeatureGateTerwayPodDensity))
	require.False(t, gates.Enabled(FeatureGateCreateFallback))
	require.True(t, gates.Enabled(FeatureGatePricingRefresh))
}

func TestParseFeatureGatesExplicitEnableDisable(t *testing.T) {
	gates, err := ParseFeatureGates("DeploymentSet=true,PricingRefresh=false")
	require.NoError(t, err)

	require.True(t, gates.Enabled(FeatureGateDeploymentSet))
	require.False(t, gates.Enabled(FeatureGatePricingRefresh))
	require.False(t, gates.Enabled(FeatureGateMetadataOptions))
}

func TestParseFeatureGatesRejectsUnknownGate(t *testing.T) {
	_, err := ParseFeatureGates("UnknownGate=true")
	require.ErrorContains(t, err, "unknown feature gate")
}

func TestParseFeatureGatesRejectsDuplicateGate(t *testing.T) {
	_, err := ParseFeatureGates("DeploymentSet=true,DeploymentSet=false")
	require.ErrorContains(t, err, "duplicate feature gate")
}

func TestParseFeatureGatesRejectsInvalidValue(t *testing.T) {
	_, err := ParseFeatureGates("DeploymentSet=yes")
	require.ErrorContains(t, err, "invalid feature gate value")
}
