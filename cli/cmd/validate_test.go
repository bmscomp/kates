package cmd

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/client"
)

func TestValidateSLAs_AllPass(t *testing.T) {
	run := &client.TestRun{
		Results: []client.PhaseResult{
			{P99LatencyMs: 40, AvgLatencyMs: 8, ThroughputRecordsPerSec: 60000},
		},
	}
	v := &ValidationSpec{MaxP99Latency: 50, MaxAvgLatency: 10, MinThroughput: 50000}
	violations := validateSLAs(run, v)
	if len(violations) != 0 {
		t.Errorf("expected no violations, got %v", violations)
	}
}

func TestValidateSLAs_P99Violation(t *testing.T) {
	run := &client.TestRun{
		Results: []client.PhaseResult{
			{P99LatencyMs: 120, AvgLatencyMs: 8, ThroughputRecordsPerSec: 60000},
		},
	}
	v := &ValidationSpec{MaxP99Latency: 50}
	violations := validateSLAs(run, v)
	if len(violations) == 0 {
		t.Fatal("expected p99 violation")
	}
	if !strings.Contains(violations[0], "p99") {
		t.Errorf("violation should mention p99, got: %s", violations[0])
	}
}

func TestValidateSLAs_ThroughputViolation(t *testing.T) {
	run := &client.TestRun{
		Results: []client.PhaseResult{
			{ThroughputRecordsPerSec: 5000},
		},
	}
	v := &ValidationSpec{MinThroughput: 10000}
	violations := validateSLAs(run, v)
	if len(violations) == 0 {
		t.Fatal("expected throughput violation")
	}
	if !strings.Contains(violations[0], "throughput") {
		t.Errorf("violation should mention throughput, got: %s", violations[0])
	}
}

func TestValidateSLAs_IntegrityViolation(t *testing.T) {
	run := &client.TestRun{
		Results: []client.PhaseResult{
			{
				Integrity: &client.IntegrityResult{
					DataLossPercent: 0.5,
					OutOfOrderCount: 10,
					CrcFailures:     2,
				},
			},
		},
	}
	v := &ValidationSpec{MaxDataLoss: -1, MaxOutOfOrder: -1, MaxCrcFailures: -1}
	violations := validateSLAs(run, v)
	if len(violations) != 0 {
		t.Errorf("negative thresholds should be ignored, got %v", violations)
	}

	v2 := &ValidationSpec{MaxOutOfOrder: 0, MaxCrcFailures: 0}
	violations2 := validateSLAs(run, v2)
	if len(violations2) < 2 {
		t.Errorf("expected at least 2 integrity violations, got %d: %v", len(violations2), violations2)
	}
}

// integrityRun decodes a one-phase run the way the CLI receives it, so these
// tests pin the wire keys the backend sends and not just the struct fields.
func integrityRun(t *testing.T, integrityJSON string) *client.TestRun {
	t.Helper()
	var run client.TestRun
	body := `{"results":[{"phaseName":"integrity","integrity":` + integrityJSON + `}]}`
	if err := json.Unmarshal([]byte(body), &run); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	return &run
}

func TestValidateSLAs_RtoRpoGatesFireOnMeasuredValues(t *testing.T) {
	// The keys IntegrityResult serializes: Duration components in seconds
	// alongside the millisecond accessors the gates read.
	run := integrityRun(t, `{"maxRto":45.0,"rpo":6.0,
		"producerRtoMs":45000.0,"consumerRtoMs":12000.0,"maxRtoMs":45000.0,"rpoMs":6000.0,"verdict":"PASS"}`)
	v := &ValidationSpec{MaxRtoMs: 30000, MaxRpoMs: 5000}

	violations := validateSLAs(run, v)
	want := []string{"rto=45000ms > 30000ms", "rpo=6000ms > 5000ms"}
	if !reflect.DeepEqual(violations, want) {
		t.Errorf("violations = %v, want %v", violations, want)
	}
	if gates := unevaluableSLAs(run, v); len(gates) != 0 {
		t.Errorf("measured values must be evaluable, got %v", gates)
	}
}

func TestValidateSLAs_RtoRpoWithinGatesPass(t *testing.T) {
	// A measured zero RPO is a real measurement ("nothing at risk"), not a gap.
	run := integrityRun(t, `{"maxRtoMs":1200.0,"rpoMs":0.0}`)
	v := &ValidationSpec{MaxRtoMs: 30000, MaxRpoMs: 5000}

	if violations := validateSLAs(run, v); len(violations) != 0 {
		t.Errorf("expected no violations, got %v", violations)
	}
	if gates := unevaluableSLAs(run, v); len(gates) != 0 {
		t.Errorf("expected every gate evaluated, got %v", gates)
	}
}

func TestValidateSLAs_UnmeasuredRpoIsNotEvaluable(t *testing.T) {
	// rpoMs -1 is the backend's "no chaos start to measure from".
	run := integrityRun(t, `{"maxRtoMs":1200.0,"rpoMs":-1.0}`)
	v := &ValidationSpec{MaxRtoMs: 30000, MaxRpoMs: 5000}

	if violations := validateSLAs(run, v); len(violations) != 0 {
		t.Errorf("an unmeasured RPO is not a violation, got %v", violations)
	}
	want := []string{"maxRpoMs (RPO not measured)"}
	if gates := unevaluableSLAs(run, v); !reflect.DeepEqual(gates, want) {
		t.Errorf("unevaluable = %v, want %v", gates, want)
	}
}

func TestValidateSLAs_MissingMsKeysAreNotEvaluable(t *testing.T) {
	// What the backend sent before its *Ms accessors were serialized: the
	// Durations in decimal seconds and nothing else. Read as zero, a 45s RTO
	// passed a 30s gate.
	run := integrityRun(t, `{"producerRto":45.0,"consumerRto":12.0,"maxRto":45.0,"rpo":6.0}`)
	v := &ValidationSpec{MaxRtoMs: 30000, MaxRpoMs: 5000}

	if violations := validateSLAs(run, v); len(violations) != 0 {
		t.Errorf("absent values cannot violate, got %v", violations)
	}
	want := []string{"maxRtoMs (RTO not measured)", "maxRpoMs (RPO not measured)"}
	if gates := unevaluableSLAs(run, v); !reflect.DeepEqual(gates, want) {
		t.Errorf("unevaluable = %v, want %v", gates, want)
	}
}

func TestUnevaluableSLAs_OnlyDeclaredGates(t *testing.T) {
	// No integrity check at all: only the gates the scenario declared are
	// reported, and a scenario without RTO/RPO gates reports nothing.
	run := &client.TestRun{Results: []client.PhaseResult{{P99LatencyMs: 10}}}

	if gates := unevaluableSLAs(run, &ValidationSpec{MaxP99Latency: 50}); len(gates) != 0 {
		t.Errorf("no RTO/RPO gates declared, got %v", gates)
	}
	want := []string{"maxRpoMs (RPO not measured)"}
	if gates := unevaluableSLAs(run, &ValidationSpec{MaxRpoMs: 5000}); !reflect.DeepEqual(gates, want) {
		t.Errorf("unevaluable = %v, want %v", gates, want)
	}
}

func TestValidateSLAs_MultipleViolations(t *testing.T) {
	run := &client.TestRun{
		Results: []client.PhaseResult{
			{P99LatencyMs: 200, AvgLatencyMs: 100, ThroughputRecordsPerSec: 500},
		},
	}
	v := &ValidationSpec{MaxP99Latency: 50, MaxAvgLatency: 10, MinThroughput: 10000}
	violations := validateSLAs(run, v)
	if len(violations) != 3 {
		t.Errorf("expected 3 violations, got %d: %v", len(violations), violations)
	}
}

func TestValidateSLAs_ZeroThresholds(t *testing.T) {
	run := &client.TestRun{
		Results: []client.PhaseResult{
			{P99LatencyMs: 10, ThroughputRecordsPerSec: 100},
		},
	}
	v := &ValidationSpec{}
	violations := validateSLAs(run, v)
	if len(violations) != 0 {
		t.Errorf("zero thresholds should produce no violations, got %v", violations)
	}
}

func TestValidateSLAs_EmptyResults(t *testing.T) {
	run := &client.TestRun{}
	v := &ValidationSpec{MaxP99Latency: 50}
	violations := validateSLAs(run, v)
	if len(violations) != 0 {
		t.Errorf("empty results should produce no violations, got %v", violations)
	}
}

func TestValidateSLAs_MultiplePhases(t *testing.T) {
	run := &client.TestRun{
		Results: []client.PhaseResult{
			{P99LatencyMs: 40, ThroughputRecordsPerSec: 60000},
			{P99LatencyMs: 120, ThroughputRecordsPerSec: 60000},
		},
	}
	v := &ValidationSpec{MaxP99Latency: 50}
	violations := validateSLAs(run, v)
	if len(violations) != 1 {
		t.Errorf("expected 1 violation from second phase, got %d: %v", len(violations), violations)
	}
}
