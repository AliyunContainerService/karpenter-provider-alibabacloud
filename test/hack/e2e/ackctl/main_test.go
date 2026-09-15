package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSetupDryRunWritesManifestAndEnv(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "deploy-config.yaml")
	envPath := filepath.Join(dir, "ack.env")
	manifestPath := filepath.Join(dir, "manifest.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
kubeconfig: /tmp/kubeconfig
alibaba_cloud:
  access_key_id: ak
  access_key_secret: sk
  region_id: cn-shanghai
cluster:
  name: ack-e2e
  cluster_type: ManagedKubernetes
  endpoint_public_access: true
  node_pool:
    instance_types: ["ecs.g6.xlarge"]
    desired_size: 2
autoscaling:
  enabled: false
`), 0600))

	err := run([]string{
		"setup",
		"--config", configPath,
		"--cluster-name", "ack-e2e-fixed",
		"--git-ref", "abc123",
		"--output-env", envPath,
		"--manifest", manifestPath,
		"--dry-run",
	})
	require.NoError(t, err)

	envData := string(requireReadFile(t, envPath))
	require.Contains(t, envData, "export TEST_REGION='cn-shanghai'")
	require.Contains(t, envData, "export TEST_CLUSTER_NAME='karpenter-alibabacloud-e2e-ack-e2e-fixed'")
	require.NotContains(t, envData, "sk")

	manifest, err := LoadManifest(manifestPath)
	require.NoError(t, err)
	require.Equal(t, "karpenter-alibabacloud-e2e-ack-e2e-fixed", manifest.ClusterName)
	require.Equal(t, "cn-shanghai", manifest.Region)
	require.Equal(t, "abc123", manifest.GitRef)
	require.Equal(t, "e2e", manifest.OwnershipTags["testing/type"])
}

func TestEnvValuesIncludesCapacityReservationID(t *testing.T) {
	values := envValues(&Config{}, &Manifest{}, "/tmp/deploy.yaml", "https://example.com", nil, ClusterResources{
		CapacityReservationID: "crp-123456",
	})

	require.Equal(t, "crp-123456", values["TEST_CAPACITY_RESERVATION_ID"])
	require.Equal(t, "ecs.c9i.large", values["TEST_CAPACITY_RESERVATION_INSTANCE_TYPE"])
}

func TestEnvValuesUsesActualCapacityReservationInstanceType(t *testing.T) {
	values := envValues(&Config{}, &Manifest{}, "/tmp/deploy.yaml", "https://example.com", nil, ClusterResources{
		CapacityReservationID:           "crp-123456",
		CapacityReservationInstanceType: "ecs.g6.xlarge",
	})

	require.Equal(t, "crp-123456", values["TEST_CAPACITY_RESERVATION_ID"])
	require.Equal(t, "ecs.g6.xlarge", values["TEST_CAPACITY_RESERVATION_INSTANCE_TYPE"])
}

func TestUpdateEnvFileValuesPreservesExistingValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ack.env")
	require.NoError(t, os.WriteFile(path, []byte("export A='1'\nexport TEST_CAPACITY_RESERVATION_ID='old'\n"), 0600))

	require.NoError(t, updateEnvFileValues(path, map[string]string{
		"TEST_CAPACITY_RESERVATION_ID":            "new'id",
		"TEST_CAPACITY_RESERVATION_INSTANCE_TYPE": "ecs.c9i.large",
	}))

	data := string(requireReadFile(t, path))
	require.Contains(t, data, "export A='1'\n")
	require.Contains(t, data, "export TEST_CAPACITY_RESERVATION_ID='new'\"'\"'id'\n")
	require.Contains(t, data, "export TEST_CAPACITY_RESERVATION_INSTANCE_TYPE='ecs.c9i.large'\n")
	require.NotContains(t, data, "old")
}

func TestResolveE2EClusterNameAddsRequiredPrefix(t *testing.T) {
	require.Equal(t, "karpenter-alibabacloud-e2e-scale-123", ResolveE2EClusterName("Scale_123"))
	require.Equal(t, "karpenter-alibabacloud-e2e-existing", ResolveE2EClusterName("karpenter-alibabacloud-e2e-existing"))
}

func TestReusableSetupManifestClusterID(t *testing.T) {
	require.Equal(t, "c-123", reusableSetupManifestClusterID(&Manifest{
		ClusterID: "c-123",
		Resources: []Resource{{
			Type:  "ack-cluster",
			ID:    "c-123",
			State: ResourceStatePending,
		}},
	}))
	require.Empty(t, reusableSetupManifestClusterID(&Manifest{
		ClusterID: "c-123",
		Resources: []Resource{{
			Type:  "ack-cluster",
			ID:    "c-123",
			State: ResourceStateDeleted,
		}},
	}))
	require.Empty(t, reusableSetupManifestClusterID(&Manifest{ClusterID: "c-123"}))
}

func TestEnsureCapacityReservationCommandRequiresInputs(t *testing.T) {
	require.ErrorContains(t, run([]string{"ensure-capacity-reservation"}), "--config")
}

func TestAllowsAllIPv4(t *testing.T) {
	require.True(t, allowsAllIPv4([]string{"117.157.14.17/32", "0.0.0.0/0"}))
	require.True(t, allowsAllIPv4([]string{" 0.0.0.0/0 "}))
	require.False(t, allowsAllIPv4([]string{"117.157.14.17/32", "117.157.14.0/24"}))
}

func TestHelmInstallControllerArgsAllowsImageOverride(t *testing.T) {
	t.Setenv("E2E_CONTROLLER_IMAGE_REPOSITORY", "registry.example.com/e2e/controller")
	t.Setenv("E2E_CONTROLLER_IMAGE_TAG", "sha123")

	t.Setenv("E2E_CONTROLLER_IMAGE_PULL_CONFIG", "/tmp/dockerconfig.json")
	t.Setenv("E2E_CONTROLLER_IMAGE_PULL_SECRET_NAME", "custom-pull")

	args := helmInstallControllerArgs(&Config{
		AlibabaCloud: CloudConfig{RegionID: "cn-shanghai"},
	}, "c-test", "https://example.com", "/repo/charts/karpenter", "custom-pull")

	require.Contains(t, args, "controller.image.repository=registry.example.com/e2e/controller")
	require.Contains(t, args, "controller.image.tag=sha123")
	require.Contains(t, args, "imagePullSecrets[0].name=custom-pull")
	require.Contains(t, args, "settings.clusterID=c-test")
	require.Contains(t, args, "settings.region=cn-shanghai")
}

func TestValidateSuiteConfigAllowsScaleWithoutGPUDiscovery(t *testing.T) {
	require.NoError(t, validateSuiteConfig(&Config{}, "Scale", false))
	require.NoError(t, validateSuiteConfig(&Config{}, "Scale", true))
	require.NoError(t, validateSuiteConfig(&Config{}, "Integration", false))
}

func TestRenderConfigCommandOverridesRuntimeFields(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "deploy-config.yaml")
	renderedPath := filepath.Join(dir, "rendered.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
kubeconfig: /tmp/kubeconfig
alibaba_cloud:
  access_key_id: ak
  access_key_secret: sk
  region_id: cn-shanghai
cluster:
  name: ack-e2e
`), 0600))

	err := run([]string{
		"render-config",
		"--config", configPath,
		"--output", renderedPath,
		"--region", "cn-hangzhou",
		"--cluster-name", "ack-e2e-runtime",
		"--kubeconfig", filepath.Join(dir, "kubeconfig"),
		"--k8s-version", "1.32",
	})
	require.NoError(t, err)

	rendered, err := LoadConfig(renderedPath)
	require.NoError(t, err)
	require.Equal(t, "cn-hangzhou", rendered.AlibabaCloud.RegionID)
	require.Equal(t, "ack-e2e-runtime", rendered.Cluster.Name)
	require.Equal(t, filepath.Join(dir, "kubeconfig"), rendered.Kubeconfig)
	require.Equal(t, "1.32", rendered.Cluster.KubernetesVersion)
}

func TestDumpWritesManifestSummary(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.yaml")
	outputDir := filepath.Join(dir, "dump")
	require.NoError(t, SaveManifest(manifestPath, &Manifest{
		ClusterName: "ack-e2e",
		ClusterID:   "c-123",
		Region:      "cn-hangzhou",
		Resources: []Resource{{
			Type:  "ack-cluster",
			ID:    "c-123",
			State: ResourceStatePending,
		}},
	}))

	err := run([]string{
		"dump",
		"--manifest", manifestPath,
		"--output-dir", outputDir,
	})

	require.NoError(t, err)
	summary := string(requireReadFile(t, filepath.Join(outputDir, "manifest-summary.txt")))
	require.Contains(t, summary, "clusterName: ack-e2e")
	require.Contains(t, summary, "clusterID: c-123")
	require.Contains(t, summary, "ack-cluster/c-123")
}

func TestEnvValuesUsesDiscoveredWorkerImageID(t *testing.T) {
	cfg := &Config{
		AlibabaCloud: CloudConfig{RegionID: "cn-shanghai"},
	}
	manifest := &Manifest{
		ClusterID:      "c-test",
		ClusterName:    "ack-e2e",
		KubeconfigPath: "/tmp/kubeconfig",
	}

	values := envValues(cfg, manifest, "deploy.yaml", "https://example", nil, ClusterResources{
		WorkerImageID: "aliyun_3_x64_20G_container_optimized_alibase_20260513.vhd",
	})

	require.Equal(t, absolutePath("deploy.yaml"), values["TEST_DEPLOY_CONFIG"])
	require.Equal(t, "aliyun_3_x64_20G_container_optimized_alibase_20260513.vhd", values["TEST_IMAGE_ID"])
	require.NotContains(t, values, "TEST_IMAGE_FAMILY")
}

func TestEnvValuesWritesAbsolutePaths(t *testing.T) {
	cfg := &Config{AlibabaCloud: CloudConfig{RegionID: "cn-shanghai"}}
	manifest := &Manifest{
		ClusterID:      "c-test",
		ClusterName:    "ack-e2e",
		KubeconfigPath: ".e2e/ack/kubeconfig",
	}

	values := envValues(cfg, manifest, "deploy.yaml", "https://example", &RenderedFixtures{
		NodeClassPath: ".e2e/ack/fixtures/default_ecsnodeclass.yaml",
		NodePoolPath:  ".e2e/ack/fixtures/default_nodepool.yaml",
	}, ClusterResources{})

	require.True(t, filepath.IsAbs(values["KUBECONFIG"]))
	require.True(t, filepath.IsAbs(values["DEFAULT_NODECLASS"]))
	require.True(t, filepath.IsAbs(values["DEFAULT_NODEPOOL"]))
}

func TestRefreshKubeconfigOutputPath(t *testing.T) {
	require.Equal(t, "/tmp/explicit", refreshKubeconfigOutputPath(
		&Config{Kubeconfig: "/tmp/config"},
		&Manifest{ClusterName: "ack-e2e"},
		"/tmp/explicit",
	))
	require.Equal(t, "/tmp/config", refreshKubeconfigOutputPath(
		&Config{Kubeconfig: "/tmp/config"},
		&Manifest{ClusterName: "ack-e2e"},
		"",
	))
	require.Equal(t, filepath.Join(".e2e", "ack-e2e", "kubeconfig"), refreshKubeconfigOutputPath(
		&Config{},
		&Manifest{ClusterName: "ack-e2e"},
		"",
	))
}

func TestEnvValuesWritesDiscoveredZones(t *testing.T) {
	cfg := &Config{
		AlibabaCloud: CloudConfig{RegionID: "cn-hangzhou"},
	}
	manifest := &Manifest{
		ClusterID:      "c-test",
		ClusterName:    "ack-e2e",
		KubeconfigPath: "/tmp/kubeconfig",
	}

	values := envValues(cfg, manifest, "deploy.yaml", "https://example", nil, ClusterResources{
		VSwitchIDs: []string{"vsw-1"},
		Zones:      []string{"cn-hangzhou-i"},
	})

	require.Equal(t, "vsw-1", values["TEST_VSWITCH_IDS"])
	require.Equal(t, "cn-hangzhou-i", values["TEST_ZONES"])
}

func TestEnvValuesWritesDiscoveredGPUCapacity(t *testing.T) {
	cfg := &Config{
		AlibabaCloud: CloudConfig{RegionID: "cn-hangzhou"},
	}
	manifest := &Manifest{
		ClusterID:      "c-test",
		ClusterName:    "ack-e2e",
		KubeconfigPath: "/tmp/kubeconfig",
	}

	values := envValues(cfg, manifest, "deploy.yaml", "https://example", nil, ClusterResources{
		GPUInstanceTypes: []string{"ecs.gn6v-c8g1.2xlarge"},
		GPUZones:         []string{"cn-hangzhou-i"},
	})

	require.Equal(t, "ecs.gn6v-c8g1.2xlarge", values["TEST_GPU_INSTANCE_TYPES"])
	require.Equal(t, "cn-hangzhou-i", values["TEST_GPU_ZONES"])
}

func TestEnvValuesWritesIPv6Family(t *testing.T) {
	cfg := &Config{
		AlibabaCloud: CloudConfig{RegionID: "cn-hangzhou"},
		Cluster:      ClusterConfig{IPStack: "ipv6"},
	}
	manifest := &Manifest{
		ClusterID:      "c-test",
		ClusterName:    "ack-e2e",
		KubeconfigPath: "/tmp/kubeconfig",
	}

	values := envValues(cfg, manifest, "deploy.yaml", "https://example", nil, ClusterResources{})

	require.Equal(t, "ipv6", values["TEST_IP_FAMILY"])
}

func TestValidateSuiteConfigRequiresIPv6StackForIPv6Suite(t *testing.T) {
	require.ErrorContains(t, validateSuiteConfig(&Config{}, "ipv6", false), "cluster.ip_stack=ipv6")
	require.NoError(t, validateSuiteConfig(&Config{Cluster: ClusterConfig{IPStack: "ipv6"}}, "IPv6", false))
	require.NoError(t, validateSuiteConfig(&Config{}, "scale", true))
	require.NoError(t, validateSuiteConfig(&Config{}, "scale", false))
}

func TestGPUDiscoveryOptionsSearchesAllRegionsByDefault(t *testing.T) {
	cfg := &Config{
		AlibabaCloud: CloudConfig{RegionID: "cn-shanghai"},
	}

	opts := gpuDiscoveryOptions(cfg)

	require.Empty(t, opts.Regions)
	require.Contains(t, opts.InstanceTypes, "ecs.gn6v-c8g1.2xlarge")
}

func TestRecordACKClusterManifestPersistsClusterIDImmediately(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "manifest.yaml")
	manifest := newManifest(&Config{
		AlibabaCloud: CloudConfig{RegionID: "cn-shanghai"},
	}, "ack-e2e", "abc123", "scale", "alibabacloud", "/tmp/kubeconfig")

	require.NoError(t, recordACKClusterManifest(manifest, manifestPath, "c-123", "ack-e2e", ""))

	loaded, err := LoadManifest(manifestPath)
	require.NoError(t, err)
	require.Equal(t, "c-123", loaded.ClusterID)
	require.Len(t, loaded.Resources, 1)
	require.Equal(t, "ack-cluster", loaded.Resources[0].Type)
	require.Equal(t, ResourceStatePending, loaded.Resources[0].State)
}

func TestCleanupRequiresOwnedManifestCluster(t *testing.T) {
	cfg := &Config{AlibabaCloud: CloudConfig{ClusterID: "c-config"}}
	_, err := cleanupClusterID(cfg, &Manifest{})
	require.ErrorContains(t, err, "manifest cluster id is required")

	_, err = cleanupClusterID(cfg, &Manifest{ClusterID: "c-123"})
	require.ErrorContains(t, err, "owned ack-cluster resource")

	clusterID, err := cleanupClusterID(cfg, &Manifest{
		ClusterID: "c-123",
		Resources: []Resource{{
			Type:  "ack-cluster",
			ID:    "c-123",
			State: ResourceStatePending,
		}},
	})
	require.NoError(t, err)
	require.Equal(t, "c-123", clusterID)
}

func TestCleanupClusterIDAlreadyDeletedIsNoop(t *testing.T) {
	clusterID, err := cleanupClusterID(&Config{}, &Manifest{
		ClusterID: "c-123",
		Resources: []Resource{{
			Type:  "ack-cluster",
			ID:    "c-123",
			State: ResourceStateDeleted,
		}},
	})

	require.NoError(t, err)
	require.Empty(t, clusterID)
}

func TestIsClusterNotFoundRecognizesACKDelete404(t *testing.T) {
	require.True(t, isClusterNotFound(errors.New("SDKError: Code: ErrorClusterNotFound Message: cluster (c-123) not found in our records")))
}

func TestIsLimitedEndTimeCapacityReservation(t *testing.T) {
	require.True(t, isLimitedEndTimeCapacityReservation(errors.New("Code: Invalid.Action.ReleaseCapacityReservation")))
	require.False(t, isLimitedEndTimeCapacityReservation(errors.New("other error")))
}

func TestShouldSkipCapacityReservationFromEnv(t *testing.T) {
	require.False(t, shouldSkipCapacityReservation())

	t.Setenv("TEST_SKIP_CAPACITY_RESERVATION", "true")
	require.True(t, shouldSkipCapacityReservation())
}

func TestApplyManifestRuntimeUsesActualClusterRegion(t *testing.T) {
	cfg := &Config{
		AlibabaCloud: CloudConfig{RegionID: "cn-shanghai"},
		Kubeconfig:   "/tmp/config-kubeconfig",
	}

	applyManifestRuntime(cfg, &Manifest{
		Region:         "cn-qingdao",
		KubeconfigPath: "/tmp/manifest-kubeconfig",
	})

	require.Equal(t, "cn-qingdao", cfg.AlibabaCloud.RegionID)
	require.Equal(t, "/tmp/manifest-kubeconfig", cfg.Kubeconfig)
}

func TestKubeCommandEnvDisablesProxy(t *testing.T) {
	env := kubeCommandEnv([]string{
		"HTTPS_PROXY=socks5://127.0.0.1:5003",
		"HTTP_PROXY=socks5://127.0.0.1:5003",
		"ALL_PROXY=socks5://127.0.0.1:5003",
		"NO_PROXY=old",
		"PATH=/bin",
	}, "/tmp/kubeconfig")

	require.Contains(t, env, "KUBECONFIG=/tmp/kubeconfig")
	require.Contains(t, env, "NO_PROXY=*")
	require.Contains(t, env, "HTTPS_PROXY=")
	require.Contains(t, env, "HTTP_PROXY=")
	require.Contains(t, env, "ALL_PROXY=")
	require.Contains(t, env, "PATH=/bin")
}
