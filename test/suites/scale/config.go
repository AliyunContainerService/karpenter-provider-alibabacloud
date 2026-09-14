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

package scale

import (
	"os"
	"strings"
)

var (
	defaultScaleInstanceTypes = []string{"ecs.g7.large", "ecs.g7.xlarge"}
)

func testInstanceTypes() []string {
	return envListOrDefault("TEST_INSTANCE_TYPES", defaultScaleInstanceTypes)
}

func testGPUInstanceTypes() []string {
	return envList("TEST_GPU_INSTANCE_TYPES")
}

func testGPUZones() []string {
	return envList("TEST_GPU_ZONES")
}

func envListOrDefault(key string, defaults []string) []string {
	if values := envList(key); len(values) > 0 {
		return values
	}
	return append([]string(nil), defaults...)
}

func envList(key string) []string {
	raw := os.Getenv(key)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			values = append(values, value)
		}
	}
	return values
}
