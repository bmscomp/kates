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

		if s := result.Summary; s != nil {
			output.SubHeader("Throughput")
			output.KeyValue("Total Records", fmtNum(s.TotalRecords))
			output.KeyValue("Avg Throughput", fmt.Sprintf("%s rec/s", fmtNum(s.AvgThroughputRecPerSec)))
			output.KeyValue("Peak Throughput", fmt.Sprintf("%s rec/s", fmtNum(s.PeakThroughputRecPerSec)))
			output.KeyValue("Avg MB/s", fmtFloat(s.AvgThroughputMBPerSec, 2))

			output.SubHeader("Latency Distribution")
			maxLat := s.MaxLatencyMs
			if maxLat == 0 {
				maxLat = 1
			}
			output.MetricBar("Average", s.AvgLatencyMs, maxLat)
			output.MetricBar("P50", s.P50LatencyMs, maxLat)
			output.MetricBar("P95", s.P95LatencyMs, maxLat)
			output.MetricBar("P99", s.P99LatencyMs, maxLat)
			output.MetricBar("Max", s.MaxLatencyMs, maxLat)

			output.SubHeader("Reliability")
			output.KeyValue("Error Rate", fmt.Sprintf("%.4f%%", s.ErrorRate*100))
		}

		if v := result.OverallSlaVerdict; v != nil {
			output.SubHeader("SLA Verdict")
			if v.Passed {
				output.Success("All SLA thresholds met")
			} else {
				output.Warn("SLA violations detected")
				if len(v.Violations) > 0 {
					rows := make([][]string, 0, len(v.Violations))
					for _, viol := range v.Violations {
						rows = append(rows, []string{
							viol.Metric,
							fmtFloat(viol.Threshold, 2),
							fmtFloat(viol.Actual, 2),
							"FAIL",
						})
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
			{"Total Records", fmtNum(result.TotalRecords)},
			{"Avg Throughput", fmt.Sprintf("%s rec/s", fmtNum(result.AvgThroughputRecPerSec))},
			{"Peak Throughput", fmt.Sprintf("%s rec/s", fmtNum(result.PeakThroughputRecPerSec))},
			{"Avg MB/s", fmtFloat(result.AvgThroughputMBPerSec, 2)},
			{"Avg Latency", fmt.Sprintf("%s ms", fmtFloat(result.AvgLatencyMs, 2))},
			{"P50 Latency", fmt.Sprintf("%s ms", fmtFloat(result.P50LatencyMs, 2))},
			{"P95 Latency", fmt.Sprintf("%s ms", fmtFloat(result.P95LatencyMs, 2))},
			{"P99 Latency", fmt.Sprintf("%s ms", fmtFloat(result.P99LatencyMs, 2))},
			{"Max Latency", fmt.Sprintf("%s ms", fmtFloat(result.MaxLatencyMs, 2))},
			{"Error Rate", fmt.Sprintf("%.4f%%", result.ErrorRate*100)},
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
	Short: "Export report as CSV, JUnit XML, Heatmap, Markdown, or HTML",
	Args:  cobra.ExactArgs(1),
	Example: `  kates report export abc123 --format csv
  kates report export abc123 --format junit
  kates report export abc123 --format md
  kates report export abc123 --format html
  kates report export abc123 --format heatmap
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

		case "heatmap":
			heatmap, err := apiClient.ExportHeatmap(context.Background(), id, "json")
			if err != nil {
				return cmdErr("Export failed: " + err.Error())
			}
			if isTerminal() {
				file := "kates-heatmap-" + id + ".json"
				if err := os.WriteFile(file, []byte(heatmap), 0644); err != nil {
					return cmdErr("Write failed: " + err.Error())
				}
				output.Success("Exported to " + file)
			} else {
				fmt.Print(heatmap)
			}

		case "heatmap-csv":
			heatmapCsv, err := apiClient.ExportHeatmap(context.Background(), id, "csv")
			if err != nil {
				return cmdErr("Export failed: " + err.Error())
			}
			if isTerminal() {
				file := "kates-heatmap-" + id + ".csv"
				if err := os.WriteFile(file, []byte(heatmapCsv), 0644); err != nil {
					return cmdErr("Write failed: " + err.Error())
				}
				output.Success("Exported to " + file)
			} else {
				fmt.Print(heatmapCsv)
			}

		case "md":
			report, err := apiClient.Report(context.Background(), id)
			if err != nil {
				return cmdErr("Export failed: " + err.Error())
			}
			md := renderMarkdownReport(id, report)
			if isTerminal() {
				file := "kates-report-" + id + ".md"
				if err := os.WriteFile(file, []byte(md), 0644); err != nil {
					return cmdErr("Write failed: " + err.Error())
				}
				output.Success("Exported to " + file)
			} else {
				fmt.Print(md)
			}

		case "html":
			report, err := apiClient.Report(context.Background(), id)
			if err != nil {
				return cmdErr("Export failed: " + err.Error())
			}
			html := renderHTMLReport(id, report)
			file := "kates-report-" + id + ".html"
			if err := os.WriteFile(file, []byte(html), 0644); err != nil {
				return cmdErr("Write failed: " + err.Error())
			}
			output.Success("Exported to " + file)
			output.Hint("  Open: open " + file)

		default:
			return cmdErr("Unknown format '" + exportFormat + "'. Use 'csv', 'junit', 'heatmap', 'heatmap-csv', 'md', or 'html'.")
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
	reportExportCmd.Flags().StringVar(&exportFormat, "format", "csv", "Export format: csv, junit, heatmap, or heatmap-csv")

	reportCmd.AddCommand(reportShowCmd)
	reportCmd.AddCommand(reportSummaryCmd)
	reportCmd.AddCommand(reportCompareCmd)
	reportCmd.AddCommand(reportExportCmd)
	rootCmd.AddCommand(reportCmd)
}
