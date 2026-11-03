package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/internal/helm"
	"github.com/bmscomp/kates/cli/internal/proc"
	"github.com/bmscomp/kates/cli/pkg/detect"
)

// kates auto --dry-run introspected the cluster with every write probe on: a
// namespace, a Secret and prober pods, where kates deploy --dry-run writes
// nothing. The run without --dry-run is the control: the same introspection
// does write.
func TestAutoDryRunCollectsReadOnly(t *testing.T) {
	origExecutor, origDryRun, origChart := defaultExecutor, autoDryRun, autoChartDir
	t.Cleanup(func() { defaultExecutor, autoDryRun, autoChartDir = origExecutor, origDryRun, origChart })
	// No chart there, so the run stops before helm, right after introspection.
	autoChartDir = filepath.Join(t.TempDir(), "no-chart")

	writes := func(dryRun bool) []string {
		rec := &recordingExecutor{}
		defaultExecutor, autoDryRun = rec, dryRun
		if err := runAuto(autoCmd, nil); err == nil || !strings.Contains(err.Error(), "helm chart not found") {
			t.Fatalf("dry run %v: err = %v, want the missing chart", dryRun, err)
		}
		var out []string
		for _, c := range rec.commands() {
			if clusterWrite.MatchString(c) {
				out = append(out, c)
			}
		}
		return out
	}

	if len(writes(false)) == 0 {
		t.Fatal("control: introspection without --dry-run wrote nothing, so the dry run proves nothing")
	}
	if got := writes(true); len(got) != 0 {
		t.Errorf("auto --dry-run wrote to the cluster:\n  %s", strings.Join(got, "\n  "))
	}
}

// The detected values go to values-detected.yaml under the build directory,
// merged over --values when given; a --values file that cannot be read fails
// before the values file is created. Errors from rendering and closing the
// file were dropped, so a failed write still reported "Configuration
// generated" and helm installed what was left.
func TestWriteAutoValues(t *testing.T) {
	origBase := autoBaseValues
	t.Cleanup(func() { autoBaseValues = origBase })
	report := &detect.DetectReport{}

	dir := filepath.Join(t.TempDir(), "build")
	autoBaseValues = ""
	path, err := writeAutoValues(report, dir)
	if err != nil || path != filepath.Join(dir, "values-detected.yaml") {
		t.Fatalf("writeAutoValues = %q, %v", path, err)
	}
	if data, err := os.ReadFile(path); err != nil || len(data) == 0 {
		t.Fatalf("values file: %d bytes, %v", len(data), err)
	}

	base := filepath.Join(t.TempDir(), "base.yaml")
	if err := os.WriteFile(base, []byte("customKey: kept\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	autoBaseValues = base
	if path, err = writeAutoValues(report, dir); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "customKey: kept") {
		t.Errorf("the base values are not merged in:\n%s", data)
	}

	autoBaseValues = filepath.Join(t.TempDir(), "missing.yaml")
	dir = filepath.Join(t.TempDir(), "other")
	if _, err := writeAutoValues(report, dir); err == nil || !strings.Contains(err.Error(), "failed to read base values") {
		t.Errorf("a missing --values file: err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "values-detected.yaml")); !os.IsNotExist(err) {
		t.Errorf("a values file was created though --values could not be read: %v", err)
	}
}

// The monitoring release gets .policies' monitoring values, as deploy's does.
func TestAutoMonitoringPassesPolicies(t *testing.T) {
	origChart := autoChartDir
	t.Cleanup(func() { autoChartDir = origChart })
	root := t.TempDir()
	autoChartDir = filepath.Join(root, "kafka-cluster")
	if err := os.MkdirAll(filepath.Join(root, "monitoring"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "monitoring", "Chart.yaml"), []byte("name: monitoring\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	fake := &proc.Fake{}
	deployAutoMonitoring(helm.NewWith(fake, ""), []string{"-f", "monitoring-policies.yaml"})
	lines := fake.Lines()
	if len(lines) == 0 || !strings.Contains(lines[len(lines)-1], "upgrade --install monitoring ") || !strings.Contains(lines[len(lines)-1], " -f monitoring-policies.yaml") {
		t.Errorf("monitoring must be installed with the policies values: %q", lines)
	}
}
