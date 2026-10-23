package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/pkg/strimzi"
)

func TestDescribeInstalled(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll("charts/kafka-ui", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("charts/kafka-ui", "Chart.yaml"), []byte("apiVersion: v2\nname: kafka-ui\nversion: 0.3.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := DeploySummaryEntry{Name: "Kafka UI", Release: "kafka-ui", Namespace: "team-ui"}

	for chart, want := range map[string]string{
		"kafka-ui-0.3.0":     "Kafka UI 0.3.0 → 0.3.1 in team-ui (upgrade)",
		"kafka-ui-0.3.1":     "Kafka UI 0.3.1 → 0.3.1 in team-ui (same chart; reapply)",
		"kafka-ui-0.4.0":     "Kafka UI 0.4.0 → 0.3.1 in team-ui (older chart here; replace)",
		"provectus-ui-1.2.0": "Kafka UI provectus-ui-1.2.0 → 0.3.1 in team-ui (replace with kafka-ui)",
	} {
		if got := describeInstalled(e, chart, "charts/kafka-ui"); got != want {
			t.Errorf("%s: got %q, want %q", chart, got, want)
		}
	}
}

func TestInstalledReleaseChoices(t *testing.T) {
	orig := runExecOutputFn
	t.Cleanup(func() { runExecOutputFn = orig })
	runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		for release, chart := range map[string]string{"kafka-ui": "kafka-ui-0.3.0", "kates": "kates-0.13.0", "postgresql": "postgresql-16.4.0"} {
			if strings.Contains(joined, "-n ns-"+release+" --deployed --filter ^"+release+"$") {
				return []byte(`[{"name":"` + release + `","chart":"` + chart + `"}]`), nil
			}
		}
		return []byte("[]"), nil
	}
	var entries []DeploySummaryEntry
	for _, release := range []string{"kafka-ui", "mm2", "kates", "postgresql"} {
		entries = append(entries, DeploySummaryEntry{Name: release, Release: release, Namespace: "ns-" + release})
	}

	options, notes := installedReleaseChoices(context.Background(), entries)
	if len(options) != 1 || options[0].Value != "kafka-ui" {
		t.Errorf("want only the installed kafka-ui offered, got %v", options)
	}
	if len(notes) != 2 || !strings.HasSuffix(notes[0], "always upgraded") || !strings.Contains(notes[1], "postgresql-16.4.0 in ns-postgresql: kept") {
		t.Errorf("want kates and postgresql noted, got %q", notes)
	}
}

func TestReleaseKept_UpgradeListTakesItOut(t *testing.T) {
	orig := isHelmReleaseDeployedFn
	t.Cleanup(func() { isHelmReleaseDeployedFn = orig; deployUpgradeReleases = map[string]bool{} })
	isHelmReleaseDeployedFn = func(context.Context, string, string) bool { return true }

	deployUpgradeReleases = map[string]bool{"kafka-ui": true}
	if releaseKept(context.Background(), "kafka-ui", "ns") {
		t.Error("a release on the upgrade list was kept")
	}
	if !releaseKept(context.Background(), "mm2", "ns") {
		t.Error("an installed release off the list was not kept")
	}
}

func TestAgainstInstalled(t *testing.T) {
	op := &strimzi.Operator{Version: "1.2.0"}
	for version, want := range map[string]string{
		"1.3.0": "1.3.0  (upgrade from 1.2.0)",
		"1.2.0": "1.2.0",
		"1.1.0": "1.1.0",
	} {
		if got := againstInstalled(version, version, op); got != want {
			t.Errorf("%s: got %q, want %q", version, got, want)
		}
	}
	if !olderThan("1.1.0", op) || olderThan("1.2.0", op) || olderThan("1.1.0", nil) {
		t.Error("olderThan: want only a release below the installed operator left out")
	}
}
