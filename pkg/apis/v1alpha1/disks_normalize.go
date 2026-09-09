package v1alpha1

import "fmt"

const (
	defaultSystemDiskCategory         = "cloud_essd"
	defaultSystemDiskSize       int32 = 40
	defaultESSDPerformanceLevel       = "PL0"
	instanceStorePolicyRAID0          = "RAID0"
	diskCategoryValues                = "cloud_essd, cloud_essd_entry, cloud_efficiency, cloud_pperf, cloud_sperf, cloud_ssd, cloud_auto, ephemeral_ssd, cloud, cloud_essd_xc0, cloud_essd_xc1, elastic_ephemeral_disk_premium, elastic_ephemeral_disk_standard"
)

type diskCategoryConstraint struct {
	minSize   int32
	maxSize   int32
	available bool
}

var diskCategoryConstraints = map[string]diskCategoryConstraint{
	"cloud_essd":                      {minSize: 1, maxSize: 65536, available: true},
	"cloud_essd_entry":                {minSize: 10, maxSize: 32768, available: true},
	"cloud_efficiency":                {minSize: 20, maxSize: 32768, available: true},
	"cloud_pperf":                     {minSize: 20, maxSize: 32768, available: true},
	"cloud_sperf":                     {minSize: 20, maxSize: 32768, available: true},
	"cloud_ssd":                       {minSize: 20, maxSize: 32768, available: true},
	"cloud_auto":                      {minSize: 1, maxSize: 65536, available: true},
	"ephemeral_ssd":                   {minSize: 5, maxSize: 800, available: true},
	"cloud":                           {minSize: 5, maxSize: 2000, available: true},
	"cloud_essd_xc0":                  {minSize: 40, maxSize: 2048, available: true},
	"cloud_essd_xc1":                  {available: false},
	"elastic_ephemeral_disk_premium":  {minSize: 64, maxSize: 8192, available: true},
	"elastic_ephemeral_disk_standard": {minSize: 64, maxSize: 8192, available: true},
}

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
	if err := validateDiskCategoryAndSize("systemDisk", normalized.SystemDisk.Category, normalized.SystemDisk.Size); err != nil {
		return NormalizedDisks{}, err
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
			path := fmt.Sprintf("dataDisks[%d]", i)
			if err := validateDiskCategoryAndSize(path, dataDisk.Category, dataDisk.Size); err != nil {
				return NormalizedDisks{}, err
			}
			if err := normalizeDiskPerformance(path, dataDisk.Category, &dataDisk.PerformanceLevel); err != nil {
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

func validateDiskCategoryAndSize(path, category string, size int32) error {
	constraint, ok := diskCategoryConstraints[category]
	if !ok {
		return fmt.Errorf("%s.category must be one of: %s", path, diskCategoryValues)
	}
	if !constraint.available {
		return fmt.Errorf("%s.category %q is not supported", path, category)
	}
	if size < constraint.minSize || size > constraint.maxSize {
		return fmt.Errorf("%s.size must be between %d and %d GB for category %s", path, constraint.minSize, constraint.maxSize, category)
	}
	return nil
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
