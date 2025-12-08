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

package v1alpha1

import (
	"fmt"
	"regexp"
)

// Validate validates the ECSNodeClass spec
func (nc *ECSNodeClass) Validate() error {
	if err := nc.validateVSwitchSelectors(); err != nil {
		return err
	}
	if err := nc.validateSecurityGroupSelectors(); err != nil {
		return err
	}
	if err := nc.validateImageSelectors(); err != nil {
		return err
	}
	if err := nc.validateSystemDisk(); err != nil {
		return err
	}
	if err := nc.validateDataDisks(); err != nil {
		return err
	}
	if err := nc.validateSpotConfig(); err != nil {
		return err
	}
	if err := nc.validateCapacityReservation(); err != nil {
		return err
	}
	if err := nc.validateMetadataOptions(); err != nil {
		return err
	}
	return nil
}

func (nc *ECSNodeClass) validateVSwitchSelectors() error {
	if len(nc.Spec.VSwitchSelectorTerms) == 0 {
		return fmt.Errorf("vSwitchSelectorTerms is required")
	}
	for i, term := range nc.Spec.VSwitchSelectorTerms {
		if term.ID == nil && len(term.Tags) == 0 && term.ZoneID == nil {
			return fmt.Errorf("vSwitchSelectorTerms[%d] must specify at least one of: id, tags, or zoneID", i)
		}
		if term.ID != nil && !isValidResourceID(*term.ID, "vsw") {
			return fmt.Errorf("vSwitchSelectorTerms[%d].id is not a valid VSwitch ID", i)
		}
	}
	return nil
}

func (nc *ECSNodeClass) validateSecurityGroupSelectors() error {
	if len(nc.Spec.SecurityGroupSelectorTerms) == 0 {
		return fmt.Errorf("securityGroupSelectorTerms is required")
	}
	if len(nc.Spec.SecurityGroupSelectorTerms) > 5 {
		return fmt.Errorf("maximum 5 security groups allowed, got %d", len(nc.Spec.SecurityGroupSelectorTerms))
	}
	for i, term := range nc.Spec.SecurityGroupSelectorTerms {
		if term.ID == nil && term.Name == nil && len(term.Tags) == 0 {
			return fmt.Errorf("securityGroupSelectorTerms[%d] must specify at least one of: id, name, or tags", i)
		}
		if term.ID != nil && !isValidResourceID(*term.ID, "sg") {
			return fmt.Errorf("securityGroupSelectorTerms[%d].id is not a valid security group ID", i)
		}
	}
	return nil
}

func (nc *ECSNodeClass) validateImageSelectors() error {
	if len(nc.Spec.ImageSelectorTerms) == 0 {
		return fmt.Errorf("imageSelectorTerms is required")
	}
	for i, term := range nc.Spec.ImageSelectorTerms {
		count := 0
		if term.ID != nil {
			count++
		}
		if term.ImageOwnerAlias != nil {
			count++
		}
		if term.Name != nil {
			count++
		}
		if len(term.Tags) > 0 {
			count++
		}
		if count == 0 {
			return fmt.Errorf("imageSelectorTerms[%d] must specify at least one of: id, alias, name, or tags", i)
		}
		if term.ID != nil && !isValidResourceID(*term.ID, "m") {
			return fmt.Errorf("imageSelectorTerms[%d].id is not a valid image ID", i)
		}
		if term.ImageOwnerAlias != nil && !isValidImageOwnerAlias(*term.ImageOwnerAlias) {
			return fmt.Errorf("imageSelectorTerms[%d].imageOwnerAlias must be one of: system, self, others, marketplace", i)
		}
	}
	return nil
}

func (nc *ECSNodeClass) validateSystemDisk() error {
	if nc.Spec.SystemDisk == nil {
		return nil
	}
	disk := nc.Spec.SystemDisk
	if !isValidDiskCategory(disk.Category) {
		return fmt.Errorf("systemDisk.category must be one of: cloud_efficiency, cloud_ssd, cloud_essd")
	}
	if disk.Size != nil && (*disk.Size < 20 || *disk.Size > 500) {
		return fmt.Errorf("systemDisk.size must be between 20 and 500 GB")
	}
	if disk.PerformanceLevel != nil && !isValidPerformanceLevel(*disk.PerformanceLevel) {
		return fmt.Errorf("systemDisk.performanceLevel must be one of: PL0, PL1, PL2, PL3")
	}
	return nil
}

func (nc *ECSNodeClass) validateDataDisks() error {
	if len(nc.Spec.DataDisks) > 16 {
		return fmt.Errorf("maximum 16 data disks allowed, got %d", len(nc.Spec.DataDisks))
	}
	for i, disk := range nc.Spec.DataDisks {
		if !isValidDiskCategory(disk.Category) {
			return fmt.Errorf("dataDisks[%d].category must be one of: cloud_efficiency, cloud_ssd, cloud_essd", i)
		}
		if disk.Size < 20 || disk.Size > 32768 {
			return fmt.Errorf("dataDisks[%d].size must be between 20 and 32768 GB", i)
		}
		if disk.PerformanceLevel != nil && !isValidPerformanceLevel(*disk.PerformanceLevel) {
			return fmt.Errorf("dataDisks[%d].performanceLevel must be one of: PL0, PL1, PL2, PL3", i)
		}
	}
	return nil
}

func (nc *ECSNodeClass) validateSpotConfig() error {
	if nc.Spec.SpotStrategy != nil {
		if !isValidSpotStrategy(*nc.Spec.SpotStrategy) {
			return fmt.Errorf("spotStrategy must be one of: SpotAsPriceGo, SpotWithPriceLimit")
		}
		if *nc.Spec.SpotStrategy == "SpotWithPriceLimit" && nc.Spec.SpotPriceLimit == nil {
			return fmt.Errorf("spotPriceLimit is required when spotStrategy is SpotWithPriceLimit")
		}
	}
	if nc.Spec.SpotPriceLimit != nil && *nc.Spec.SpotPriceLimit <= 0 {
		return fmt.Errorf("spotPriceLimit must be greater than 0")
	}
	return nil
}

func (nc *ECSNodeClass) validateCapacityReservation() error {
	if nc.Spec.CapacityReservationPreference != nil {
		if !isValidCapacityReservationPreference(*nc.Spec.CapacityReservationPreference) {
			return fmt.Errorf("capacityReservationPreference must be one of: open, target, none")
		}
	}
	// Validate capacity reservation selector terms
	for i, term := range nc.Spec.CapacityReservationSelectorTerms {
		if term.ID == nil && len(term.Tags) == 0 {
			return fmt.Errorf("capacityReservationSelectorTerms[%d] must specify at least one of: id or tags", i)
		}
		if term.ID != nil && !isValidResourceID(*term.ID, "cr") {
			return fmt.Errorf("capacityReservationSelectorTerms[%d].id is not a valid capacity reservation ID", i)
		}
	}
	return nil
}

// Helper functions

func isValidResourceID(id, prefix string) bool {
	pattern := fmt.Sprintf("^%s-[a-z0-9]+$", prefix)
	matched, _ := regexp.MatchString(pattern, id)
	return matched
}

func isValidImageOwnerAlias(owner string) bool {
	return owner == "system" || owner == "self" || owner == "others" || owner == "marketplace"
}

func isValidDiskCategory(category string) bool {
	validCategories := map[string]bool{
		"cloud_efficiency": true,
		"cloud_ssd":        true,
		"cloud_essd":       true,
	}
	return validCategories[category]
}

func isValidPerformanceLevel(level string) bool {
	validLevels := map[string]bool{
		"PL0": true,
		"PL1": true,
		"PL2": true,
		"PL3": true,
	}
	return validLevels[level]
}

func isValidSpotStrategy(strategy string) bool {
	return strategy == "SpotAsPriceGo" || strategy == "SpotWithPriceLimit"
}

func isValidCapacityReservationPreference(pref string) bool {
	return pref == "open" || pref == "none" || pref == "target"
}

func (nc *ECSNodeClass) validateMetadataOptions() error {
	if nc.Spec.MetadataOptions == nil {
		return nil
	}
	validTokens := map[string]bool{"optional": true, "required": true}
	if !validTokens[nc.Spec.MetadataOptions.HttpTokens] {
		return fmt.Errorf("metadataOptions.httpTokens must be one of: optional, required")
	}
	if nc.Spec.MetadataOptions.HttpPutResponseHopLimit != nil {
		if *nc.Spec.MetadataOptions.HttpPutResponseHopLimit < 1 || *nc.Spec.MetadataOptions.HttpPutResponseHopLimit > 64 {
			return fmt.Errorf("metadataOptions.httpPutResponseHopLimit must be between 1 and 64")
		}
	}
	return nil
}
