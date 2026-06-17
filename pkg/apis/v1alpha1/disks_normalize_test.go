package v1alpha1

import "testing"

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
