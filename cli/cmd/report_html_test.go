package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/client"
)

func mustRenderHTMLReport(t *testing.T, id string, r *client.Report) string {
	t.Helper()
	page, err := renderHTMLReport(id, r)
	if err != nil {
		t.Fatalf("renderHTMLReport: %v", err)
	}
	return page
}

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

	html := mustRenderHTMLReport(t, "test-123", report)

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
		"https://github.com/bmscomp/kates",
	}
	for _, exp := range expectations {
		if !strings.Contains(html, exp) {
			t.Errorf("HTML report missing: %s", exp)
		}
	}
}

func TestRenderHTMLReport_HasCSS(t *testing.T) {
	report := &client.Report{Summary: &client.ReportSummary{TotalRecords: 1}}
	html := mustRenderHTMLReport(t, "css-test", report)

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

	html := mustRenderHTMLReport(t, "sla-pass", report)

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

	html := mustRenderHTMLReport(t, "sla-fail", report)

	if !strings.Contains(html, "badge-fail") {
		t.Error("expected fail badge class")
	}
	if !strings.Contains(html, "p99LatencyMs") {
		t.Error("expected violation metric")
	}
}

func TestRenderHTMLReport_ViolationWithAReason(t *testing.T) {
	report := &client.Report{
		OverallSlaVerdict: &client.SlaVerdict{
			Violations: []client.SlaViolation{
				{Metric: "status", Threshold: -1, Actual: -1, Reason: "FAILED: <script>alert(1)</script>"},
				{Metric: "p99LatencyMs", Threshold: 50, Actual: -1, Reason: "not measured"},
			},
		},
	}

	html := mustRenderHTMLReport(t, "reason-test", report)

	// A task's error is the backend's text, not markup.
	if strings.Contains(html, "<script>") || !strings.Contains(html, "FAILED: &lt;script&gt;") {
		t.Errorf("the reason is not escaped:\n%s", html)
	}
	if !strings.Contains(html, "not measured") || strings.Contains(html, "-1.00") {
		t.Errorf("the reason does not stand in for the missing value:\n%s", html)
	}
}

func TestRenderHTMLReport_WithPhases(t *testing.T) {
	report := &client.Report{
		Summary: &client.ReportSummary{TotalRecords: 1},
		Phases: []client.ReportPhase{
			{Name: "warmup", Metrics: &client.ReportSummary{TotalRecords: 500}, SlaVerdict: &client.SlaVerdict{Passed: true}},
			{Name: "<script>", Metrics: &client.ReportSummary{TotalRecords: 200}, SlaVerdict: &client.SlaVerdict{Passed: false}},
		},
	}

	html := mustRenderHTMLReport(t, "phase-test", report)

	if !strings.Contains(html, "Phase Breakdown") {
		t.Error("expected phase breakdown section")
	}
	if !strings.Contains(html, "<td>warmup</td>\n      <td><span class=\"badge badge-pass\">") {
		t.Errorf("a phase whose SLA passed has no pass badge:\n%s", html)
	}
	// A phase name comes from the user's scenario file, not markup.
	if strings.Contains(html, "<script>") || !strings.Contains(html, "<td>&lt;script&gt;</td>\n      <td><span class=\"badge badge-fail\">") {
		t.Errorf("the phase name is not escaped, or a failed SLA has no fail badge:\n%s", html)
	}
}

func TestRenderHTMLReport_NilSummary(t *testing.T) {
	report := &client.Report{}
	html := mustRenderHTMLReport(t, "nil-test", report)
	if !strings.Contains(html, "<!DOCTYPE html>") {
		t.Error("should still produce valid HTML")
	}
}

func TestRenderHTMLReport_SelfContained(t *testing.T) {
	report := &client.Report{Summary: &client.ReportSummary{TotalRecords: 1}}
	html := mustRenderHTMLReport(t, "self-test", report)

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
		html := mustRenderHTMLReport(t, "green-test", report)
		if !strings.Contains(html, "var(--green)") {
			t.Error("zero error rate should use green")
		}
	})

	t.Run("red for high errors", func(t *testing.T) {
		report := &client.Report{Summary: &client.ReportSummary{ErrorRate: 0.05}}
		html := mustRenderHTMLReport(t, "red-test", report)
		if !strings.Contains(html, "var(--red)") {
			t.Error("high error rate should use red")
		}
	})
}

// The page against golden files, one per SLA outcome, so a change to the
// template shows up as a diff: every section, a violation with a reason, a
// phase with no metrics and one with no verdict, and a name that is markup.
func TestRenderHTMLReport_Golden(t *testing.T) {
	summary := &client.ReportSummary{
		TotalRecords: 1_250_000, AvgThroughputRecPerSec: 41_666.7, PeakThroughputRecPerSec: 52_000,
		AvgThroughputMBPerSec: 40.69, AvgLatencyMs: 3.25, P50LatencyMs: 2.5, P95LatencyMs: 9.75,
		P99LatencyMs: 18.5, MaxLatencyMs: 250, ErrorRate: 0.0005,
	}
	cases := map[string]*client.Report{
		"report-failed.golden.html": {
			Summary: summary,
			OverallSlaVerdict: &client.SlaVerdict{Violations: []client.SlaViolation{
				{Metric: "p99LatencyMs", Threshold: 10, Actual: 18.5},
				{Metric: "status", Threshold: -1, Actual: -1, Reason: "FAILED: <b>broker 2 down</b> & retried"},
			}},
			Phases: []client.ReportPhase{
				{Name: "warmup", Metrics: &client.ReportSummary{TotalRecords: 5_000, AvgThroughputRecPerSec: 2_500.5, P99LatencyMs: 4.25},
					SlaVerdict: &client.SlaVerdict{Passed: true}},
				{Name: "<peak>", Metrics: summary, SlaVerdict: &client.SlaVerdict{}},
				{Name: "cooldown"},
			},
		},
		"report-passed.golden.html": {
			Summary:           &client.ReportSummary{TotalRecords: 999, ErrorRate: 0.01, AvgLatencyMs: 1.5},
			OverallSlaVerdict: &client.SlaVerdict{Passed: true},
		},
	}
	for name, report := range cases {
		t.Run(name, func(t *testing.T) {
			got := mustRenderHTMLReport(t, "0b5e7a2c-91d4-4c3e-8f00-6d2b9a7e1f42", report)
			golden := filepath.Join("testdata", name)
			if os.Getenv("UPDATE_GOLDEN") == "1" {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v; run UPDATE_GOLDEN=1 go test ./cmd -run TestRenderHTMLReport_Golden", err)
			}
			if got != string(want) {
				t.Errorf("the page differs from %s:\n%s", golden, got)
			}
		})
	}
}
