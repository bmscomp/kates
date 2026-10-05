package cmd

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// A report whose status is ERROR says why, and the command prints it: the
// reason used to reach only the server log.
func TestResilienceRun_PrintsWhyTheReportIsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.yaml")
	data := "testRequest:\n  type: LOAD\nchaosSpec:\n  experimentName: x\nsteadyStateSec: 1\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	ts, buf := setupTest(t, "POST", "/api/resilience", 200,
		`{"status":"ERROR","error":"The benchmark did not start: Concurrency limit reached"}`)
	defer ts.Close()
	resilienceFile, resilienceDryRun = path, false
	defer func() { resilienceFile = "" }()

	if err := resilienceRunCmd.RunE(resilienceRunCmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out := stripAnsi(buf.String()); !strings.Contains(out, "The benchmark did not start: Concurrency limit reached") {
		t.Errorf("the reason is not printed:\n%s", out)
	}
}

// resilienceReport is a report as the Kates API writes it, behind the spaces
// it sends every 10 seconds to keep the call open. Jackson writes a Duration
// as decimal seconds with nine places, and a long as all its digits:
// chaosStartNanos is 2^53+1, which a float64 can't hold.
const resilienceReport = `   {
  "performanceReport": {
    "run": {
      "id": "8f14e45f-ceea-467f-a0e6-5f3c1a2b9d70",
      "testType": "INTEGRITY",
      "status": "RUNNING",
      "createdAt": "2026-10-05T11:58:30.120Z"
    },
    "summary": {"totalRecords": 41500, "avgThroughputRecPerSec": 498.2, "p99LatencyMs": 41.8, "errorRate": 0.0},
    "generatedAt": "2026-10-05T12:01:42.551Z"
  },
  "chaosOutcome": {
    "engineName": "litmus",
    "experimentName": "kafka-pod-kill",
    "chaosStartTime": "2026-10-05T11:59:00.412Z",
    "chaosEndTime": "2026-10-05T11:59:30.412Z",
    "chaosStartNanos": 9007199254740993,
    "chaosDuration": 30.000000000,
    "verdict": "Pass",
    "failureReason": null,
    "probeSuccessPercentage": "100",
    "failStep": null,
    "phase": "Completed"
  },
  "preChaosSummary": {"avgThroughputRecPerSec": 499.6, "p99LatencyMs": 6.0, "errorRate": 0.0},
  "postChaosSummary": {"avgThroughputRecPerSec": 498.2, "p99LatencyMs": 41.8, "errorRate": 0.0},
  "impactDeltas": {"throughputRecPerSec": -0.28, "p99LatencyMs": 596.67, "errorRate": 0.0},
  "status": "COMPLETED",
  "integrityResult": {"lostRecords": 0, "rpoMs": 0.0, "verdict": "PASS"},
  "baselineProbes": [
    {"name": "isr-health-check", "passed": true, "output": "0", "durationMs": 812, "evaluatedAt": "2026-10-05T11:58:59.101Z"},
    {"name": "producer-throughput", "passed": true, "output": "10 records sent", "durationMs": 2310, "evaluatedAt": "2026-10-05T11:59:00.204Z"},
    {"name": "cluster-ready", "passed": true, "output": "{conditions=[{type=Ready, status=True}]}", "durationMs": 95, "evaluatedAt": "2026-10-05T11:59:00.301Z"}
  ],
  "duringChaosProbes": [
    {"name": "isr-health-check", "passed": true, "output": "12", "durationMs": 790, "evaluatedAt": "2026-10-05T11:59:01.204Z"},
    {"name": "producer-throughput", "passed": true, "output": "10 records sent", "durationMs": 2401, "evaluatedAt": "2026-10-05T11:59:03.611Z"},
    {"name": "isr-health-check", "passed": false, "output": "61 (want <= 50)", "durationMs": 803, "evaluatedAt": "2026-10-05T11:59:13.614Z"},
    {"name": "producer-throughput", "passed": true, "output": "10 records sent", "durationMs": 2290, "evaluatedAt": "2026-10-05T11:59:15.905Z"},
    {"name": "isr-health-check", "passed": false, "output": "58 (want <= 50)", "durationMs": 811, "evaluatedAt": "2026-10-05T11:59:25.910Z"},
    {"name": "producer-throughput", "passed": true, "output": "10 records sent", "durationMs": 2330, "evaluatedAt": "2026-10-05T11:59:28.241Z"}
  ],
  "postRecoveryProbes": [
    {"name": "isr-health-check", "passed": true, "output": "3", "durationMs": 798, "evaluatedAt": "2026-10-05T11:59:43.551Z"},
    {"name": "producer-throughput", "passed": true, "output": "10 records sent", "durationMs": 2280, "evaluatedAt": "2026-10-05T11:59:45.832Z"},
    {"name": "cluster-ready", "passed": false, "output": "K8s probe failed: Read timed out\nafter 30000 ms", "durationMs": 30004, "evaluatedAt": "2026-10-05T12:00:15.836Z"}
  ],
  "recoveryTime": 12.345678901
}
`

// resilienceRunAnswered runs kates resilience run in an output mode against a
// Kates API that answers 200 with body. It returns what the command printed
// through the output layer, what it printed straight to stdout, and its
// error. Both go to stdout outside tests.
func resilienceRunAnswered(t *testing.T, mode, body string) (out, stdout string, err error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "r.yaml")
	data := "testRequest:\n  type: LOAD\nchaosSpec:\n  experimentName: x\nsteadyStateSec: 1\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	prevMode := outputMode
	ts, buf := setupTest(t, "POST", "/api/resilience", 200, body)
	defer ts.Close()
	outputMode = mode
	defer func() { outputMode = prevMode }()
	resilienceFile, resilienceDryRun = path, false
	defer func() { resilienceFile = "" }()

	// The panic is reported once captureStdout has put stdout back.
	var panicked any
	stdout = captureStdout(t, func() {
		defer func() { panicked = recover() }()
		err = resilienceRunCmd.RunE(resilienceRunCmd, nil)
	})
	if panicked != nil {
		t.Fatalf("kates resilience run -o %s panicked on the answer %q: %v", mode, body, panicked)
	}
	return buf.String(), stdout, err
}

// resilienceLines is out without its styling, one string per line, each run
// of spaces made one, so a line reads as its words.
func resilienceLines(out string) []string {
	var lines []string
	for _, line := range strings.Split(stripAnsi(out), "\n") {
		lines = append(lines, strings.Join(strings.Fields(line), " "))
	}
	return lines
}

// -o json prints the report as the Kates API sent it. It used to print the
// struct the CLI reads the report into, which dropped the recovery time,
// every probe result and the test run with its id, and it printed the
// progress line ahead of the JSON.
func TestResilienceRun_JSONPrintsTheReportAsSent(t *testing.T) {
	out, stdout, err := resilienceRunAnswered(t, "json", resilienceReport)
	if err != nil {
		t.Fatalf("kates resilience run -o json: %v", err)
	}
	if stdout != "" {
		t.Errorf("stdout carries more than the report:\n%s", stdout)
	}

	decode := func(text string) any {
		dec := json.NewDecoder(strings.NewReader(text))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, text)
		}
		if _, err := dec.Token(); err != io.EOF {
			t.Fatalf("more than one JSON value:\n%s", text)
		}
		return v
	}
	// UseNumber keeps each number's text, so a number re-encoded through a
	// float64 (2^53+1, or 30.000000000 as 30) differs here.
	if got, want := decode(out), decode(resilienceReport); !reflect.DeepEqual(got, want) {
		t.Errorf("-o json printed another report:\n%s", out)
	}
	if !strings.Contains(out, `"61 (want <= 50)"`) {
		t.Errorf("-o json escaped a probe's output:\n%s", out)
	}
}

// The table names the test run for kates test get, gives the recovery time,
// counts each phase's passing probes, and lists each probe that failed with
// what it printed last. During the fault a Continuous probe runs again and
// again, so its row counts its failures.
func TestResilienceRun_PrintsTheRunTheRecoveryAndTheProbes(t *testing.T) {
	out, _, err := resilienceRunAnswered(t, "table", resilienceReport)
	if err != nil {
		t.Fatalf("kates resilience run: %v", err)
	}
	lines := resilienceLines(out)
	for _, want := range []string{
		"Test Run 8f14e45f-ceea-467f-a0e6-5f3c1a2b9d70",
		"Recovery Time 12346 ms",
		"Baseline 3/3 passed",
		"During the fault 4/6 passed",
		"After recovery 2/3 passed",
		"Phase Probe Failed Output",
		"During the fault isr-health-check 2/3 58 (want <= 50)",
		// A line break in a probe's output would split its row.
		"After recovery cluster-ready 1/1 K8s probe failed: Read timed out after 30000 ms",
		"Full details: kates test get 8f14e45f-ceea-467f-a0e6-5f3c1a2b9d70",
	} {
		if !slices.Contains(lines, want) {
			t.Errorf("no line %q in:\n%s", want, stripAnsi(out))
		}
	}
	if strings.Contains(out, "61 (want <= 50)") {
		t.Errorf("printed a failure older than the probe's last one:\n%s", stripAnsi(out))
	}
}

// With every probe passing there is nothing to list, and a fault with no
// Continuous probe has none run during it, which the table says rather than
// "0/0 passed".
func TestResilienceRun_SaysWhenNoProbeRanDuringTheFault(t *testing.T) {
	report := `{"status":"COMPLETED",
	  "chaosOutcome":{"experimentName":"kafka-pod-kill","verdict":"Pass","chaosDuration":30.000000000},
	  "baselineProbes":[{"name":"cluster-ready","passed":true,"output":"Ready"}],
	  "duringChaosProbes":[],
	  "postRecoveryProbes":[{"name":"cluster-ready","passed":true,"output":"Ready"}],
	  "recoveryTime":0.951000000,
	  "performanceReport":{"run":{"id":"run-1"}}}`
	out, _, err := resilienceRunAnswered(t, "table", report)
	if err != nil {
		t.Fatalf("kates resilience run: %v", err)
	}
	lines := resilienceLines(out)
	for _, want := range []string{
		"Recovery Time 951 ms",
		"Baseline 1/1 passed",
		"During the fault none ran: only Continuous probes run during the fault",
		"After recovery 1/1 passed",
	} {
		if !slices.Contains(lines, want) {
			t.Errorf("no line %q in:\n%s", want, stripAnsi(out))
		}
	}
	if slices.Contains(lines, "Phase Probe Failed Output") {
		t.Errorf("listed failing probes when none failed:\n%s", stripAnsi(out))
	}
}

// An answer without a report is an error in either output mode. An empty one
// used to reach the command as a nil report, which the table dereferenced and
// -o json printed as null. The Kates API sends one when it fails to write the
// report: the keep-alive spaces, then nothing.
func TestResilienceRun_RefusesAnAnswerWithNoReport(t *testing.T) {
	for _, tt := range []struct {
		name, body, want string
	}{
		{"empty", "", "answered with no report"},
		{"keep-alive spaces only", "          ", "answered with no report"},
		{"null", "null", "not a resilience report"},
		{"an object without a status", "{}", "not a resilience report"},
		{"not JSON", "<html>502 Bad Gateway</html>", "not a resilience report"},
		{"cut short", `   {"status":"COMPLETED","chaosOutcome":{`, "not a resilience report"},
		{"not an object", `["COMPLETED"]`, "not a resilience report"},
	} {
		for _, mode := range []string{"table", "json"} {
			t.Run(tt.name+" -o "+mode, func(t *testing.T) {
				out, _, err := resilienceRunAnswered(t, mode, tt.body)
				if err == nil {
					t.Fatalf("no error; printed:\n%s", out)
				}
				if !strings.Contains(err.Error(), tt.want) {
					t.Errorf("err = %q, want it to say %q", err, tt.want)
				}
			})
		}
	}
}
