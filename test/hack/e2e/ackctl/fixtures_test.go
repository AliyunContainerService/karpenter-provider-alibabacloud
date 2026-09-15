package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultImageFamilyUsesACKContainerOptimizedImage(t *testing.T) {
	t.Setenv("TEST_IMAGE_FAMILY", "")

	require.Equal(t, "acs:alibaba_cloud_linux_3_2104_x64_container_optimized", defaultImageFamily())
}

func TestRenderFixturesSubstitutesClusterValues(t *testing.T) {
	dir := t.TempDir()
	nodeClassTemplate := filepath.Join(dir, "nodeclass.yaml")
	nodePoolTemplate := filepath.Join(dir, "nodepool.yaml")
	outDir := filepath.Join(dir, "rendered")
	require.NoError(t, os.WriteFile(nodeClassTemplate, []byte(`clusterID: "{{ .ClusterID }}"
clusterName: "{{ .ClusterName }}"
clusterEndpoint: "{{ .ClusterEndpoint }}"
imageFamily: "{{ .ImageFamily }}"
{{ range .VSwitchIDs }}vswitch: "{{ . }}"
{{ end }}{{ range .SecurityGroupIDs }}securityGroup: "{{ . }}"
{{ end }}
role: "{{ .RAMRole }}"
`), 0600))
	require.NoError(t, os.WriteFile(nodePoolTemplate, []byte(`name: "{{ .ClusterName }}"
`), 0600))

	rendered, err := RenderFixtures(nodeClassTemplate, nodePoolTemplate, outDir, FixtureData{
		ClusterID:        "c-123",
		ClusterName:      "ack-e2e",
		ClusterEndpoint:  "https://endpoint",
		ImageFamily:      "acs:alibaba_cloud_linux_3_2104_lts_x64",
		VSwitchIDs:       []string{"vsw-1", "vsw-2"},
		SecurityGroupIDs: []string{"sg-1"},
		RAMRole:          "KubernetesWorkerRole-test",
	})
	require.NoError(t, err)

	nodeClass := string(requireReadFile(t, rendered.NodeClassPath))
	require.Contains(t, nodeClass, `clusterID: "c-123"`)
	require.Contains(t, nodeClass, `clusterEndpoint: "https://endpoint"`)
	require.Contains(t, nodeClass, `imageFamily: "acs:alibaba_cloud_linux_3_2104_lts_x64"`)
	require.Contains(t, nodeClass, `vswitch: "vsw-1"`)
	require.Contains(t, nodeClass, `vswitch: "vsw-2"`)
	require.Contains(t, nodeClass, `securityGroup: "sg-1"`)
	require.Contains(t, nodeClass, `role: "KubernetesWorkerRole-test"`)

	nodePool := string(requireReadFile(t, rendered.NodePoolPath))
	require.Contains(t, nodePool, `name: "ack-e2e"`)
}
