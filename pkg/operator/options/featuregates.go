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
	"fmt"
	"strings"
)

const (
	FeatureGateLaunchTemplateID     = "LaunchTemplateID"
	FeatureGateMetadataOptions      = "MetadataOptions"
	FeatureGateCapacityReservation  = "CapacityReservation"
	FeatureGateDeploymentSet        = "DeploymentSet"
	FeatureGateInstanceStoreRAID0   = "InstanceStoreRAID0"
	FeatureGateInterruptionHandling = "InterruptionHandling"
	FeatureGateTerwayPodDensity     = "TerwayPodDensity"
	FeatureGateCreateFallback       = "CreateFallback"
	FeatureGatePricingRefresh       = "PricingRefresh"
)

// FeatureGates contains parsed provider feature gate state.
type FeatureGates struct {
	values map[string]bool
}

// Enabled returns true when the named feature gate is enabled.
func (f *FeatureGates) Enabled(name string) bool {
	if f == nil {
		return DefaultFeatureGates().Enabled(name)
	}
	return f.values[name]
}

// DefaultFeatureGates returns provider feature gates with their documented defaults.
func DefaultFeatureGates() *FeatureGates {
	return &FeatureGates{
		values: map[string]bool{
			FeatureGateLaunchTemplateID:     false,
			FeatureGateMetadataOptions:      false,
			FeatureGateCapacityReservation:  false,
			FeatureGateDeploymentSet:        false,
			FeatureGateInstanceStoreRAID0:   false,
			FeatureGateInterruptionHandling: false,
			FeatureGateTerwayPodDensity:     false,
			FeatureGateCreateFallback:       false,
			FeatureGatePricingRefresh:       true,
		},
	}
}

// ParseFeatureGates parses comma-separated Name=true|false provider feature gates.
func ParseFeatureGates(input string) (*FeatureGates, error) {
	gates := DefaultFeatureGates()
	input = strings.TrimSpace(input)
	if input == "" {
		return gates, nil
	}

	seen := map[string]struct{}{}
	for _, entry := range strings.Split(input, ",") {
		entry = strings.TrimSpace(entry)
		parts := strings.Split(entry, "=")
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
			return nil, fmt.Errorf("invalid feature gate %q, expected Name=true|false", entry)
		}
		name := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if _, ok := gates.values[name]; !ok {
			return nil, fmt.Errorf("unknown feature gate %q", name)
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("duplicate feature gate %q", name)
		}
		seen[name] = struct{}{}
		switch value {
		case "true":
			gates.values[name] = true
		case "false":
			gates.values[name] = false
		default:
			return nil, fmt.Errorf("invalid feature gate value %q for %q, expected true or false", value, name)
		}
	}
	return gates, nil
}
