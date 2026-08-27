package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuditReportsMissingSuitesWorkflowsAndActions(t *testing.T) {
	repo := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "test", "suites", "scale"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "test", "suites", "nodeclaim"), 0755))

	report, err := Audit(repo)
	require.NoError(t, err)

	require.False(t, report.Complete())
	require.ElementsMatch(t, []string{
		"ami",
		"consolidation",
		"drift",
		"integration",
		"interruption",
		"ipv6",
		"localzone",
		"nodeclaim",
		"scale",
		"scheduling",
		"storage",
	}, report.MissingSuiteNames())
	require.Contains(t, report.MissingWorkflowPaths(), ".github/workflows/e2e.yaml")
	require.Contains(t, report.MissingWorkflowPaths(), ".github/workflows/e2e-soak-trigger.yaml")
	require.Contains(t, report.MissingActionPaths(), ".github/actions/e2e/setup-cluster/action.yaml")
	require.Contains(t, report.MissingActionPaths(), ".github/actions/e2e/cleanup/action.yaml")
}

func TestAuditCompletesWhenAllExpectedArtifactsExist(t *testing.T) {
	repo := t.TempDir()
	for _, suite := range ExpectedSuites {
		writeFile(t, filepath.Join(repo, "test", "suites", suite.Name, "suite_test.go"), semanticSuiteFixture(suite.Name))
	}
	for _, workflow := range ExpectedWorkflows {
		writeFile(t, filepath.Join(repo, workflow.Path), "name: e2e\n")
	}
	for _, action := range ExpectedActions {
		writeFile(t, filepath.Join(repo, action.Path), "name: action\n")
	}

	report, err := Audit(repo)
	require.NoError(t, err)

	require.Empty(t, report.MissingSuites)
	require.Empty(t, report.SemanticGaps)
	require.Empty(t, report.MissingWorkflows)
	require.Empty(t, report.MissingActions)
	require.True(t, report.Complete())
	require.Empty(t, report.MissingSuiteNames())
	require.Empty(t, report.MissingWorkflowPaths())
	require.Empty(t, report.MissingActionPaths())
}

func TestAuditReportsSemanticGaps(t *testing.T) {
	repo := t.TempDir()
	for _, suite := range ExpectedSuites {
		writeFile(t, filepath.Join(repo, "test", "suites", suite.Name, "suite_test.go"), "package "+suite.Name+"\n")
	}
	for _, workflow := range ExpectedWorkflows {
		writeFile(t, filepath.Join(repo, workflow.Path), "name: e2e\n")
	}
	for _, action := range ExpectedActions {
		writeFile(t, filepath.Join(repo, action.Path), "name: action\n")
	}

	report, err := Audit(repo)
	require.NoError(t, err)

	require.False(t, report.Complete())
	require.NotEmpty(t, report.SemanticGaps)
	require.Contains(t, FormatText(report), "semantic gaps")
}

func TestAuditTreatsEmptyArtifactsAsMissing(t *testing.T) {
	repo := t.TempDir()
	for _, suite := range ExpectedSuites {
		require.NoError(t, os.MkdirAll(filepath.Join(repo, "test", "suites", suite.Name), 0755))
	}
	for _, workflow := range ExpectedWorkflows {
		writeEmptyFile(t, filepath.Join(repo, workflow.Path))
	}
	for _, action := range ExpectedActions {
		writeEmptyFile(t, filepath.Join(repo, action.Path))
	}

	report, err := Audit(repo)
	require.NoError(t, err)

	require.False(t, report.Complete())
	require.Len(t, report.MissingSuites, len(ExpectedSuites))
	require.Len(t, report.MissingWorkflows, len(ExpectedWorkflows))
	require.Len(t, report.MissingActions, len(ExpectedActions))
}

func TestFormatTextContainsCurrentStatus(t *testing.T) {
	repo := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "test", "suites", "scale"), 0755))

	report, err := Audit(repo)
	require.NoError(t, err)

	text := FormatText(report)
	require.Contains(t, text, "E2E parity: incomplete")
	require.Contains(t, text, "missing suites")
	require.Contains(t, text, "nodeclaim")
	require.Contains(t, text, ".github/workflows/e2e.yaml")
}

func writeEmptyFile(t *testing.T, path string) {
	t.Helper()
	writeFile(t, path, "")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
}

func semanticSuiteFixture(name string) string {
	coverage := ExpectedSuiteCoverageByName[name]
	var out strings.Builder
	out.WriteString("package ")
	out.WriteString(name)
	out.WriteString("\n")
	for i := 0; i < coverage.MinSpecs; i++ {
		out.WriteString("func _() { It(\"covers AWS provider baseline\", func() {}) }\n")
	}
	for _, topic := range coverage.RequiredTopics {
		out.WriteString("// ")
		out.WriteString(regexFixtureText(topic))
		out.WriteByte('\n')
	}
	return out.String()
}

func regexFixtureText(pattern string) string {
	first, _, _ := strings.Cut(pattern, "|")
	replacer := strings.NewReplacer(
		".?", "",
		"?", "",
		"\\", "",
		"(", "",
		")", "",
		"[", "",
		"]", "",
	)
	return replacer.Replace(first)
}
