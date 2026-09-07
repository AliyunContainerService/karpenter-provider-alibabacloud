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
