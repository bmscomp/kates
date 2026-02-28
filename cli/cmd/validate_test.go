package cmd

import (
	"strings"
	"testing"

	"github.com/klster/kates-cli/client"
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
