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

package cs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/apis/v1alpha1"
	"github.com/stretchr/testify/require"
)

func TestDefaultECSNodeClassUsesRuntimeResourceIDs(t *testing.T) {
	t.Setenv("TEST_VSWITCH_IDS", "vsw-1, vsw-2")
	t.Setenv("TEST_SECURITY_GROUP_IDS", "sg-1")
	t.Setenv("TEST_RAM_ROLE", "KubernetesWorkerRole-test")

	env := &Environment{
		ClusterID:   "c-123",
		ClusterName: "ack-e2e",
	}

	nodeClass := env.DefaultECSNodeClass()

	require.Equal(t, "ack-e2e", nodeClass.Spec.ClusterName)
	require.Len(t, nodeClass.Spec.VSwitchSelectorTerms, 2)
	require.Equal(t, "vsw-1", *nodeClass.Spec.VSwitchSelectorTerms[0].ID)
	require.Equal(t, "vsw-2", *nodeClass.Spec.VSwitchSelectorTerms[1].ID)
	require.Len(t, nodeClass.Spec.SecurityGroupSelectorTerms, 1)
	require.Equal(t, "sg-1", *nodeClass.Spec.SecurityGroupSelectorTerms[0].ID)
	require.Len(t, nodeClass.Spec.ImageSelectorTerms, 1)
	require.Equal(t, "acs:alibaba_cloud_linux_3_2104_x64_container_optimized", *nodeClass.Spec.ImageSelectorTerms[0].ImageFamily)
	require.Nil(t, nodeClass.Spec.ImageSelectorTerms[0].ID)
	require.Equal(t, "KubernetesWorkerRole-test", *nodeClass.Spec.Role)
}

func TestE2EOwnershipTags(t *testing.T) {
	env := &Environment{
		ClusterName: "karpenter-alibabacloud-e2e-tags",
	}

	require.Equal(t, map[string]string{
		"testing/cluster":        "karpenter-alibabacloud-e2e-tags",
		"karpenter.sh/discovery": "karpenter-alibabacloud-e2e-tags",
		v1alpha1.TagManagedBy:    v1alpha1.TagManagedByValue,
	}, env.OwnershipTags())

	require.Equal(t, map[string]string{
		"testing/cluster":        "karpenter-alibabacloud-e2e-tags",
		"karpenter.sh/discovery": "karpenter-alibabacloud-e2e-tags",
		v1alpha1.TagManagedBy:    v1alpha1.TagManagedByValue,
		"testing/type":           "storage",
	}, env.TestTags("storage"))
}

func TestDefaultECSNodeClassAllowsImageIDOverride(t *testing.T) {
	t.Setenv("TEST_IMAGE_ID", "m-123")

	env := &Environment{
		ClusterID:   "c-123",
		ClusterName: "ack-e2e",
	}

	nodeClass := env.DefaultECSNodeClass()

	require.Len(t, nodeClass.Spec.ImageSelectorTerms, 1)
	require.Equal(t, "m-123", *nodeClass.Spec.ImageSelectorTerms[0].ID)
	require.Nil(t, nodeClass.Spec.ImageSelectorTerms[0].ImageFamily)
}

func TestValidateTestClusterTargetRejectsNonE2EClusters(t *testing.T) {
	require.NoError(t, validateTestClusterTarget("c-123", "karpenter-alibabacloud-e2e-20260623-173620"))
	require.ErrorContains(t, validateTestClusterTarget("", "karpenter-alibabacloud-e2e-20260623-173620"), "TEST_CLUSTER_ID")
	require.ErrorContains(t, validateTestClusterTarget("c-123", "production"), "refusing to run")
}

func TestLoadTestConfigFallsBackToDeployConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deploy-config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
alibaba_cloud:
  region_id: cn-shanghai
  access_key_id: ak-from-file
  access_key_secret: sk-from-file
cluster:
  name: e2e
`), 0600))
	t.Setenv("TEST_DEPLOY_CONFIG", path)

	cfg, err := loadTestConfig()

	require.NoError(t, err)
	require.Equal(t, "cn-shanghai", cfg.Region)
	require.Equal(t, "ak-from-file", cfg.AccessKeyID)
	require.Equal(t, "sk-from-file", cfg.AccessKeySecret)
}
