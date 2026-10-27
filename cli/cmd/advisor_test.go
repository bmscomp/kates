package cmd

import (
	"strings"
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

// Each rule is a check of its own; analyzeRun prints them in table order,
// over the average throughput of the phases.
func TestAnalyzeRun_RulesInOrder(t *testing.T) {
	batch, zero := 8192, 0
	run := &client.TestRun{
		Spec: &client.TestSpec{BatchSize: &batch, LingerMs: &zero, Acks: "0", ReplicationFactor: 3,
			CompressionType: "gzip", Partitions: 3, ParallelProducers: 3, RecordSizeBytes: 100},
		Results: []client.PhaseResult{{ThroughputRecordsPerSec: 40_000}, {ThroughputRecordsPerSec: 80_000}},
	}
	var got []string
	for _, r := range analyzeRun(run, nil) {
		got = append(got, r.Severity+" "+r.Title)
	}
	want := []string{
		"HIGH batch.size=8192 is leaving throughput on the table",
		"HIGH linger.ms=0 causes excessive small-batch sends",
		"HIGH acks=0 with replicationFactor=3 risks data loss",
		"MED gzip compression has highest CPU overhead",
		"MED partitions=3 with 3 producers limits parallelism",
		"MED recordSize=100B is small — high per-record overhead",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("rules:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if rules := analyzeRun(&client.TestRun{Spec: run.Spec}, nil); len(rules) != 0 {
		t.Errorf("a run with no results: %d rules, want none", len(rules))
	}
}

func TestAdviseCompression(t *testing.T) {
	for _, tt := range []struct {
		compression string
		throughput  float64
		severity    string
	}{
		{"", 25_000, "HIGH"}, {"none", 1_000, "MED"}, {"gzip", 1_000, "MED"}, {"zstd", 1_000, "OK"},
	} {
		r := adviseCompression(&client.TestSpec{CompressionType: tt.compression}, tt.throughput, 0)
		if r == nil || r.Severity != tt.severity {
			t.Errorf("compression %q at %v rec/s: %+v, want %s", tt.compression, tt.throughput, r, tt.severity)
		}
	}
}
