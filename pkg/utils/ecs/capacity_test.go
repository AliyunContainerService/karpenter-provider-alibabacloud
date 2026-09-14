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

package ecs

import (
	"testing"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
)

func strptr(s string) *string { return &s }

func TestCapacityTypeFromInstance(t *testing.T) {
	cases := []struct {
		name         string
		chargeType   string
		spotStrategy string
		want         string
	}{
		// The critical regression: spot instances report PostPaid charge type.
		{"spot with PostPaid charge type (SpotAsPriceGo)", "PostPaid", "SpotAsPriceGo", v1alpha1.CapacityTypeSpot},
		{"spot with PostPaid charge type (SpotWithPriceLimit)", "PostPaid", "SpotWithPriceLimit", v1alpha1.CapacityTypeSpot},
		{"on-demand PostPaid NoSpot", "PostPaid", "NoSpot", v1alpha1.CapacityTypeOnDemand},
		{"on-demand PostPaid empty strategy", "PostPaid", "", v1alpha1.CapacityTypeOnDemand},
		{"subscription PrePaid", "PrePaid", "NoSpot", v1alpha1.CapacityTypePrePaid},
		{"subscription PrePaid empty strategy", "PrePaid", "", v1alpha1.CapacityTypePrePaid},
		{"unknown charge type no spot defaults on-demand", "", "", v1alpha1.CapacityTypeOnDemand},
		// Even if charge type were reported oddly, spot strategy wins.
		{"spot wins over PrePaid", "PrePaid", "SpotAsPriceGo", v1alpha1.CapacityTypeSpot},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CapacityTypeFromInstance(tc.chargeType, tc.spotStrategy)
			if got != tc.want {
				t.Fatalf("CapacityTypeFromInstance(%q,%q)=%q, want %q", tc.chargeType, tc.spotStrategy, got, tc.want)
			}
		})
	}
}

func TestIsSpotStrategy(t *testing.T) {
	cases := map[string]bool{
		"":                   false,
		"NoSpot":             false,
		"  ":                 false,
		"SpotAsPriceGo":      true,
		"SpotWithPriceLimit": true,
	}
	for in, want := range cases {
		if got := IsSpotStrategy(in); got != want {
			t.Errorf("IsSpotStrategy(%q)=%v, want %v", in, got, want)
		}
	}
}

func TestSpotStrategyForCapacityType(t *testing.T) {
	cases := []struct {
		name      string
		capacity  string
		requested *string
		want      string
	}{
		{"on-demand -> NoSpot", v1alpha1.CapacityTypeOnDemand, nil, SpotStrategyNoSpot},
		{"on-demand ignores requested strategy", v1alpha1.CapacityTypeOnDemand, strptr("SpotAsPriceGo"), SpotStrategyNoSpot},
		{"pre-paid -> NoSpot", v1alpha1.CapacityTypePrePaid, nil, SpotStrategyNoSpot},
		{"spot nil -> default SpotAsPriceGo", v1alpha1.CapacityTypeSpot, nil, SpotStrategyAsPriceGo},
		{"spot empty -> default SpotAsPriceGo", v1alpha1.CapacityTypeSpot, strptr(""), SpotStrategyAsPriceGo},
		{"spot invalid -> default SpotAsPriceGo", v1alpha1.CapacityTypeSpot, strptr("bogus"), SpotStrategyAsPriceGo},
		{"spot explicit AsPriceGo", v1alpha1.CapacityTypeSpot, strptr("SpotAsPriceGo"), SpotStrategyAsPriceGo},
		{"spot explicit WithPriceLimit", v1alpha1.CapacityTypeSpot, strptr("SpotWithPriceLimit"), SpotStrategyWithPriceLimit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SpotStrategyForCapacityType(tc.capacity, tc.requested)
			if got != tc.want {
				t.Fatalf("SpotStrategyForCapacityType(%q,%v)=%q, want %q", tc.capacity, tc.requested, got, tc.want)
			}
		})
	}
}

func TestIsValidSpotStrategy(t *testing.T) {
	valid := []string{"SpotAsPriceGo", "SpotWithPriceLimit"}
	invalid := []string{"", "NoSpot", "spot", "bogus"}
	for _, s := range valid {
		if !IsValidSpotStrategy(s) {
			t.Errorf("IsValidSpotStrategy(%q)=false, want true", s)
		}
	}
	for _, s := range invalid {
		if IsValidSpotStrategy(s) {
			t.Errorf("IsValidSpotStrategy(%q)=true, want false", s)
		}
	}
}

func TestKubeArchitecture(t *testing.T) {
	cases := map[string]string{
		"X86":     v1alpha1.ArchitectureAmd64,
		"x86_64":  v1alpha1.ArchitectureAmd64,
		"amd64":   v1alpha1.ArchitectureAmd64,
		"ARM":     v1alpha1.ArchitectureArm64,
		"ARM64":   v1alpha1.ArchitectureArm64,
		"aarch64": v1alpha1.ArchitectureArm64,
		"":        v1alpha1.ArchitectureAmd64,
		"weird":   v1alpha1.ArchitectureAmd64,
	}
	for in, want := range cases {
		if got := KubeArchitecture(in); got != want {
			t.Errorf("KubeArchitecture(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestArchitectureFromInstanceType(t *testing.T) {
	cases := map[string]string{
		// ARM families
		"ecs.g8y.large":   v1alpha1.ArchitectureArm64,
		"ecs.c8y.xlarge":  v1alpha1.ArchitectureArm64,
		"ecs.r8y.2xlarge": v1alpha1.ArchitectureArm64,
		"ecs.g6r.large":   v1alpha1.ArchitectureArm64,
		// x86 families (including GPU families that the old buggy code misread as arm64)
		"ecs.gn7i-c8g1.2xlarge": v1alpha1.ArchitectureAmd64,
		"ecs.cu6.large":         v1alpha1.ArchitectureAmd64,
		"ecs.g7.large":          v1alpha1.ArchitectureAmd64,
		"ecs.c7.xlarge":         v1alpha1.ArchitectureAmd64,
		"":                      v1alpha1.ArchitectureAmd64,
	}
	for in, want := range cases {
		if got := ArchitectureFromInstanceType(in); got != want {
			t.Errorf("ArchitectureFromInstanceType(%q)=%q, want %q", in, got, want)
		}
	}
}
