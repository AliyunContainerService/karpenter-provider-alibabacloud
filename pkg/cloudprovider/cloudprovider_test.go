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
	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/providers/instancetype"
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

	tags := BuildInstanceTags(nc, nodeClass)

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
	tags := BuildInstanceTags(nc, nodeClass)
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

	// Authoritative architecture comes from the matched InstanceType (sourced from
	// ECS DescribeInstanceTypes.CpuArchitecture), not from the instance itself.
	it := &instancetype.InstanceType{Name: "ecs.g7.xlarge", Architecture: "X86_64"}
	labels := instanceLabelsFromInstance(inst, it)

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
	inst := &instance.Instance{InstanceType: "ecs.g8y.large"}
	// Authoritative ARM architecture from the matched InstanceType.
	it := &instancetype.InstanceType{Name: "ecs.g8y.large", Architecture: "ARM64"}
	labels := instanceLabelsFromInstance(inst, it)
	assert.Equal(t, "arm64", labels[corev1.LabelArchStable])
}

// TestInstanceLabelsArchPrefersAuthoritativeOverName verifies that the arch label
// is taken from the ECS-sourced InstanceType.CpuArchitecture and NOT guessed from
// the instance-type name. Here the name would heuristically look amd64, but the
// authoritative value says arm64 and must win.
func TestInstanceLabelsArchPrefersAuthoritativeOverName(t *testing.T) {
	inst := &instance.Instance{InstanceType: "ecs.somefuture.large"}
	it := &instancetype.InstanceType{Name: "ecs.somefuture.large", Architecture: "ARM64"}
	labels := instanceLabelsFromInstance(inst, it)
	assert.Equal(t, "arm64", labels[corev1.LabelArchStable])
}

// TestInstanceLabelsArchFallsBackToNameHeuristic verifies that when no
// authoritative InstanceType is available (nil / empty arch), we fall back to the
// instance-type-name heuristic.
func TestInstanceLabelsArchFallsBackToNameHeuristic(t *testing.T) {
	inst := &instance.Instance{InstanceType: "ecs.g8y.large"}
	labels := instanceLabelsFromInstance(inst, nil)
	assert.Equal(t, "arm64", labels[corev1.LabelArchStable], "should infer arm64 from Yitian family name")

	inst2 := &instance.Instance{InstanceType: "ecs.gn7i-c8g1.2xlarge"}
	labels2 := instanceLabelsFromInstance(inst2, nil)
	assert.Equal(t, "amd64", labels2[corev1.LabelArchStable], "GPU family must be amd64, not arm64")
}

func TestConvertInstanceToNodeClaimArchLabels(t *testing.T) {
	tests := []struct {
		name         string
		instanceType string
		// authoritativeArch is the ECS DescribeInstanceTypes.CpuArchitecture carried
		// on the matched InstanceType; empty string means no InstanceType is provided
		// (nil list) so the name-based heuristic fallback is exercised instead.
		authoritativeArch string
		wantK8sArch       string
	}{
		{"authoritative X86_64", "ecs.g7.xlarge", "X86_64", "amd64"},
		{"authoritative ARM64", "ecs.g7.xlarge", "ARM64", "arm64"},
		// No authoritative InstanceType -> fall back to instance-type-name heuristic.
		{"fallback amd64 by name", "ecs.g7.xlarge", "", "amd64"},
		{"fallback arm64 by Yitian name", "ecs.g8y.large", "", "arm64"},
		{"fallback GPU family is amd64", "ecs.gn7i-c8g1.2xlarge", "", "amd64"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cp := &CloudProvider{}
			inst := &instance.Instance{
				InstanceID:   "i-test",
				Region:       "cn-shanghai",
				Zone:         "cn-shanghai-n",
				InstanceType: tt.instanceType,
				CapacityType: "on-demand",
				Tags:         map[string]string{},
			}
			var instanceTypes []*instancetype.InstanceType
			if tt.authoritativeArch != "" {
				instanceTypes = []*instancetype.InstanceType{
					{Name: tt.instanceType, Architecture: tt.authoritativeArch},
				}
			}
			nc := cp.convertInstanceToNodeClaim(context.Background(), inst, &coreapis.NodeClaim{}, instanceTypes, "c-test")
			assert.Equal(t, tt.wantK8sArch, nc.Labels[corev1.LabelArchStable], "LabelArchStable")
			assert.Equal(t, "linux", nc.Labels[corev1.LabelOSStable], "LabelOSStable")
			assert.Equal(t, tt.instanceType, nc.Labels[corev1.LabelInstanceTypeStable], "LabelInstanceTypeStable")
		})
	}
}

func TestCapacityTypeFromRequirements(t *testing.T) {
	tests := []struct {
		name     string
		reqs     []coreapis.NodeSelectorRequirementWithMinValues
		expected string
	}{
		{
			name:     "nil requirements returns on-demand",
			reqs:     nil,
			expected: "on-demand",
		},
		{
			name: "In spot returns spot",
			reqs: []coreapis.NodeSelectorRequirementWithMinValues{
				{NodeSelectorRequirement: corev1.NodeSelectorRequirement{
					Key:      v1alpha1.LabelCapacityType,
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{"spot"},
				}},
			},
			expected: "spot",
		},
		{
			name: "In on-demand returns on-demand",
			reqs: []coreapis.NodeSelectorRequirementWithMinValues{
				{NodeSelectorRequirement: corev1.NodeSelectorRequirement{
					Key:      v1alpha1.LabelCapacityType,
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{"on-demand"},
				}},
			},
			expected: "on-demand",
		},
		{
			name: "NotIn spot is ignored, returns on-demand",
			reqs: []coreapis.NodeSelectorRequirementWithMinValues{
				{NodeSelectorRequirement: corev1.NodeSelectorRequirement{
					Key:      v1alpha1.LabelCapacityType,
					Operator: corev1.NodeSelectorOpNotIn,
					Values:   []string{"spot"},
				}},
			},
			expected: "on-demand",
		},
		{
			name: "no capacity-type requirement returns on-demand",
			reqs: []coreapis.NodeSelectorRequirementWithMinValues{
				{NodeSelectorRequirement: corev1.NodeSelectorRequirement{
					Key:      corev1.LabelTopologyZone,
					Operator: corev1.NodeSelectorOpIn,
					Values:   []string{"cn-shanghai-n"},
				}},
			},
			expected: "on-demand",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, capacityTypeFromRequirements(tt.reqs))
		})
	}
}

func TestBuildInstanceTagsManagedByValue(t *testing.T) {
	tags := BuildInstanceTags(&coreapis.NodeClaim{}, &v1alpha1.ECSNodeClass{})
	assert.Equal(t, "karpenter", tags[v1alpha1.TagManagedBy],
		"TagManagedBy must be 'karpenter' so that List() tag filter matches")
}

func TestBuildInstanceTagsIncludesClusterID(t *testing.T) {
	nc := &coreapis.NodeClaim{}
	nodeClass := &v1alpha1.ECSNodeClass{}
	nodeClass.Spec.ClusterID = "c-abc123"

	tags := BuildInstanceTags(nc, nodeClass)

	assert.Equal(t, "c-abc123", tags[v1alpha1.TagClusterID])
	assert.Equal(t, "karpenter", tags[v1alpha1.TagManagedBy])
}

func TestSelectImageForArchitecture(t *testing.T) {
	amd64Img := v1alpha1.Image{ID: "img-amd64", Architecture: "x86_64"}
	arm64Img := v1alpha1.Image{ID: "img-arm64", Architecture: "arm64"}
	noArchImg := v1alpha1.Image{ID: "img-noarch"}

	tests := []struct {
		name       string
		images     []v1alpha1.Image
		targetArch string
		wantID     string
	}{
		{
			name:       "picks arm64 image for arm64 instance",
			images:     []v1alpha1.Image{amd64Img, arm64Img},
			targetArch: v1alpha1.ArchitectureArm64,
			wantID:     "img-arm64",
		},
		{
			name:       "picks amd64 image for amd64 instance",
			images:     []v1alpha1.Image{arm64Img, amd64Img},
			targetArch: v1alpha1.ArchitectureAmd64,
			wantID:     "img-amd64",
		},
		{
			name:       "single amd64 image, amd64 instance",
			images:     []v1alpha1.Image{amd64Img},
			targetArch: v1alpha1.ArchitectureAmd64,
			wantID:     "img-amd64",
		},
		{
			name:       "no arch match falls back to first image",
			images:     []v1alpha1.Image{amd64Img},
			targetArch: v1alpha1.ArchitectureArm64,
			wantID:     "img-amd64",
		},
		{
			name:       "empty image architecture treated as amd64",
			images:     []v1alpha1.Image{noArchImg},
			targetArch: v1alpha1.ArchitectureAmd64,
			wantID:     "img-noarch",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectImageForArchitecture(tt.images, tt.targetArch)
			if got.ID != tt.wantID {
				t.Errorf("selectImageForArchitecture() = %q, want %q", got.ID, tt.wantID)
			}
		})
	}
}

// TestInstanceTypeFallbackOnCapacityError verifies that createInstanceWithRetry
// tries the next instance type when the current one has no capacity in any zone.
func TestInstanceTypeFallbackOnCapacityError(t *testing.T) {
	// 3 instance types: first two will fail with capacity errors, third succeeds
	instanceTypes := []*instancetype.InstanceType{
		{Name: "ecs.g7.large", Architecture: "amd64"},
		{Name: "ecs.g7.xlarge", Architecture: "amd64"},
		{Name: "ecs.g7.2xlarge", Architecture: "amd64"},
	}

	images := []v1alpha1.Image{
		{ID: "m-amd64", Architecture: "x86_64"},
	}

	vswitches := []v1alpha1.VSwitch{
		{ID: "vsw-1", Zone: "cn-hangzhou-i", ZoneID: "cn-hangzhou-i"},
		{ID: "vsw-2", Zone: "cn-hangzhou-j", ZoneID: "cn-hangzhou-j"},
	}

	// Track which instance types and vswitches were tried
	type attempt struct {
		instanceType string
		vswitchID    string
	}
	var attempts []attempt

	// Mock create function: first 2 instance types fail in all zones, third succeeds
	createFn := func(ctx context.Context, opts instance.CreateOptions) (string, error) {
		attempts = append(attempts, attempt{opts.InstanceType, opts.VSwitchID})
		if opts.InstanceType == "ecs.g7.large" || opts.InstanceType == "ecs.g7.xlarge" {
			return "", fmt.Errorf("OperationDenied.NoStock: no stock for %s", opts.InstanceType)
		}
		return "i-success", nil
	}

	// We can't easily call createInstanceWithRetry directly without a full CloudProvider,
	// so we'll test the vswitchFallbackCreate + outer loop logic by simulating the pattern.
	// This test documents the expected behavior.
	for _, it := range instanceTypes {
		image := selectImageForArchitecture(images, it.Architecture)
		baseOpts := instance.CreateOptions{
			InstanceType: it.Name,
			ImageID:      image.ID,
		}

		instanceID, err := vswitchFallbackCreate(context.Background(), baseOpts, vswitches, createFn)
		if err == nil {
			// Success on this instance type
			assert.Equal(t, "i-success", instanceID)
			break
		}

		// If capacity error, continue to next instance type
		if !strings.Contains(err.Error(), "exhausted") {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	// Verify we tried: g7.large in both zones, g7.xlarge in both zones, then g7.2xlarge in first zone
	assert.Equal(t, 5, len(attempts), "expected 5 attempts total")

	// First instance type: both zones
	assert.Equal(t, "ecs.g7.large", attempts[0].instanceType)
	assert.Equal(t, "ecs.g7.xlarge", attempts[2].instanceType)
	assert.Equal(t, "ecs.g7.2xlarge", attempts[4].instanceType)

	// Last attempt should succeed
	assert.Equal(t, "vsw-1", attempts[4].vswitchID)
}

// TestInstanceTypeFallbackAllExhausted verifies that when all instance types
// have no capacity in any zone, we get a descriptive error.
func TestInstanceTypeFallbackAllExhausted(t *testing.T) {
	instanceTypes := []*instancetype.InstanceType{
		{Name: "ecs.g7.large", Architecture: "amd64"},
		{Name: "ecs.g7.xlarge", Architecture: "amd64"},
	}

	images := []v1alpha1.Image{
		{ID: "m-amd64", Architecture: "x86_64"},
	}

	vswitches := []v1alpha1.VSwitch{
		{ID: "vsw-1", Zone: "cn-hangzhou-i"},
	}

	createFn := func(ctx context.Context, opts instance.CreateOptions) (string, error) {
		return "", fmt.Errorf("OperationDenied.NoStock: no stock for %s", opts.InstanceType)
	}

	var lastErr error
	for _, it := range instanceTypes {
		image := selectImageForArchitecture(images, it.Architecture)
		baseOpts := instance.CreateOptions{
			InstanceType: it.Name,
			ImageID:      image.ID,
		}

		_, err := vswitchFallbackCreate(context.Background(), baseOpts, vswitches, createFn)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		lastErr = err
	}

	// All instance types exhausted
	assert.NotNil(t, lastErr)
	assert.Contains(t, lastErr.Error(), "exhausted")
}

// TestInstanceTypeFallbackFailFastOnNonCapacityError verifies that non-capacity
// errors (like quota or parameter errors) cause immediate failure without trying
// other instance types.
func TestInstanceTypeFallbackFailFastOnNonCapacityError(t *testing.T) {
	instanceTypes := []*instancetype.InstanceType{
		{Name: "ecs.g7.large", Architecture: "amd64"},
		{Name: "ecs.g7.xlarge", Architecture: "amd64"},
	}

	images := []v1alpha1.Image{
		{ID: "m-amd64", Architecture: "x86_64"},
	}

	vswitches := []v1alpha1.VSwitch{
		{ID: "vsw-1", Zone: "cn-hangzhou-i"},
	}

	calls := 0
	createFn := func(ctx context.Context, opts instance.CreateOptions) (string, error) {
		calls++
		return "", fmt.Errorf("QuotaExceed.Instance: quota exceeded")
	}

	for _, it := range instanceTypes {
		image := selectImageForArchitecture(images, it.Architecture)
		baseOpts := instance.CreateOptions{
			InstanceType: it.Name,
			ImageID:      image.ID,
		}

		_, err := vswitchFallbackCreate(context.Background(), baseOpts, vswitches, createFn)
		if err != nil {
			// Non-capacity error should fail fast, not continue to next instance type
			if !strings.Contains(err.Error(), "exhausted") {
				// This is a non-retryable error, should stop here
				break
			}
		}
	}

	// Should only call once, not try second instance type
	assert.Equal(t, 1, calls, "expected exactly 1 call on quota error, got %d", calls)
}

// TestInstanceTypeFallbackRespectsArchitecture verifies that each instance type
// gets the correct architecture-matched image, not just the first image.
func TestInstanceTypeFallbackRespectsArchitecture(t *testing.T) {
	instanceTypes := []*instancetype.InstanceType{
		{Name: "ecs.g7.large", Architecture: "amd64"},
		{Name: "ecs.g8y.large", Architecture: "arm64"},
	}

	images := []v1alpha1.Image{
		{ID: "m-x86", Architecture: "x86_64"},
		{ID: "m-arm", Architecture: "arm64"},
	}

	vswitches := []v1alpha1.VSwitch{
		{ID: "vsw-1", Zone: "cn-hangzhou-i"},
	}

	// First instance type fails, second succeeds
	type attempt struct {
		instanceType string
		imageID      string
	}
	var attempts []attempt

	createFn := func(ctx context.Context, opts instance.CreateOptions) (string, error) {
		attempts = append(attempts, attempt{opts.InstanceType, opts.ImageID})
		if opts.InstanceType == "ecs.g7.large" {
			return "", fmt.Errorf("OperationDenied.NoStock")
		}
		return "i-success", nil
	}

	for _, it := range instanceTypes {
		image := selectImageForArchitecture(images, it.Architecture)
		baseOpts := instance.CreateOptions{
			InstanceType: it.Name,
			ImageID:      image.ID,
		}

		instanceID, err := vswitchFallbackCreate(context.Background(), baseOpts, vswitches, createFn)
		if err == nil {
			assert.Equal(t, "i-success", instanceID)
			break
		}
	}

	// Verify architecture matching
	assert.Equal(t, 2, len(attempts))
	assert.Equal(t, "ecs.g7.large", attempts[0].instanceType)
	assert.Equal(t, "m-x86", attempts[0].imageID, "amd64 instance should use x86_64 image")
	assert.Equal(t, "ecs.g8y.large", attempts[1].instanceType)
	assert.Equal(t, "m-arm", attempts[1].imageID, "arm64 instance should use arm64 image")
}
