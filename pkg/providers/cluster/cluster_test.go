package cluster

import (
	"context"
	"testing"

	"github.com/AliyunContainerService/karpenter-provider-alibabacloud/pkg/clients"
	cs "github.com/alibabacloud-go/cs-20151215/v5/client"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// mockCSClient implements clients.CSClient for tests; add DescribeKubernetesVersionMetadata
// stub here after Task 5 adds it to the interface.
type mockCSClient struct {
	mock.Mock
}

var _ clients.CSClient = &mockCSClient{}

func (m *mockCSClient) DescribeClusterAttachScripts(ctx context.Context, clusterID string, request *cs.DescribeClusterAttachScriptsRequest) (string, error) {
	return "", nil
}

func (m *mockCSClient) GetClusterAddonInstance(ctx context.Context, clusterID, addonName string) (*cs.GetClusterAddonInstanceResponse, error) {
	args := m.Called(ctx, clusterID, addonName)
	return args.Get(0).(*cs.GetClusterAddonInstanceResponse), args.Error(1)
}

func (m *mockCSClient) DescribeClusterDetail(ctx context.Context, clusterID string) (*cs.DescribeClusterDetailResponse, error) {
	args := m.Called(ctx, clusterID)
	return args.Get(0).(*cs.DescribeClusterDetailResponse), args.Error(1)
}

// DescribeKubernetesVersionMetadata is added in Task 5; stub it here so the cluster test
// compiles both before and after Task 5. Remove the stub once the real method exists.
func (m *mockCSClient) DescribeKubernetesVersionMetadata(ctx context.Context, k8sVersion string) ([]*cs.DescribeKubernetesVersionMetadataResponseBody, error) {
	return nil, nil
}

func TestInitializeClusterNetworkConfigDualStack(t *testing.T) {
	m := &mockCSClient{}

	// getClusterNetworkAddon tries "terway-eniip" first; return a valid response so it
	// doesn't proceed to the other addons (which are not mocked).
	addonResp := &cs.GetClusterAddonInstanceResponse{
		Body: &cs.GetClusterAddonInstanceResponseBody{
			Name: tea.String("terway-eniip"),
		},
	}
	m.On("GetClusterAddonInstance", mock.Anything, "c-test", "terway-eniip").Return(addonResp, nil)

	// DescribeClusterDetail returns IpStack=ipv6
	detailResp := &cs.DescribeClusterDetailResponse{
		Body: &cs.DescribeClusterDetailResponseBody{
			IpStack:      tea.String("ipv6"),
			NodeCidrMask: tea.String("24"),
		},
	}
	m.On("DescribeClusterDetail", mock.Anything, "c-test").Return(detailResp, nil)

	nc, err := InitializeClusterNetworkConfig(m, "c-test")
	assert.NoError(t, err)
	assert.True(t, nc.DualStack, "DualStack must be true when IpStack=ipv6")
}

func TestInitializeClusterNetworkConfigSingleStack(t *testing.T) {
	m := &mockCSClient{}

	addonResp := &cs.GetClusterAddonInstanceResponse{
		Body: &cs.GetClusterAddonInstanceResponseBody{
			Name: tea.String("terway-eniip"),
		},
	}
	m.On("GetClusterAddonInstance", mock.Anything, "c-test", "terway-eniip").Return(addonResp, nil)

	detailResp := &cs.DescribeClusterDetailResponse{
		Body: &cs.DescribeClusterDetailResponseBody{
			IpStack:      tea.String("ipv4"),
			NodeCidrMask: tea.String("24"),
		},
	}
	m.On("DescribeClusterDetail", mock.Anything, "c-test").Return(detailResp, nil)

	nc, err := InitializeClusterNetworkConfig(m, "c-test")
	assert.NoError(t, err)
	assert.False(t, nc.DualStack, "DualStack must be false when IpStack=ipv4")
}
