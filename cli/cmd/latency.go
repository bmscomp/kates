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
// Only rows that measured latency count. A consumer's row is left out: on the
// native backend it records no latency, so all its figures are 0, and on
// Trogdor it records how long each poll took. Rows of consumers alone keep
// them. A row that reports no latency at all is left out too. Percentiles are
// never averaged: each is the highest row's, which with one producer is that
// producer's own and with several an upper bound on the run's. The average is
// weighted by records.
func latencyOf(results []client.PhaseResult) runLatency {
	var candidates []client.PhaseResult
	for _, r := range results {
		if r.PhaseName != "consume" {
			candidates = append(candidates, r)
		}
	}
	if len(candidates) == 0 {
		candidates = results
	}

	var l runLatency
	var records, weighted, sum float64
	n := 0
	for _, r := range candidates {
		if r.AvgLatencyMs <= 0 && r.P50LatencyMs <= 0 && r.P95LatencyMs <= 0 && r.P99LatencyMs <= 0 && r.MaxLatencyMs <= 0 {
			continue
		}
		n++
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
	case n > 0:
		l.AvgMs = sum / float64(n)
	}
	return l
}
