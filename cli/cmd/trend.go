package cmd

import (
	"fmt"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var (
	trendType     string
	trendMetric   string
	trendDays     int
	trendBaseline int
)

var trendCmd = &cobra.Command{
	Use:   "trend",
	Short: "Analyze historical performance trends",
	Example: `  kates trend --type LOAD --metric p99LatencyMs --days 30
  kates trend --type ENDURANCE --metric avgThroughputRecPerSec --days 7 --baseline 3`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if trendType == "" {
			output.Error("--type is required (e.g. LOAD, ENDURANCE, BURST)")
			return nil
		}

		result, err := apiClient.Trends(trendType, trendMetric, trendDays, trendBaseline)
		if err != nil {
			output.Error("Failed to fetch trends: " + err.Error())
			return nil
		}

		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		output.Header(fmt.Sprintf("Trend: %s / %s (%d days)", trendType, trendMetric, trendDays))

		baseline := numVal(result, "baseline")
		output.KeyValue("Baseline", fmt.Sprintf("%.2f", baseline))

		// Data points
		if points, ok := result["dataPoints"].([]interface{}); ok {
			output.SubHeader("Data Points")
			if len(points) == 0 {
				output.Hint("  No data points in the selected range.")
			} else {
				rows := make([][]string, 0, len(points))
				for _, p := range points {
					if pm, ok := p.(map[string]interface{}); ok {
						ts := valStr(pm, "timestamp")
						if len(ts) > 19 {
							ts = ts[:19]
						}
						value := numVal(pm, "value")
						marker := ""
						if baseline > 0 {
							pct := ((value - baseline) / baseline) * 100
							if pct > 20 {
								marker = "▲"
							} else if pct < -20 {
								marker = "▼"
							}
						}
						rows = append(rows, []string{
							valStr(pm, "runId"),
							ts,
							fmt.Sprintf("%.2f", value),
							marker,
						})
					}
				}
				output.Table([]string{"Run ID", "Timestamp", "Value", ""}, rows)
			}
		}

		// Regressions
		if regressions, ok := result["regressions"].([]interface{}); ok && len(regressions) > 0 {
			output.SubHeader("⚠ Regressions Detected")
			rows := make([][]string, 0, len(regressions))
			for _, r := range regressions {
				if rm, ok := r.(map[string]interface{}); ok {
					rows = append(rows, []string{
						valStr(rm, "runId"),
						fmt.Sprintf("%.2f", numVal(rm, "value")),
						fmt.Sprintf("%.2f", numVal(rm, "baseline")),
						fmt.Sprintf("%+.1f%%", numVal(rm, "deviationPercent")),
					})
				}
			}
			output.Table([]string{"Run ID", "Value", "Baseline", "Deviation"}, rows)
		}

		return nil
	},
}

func init() {
	trendCmd.Flags().StringVar(&trendType, "type", "", "Test type (required)")
	trendCmd.Flags().StringVar(&trendMetric, "metric", "avgThroughputRecPerSec", "Metric to analyze")
	trendCmd.Flags().IntVar(&trendDays, "days", 30, "Look-back window in days")
	trendCmd.Flags().IntVar(&trendBaseline, "baseline", 5, "Number of recent runs for baseline")

	rootCmd.AddCommand(trendCmd)
}
