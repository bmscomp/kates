package cmd

import (
	"testing"
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
	if req.Spec.DurationSeconds != 300 {
		t.Errorf("expected 300 duration, got %d", req.Spec.DurationSeconds)
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
