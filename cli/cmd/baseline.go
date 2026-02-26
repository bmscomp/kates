package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var (
	regressionGreen = lipgloss.NewStyle().Foreground(lipgloss.Color("#00C853"))
	regressionRed   = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF1744"))
)

var testBaselineCmd = &cobra.Command{
	Use:   "baseline",
	Short: "Manage test baselines for regression detection",
}

var baselineSetCmd = &cobra.Command{
	Use:   "set <run-id>",
	Short: "Mark a test run as the baseline for its type",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		test, err := apiClient.GetTest(ctx, args[0])
		if err != nil {
			return fmt.Errorf("fetch test run: %w", err)
		}
		testType := test.TestType

		entry, err := apiClient.BaselineSet(ctx, testType, args[0])
		if err != nil {
			return fmt.Errorf("set baseline: %w", err)
		}

		if outputMode == "json" {
			data, _ := json.MarshalIndent(entry, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		fmt.Printf("✓ Baseline set for %s → run %s\n", entry.TestType, entry.RunID)
		return nil
	},
}

var baselineUnsetCmd = &cobra.Command{
	Use:   "unset <type>",
	Short: "Remove the baseline for a test type",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		err := apiClient.BaselineUnset(context.Background(), strings.ToUpper(args[0]))
		if err != nil {
			return fmt.Errorf("unset baseline: %w", err)
		}
		fmt.Printf("✓ Baseline removed for %s\n", strings.ToUpper(args[0]))
		return nil
	},
}

var baselineShowCmd = &cobra.Command{
	Use:   "show <type>",
	Short: "Show the current baseline for a test type",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		entry, err := apiClient.BaselineGet(context.Background(), strings.ToUpper(args[0]))
		if err != nil {
			return fmt.Errorf("get baseline: %w", err)
		}

		if outputMode == "json" {
			data, _ := json.MarshalIndent(entry, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		fmt.Printf("Type:    %s\n", entry.TestType)
		fmt.Printf("Run ID:  %s\n", entry.RunID)
		fmt.Printf("Set At:  %s\n", entry.SetAt)
		return nil
	},
}

var baselineListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all configured baselines",
	RunE: func(cmd *cobra.Command, args []string) error {
		entries, err := apiClient.BaselineList(context.Background())
		if err != nil {
			return fmt.Errorf("list baselines: %w", err)
		}

		if outputMode == "json" {
			data, _ := json.MarshalIndent(entries, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		if len(entries) == 0 {
			fmt.Println("No baselines configured.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "TYPE\tRUN ID\tSET AT")
		for _, e := range entries {
			fmt.Fprintf(w, "%s\t%s\t%s\n", e.TestType, e.RunID, e.SetAt)
		}
		w.Flush()
		return nil
	},
}

var reportRegressionCmd = &cobra.Command{
	Use:   "regression <run-id>",
	Short: "Compare a test run against its type's baseline",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		report, err := apiClient.ReportRegression(context.Background(), args[0])
		if err != nil {
			return fmt.Errorf("regression check: %w", err)
		}

		if outputMode == "json" {
			data, _ := json.MarshalIndent(report, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		header := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#B388FF"))
		fmt.Println(header.Render("Regression Report"))
		fmt.Println()
		fmt.Printf("  Run:      %s\n", report.RunID)
		fmt.Printf("  Baseline: %s\n", report.BaselineID)
		fmt.Printf("  Type:     %s\n", report.TestType)
		fmt.Println()

		metricOrder := []string{
			"avgThroughputRecPerSec",
			"peakThroughputRecPerSec",
			"avgLatencyMs",
			"p50LatencyMs",
			"p95LatencyMs",
			"p99LatencyMs",
			"maxLatencyMs",
			"errorRate",
		}

		metricLabels := map[string]string{
			"avgThroughputRecPerSec":  "Avg Throughput (rec/s)",
			"peakThroughputRecPerSec": "Peak Throughput (rec/s)",
			"avgLatencyMs":            "Avg Latency (ms)",
			"p50LatencyMs":            "P50 Latency (ms)",
			"p95LatencyMs":            "P95 Latency (ms)",
			"p99LatencyMs":            "P99 Latency (ms)",
			"maxLatencyMs":            "Max Latency (ms)",
			"errorRate":               "Error Rate",
		}

		throughputMetrics := map[string]bool{
			"avgThroughputRecPerSec":  true,
			"peakThroughputRecPerSec": true,
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "  METRIC\tBASELINE\tCURRENT\tDELTA")
		for _, key := range metricOrder {
			d, ok := report.Deltas[key]
			if !ok {
				continue
			}
			label := metricLabels[key]
			deltaStr := "-"
			if d.Delta != nil {
				pct := *d.Delta
				arrow := "▲"
				style := regressionGreen
				if throughputMetrics[key] {
					if pct < 0 {
						arrow = "▼"
						style = regressionRed
					}
				} else {
					if pct > 0 {
						arrow = "▲"
						style = regressionRed
					} else {
						arrow = "▼"
					}
				}
				deltaStr = style.Render(fmt.Sprintf("%s %.1f%%", arrow, pct))
			}
			fmt.Fprintf(w, "  %s\t%.2f\t%.2f\t%s\n", label, d.Baseline, d.Current, deltaStr)
		}
		w.Flush()
		fmt.Println()

		if report.RegressionDetected {
			fmt.Println(regressionRed.Bold(true).Render("  ⚠ REGRESSION DETECTED"))
			for _, warn := range report.Warnings {
				fmt.Printf("    • %s\n", warn)
			}
		} else {
			fmt.Println(regressionGreen.Bold(true).Render("  ✓ No regression detected"))
		}
		fmt.Println()

		return nil
	},
}

func init() {
	testBaselineCmd.AddCommand(baselineSetCmd)
	testBaselineCmd.AddCommand(baselineUnsetCmd)
	testBaselineCmd.AddCommand(baselineShowCmd)
	testBaselineCmd.AddCommand(baselineListCmd)
	testCmd.AddCommand(testBaselineCmd)
	reportCmd.AddCommand(reportRegressionCmd)
}
