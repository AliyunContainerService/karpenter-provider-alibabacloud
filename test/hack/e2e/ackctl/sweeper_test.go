package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSweepSelectorsIncludeOwnershipTags(t *testing.T) {
	manifest := &Manifest{
		ClusterName: "karpenter-alibabacloud-e2e-test",
		OwnershipTags: map[string]string{
			"testing/cluster":        "karpenter-alibabacloud-e2e-test",
			"karpenter.sh/discovery": "karpenter-alibabacloud-e2e-test",
		},
	}

	selectors := sweepTagSelectors(manifest)

	require.Equal(t, []map[string]string{
		{"karpenter.sh/discovery": "karpenter-alibabacloud-e2e-test", "testing/cluster": "karpenter-alibabacloud-e2e-test"},
		{"karpenter.sh/managed-by": "true", "testing/cluster": "karpenter-alibabacloud-e2e-test"},
		{"karpenter.sh/managed-by": "karpenter", "testing/cluster": "karpenter-alibabacloud-e2e-test"},
	}, selectors)
}

func TestSweepReportResidueError(t *testing.T) {
	report := SweepReport{
		Resources: []SweepResource{
			{Type: "ecs-instance", ID: "i-1"},
			{Type: "disk", ID: "d-1"},
		},
	}

	require.True(t, report.HasResidue())
	require.ErrorContains(t, report.ResidueError(), "ecs-instance/i-1")
	require.ErrorContains(t, report.ResidueError(), "disk/d-1")
}

func TestParseSweepSelectorFlags(t *testing.T) {
	selectors, err := parseSweepSelectorFlags([]string{
		"karpenter.sh/managed-by=true",
		"karpenter.sh/discovery=karpenter-alibabacloud-e2e-test",
	})

	require.NoError(t, err)
	require.Equal(t, []map[string]string{{
		"karpenter.sh/managed-by": "true",
		"karpenter.sh/discovery":  "karpenter-alibabacloud-e2e-test",
	}}, selectors)
}

func TestParseSweepSelectorFlagsRejectsMalformedSelector(t *testing.T) {
	_, err := parseSweepSelectorFlags([]string{"karpenter.sh/managed-by"})

	require.ErrorContains(t, err, "key=value")
}

func TestSweepReportNoResidue(t *testing.T) {
	report := SweepReport{}

	require.False(t, report.HasResidue())
	require.NoError(t, report.ResidueError())
}

func TestAppendSweepResourceDeduplicates(t *testing.T) {
	var report SweepReport

	report.Append("ecs-instance", "i-1", "running")
	report.Append("ecs-instance", "i-1", "running")
	report.Append("disk", "d-1", "available")

	require.Equal(t, []SweepResource{
		{Type: "ecs-instance", ID: "i-1", Status: "running"},
		{Type: "disk", ID: "d-1", Status: "available"},
	}, report.Resources)
}

func TestClassicLoadBalancerTags(t *testing.T) {
	selector := map[string]string{
		"testing/cluster":        "ack-e2e",
		"karpenter.sh/discovery": "ack-e2e",
	}

	require.Len(t, slbLoadBalancerTags(selector), 2)
	require.Len(t, albLoadBalancerTags(selector), 2)
	require.Len(t, nlbLoadBalancerTags(selector), 2)
}

func TestECSLaunchTemplateTags(t *testing.T) {
	selector := map[string]string{
		"testing/cluster":        "ack-e2e",
		"karpenter.sh/discovery": "ack-e2e",
	}

	tags := ecsLaunchTemplateTags(selector)

	require.Len(t, tags, 2)
	require.Equal(t, "testing/cluster", *tags[0].Key)
	require.Equal(t, "ack-e2e", *tags[0].Value)
}
