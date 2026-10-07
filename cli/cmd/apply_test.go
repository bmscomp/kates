package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"gopkg.in/yaml.v3"
)

func TestScenarioToRequest_BasicFields(t *testing.T) {
	s := TestScenario{
		Type:    "LOAD",
		Backend: "native",
		Spec: map[string]interface{}{
			"records":           100000.0,
			"parallelProducers": 4.0,
			"recordSizeBytes":   2048.0,
			"durationSeconds":   300.0,
			"topic":             "my-topic",
		},
	}

	req := scenarioToRequest(s)

	if req.TestType != "LOAD" {
		t.Errorf("expected LOAD, got %s", req.TestType)
	}
	if req.Backend != "native" {
		t.Errorf("expected native, got %s", req.Backend)
	}
	if req.Spec == nil {
		t.Fatal("expected non-nil spec")
	}
	if req.Spec.Records != 100000 {
		t.Errorf("expected 100000 records, got %d", req.Spec.Records)
	}
	if req.Spec.ParallelProducers != 4 {
		t.Errorf("expected 4 producers, got %d", req.Spec.ParallelProducers)
	}
	if req.Spec.RecordSizeBytes != 2048 {
		t.Errorf("expected 2048 record size, got %d", req.Spec.RecordSizeBytes)
	}
	// durationSeconds: 300 in the scenario is 300_000 on the wire — the wire
	// field is milliseconds. The previous assertion (== 300) enshrined a 1000x
	// bug that made every scenario test finish almost instantly.
	if req.Spec.DurationMs != 300_000 {
		t.Errorf("expected 300000 ms duration, got %d", req.Spec.DurationMs)
	}
	if req.Spec.Topic != "my-topic" {
		t.Errorf("expected my-topic, got %s", req.Spec.Topic)
	}
}

func TestScenarioToRequest_ProducerTuning(t *testing.T) {
	s := TestScenario{
		Type: "STRESS",
		Spec: map[string]interface{}{
			"acks":            "1",
			"batchSize":       131072.0,
			"lingerMs":        10.0,
			"compressionType": "zstd",
		},
	}

	req := scenarioToRequest(s)
	spec := req.Spec

	if spec.Acks != "1" {
		t.Errorf("expected acks=1, got %s", spec.Acks)
	}
	if spec.BatchSize == nil || *spec.BatchSize != 131072 {
		t.Errorf("expected batchSize=131072, got %v", spec.BatchSize)
	}
	if spec.LingerMs == nil || *spec.LingerMs != 10 {
		t.Errorf("expected lingerMs=10, got %v", spec.LingerMs)
	}
	if spec.CompressionType != "zstd" {
		t.Errorf("expected compression=zstd, got %s", spec.CompressionType)
	}
}

func TestScenarioToRequest_ConsumerFields(t *testing.T) {
	s := TestScenario{
		Type: "LOAD",
		Spec: map[string]interface{}{
			"numConsumers":   4.0,
			"consumerGroup":  "test-cg",
			"fetchMinBytes":  1048576.0,
			"fetchMaxWaitMs": 1000.0,
		},
	}

	req := scenarioToRequest(s)
	spec := req.Spec

	if spec.NumConsumers != 4 {
		t.Errorf("expected 4 consumers, got %d", spec.NumConsumers)
	}
	if spec.ConsumerGroup != "test-cg" {
		t.Errorf("expected test-cg, got %s", spec.ConsumerGroup)
	}
	if spec.FetchMinBytes != 1048576 {
		t.Errorf("expected fetchMinBytes=1048576, got %d", spec.FetchMinBytes)
	}
	if spec.FetchMaxWaitMs == nil || *spec.FetchMaxWaitMs != 1000 {
		t.Errorf("expected fetchMaxWaitMs=1000, got %v", spec.FetchMaxWaitMs)
	}
}

func TestScenarioToRequest_TopicSettings(t *testing.T) {
	s := TestScenario{
		Type: "LOAD",
		Spec: map[string]interface{}{
			"partitions":        12.0,
			"replicationFactor": 3.0,
			"minInsyncReplicas": 2.0,
		},
	}

	req := scenarioToRequest(s)
	spec := req.Spec

	if spec.Partitions != 12 {
		t.Errorf("expected 12 partitions, got %d", spec.Partitions)
	}
	if spec.ReplicationFactor != 3 {
		t.Errorf("expected RF=3, got %d", spec.ReplicationFactor)
	}
	if spec.MinInsyncReplicas != 2 {
		t.Errorf("expected ISR=2, got %d", spec.MinInsyncReplicas)
	}
}

func TestScenarioToRequest_TargetThroughput(t *testing.T) {
	s := TestScenario{
		Type: "ENDURANCE",
		Spec: map[string]interface{}{
			"targetThroughput": 5000.0,
		},
	}

	req := scenarioToRequest(s)

	if req.Spec.TargetThroughput != 5000 {
		t.Errorf("expected throughput=5000, got %d", req.Spec.TargetThroughput)
	}
}

// An integrity option the file sets to false is sent as false. omitempty on a
// plain bool dropped it, so enableCrc: false reached the backend as nothing,
// and the run checked CRCs anyway.
func TestScenarioToRequest_ExplicitFalseIsSent(t *testing.T) {
	s := TestScenario{
		Type: "INTEGRITY",
		Spec: map[string]interface{}{
			"enableCrc":          false,
			"enableIdempotence":  false,
			"enableTransactions": true,
		},
	}

	body, err := json.Marshal(scenarioToRequest(s))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"enableCrc":false`, `"enableIdempotence":false`, `"enableTransactions":true`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("request %s lacks %s", body, want)
		}
	}

	body, err = json.Marshal(scenarioToRequest(TestScenario{Type: "LOAD", Spec: map[string]interface{}{"records": 5.0}}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "enable") {
		t.Errorf("a file that sets no option sends none: %s", body)
	}
}

// A 0 in batchSize, lingerMs or fetchMaxWaitMs is a Kafka setting, so the
// request sends it, from a YAML file and from a JSON one. Read with toInt it
// was left out, and a LOAD scenario's lingerMs: 0 lingered the type's 5 ms;
// perf-round-trip and spike-test got their 0 only because their types'
// default is 0 too. A key the scenario leaves out is still left out, and so
// is a 0 in the other number keys.
func TestScenarioToRequest_SendsAZeroWhereItIsASetting(t *testing.T) {
	const file = `{"scenarios": [
  {"name": "zeros", "type": "LOAD",
   "spec": {"batchSize": 0, "lingerMs": 0, "fetchMaxWaitMs": 0, "numConsumers": 0, "targetThroughput": 0}},
  {"name": "unset", "type": "LOAD", "spec": {"records": 1000}}]}`
	want := []string{
		`{"type":"LOAD","spec":{"batchSize":0,"lingerMs":0,"fetchMaxWaitMs":0}}`,
		`{"type":"LOAD","spec":{"numRecords":1000}}`,
	}
	var fromYAML, fromJSON ScenarioFile
	if err := yaml.Unmarshal([]byte(file), &fromYAML); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(file), &fromJSON); err != nil {
		t.Fatal(err)
	}
	for _, sf := range []ScenarioFile{fromYAML, fromJSON} {
		for i, s := range sf.Scenarios {
			body, err := json.Marshal(scenarioToRequest(s))
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != want[i] {
				t.Errorf("scenario %s sends %s, want %s", s.Name, body, want[i])
			}
		}
	}
}

// A value that is not a number is not sent as 0 either: it is left out, as
// it was, and the type's default applies.
func TestScenarioToRequest_AValueThatIsNotANumberIsNotSentAsZero(t *testing.T) {
	req := scenarioToRequest(TestScenario{Type: "LOAD", Spec: map[string]any{
		"batchSize": "64k", "lingerMs": nil, "fetchMaxWaitMs": true,
	}})
	if s := req.Spec; s.BatchSize != nil || s.LingerMs != nil || s.FetchMaxWaitMs != nil {
		body, _ := json.Marshal(req)
		t.Errorf("sent %s", body)
	}
}

// kates test apply posts the 0 a scenario file sets.
func TestApply_PostsAZeroSetting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.yaml")
	if err := os.WriteFile(path, []byte("scenarios:\n  - name: no-linger\n    type: LOAD\n    spec:\n      lingerMs: 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var posted []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		posted = append(posted, string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"run-1","testType":"LOAD","status":"PENDING"}`)
	}))
	defer ts.Close()
	apiClient = client.New(ts.URL)
	output.ResetForTesting()
	applyFile, applyWait = path, false
	defer func() { applyFile = "" }()

	if err := testApplyCmd.RunE(testApplyCmd, nil); err != nil {
		t.Fatal(err)
	}
	if want := `{"type":"LOAD","spec":{"lingerMs":0}}`; len(posted) != 1 || posted[0] != want {
		t.Errorf("posted %q, want %s", posted, want)
	}
}

// parseScenarioFile reads a file as kates test apply runs it: its scenarios
// list, or, in a file without one, the one scenario at its top level. A file
// of one scenario used to run nothing: both decoders read it as an empty
// list, and the top level was read only when the list could not be read.
func TestParseScenarioFile(t *testing.T) {
	for _, tt := range []struct {
		name, file string
		types      []string // the types of the scenarios read
		lone       bool
		err        string // part of the error, for a file that is refused
	}{
		{"list.yaml", "scenarios:\n  - {name: a, type: LOAD}\n  - {name: b, type: STRESS}\n", []string{"LOAD", "STRESS"}, false, ""},
		{"lone.yaml", "name: a\ntype: LOAD\nspec:\n  records: 1000\n", []string{"LOAD"}, true, ""},
		// encoding/json matches a key to a field in any case; yaml.v3 does not.
		{"list.json", `{"Scenarios": [{"name": "a", "type": "LOAD"}]}`, []string{"LOAD"}, false, ""},
		{"lone.json", `{"name": "a", "Type": "LOAD"}`, []string{"LOAD"}, true, ""},
		{"lone-json.yaml", `{"name": "a", "Type": "LOAD"}`, nil, false, ""},
		// Text that is not JSON, in a file named .json, is read as YAML.
		{"yaml.json", "name: a\ntype: LOAD\n", []string{"LOAD"}, true, ""},
		// A list is read, and a scenario at the top level beside it is not.
		{"both.yaml", "type: STRESS\nscenarios:\n  - {name: a, type: LOAD}\n", []string{"LOAD"}, false, ""},
		{"empty.yaml", "scenarios: []\n", nil, false, ""},
		{"untyped.yaml", "name: a\nspec:\n  records: 1000\n", nil, false, ""},
		// With a type it is a scenario, so a value one of its fields cannot
		// hold is named, rather than the file read as holding none.
		{"bad.yaml", "name: a\ntype: LOAD\nvalidate:\n  maxP99LatencyMs: 50ms\n", nil, false, "cannot unmarshal !!str `50ms`"},
		{"bad.json", `{"type": "LOAD", "validate": {"maxOutOfOrder": 0.5}}`, nil, false, "maxOutOfOrder"},
		{"broken.yaml", "scenarios: [\n", nil, false, "yaml:"},
	} {
		sf, err := parseScenarioFile(tt.name, []byte(tt.file))
		var types []string
		for _, s := range sf.Scenarios {
			types = append(types, s.Type)
		}
		switch {
		case tt.err != "" && (err == nil || !strings.Contains(err.Error(), tt.err)):
			t.Errorf("%s: err = %v, want one that says %s", tt.name, err, tt.err)
		case tt.err == "" && err != nil:
			t.Errorf("%s: %v", tt.name, err)
		case !slices.Equal(types, tt.types) || sf.lone != tt.lone:
			t.Errorf("%s: read %v (lone %t), want %v (lone %t)", tt.name, types, sf.lone, tt.types, tt.lone)
		}
	}
}

// kates test apply runs a file of one scenario, written at its top level, in
// YAML or in JSON. It used to stop with "No scenarios found in file".
func TestApply_RunsAFileOfOneScenario(t *testing.T) {
	for _, tt := range []struct{ name, file string }{
		{"one.yaml", "name: one\ntype: load\nspec:\n  records: 1000\n  lingerMs: 0\n"},
		{"one.json", `{"name": "one", "type": "load", "spec": {"records": 1000, "lingerMs": 0}}`},
		{"none.yaml", "name: none\nspec:\n  records: 1000\n"},
	} {
		path := filepath.Join(t.TempDir(), tt.name)
		if err := os.WriteFile(path, []byte(tt.file), 0o600); err != nil {
			t.Fatal(err)
		}
		var posted []string
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			posted = append(posted, string(body))
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"run-1","testType":"LOAD","status":"PENDING"}`)
		}))
		apiClient = client.New(ts.URL)
		output.ResetForTesting()
		applyFile, applyWait = path, false

		err := testApplyCmd.RunE(testApplyCmd, nil)
		ts.Close()
		applyFile = ""

		if tt.name == "none.yaml" {
			if err == nil || !strings.Contains(err.Error(), "No scenarios found in file: it needs a scenarios list, or one scenario with a type") || len(posted) != 0 {
				t.Errorf("%s: err = %v, posted %q", tt.name, err, posted)
			}
			continue
		}
		if want := `{"type":"LOAD","spec":{"numRecords":1000,"lingerMs":0}}`; err != nil || len(posted) != 1 || posted[0] != want {
			t.Errorf("%s: err = %v, posted %q, want %s", tt.name, err, posted, want)
		}
	}
}

func TestScenarioToRequest_NilSpec(t *testing.T) {
	s := TestScenario{
		Type: "LOAD",
	}

	req := scenarioToRequest(s)

	if req.Spec != nil {
		t.Error("expected nil spec when no spec provided")
	}
}

func TestScenarioToRequest_EmptySpec(t *testing.T) {
	s := TestScenario{
		Type: "LOAD",
		Spec: map[string]interface{}{},
	}

	req := scenarioToRequest(s)

	if req.Spec == nil {
		t.Fatal("expected non-nil spec")
	}
	if req.Spec.Records != 0 {
		t.Errorf("expected 0 records for empty spec, got %d", req.Spec.Records)
	}
}

func TestScenarioToRequest_TypeUppercase(t *testing.T) {
	s := TestScenario{Type: "load"}
	req := scenarioToRequest(s)
	if req.TestType != "LOAD" {
		t.Errorf("expected LOAD, got %s", req.TestType)
	}
}

func TestToInt_Float64(t *testing.T) {
	if toInt(42.0) != 42 {
		t.Error("toInt(42.0) should be 42")
	}
}

func TestToInt_Int(t *testing.T) {
	if toInt(42) != 42 {
		t.Error("toInt(42) should be 42")
	}
}

func TestToInt_Unknown(t *testing.T) {
	if toInt("not-a-number") != 0 {
		t.Error("toInt(string) should be 0")
	}
}

// A flag that is not a YAML boolean is not sent as false. yes, on and a bare
// key all used to become an explicit false, which turns CRC checks or the
// producer's idempotence off where the backend would otherwise keep them on.
func TestScenarioToRequest_AFlagThatIsNotABooleanIsNotSent(t *testing.T) {
	var sf ScenarioFile
	if err := yaml.Unmarshal([]byte("scenarios:\n  - name: x\n    type: INTEGRITY\n    spec:\n      enableCrc: yes\n      enableIdempotence: on\n      enableTransactions:\n"), &sf); err != nil {
		t.Fatal(err)
	}
	req := scenarioToRequest(sf.Scenarios[0])
	if req.Spec.EnableCrc != nil || req.Spec.EnableIdempotence != nil || req.Spec.EnableTransactions != nil {
		body, _ := json.Marshal(req)
		t.Errorf("sent %s", body)
	}

	problems := scenarioSpecProblems(sf.Scenarios[0])
	if len(problems) != 3 || !strings.Contains(strings.Join(problems, "; "), "spec.enableCrc is yes; write true or false") {
		t.Errorf("problems = %q", problems)
	}
	sf.Scenarios[0].Spec = map[string]any{"enableCrc": false, "enableIdempotence": "true"}
	if problems := scenarioSpecProblems(sf.Scenarios[0]); len(problems) != 0 {
		t.Errorf("true and false, quoted or not, are fine: %q", problems)
	}
}

// kates test apply refuses a file with such a flag before it starts any of
// its tests.
func TestApply_RefusesAFlagThatIsNotABoolean(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.yaml")
	data := "scenarios:\n  - name: first\n    type: LOAD\n  - name: second\n    type: INTEGRITY\n    spec:\n      enableCrc: yes\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	var requests int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++ }))
	defer ts.Close()
	apiClient = client.New(ts.URL)
	output.ResetForTesting()
	applyFile, applyWait = path, false
	defer func() { applyFile = "" }()

	err := testApplyCmd.RunE(testApplyCmd, nil)

	if err == nil || !strings.Contains(err.Error(), "scenario 2 (second): spec.enableCrc is yes") {
		t.Errorf("err = %v", err)
	}
	if requests != 0 {
		t.Errorf("%d requests reached the backend", requests)
	}
}
