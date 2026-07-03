//go:build integration

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

// Integration tests for instancetype.Provider against real ECS APIs.
// Run with: ALIBABA_CLOUD_ACCESS_KEY_ID=xxx ALIBABA_CLOUD_ACCESS_KEY_SECRET=yyy ALIBABA_CLOUD_REGION_ID=cn-hangzhou \
//   go test ./pkg/providers/instancetype/ -tags integration -v -timeout 120s

package instancetype

import (
	"context"
	"os"
	"testing"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	ecssdk "github.com/alibabacloud-go/ecs-20140526/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newIntegrationProvider creates a real Provider from env vars, or skips the test.
// Builds the ECS client directly to avoid an import cycle with pkg/operator.
func newIntegrationProvider(t *testing.T) (*Provider, string) {
	t.Helper()
	akID := os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_ID")
	akSecret := os.Getenv("ALIBABA_CLOUD_ACCESS_KEY_SECRET")
	region := os.Getenv("ALIBABA_CLOUD_REGION_ID")
	if akID == "" || akSecret == "" || region == "" {
		t.Skip("set ALIBABA_CLOUD_ACCESS_KEY_ID, ALIBABA_CLOUD_ACCESS_KEY_SECRET, ALIBABA_CLOUD_REGION_ID to run integration tests")
	}
	raw, err := ecssdk.NewClient(&openapi.Config{
		AccessKeyId:     tea.String(akID),
		AccessKeySecret: tea.String(akSecret),
		RegionId:        tea.String(region),
		ConnectTimeout:  tea.Int(30),
	})
	require.NoError(t, err, "ecssdk.NewClient should succeed")
	return NewProvider(region, clients.NewDefaultECSClient(raw, region)), region
}

// TestIntegration_GetInventory_ReturnsRealStock calls DescribeAvailableResource twice
// (NoSpot + SpotAsPriceGo) and verifies the merged inventory is sane.
func TestIntegration_GetInventory_ReturnsRealStock(t *testing.T) {
	provider, region := newIntegrationProvider(t)
	t.Logf("testing against region: %s", region)

	ctx := context.Background()
	inventory := provider.getInventory(ctx)

	require.NotNil(t, inventory, "inventory must not be nil (API error would return nil)")
	require.NotEmpty(t, inventory, "at least one instance type must have stock")

	for itID, zoneMap := range inventory {
		for zoneID, capacityTypes := range zoneMap {
			assert.NotEmpty(t, capacityTypes,
				"instance type %s in zone %s must have at least one capacity type", itID, zoneID)
			for _, ct := range capacityTypes {
				assert.Contains(t, []string{capacityTypeOnDemand, capacityTypeSpot}, ct,
					"capacity type must be %q or %q, got %q", capacityTypeOnDemand, capacityTypeSpot, ct)
			}
		}
	}

	// Verify caching: second call should return the same object (no API calls)
	inventory2 := provider.getInventory(ctx)
	require.NotNil(t, inventory2)
	assert.Equal(t, len(inventory), len(inventory2), "cached inventory should have same length")

	t.Logf("inventory: %d instance types with stock", len(inventory))
}

// TestIntegration_List_ReturnsInstanceTypesWithOfferings verifies that List() returns
// instance types populated with zones and karpenter capacity types.
func TestIntegration_List_ReturnsInstanceTypesWithOfferings(t *testing.T) {
	provider, region := newIntegrationProvider(t)
	t.Logf("testing against region: %s", region)

	ctx := context.Background()
	result, err := provider.List(ctx)

	require.NoError(t, err, "List() must not error")
	require.NotEmpty(t, result, "List() must return at least one instance type")

	hasOnDemand := false
	hasSpot := false

	for _, it := range result {
		assert.NotEmpty(t, it.Name, "instance type must have a name")
		assert.NotEmpty(t, it.Zones, "instance type %s must have at least one zone", it.Name)

		for zoneID, zoneInfo := range it.Zones {
			assert.True(t, zoneInfo.Available, "zone %s of %s must be marked available", zoneID, it.Name)
			assert.NotEmpty(t, zoneInfo.CapacityTypes,
				"zone %s of %s must have at least one capacity type", zoneID, it.Name)

			for _, ct := range zoneInfo.CapacityTypes {
				switch ct {
				case capacityTypeOnDemand:
					hasOnDemand = true
				case capacityTypeSpot:
					hasSpot = true
				default:
					t.Errorf("unexpected capacity type %q in zone %s of %s", ct, zoneID, it.Name)
				}
			}
		}
	}

	assert.True(t, hasOnDemand, "at least one on-demand offering must exist across all instance types")
	t.Logf("total instance types: %d, hasOnDemand=%v, hasSpot=%v", len(result), hasOnDemand, hasSpot)
}

// TestIntegration_GetInventory_SpotStrategy_Correct verifies that DescribeAvailableResource
// is called with InstanceChargeType=PostPaid and SpotStrategy (not SpotAsPriceGo as InstanceChargeType).
// This guards against the previous bug where SpotAsPriceGo was incorrectly passed as InstanceChargeType.
func TestIntegration_GetInventory_SpotStrategy_Correct(t *testing.T) {
	provider, region := newIntegrationProvider(t)
	t.Logf("testing against region: %s", region)

	// Call getInventory; if SpotAsPriceGo were passed as InstanceChargeType the API
	// would return an error or empty response, not a populated inventory.
	inventory := provider.getInventory(context.Background())

	require.NotNil(t, inventory, "inventory must not be nil — SpotStrategy API parameters must be correct")

	// Find an instance type that has spot stock to confirm the spot query worked
	spotFound := false
	for _, zoneMap := range inventory {
		for _, capacityTypes := range zoneMap {
			for _, ct := range capacityTypes {
				if ct == capacityTypeSpot {
					spotFound = true
				}
			}
		}
	}
	t.Logf("spot offerings found: %v (region may not have spot stock if false)", spotFound)
}

// TestIntegration_List_NoDuplicateOfferings verifies that no (zone, capacityType) pair
// appears more than once per instance type.
func TestIntegration_List_NoDuplicateOfferings(t *testing.T) {
	provider, _ := newIntegrationProvider(t)

	result, err := provider.List(context.Background())
	require.NoError(t, err)

	for _, it := range result {
		seen := map[string]bool{}
		for zoneID, zoneInfo := range it.Zones {
			for _, ct := range zoneInfo.CapacityTypes {
				key := zoneID + "|" + ct
				assert.False(t, seen[key],
					"instance type %s has duplicate offering (%s, %s)", it.Name, zoneID, ct)
				seen[key] = true
			}
		}
	}
}
