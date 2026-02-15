package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/klster/kates-cli/client"
	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var (
	trendType      string
	trendMetric    string
	trendDays      int
	trendBaseline  int
	trendPhase     string
	trendAllPhases bool
	trendBroker    int
)

var trendCmd = &cobra.Command{
	Use:   "trend",
	Short: "Analyze historical performance trends",
	Example: `  kates trend --type LOAD --metric p99LatencyMs --days 30
  kates trend --type SPIKE --phase spike --metric avgThroughputRecPerSec
  kates trend --type ENDURANCE --all-phases --metric p99LatencyMs
  kates trend phases --type SPIKE --days 30`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if trendType == "" {
			return cmdErr("--type is required (e.g. LOAD, ENDURANCE, SPIKE)")
		}

		if trendAllPhases {
			return runAllPhases()
		}

		if trendBroker >= 0 {
			return runBrokerTrend()
		}

		result, err := apiClient.Trends(context.Background(), trendType, trendMetric, trendDays, trendBaseline, trendPhase)
		if err != nil {
			return cmdErr("Failed to fetch trends: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		subtitle := fmt.Sprintf("%s · %s · %dd window", trendType, trendMetric, trendDays)
		if result.Phase != "" {
			subtitle = fmt.Sprintf("%s · phase:%s · %s · %dd window", trendType, result.Phase, trendMetric, trendDays)
		}
		output.Banner("Trend Analysis", subtitle)

		output.KeyValue("Baseline", fmt.Sprintf("%.2f", result.Baseline))

		higherIsBetter := strings.Contains(strings.ToLower(trendMetric), "throughput")

		if len(result.DataPoints) == 0 {
			output.Hint("No data points in the selected range.")
		} else {
			renderTrendDataPoints(result.DataPoints, result.Baseline, higherIsBetter)
		}

		renderTrendRegressions(result.Regressions)
		return nil
	},
}

var trendPhasesCmd = &cobra.Command{
	Use:   "phases",
	Short: "List available phase names for a test type",
	Example: `  kates trend phases --type SPIKE
  kates trend phases --type ENDURANCE --days 7`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if trendType == "" {
			return cmdErr("--type is required")
		}

		phases, err := apiClient.TrendPhases(context.Background(), trendType, trendDays)
		if err != nil {
			return cmdErr("Failed to fetch phases: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(phases)
			return nil
		}

		if len(phases) == 0 {
			output.Hint("No phases found for type " + trendType + " in the last " + fmt.Sprintf("%d", trendDays) + " days.")
			return nil
		}

		output.Banner("Phases", fmt.Sprintf("%s · %dd window", trendType, trendDays))
		rows := make([][]string, 0, len(phases))
		for _, p := range phases {
			rows = append(rows, []string{p})
		}
		output.Table([]string{"Phase Name"}, rows)
		return nil
	},
}

func runAllPhases() error {
	result, err := apiClient.TrendBreakdown(context.Background(), trendType, trendMetric, trendDays, trendBaseline)
	if err != nil {
		return cmdErr("Failed to fetch breakdown: " + err.Error())
	}

	if outputMode == "json" {
		output.JSON(result)
		return nil
	}

	output.Banner("Phase Breakdown", fmt.Sprintf("%s · %s · %dd window", trendType, trendMetric, trendDays))

	if len(result.Phases) == 0 {
		output.Hint("No phase data available.")
		return nil
	}

	higherIsBetter := strings.Contains(strings.ToLower(trendMetric), "throughput")

	for _, pt := range result.Phases {
		output.SubHeader(fmt.Sprintf("Phase: %s", pt.Phase))
		output.KeyValue("Baseline", fmt.Sprintf("%.2f", pt.Baseline))

		if len(pt.DataPoints) == 0 {
			output.Hint("  No data points.")
			continue
		}

		renderTrendDataPoints(pt.DataPoints, pt.Baseline, higherIsBetter)
		renderTrendRegressions(pt.Regressions)
	}

	return nil
}

func renderTrendDataPoints(dataPoints []client.DataPoint, baseline float64, higherIsBetter bool) {
	values := make([]float64, len(dataPoints))
	for i, dp := range dataPoints {
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
	rows := make([][]string, 0, len(dataPoints))
	for _, dp := range dataPoints {
		ts := dp.Timestamp
		if len(ts) > 19 {
			ts = ts[:19]
		}
		marker := ""
		if baseline > 0 {
			pct := ((dp.Value - baseline) / baseline) * 100
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

func renderTrendRegressions(regressions []client.Regression) {
	if len(regressions) == 0 {
		return
	}
	output.SubHeader("⚠ Regressions Detected")
	rows := make([][]string, 0, len(regressions))
	for _, r := range regressions {
		rows = append(rows, []string{
			r.RunID,
			fmt.Sprintf("%.2f", r.Value),
			fmt.Sprintf("%.2f", r.Baseline),
			fmt.Sprintf("%+.1f%%", r.DeviationPercent),
		})
	}
	output.Table([]string{"Run ID", "Value", "Baseline", "Deviation"}, rows)
}

func runBrokerTrend() error {
	result, err := apiClient.BrokerTrend(
		context.Background(), trendType, trendMetric, trendBroker, trendDays, trendBaseline)
	if err != nil {
		return cmdErr("Failed to fetch broker trend: " + err.Error())
	}

	if outputMode == "json" {
		output.JSON(result)
		return nil
	}

	output.Banner("Broker Trend Analysis",
		fmt.Sprintf("%s · broker:%d · %s · %dd window", trendType, trendBroker, trendMetric, trendDays))

	output.KeyValue("Baseline", fmt.Sprintf("%.2f", result.Baseline))

	higherIsBetter := strings.Contains(strings.ToLower(trendMetric), "throughput")

	if len(result.DataPoints) == 0 {
		output.Hint("No data points for this broker in the selected range.")
	} else {
		values := make([]float64, len(result.DataPoints))
		for i, dp := range result.DataPoints {
			values[i] = dp.Value
		}
		output.SubHeader("Sparkline")
		fmt.Println("  " + output.SparklineColored(values, higherIsBetter))
		fmt.Println()
		renderTrendDataPoints(result.DataPoints, result.Baseline, higherIsBetter)
	}

	renderTrendRegressions(result.Regressions)
	return nil
}

func init() {
	trendCmd.Flags().StringVar(&trendType, "type", "", "Test type (required)")
	trendCmd.Flags().StringVar(&trendMetric, "metric", "avgThroughputRecPerSec", "Metric to analyze")
	trendCmd.Flags().IntVar(&trendDays, "days", 30, "Look-back window in days")
	trendCmd.Flags().IntVar(&trendBaseline, "baseline", 5, "Number of recent runs for baseline")
	trendCmd.Flags().StringVar(&trendPhase, "phase", "", "Phase name to analyze (omit for overall)")
	trendCmd.Flags().BoolVar(&trendAllPhases, "all-phases", false, "Show trends for all phases side-by-side")
	trendCmd.Flags().IntVar(&trendBroker, "broker", -1, "Broker ID to scope trend analysis")

	trendPhasesCmd.Flags().StringVar(&trendType, "type", "", "Test type (required)")
	trendPhasesCmd.Flags().IntVar(&trendDays, "days", 30, "Look-back window in days")

	trendCmd.AddCommand(trendPhasesCmd)
	rootCmd.AddCommand(trendCmd)
}
