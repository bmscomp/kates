package cmd

import (
	"fmt"
	"math"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var reportDiffCmd = &cobra.Command{
	Use:   "diff <id1> <id2>",
	Short: "Side-by-side comparison of two test run reports",
	Args:  cobra.ExactArgs(2),
	Example: `  kates report diff abc123 def456
  kates report diff abc123 def456 -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		s1, err := apiClient.ReportSummary(args[0])
		if err != nil {
			output.Error("Failed to get report for " + args[0] + ": " + err.Error())
			return nil
		}
		s2, err := apiClient.ReportSummary(args[1])
		if err != nil {
			output.Error("Failed to get report for " + args[1] + ": " + err.Error())
			return nil
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
			higherOK bool // true = higher is better (throughput), false = lower is better (latency)
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
			v1 := numVal(s1, m.key)
			v2 := numVal(s2, m.key)

			// Format values
			fmtV1 := formatMetricVal(v1, m.key, m.unit)
			fmtV2 := formatMetricVal(v2, m.key, m.unit)

			// Compute delta
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

func formatMetricVal(v float64, key, unit string) string {
	if key == "errorRate" {
		return fmt.Sprintf("%.4f%%", v*100)
	}
	if v >= 1_000_000 {
		return fmt.Sprintf("%.1fM %s", v/1_000_000, unit)
	}
	if v >= 1_000 {
		return fmt.Sprintf("%.1fK %s", v/1_000, unit)
	}
	return fmt.Sprintf("%.2f %s", v, unit)
}

func init() {
	reportCmd.AddCommand(reportDiffCmd)
}
