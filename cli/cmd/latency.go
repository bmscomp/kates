package cmd

import "github.com/bmscomp/kates/cli/client"

// runLatency is a run's latency read from its task rows.
type runLatency struct {
	AvgMs, P50Ms, P95Ms, P99Ms, MaxMs float64
}

// latencyOf reads a run's latency from its task rows by the rule of the
// backend's report summary (MetricUtils.computeSummary), so a command that
// works from rows agrees with the report.
//
// Only rows that measured latency count (latencyRows). Percentiles are never
// averaged: each is the highest row's, which with one producer is that
// producer's own and with several an upper bound on the run's. The average is
// weighted by records.
func latencyOf(results []client.PhaseResult) runLatency {
	var l runLatency
	var records, weighted, sum float64
	rows := latencyRows(results)
	for _, r := range rows {
		records += r.RecordsSent
		weighted += r.AvgLatencyMs * r.RecordsSent
		sum += r.AvgLatencyMs
		l.P50Ms = max(l.P50Ms, r.P50LatencyMs)
		l.P95Ms = max(l.P95Ms, r.P95LatencyMs)
		l.P99Ms = max(l.P99Ms, r.P99LatencyMs)
		l.MaxMs = max(l.MaxMs, r.MaxLatencyMs)
	}
	switch {
	case records > 0:
		l.AvgMs = weighted / records
	case len(rows) > 0:
		l.AvgMs = sum / float64(len(rows))
	}
	return l
}

// latencyRows are the rows whose latency describes the run
// (MetricUtils.latencyRows). A consumer's row is left out: on the native
// backend it records no latency, so all its figures are 0, and on Trogdor it
// records how long each poll took. Rows of consumers alone keep them. A row
// that reports no latency at all is left out too.
func latencyRows(results []client.PhaseResult) []client.PhaseResult {
	var candidates []client.PhaseResult
	for _, r := range results {
		if r.PhaseName != "consume" {
			candidates = append(candidates, r)
		}
	}
	if len(candidates) == 0 {
		candidates = results
	}

	var rows []client.PhaseResult
	for _, r := range candidates {
		if r.AvgLatencyMs <= 0 && r.P50LatencyMs <= 0 && r.P95LatencyMs <= 0 && r.P99LatencyMs <= 0 && r.MaxLatencyMs <= 0 {
			continue
		}
		rows = append(rows, r)
	}
	return rows
}

// measuredLatency reports whether any row measured latency. When none did, a
// run's latency figures are all 0, which means not measured: the backend's
// report then fails a latency gate as "not measured" (MetricUtils.measuredLatency,
// SlaEvaluator), and a command that judges latency itself fails it too.
func measuredLatency(results []client.PhaseResult) bool {
	return len(latencyRows(results)) > 0
}

// summaryMeasuredLatency is measuredLatency for a report summary, which keeps
// no rows: computeSummary leaves every latency figure at 0 only when no row
// measured latency, and a row that did has at least one above 0.
func summaryMeasuredLatency(s *client.ReportSummary) bool {
	return s.AvgLatencyMs > 0 || s.P50LatencyMs > 0 || s.P95LatencyMs > 0 || s.P99LatencyMs > 0 || s.MaxLatencyMs > 0
}
