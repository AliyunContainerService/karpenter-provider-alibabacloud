package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestManifestRoundTripAndRetryableResources(t *testing.T) {
	manifest := &Manifest{
		ClusterName: "ack-e2e",
		ClusterID:   "c-123",
		Region:      "cn-shanghai",
		GitRef:      "abc123",
		OwnershipTags: map[string]string{
			"testing/type":    "e2e",
			"testing/cluster": "ack-e2e",
		},
		Resources: []Resource{
			{Type: "ecs-instance", ID: "i-1", State: ResourceStatePending, SupportsTags: true},
			{Type: "vswitch", ID: "vsw-1", State: ResourceStateDeleted, SupportsTags: true},
			{Type: "ram-role", ID: "role-1", State: ResourceStateFailed, SupportsTags: false},
			{Type: "sls", ID: "sls-1", State: ResourceStateSkipped, SupportsTags: false},
		},
	}
	path := filepath.Join(t.TempDir(), "manifest.yaml")

	require.NoError(t, SaveManifest(path, manifest))
	loaded, err := LoadManifest(path)
	require.NoError(t, err)
	require.Equal(t, manifest.ClusterName, loaded.ClusterName)
	require.Equal(t, manifest.OwnershipTags, loaded.OwnershipTags)

	retryable := loaded.RetryableResources()
	require.Len(t, retryable, 2)
	require.Equal(t, "i-1", retryable[0].ID)
	require.Equal(t, "role-1", retryable[1].ID)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(data), "access_key")
	require.NotContains(t, string(data), "secret")
}
