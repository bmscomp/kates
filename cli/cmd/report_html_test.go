package cmd

import (
	"strings"
	"testing"

	"github.com/klster/kates-cli/client"
)

func TestRenderHTMLReport_Basic(t *testing.T) {
	report := &client.Report{
		Summary: &client.ReportSummary{
			TotalRecords:            100000,
			AvgThroughputRecPerSec:  50000,
			PeakThroughputRecPerSec: 75000,
			AvgThroughputMBPerSec:   48.8,
			AvgLatencyMs:            5.2,
			P50LatencyMs:            4.1,
			P95LatencyMs:            12.3,
			P99LatencyMs:            25.7,
			MaxLatencyMs:            120.0,
			ErrorRate:               0.0001,
		},
	}

	html := renderHTMLReport("test-123", report)

	expectations := []string{
		"<!DOCTYPE html>",
		"<html",
		"Performance Report",
		"test-123",
		"Throughput",
		"Latency Distribution",
		"Reliability",
		"100.0K",
		"bar-fill",
	}
	for _, exp := range expectations {
		if !strings.Contains(html, exp) {
			t.Errorf("HTML report missing: %s", exp)
		}
	}
}

func TestRenderHTMLReport_HasCSS(t *testing.T) {
	report := &client.Report{Summary: &client.ReportSummary{TotalRecords: 1}}
	html := renderHTMLReport("css-test", report)

	cssProps := []string{"--bg:", "--surface:", "--accent:", "--green:", "--red:"}
	for _, prop := range cssProps {
		if !strings.Contains(html, prop) {
			t.Errorf("HTML report missing CSS property: %s", prop)
		}
	}
}

func TestRenderHTMLReport_SLAPassed(t *testing.T) {
	report := &client.Report{
		Summary:           &client.ReportSummary{TotalRecords: 1},
		OverallSlaVerdict: &client.SlaVerdict{Passed: true},
	}

	html := renderHTMLReport("sla-pass", report)

	if !strings.Contains(html, "badge-pass") {
		t.Error("expected pass badge class")
	}
	if !strings.Contains(html, "PASSED") {
		t.Error("expected PASSED text")
	}
}

func TestRenderHTMLReport_SLAFailed(t *testing.T) {
	report := &client.Report{
		Summary: &client.ReportSummary{TotalRecords: 1},
		OverallSlaVerdict: &client.SlaVerdict{
			Passed: false,
			Violations: []client.SlaViolation{
				{Metric: "p99LatencyMs", Threshold: 50, Actual: 120},
				{Metric: "throughput", Threshold: 100000, Actual: 50000},
			},
		},
	}

	html := renderHTMLReport("sla-fail", report)

	if !strings.Contains(html, "badge-fail") {
		t.Error("expected fail badge class")
	}
	if !strings.Contains(html, "p99LatencyMs") {
		t.Error("expected violation metric")
	}
}

func TestRenderHTMLReport_WithPhases(t *testing.T) {
	report := &client.Report{
		Summary: &client.ReportSummary{TotalRecords: 1},
		Phases: []client.ReportPhase{
			{Name: "warmup", Status: "DONE", RecordsSent: 500},
			{Name: "burst", Status: "FAILED", RecordsSent: 200},
		},
	}

	html := renderHTMLReport("phase-test", report)

	if !strings.Contains(html, "Phase Breakdown") {
		t.Error("expected phase breakdown section")
	}
	if !strings.Contains(html, "warmup") {
		t.Error("expected warmup phase")
	}
	if !strings.Contains(html, "burst") {
		t.Error("expected burst phase")
	}
}

func TestRenderHTMLReport_NilSummary(t *testing.T) {
	report := &client.Report{}
	html := renderHTMLReport("nil-test", report)
	if !strings.Contains(html, "<!DOCTYPE html>") {
		t.Error("should still produce valid HTML")
	}
}

func TestRenderHTMLReport_SelfContained(t *testing.T) {
	report := &client.Report{Summary: &client.ReportSummary{TotalRecords: 1}}
	html := renderHTMLReport("self-test", report)

	if strings.Contains(html, "link rel=\"stylesheet\"") {
		t.Error("HTML should not reference external stylesheets")
	}
	if strings.Contains(html, "<script src=") {
		t.Error("HTML should not reference external scripts")
	}
	if !strings.Contains(html, "<style>") {
		t.Error("HTML should have inline styles")
	}
}

func TestRenderHTMLReport_ErrorRateColors(t *testing.T) {
	t.Run("green for zero errors", func(t *testing.T) {
		report := &client.Report{Summary: &client.ReportSummary{ErrorRate: 0}}
		html := renderHTMLReport("green-test", report)
		if !strings.Contains(html, "var(--green)") {
			t.Error("zero error rate should use green")
		}
	})

	t.Run("red for high errors", func(t *testing.T) {
		report := &client.Report{Summary: &client.ReportSummary{ErrorRate: 0.05}}
		html := renderHTMLReport("red-test", report)
		if !strings.Contains(html, "var(--red)") {
			t.Error("high error rate should use red")
		}
	})
}
