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
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestECSNodeClassCRDTagSchemaBounds(t *testing.T) {
	paths := []string{
		"../../../pkg/apis/crds/karpenter.alibabacloud.com_ecsnodeclasses.yaml",
		"../../../charts/karpenter/crds/karpenter.alibabacloud.com_ecsnodeclasses.yaml",
	}

	var previousCRD []byte
	for _, crdPath := range paths {
		t.Run(crdPath, func(t *testing.T) {
			crd := loadCRDSchema(t, crdPath)
			if previousCRD != nil && !reflect.DeepEqual(previousCRD, mustReadFile(t, crdPath)) {
				t.Fatalf("%s differs from %s", crdPath, paths[0])
			}
			previousCRD = mustReadFile(t, crdPath)

			// Selector terms 只检查 maxProperties，不检查 value maxLength
			// (kubebuilder marker 不支持给 map value 加约束)
			assertTagSchemaBounds(t, crd, []string{"imageSelectorTerms", "items", "properties", "tags"}, 20, false)
			assertTagSchemaBounds(t, crd, []string{"securityGroupSelectorTerms", "items", "properties", "tags"}, 20, false)
			assertTagSchemaBounds(t, crd, []string{"vSwitchSelectorTerms", "items", "properties", "tags"}, 20, false)

			// 顶级 tags 字段只检查 maxProperties (kubebuilder 不支持 map value maxLength)
			assertTagSchemaBounds(t, crd, []string{"tags"}, 64, false)
		})
	}
}

func TestECSNodeClassCRDDiskSchema(t *testing.T) {
	paths := []string{
		"../../../pkg/apis/crds/karpenter.alibabacloud.com_ecsnodeclasses.yaml",
		"../../../charts/karpenter/crds/karpenter.alibabacloud.com_ecsnodeclasses.yaml",
	}
	wantCategories := []any{
		"cloud_essd", "cloud_essd_entry", "cloud_efficiency", "cloud_pperf", "cloud_sperf", "cloud_ssd",
		"cloud_auto", "ephemeral_ssd", "cloud", "cloud_essd_xc0", "cloud_essd_xc1",
		"elastic_ephemeral_disk_premium", "elastic_ephemeral_disk_standard",
	}
	wantPerformanceLevels := []any{"PL0", "PL1", "PL2", "PL3"}

	for _, crdPath := range paths {
		t.Run(crdPath, func(t *testing.T) {
			crd := loadCRDSchema(t, crdPath)
			for _, disk := range []struct {
				name string
				path []string
			}{
				{name: "systemDisk", path: []string{"systemDisk"}},
				{name: "dataDisks", path: []string{"dataDisks", "items"}},
			} {
				t.Run(disk.name, func(t *testing.T) {
					diskSchema := jsonPath(t, crd, append([]string{
						"spec", "versions", "0", "schema", "openAPIV3Schema", "properties", "spec", "properties",
					}, disk.path...))
					properties, ok := diskSchema["properties"].(map[string]any)
					if !ok {
						t.Fatalf("%s properties missing or invalid", disk.name)
					}
					categorySchema := properties["category"].(map[string]any)
					if got := categorySchema["enum"]; !reflect.DeepEqual(got, wantCategories) {
						t.Fatalf("%s category enum = %#v, want %#v", disk.name, got, wantCategories)
					}
					performanceSchema := properties["performanceLevel"].(map[string]any)
					if _, ok := performanceSchema["default"]; ok {
						t.Fatalf("%s performanceLevel must not have an unconditional default", disk.name)
					}
					if got := performanceSchema["enum"]; !reflect.DeepEqual(got, wantPerformanceLevels) {
						t.Fatalf("%s performanceLevel enum = %#v, want %#v", disk.name, got, wantPerformanceLevels)
					}

					validations, ok := diskSchema["x-kubernetes-validations"].([]any)
					if !ok {
						t.Fatalf("%s CEL validations missing or invalid", disk.name)
					}
					var rules []string
					for _, validation := range validations {
						rules = append(rules, validation.(map[string]any)["rule"].(string))
					}
					joinedRules := strings.Join(rules, "\n")
					for _, expected := range []string{
						"self.category != 'cloud_essd_xc1'",
						"self.category != 'cloud_essd' || (self.size >= 20 && self.size <= 32768)",
						"self.category != 'cloud_essd_entry' || (self.size >= 20 && self.size <= 32768)",
						"self.category != 'cloud_efficiency' || (self.size >= 20 && self.size <= 32768)",
						"self.category != 'cloud_pperf' || (self.size >= 20 && self.size <= 32768)",
						"self.category != 'cloud_sperf' || (self.size >= 20 && self.size <= 32768)",
						"self.category != 'cloud_ssd' || (self.size >= 20 && self.size <= 32768)",
						"self.category != 'cloud_auto' || (self.size >= 40 && self.size <= 32768)",
						"self.category != 'ephemeral_ssd' || (self.size >= 5 && self.size <= 800)",
						"self.category != 'cloud' || (self.size >= 5 && self.size <= 2000)",
						"self.category != 'cloud_essd_xc0' || (self.size >= 40 && self.size <= 2048)",
						"self.category != 'elastic_ephemeral_disk_premium' || (self.size >= 64 && self.size <= 8192)",
						"self.category != 'elastic_ephemeral_disk_standard' || (self.size >= 64 && self.size <= 8192)",
						"!has(self.performanceLevel) || self.category == 'cloud_essd'",
					} {
						if !strings.Contains(joinedRules, expected) {
							t.Fatalf("%s CEL rules do not contain %q: %s", disk.name, expected, joinedRules)
						}
					}
				})
			}
		})
	}
}

func assertTagSchemaBounds(t *testing.T, crd map[string]any, relativePath []string, maxProperties float64, checkMaxLength bool) {
	t.Helper()

	path := append([]string{
		"spec", "versions", "0", "schema", "openAPIV3Schema", "properties", "spec", "properties",
	}, relativePath...)
	tagSchema := jsonPath(t, crd, path)

	if got := tagSchema["maxProperties"]; got != maxProperties {
		t.Fatalf("%s maxProperties = %v, want %v", relativePath[len(relativePath)-1], got, maxProperties)
	}

	if checkMaxLength {
		additionalProperties, ok := tagSchema["additionalProperties"].(map[string]any)
		if !ok {
			t.Fatalf("%v additionalProperties missing or invalid", relativePath)
		}
		if got := additionalProperties["maxLength"]; got != float64(256) {
			t.Fatalf("%v additionalProperties.maxLength = %v, want 256", relativePath, got)
		}
	}
}

func loadCRDSchema(t *testing.T, crdPath string) map[string]any {
	t.Helper()

	jsonBytes, err := yaml.YAMLToJSON(mustReadFile(t, crdPath))
	if err != nil {
		t.Fatalf("parsing %s as yaml: %v", crdPath, err)
	}
	var crd map[string]any
	if err := json.Unmarshal(jsonBytes, &crd); err != nil {
		t.Fatalf("parsing %s as json: %v", crdPath, err)
	}
	return crd
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return content
}

func jsonPath(t *testing.T, value map[string]any, path []string) map[string]any {
	t.Helper()

	current := any(value)
	for _, segment := range path {
		switch typed := current.(type) {
		case map[string]any:
			var ok bool
			current, ok = typed[segment]
			if !ok {
				t.Fatalf("missing path segment %q in %v", segment, path)
			}
		case []any:
			if segment != "0" {
				t.Fatalf("unsupported array segment %q in %v", segment, path)
			}
			if len(typed) == 0 {
				t.Fatalf("empty array at %v", path)
			}
			current = typed[0]
		default:
			t.Fatalf("unexpected value %T at segment %q in %v", current, segment, path)
		}
	}

	result, ok := current.(map[string]any)
	if !ok {
		t.Fatalf("path %v resolved to %T, want object", path, current)
	}
	return result
}
