package cmd

import (
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/output"
)

func TestTestGet_ShowsIntegrityRtoRpoAndVerdict(t *testing.T) {
	mockResponse := `{
		"id": "run-1",
		"testType": "INTEGRITY",
		"status": "DONE",
		"results": [{
			"phaseName": "integrity",
			"status": "DONE",
			"integrity": {
				"totalSent": 1000, "totalAcked": 1000, "totalConsumed": 998,
				"lostRecords": 2, "dataLossPercent": 0.2,
				"producerRto": 1.5, "maxRto": 1.5, "rpo": null,
				"producerRtoMs": 1500.0, "consumerRtoMs": 0.0, "maxRtoMs": 1500.0, "rpoMs": -1.0,
				"verdict": "DATA_LOSS"
			}
		}]
	}`
	ts, buf := setupTest(t, "GET", "/api/tests/run-1", 200, mockResponse)
	defer ts.Close()

	if err := testGetCmd.RunE(testGetCmd, []string{"run-1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stripAnsi(buf.String())
	for _, want := range []string{"Producer RTO", "1500 ms", "Max RTO", "DATA_LOSS"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// rpoMs -1 is "not measured", never a zero or a hidden line.
	if !strings.Contains(out, "not measured") {
		t.Errorf("unmeasured RPO should print as not measured:\n%s", out)
	}
	if strings.Contains(out, "-1 ms") {
		t.Errorf("the -1 sentinel must not be printed as a value:\n%s", out)
	}
}

// The Kates API lists at most the first 1,000 lost ranges, while Lost counts
// every lost record. A table whose ranges hold fewer records than that says
// it shows the first ones, rather than passing for the whole list.
func TestTestGet_SaysWhenTheLostRangesAreTheFirstOnes(t *testing.T) {
	for _, tc := range []struct {
		lost string
		hint bool
	}{{lost: "1000", hint: true}, {lost: "2", hint: false}} {
		ts, buf := setupTest(t, "GET", "/api/tests/run-3", 200, `{
			"id": "run-3", "testType": "INTEGRITY", "status": "DONE",
			"results": [{"phaseName": "integrity", "status": "DONE", "integrity": {
				"totalSent": 6000, "totalAcked": 6000, "lostRecords": `+tc.lost+`,
				"rpoMs": -1.0, "verdict": "DATA_LOSS",
				"lostRanges": [{"fromSeq": 1, "toSeq": 1, "count": 1}, {"fromSeq": 7, "toSeq": 7, "count": 1}]
			}}]
		}`)
		if err := testGetCmd.RunE(testGetCmd, []string{"run-3"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		ts.Close()
		out := stripAnsi(buf.String())
		if got := strings.Contains(out, "showing the first 2 ranges: 2 of the 1000 lost records"); got != tc.hint {
			t.Errorf("%s lost records in 2 listed ranges: hint shown %t, want %t:\n%s", tc.lost, got, tc.hint, out)
		}
	}
}

// The throughput bar is drawn against the rate the run used, spec.throughput:
// throughput wins over targetThroughput when a request sets both, and a
// request that sets only throughput has no targetThroughput at all.
func TestTestGet_ThroughputBarUsesTheRateTheRunUsed(t *testing.T) {
	ts, buf := setupTest(t, "GET", "/api/tests/run-2", 200, `{
		"id": "run-2", "testType": "LOAD", "status": "DONE",
		"spec": {"throughput": 300, "targetThroughput": 2000},
		"results": [{"phaseName": "produce", "status": "DONE", "throughputRecordsPerSec": 290}]
	}`)
	defer ts.Close()

	if err := testGetCmd.RunE(testGetCmd, []string{"run-2"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 290 of 300 fills 19 of the bar's 20 cells; against 2000 it filled 2.
	var bar string
	for _, line := range strings.Split(stripAnsi(buf.String()), "\n") {
		if strings.Contains(line, "Throughput") && strings.Contains(line, output.Glyphs().BarEmpty) {
			bar = line
		}
	}
	if got := strings.Count(bar, output.Glyphs().BarFull); got != 19 {
		t.Errorf("bar %q fills %d cells, want 19: it is not drawn against the rate the run used", bar, got)
	}
}
