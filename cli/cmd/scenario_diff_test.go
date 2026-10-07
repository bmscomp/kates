package cmd

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/client"
)

// runScenarioDiff runs kates scenario-diff on a file named name that holds
// file, against a backend that answers GET /api/tests/run-1 with run, with
// pick as --scenario and mode as -o. It returns what the command printed,
// without colour, and its error.
func runScenarioDiff(t *testing.T, name, file, run, pick, mode string) (string, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	ts, buf := setupTest(t, "GET", "/api/tests/run-1", 200, run)
	defer ts.Close()
	outputMode, scenarioDiffPick = mode, pick
	defer func() { outputMode, scenarioDiffPick = "table", "" }()

	err := scenarioDiffCmd.RunE(scenarioDiffCmd, []string{path, "run-1"})
	return stripAnsi(buf.String()), err
}

// diffScenarioJSON runs kates scenario-diff -o json and returns what it found.
func diffScenarioJSON(t *testing.T, name, file, run, pick string) scenarioDiff {
	t.Helper()
	out, err := runScenarioDiff(t, name, file, run, pick, "json")
	if err != nil {
		t.Fatalf("kates scenario-diff: %v\n%s", err, out)
	}
	var d scenarioDiff
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("-o json printed something else: %v\n%s", err, out)
	}
	return d
}

// diffRun is a run as GET /api/tests/{id} returns it, with spec as its spec.
func diffRun(testType, spec string) string {
	return `{"id": "run-1", "testType": "` + testType + `", "status": "DONE", "backend": "native", "spec": ` + spec + `}`
}

// A scenarios list, the form kates test apply runs and every shipped file
// has, is compared by the keys apply reads. scenario-diff used to read type,
// backend and spec at the top of the file, so it compared nothing in a list
// and printed "No configuration drift".
func TestScenarioDiff_ReportsDriftInAScenariosList(t *testing.T) {
	const file = `scenarios:
  - name: "Quick Load Test"
    type: LOAD
    backend: trogdor
    spec:
      records: 50000
      parallelProducers: 2
      recordSizeBytes: 1024
      acks: all
`
	run := diffRun("LOAD", `{"numRecords": 40000, "numProducers": 2, "recordSize": 512, "acks": "all", "lingerMs": 5}`)
	out, err := runScenarioDiff(t, "quick-load.yaml", file, run, "", "table")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"quick-load.yaml (Quick Load Test) vs run-1",
		"Backend:\n    - scenario: trogdor\n    + actual:   native",
		"Records:\n    - scenario: 50000\n    + actual:   40000",
		"Record Size:\n    - scenario: 1024\n    + actual:   512",
		"3 configuration difference(s) detected.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Producers:") || strings.Contains(out, "Acks:") {
		t.Errorf("a field that matches is reported:\n%s", out)
	}

	d := diffScenarioJSON(t, "quick-load.yaml", file, run, "")
	if !strings.HasSuffix(d.File, "quick-load.yaml") || d.Scenario != "Quick Load Test" || d.TestID != "run-1" {
		t.Errorf("-o json names file %q, scenario %q, run %q", d.File, d.Scenario, d.TestID)
	}
	want := []scenarioDiffItem{
		{Key: "backend", Field: "backend", Scenario: "trogdor", Actual: "native"},
		{Key: "spec.records", Field: "numRecords", Scenario: "50000", Actual: "40000"},
		{Key: "spec.recordSizeBytes", Field: "recordSize", Scenario: "1024", Actual: "512"},
	}
	if !reflect.DeepEqual(d.Differences, want) {
		t.Errorf("differences %+v, want %+v", d.Differences, want)
	}
}

// A scenario the run matches has no drift: the type and the text settings
// are compared ignoring case, as apply upper-cases the type.
func TestScenarioDiff_AMatchingScenarioHasNoDrift(t *testing.T) {
	const file = `scenarios:
  - name: soak
    type: endurance
    backend: native
    spec:
      records: 200000
      parallelProducers: 1
      durationSeconds: 120
      topic: kates-soak
      acks: all
      compressionType: lz4
      lingerMs: 5
      targetThroughput: 5000
      enableIdempotence: true
`
	run := diffRun("ENDURANCE", `{"numRecords": 200000, "numProducers": 1, "durationMs": 120000, "topic": "kates-soak",
		"acks": "all", "compressionType": "LZ4", "lingerMs": 5, "batchSize": 65536, "partitions": 3,
		"throughput": 5000, "targetThroughput": 5000, "enableIdempotence": true}`)
	out, err := runScenarioDiff(t, "soak.yaml", file, run, "", "table")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "No configuration drift — scenario matches test run.") || strings.Contains(out, "Not compared") {
		t.Errorf("output:\n%s", out)
	}

	d := diffScenarioJSON(t, "soak.yaml", file, run, "")
	want := []string{"type", "backend", "spec.records", "spec.parallelProducers", "spec.durationSeconds", "spec.topic",
		"spec.acks", "spec.lingerMs", "spec.compressionType", "spec.targetThroughput", "spec.enableIdempotence"}
	if !slices.Equal(d.Compared, want) || len(d.Differences) != 0 || len(d.NotCompared) != 0 {
		t.Errorf("compared %v, differences %+v, not compared %+v; want %v compared and no others",
			d.Compared, d.Differences, d.NotCompared, want)
	}
}

// A 0 in batchSize, lingerMs or fetchMaxWaitMs is sent as the setting it is,
// so a run that lingered 5 ms has drifted from lingerMs: 0. diffSpecField
// used to skip every scenario value of 0.
func TestScenarioDiff_AZeroSettingIsCompared(t *testing.T) {
	const file = "scenarios:\n  - name: no-linger\n    type: LOAD\n    spec:\n      lingerMs: 0\n      batchSize: 0\n      fetchMaxWaitMs: 0\n"
	run := diffRun("LOAD", `{"lingerMs": 5, "batchSize": 16384, "fetchMaxWaitMs": 500}`)
	out, err := runScenarioDiff(t, "s.yaml", file, run, "", "table")
	if err != nil {
		t.Fatal(err)
	}
	if want := "Linger Ms:\n    - scenario: 0\n    + actual:   5"; !strings.Contains(out, want) {
		t.Errorf("the output lacks %q:\n%s", want, out)
	}

	d := diffScenarioJSON(t, "s.yaml", file, run, "")
	want := []scenarioDiffItem{
		{Key: "spec.batchSize", Field: "batchSize", Scenario: "0", Actual: "16384"},
		{Key: "spec.lingerMs", Field: "lingerMs", Scenario: "0", Actual: "5"},
		{Key: "spec.fetchMaxWaitMs", Field: "fetchMaxWaitMs", Scenario: "0", Actual: "500"},
	}
	if !reflect.DeepEqual(d.Differences, want) {
		t.Errorf("differences %+v, want %+v", d.Differences, want)
	}

	d = diffScenarioJSON(t, "s.yaml", file, diffRun("LOAD", `{"lingerMs": 0, "batchSize": 0, "fetchMaxWaitMs": 0}`), "")
	if len(d.Differences) != 0 || len(d.NotCompared) != 0 || len(d.Compared) != 4 {
		t.Errorf("a run at those 0s: compared %v, differences %+v, not compared %+v", d.Compared, d.Differences, d.NotCompared)
	}
}

// durationSeconds is compared as the durationMs scenarioToRequest sends for
// it. scenario-diff used to look for a durationMs key, which apply does not
// read.
func TestScenarioDiff_ComparesDurationSecondsAsTheDurationMsSent(t *testing.T) {
	const file = "scenarios:\n  - name: soak\n    type: ENDURANCE\n    spec:\n      durationSeconds: 120\n"
	for _, durationMs := range []int{120000, 120, 60000} {
		d := diffScenarioJSON(t, "s.yaml", file, diffRun("ENDURANCE", fmt.Sprintf(`{"durationMs": %d}`, durationMs)), "")
		want := []scenarioDiffItem{}
		if durationMs != 120000 {
			want = append(want, scenarioDiffItem{Key: "spec.durationSeconds", Field: "durationMs", Scenario: "120000", Actual: strconv.Itoa(durationMs)})
		}
		if !reflect.DeepEqual(d.Differences, want) {
			t.Errorf("against a run of %d ms: differences %+v, want %+v", durationMs, d.Differences, want)
		}
	}
}

// A field the run's spec does not report is not compared, rather than read
// as 0: a backend that predates them keeps no consumerGroup, fetch setting or
// enable option in a run's spec, even when the request set them.
func TestScenarioDiff_AFieldTheRunDoesNotReportIsNotCompared(t *testing.T) {
	const file = `scenarios:
  - name: consumer
    type: LOAD
    backend: native
    spec:
      records: 1000
      consumerGroup: kates-group
      targetThroughput: 5000
      fetchMinBytes: 1024
      fetchMaxWaitMs: 0
      enableIdempotence: false
`
	unreported := []scenarioDiffSkip{
		{"spec.consumerGroup", "the run's spec does not report it"},
		{"spec.targetThroughput", "the run's spec does not report it"},
		{"spec.fetchMinBytes", "the run's spec does not report it"},
		{"spec.fetchMaxWaitMs", "the run's spec does not report it"},
		{"spec.enableIdempotence", "the run's spec does not report it"},
	}
	run := diffRun("LOAD", `{"numRecords": 1000}`)
	d := diffScenarioJSON(t, "s.yaml", file, run, "")
	if !slices.Equal(d.Compared, []string{"type", "backend", "spec.records"}) || len(d.Differences) != 0 ||
		!reflect.DeepEqual(d.NotCompared, unreported) {
		t.Errorf("compared %v, differences %+v, not compared %+v", d.Compared, d.Differences, d.NotCompared)
	}
	out, err := runScenarioDiff(t, "s.yaml", file, run, "", "table")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Not compared:\n    spec.consumerGroup: the run's spec does not report it\n",
		"No configuration drift in the 3 field(s) compared.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the output lacks %q:\n%s", want, out)
		}
	}

	// A run that reports no backend and no spec.
	d = diffScenarioJSON(t, "s.yaml", file, `{"id": "run-1", "testType": "LOAD", "status": "DONE"}`, "")
	unreported = append([]scenarioDiffSkip{
		{"backend", "the run does not report it"},
		{"spec.records", "the run's spec does not report it"},
	}, unreported...)
	if !slices.Equal(d.Compared, []string{"type"}) || len(d.Differences) != 0 || !reflect.DeepEqual(d.NotCompared, unreported) {
		t.Errorf("a run with no backend or spec: compared %v, differences %+v, not compared %+v", d.Compared, d.Differences, d.NotCompared)
	}
}

// A scenario's rate is sent as targetThroughput, and the run's spec reports
// the rate the run used as throughput, whichever name its request gave it.
func TestScenarioDiff_ComparesTheRateWithTheRateTheRunUsed(t *testing.T) {
	const file = "scenarios:\n  - name: rate\n    type: LOAD\n    spec:\n      targetThroughput: 5000\n"
	drift := []scenarioDiffItem{{Key: "spec.targetThroughput", Field: "throughput", Scenario: "5000", Actual: "8000"}}
	for _, tt := range []struct {
		spec string
		want []scenarioDiffItem
	}{
		{`{"throughput": 5000, "targetThroughput": 5000}`, []scenarioDiffItem{}},
		// Started with a throughput and no targetThroughput.
		{`{"throughput": 8000}`, drift},
		// Its request set both, and the run used the throughput.
		{`{"throughput": 8000, "targetThroughput": 5000}`, drift},
	} {
		d := diffScenarioJSON(t, "s.yaml", file, diffRun("LOAD", tt.spec), "")
		if !reflect.DeepEqual(d.Differences, tt.want) {
			t.Errorf("against %s: differences %+v, want %+v", tt.spec, d.Differences, tt.want)
		}
	}
}

// A key kates test apply does not read, or a value it leaves out of the
// request, is not compared: the run did not get it from the file. The API
// names scenario-diff used to read (numRecords and the like) are among the
// keys apply does not read.
func TestScenarioDiff_KeysApplyDoesNotSendAreNotCompared(t *testing.T) {
	const file = `scenarios:
  - name: api-names
    type: STRESS
    spec:
      numRecords: 500000
      throughput: 5000
      records: "50000"
      topic: ""
      partitions: 0
`
	d := diffScenarioJSON(t, "s.yaml", file,
		diffRun("STRESS", `{"numRecords": 1000000, "throughput": -1, "partitions": 3, "topic": "stress-test"}`), "")
	want := []scenarioDiffSkip{
		{"spec.records", "it is not a number, so kates test apply leaves it out"},
		{"spec.topic", "it is empty, so kates test apply leaves it out"},
		{"spec.partitions", "kates test apply reads it as 0 and leaves it out"},
		{"spec.numRecords", "kates test apply does not read it; the scenario key is records"},
		{"spec.throughput", "kates test apply does not read it; the scenario key for the producer rate is targetThroughput"},
	}
	if !slices.Equal(d.Compared, []string{"type"}) || len(d.Differences) != 0 || !reflect.DeepEqual(d.NotCompared, want) {
		t.Errorf("compared %v, differences %+v, not compared %+v; want only type compared and %+v not",
			d.Compared, d.Differences, d.NotCompared, want)
	}
}

// A file of more than one scenario needs --scenario, which takes the name
// kates test apply shows for a scenario or its number in the file.
func TestScenarioDiff_PicksTheScenarioToCompare(t *testing.T) {
	const file = `scenarios:
  - name: "Quick Load Test"
    type: LOAD
    spec:
      records: 50000
  - type: ENDURANCE
    spec:
      records: 200000
  - name: twin
    type: LOAD
  - name: twin
    type: STRESS
`
	run := diffRun("ENDURANCE", `{"numRecords": 200000}`)
	for _, tt := range []struct {
		pick, scenario string
		drift          []string
	}{
		{"Scenario 2", "Scenario 2", nil},
		{"2", "Scenario 2", nil},
		{"Quick Load Test", "Quick Load Test", []string{"type", "spec.records"}},
		{"1", "Quick Load Test", []string{"type", "spec.records"}},
		{"4", "twin", []string{"type"}},
	} {
		d := diffScenarioJSON(t, "s.yaml", file, run, tt.pick)
		var drift []string
		for _, item := range d.Differences {
			drift = append(drift, item.Key)
		}
		if d.Scenario != tt.scenario || !slices.Equal(drift, tt.drift) {
			t.Errorf("--scenario %q compared %q with drift in %v; want %q with drift in %v", tt.pick, d.Scenario, drift, tt.scenario, tt.drift)
		}
	}

	for _, tt := range []struct{ pick, want string }{
		{"", `holds 4 scenarios; choose one with --scenario <name|number>: 1 "Quick Load Test" (LOAD), ` +
			`2 "Scenario 2" (ENDURANCE), 3 "twin" (LOAD), 4 "twin" (STRESS)`},
		{"twin", `are named "twin"; choose one by number with --scenario: 3, 4`},
		{"Nope", `has no scenario "Nope"; choose one with --scenario <name|number>: 1 "Quick Load Test" (LOAD)`},
		{"5", `has no scenario "5"`},
		{"0", `has no scenario "0"`},
	} {
		out, err := runScenarioDiff(t, "s.yaml", file, run, tt.pick, "table")
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("--scenario %q: err = %v, want one that says %s", tt.pick, err, tt.want)
		}
		if strings.Contains(out, "Scenario Diff") {
			t.Errorf("--scenario %q compared a scenario:\n%s", tt.pick, out)
		}
	}
}

// The file is read as kates test apply reads it: with encoding/json when it
// is named .json, and as the one scenario at its top level when it holds no
// scenarios list.
func TestScenarioDiff_ReadsTheFileAsApplyDoes(t *testing.T) {
	// encoding/json matches a key to a field in any case; yaml.v3 does not.
	const listJSON = `{"Scenarios": [{"name": "j", "type": "LOAD", "spec": {"records": 2000}}]}`
	run := diffRun("LOAD", `{"numRecords": 1000}`)
	for _, tt := range []struct{ name, file, scenario string }{
		{"s.json", listJSON, "j"},
		// One scenario at the top level of a file without a list.
		{"s.yaml", "name: y\ntype: LOAD\nspec:\n  records: 2000\n", "y"},
		{"s.json", `{"name": "j", "type": "LOAD", "spec": {"records": 2000}}`, "j"},
		// Not JSON, so encoding/json reads no list; read as YAML, it is one
		// scenario.
		{"s.json", "name: y\ntype: LOAD\nspec:\n  records: 2000\n", "y"},
	} {
		d := diffScenarioJSON(t, tt.name, tt.file, run, "")
		want := []scenarioDiffItem{{Key: "spec.records", Field: "numRecords", Scenario: "2000", Actual: "1000"}}
		if d.Scenario != tt.scenario || !reflect.DeepEqual(d.Differences, want) {
			t.Errorf("%s holding %q: scenario %q, differences %+v", tt.name, tt.file, d.Scenario, d.Differences)
		}
	}
	if _, err := runScenarioDiff(t, "s.yaml", listJSON, run, "", "table"); err == nil || !strings.Contains(err.Error(), "No scenarios found in") {
		t.Errorf("the JSON list named .yaml: err = %v", err)
	}
}

// kates test apply refuses a flag that is not true or false, so no run came
// from such a scenario.
func TestScenarioDiff_RefusesAFlagApplyRefuses(t *testing.T) {
	const file = "scenarios:\n  - name: crc\n    type: INTEGRITY\n    spec:\n      enableCrc: yes\n"
	_, err := runScenarioDiff(t, "s.yaml", file, diffRun("INTEGRITY", `{"enableCrc": true}`), "", "table")
	if err == nil || !strings.Contains(err.Error(), "scenario 1 (crc): spec.enableCrc is yes; write true or false") {
		t.Errorf("err = %v", err)
	}
}

// scenarioDiffFields compares every spec key scenarioToRequest reads (the keys
// of mcpScnSpecKeys, which TestMCPDraftScenarioSpecKeysMatchScenarioToRequest
// holds to apply.go), and reads each from the field its key sets in the
// request and from the field the run reports it under.
func TestScenarioDiffFieldsMatchScenarioToRequest(t *testing.T) {
	var keys []string
	for _, f := range scenarioDiffFields {
		keys = append(keys, f.key)
	}
	if got, want := slices.Sorted(slices.Values(keys)), slices.Sorted(maps.Keys(mcpScnSpecKeys)); !slices.Equal(got, want) {
		t.Errorf("scenario-diff compares %v; scenarioToRequest reads %v", got, want)
	}
	for _, f := range scenarioDiffFields {
		k := mcpScnSpecKeys[f.key]
		var v any = 7
		sent, reported := "7", "7"
		switch k.kind {
		case 's':
			v, sent, reported = "x", "x", `"x"`
		case 'b':
			v, sent, reported = true, "true", "true"
		}
		if k.scale != 0 {
			sent = strconv.FormatInt(7*k.scale, 10)
			reported = sent
		}
		if got, ok := f.read(scenarioToRequest(TestScenario{Type: "LOAD", Spec: map[string]any{f.key: v}}).Spec); !ok || got != sent {
			t.Errorf("spec.%s: read from the request as %q (%t), want %q", f.key, got, ok, sent)
		}
		var spec client.TestSpec
		if err := json.Unmarshal([]byte(`{"`+f.field+`": `+reported+`}`), &spec); err != nil {
			t.Fatal(err)
		}
		ran := f.read
		if f.ran != nil {
			ran = f.ran
		}
		if got, ok := ran(&spec); !ok || got != sent {
			t.Errorf("spec.%s: read from a run's %s as %q (%t), want %q", f.key, f.field, got, ok, sent)
		}
		if f.ran == nil && f.field != k.wire {
			t.Errorf("spec.%s is sent as %s, and compared as the run's %s", f.key, k.wire, f.field)
		}
	}
}
