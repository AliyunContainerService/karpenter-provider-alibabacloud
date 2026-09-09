package v1alpha1

import (
	"strings"
	"testing"
)

func TestNormalizeDisksDefaultEquivalence(t *testing.T) {
	defaulted := ECSNodeClassSpec{
		SystemDisk: &SystemDiskSpec{
			Category:         "cloud_essd",
			Size:             ptrForUnit(int32(40)),
			PerformanceLevel: ptrForUnit("PL0"),
			Encrypted:        ptrForUnit(false),
		},
	}

	base, err := NormalizeDisks(ECSNodeClassSpec{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	explicit, err := NormalizeDisks(defaulted)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if base.SystemDisk != explicit.SystemDisk {
		t.Fatalf("expected default system disks to normalize equally, got %#v != %#v", base.SystemDisk, explicit.SystemDisk)
	}
}

func TestNormalizeDisksPreservesDataDiskOrder(t *testing.T) {
	normalized, err := NormalizeDisks(ECSNodeClassSpec{
		DataDisks: []DataDiskSpec{
			{Category: "cloud_essd", Size: 40, Device: ptrForUnit("/dev/xvdb")},
			{Category: "cloud_ssd", Size: 80, Device: ptrForUnit("/dev/xvdc")},
		},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if normalized.DataDisks[0].Device != "/dev/xvdb" || normalized.DataDisks[1].Device != "/dev/xvdc" {
		t.Fatalf("expected data disk order to be preserved, got %#v", normalized.DataDisks)
	}
}

func TestNormalizeDisksValidation(t *testing.T) {
	tests := []struct {
		name string
		spec ECSNodeClassSpec
	}{
		{
			name: "rejects non ESSD performance level on system disk",
			spec: ECSNodeClassSpec{
				SystemDisk: &SystemDiskSpec{Category: "cloud_ssd", Size: ptrForUnit(int32(40)), PerformanceLevel: ptrForUnit("PL1")},
			},
		},
		{
			name: "rejects non ESSD performance level on data disk",
			spec: ECSNodeClassSpec{
				DataDisks: []DataDiskSpec{{Category: "cloud_ssd", Size: 40, PerformanceLevel: ptrForUnit("PL1")}},
			},
		},
		{
			name: "rejects system disk kms without encryption",
			spec: ECSNodeClassSpec{
				SystemDisk: &SystemDiskSpec{Category: "cloud_essd", Size: ptrForUnit(int32(40)), KMSKeyID: ptrForUnit("kms-1")},
			},
		},
		{
			name: "rejects data disk kms without encryption",
			spec: ECSNodeClassSpec{
				DataDisks: []DataDiskSpec{{Category: "cloud_essd", Size: 40, KMSKeyID: ptrForUnit("kms-1")}},
			},
		},
		{
			name: "rejects invalid instance store policy",
			spec: ECSNodeClassSpec{InstanceStorePolicy: ptrForUnit("None")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NormalizeDisks(tt.spec); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestNormalizeDisksCategorySizeLimits(t *testing.T) {
	categories := []struct {
		name string
		min  int32
		max  int32
	}{
		{name: "cloud_essd", min: 1, max: 65536},
		{name: "cloud_essd_entry", min: 10, max: 32768},
		{name: "cloud_efficiency", min: 20, max: 32768},
		{name: "cloud_pperf", min: 20, max: 32768},
		{name: "cloud_sperf", min: 20, max: 32768},
		{name: "cloud_ssd", min: 20, max: 32768},
		{name: "cloud_auto", min: 1, max: 65536},
		{name: "ephemeral_ssd", min: 5, max: 800},
		{name: "cloud", min: 5, max: 2000},
		{name: "cloud_essd_xc0", min: 40, max: 2048},
		{name: "elastic_ephemeral_disk_premium", min: 64, max: 8192},
		{name: "elastic_ephemeral_disk_standard", min: 64, max: 8192},
	}

	for _, category := range categories {
		for _, boundary := range []struct {
			name  string
			size  int32
			valid bool
		}{
			{name: "below minimum", size: category.min - 1},
			{name: "minimum", size: category.min, valid: true},
			{name: "maximum", size: category.max, valid: true},
			{name: "above maximum", size: category.max + 1},
		} {
			t.Run(category.name+"/system/"+boundary.name, func(t *testing.T) {
				_, err := NormalizeDisks(ECSNodeClassSpec{SystemDisk: &SystemDiskSpec{Category: category.name, Size: ptrForUnit(boundary.size)}})
				if boundary.valid && err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				if !boundary.valid && err == nil {
					t.Fatal("expected error")
				}
			})
			t.Run(category.name+"/data/"+boundary.name, func(t *testing.T) {
				_, err := NormalizeDisks(ECSNodeClassSpec{DataDisks: []DataDiskSpec{{Category: category.name, Size: boundary.size}}})
				if boundary.valid && err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				if !boundary.valid && err == nil {
					t.Fatal("expected error")
				}
			})
		}
	}
}

func TestNormalizeDisksCategoryAvailability(t *testing.T) {
	tests := []struct {
		name        string
		category    string
		expectError string
	}{
		{name: "unknown category", category: "unknown", expectError: "category must be one of"},
		{name: "unavailable category", category: "cloud_essd_xc1", expectError: `category "cloud_essd_xc1" is not supported`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NormalizeDisks(ECSNodeClassSpec{SystemDisk: &SystemDiskSpec{Category: tt.category, Size: ptrForUnit(int32(40))}})
			if err == nil || !strings.Contains(err.Error(), tt.expectError) {
				t.Fatalf("expected error containing %q, got %v", tt.expectError, err)
			}
		})
	}
}

func TestNormalizeDisksPerformanceLevels(t *testing.T) {
	for _, level := range []string{"PL0", "PL1", "PL2", "PL3"} {
		t.Run(level, func(t *testing.T) {
			normalized, err := NormalizeDisks(ECSNodeClassSpec{
				SystemDisk: &SystemDiskSpec{Category: "cloud_essd", Size: ptrForUnit(int32(40)), PerformanceLevel: ptrForUnit(level)},
				DataDisks:  []DataDiskSpec{{Category: "cloud_essd", Size: 40, PerformanceLevel: ptrForUnit(level)}},
			})
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if normalized.SystemDisk.PerformanceLevel != level || normalized.DataDisks[0].PerformanceLevel != level {
				t.Fatalf("expected %s to be preserved, got %#v", level, normalized)
			}
		})
	}

	normalized, err := NormalizeDisks(ECSNodeClassSpec{
		SystemDisk: &SystemDiskSpec{Category: "cloud_efficiency", Size: ptrForUnit(int32(40))},
		DataDisks:  []DataDiskSpec{{Category: "cloud_ssd", Size: 40}},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if normalized.SystemDisk.PerformanceLevel != "" || normalized.DataDisks[0].PerformanceLevel != "" {
		t.Fatalf("expected non-ESSD performance levels to remain empty, got %#v", normalized)
	}
}

func TestNormalizeDisksDeleteWithInstanceDefaultEquivalence(t *testing.T) {
	unset, err := NormalizeDisks(ECSNodeClassSpec{
		DataDisks: []DataDiskSpec{{Category: "cloud_essd", Size: 40}},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	explicitTrue, err := NormalizeDisks(ECSNodeClassSpec{
		DataDisks: []DataDiskSpec{{Category: "cloud_essd", Size: 40, DeleteWithInstance: ptrForUnit(true)}},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	explicitFalse, err := NormalizeDisks(ECSNodeClassSpec{
		DataDisks: []DataDiskSpec{{Category: "cloud_essd", Size: 40, DeleteWithInstance: ptrForUnit(false)}},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if unset.DataDisks[0].DeleteWithInstance != explicitTrue.DataDisks[0].DeleteWithInstance {
		t.Fatalf("expected nil and true deleteWithInstance to normalize equally")
	}
	if explicitFalse.DataDisks[0].DeleteWithInstance == nil || *explicitFalse.DataDisks[0].DeleteWithInstance {
		t.Fatalf("expected explicit false to be preserved, got %#v", explicitFalse.DataDisks[0].DeleteWithInstance)
	}
}
