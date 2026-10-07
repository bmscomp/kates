package cmd

import (
	"testing"

	"github.com/bmscomp/kates/cli/client"
)

// The linger rule fires on the linger.ms a run reports as 0. A spec that
// reports none is not at 0: the Kates API serves every run's linger.ms, and
// its own advisor reads a missing one as 5 ms.
func TestAnalyzeRun_TheLingerRuleNeedsALingerOfZero(t *testing.T) {
	zero, five := 0, 5
	results := []client.PhaseResult{{PhaseName: "produce", Status: "DONE", ThroughputRecordsPerSec: 12000}}
	for _, tt := range []struct {
		name     string
		lingerMs *int
		want     bool
	}{{"0", &zero, true}, {"5", &five, false}, {"absent", nil, false}} {
		run := &client.TestRun{Spec: &client.TestSpec{LingerMs: tt.lingerMs, CompressionType: "lz4"}, Results: results}
		got := false
		for _, r := range analyzeRun(run, nil) {
			got = got || r.Title == "linger.ms=0 causes excessive small-batch sends"
		}
		if got != tt.want {
			t.Errorf("lingerMs %s: the linger rule fires: %t, want %t", tt.name, got, tt.want)
		}
	}
}
