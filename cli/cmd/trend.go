package cmd

import (
	"context"
	"fmt"
	"strings"

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
			return cmdErr("--type is required (e.g. LOAD, ENDURANCE, BURST)")
		}

		result, err := apiClient.Trends(context.Background(), trendType, trendMetric, trendDays, trendBaseline)
		if err != nil {
			return cmdErr("Failed to fetch trends: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		output.Banner("Trend Analysis", fmt.Sprintf("%s · %s · %dd window", trendType, trendMetric, trendDays))

		output.KeyValue("Baseline", fmt.Sprintf("%.2f", result.Baseline))

		higherIsBetter := strings.Contains(strings.ToLower(trendMetric), "throughput")

		if len(result.DataPoints) == 0 {
			output.Hint("No data points in the selected range.")
		} else {
			values := make([]float64, len(result.DataPoints))
			for i, dp := range result.DataPoints {
				values[i] = dp.Value
			}

			output.SubHeader("Trend Chart")
			spark := output.SparklineColored(values, higherIsBetter)
			trendDir := "→"
			if len(values) >= 2 {
				first := values[0]
				last := values[len(values)-1]
				if last > first*1.05 {
					if higherIsBetter {
						trendDir = output.SuccessStyle.Render("↗")
					} else {
						trendDir = output.ErrorStyle.Render("↗")
					}
				} else if last < first*0.95 {
					if higherIsBetter {
						trendDir = output.ErrorStyle.Render("↘")
					} else {
						trendDir = output.SuccessStyle.Render("↘")
					}
				} else {
					trendDir = output.DimStyle.Render("→")
				}
			}
			fmt.Printf("  %s  %s  (%d data points)\n", spark, trendDir, len(values))
			fmt.Println()

			var minV, maxV, sum float64
			minV = values[0]
			maxV = values[0]
			for _, v := range values {
				sum += v
				if v < minV {
					minV = v
				}
				if v > maxV {
					maxV = v
				}
			}
			avg := sum / float64(len(values))
			output.KeyValue("Min", fmt.Sprintf("%.2f", minV))
			output.KeyValue("Max", fmt.Sprintf("%.2f", maxV))
			output.KeyValue("Average", fmt.Sprintf("%.2f", avg))

			output.SubHeader("Data Points")
			rows := make([][]string, 0, len(result.DataPoints))
			for _, dp := range result.DataPoints {
				ts := dp.Timestamp
				if len(ts) > 19 {
					ts = ts[:19]
				}
				marker := ""
				if result.Baseline > 0 {
					pct := ((dp.Value - result.Baseline) / result.Baseline) * 100
					if pct > 20 {
						marker = "▲"
					} else if pct < -20 {
						marker = "▼"
					}
				}
				rows = append(rows, []string{
					truncID(dp.RunID),
					ts,
					fmt.Sprintf("%.2f", dp.Value),
					marker,
				})
			}
			output.Table([]string{"Run ID", "Timestamp", "Value", ""}, rows)
		}

		if len(result.Regressions) > 0 {
			output.SubHeader("⚠ Regressions Detected")
			rows := make([][]string, 0, len(result.Regressions))
			for _, r := range result.Regressions {
				rows = append(rows, []string{
					r.RunID,
					fmt.Sprintf("%.2f", r.Value),
					fmt.Sprintf("%.2f", r.Baseline),
					fmt.Sprintf("%+.1f%%", r.DeviationPercent),
				})
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
