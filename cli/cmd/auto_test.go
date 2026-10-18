package cmd

import (
	"path/filepath"
	"strings"
	"testing"
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
		if err := runAuto(autoCmd, nil); err == nil || !strings.Contains(err.Error(), "Helm chart not found") {
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
