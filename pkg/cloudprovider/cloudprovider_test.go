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
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instance"
	"github.com/stretchr/testify/assert"
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

// TestVSwitchFallbackOnNoStock verifies that vswitchFallbackCreate falls back to the next vswitch
// when the first one returns a NoStock capacity error (issue #9).
// TestVSwitchFallbackSortsByIPCount verifies that vswitches are tried in descending order of
// AvailableIPAddressCount, so the one with the most IPs is attempted first.
func TestVSwitchFallbackSortsByIPCount(t *testing.T) {
	vswitches := []v1alpha1.VSwitch{
		{ID: "vsw-low", Zone: "cn-shanghai-a", AvailableIPAddressCount: 5},
		{ID: "vsw-high", Zone: "cn-shanghai-b", AvailableIPAddressCount: 50},
		{ID: "vsw-mid", Zone: "cn-shanghai-c", AvailableIPAddressCount: 20},
	}

	// Make the two highest-IP vswitches fail with capacity errors so the loop
	// visits all three in order, letting us verify the sort.
	callOrder := []string{}
	createFn := func(_ context.Context, opts instance.CreateOptions) (string, error) {
		callOrder = append(callOrder, opts.VSwitchID)
		if opts.VSwitchID == "vsw-high" || opts.VSwitchID == "vsw-mid" {
			return "", fmt.Errorf("OperationDenied.NoStock: no stock in zone")
		}
		return "i-success", nil
	}

	if _, err := vswitchFallbackCreate(context.Background(), instance.CreateOptions{}, vswitches, createFn); err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	want := []string{"vsw-high", "vsw-mid", "vsw-low"}
	if len(callOrder) != len(want) {
		t.Fatalf("expected %d calls, got %d: %v", len(want), len(callOrder), callOrder)
	}
	for i, id := range want {
		if callOrder[i] != id {
			t.Errorf("call[%d]: want %q, got %q", i, id, callOrder[i])
		}
	}
}

// TestVSwitchFallbackDoesNotMutateInputSlice verifies that the original vswitches slice is not
// reordered by vswitchFallbackCreate (important when the slice is backed by a cache).
func TestVSwitchFallbackDoesNotMutateInputSlice(t *testing.T) {
	vswitches := []v1alpha1.VSwitch{
		{ID: "vsw-low", Zone: "cn-shanghai-a", AvailableIPAddressCount: 5},
		{ID: "vsw-high", Zone: "cn-shanghai-b", AvailableIPAddressCount: 50},
		{ID: "vsw-mid", Zone: "cn-shanghai-c", AvailableIPAddressCount: 20},
	}
	originalOrder := []string{vswitches[0].ID, vswitches[1].ID, vswitches[2].ID}

	createFn := func(_ context.Context, opts instance.CreateOptions) (string, error) {
		return "i-success", nil
	}

	if _, err := vswitchFallbackCreate(context.Background(), instance.CreateOptions{}, vswitches, createFn); err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	for i, id := range originalOrder {
		if vswitches[i].ID != id {
			t.Errorf("input slice mutated at index %d: want %q, got %q", i, id, vswitches[i].ID)
		}
	}
}

func TestVSwitchFallbackOnNoStock(t *testing.T) {
	vswitches := []v1alpha1.VSwitch{
		{ID: "vsw-l", Zone: "cn-shanghai-l"},
		{ID: "vsw-n", Zone: "cn-shanghai-n"},
	}

	callOrder := []string{}
	createFn := func(_ context.Context, opts instance.CreateOptions) (string, error) {
		callOrder = append(callOrder, opts.VSwitchID)
		if opts.VSwitchID == "vsw-l" {
			return "", fmt.Errorf("OperationDenied.NoStock: no available instance in zone")
		}
		return "i-success", nil
	}

	id, err := vswitchFallbackCreate(context.Background(), instance.CreateOptions{}, vswitches, createFn)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if id != "i-success" {
		t.Errorf("expected instanceID i-success, got %q", id)
	}
	if len(callOrder) != 2 || callOrder[0] != "vsw-l" || callOrder[1] != "vsw-n" {
		t.Errorf("unexpected call order: %v", callOrder)
	}
}

// TestVSwitchFallbackFailFastOnQuotaError verifies that a non-retryable error causes an immediate
// failure without trying additional vswitches.
func TestVSwitchFallbackFailFastOnQuotaError(t *testing.T) {
	vswitches := []v1alpha1.VSwitch{
		{ID: "vsw-l", Zone: "cn-shanghai-l"},
		{ID: "vsw-n", Zone: "cn-shanghai-n"},
	}

	calls := 0
	createFn := func(_ context.Context, opts instance.CreateOptions) (string, error) {
		calls++
		return "", fmt.Errorf("QuotaExceed.Instance: quota exceeded")
	}

	_, err := vswitchFallbackCreate(context.Background(), instance.CreateOptions{}, vswitches, createFn)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if calls != 1 {
		t.Errorf("expected exactly 1 create call on quota error, got %d", calls)
	}
}

// TestVSwitchFallbackAllExhausted verifies that when all vswitches report IP exhaustion, the
// function returns a descriptive error including the last failure.
func TestVSwitchFallbackAllExhausted(t *testing.T) {
	vswitches := []v1alpha1.VSwitch{
		{ID: "vsw-l", Zone: "cn-shanghai-l"},
		{ID: "vsw-n", Zone: "cn-shanghai-n"},
	}

	createFn := func(_ context.Context, opts instance.CreateOptions) (string, error) {
		return "", fmt.Errorf("InvalidVSwitchId.IpNotEnough: vswitch %s has no available IPs", opts.VSwitchID)
	}

	_, err := vswitchFallbackCreate(context.Background(), instance.CreateOptions{}, vswitches, createFn)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "all vSwitches exhausted") {
		t.Errorf("expected 'all vSwitches exhausted' in error, got: %v", err)
	}
}

func TestEcsArchToKubernetesArch(t *testing.T) {
	tests := []struct {
		ecsArch  string
		expected string
	}{
		{"X86", "amd64"},
		{"x86", "amd64"},
		{"ARM", "arm64"},
		{"arm", "arm64"},
		{"Arm", "arm64"},
		{"", "amd64"},
		{"unknown", "amd64"},
	}
	for _, tt := range tests {
		t.Run(tt.ecsArch, func(t *testing.T) {
			assert.Equal(t, tt.expected, ecsArchToKubernetesArch(tt.ecsArch))
		})
	}
}

func TestBuildInstanceTagsThreeLayerMerge(t *testing.T) {
	nc := &coreapis.NodeClaim{}
	nc.Name = "nodeclaim-abc"
	nc.Labels = map[string]string{
		coreapis.NodePoolLabelKey: "my-pool",
		// extra label that must NOT leak into ECS tags
		"kubernetes.io/arch": "amd64",
	}
	nodeClass := &v1alpha1.ECSNodeClass{}
	nodeClass.Spec.ClusterID = "c-abc"
	nodeClass.Spec.Tags = map[string]string{
		"custom-tag": "custom-value",
		// user tag can override management tag (layer 3 wins)
	}

	tags := buildInstanceTags(nc, nodeClass)

	// Layer 1: management tags always present
	assert.Equal(t, "karpenter", tags[v1alpha1.TagManagedBy])
	assert.Equal(t, "c-abc", tags[v1alpha1.TagClusterID])
	// Layer 2: traceability tags
	assert.Equal(t, "my-pool", tags[v1alpha1.TagNodePool])
	assert.Equal(t, "nodeclaim-abc", tags[v1alpha1.TagNodeClaim])
	// Layer 3: user tags
	assert.Equal(t, "custom-value", tags["custom-tag"])
	// NodeClaim labels must NOT be in ECS tags
	_, hasArch := tags["kubernetes.io/arch"]
	assert.False(t, hasArch, "NodeClaim label 'kubernetes.io/arch' must not leak into ECS tags")
}

func TestBuildInstanceTagsNilUserTags(t *testing.T) {
	nc := &coreapis.NodeClaim{}
	nodeClass := &v1alpha1.ECSNodeClass{}
	// nodeClass.Spec.Tags is nil — must not panic
	tags := buildInstanceTags(nc, nodeClass)
	assert.Equal(t, "karpenter", tags[v1alpha1.TagManagedBy])
}

func TestInstanceLabelsFromInstance(t *testing.T) {
	inst := &instance.Instance{
		Zone:         "cn-shanghai-n",
		InstanceType: "ecs.g7.xlarge",
		Architecture: "X86_64",
		CapacityType: "on-demand",
		Tags: map[string]string{
			v1alpha1.TagNodePool: "my-pool",
			// raw ECS tag with invalid K8s label value — must NOT surface
			"ecs.aliyuncs.com/owner": "user@company.com",
		},
	}

	labels := instanceLabelsFromInstance(inst)

	assert.Equal(t, "cn-shanghai-n", labels[corev1.LabelTopologyZone])
	assert.Equal(t, "ecs.g7.xlarge", labels[v1alpha1.LabelInstanceType])
	assert.Equal(t, "on-demand", labels[v1alpha1.LabelCapacityType])
	assert.Equal(t, "amd64", labels[corev1.LabelArchStable])
	assert.Equal(t, "linux", labels[corev1.LabelOSStable])
	assert.Equal(t, "ecs.g7.xlarge", labels[corev1.LabelInstanceTypeStable])
	assert.Equal(t, "my-pool", labels[coreapis.NodePoolLabelKey])
	// raw ECS tag with colon/@ must not appear
	_, hasOwner := labels["ecs.aliyuncs.com/owner"]
	assert.False(t, hasOwner)
}

func TestInstanceLabelsFromInstanceARM(t *testing.T) {
	inst := &instance.Instance{Architecture: "ARM64"}
	labels := instanceLabelsFromInstance(inst)
	assert.Equal(t, "arm64", labels[corev1.LabelArchStable])
}

func TestConvertInstanceToNodeClaimArchLabels(t *testing.T) {
	tests := []struct {
		ecsArch     string
		wantK8sArch string
	}{
		{"X86_64", "amd64"},
		{"ARM64", "arm64"},
		{"", "amd64"},
	}
	for _, tt := range tests {
		t.Run(tt.ecsArch, func(t *testing.T) {
			cp := &CloudProvider{}
			inst := &instance.Instance{
				InstanceID:   "i-test",
				Region:       "cn-shanghai",
				Zone:         "cn-shanghai-n",
				InstanceType: "ecs.g7.xlarge",
				Architecture: tt.ecsArch,
				CapacityType: "on-demand",
				Tags:         map[string]string{},
			}
			nc := cp.convertInstanceToNodeClaim(context.Background(), inst, &coreapis.NodeClaim{}, nil, "c-test")
			assert.Equal(t, tt.wantK8sArch, nc.Labels[corev1.LabelArchStable], "LabelArchStable")
			assert.Equal(t, "linux", nc.Labels[corev1.LabelOSStable], "LabelOSStable")
			assert.Equal(t, "ecs.g7.xlarge", nc.Labels[corev1.LabelInstanceTypeStable], "LabelInstanceTypeStable")
		})
	}
}

func TestBuildInstanceTagsManagedByValue(t *testing.T) {
	tags := buildInstanceTags(&coreapis.NodeClaim{}, &v1alpha1.ECSNodeClass{})
	assert.Equal(t, "karpenter", tags[v1alpha1.TagManagedBy],
		"TagManagedBy must be 'karpenter' so that List() tag filter matches")
}

func TestBuildInstanceTagsIncludesClusterID(t *testing.T) {
	nc := &coreapis.NodeClaim{}
	nodeClass := &v1alpha1.ECSNodeClass{}
	nodeClass.Spec.ClusterID = "c-abc123"

	tags := buildInstanceTags(nc, nodeClass)

	assert.Equal(t, "c-abc123", tags[v1alpha1.TagClusterID])
	assert.Equal(t, "karpenter", tags[v1alpha1.TagManagedBy])
}
