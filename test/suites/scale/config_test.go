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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScaleConfigDefaults(t *testing.T) {
	t.Setenv("TEST_INSTANCE_TYPES", "")
	t.Setenv("TEST_GPU_INSTANCE_TYPES", "")
	t.Setenv("TEST_GPU_ZONES", "")
	t.Setenv("TEST_ZONES", "")

	require.Equal(t, []string{"ecs.c9i.large", "ecs.c9i.xlarge"}, testInstanceTypes())
	require.Empty(t, testGPUInstanceTypes())
	require.Empty(t, testGPUZones())
}

func TestScaleConfigUsesEnvironmentOverrides(t *testing.T) {
	t.Setenv("TEST_INSTANCE_TYPES", "ecs.c9i.large, ecs.c9i.xlarge")
	t.Setenv("TEST_GPU_INSTANCE_TYPES", "ecs.gn6i-c4g1.xlarge")
	t.Setenv("TEST_GPU_ZONES", "cn-hangzhou-i")

	require.Equal(t, []string{"ecs.c9i.large", "ecs.c9i.xlarge"}, testInstanceTypes())
	require.Equal(t, []string{"ecs.gn6i-c4g1.xlarge"}, testGPUInstanceTypes())
	require.Equal(t, []string{"cn-hangzhou-i"}, testGPUZones())
}

func TestScaleConfigDoesNotUseNonGPUZonesForGPU(t *testing.T) {
	t.Setenv("TEST_ZONES", "cn-hangzhou-i, cn-hangzhou-k")
	t.Setenv("TEST_GPU_ZONES", "")

	require.Empty(t, testGPUZones())
}
