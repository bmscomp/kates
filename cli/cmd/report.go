package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
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

		summary := result.Summary

		if summary == nil || summary.AvgThroughputRecPerSec == 0 {
			run, fetchErr := apiClient.GetTest(context.Background(), args[0])
			if fetchErr == nil && len(run.Results) > 0 && !isStaleResult(run.Results) {
				s := &client.ReportSummary{}
				count := 0
				for _, r := range run.Results {
					s.TotalRecords += r.RecordsSent
					if r.ThroughputRecordsPerSec > s.PeakThroughputRecPerSec {
						s.PeakThroughputRecPerSec = r.ThroughputRecordsPerSec
					}
					if r.ThroughputRecordsPerSec > 0 {
						s.AvgThroughputRecPerSec += r.ThroughputRecordsPerSec
						count++
					}
					if r.P99LatencyMs > s.P99LatencyMs {
						s.P99LatencyMs = r.P99LatencyMs
					}
					if r.AvgLatencyMs > s.AvgLatencyMs {
						s.AvgLatencyMs = r.AvgLatencyMs
					}
					if r.P50LatencyMs > s.P50LatencyMs {
						s.P50LatencyMs = r.P50LatencyMs
					}
					if r.P95LatencyMs > s.P95LatencyMs {
						s.P95LatencyMs = r.P95LatencyMs
					}
					if r.MaxLatencyMs > s.MaxLatencyMs {
						s.MaxLatencyMs = r.MaxLatencyMs
					}
				}
				if count > 0 {
					s.AvgThroughputRecPerSec /= float64(count)
				}
				summary = s
			}
		}

		if summary != nil && summary.AvgThroughputRecPerSec > 0 {
			output.SubHeader("Throughput")
			output.KeyValue("Total Records", fmtNum(summary.TotalRecords))
			output.KeyValue("Avg Throughput", fmt.Sprintf("%s rec/s", fmtNum(summary.AvgThroughputRecPerSec)))
			output.KeyValue("Peak Throughput", fmt.Sprintf("%s rec/s", fmtNum(summary.PeakThroughputRecPerSec)))
			output.KeyValue("Avg MB/s", fmtFloat(summary.AvgThroughputMBPerSec, 2))

			output.SubHeader("Latency Distribution")
			maxLat := summary.MaxLatencyMs
			if maxLat == 0 {
				maxLat = 1
			}
			output.MetricBar("Average", summary.AvgLatencyMs, maxLat)
			output.MetricBar("P50", summary.P50LatencyMs, maxLat)
			output.MetricBar("P95", summary.P95LatencyMs, maxLat)
			output.MetricBar("P99", summary.P99LatencyMs, maxLat)
			output.MetricBar("Max", summary.MaxLatencyMs, maxLat)

			output.SubHeader("Reliability")
			output.KeyValue("Error Rate", fmt.Sprintf("%.4f%%", summary.ErrorRate*100))
		} else {
			output.SubHeader("Diagnosis")
			output.Warn("Report has no performance data")
			output.Hint("  The backend may not have collected metrics for this test.")
			output.Hint("  Check logs: kubectl logs -n kates -l app=kates --tail=50")
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
						threshold, actual := violationCells(viol)
						rows = append(rows, []string{viol.Metric, threshold, actual, "FAIL"})
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

var (
	exportFormat string
	exportFile   string
)

var reportExportCmd = &cobra.Command{
	Use:   "export <id>",
	Short: "Export report as csv, junit, heatmap, heatmap-csv, md, or html",
	Long: `Export a run's report in one of six formats.

With -f the export goes to that file. Without it, in a terminal it goes to a
file named after the run (kates-report-<id>.csv, kates-heatmap-<id>.json, …)
in the current directory; piped, redirected or with --plain it goes to stdout,
for every format including html.`,
	Args: cobra.ExactArgs(1),
	Example: `  kates report export abc123 --format csv
  kates report export abc123 --format junit -f results.xml
  kates report export abc123 --format md
  kates report export abc123 --format html
  kates report export abc123 --format heatmap
  kates report export abc123 --format csv > report.csv`,
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		ctx := context.Background()

		var body, defaultFile string
		var err error
		switch exportFormat {
		case "csv":
			body, err = apiClient.ExportCSV(ctx, id)
			defaultFile = "kates-report-" + id + ".csv"
		case "junit":
			body, err = apiClient.ExportJUnit(ctx, id)
			defaultFile = "kates-report-" + id + ".xml"
		case "heatmap":
			body, err = apiClient.ExportHeatmap(ctx, id, "json")
			defaultFile = "kates-heatmap-" + id + ".json"
		case "heatmap-csv":
			body, err = apiClient.ExportHeatmap(ctx, id, "csv")
			defaultFile = "kates-heatmap-" + id + ".csv"
		case "md", "html":
			var report *client.Report
			report, err = apiClient.Report(ctx, id)
			if err == nil && exportFormat == "md" {
				body, defaultFile = renderMarkdownReport(id, report), "kates-report-"+id+".md"
			} else if err == nil {
				body, defaultFile = renderHTMLReport(id, report), "kates-report-"+id+".html"
			}
		default:
			return cmdErr("Unknown format '" + exportFormat + "'. Use 'csv', 'junit', 'heatmap', 'heatmap-csv', 'md', or 'html'.")
		}
		if err != nil {
			return cmdErr("Export failed: " + err.Error())
		}
		return writeReportExport(exportFile, defaultFile, []byte(body))
	},
}

// writeReportExport writes an export where report export puts it: to file
// when -f named one ("-" for stdout); otherwise to stdout when no one is
// watching a terminal, which is what a pipe or a CI step reads, and to
// defaultFile in the current directory when someone is. The html format used
// to go to the file even when piped, so `kates report export x --format html
// > r.html` left an empty r.html and a file in the working directory.
func writeReportExport(file, defaultFile string, data []byte) error {
	switch {
	case file == "-":
		fmt.Print(string(data))
		return nil
	case file == "" && !IsInteractive():
		fmt.Print(string(data))
		return nil
	case file == "":
		file = defaultFile
	}
	if err := writeExportFile(file, data); err != nil {
		return cmdErr("Write failed: " + err.Error())
	}
	output.Success("Exported to " + file)
	if strings.HasSuffix(file, ".html") {
		output.Hint("  Open: open " + file)
	}
	return nil
}

// violationCells gives a report violation's threshold and actual value as the
// report tables print them. A reason stands in for an actual value the run
// did not measure, or that a FAILED run's status cannot have, and a status has
// no threshold either: the backend sends -1 for each missing number. A reason
// can quote a task's error, so it is made printable.
func violationCells(v client.SlaViolation) (threshold, actual string) {
	threshold, actual = "—", output.Printable(v.Reason)
	if v.Threshold >= 0 {
		threshold = fmtFloat(v.Threshold, 2)
	}
	if v.Reason == "" {
		actual = fmtFloat(v.Actual, 2)
	}
	return threshold, actual
}

func init() {
	reportExportCmd.Flags().StringVar(&exportFormat, "format", "csv", "Export format: csv, junit, heatmap, heatmap-csv, md, or html")
	addOutputFileFlag(reportExportCmd, &exportFile)

	reportCmd.AddCommand(reportShowCmd)
	reportCmd.AddCommand(reportSummaryCmd)
	reportCmd.AddCommand(reportCompareCmd)
	reportCmd.AddCommand(reportExportCmd)
	rootCmd.AddCommand(reportCmd)
}
