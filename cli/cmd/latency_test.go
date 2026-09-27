package cmd

import (
	"testing"

	"github.com/bmscomp/kates/cli/client"
)

func TestLatencyOf(t *testing.T) {
	produce := client.PhaseResult{PhaseName: "produce", RecordsSent: 10000,
		AvgLatencyMs: 12, P50LatencyMs: 8, P95LatencyMs: 25, P99LatencyMs: 40, MaxLatencyMs: 95}
	nativeConsume := client.PhaseResult{PhaseName: "consume", RecordsSent: 10000}
	trogdorConsume := client.PhaseResult{PhaseName: "consume", RecordsSent: 10000,
		AvgLatencyMs: 300, P50LatencyMs: 250, P95LatencyMs: 480, P99LatencyMs: 500}

	tests := []struct {
		name    string
		results []client.PhaseResult
		want    runLatency
	}{
		{
			// The consumer's 0s used to halve the producer's figures.
			name:    "LOAD run is its producer's",
			results: []client.PhaseResult{produce, nativeConsume},
			want:    runLatency{AvgMs: 12, P50Ms: 8, P95Ms: 25, P99Ms: 40, MaxMs: 95},
		},
		{
			name:    "Trogdor poll time is not the run's latency",
			results: []client.PhaseResult{trogdorConsume, produce},
			want:    runLatency{AvgMs: 12, P50Ms: 8, P95Ms: 25, P99Ms: 40, MaxMs: 95},
		},
		{
			name: "several producers: highest percentile, record-weighted average",
			results: []client.PhaseResult{
				{PhaseName: "produce", RecordsSent: 3000, AvgLatencyMs: 10, P50LatencyMs: 5, P95LatencyMs: 20, P99LatencyMs: 30, MaxLatencyMs: 70},
				{PhaseName: "produce", RecordsSent: 1000, AvgLatencyMs: 30, P50LatencyMs: 15, P95LatencyMs: 45, P99LatencyMs: 60, MaxLatencyMs: 90},
			},
			want: runLatency{AvgMs: 15, P50Ms: 15, P95Ms: 45, P99Ms: 60, MaxMs: 90},
		},
		{
			name:    "a producer without a sample does not pull the average down",
			results: []client.PhaseResult{produce, {PhaseName: "produce", RecordsSent: 10000, Error: "NOT_ENOUGH_REPLICAS"}},
			want:    runLatency{AvgMs: 12, P50Ms: 8, P95Ms: 25, P99Ms: 40, MaxMs: 95},
		},
		{
			name: "rows without phase names still ignore the consumer",
			results: []client.PhaseResult{
				{RecordsSent: 10000, AvgLatencyMs: 12, P99LatencyMs: 40},
				{RecordsSent: 10000},
			},
			want: runLatency{AvgMs: 12, P99Ms: 40},
		},
		{
			name:    "consumers alone keep what they measured",
			results: []client.PhaseResult{trogdorConsume},
			want:    runLatency{AvgMs: 300, P50Ms: 250, P95Ms: 480, P99Ms: 500},
		},
		{
			name:    "nothing measured",
			results: []client.PhaseResult{nativeConsume},
		},
		{
			name: "no results",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := latencyOf(tt.results); got != tt.want {
				t.Errorf("latencyOf = %+v, want %+v", got, tt.want)
			}
		})
	}
}
