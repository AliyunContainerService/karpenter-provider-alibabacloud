package clients

import "testing"

func TestBuildDescribeVSwitchesRequestMapsSelectorFields(t *testing.T) {
	req := buildDescribeVSwitchesRequest("cn-hangzhou", "vsw-1", map[string]string{"env": "prod"}, "cn-hangzhou-h")

	if req.RegionId == nil || *req.RegionId != "cn-hangzhou" {
		t.Fatalf("expected region to be mapped")
	}
	if req.VSwitchId == nil || *req.VSwitchId != "vsw-1" {
		t.Fatalf("expected VSwitchId to be mapped")
	}
	if req.ZoneId == nil || *req.ZoneId != "cn-hangzhou-h" {
		t.Fatalf("expected ZoneId to be mapped")
	}
	if len(req.Tag) != 1 || req.Tag[0].Key == nil || *req.Tag[0].Key != "env" || req.Tag[0].Value == nil || *req.Tag[0].Value != "prod" {
		t.Fatalf("expected tag filter to be mapped, got %#v", req.Tag)
	}
}

func TestBuildDescribeSecurityGroupsRequestMapsSelectorFields(t *testing.T) {
	req := buildDescribeSecurityGroupsRequest("cn-hangzhou", "sg-1", "app-sg", map[string]string{"env": "prod"})

	if req.SecurityGroupId == nil || *req.SecurityGroupId != "sg-1" {
		t.Fatalf("expected SecurityGroupId to be mapped")
	}
	if req.SecurityGroupName == nil || *req.SecurityGroupName != "app-sg" {
		t.Fatalf("expected SecurityGroupName to be mapped")
	}
	if len(req.Tag) != 1 || req.Tag[0].Key == nil || *req.Tag[0].Key != "env" || req.Tag[0].Value == nil || *req.Tag[0].Value != "prod" {
		t.Fatalf("expected tag filter to be mapped, got %#v", req.Tag)
	}
}

func TestBuildDescribeImagesRequestMapsSelectorFields(t *testing.T) {
	req, err := buildDescribeImagesRequest("cn-hangzhou", nil, map[string]string{
		"ImageFamily":     "aliyun_3",
		"ImageName":       "app-*",
		"ImageOwnerAlias": "self",
		"ImageOwnerID":    "1234567890123456",
		"tag:env":         "prod",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if req.ImageFamily == nil || *req.ImageFamily != "aliyun_3" {
		t.Fatalf("expected ImageFamily to be mapped")
	}
	if req.ImageName == nil || *req.ImageName != "app-*" {
		t.Fatalf("expected ImageName to be mapped")
	}
	if req.ImageOwnerAlias == nil || *req.ImageOwnerAlias != "self" {
		t.Fatalf("expected ImageOwnerAlias to be mapped")
	}
	if req.ImageOwnerId == nil || *req.ImageOwnerId != 1234567890123456 {
		t.Fatalf("expected ImageOwnerId to be mapped")
	}
	if len(req.Tag) != 1 || req.Tag[0].Key == nil || *req.Tag[0].Key != "env" || req.Tag[0].Value == nil || *req.Tag[0].Value != "prod" {
		t.Fatalf("expected image tag filter to be mapped, got %#v", req.Tag)
	}
}

func TestBuildDescribeImagesRequestRejectsInvalidOwnerID(t *testing.T) {
	if _, err := buildDescribeImagesRequest("cn-hangzhou", nil, map[string]string{"ImageOwnerID": "owner-1"}); err == nil {
		t.Fatal("expected invalid owner ID error")
	}
}

func TestBuildDescribeImagesRequestAlwaysSetsShowExpired(t *testing.T) {
	// ShowExpired must always be true to allow querying ContainerOS / LifseaOS
	// images that are hidden by default. See GH issue #13.
	tests := []struct {
		name      string
		imageIDs  []string
		filters   map[string]string
	}{
		{
			name:     "no filters",
			imageIDs: nil,
			filters:  nil,
		},
		{
			name:     "with image ID",
			imageIDs: []string{"lifsea_3_x64_5G_alibase_20260519.qcow2"},
			filters:  nil,
		},
		{
			name:     "with ImageFamily",
			imageIDs: nil,
			filters:  map[string]string{"ImageFamily": "acs:lifsea_os_3_x64"},
		},
		{
			name:     "with multiple filters",
			imageIDs: []string{"m-xxx"},
			filters:  map[string]string{"ImageOwnerAlias": "system", "ImageFamily": "acs:alibaba_cloud_linux_3_2104_lts_x64"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := buildDescribeImagesRequest("cn-hangzhou", tt.imageIDs, tt.filters)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if req.ShowExpired == nil {
				t.Fatal("ShowExpired must be set")
			}
			if *req.ShowExpired != true {
				t.Fatalf("ShowExpired must be true, got %v", *req.ShowExpired)
			}
		})
	}
}
