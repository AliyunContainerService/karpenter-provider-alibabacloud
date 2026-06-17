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

package cloudprovider

import (
	"testing"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	coreapis "sigs.k8s.io/karpenter/pkg/apis/v1"
)

func TestZonesFromRequirements(t *testing.T) {
	tests := []struct {
		name     string
		reqs     []coreapis.NodeSelectorRequirementWithMinValues
		expected []string
	}{
		{
			name:     "no requirements returns nil",
			reqs:     nil,
			expected: nil,
		},
		{
			name: "zone In requirement returns zone values",
			reqs: []coreapis.NodeSelectorRequirementWithMinValues{
				{NodeSelectorRequirement: corev1.NodeSelectorRequirement{
					Key:      corev1.LabelTopologyZone,
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{"cn-shanghai-n"},
				}},
			},
			expected: []string{"cn-shanghai-n"},
		},
		{
			name: "multiple zones in requirement",
			reqs: []coreapis.NodeSelectorRequirementWithMinValues{
				{NodeSelectorRequirement: corev1.NodeSelectorRequirement{
					Key:      corev1.LabelTopologyZone,
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{"cn-shanghai-l", "cn-shanghai-n"},
				}},
			},
			expected: []string{"cn-shanghai-l", "cn-shanghai-n"},
		},
		{
			name: "non-zone requirement returns nil",
			reqs: []coreapis.NodeSelectorRequirementWithMinValues{
				{NodeSelectorRequirement: corev1.NodeSelectorRequirement{
					Key:      "node.kubernetes.io/instance-type",
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{"ecs.g7.xlarge"},
				}},
			},
			expected: nil,
		},
		{
			name: "NotIn zone operator is ignored, returns nil",
			reqs: []coreapis.NodeSelectorRequirementWithMinValues{
				{NodeSelectorRequirement: corev1.NodeSelectorRequirement{
					Key:      corev1.LabelTopologyZone,
					Operator: corev1.NodeSelectorOpNotIn,
					Values:   []string{"cn-shanghai-l"},
				}},
			},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := zonesFromRequirements(tt.reqs)
			if len(got) != len(tt.expected) {
				t.Fatalf("zonesFromRequirements() = %v, want %v", got, tt.expected)
			}
			for i := range tt.expected {
				if got[i] != tt.expected[i] {
					t.Errorf("zonesFromRequirements()[%d] = %q, want %q", i, got[i], tt.expected[i])
				}
			}
		})
	}
}

func TestFilterVSwitchesByZones(t *testing.T) {
	vsw := []v1alpha1.VSwitch{
		{ID: "vsw-l", Zone: "cn-shanghai-l", ZoneID: "cn-shanghai-l"},
		{ID: "vsw-n", Zone: "cn-shanghai-n", ZoneID: "cn-shanghai-n"},
		{ID: "vsw-m", Zone: "cn-shanghai-m", ZoneID: "cn-shanghai-m"},
	}

	tests := []struct {
		name         string
		vswitches    []v1alpha1.VSwitch
		allowedZones []string
		wantIDs      []string
	}{
		{
			name:         "nil allowedZones returns all vswitches",
			vswitches:    vsw,
			allowedZones: nil,
			wantIDs:      []string{"vsw-l", "vsw-n", "vsw-m"},
		},
		{
			name:         "empty allowedZones returns all vswitches",
			vswitches:    vsw,
			allowedZones: []string{},
			wantIDs:      []string{"vsw-l", "vsw-n", "vsw-m"},
		},
		{
			name:         "filter to single zone returns only matching vswitch",
			vswitches:    vsw,
			allowedZones: []string{"cn-shanghai-n"},
			wantIDs:      []string{"vsw-n"},
		},
		{
			name:         "filter to multiple zones returns matching vswitches",
			vswitches:    vsw,
			allowedZones: []string{"cn-shanghai-l", "cn-shanghai-m"},
			wantIDs:      []string{"vsw-l", "vsw-m"},
		},
		{
			name:         "zone not in vswitches returns empty",
			vswitches:    vsw,
			allowedZones: []string{"cn-hangzhou-a"},
			wantIDs:      nil,
		},
		{
			name: "falls back to Zone field when ZoneID is empty",
			vswitches: []v1alpha1.VSwitch{
				{ID: "vsw-x", Zone: "cn-shanghai-n", ZoneID: ""},
			},
			allowedZones: []string{"cn-shanghai-n"},
			wantIDs:      []string{"vsw-x"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterVSwitchesByZones(tt.vswitches, tt.allowedZones)
			if len(got) != len(tt.wantIDs) {
				t.Fatalf("filterVSwitchesByZones() returned %d vswitches, want %d: got %v", len(got), len(tt.wantIDs), got)
			}
			for i, id := range tt.wantIDs {
				if got[i].ID != id {
					t.Errorf("filterVSwitchesByZones()[%d].ID = %q, want %q", i, got[i].ID, id)
				}
			}
		})
	}
}

// TestVSwitchZoneFilteringBug is the regression test for issue #6.
// Before the fix, createInstanceWithRetry always picked vswitches[0] regardless
// of the NodePool zone requirement, causing instances to land in the wrong zone.
func TestVSwitchZoneFilteringBug(t *testing.T) {
	// ECSNodeClass has vswitches in both cn-shanghai-l (first) and cn-shanghai-n.
	allVSwitches := []v1alpha1.VSwitch{
		{ID: "vsw-l", Zone: "cn-shanghai-l", ZoneID: "cn-shanghai-l"},
		{ID: "vsw-n", Zone: "cn-shanghai-n", ZoneID: "cn-shanghai-n"},
	}

	// NodePool restricts to cn-shanghai-n only.
	requirements := []coreapis.NodeSelectorRequirementWithMinValues{
		{NodeSelectorRequirement: corev1.NodeSelectorRequirement{
			Key:      corev1.LabelTopologyZone,
			Operator: corev1.NodeSelectorOpIn,
			Values:   []string{"cn-shanghai-n"},
		}},
	}

	zones := zonesFromRequirements(requirements)
	filtered := filterVSwitchesByZones(allVSwitches, zones)

	if len(filtered) != 1 {
		t.Fatalf("expected 1 vswitch after zone filtering, got %d: %v", len(filtered), filtered)
	}
	if filtered[0].ID != "vsw-n" {
		t.Errorf("expected vswitch vsw-n (zone cn-shanghai-n), got %q (zone %q)", filtered[0].ID, filtered[0].Zone)
	}
}
