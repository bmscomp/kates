package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/client"
)

// advisor --apply writes the run's scenario with each recommendation's
// setting applied; it used to print one hint and no file.
func TestTunedScenario_AppliesTheRecommendedSettings(t *testing.T) {
	batch, linger := 16384, 0
	run := &client.TestRun{
		ID: "3fa85f64-5717-4562-b3fc-2c963f66afa6", TestType: "LOAD", Backend: "java", ScenarioName: "nightly",
		Spec: &client.TestSpec{Records: 100000, ParallelProducers: 4, RecordSizeBytes: 1024, DurationMs: 60000,
			Topic: "orders", Acks: "1", BatchSize: &batch, LingerMs: &linger, ReplicationFactor: 3, Partitions: 4},
		Results: []client.PhaseResult{{PhaseName: "produce", Status: "DONE", ThroughputRecordsPerSec: 30000}},
	}
	got, err := tunedScenario(run, analyzeRun(run, nil))
	if err != nil {
		t.Fatal(err)
	}
	want := TestScenario{Name: "nightly-tuned", Type: "load", Backend: "java", Spec: map[string]any{
		"records": 100000, "parallelProducers": 4, "recordSizeBytes": 1024, "durationSeconds": 60,
		"topic": "orders", "replicationFactor": 3,
		// Tuned: batch, linger, acks, compression and partitions.
		"batchSize": 65536, "lingerMs": 10, "acks": "all", "compressionType": "lz4", "partitions": 16,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tuned scenario\n%+v\nwant\n%+v", got, want)
	}
	// What kates apply would send from it carries the tuned values.
	req := scenarioToRequest(got)
	if req.Spec == nil || *req.Spec.BatchSize != 65536 || *req.Spec.LingerMs != 10 || req.Spec.Acks != "all" || req.Spec.DurationMs != 60000 {
		t.Errorf("scenarioToRequest(tuned) = %+v", req.Spec)
	}
}

// A run whose settings draw no recommendation gets no file: the user asked
// for a tuned scenario, not a copy.
func TestTunedScenario_NothingToApply(t *testing.T) {
	run := &client.TestRun{ID: "abc", TestType: "LOAD",
		Spec:    &client.TestSpec{CompressionType: "lz4", Acks: "all", ParallelProducers: 2, Partitions: 16},
		Results: []client.PhaseResult{{PhaseName: "produce", Status: "DONE", ThroughputRecordsPerSec: 1000}}}
	if _, err := tunedScenario(run, analyzeRun(run, nil)); err == nil || !strings.Contains(err.Error(), "nothing to apply") {
		t.Errorf("err = %v, want nothing to apply", err)
	}
	if _, err := tunedScenario(&client.TestRun{ID: "abc"}, nil); err == nil {
		t.Error("a run without a spec: no error")
	}
	// A run without a scenario name is named after its id.
	run.Spec.CompressionType = "gzip"
	got, err := tunedScenario(run, analyzeRun(run, nil))
	if err != nil || got.Name != "run-abc-tuned" || got.Spec["compressionType"] != "lz4" {
		t.Errorf("tunedScenario = %+v, %v", got, err)
	}
}

// --replicas 0 made the network-out term (replicas-1) negative and the total
// with it.
func TestValidCostInputs(t *testing.T) {
	if err := validCostInputs(3, 100000, 512, 300, 3); err != nil {
		t.Errorf("defaults: %v", err)
	}
	if err := validCostInputs(1, 0, 0, 0, 0); err != nil {
		t.Errorf("zeros with one replica: %v", err)
	}
	for name, args := range map[string][5]int{
		"replicas 0":       {0, 100000, 512, 300, 3},
		"replicas -1":      {-1, 100000, 512, 300, 3},
		"negative records": {3, -1, 512, 300, 3},
		"negative brokers": {3, 100000, 512, 300, -3},
	} {
		if err := validCostInputs(args[0], args[1], args[2], args[3], args[4]); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if err := validCostInputs(0, 1, 1, 1, 1); err == nil || !strings.Contains(err.Error(), "--replicas") {
		t.Errorf("replicas 0: err = %v, want it to name --replicas", err)
	}
}

// changelog --until used to ignore a date it could not parse and list
// everything up to now.
func TestParseChangelogUntil(t *testing.T) {
	if got, err := parseChangelogUntil(""); err != nil || !got.IsZero() {
		t.Errorf("empty: %v, %v; want the zero time", got, err)
	}
	got, err := parseChangelogUntil("2024-01-31")
	if err != nil || got.Year() != 2024 || got.Month() != 1 || got.Day() != 31 {
		t.Errorf("2024-01-31: %v, %v", got, err)
	}
	for _, bad := range []string{"2024-13-01", "31/01/2024", "yesterday", "2024-01-31T00:00:00Z"} {
		if _, err := parseChangelogUntil(bad); err == nil || !strings.Contains(err.Error(), bad) {
			t.Errorf("%q: err = %v, want an error naming it", bad, err)
		}
	}
}

// A snapshot name becomes a file name under ~/.kates/snapshots, and "../x"
// used to escape it.
func TestSnapshotNames(t *testing.T) {
	for _, ok := range []string{"pre-upgrade", "production-v3.2", "a_b", "1"} {
		if err := validSnapshotName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "../x", "a/b", ".", "..", "with space", "semi;colon"} {
		if err := validSnapshotName(bad); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := saveSnapshot(ClusterSnapshot{Name: "../escaped"}); err == nil {
		t.Error("saveSnapshot(../escaped): no error")
	}
	if _, err := os.Stat(filepath.Join(home, ".kates", "escaped.json")); err == nil {
		t.Error("saveSnapshot wrote outside the snapshot directory")
	}
	if _, err := loadSnapshot("../../etc/passwd"); err == nil || !strings.Contains(err.Error(), "invalid snapshot name") {
		t.Errorf("loadSnapshot(../../etc/passwd): %v", err)
	}
	if err := saveSnapshot(ClusterSnapshot{Name: "ok"}); err != nil {
		t.Fatal(err)
	}
	if s, err := loadSnapshot("ok"); err != nil || s.Name != "ok" {
		t.Errorf("loadSnapshot(ok) = %+v, %v", s, err)
	}
}

// The label and value are path segments of the shields.io URL: the "/" in
// "N/A" and "rec/s" used to make a fourth segment.
func TestBadgeURL(t *testing.T) {
	for _, tt := range []struct{ label, value, color, want string }{
		{"kates", "A+", "brightgreen", "https://img.shields.io/badge/kates-A+-brightgreen"},
		{"kates load", "N/A", "red", "https://img.shields.io/badge/kates%20load-N%2FA-red"},
		{"kates stress", "12.3K rec/s", "blue", "https://img.shields.io/badge/kates%20stress-12.3K%20rec%2Fs-blue"},
		{"kates", "42ms", "blue", "https://img.shields.io/badge/kates-42ms-blue"},
	} {
		if got := badgeURL(tt.label, tt.value, tt.color); got != tt.want {
			t.Errorf("badgeURL(%q, %q, %q) = %s, want %s", tt.label, tt.value, tt.color, got, tt.want)
		}
	}
}
