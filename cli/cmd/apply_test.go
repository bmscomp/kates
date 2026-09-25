package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	if spec.BatchSize != 131072 {
		t.Errorf("expected batchSize=131072, got %d", spec.BatchSize)
	}
	if spec.LingerMs != 10 {
		t.Errorf("expected lingerMs=10, got %d", spec.LingerMs)
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
	if spec.FetchMaxWaitMs != 1000 {
		t.Errorf("expected fetchMaxWaitMs=1000, got %d", spec.FetchMaxWaitMs)
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
