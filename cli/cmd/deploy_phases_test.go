package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/pkg/detect"
)

// The platform line names the account from the cluster-admin check.
func TestAccountLabel(t *testing.T) {
	for _, tc := range []struct {
		admin, ok bool
		want      string
	}{
		{true, true, "cluster admin"},
		{false, true, "not cluster admin"},
		{false, false, "cluster admin not confirmed"},
		{true, false, "cluster admin not confirmed"},
	} {
		if got := accountLabel(tc.admin, tc.ok); got != tc.want {
			t.Errorf("accountLabel(%v, %v) = %q, want %q", tc.admin, tc.ok, got, tc.want)
		}
	}
}

// kind is told by its CNI or its provider, and picks values-kind.yaml.
func TestChartOverlayFollowsTheClusterType(t *testing.T) {
	kindByCNI := &detect.DetectReport{}
	kindByCNI.Network.CNI = "kindnet"
	for _, tc := range []struct {
		name   string
		report *detect.DetectReport
		want   string
	}{
		{"kindnet", kindByCNI, "charts/kates/values-kind.yaml"},
		{"kind provider", &detect.DetectReport{Provider: "kind"}, "charts/kates/values-kind.yaml"},
		{"eks", &detect.DetectReport{Provider: "eks"}, "charts/kates/values-generic.yaml"},
	} {
		if got := chartOverlayFor(isKindReport(tc.report), "charts/kates"); got != tc.want {
			t.Errorf("%s: overlay %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Admin-less, nothing was introspected: the values file is created, empty,
// with its directory.
func TestWriteDeployValuesAdminlessLeavesTheFileEmpty(t *testing.T) {
	orig := deployAdminless
	t.Cleanup(func() { deployAdminless = orig })
	deployAdminless = true

	path := filepath.Join(t.TempDir(), "nested", "values-detected.yaml")
	if err := writeDeployValues(defaultExecutor, path, &detect.DetectReport{}, "krafter"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatalf("values file has %d bytes, want none", len(data))
	}
}

// A values file that cannot be created fails the deploy before any Helm
// call.
func TestWriteDeployValuesReportsACreateFailure(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	err := writeDeployValues(defaultExecutor, filepath.Join(blocker, "values.yaml"), &detect.DetectReport{}, "krafter")
	if err == nil || !strings.Contains(err.Error(), "failed to create values file") {
		t.Fatalf("err = %v, want a create failure", err)
	}
}

// The components pane is 55% of the window, between 50 and 70 columns; the
// logs pane takes the rest of the row, at least 20.
func TestDashboardPaneWidths(t *testing.T) {
	for _, tc := range []struct {
		width, comp, logs int
	}{
		{200, 70, 128},
		{100, 55, 43},
		{60, 50, 20},
	} {
		comp, logs := dashboardPaneWidths(tc.width)
		if comp != tc.comp || logs != tc.logs {
			t.Errorf("width %d: panes %d and %d, want %d and %d", tc.width, comp, logs, tc.comp, tc.logs)
		}
	}
}

func TestUpgradeLdflagsStampsTheBuild(t *testing.T) {
	got := upgradeLdflags("main", "abc1234", "2026-10-27T00:00:00Z")
	for _, want := range []string{
		"cmd.Version=main-abc1234",
		"cmd.Commit=abc1234",
		"cmd.BuildDate=2026-10-27T00:00:00Z",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("ldflags %q lack %q", got, want)
		}
	}
}
