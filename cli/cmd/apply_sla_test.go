package cmd

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bmscomp/kates/cli/client"
)

func producers(n int, throughput, p99 float64) []client.PhaseResult {
	rows := make([]client.PhaseResult, n)
	for i := range rows {
		rows[i] = client.PhaseResult{PhaseName: "produce", RecordsSent: 1000, ThroughputRecordsPerSec: throughput, P99LatencyMs: p99, AvgLatencyMs: p99 / 4}
	}
	return rows
}

// The shipped stress-test asks 16 producers for 100,000 rec/s. Per task row
// that was 100,000 from each, 1.6M in all; at the report scope it is the 16
// together.
func TestValidateSLAsAggregate_ProducersAddUp(t *testing.T) {
	run := &client.TestRun{Status: "DONE", Results: producers(16, 7000, 150)}
	v := &ValidationSpec{MaxP99Latency: 200, MinThroughput: 100000}
	if got := validateSLAsAggregate(run, v); len(got) != 0 {
		t.Errorf("aggregate: %q, want none: 16 × 7000 = 112,000 rec/s", got)
	}
	if got := validateSLAs(run, v); len(got) != 16 {
		t.Errorf("per task: %d violations, want one per producer: %q", len(got), got)
	}
	run.Results = producers(16, 6000, 250)
	want := []string{"p99=250ms > 200ms", "throughput=96000 < 100000 rec/s"}
	if got := validateSLAsAggregate(run, v); !slices.Equal(got, want) {
		t.Errorf("aggregate: %q, want %q", got, want)
	}
}

// A LOAD run's consumer row is left out, as latencyOf leaves it out: its
// rate is not the producer's, and on the native backend it records no
// latency. Per task it failed a minThroughputRecPerSec its producer met.
func TestValidateSLAsAggregate_ConsumerRowLeftOut(t *testing.T) {
	run := &client.TestRun{Status: "DONE", Results: []client.PhaseResult{
		{PhaseName: "produce", RecordsSent: 50000, ThroughputRecordsPerSec: 9000, P99LatencyMs: 40, AvgLatencyMs: 8},
		{PhaseName: "consume", RecordsSent: 50000, ThroughputRecordsPerSec: 800, P99LatencyMs: 900},
	}}
	v := &ValidationSpec{MaxP99Latency: 50, MinThroughput: 5000}
	if got := validateSLAsAggregate(run, v); len(got) != 0 {
		t.Errorf("aggregate: %q, want none", got)
	}
	if got := validateSLAs(run, v); !slices.Equal(got, []string{"p99=900ms > 50ms", "throughput=800 < 5000 rec/s"}) {
		t.Errorf("per task: %q", got)
	}
	// Rows of consumers alone keep them, as latencyRows does.
	run.Results = run.Results[1:]
	if got := validateSLAsAggregate(run, v); !slices.Equal(got, []string{"p99=900ms > 50ms", "throughput=800 < 5000 rec/s"}) {
		t.Errorf("consumers alone: %q", got)
	}
}

func TestValidateSLAsAggregate_FailedUnmeasuredAndIntegrity(t *testing.T) {
	run := &client.TestRun{Status: "FAILED", Results: []client.PhaseResult{{PhaseName: "produce", Error: "boom"}}}
	want := []string{"run FAILED: boom", "p99 not measured"}
	if got := validateSLAsAggregate(run, &ValidationSpec{MaxP99Latency: 50}); !slices.Equal(got, want) {
		t.Errorf("failed: %q, want %q", got, want)
	}
	rto := 900.0
	run = &client.TestRun{Status: "DONE", Results: []client.PhaseResult{
		{PhaseName: "produce", P99LatencyMs: 10, Integrity: &client.IntegrityResult{DataLossPercent: 0.1, OutOfOrderCount: 1, MaxRtoMs: &rto}},
		{PhaseName: "consume", Integrity: &client.IntegrityResult{DataLossPercent: 0.3, OutOfOrderCount: 2, CrcFailures: 1}},
	}}
	want = []string{"dataLoss=0.3000% > 0.0000%", "rto=900ms > 500ms", "outOfOrder=3 > 2", "crcFail=1 > 0"}
	if got := validateSLAsAggregate(run, &ValidationSpec{MaxOutOfOrder: 2, MaxRtoMs: 500}); !slices.Equal(got, want) {
		t.Errorf("integrity: %q, want %q", got, want)
	}
}

func TestScenarioSLAScope(t *testing.T) {
	for _, tt := range []struct {
		v          *ValidationSpec
		flag, want string
	}{
		{nil, "", "report"},
		{&ValidationSpec{}, "", "report"},
		{&ValidationSpec{}, "task", "task"},
		{&ValidationSpec{PerTask: true}, "", "task"},
		{&ValidationSpec{PerTask: true}, "report", "task"},
	} {
		if got := scenarioSLAScope(tt.v, tt.flag); got != tt.want {
			t.Errorf("scenarioSLAScope(%+v, %q) = %q, want %q", tt.v, tt.flag, got, tt.want)
		}
	}
}

// Two STRESS producers at 6,000 rec/s each, against a gate of 10,000.
const applyStressFinal = `{"id":"{{id}}","testType":"STRESS","status":"DONE","results":[
 {"phaseName":"produce","status":"DONE","recordsSent":1000,"throughputRecordsPerSec":6000,"p99LatencyMs":20},
 {"phaseName":"produce","status":"DONE","recordsSent":1000,"throughputRecordsPerSec":6000,"p99LatencyMs":30}]}`

const applyStressScenario = "scenarios:\n  - name: two\n    type: STRESS\n    validate: {minThroughputRecPerSec: 10000}\n"

func applyJSON(t *testing.T, args ...string) (applyResult, error) {
	t.Helper()
	stdout, _, err := commandOutput(t, func() error {
		return runCommandLine(t, append([]string{"test", "apply"}, append(args, "-o", "json")...)...)
	})
	var got applyResult
	decodeStdout(t, stdout, &got)
	return got, err
}

func TestApply_SLAScopes(t *testing.T) {
	b := applyBackend(t)
	b.final["STRESS"] = applyStressFinal
	file := writeScenarioFile(t, applyStressScenario)

	// The report scope, the default: the two producers meet the gate
	// together.
	got, err := applyJSON(t, "-f", file, "--wait")
	if err != nil {
		t.Fatalf("report scope: %v", err)
	}
	if got.SLA != (applySLAMode{Checked: true, Scope: "report"}) || !reflect.DeepEqual(got.Scenarios[0].SLA, &applySLAResult{Scope: "report", Violations: []string{}}) {
		t.Errorf("report scope: %+v %+v", got.SLA, got.Scenarios[0].SLA)
	}

	// --sla-scope task: each producer misses it.
	b.resetFlags()
	got, err = applyJSON(t, "-f", file, "--wait", "--sla-scope", "task")
	if exitCode(err) != 1 {
		t.Errorf("task scope: err = %v, want exit 1", err)
	}
	want := &applySLAResult{Scope: "task", Violations: []string{"throughput=6000 < 10000 rec/s", "throughput=6000 < 10000 rec/s"}}
	if got.SLA != (applySLAMode{Checked: true, Scope: "task"}) || !reflect.DeepEqual(got.Scenarios[0].SLA, want) {
		t.Errorf("task scope: %+v %+v", got.SLA, got.Scenarios[0].SLA)
	}

	// perTask: true in the block does the same for its scenario.
	b.resetFlags()
	perTask := writeScenarioFile(t, strings.Replace(applyStressScenario, "10000}", "10000, perTask: true}", 1))
	got, err = applyJSON(t, "-f", perTask, "--wait")
	if exitCode(err) != 1 || got.SLA.Scope != "report" || !reflect.DeepEqual(got.Scenarios[0].SLA, want) {
		t.Errorf("perTask: err = %v, %+v %+v", err, got.SLA, got.Scenarios[0].SLA)
	}
}

// --no-sla runs and waits, checks no gate, and exits on how the runs ended.
func TestApply_NoSLA(t *testing.T) {
	b := applyBackend(t)
	file := writeScenarioFile(t, applyScenarios)

	got, err := applyJSON(t, "-f", file, "--wait", "--no-sla")
	if err != nil {
		t.Errorf("err = %v: slow violates its gate, but no gate is checked", err)
	}
	if got.SLA != (applySLAMode{Checked: false}) {
		t.Errorf("sla = %+v, want checked false", got.SLA)
	}
	for _, s := range got.Scenarios {
		if s.SLA != nil || s.Status != "DONE" {
			t.Errorf("%s: %+v", s.Name, s)
		}
	}

	// A run that ends FAILED still fails the apply.
	b.resetFlags()
	b.final["STRESS"] = `{"id":"{{id}}","testType":"STRESS","status":"FAILED","results":[]}`
	if _, err := applyJSON(t, "-f", file, "--wait", "--no-sla"); exitCode(err) != 1 {
		t.Errorf("failed run: err = %v, want exit 1", err)
	}
}

// Flags that make no sense together exit 2 before anything starts.
func TestApply_SLAFlagsRefused(t *testing.T) {
	b := applyBackend(t)
	file := writeScenarioFile(t, applyScenarios)
	for _, args := range [][]string{
		{"--wait", "--sla-scope", "row"},
		{"--sla-scope", "task"},
		{"--no-sla"},
		{"--wait", "--no-sla", "--sla-scope", "report"},
		{"--output-junit", filepath.Join(t.TempDir(), "x.xml")},
		{"--wait", "--output-junit", "-"},
	} {
		b.resetFlags()
		err := runCommandLine(t, append([]string{"test", "apply", "-f", file}, args...)...)
		if exitCode(err) != 2 {
			t.Errorf("%q: err = %v, want exit 2", args, err)
		}
	}
	if created := b.createdTypes(); len(created) != 0 {
		t.Errorf("created %v", created)
	}
}

// A typo in the fourth scenario stops the apply before the first starts.
func TestApply_LintsTheWholeFileFirst(t *testing.T) {
	b := applyBackend(t)
	file := writeScenarioFile(t, applyScenarios+"  - name: third\n    type: LOAD\n    validate: {maxP99Latency: 50}\n")
	_, stderr, err := commandOutput(t, func() error {
		return runCommandLine(t, "test", "apply", "-f", file, "--wait")
	})
	if exitCode(err) != 2 || !strings.Contains(stderr, `scenarios.yaml:13:16: error: scenarios[3].validate.maxP99Latency: unknown field (did you mean "maxP99LatencyMs"?) [unknown-field]`) {
		t.Errorf("err = %v\n%s", err, stderr)
	}
	if created := b.createdTypes(); len(created) != 0 {
		t.Errorf("created %v; no scenario may start", created)
	}
}

// The JUnit report of an apply, against a golden file: a pass, an SLA
// failure, a run that failed, one that could not be created, one lost track
// of, the one Ctrl-C stopped and one never started.
func TestWriteApplyJUnit_Golden(t *testing.T) {
	res := applyResult{File: "suite.yaml", Waited: true, SLA: applySLAMode{Checked: true, Scope: "report"}, Interrupted: true,
		Scenarios: []applyScenarioResult{
			{Name: "fast", Type: "LOAD", RunID: "run-1", Status: "DONE", elapsed: 1500 * time.Millisecond,
				SLA: &applySLAResult{Scope: "report", Violations: []string{}, NotEvaluable: []string{"maxRtoMs (RTO not measured)"}}},
			{Name: "slow", Type: "STRESS", RunID: "run-2", Status: "DONE", elapsed: 2 * time.Second,
				SLA: &applySLAResult{Scope: "task", Violations: []string{"p99=210ms > 50ms", "throughput=6000 < 10000 rec/s"}}},
			{Name: "crashed", Type: "LOAD", RunID: "run-3", Status: "FAILED", elapsed: 250 * time.Millisecond,
				SLA: &applySLAResult{Scope: "report", Violations: []string{"run FAILED: boom"}}},
			{Name: "refused", Type: "SPIKE", Status: "FAILED", Error: "[400] Validation Failed: throughput"},
			{Name: "lost", Type: "LOAD", RunID: "run-5", Status: "ERROR", Error: "connection lost", elapsed: 3 * time.Second},
			{Name: "stopped", Type: "LOAD", RunID: "run-6", Status: "CANCELLED", Error: "interrupted; the run was cancelled",
				elapsed: time.Second, interrupted: true},
		}}
	path := filepath.Join(t.TempDir(), "apply.xml")
	if err := writeApplyJUnit(path, res, []TestScenario{{Type: "integrity"}}, 9*time.Second); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "apply-junit.golden.xml")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v; run UPDATE_GOLDEN=1 go test ./cmd -run TestWriteApplyJUnit_Golden", err)
	}
	if string(got) != string(want) {
		t.Errorf("JUnit differs from %s:\n%s", golden, got)
	}
	var suites junitTestSuites
	if err := xml.Unmarshal(got, &suites); err != nil {
		t.Fatalf("not XML: %v", err)
	}
}

// --output-junit through the command: one testcase per scenario, the SLA
// violation a failure, none with --no-sla.
func TestApply_OutputJUnit(t *testing.T) {
	b := applyBackend(t)
	file := writeScenarioFile(t, applyScenarios)
	for _, tt := range []struct {
		args     []string
		failures int
	}{
		{nil, 1},
		{[]string{"--no-sla"}, 0},
	} {
		b.resetFlags()
		path := filepath.Join(t.TempDir(), "apply.xml")
		_, _ = applyJSON(t, append([]string{"-f", file, "--wait", "--junit", path}, tt.args...)...)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var suites junitTestSuites
		if err := xml.Unmarshal(data, &suites); err != nil {
			t.Fatal(err)
		}
		s := suites.Suites[0]
		if s.Tests != 3 || s.Failures != tt.failures || s.Errors != 0 || len(s.Cases) != 3 {
			t.Errorf("%q: tests %d failures %d errors %d\n%s", tt.args, s.Tests, s.Failures, s.Errors, data)
		}
	}
}

// The JSON summary keeps its keys: a script reads sla.checked to tell a run
// graded from one that was not.
func TestApply_JSONSummaryShape(t *testing.T) {
	applyBackend(t)
	file := writeScenarioFile(t, applyScenarios)
	stdout, _, _ := commandOutput(t, func() error {
		return runCommandLine(t, "test", "apply", "-f", file, "-o", "json")
	})
	var raw map[string]any
	if err := json.Unmarshal(stdout, &raw); err != nil {
		t.Fatal(err)
	}
	if sla, _ := raw["sla"].(map[string]any); !reflect.DeepEqual(sla, map[string]any{"checked": false}) {
		t.Errorf("without --wait sla = %v, want {checked: false}", raw["sla"])
	}
}
