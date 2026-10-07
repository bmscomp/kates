package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"gopkg.in/yaml.v3"
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

// A report that ended ERROR after its test run started names the run, so the
// run's task errors, and an INTEGRITY run's verdict, are a kates test get
// away. The id used to come only in the performance report, which a run that
// ended before the recovery wait doesn't have. The test above reads the id
// from there, as an API without testRunId sends it.
func TestResilienceRun_NamesTheRunOfAReportThatErred(t *testing.T) {
	out, _, err := resilienceRunAnswered(t, "table",
		`{"status":"ERROR","error":"java.util.concurrent.TimeoutException","testRunId":"3f6c2a1e"}`)
	if err != nil {
		t.Fatalf("kates resilience run: %v", err)
	}
	lines := resilienceLines(out)
	for _, want := range []string{
		"Test Run 3f6c2a1e",
		"Error java.util.concurrent.TimeoutException",
		"Full details: kates test get 3f6c2a1e",
	} {
		if !slices.Contains(lines, want) {
			t.Errorf("no line %q in:\n%s", want, stripAnsi(out))
		}
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

// A run whose probes never all passed in one poll has no recovery time. The
// table says so, with how long after the fault the last poll ended: the
// Kates API used to report that wait as the recovery time.
func TestResilienceRun_SaysWhenTheRunDidNotRecover(t *testing.T) {
	report := `{"status":"COMPLETED",
	  "chaosOutcome":{"experimentName":"kafka-pod-kill","verdict":"Pass","chaosDuration":30.000000000},
	  "baselineProbes":[{"name":"isr-health-check","passed":true,"output":"0"}],
	  "duringChaosProbes":[{"name":"isr-health-check","passed":false,"output":"61"}],
	  "postRecoveryProbes":[{"name":"isr-health-check","passed":false,"output":"58"}],
	  "unrecoveredAfter":115.250000000,
	  "performanceReport":{"run":{"id":"run-1"}}}`
	out, _, err := resilienceRunAnswered(t, "table", report)
	if err != nil {
		t.Fatalf("kates resilience run: %v", err)
	}
	lines := resilienceLines(out)
	at := slices.IndexFunc(lines, func(line string) bool { return strings.HasPrefix(line, "Recovery Time ") })
	if at < 0 || !strings.HasSuffix(lines[at], "NOT RECOVERED 115250 ms after the fault") {
		t.Errorf("no line saying the run did not recover in:\n%s", stripAnsi(out))
	}
	for _, want := range []string{
		"After recovery 0/1 passed",
		"After recovery isr-health-check 1/1 58",
	} {
		if !slices.Contains(lines, want) {
			t.Errorf("no line %q in:\n%s", want, stripAnsi(out))
		}
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

// writeResilienceFile writes data to a file of that name in a fresh
// directory and returns its path.
func writeResilienceFile(t *testing.T, name, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// resilienceRunPosting runs kates resilience run on the file at path against
// a backend that answers COMPLETED, and returns the body it posted.
func resilienceRunPosting(t *testing.T, path string) []byte {
	t.Helper()
	var posted []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/resilience" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		posted = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"COMPLETED"}`))
	}))
	defer ts.Close()
	apiClient = client.New(ts.URL)
	outputMode = "table"
	output.ResetForTesting()
	resilienceFile, resilienceDryRun = path, false
	defer func() { resilienceFile = "" }()

	if err := resilienceRunCmd.RunE(resilienceRunCmd, nil); err != nil {
		t.Fatalf("kates resilience run -f %s: %v", path, err)
	}
	if posted == nil {
		t.Fatalf("kates resilience run -f %s posted nothing", path)
	}
	return posted
}

// asJSON is v as encoding/json reads it back, the form a posted body takes
// once decoded: numbers become float64.
func asJSON(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// checkSentAsWritten fails unless body, the JSON a run posted, holds what the
// YAML file says, each field once, and nothing else.
func checkSentAsWritten(t *testing.T, path string, body []byte) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file any
	if err := yaml.Unmarshal(data, &file); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	var sent any
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("posted %q: %v", body, err)
	}
	if want := asJSON(t, file); !reflect.DeepEqual(sent, want) {
		t.Errorf("%s:\nposted %v\nfile   %v", path, sent, want)
	}
}

// The file is the request. Decoded into a struct, a probe lost its mode,
// expectedOutput, comparator, intervalSec and timeoutSec, so the API ran each
// as an Edge probe that passed whatever its command printed; a scenario was
// dropped; gracePeriodSec: 0 and targetBrokerId: 0 were left out, which the
// API reads as 30 seconds and a random broker; and a file without
// steadyStateSec sent 0 for it, where the API waits 30 seconds.
func TestResilienceRun_SendsTheFileAsWritten(t *testing.T) {
	path := writeResilienceFile(t, "r.yaml", `testRequest:
  type: LOAD
  scenario:
    name: two-topics
    phases:
      - name: steady
        phaseType: STEADY
        durationMs: 60000
chaosSpec:
  experimentName: x
  disruptionType: POD_DELETE
  gracePeriodSec: 0
  targetBrokerId: 0
probes:
  - name: isr
    type: cmdProbe
    mode: Continuous
    command: "echo 0"
    expectedOutput: "0"
    comparator: "<="
    intervalSec: 5
    timeoutSec: 20
`)
	body := resilienceRunPosting(t, path)
	checkSentAsWritten(t, path, body)

	var sent map[string]any
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatal(err)
	}
	if v, ok := sent["steadyStateSec"]; ok {
		t.Errorf("steadyStateSec is sent as %v; the file has none, so the API's default applies", v)
	}
}

// Every resilience example cli/examples ships reaches the Kates API whole.
// ResilienceExamplesTest (kates/src/test) checks that the API reads each
// field of it as the example means it.
func TestResilienceRun_SendsEveryExampleAsWritten(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "examples", "resilience-*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) < 12 {
		t.Fatalf("found only %d resilience examples: %v", len(paths), paths)
	}
	for _, path := range paths {
		checkSentAsWritten(t, path, resilienceRunPosting(t, path))
	}
}

// A .json file's numbers reach the API as written, not through a float64,
// which holds integers exactly only up to 2^53.
func TestResilienceRun_SendsAJSONFilesNumbersAsWritten(t *testing.T) {
	path := writeResilienceFile(t, "r.json",
		`{"testRequest":{"type":"LOAD","spec":{"numRecords":9007199254740993}},"chaosSpec":{"experimentName":"x"}}`)

	if body := resilienceRunPosting(t, path); !strings.Contains(string(body), `"numRecords":9007199254740993`) {
		t.Errorf("posted %s", body)
	}
}

// A scenario may carry the test's type itself: the Kates API takes
// testRequest.scenario.type in place of testRequest.type.
func TestResilienceRun_TakesTheTypeFromTheScenario(t *testing.T) {
	path := writeResilienceFile(t, "r.yaml", `testRequest:
  scenario:
    type: LOAD
    phases:
      - name: steady
        phaseType: STEADY
chaosSpec:
  experimentName: x
`)
	checkSentAsWritten(t, path, resilienceRunPosting(t, path))
}

// --dry-run prints the body a run would send, which is the file, with its
// comparators as written rather than escaped for HTML.
func TestResilienceRun_DryRunPrintsTheFile(t *testing.T) {
	path := writeResilienceFile(t, "r.yaml", `testRequest:
  type: LOAD
chaosSpec:
  experimentName: x
probes:
  - name: isr
    mode: Continuous
    comparator: "<="
    timeoutSec: 20
`)
	output.ResetForTesting()
	resilienceFile, resilienceDryRun = path, true
	defer func() { resilienceFile, resilienceDryRun = "", false }()

	out := captureStdout(t, func() {
		if err := resilienceRunCmd.RunE(resilienceRunCmd, nil); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
	for _, want := range []string{`"mode": "Continuous"`, `"comparator": "<="`, `"timeoutSec": 20`} {
		if !strings.Contains(out, want) {
			t.Errorf("the dry run does not print %s:\n%s", want, out)
		}
	}
}

// A file the command can't send as the request it says is refused before
// anything is sent.
func TestResilienceRun_RefusesAFileItCannotSend(t *testing.T) {
	for _, tc := range []struct{ name, file, data, want string }{
		{"no type", "r.yaml", "testRequest:\n  spec: {}\nchaosSpec:\n  experimentName: x\n", "testRequest.type is required"},
		{"no experiment name", "r.yaml", "testRequest:\n  type: LOAD\nchaosSpec: {}\n", "chaosSpec.experimentName is required"},
		{"a value JSON can't hold", "r.yaml", "testRequest:\n  type: LOAD\nchaosSpec:\n  experimentName: x\n  networkLatencyMs: .inf\n", "doesn't convert to JSON"},
		{"two JSON values", "r.json", `{"testRequest":{"type":"LOAD"},"chaosSpec":{"experimentName":"x"}} {}`, "after the top-level JSON value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				_, _ = w.Write([]byte(`{"status":"COMPLETED"}`))
			}))
			defer ts.Close()
			apiClient = client.New(ts.URL)
			output.ResetForTesting()
			resilienceFile, resilienceDryRun = writeResilienceFile(t, tc.file, tc.data), false
			defer func() { resilienceFile = "" }()

			err := resilienceRunCmd.RunE(resilienceRunCmd, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one that says %q", err, tc.want)
			}
		})
	}
}
