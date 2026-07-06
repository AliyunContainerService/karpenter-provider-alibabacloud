package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type ExpectedArtifact struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type Report struct {
	ExpectedSuites    []ExpectedArtifact `json:"expectedSuites"`
	MissingSuites     []ExpectedArtifact `json:"missingSuites"`
	SemanticGaps      []SemanticGap      `json:"semanticGaps"`
	ExpectedWorkflows []ExpectedArtifact `json:"expectedWorkflows"`
	MissingWorkflows  []ExpectedArtifact `json:"missingWorkflows"`
	ExpectedActions   []ExpectedArtifact `json:"expectedActions"`
	MissingActions    []ExpectedArtifact `json:"missingActions"`
}

type ExpectedSuiteCoverage struct {
	Name           string
	Path           string
	MinSpecs       int
	RequiredTopics []string
}

type SemanticGap struct {
	Suite   string `json:"suite"`
	Reason  string `json:"reason"`
	Current int    `json:"current,omitempty"`
	Want    int    `json:"want,omitempty"`
	Pattern string `json:"pattern,omitempty"`
}

var ExpectedSuites = []ExpectedArtifact{
	{Name: "ami", Path: "test/suites/ami"},
	{Name: "consolidation", Path: "test/suites/consolidation"},
	{Name: "drift", Path: "test/suites/drift"},
	{Name: "integration", Path: "test/suites/integration"},
	{Name: "interruption", Path: "test/suites/interruption"},
	{Name: "ipv6", Path: "test/suites/ipv6"},
	{Name: "localzone", Path: "test/suites/localzone"},
	{Name: "nodeclaim", Path: "test/suites/nodeclaim"},
	{Name: "scale", Path: "test/suites/scale"},
	{Name: "scheduling", Path: "test/suites/scheduling"},
	{Name: "storage", Path: "test/suites/storage"},
}

var ExpectedSuiteCoverageByName = map[string]ExpectedSuiteCoverage{
	"ami": {
		Name:     "ami",
		Path:     "test/suites/ami",
		MinSpecs: 17,
		RequiredTopics: []string{
			"image selected by id|image-id|ami selector ids",
			"image family|alias|most recent",
			"userdata|user data",
			"status.*image|image.*status",
			"not ready|not resolved|validation",
		},
	},
	"consolidation": {
		Name:     "consolidation",
		Path:     "test/suites/consolidation",
		MinSpecs: 13,
		RequiredTopics: []string{
			"empty",
			"underutilized|utilization",
			"budget",
			"replace",
			"spot",
			"reserved|reservation|capacity reservation",
		},
	},
	"drift": {
		Name:     "drift",
		Path:     "test/suites/drift",
		MinSpecs: 11,
		RequiredTopics: []string{
			"image|ami",
			"security.?group",
			"subnet|vswitch",
			"role|instance.?profile|ram",
			"block.?device|disk",
			"hash",
			"reservation|capacity reservation",
		},
	},
	"integration": {
		Name:     "integration",
		Path:     "test/suites/integration",
		MinSpecs: 58,
		RequiredTopics: []string{
			"metadata",
			"block.?device|data.?disk|disk",
			"cni|terway|maxpods",
			"extended.?resources|gpu|accelerator",
			"hash",
			"instance.?profile|ram.?role|role",
			"kubelet",
			"launch.?template",
			"metrics",
			"network.?interface|eni",
			"nodeclass",
			"repair",
			"security.?group",
			"subnet|vswitch",
			"tags?",
			"validation|validating",
		},
	},
	"interruption": {
		Name:     "interruption",
		Path:     "test/suites/interruption",
		MinSpecs: 4,
		RequiredTopics: []string{
			"spot",
			"stopped|terminated|delete",
			"scheduled.?change|health.?event|event",
			"replacement|recover",
		},
	},
	"ipv6": {
		Name:     "ipv6",
		Path:     "test/suites/ipv6",
		MinSpecs: 3,
		RequiredTopics: []string{
			"ipv6",
			"dns",
			"prefix|primary",
		},
	},
	"localzone": {
		Name:     "localzone",
		Path:     "test/suites/localzone",
		MinSpecs: 1,
		RequiredTopics: []string{
			"local.?zone|zone",
		},
	},
	"nodeclaim": {
		Name:     "nodeclaim",
		Path:     "test/suites/nodeclaim",
		MinSpecs: 1,
		RequiredTopics: []string{
			"garbage.?collect|deleted without the cluster",
		},
	},
	"scale": {
		Name:     "scale",
		Path:     "test/suites/scale",
		MinSpecs: 12,
		RequiredTopics: []string{
			"node.?dense",
			"pod.?dense",
			"minvalues",
			"consolidation",
			"empty",
			"expiration",
			"drift",
			"interrupt",
		},
	},
	"scheduling": {
		Name:     "scheduling",
		Path:     "test/suites/scheduling",
		MinSpecs: 26,
		RequiredTopics: []string{
			"annotations",
			"well-known labels|well known labels",
			"instance type",
			"zone",
			"gpu|accelerator",
			"naked pods|deployment",
			"topology spread|affinity",
			"minvalues",
			"priority",
			"initcontainers",
			"reservation|capacity reservation",
			"hugepages",
		},
	},
	"storage": {
		Name:     "storage",
		Path:     "test/suites/storage",
		MinSpecs: 10,
		RequiredTopics: []string{
			"pre-bound|persistent volume",
			"storage class",
			"topology",
			"generic ephemeral",
			"dynamic",
			"volume limits?",
			"disrupted|node deletion|drain",
		},
	},
}

var ExpectedWorkflows = []ExpectedArtifact{
	{Name: "e2e", Path: ".github/workflows/e2e.yaml"},
	{Name: "e2e-cleanup", Path: ".github/workflows/e2e-cleanup.yaml"},
	{Name: "e2e-kwok", Path: ".github/workflows/e2e-kwok.yaml"},
	{Name: "e2e-matrix", Path: ".github/workflows/e2e-matrix.yaml"},
	{Name: "e2e-matrix-trigger", Path: ".github/workflows/e2e-matrix-trigger.yaml"},
	{Name: "e2e-parity", Path: ".github/workflows/e2e-parity.yaml"},
	{Name: "e2e-private-cluster-trigger", Path: ".github/workflows/e2e-private-cluster-trigger.yaml"},
	{Name: "e2e-scale-trigger", Path: ".github/workflows/e2e-scale-trigger.yaml"},
	{Name: "e2e-soak-trigger", Path: ".github/workflows/e2e-soak-trigger.yaml"},
	{Name: "e2e-upgrade", Path: ".github/workflows/e2e-upgrade.yaml"},
	{Name: "e2e-version-compatibility-trigger", Path: ".github/workflows/e2e-version-compatibility-trigger.yaml"},
}

var ExpectedActions = []ExpectedArtifact{
	{Name: "cleanup", Path: ".github/actions/e2e/cleanup/action.yaml"},
	{Name: "dump-logs", Path: ".github/actions/e2e/dump-logs/action.yaml"},
	{Name: "install-helm", Path: ".github/actions/e2e/install-helm/action.yaml"},
	{Name: "install-karpenter", Path: ".github/actions/e2e/install-karpenter/action.yaml"},
	{Name: "install-prometheus", Path: ".github/actions/e2e/install-prometheus/action.yaml"},
	{Name: "run-tests-private-cluster", Path: ".github/actions/e2e/run-tests-private-cluster/action.yaml"},
	{Name: "setup-cluster", Path: ".github/actions/e2e/setup-cluster/action.yaml"},
	{Name: "upgrade-crds", Path: ".github/actions/e2e/upgrade-crds/action.yaml"},
}

func Audit(repo string) (Report, error) {
	repo, err := filepath.Abs(repo)
	if err != nil {
		return Report{}, err
	}
	report := Report{
		ExpectedSuites:    append([]ExpectedArtifact(nil), ExpectedSuites...),
		ExpectedWorkflows: append([]ExpectedArtifact(nil), ExpectedWorkflows...),
		ExpectedActions:   append([]ExpectedArtifact(nil), ExpectedActions...),
	}
	report.MissingSuites = missing(repo, ExpectedSuites)
	report.SemanticGaps = semanticGaps(repo, ExpectedSuiteCoverageByName)
	report.MissingWorkflows = missing(repo, ExpectedWorkflows)
	report.MissingActions = missing(repo, ExpectedActions)
	return report, nil
}

func (r Report) Complete() bool {
	return len(r.MissingSuites) == 0 && len(r.SemanticGaps) == 0 && len(r.MissingWorkflows) == 0 && len(r.MissingActions) == 0
}

func (r Report) MissingSuiteNames() []string {
	return artifactNames(r.MissingSuites)
}

func (r Report) MissingWorkflowPaths() []string {
	return artifactPaths(r.MissingWorkflows)
}

func (r Report) MissingActionPaths() []string {
	return artifactPaths(r.MissingActions)
}

func FormatText(report Report) string {
	var buf bytes.Buffer
	status := "complete"
	if !report.Complete() {
		status = "incomplete"
	}
	fmt.Fprintf(&buf, "E2E parity: %s\n", status)
	writeArtifactSection(&buf, "missing suites", report.MissingSuites, false)
	writeSemanticSection(&buf, report.SemanticGaps)
	writeArtifactSection(&buf, "missing workflows", report.MissingWorkflows, true)
	writeArtifactSection(&buf, "missing actions", report.MissingActions, true)
	return buf.String()
}

func FormatJSON(report Report) ([]byte, error) {
	return json.MarshalIndent(report, "", "  ")
}

func semanticGaps(repo string, expected map[string]ExpectedSuiteCoverage) []SemanticGap {
	var names []string
	for name := range expected {
		names = append(names, name)
	}
	sort.Strings(names)

	var gaps []SemanticGap
	for _, name := range names {
		coverage := expected[name]
		path := filepath.Join(repo, coverage.Path)
		content := suiteContent(path)
		if strings.TrimSpace(content) == "" {
			continue
		}
		specCount := strings.Count(content, "It(")
		if specCount < coverage.MinSpecs {
			gaps = append(gaps, SemanticGap{
				Suite:   coverage.Name,
				Reason:  "spec count below AWS provider baseline",
				Current: specCount,
				Want:    coverage.MinSpecs,
			})
		}
		lower := strings.ToLower(content)
		for _, topic := range coverage.RequiredTopics {
			matched, err := regexp.MatchString(topic, lower)
			if err != nil || !matched {
				gaps = append(gaps, SemanticGap{
					Suite:   coverage.Name,
					Reason:  "missing required semantic topic",
					Pattern: topic,
				})
			}
		}
	}
	return gaps
}

func suiteContent(path string) string {
	var buf strings.Builder
	_ = filepath.WalkDir(path, func(child string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		data, err := os.ReadFile(child)
		if err != nil {
			return nil
		}
		buf.Write(data)
		buf.WriteByte('\n')
		return nil
	})
	return buf.String()
}

func missing(repo string, artifacts []ExpectedArtifact) []ExpectedArtifact {
	var result []ExpectedArtifact
	for _, artifact := range artifacts {
		if !artifactPresent(filepath.Join(repo, artifact.Path)) {
			result = append(result, artifact)
		}
	}
	return result
}

func writeSemanticSection(buf *bytes.Buffer, gaps []SemanticGap) {
	fmt.Fprintf(buf, "semantic gaps (%d):\n", len(gaps))
	if len(gaps) == 0 {
		buf.WriteString("  none\n")
		return
	}
	for _, gap := range gaps {
		switch gap.Reason {
		case "spec count below AWS provider baseline":
			fmt.Fprintf(buf, "  - %s: %s (%d/%d specs)\n", gap.Suite, gap.Reason, gap.Current, gap.Want)
		default:
			fmt.Fprintf(buf, "  - %s: %s %q\n", gap.Suite, gap.Reason, gap.Pattern)
		}
	}
}

func artifactPresent(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !info.IsDir() {
		return info.Size() > 0
	}
	hasTestFile := false
	_ = filepath.WalkDir(path, func(child string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		info, err := d.Info()
		if err == nil && info.Size() > 0 {
			hasTestFile = true
			return filepath.SkipAll
		}
		return nil
	})
	return hasTestFile
}

func artifactNames(artifacts []ExpectedArtifact) []string {
	values := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		values = append(values, artifact.Name)
	}
	sort.Strings(values)
	return values
}

func artifactPaths(artifacts []ExpectedArtifact) []string {
	values := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		values = append(values, artifact.Path)
	}
	sort.Strings(values)
	return values
}

func writeArtifactSection(buf *bytes.Buffer, title string, artifacts []ExpectedArtifact, usePath bool) {
	fmt.Fprintf(buf, "%s (%d):\n", title, len(artifacts))
	if len(artifacts) == 0 {
		buf.WriteString("  none\n")
		return
	}
	for _, artifact := range artifacts {
		value := artifact.Name
		if usePath {
			value = artifact.Path
		}
		fmt.Fprintf(buf, "  - %s\n", value)
	}
}
