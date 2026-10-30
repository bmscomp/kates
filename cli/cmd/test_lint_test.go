package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/internal/compliance"
	"github.com/bmscomp/kates/cli/output"
)

// diagKeys reads diagnostics as line:col severity code path, for a table.
func diagKeys(ds []compliance.Diagnostic) []string {
	keys := make([]string, len(ds))
	for i, d := range ds {
		keys[i] = fmt.Sprintf("%d:%d %s %s %s", d.Line, d.Col, d.Severity, d.Code, d.Path)
	}
	return keys
}

func TestLintScenarioFile(t *testing.T) {
	for _, tt := range []struct {
		name, file string
		want       []string
	}{
		{"ok.yaml", "scenarios:\n  - name: a\n    type: load\n    spec: {records: 1000, lingerMs: 0}\n    validate: {maxP99LatencyMs: 50, perTask: true}\n", nil},
		{"lone.yaml", "name: a\ntype: STRESS\nvalidate:\n  minThroughputRecPerSec: 1000\n", nil},
		{"lone.json", `{"name": "a", "type": "LOAD", "spec": {"records": 1000}}`, nil},
		{"syntax.yaml", "scenarios: [\n", []string{"1:1 error syntax "}},
		{"empty.yaml", "", []string{"0:0 error no-scenarios "}},
		{"untyped.yaml", "name: a\nspec: {records: 1}\n", []string{"1:1 error no-scenarios "}},
		{"case.json", `{"Scenarios": [{"type": "LOAD"}]}`, []string{"1:1 error no-scenarios ", "1:2 error unknown-field Scenarios"}},
		{"typos.yaml", "scenarios:\n  - name: a\n    type: LAOD\n    spec:\n      record: 10\n      durationSeconds: 1.5\n      enableCrc: yes\n    validate:\n      maxOutOfOrder: 0.5\n      maxErrorRate: 0.1\n      maxP99LatencyMs: 0\n    extra: 1\n",
			[]string{
				"3:11 error unknown-type scenarios[0].type",
				"5:7 error unknown-field scenarios[0].spec.record",
				"6:24 error wrong-value scenarios[0].spec.durationSeconds",
				"7:18 error wrong-value scenarios[0].spec.enableCrc",
				"9:22 error wrong-value scenarios[0].validate.maxOutOfOrder",
				"10:21 warning gate-not-checked scenarios[0].validate.maxErrorRate",
				"11:24 warning gate-off scenarios[0].validate.maxP99LatencyMs",
				"12:5 error unknown-field scenarios[0].extra",
			}},
		{"beside.yaml", "type: LOAD\nscenarios:\n  - {name: a}\n  - {type: LOAD, type: STRESS}\n",
			[]string{"1:1 error unknown-field type", "3:5 error missing-type scenarios[0]", "4:18 error duplicate-key scenarios[1].type"}},
		{"quoted.yaml", "type: LOAD\nspec: {records: \"5000\", topic: [a]}\nvalidate: {maxP99LatencyMs: 50ms}\n",
			[]string{"2:17 error wrong-value spec.records", "2:32 error wrong-type spec.topic", "3:29 error wrong-type validate.maxP99LatencyMs"}},
	} {
		got := diagKeys(lintScenarioFile(tt.name, []byte(tt.file)))
		if !slices.Equal(got, tt.want) {
			t.Errorf("%s:\n got %q\nwant %q", tt.name, got, tt.want)
		}
	}
}

// Every scenario file Kates ships lints clean, so apply runs it.
func TestLintShippedScenarioFiles(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("scenarios", "*.yaml"))
	examples, _ := filepath.Glob(filepath.Join("..", "examples", "perf-*.yaml"))
	for _, f := range append(append(files, examples...), filepath.Join("..", "examples", "load-test.yaml")) {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range lintScenarioFile(f, src) {
			t.Errorf("%s", d.Format(f))
		}
	}
	if len(files) == 0 {
		t.Fatal("no shipped scenarios found")
	}
}

func TestLintFlowFile(t *testing.T) {
	for _, tt := range []struct {
		name, file string
		want       []string
	}{
		{"ok", "name: f\nsteps:\n  - name: t\n    action: test\n    type: LOAD\n    records: 1000\n    gate: {minGrade: B, maxP99: 50}\n    spec: {numRecords: 10, acks: all}\n    onFail: continue\n  - {name: w, action: wait, records: 2}\n", nil},
		{"no steps", "name: f\n", []string{"1:1 error no-steps steps"}},
		{"typos", "name: f\nstep: []\n", []string{"1:1 error no-steps steps", "2:1 error unknown-field step"}},
		{"steps", "steps:\n  - name: t\n    action: tset\n    records: 1.5\n    gate: {minGrade: Z, maxP99: fast}\n    spec: {records: 10}\n    onFail: contine\n  - {action: wait, gate: {minGrade: A}}\n  - {action: notify}\n  - {name: x}\n",
			[]string{
				"3:13 error unknown-action steps[0].action",
				"4:14 error wrong-value steps[0].records",
				"5:22 error wrong-value steps[0].gate.minGrade",
				"5:33 error wrong-type steps[0].gate.maxP99",
				"6:12 error unknown-field steps[0].spec.records",
				"7:13 error wrong-value steps[0].onFail",
				"8:26 warning unused-field steps[1].gate",
				"9:14 error not-implemented steps[2].action",
				"10:5 error missing-action steps[3]",
			}},
	} {
		got := diagKeys(lintFlowFile([]byte(tt.file)))
		if !slices.Equal(got, tt.want) {
			t.Errorf("%s:\n got %q\nwant %q", tt.name, got, tt.want)
		}
	}
}

func writeLintFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// kates test lint exits as compliance lint does: 0 clean or warnings only, 1
// on an error, 2 when a file cannot be read; -o json lists the findings.
func TestTestLintCommand(t *testing.T) {
	clean := writeLintFile(t, "clean.yaml", "type: LOAD\nvalidate: {maxErrorRate: 0.1}\n")
	broken := writeLintFile(t, "broken.yaml", "type: LOAD\nspce: {}\n")
	defer func() { outputMode = "table" }()

	outputMode = "table"
	stdout, _, err := commandOutput(t, func() error { return runCommandLine(t, "test", "lint", clean) })
	if exitCode(err) != 0 || !strings.Contains(string(stdout), "0 errors, 1 warning in 1 file") {
		t.Errorf("clean: err = %v\n%s", err, stdout)
	}
	stdout, _, err = commandOutput(t, func() error { return runCommandLine(t, "test", "lint", clean, broken) })
	if exitCode(err) != 1 || !strings.Contains(string(stdout), "broken.yaml:2:1: error: spce: unknown field (did you mean \"spec\"?) [unknown-field]\n  2 | spce: {}\n    | ^") {
		t.Errorf("broken: err = %v\n%s", err, stdout)
	}
	if err := runCommandLine(t, "test", "lint", filepath.Join(t.TempDir(), "missing.yaml")); exitCode(err) != 2 {
		t.Errorf("missing: err = %v, want exit 2", err)
	}

	outputMode = "json"
	stdout, _, err = commandOutput(t, func() error { return runCommandLine(t, "test", "lint", broken) })
	var findings []map[string]any
	if jerr := json.Unmarshal(stdout, &findings); jerr != nil || exitCode(err) != 1 || len(findings) != 1 ||
		findings[0]["code"] != "unknown-field" || findings[0]["line"] != 2.0 || findings[0]["file"] != broken {
		t.Errorf("json: err = %v, %v\n%s", err, jerr, stdout)
	}
}

// kates flow run lints the file first and runs no step when it has an error.
func TestFlowRun_LintsFirst(t *testing.T) {
	b := newRunsBackend(t)
	flow := writeLintFile(t, "flow.yaml", "name: f\nsteps:\n  - {name: one, action: test}\n  - {name: two, action: tset}\n")
	prev := flowFile
	flowFile = flow
	defer func() { flowFile = prev }()
	buf := output.ResetForTesting()
	var err error
	captureStdout(t, func() { err = flowRunCmd.RunE(flowRunCmd, nil) })
	if exitCode(err) != 2 || !strings.Contains(buf.String(), "steps[1].action: there is no action \"tset\"") {
		t.Errorf("err = %v\n%s", err, buf)
	}
	if created := b.createdTypes(); len(created) != 0 {
		t.Errorf("created %v; no step may run", created)
	}
	stdout, _, err := commandOutput(t, func() error { return runCommandLine(t, "flow", "lint", flow) })
	if exitCode(err) != 1 || !strings.Contains(string(stdout), "1 error, 0 warnings in 1 file") {
		t.Errorf("flow lint: err = %v\n%s", err, stdout)
	}
}
