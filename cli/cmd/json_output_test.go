package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmscomp/kates/cli/client"
)

// runsBackend is a fake Kates API for the commands that create, poll and
// grade test runs. Each created run answers RUNNING to its first poll and
// then the final run the test gave for its type. Reports, report summaries
// and tuning reports are served from the maps; anything else fails the test.
type runsBackend struct {
	t *testing.T

	final    map[string]string // test type → final run JSON, with "{{id}}" for the run id
	reports  map[string]string // run id → GET /report
	summary  map[string]string // run id → GET /report/summary
	tuning   map[string]string // run id → GET /report/tuning
	runs     map[string]string // run id → GET /api/tests/{id} for runs the test did not create
	stuck    map[string]bool   // test type → its runs answer RUNNING to every poll
	failPost bool

	mu      sync.Mutex
	created []string          // test types, in the order created
	typeOf  map[string]string // created run id → test type
	polls   map[string]int
}

func newRunsBackend(t *testing.T) *runsBackend {
	t.Helper()
	b := &runsBackend{
		t: t, final: map[string]string{}, reports: map[string]string{}, summary: map[string]string{},
		tuning: map[string]string{}, runs: map[string]string{}, stuck: map[string]bool{}, typeOf: map[string]string{},
		polls: map[string]int{},
	}
	ts := httptest.NewServer(http.HandlerFunc(b.serve))
	t.Cleanup(ts.Close)
	apiClient = client.New(ts.URL)
	apiClient.MaxRetries = 1

	origTest, origApply, origInteractive := testPollInterval, applyPollInterval, interactiveAllowedFn
	testPollInterval, applyPollInterval = time.Millisecond, time.Millisecond
	// No terminal, as for a script or an agent; the TUI tests are elsewhere.
	interactiveAllowedFn = func() bool { return false }
	b.resetFlags()
	t.Cleanup(func() {
		b.resetFlags()
		testPollInterval, applyPollInterval, interactiveAllowedFn = origTest, origApply, origInteractive
	})
	return b
}

// resetFlags puts every variable a command line in this file can set back to
// its default, as a new process would start. Flags stay set between two
// runCommandLine calls otherwise: a second call without -o json would print
// JSON again.
func (b *runsBackend) resetFlags() {
	outputMode = "table"
	gateMinGrade, gateType, gateRecords, gateBackend, gateTimeout = "C", "LOAD", 50000, "", 180
	benchRecords = 50000
	applyFile, applyWait = "", false
	advisorApply = false
}

func (b *runsBackend) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	notFound := func() {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"status":404,"error":"Not Found","message":"not found"}`)
	}
	serveFrom := func(m map[string]string, id string) {
		if body, ok := m[id]; ok {
			_, _ = io.WriteString(w, body)
			return
		}
		notFound()
	}

	path := r.URL.Path
	switch {
	case r.Method == http.MethodPost && path == "/api/tests":
		if b.failPost {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"status":503,"error":"Unavailable","message":"engine busy"}`)
			return
		}
		var req client.CreateTestRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			b.t.Errorf("POST /api/tests: %v", err)
		}
		b.mu.Lock()
		b.created = append(b.created, req.TestType)
		id := fmt.Sprintf("run-%d", len(b.created))
		b.typeOf[id] = req.TestType
		b.mu.Unlock()
		_, _ = fmt.Fprintf(w, `{"id":%q,"testType":%q,"status":"PENDING"}`, id, req.TestType)
	case r.Method == http.MethodGet && path == "/api/tests/tuning/types":
		_, _ = io.WriteString(w, `[{"type":"TUNE_BATCHING","parameter":"batch.size","steps":4,"description":"Sweep batch.size"}]`)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/api/tests/"):
		rest := strings.TrimPrefix(path, "/api/tests/")
		id, sub, _ := strings.Cut(rest, "/")
		switch sub {
		case "":
			b.mu.Lock()
			testType, created := b.typeOf[id]
			b.polls[id]++
			first := b.polls[id] == 1
			b.mu.Unlock()
			switch {
			case !created:
				serveFrom(b.runs, id)
			case first || b.stuck[testType]:
				_, _ = fmt.Fprintf(w, `{"id":%q,"testType":%q,"status":"RUNNING"}`, id, testType)
			default:
				final, ok := b.final[testType]
				if !ok {
					b.t.Errorf("no final run for type %s", testType)
					notFound()
					return
				}
				_, _ = io.WriteString(w, strings.ReplaceAll(final, "{{id}}", id))
			}
		case "report":
			serveFrom(b.reports, id)
		case "report/summary":
			serveFrom(b.summary, id)
		case "report/tuning":
			serveFrom(b.tuning, id)
		default:
			b.t.Errorf("unexpected request %s %s", r.Method, r.URL)
			notFound()
		}
	default:
		b.t.Errorf("unexpected request %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusTeapot)
	}
}

// createdTypes returns the test types created so far, in order.
func (b *runsBackend) createdTypes() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.created...)
}

// decodeStdout reads a command's stdout as one JSON document into v, and
// fails the test when anything else is on it: a progress line, a banner, a
// second document.
func decodeStdout(t *testing.T, stdout []byte, v any) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(string(stdout)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("stdout is not the JSON result: %v\n%s", err, stdout)
	}
	if dec.More() {
		t.Fatalf("stdout carries more than one JSON document:\n%s", stdout)
	}
	if _, err := dec.Token(); err != io.EOF {
		t.Fatalf("stdout carries something after the JSON document:\n%s", stdout)
	}
}

// A finished LOAD run with two phases, one of them failed with an error the
// hint table knows.
const explainRunJSON = `{"id":"run-abc","testType":"LOAD","status":"FAILED","results":[
 {"phaseName":"produce","status":"DONE","recordsSent":40000,"throughputRecordsPerSec":25000,"p99LatencyMs":12.5},
 {"phaseName":"consume","status":"FAILED","recordsSent":1000,"throughputRecordsPerSec":900,"p99LatencyMs":80,
  "error":"org.apache.kafka.common.errors.TimeoutException: expired"}]}`

func TestExplain_JSON(t *testing.T) {
	b := newRunsBackend(t)
	b.runs["run-abc"] = explainRunJSON

	stdout, _, err := commandOutput(t, func() error { return runCommandLine(t, "explain", "run-abc", "-o", "json") })
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	var got explainResult
	decodeStdout(t, stdout, &got)
	f := func(v float64) *float64 { return &v }
	want := explainResult{
		RunID: "run-abc", TestType: "LOAD", Status: "FAILED",
		TypeDescription: "Standard load test with target throughput",
		Narrative: []string{
			"Your LOAD test failed. 1 of 2 phases encountered errors.",
			"Before failure, 41.0K records were processed.",
		},
		Phases: 2, FailedPhases: 1, TotalRecords: 41000,
		PeakThroughputRecPerSec: f(25000), BestP99LatencyMs: f(12.5), WorstP99LatencyMs: f(80),
		Errors: []explainError{{
			Message: "org.apache.kafka.common.errors.TimeoutException: expired",
			Hints:   []string{"Operation timed out — increase timeout or check broker health"},
		}},
		Verdict: "POOR", VerdictReason: "Test failed. Review errors above and re-run after fixing.",
	}
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		t.Errorf("explain -o json =\n %s\nwant\n %s", gotJSON, wantJSON)
	}

	// No phase reported a metric: null, not 0, which would be a reading.
	b.runs["run-empty"] = `{"id":"run-empty","testType":"LOAD","status":"RUNNING","results":[{"phaseName":"produce","status":"RUNNING"}]}`
	stdout = commandStdout(t, func() error { return runCommandLine(t, "explain", "run-empty", "-o", "json") })
	for _, field := range []string{"peakThroughputRecPerSec", "bestP99LatencyMs", "worstP99LatencyMs"} {
		if !strings.Contains(compactJSON(t, stdout), `"`+field+`":null`) {
			t.Errorf("%s is not null for a run that reported none:\n%s", field, stdout)
		}
	}
}

// TestExplain_TableMatchesJSON checks the table prints what the JSON holds,
// for a failed run and a finished one: every narrative line, the verdict with
// its reason, each error with its hints, and the key metrics. The expected
// text comes from the -o json result, so a change to one mode that the other
// does not follow fails here.
func TestExplain_TableMatchesJSON(t *testing.T) {
	b := newRunsBackend(t)
	b.runs["run-abc"] = explainRunJSON
	b.runs["run-ok"] = `{"id":"run-ok","testType":"STRESS","status":"DONE","results":[
	 {"phaseName":"produce","status":"DONE","recordsSent":90000,"throughputRecordsPerSec":45000,"p99LatencyMs":7.25},
	 {"phaseName":"consume","status":"DONE","recordsSent":90000,"throughputRecordsPerSec":44000,"p99LatencyMs":9.5}]}`

	for _, id := range []string{"run-abc", "run-ok"} {
		t.Run(id, func(t *testing.T) {
			var res explainResult
			decodeStdout(t, commandStdout(t, func() error { return runCommandLine(t, "explain", id, "-o", "json") }), &res)
			b.resetFlags()

			stdout, stderr, err := commandOutput(t, func() error { return runCommandLine(t, "explain", id) })
			if err != nil {
				t.Fatalf("explain: %v", err)
			}
			out, errOut := stripAnsi(string(stdout)), stripAnsi(stderr)
			want := append([]string{}, res.Narrative...)
			want = append(want, res.Verdict+" — "+res.VerdictReason, "kates test get "+res.RunID)
			if res.TypeDescription != "" {
				want = append(want, res.TypeDescription)
			}
			for _, e := range res.Errors {
				if !strings.Contains(errOut, e.Message) {
					t.Errorf("stderr lacks the error %q:\n%s", e.Message, errOut)
				}
				want = append(want, e.Hints...)
			}
			if res.Status == "DONE" && res.PeakThroughputRecPerSec != nil {
				want = append(want, fmtNum(res.TotalRecords), fmtNum(*res.PeakThroughputRecPerSec)+" rec/s")
				if res.WorstP99LatencyMs != nil {
					want = append(want, fmtFloat(*res.WorstP99LatencyMs, 3)+" ms")
				}
			}
			if len(res.Narrative) == 0 || res.Verdict == "" {
				t.Fatalf("the JSON result is empty: %+v", res)
			}
			for _, w := range want {
				if !strings.Contains(out, w) {
					t.Errorf("table lacks %q, which the JSON holds:\n%s", w, out)
				}
			}
		})
	}
}

const advisorRunJSON = `{"id":"run-adv","testType":"LOAD","status":"DONE",
 "spec":{"batchSize":16384,"lingerMs":0,"acks":"1","replicationFactor":3,"compressionType":"gzip"},
 "results":[{"phaseName":"produce","status":"DONE","throughputRecordsPerSec":12000,"p99LatencyMs":20}]}`

func TestAdvisor_JSON(t *testing.T) {
	tests := []struct {
		name      string
		run       string // "" leaves the run unknown to the backend
		report    string // "" leaves the report missing
		want      advisorResult
		wantRules []string // titles, in order
	}{
		{
			name: "analyzed", run: advisorRunJSON, report: `{"summary":{"avgThroughputRecPerSec":12000}}`,
			want: advisorResult{RunID: "run-adv", Status: advisorAnalyzed},
			wantRules: []string{
				"batch.size=16384 is leaving throughput on the table",
				"linger.ms=0 causes excessive small-batch sends",
				"acks=1 with replicationFactor=3 risks data loss",
				"gzip compression has highest CPU overhead",
			},
		},
		{
			name: "run not found",
			want: advisorResult{RunID: "run-adv", Status: advisorRunNotFound, Message: "Test run not found: run-adv"},
		},
		{
			name: "report not ready", run: advisorRunJSON,
			want: advisorResult{RunID: "run-adv", Status: advisorReportNotReady, Message: "Report not available yet for run: run-adv"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newRunsBackend(t)
			if tt.run != "" {
				b.runs["run-adv"] = tt.run
			}
			if tt.report != "" {
				b.reports["run-adv"] = tt.report
			}

			// Exit 0 in every case, as the table has always done.
			stdout := commandStdout(t, func() error { return runCommandLine(t, "advisor", "run-adv", "-o", "json") })
			var got advisorResult
			decodeStdout(t, stdout, &got)
			if got.Recommendations == nil {
				t.Errorf("recommendations = null, want a list")
			}
			var titles []string
			for _, r := range got.Recommendations {
				titles = append(titles, r.Title)
				if r.Severity == "" {
					t.Errorf("rule %q has no severity", r.Title)
				}
			}
			if !reflect.DeepEqual(titles, tt.wantRules) {
				t.Errorf("rules = %q, want %q", titles, tt.wantRules)
			}
			got.Recommendations = nil
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("advisor -o json = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestGate_JSON(t *testing.T) {
	const done = `{"id":"{{id}}","testType":"LOAD","status":"DONE"}`
	tests := []struct {
		name     string
		args     []string
		final    string
		summary  string
		want     gateResult
		wantFail bool
	}{
		{
			name: "pass", args: []string{"--min-grade", "C", "--records", "1000"},
			final: done, summary: `{"avgThroughputRecPerSec":31000,"p99LatencyMs":12.5}`,
			want: gateResult{RunID: "run-1", TestType: "LOAD", Records: 1000, Status: "DONE",
				AvgThroughputRecPerSec: gateF(31000), P99LatencyMs: gateF(12.5), Grade: "B", MinGrade: "C", Passed: true},
		},
		{
			name: "grade below the minimum", args: []string{"--min-grade", "A"},
			final: done, summary: `{"avgThroughputRecPerSec":31000,"p99LatencyMs":12.5}`,
			want: gateResult{RunID: "run-1", TestType: "LOAD", Records: 50000, Status: "DONE",
				AvgThroughputRecPerSec: gateF(31000), P99LatencyMs: gateF(12.5), Grade: "B", MinGrade: "A"},
			wantFail: true,
		},
		{
			name: "run failed", args: nil,
			final:    `{"id":"{{id}}","testType":"LOAD","status":"FAILED"}`,
			want:     gateResult{RunID: "run-1", TestType: "LOAD", Records: 50000, Status: "FAILED", MinGrade: "C", Error: "Test FAILED"},
			wantFail: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newRunsBackend(t)
			b.final["LOAD"] = tt.final
			if tt.summary != "" {
				b.summary["run-1"] = tt.summary
			}

			args := append([]string{"gate", "-o", "json"}, tt.args...)
			stdout, stderr, err := commandOutput(t, func() error { return runCommandLine(t, args...) })
			if tt.wantFail {
				// A silentErr is what Execute turns into exit code 1.
				if _, ok := err.(*silentErr); !ok {
					t.Errorf("err = %v (%T), want a silentErr, which exits 1", err, err)
				}
				if stderr == "" {
					t.Errorf("no message on stderr")
				}
			} else if err != nil {
				t.Fatalf("gate: %v\n%s", err, stderr)
			}
			var got gateResult
			decodeStdout(t, stdout, &got)
			if !reflect.DeepEqual(got, tt.want) {
				gotJSON, _ := json.Marshal(got)
				wantJSON, _ := json.Marshal(tt.want)
				t.Errorf("gate -o json =\n %s\nwant\n %s", gotJSON, wantJSON)
			}
			// A gate that ends without a grade measured nothing: null, not 0.
			if tt.want.Grade == "" && !strings.Contains(compactJSON(t, stdout), `"avgThroughputRecPerSec":null,"p99LatencyMs":null`) {
				t.Errorf("an ungraded gate reports metrics:\n%s", stdout)
			}
		})
	}
}

func gateF(v float64) *float64 { return &v }

func TestBenchmark_JSON(t *testing.T) {
	b := newRunsBackend(t)
	b.final["LOAD"] = `{"id":"{{id}}","testType":"LOAD","status":"DONE","results":[
	 {"phaseName":"produce","status":"DONE","throughputRecordsPerSec":50000,"p99LatencyMs":1},
	 {"phaseName":"consume","status":"DONE","throughputRecordsPerSec":40000,"p99LatencyMs":2}]}`
	// FAILED with no phase error: the scorecard used to call this run DONE.
	b.final["STRESS"] = `{"id":"{{id}}","testType":"STRESS","status":"FAILED","results":[
	 {"phaseName":"produce","status":"FAILED","throughputRecordsPerSec":100,"p99LatencyMs":900}]}`
	b.final["SPIKE"] = `{"id":"{{id}}","testType":"SPIKE","status":"DONE","results":[
	 {"phaseName":"produce","status":"DONE","throughputRecordsPerSec":25000,"p99LatencyMs":4}]}`

	stdout := commandStdout(t, func() error { return runCommandLine(t, "benchmark", "--records", "1000", "-o", "json") })
	var got benchmarkResult
	decodeStdout(t, stdout, &got)

	f := func(v float64) *float64 { return &v }
	want := benchmarkResult{
		RecordsPerTest: 1000,
		Tests: []benchmarkTest{
			{TestType: "LOAD", RunID: "run-1", Status: "DONE", PeakThroughputRecPerSec: f(50000), MaxP99LatencyMs: f(2), Score: f(90), Grade: "A"},
			{TestType: "STRESS", RunID: "run-2", Status: "FAILED", PeakThroughputRecPerSec: f(100), MaxP99LatencyMs: f(900)},
			{TestType: "SPIKE", RunID: "run-3", Status: "DONE", PeakThroughputRecPerSec: f(25000), MaxP99LatencyMs: f(4), Score: f(55), Grade: "F"},
		},
		// The overall grade averages the graded tests: (90 + 55) / 2 = 72.5.
		OverallGrade: "C",
	}
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		t.Errorf("benchmark -o json =\n %s\nwant\n %s", gotJSON, wantJSON)
	}
	if created := b.createdTypes(); !reflect.DeepEqual(created, []string{"LOAD", "STRESS", "SPIKE"}) {
		t.Errorf("created %v, want LOAD, STRESS, SPIKE", created)
	}

	// The table shows the same rows, and the failed run as FAILED.
	b.resetFlags()
	table := stripAnsi(string(commandStdout(t, func() error { return runCommandLine(t, "benchmark", "--records", "1000") })))
	for _, row := range [][]string{
		{"LOAD", "DONE", "50.0K rec/s", "2.00 ms", "A"},
		{"STRESS", "FAILED", "100 rec/s", "900.00 ms", "—"},
		{"SPIKE", "DONE", "25.0K rec/s", "4.00 ms", "F"},
	} {
		if !tableHasRow(table, row) {
			t.Errorf("table lacks row %q:\n%s", row, table)
		}
	}
	if !strings.Contains(table, "Overall Grade") || !strings.Contains(table, "C") {
		t.Errorf("table lacks the overall grade:\n%s", table)
	}
}

// TestBenchmark_LostRun: a run that never reports a final status may still
// be putting load on the cluster. The scorecard called it FAILED and started
// the next test on top of it.
func TestBenchmark_LostRun(t *testing.T) {
	b := newRunsBackend(t)
	b.stuck["LOAD"] = true

	stdout := commandStdout(t, func() error { return runCommandLine(t, "benchmark", "--records", "1000", "-o", "json") })
	var got benchmarkResult
	decodeStdout(t, stdout, &got)
	if created := b.createdTypes(); !reflect.DeepEqual(created, []string{"LOAD"}) {
		t.Errorf("created %v, want LOAD alone: the battery stops while a run may still be going", created)
	}
	if len(got.Tests) != 3 {
		t.Fatalf("tests = %+v, want a row per test type", got.Tests)
	}
	if lost := got.Tests[0]; lost.TestType != "LOAD" || lost.RunID != "run-1" || lost.Status != "ERROR" ||
		!strings.Contains(lost.Error, "may still be going") {
		t.Errorf("LOAD row = %+v, want ERROR with the run id: its outcome is unknown", lost)
	}
	for _, row := range got.Tests[1:] {
		if row.Status != "SKIPPED" || row.RunID != "" || row.Error == "" {
			t.Errorf("%s row = %+v, want SKIPPED with a reason", row.TestType, row)
		}
	}
	if got.OverallGrade != "" {
		t.Errorf("overall grade = %q, want none", got.OverallGrade)
	}
	// Nothing was measured: null, not 0.
	if !strings.Contains(compactJSON(t, stdout), `"status":"ERROR","peakThroughputRecPerSec":null,"maxP99LatencyMs":null`) {
		t.Errorf("the lost run reports metrics:\n%s", stdout)
	}

	b.resetFlags()
	table := stripAnsi(string(commandStdout(t, func() error { return runCommandLine(t, "benchmark", "--records", "1000") })))
	for _, row := range [][]string{{"LOAD", "ERROR", "—", "—", "—"}, {"STRESS", "SKIPPED"}, {"SPIKE", "SKIPPED"}} {
		if !tableHasRow(table, row) {
			t.Errorf("table lacks row %q:\n%s", row, table)
		}
	}
}

func TestTune_JSON(t *testing.T) {
	b := newRunsBackend(t)
	b.tuning["run-t"] = `{"testType":"TUNE_BATCHING","parameterName":"batch.size","bestStepIndex":1,
	 "recommendation":"Use batch.size=65536","steps":[
	 {"stepIndex":0,"label":"16384","metrics":{"avgThroughputRecPerSec":1000,"p99LatencyMs":5,"errorRate":0}},
	 {"stepIndex":1,"label":"65536","metrics":{"avgThroughputRecPerSec":3000,"p99LatencyMs":6,"errorRate":0.001}},
	 {"stepIndex":2,"label":"262144"}]}`

	t.Run("report", func(t *testing.T) {
		stdout := commandStdout(t, func() error { return runCommandLine(t, "tune", "report", "run-t", "-o", "json") })
		var got tuneReportResult
		decodeStdout(t, stdout, &got)
		f := func(v float64) *float64 { return &v }
		want := tuneReportResult{
			RunID: "run-t", TestType: "TUNE_BATCHING", ParameterName: "batch.size", BestStepIndex: 1,
			Recommendation: "Use batch.size=65536",
			Steps: []tuneStepRow{
				{StepIndex: 0, Label: "16384", AvgThroughputRecPerSec: f(1000), P99LatencyMs: f(5), ErrorRate: f(0), Verdict: "WORST"},
				{StepIndex: 1, Label: "65536", AvgThroughputRecPerSec: f(3000), P99LatencyMs: f(6), ErrorRate: f(0.001), Verdict: "BEST"},
				{StepIndex: 2, Label: "262144"},
			},
		}
		if !reflect.DeepEqual(got, want) {
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(want)
			t.Errorf("tune report -o json =\n %s\nwant\n %s", gotJSON, wantJSON)
		}
		// A metric the step lacks is null, not 0, which would be a reading.
		if !strings.Contains(compactJSON(t, stdout), `"label":"262144","avgThroughputRecPerSec":null`) {
			t.Errorf("a missing metric is not null:\n%s", stdout)
		}

		b.resetFlags()
		table := stripAnsi(string(commandStdout(t, func() error { return runCommandLine(t, "tune", "report", "run-t") })))
		for _, row := range [][]string{
			{"16384", "1000 rec/s", "5.0 ms", "0.000%", "WORST"},
			{"65536", "3000 rec/s", "6.0 ms", "0.100%", "BEST"},
			{"262144", "—", "—", "—"},
		} {
			if !tableHasRow(table, row) {
				t.Errorf("table lacks row %q:\n%s", row, table)
			}
		}
	})

	t.Run("run", func(t *testing.T) {
		stdout := commandStdout(t, func() error { return runCommandLine(t, "tune", "run", "batching", "-o", "json") })
		var got client.TestRun
		decodeStdout(t, stdout, &got)
		if got.ID != "run-1" || got.TestType != "TUNE_BATCHING" {
			t.Errorf("tune run -o json = %+v, want run-1 of TUNE_BATCHING", got)
		}
	})

	t.Run("types", func(t *testing.T) {
		stdout := commandStdout(t, func() error { return runCommandLine(t, "tune", "types", "-o", "json") })
		var got []client.TuningTypeInfo
		decodeStdout(t, stdout, &got)
		want := []client.TuningTypeInfo{{Type: "TUNE_BATCHING", Parameter: "batch.size", Steps: 4, Description: "Sweep batch.size"}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("tune types -o json = %+v, want %+v", got, want)
		}
	})
}

// applyScenarios is a file with a scenario that meets its gates, one that
// violates one, and one with no gates.
const applyScenarios = `scenarios:
  - name: fast
    type: load
    spec: {records: 1000}
    validate: {maxP99LatencyMs: 50, maxRtoMs: 1000}
  - name: slow
    type: STRESS
    validate: {maxP99LatencyMs: 50}
  - name: ungated
    type: SPIKE
`

func writeScenarioFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scenarios.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func applyBackend(t *testing.T) *runsBackend {
	b := newRunsBackend(t)
	b.final["LOAD"] = `{"id":"{{id}}","testType":"LOAD","status":"DONE","results":[{"phaseName":"produce","status":"DONE","p99LatencyMs":20}]}`
	b.final["STRESS"] = `{"id":"{{id}}","testType":"STRESS","status":"DONE","results":[{"phaseName":"produce","status":"DONE","p99LatencyMs":210}]}`
	b.final["SPIKE"] = `{"id":"{{id}}","testType":"SPIKE","status":"DONE","results":[{"phaseName":"produce","status":"DONE","p99LatencyMs":900}]}`
	return b
}

func TestApply_JSON(t *testing.T) {
	applyBackend(t)
	file := writeScenarioFile(t, applyScenarios)

	stdout, stderr, err := commandOutput(t, func() error {
		return runCommandLine(t, "test", "apply", "-f", file, "--wait", "-o", "json")
	})
	// The violated gate still exits 1 under -o json.
	if _, ok := err.(*silentErr); !ok {
		t.Errorf("err = %v (%T), want a silentErr, which exits 1", err, err)
	}
	var got applyResult
	decodeStdout(t, stdout, &got)
	want := applyResult{File: file, Waited: true, Scenarios: []applyScenarioResult{
		{Name: "fast", Type: "LOAD", RunID: "run-1", Status: "DONE", SLA: &applySLAResult{
			Violations: []string{}, NotEvaluable: []string{"maxRtoMs (RTO not measured)"}}},
		{Name: "slow", Type: "STRESS", RunID: "run-2", Status: "DONE", SLA: &applySLAResult{
			Violations: []string{"p99=210ms > 50ms"}}},
		{Name: "ungated", Type: "SPIKE", RunID: "run-3", Status: "DONE"},
	}}
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		t.Errorf("test apply -o json =\n %s\nwant\n %s", gotJSON, wantJSON)
	}
	// Nothing but the error: no progress lines under -o json.
	if strings.Contains(stderr, "RUNNING") || strings.Contains(stderr, "fast") {
		t.Errorf("progress on stderr under -o json:\n%s", stderr)
	}
}

// TestApply_WaitWithoutTerminal is the path a CI job or an agent's shell
// takes: no terminal, so no Bubble Tea program. Before, --wait started one
// anyway, it failed to open /dev/tty, and every scenario ended as ERROR.
func TestApply_WaitWithoutTerminal(t *testing.T) {
	applyBackend(t)
	file := writeScenarioFile(t, applyScenarios)

	stdout, stderr, err := commandOutput(t, func() error {
		return runCommandLine(t, "test", "apply", "-f", file, "--wait")
	})
	if _, ok := err.(*silentErr); !ok {
		t.Errorf("err = %v (%T), want a silentErr, which exits 1", err, err)
	}
	for _, line := range []string{
		"fast (run-1): RUNNING", "fast (run-1): DONE",
		"slow (run-2): RUNNING", "slow (run-2): DONE",
		"ungated (run-3): DONE",
	} {
		if !strings.Contains(stderr, line) {
			t.Errorf("stderr lacks progress line %q:\n%s", line, stderr)
		}
	}
	if strings.Contains(stderr, "\x1b[") {
		t.Errorf("progress lines carry escape sequences:\n%q", stderr)
	}
	table := stripAnsi(string(stdout))
	for _, row := range [][]string{
		{"fast", "run-1", "DONE", "not evaluable: maxRtoMs (RTO not measured)"},
		{"slow", "run-2", "DONE", "p99=210ms > 50ms"},
		{"ungated", "run-3", "DONE"},
	} {
		if !tableHasRow(table, row) {
			t.Errorf("summary lacks row %q:\n%s", row, table)
		}
	}
	if strings.Contains(table, "ERROR") || strings.Contains(table, "/dev/tty") {
		t.Errorf("a scenario was lost while waiting:\n%s", table)
	}
	if !strings.Contains(stderr, "One or more SLA gates violated") {
		t.Errorf("stderr lacks the violation:\n%s", stderr)
	}
}

// TestApply_WhichWait checks where apply --wait draws its spinner: only in a
// terminal, and never under -o json, where its frames would land in the JSON.
// The Bubble Tea program is replaced, so nothing opens /dev/tty here.
func TestApply_WhichWait(t *testing.T) {
	tests := []struct {
		name        string
		interactive bool
		args        []string
		wantTUI     bool
	}{
		{"terminal", true, nil, true},
		{"terminal with -o json", true, []string{"-o", "json"}, false},
		{"no terminal (or --plain)", false, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			applyBackend(t)
			file := writeScenarioFile(t, "scenarios:\n  - name: solo\n    type: LOAD\n")
			interactiveAllowedFn = func() bool { return tt.interactive }
			origTUI := waitForTestTUI
			t.Cleanup(func() { waitForTestTUI = origTUI })
			usedTUI := false
			waitForTestTUI = func(id, name string) (*client.TestRun, error) {
				usedTUI = true
				return &client.TestRun{ID: id, TestType: "LOAD", Status: "DONE"}, nil
			}

			args := append([]string{"test", "apply", "-f", file, "--wait"}, tt.args...)
			commandStdout(t, func() error { return runCommandLine(t, args...) })
			if usedTUI != tt.wantTUI {
				t.Errorf("used the spinner = %v, want %v", usedTUI, tt.wantTUI)
			}
		})
	}
}

// TestApply_JSONWithoutWait covers the other statuses: a submitted run and
// one the backend refused.
func TestApply_JSONWithoutWait(t *testing.T) {
	b := applyBackend(t)
	b.failPost = true
	file := writeScenarioFile(t, "scenarios:\n  - name: solo\n    type: LOAD\n")

	stdout, _, err := commandOutput(t, func() error {
		return runCommandLine(t, "test", "apply", "-f", file, "-o", "json")
	})
	if _, ok := err.(*silentErr); !ok {
		t.Errorf("err = %v (%T), want a silentErr, which exits 1", err, err)
	}
	var got applyResult
	decodeStdout(t, stdout, &got)
	if len(got.Scenarios) != 1 || got.Scenarios[0].Status != "FAILED" || !strings.Contains(got.Scenarios[0].Error, "engine busy") {
		t.Errorf("scenarios = %+v, want one FAILED with the backend's message", got.Scenarios)
	}

	b.failPost = false
	stdout = commandStdout(t, func() error { return runCommandLine(t, "test", "apply", "-f", file, "-o", "json") })
	decodeStdout(t, stdout, &got)
	if len(got.Scenarios) != 1 || got.Scenarios[0].Status != "SUBMITTED" || got.Scenarios[0].RunID == "" || got.Waited {
		t.Errorf("result = %+v, want one SUBMITTED scenario with its id", got)
	}
}
