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

// Package ecs centralizes conversions between Alibaba Cloud ECS API values and
// the Karpenter domain model. Every place that reads an ECS API field or writes
// an ECS API request parameter for capacity type / spot strategy / architecture
// MUST reuse these helpers instead of re-implementing string comparisons inline.
// This prevents the "changed in one place, missed in three others" class of bugs.
package ecs

import (
	"strings"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
)

// ECS API string values. These are the raw wire values used by the Alibaba
// Cloud ECS OpenAPI (RunInstances / DescribeInstances / DescribeAvailableResource
// / CreateLaunchTemplate). They are intentionally kept private to this package;
// callers should go through the helper functions below.
const (
	// ChargeTypePostPaid is pay-as-you-go. NOTE: spot instances also report
	// InstanceChargeType=PostPaid — spot is distinguished solely by SpotStrategy.
	ChargeTypePostPaid = "PostPaid"
	// ChargeTypePrePaid is subscription (monthly/yearly).
	ChargeTypePrePaid = "PrePaid"

	// SpotStrategyNoSpot means the instance is NOT a spot instance.
	SpotStrategyNoSpot = "NoSpot"
	// SpotStrategyAsPriceGo bids at the market price (SpotAsPriceGo).
	SpotStrategyAsPriceGo = "SpotAsPriceGo"
	// SpotStrategyWithPriceLimit bids with an explicit price ceiling.
	SpotStrategyWithPriceLimit = "SpotWithPriceLimit"
)

// IsSpotStrategy reports whether the given ECS SpotStrategy value denotes a spot
// instance. Empty string and "NoSpot" are treated as non-spot.
func IsSpotStrategy(spotStrategy string) bool {
	s := strings.TrimSpace(spotStrategy)
	return s != "" && s != SpotStrategyNoSpot
}

// CapacityTypeFromInstance derives the Karpenter capacity-type label value
// ("spot" / "on-demand" / "pre-paid") from an ECS instance's charge type and
// spot strategy, as returned by DescribeInstances.
//
// IMPORTANT: spot instances on Alibaba Cloud keep InstanceChargeType=PostPaid and
// are distinguished only by SpotStrategy, so the spot check MUST come first;
// otherwise spot instances would be misclassified as on-demand.
func CapacityTypeFromInstance(instanceChargeType, spotStrategy string) string {
	if IsSpotStrategy(spotStrategy) {
		return v1alpha1.CapacityTypeSpot
	}
	switch strings.TrimSpace(instanceChargeType) {
	case ChargeTypePrePaid:
		return v1alpha1.CapacityTypePrePaid
	case ChargeTypePostPaid:
		return v1alpha1.CapacityTypeOnDemand
	default:
		// Unknown/empty charge type with no spot strategy: default to on-demand,
		// which is the safe assumption for a running pay-as-you-go instance.
		return v1alpha1.CapacityTypeOnDemand
	}
}

// SpotStrategyForCapacityType maps a Karpenter capacity-type value plus an
// optional user-provided ECSNodeClass.Spec.SpotStrategy into the ECS SpotStrategy
// request parameter to send to RunInstances / CreateLaunchTemplate.
//
//   - capacity-type == "spot": use the caller's strategy when set (and valid),
//     otherwise fall back to SpotAsPriceGo.
//   - anything else: NoSpot.
func SpotStrategyForCapacityType(capacityType string, requestedStrategy *string) string {
	if capacityType != v1alpha1.CapacityTypeSpot {
		return SpotStrategyNoSpot
	}
	if requestedStrategy != nil {
		if s := strings.TrimSpace(*requestedStrategy); IsValidSpotStrategy(s) {
			return s
		}
	}
	return SpotStrategyAsPriceGo
}

// IsValidSpotStrategy reports whether the given value is a spot strategy that
// actually requests spot capacity (SpotAsPriceGo or SpotWithPriceLimit).
func IsValidSpotStrategy(strategy string) bool {
	switch strings.TrimSpace(strategy) {
	case SpotStrategyAsPriceGo, SpotStrategyWithPriceLimit:
		return true
	default:
		return false
	}
}

// KubeArchitecture maps an ECS CpuArchitecture value (as returned by
// DescribeInstanceTypes: "X86"/"x86_64"/"ARM"/"ARM64"/"aarch64") to the
// Kubernetes kubernetes.io/arch label value ("amd64"/"arm64").
//
// DescribeInstances does NOT return CpuArchitecture, so callers that only have a
// running instance must obtain the architecture from its instance type via
// DescribeInstanceTypes rather than guessing from the instance-type name.
func KubeArchitecture(ecsArch string) string {
	switch strings.ToLower(strings.TrimSpace(ecsArch)) {
	case "arm", "arm64", "aarch64":
		return v1alpha1.ArchitectureArm64
	case "x86", "x86_64", "amd64", "i386":
		return v1alpha1.ArchitectureAmd64
	default:
		// Default to amd64: the overwhelming majority of ECS instance types are
		// x86_64, and an unknown value is far more likely x86 than ARM.
		return v1alpha1.ArchitectureAmd64
	}
}

// armInstanceFamilyPrefixes lists the ECS instance-type family prefixes that use
// ARM (aarch64) CPUs. Alibaba Cloud ARM offerings are the Yitian 710 families
// (g8y/c8y/r8y) and the Ampere Altra families (g6r/c6r/r6r). Everything else
// (including GPU families such as ecs.gn* and ecs.cu*) is x86_64.
var armInstanceFamilyPrefixes = []string{
	"ecs.g8y", "ecs.c8y", "ecs.r8y",
	"ecs.g6r", "ecs.c6r", "ecs.r6r",
}

// ArchitectureFromInstanceType infers the Kubernetes arch label value from an ECS
// instance-type name. This is a heuristic used only when the authoritative
// CpuArchitecture is unavailable (DescribeInstances does not return it); prefer
// KubeArchitecture with DescribeInstanceTypes' CpuArchitecture whenever possible.
func ArchitectureFromInstanceType(instanceType string) string {
	it := strings.ToLower(strings.TrimSpace(instanceType))
	for _, prefix := range armInstanceFamilyPrefixes {
		if strings.HasPrefix(it, prefix) {
			return v1alpha1.ArchitectureArm64
		}
	}
	return v1alpha1.ArchitectureAmd64
}
