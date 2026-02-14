package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var reportCmd = &cobra.Command{
	Use:     "report",
	Aliases: []string{"r"},
	Short:   "View and export test reports",
}

var reportShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show the full report for a test run",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.Report(context.Background(), args[0])
		if err != nil {
			return cmdErr("Failed to get report: " + err.Error())
		}
		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		output.Banner("Performance Report", "Test: "+truncID(args[0]))

		if summary, ok := result["summary"].(map[string]interface{}); ok {
			output.SubHeader("Throughput")
			output.KeyValue("Total Records", fmtNum(numVal(summary, "totalRecords")))
			output.KeyValue("Avg Throughput", fmt.Sprintf("%s rec/s", fmtNum(numVal(summary, "avgThroughputRecPerSec"))))
			output.KeyValue("Peak Throughput", fmt.Sprintf("%s rec/s", fmtNum(numVal(summary, "peakThroughputRecPerSec"))))
			output.KeyValue("Avg MB/s", fmtFloat(numVal(summary, "avgThroughputMBPerSec"), 2))

			output.SubHeader("Latency Distribution")
			maxLat := numVal(summary, "maxLatencyMs")
			if maxLat == 0 {
				maxLat = 1
			}
			output.MetricBar("Average", numVal(summary, "avgLatencyMs"), maxLat)
			output.MetricBar("P50", numVal(summary, "p50LatencyMs"), maxLat)
			output.MetricBar("P95", numVal(summary, "p95LatencyMs"), maxLat)
			output.MetricBar("P99", numVal(summary, "p99LatencyMs"), maxLat)
			output.MetricBar("Max", numVal(summary, "maxLatencyMs"), maxLat)

			errRate := numVal(summary, "errorRate") * 100
			output.SubHeader("Reliability")
			output.KeyValue("Error Rate", fmt.Sprintf("%.4f%%", errRate))
		}

		// SLA
		if verdict, ok := result["overallSlaVerdict"].(map[string]interface{}); ok {
			output.SubHeader("SLA Verdict")
			passed := false
			if p, ok := verdict["passed"].(bool); ok {
				passed = p
			}
			if passed {
				output.Success("All SLA thresholds met")
			} else {
				output.Warn("SLA violations detected")
				if violations, ok := verdict["violations"].([]interface{}); ok {
					rows := make([][]string, 0, len(violations))
					for _, v := range violations {
						if vm, ok := v.(map[string]interface{}); ok {
							rows = append(rows, []string{
								mapStr(vm, "metric"),
								fmtFloat(numVal(vm, "threshold"), 2),
								fmtFloat(numVal(vm, "actual"), 2),
								"FAIL",
							})
						}
					}
					output.Table([]string{"Metric", "Threshold", "Actual", "Status"}, rows)
				}
			}
		}

		output.Hint(fmt.Sprintf("Export: kates report export %s --format csv", args[0]))
		return nil
	},
}

var reportSummaryCmd = &cobra.Command{
	Use:   "summary <id>",
	Short: "Show compact summary metrics for a test run",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.ReportSummary(context.Background(), args[0])
		if err != nil {
			return cmdErr("Failed to get summary: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		output.Header("Summary: " + truncID(args[0]))
		rows := [][]string{
			{"Total Records", fmtNum(numVal(result, "totalRecords"))},
			{"Avg Throughput", fmt.Sprintf("%s rec/s", fmtNum(numVal(result, "avgThroughputRecPerSec")))},
			{"Peak Throughput", fmt.Sprintf("%s rec/s", fmtNum(numVal(result, "peakThroughputRecPerSec")))},
			{"Avg MB/s", fmtFloat(numVal(result, "avgThroughputMBPerSec"), 2)},
			{"Avg Latency", fmt.Sprintf("%s ms", fmtFloat(numVal(result, "avgLatencyMs"), 2))},
			{"P50 Latency", fmt.Sprintf("%s ms", fmtFloat(numVal(result, "p50LatencyMs"), 2))},
			{"P95 Latency", fmt.Sprintf("%s ms", fmtFloat(numVal(result, "p95LatencyMs"), 2))},
			{"P99 Latency", fmt.Sprintf("%s ms", fmtFloat(numVal(result, "p99LatencyMs"), 2))},
			{"Max Latency", fmt.Sprintf("%s ms", fmtFloat(numVal(result, "maxLatencyMs"), 2))},
			{"Error Rate", fmt.Sprintf("%.4f%%", numVal(result, "errorRate")*100)},
		}
		output.Table([]string{"Metric", "Value"}, rows)
		return nil
	},
}

var reportCompareCmd = &cobra.Command{
	Use:   "compare <id1,id2,...>",
	Short: "Compare metrics across multiple test runs",
	Args:  cobra.ExactArgs(1),
	Example: `  kates report compare abc123,def456
  kates report compare abc123,def456,ghi789 -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		data, err := apiClient.Compare(context.Background(), args[0])
		if err != nil {
			return cmdErr("Failed to compare: " + err.Error())
		}
		output.Header("Comparison")
		output.RawJSON(data)
		return nil
	},
}

var exportFormat string

var reportExportCmd = &cobra.Command{
	Use:   "export <id>",
	Short: "Export report as CSV or JUnit XML",
	Args:  cobra.ExactArgs(1),
	Example: `  kates report export abc123 --format csv
  kates report export abc123 --format junit
  kates report export abc123 --format csv > report.csv`,
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]

		switch exportFormat {
		case "csv":
			csv, err := apiClient.ExportCSV(context.Background(), id)
			if err != nil {
				return cmdErr("Export failed: " + err.Error())
			}
			if isTerminal() {
				file := "kates-report-" + id + ".csv"
				if err := os.WriteFile(file, []byte(csv), 0644); err != nil {
					return cmdErr("Write failed: " + err.Error())
				}
				output.Success("Exported to " + file)
			} else {
				fmt.Print(csv)
			}

		case "junit":
			xml, err := apiClient.ExportJUnit(context.Background(), id)
			if err != nil {
				return cmdErr("Export failed: " + err.Error())
			}
			if isTerminal() {
				file := "kates-report-" + id + ".xml"
				if err := os.WriteFile(file, []byte(xml), 0644); err != nil {
					return cmdErr("Write failed: " + err.Error())
				}
				output.Success("Exported to " + file)
			} else {
				fmt.Print(xml)
			}

		default:
			output.Error("Unknown format '" + exportFormat + "'. Use 'csv' or 'junit'.")
		}
		return nil
	},
}

func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return true
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func init() {
	reportExportCmd.Flags().StringVar(&exportFormat, "format", "csv", "Export format: csv or junit")

	reportCmd.AddCommand(reportShowCmd)
	reportCmd.AddCommand(reportSummaryCmd)
	reportCmd.AddCommand(reportCompareCmd)
	reportCmd.AddCommand(reportExportCmd)
	rootCmd.AddCommand(reportCmd)
}
