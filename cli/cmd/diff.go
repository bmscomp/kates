package cmd

import (
	"context"
	"fmt"
	"math"

	"github.com/klster/kates-cli/client"
	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

func summaryValue(s *client.ReportSummary, key string) float64 {
	switch key {
	case "totalRecords":
		return s.TotalRecords
	case "avgThroughputRecPerSec":
		return s.AvgThroughputRecPerSec
	case "peakThroughputRecPerSec":
		return s.PeakThroughputRecPerSec
	case "avgThroughputMBPerSec":
		return s.AvgThroughputMBPerSec
	case "avgLatencyMs":
		return s.AvgLatencyMs
	case "p50LatencyMs":
		return s.P50LatencyMs
	case "p95LatencyMs":
		return s.P95LatencyMs
	case "p99LatencyMs":
		return s.P99LatencyMs
	case "maxLatencyMs":
		return s.MaxLatencyMs
	case "errorRate":
		return s.ErrorRate
	default:
		return 0
	}
}

var reportDiffCmd = &cobra.Command{
	Use:   "diff <id1> <id2>",
	Short: "Side-by-side comparison of two test run reports",
	Args:  cobra.ExactArgs(2),
	Example: `  kates report diff abc123 def456
  kates report diff abc123 def456 -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		s1, err := apiClient.ReportSummary(context.Background(), args[0])
		if err != nil {
			return cmdErr("Failed to get report for " + args[0] + ": " + err.Error())
		}
		s2, err := apiClient.ReportSummary(context.Background(), args[1])
		if err != nil {
			return cmdErr("Failed to get report for " + args[1] + ": " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(map[string]interface{}{
				"baseline":   s1,
				"comparison": s2,
			})
			return nil
		}

		output.Banner("Report Diff", fmt.Sprintf("%s vs %s", truncID(args[0]), truncID(args[1])))

		type metric struct {
			label    string
			key      string
			unit     string
			higherOK bool
		}

		metrics := []metric{
			{"Avg Throughput", "avgThroughputRecPerSec", "rec/s", true},
			{"Peak Throughput", "peakThroughputRecPerSec", "rec/s", true},
			{"Avg MB/s", "avgThroughputMBPerSec", "MB/s", true},
			{"Avg Latency", "avgLatencyMs", "ms", false},
			{"P50 Latency", "p50LatencyMs", "ms", false},
			{"P95 Latency", "p95LatencyMs", "ms", false},
			{"P99 Latency", "p99LatencyMs", "ms", false},
			{"Max Latency", "maxLatencyMs", "ms", false},
			{"Error Rate", "errorRate", "%", false},
		}

		rows := make([][]string, 0, len(metrics))
		for _, m := range metrics {
			v1 := summaryValue(s1, m.key)
			v2 := summaryValue(s2, m.key)

			fmtV1 := formatMetricVal(v1, m.key, m.unit)
			fmtV2 := formatMetricVal(v2, m.key, m.unit)

			delta := ""
			indicator := ""
			if v1 != 0 {
				pctChange := ((v2 - v1) / math.Abs(v1)) * 100
				delta = fmt.Sprintf("%+.1f%%", pctChange)

				improved := (m.higherOK && pctChange > 0) || (!m.higherOK && pctChange < 0)
				regressed := (m.higherOK && pctChange < 0) || (!m.higherOK && pctChange > 0)

				if math.Abs(pctChange) < 2 {
					indicator = "="
				} else if improved {
					indicator = "▲"
				} else if regressed {
					indicator = "▼"
				}
			}

			rows = append(rows, []string{m.label, fmtV1, fmtV2, delta, indicator})
		}

		output.Table(
			[]string{"Metric", truncID(args[0]), truncID(args[1]), "Delta", ""},
			rows,
		)

		output.Hint(fmt.Sprintf("Baseline: %s │ Comparison: %s", truncID(args[0]), truncID(args[1])))
		output.Hint("▲ = improved │ ▼ = regressed │ = ≈ no change")
		return nil
	},
}

func init() {
	reportCmd.AddCommand(reportDiffCmd)
}
