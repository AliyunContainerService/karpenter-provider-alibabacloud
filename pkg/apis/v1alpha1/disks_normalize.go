package v1alpha1

import "fmt"

const (
	defaultSystemDiskCategory         = "cloud_essd"
	defaultSystemDiskSize       int32 = 40
	defaultESSDPerformanceLevel       = "PL0"
	instanceStorePolicyRAID0          = "RAID0"
)

// NormalizedDisks is the canonical launch-affecting disk state.
type NormalizedDisks struct {
	SystemDisk          NormalizedSystemDisk `json:"systemDisk"`
	DataDisks           []NormalizedDataDisk `json:"dataDisks,omitempty"`
	InstanceStorePolicy *string              `json:"instanceStorePolicy,omitempty"`
}

type NormalizedSystemDisk struct {
	Category         string `json:"category"`
	Size             int32  `json:"size"`
	PerformanceLevel string `json:"performanceLevel,omitempty"`
	Encrypted        *bool  `json:"encrypted,omitempty"`
	KMSKeyID         string `json:"kmsKeyID,omitempty"`
}

type NormalizedDataDisk struct {
	Category           string `json:"category"`
	Size               int32  `json:"size"`
	Device             string `json:"device,omitempty"`
	PerformanceLevel   string `json:"performanceLevel,omitempty"`
	Encrypted          *bool  `json:"encrypted,omitempty"`
	KMSKeyID           string `json:"kmsKeyID,omitempty"`
	SnapshotID         string `json:"snapshotID,omitempty"`
	DeleteWithInstance *bool  `json:"deleteWithInstance,omitempty"`
}

// NormalizeDisks applies all disk defaults and validation used by validation,
// create requests, batch keys, hashing, and drift checks.
func NormalizeDisks(spec ECSNodeClassSpec) (NormalizedDisks, error) {
	normalized := NormalizedDisks{
		SystemDisk: NormalizedSystemDisk{
			Category: defaultSystemDiskCategory,
			Size:     defaultSystemDiskSize,
		},
	}

	if spec.InstanceStorePolicy != nil {
		if *spec.InstanceStorePolicy != instanceStorePolicyRAID0 {
			return NormalizedDisks{}, fmt.Errorf("instanceStorePolicy must be unset or RAID0")
		}
		policy := *spec.InstanceStorePolicy
		normalized.InstanceStorePolicy = &policy
	}

	if spec.SystemDisk != nil {
		disk := spec.SystemDisk
		if disk.Category != "" {
			normalized.SystemDisk.Category = disk.Category
		}
		if disk.Size != nil {
			normalized.SystemDisk.Size = *disk.Size
		}
		if disk.PerformanceLevel != nil {
			normalized.SystemDisk.PerformanceLevel = *disk.PerformanceLevel
		}
		normalized.SystemDisk.Encrypted = normalizeEncrypted(disk.Encrypted)
		if disk.KMSKeyID != nil {
			normalized.SystemDisk.KMSKeyID = *disk.KMSKeyID
		}
	}
	if !isValidDiskCategory(normalized.SystemDisk.Category) {
		return NormalizedDisks{}, fmt.Errorf("systemDisk.category must be one of: cloud_efficiency, cloud_ssd, cloud_essd")
	}
	if err := normalizeDiskPerformance("systemDisk", normalized.SystemDisk.Category, &normalized.SystemDisk.PerformanceLevel); err != nil {
		return NormalizedDisks{}, err
	}
	if normalized.SystemDisk.KMSKeyID != "" && normalized.SystemDisk.Encrypted == nil {
		return NormalizedDisks{}, fmt.Errorf("systemDisk.kmsKeyID requires encrypted=true")
	}

	if len(spec.DataDisks) > 0 {
		normalized.DataDisks = make([]NormalizedDataDisk, 0, len(spec.DataDisks))
		for i, disk := range spec.DataDisks {
			dataDisk := NormalizedDataDisk{
				Category:  disk.Category,
				Size:      disk.Size,
				Encrypted: normalizeEncrypted(disk.Encrypted),
			}
			if disk.Device != nil {
				dataDisk.Device = *disk.Device
			}
			if disk.PerformanceLevel != nil {
				dataDisk.PerformanceLevel = *disk.PerformanceLevel
			}
			if disk.KMSKeyID != nil {
				dataDisk.KMSKeyID = *disk.KMSKeyID
			}
			if disk.SnapshotID != nil {
				dataDisk.SnapshotID = *disk.SnapshotID
			}
			if disk.DeleteWithInstance != nil && !*disk.DeleteWithInstance {
				value := false
				dataDisk.DeleteWithInstance = &value
			}
			if !isValidDiskCategory(dataDisk.Category) {
				return NormalizedDisks{}, fmt.Errorf("dataDisks[%d].category must be one of: cloud_efficiency, cloud_ssd, cloud_essd", i)
			}
			if err := normalizeDiskPerformance(fmt.Sprintf("dataDisks[%d]", i), dataDisk.Category, &dataDisk.PerformanceLevel); err != nil {
				return NormalizedDisks{}, err
			}
			if dataDisk.KMSKeyID != "" && dataDisk.Encrypted == nil {
				return NormalizedDisks{}, fmt.Errorf("dataDisks[%d].kmsKeyID requires encrypted=true", i)
			}
			normalized.DataDisks = append(normalized.DataDisks, dataDisk)
		}
	}

	return normalized, nil
}

func normalizeEncrypted(encrypted *bool) *bool {
	if encrypted == nil || !*encrypted {
		return nil
	}
	value := true
	return &value
}

func normalizeDiskPerformance(path, category string, performanceLevel *string) error {
	if category != defaultSystemDiskCategory {
		if *performanceLevel != "" {
			return fmt.Errorf("%s.performanceLevel is only supported for cloud_essd disks", path)
		}
		return nil
	}
	if *performanceLevel == "" {
		*performanceLevel = defaultESSDPerformanceLevel
	}
	if !isValidPerformanceLevel(*performanceLevel) {
		return fmt.Errorf("%s.performanceLevel must be one of: PL0, PL1, PL2, PL3", path)
	}
	return nil
}
