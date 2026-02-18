package cmd

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/klster/kates-cli/client"
	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var testCompareCmd = &cobra.Command{
	Use:     "compare <id1> <id2>",
	Aliases: []string{"diff", "cmp"},
	Short:   "Side-by-side comparison of two test runs",
	Example: "  kates test compare abc123 def456",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		a, err := apiClient.GetTest(ctx, args[0])
		if err != nil {
			return cmdErr("Test A not found: " + err.Error())
		}
		b, err := apiClient.GetTest(ctx, args[1])
		if err != nil {
			return cmdErr("Test B not found: " + err.Error())
		}

		output.Banner("Test Comparison", truncID(a.ID)+" vs "+truncID(b.ID))
		fmt.Println()

		output.SubHeader("Overview")
		overviewRows := [][]string{
			{"Type", a.TestType, b.TestType, ""},
			{"Status", a.Status, b.Status, ""},
			{"Backend", a.Backend, b.Backend, ""},
			{"Created", formatTime(a.CreatedAt), formatTime(b.CreatedAt), ""},
		}
		output.Table([]string{"", truncID(a.ID), truncID(b.ID), "Delta"}, overviewRows)

		aMetrics := aggregatePhaseMetrics(a.Results)
		bMetrics := aggregatePhaseMetrics(b.Results)

		output.SubHeader("Performance Delta")
		metricRows := [][]string{
			cmpMetricRow("Records", aMetrics.records, bMetrics.records, false),
			cmpMetricRow("Throughput (rec/s)", aMetrics.throughput, bMetrics.throughput, true),
			cmpMetricRow("Throughput (MB/s)", aMetrics.throughputMB, bMetrics.throughputMB, true),
			cmpMetricRow("Avg Latency (ms)", aMetrics.avgLat, bMetrics.avgLat, false),
			cmpMetricRow("P99 Latency (ms)", aMetrics.p99Lat, bMetrics.p99Lat, false),
		}
		output.Table([]string{"Metric", truncID(a.ID), truncID(b.ID), "Delta"}, metricRows)

		return nil
	},
}

type phaseAgg struct {
	records      float64
	throughput   float64
	throughputMB float64
	avgLat       float64
	p99Lat       float64
}

func aggregatePhaseMetrics(results []client.PhaseResult) phaseAgg {
	var m phaseAgg
	count := 0
	for _, r := range results {
		m.records += r.RecordsSent
		if r.ThroughputRecordsPerSec > m.throughput {
			m.throughput = r.ThroughputRecordsPerSec
		}
		if r.ThroughputMBPerSec > m.throughputMB {
			m.throughputMB = r.ThroughputMBPerSec
		}
		if r.AvgLatencyMs > 0 {
			m.avgLat += r.AvgLatencyMs
			count++
		}
		if r.P99LatencyMs > m.p99Lat {
			m.p99Lat = r.P99LatencyMs
		}
	}
	if count > 0 {
		m.avgLat /= float64(count)
	}
	return m
}

func cmpMetricRow(name string, a, b float64, higherBetter bool) []string {
	return []string{name, fmtMetricCompact(a), fmtMetricCompact(b), deltaString(a, b, higherBetter)}
}

func fmtMetricCompact(v float64) string {
	if v == 0 {
		return "—"
	}
	if v >= 1_000_000 {
		return fmt.Sprintf("%.1fM", v/1_000_000)
	}
	if v >= 1_000 {
		return fmt.Sprintf("%.1fK", v/1_000)
	}
	return fmt.Sprintf("%.2f", v)
}

func deltaString(a, b float64, higherBetter bool) string {
	if a == 0 || b == 0 {
		return "—"
	}
	pct := ((b - a) / a) * 100
	arrow := "→"
	if pct > 1 {
		if higherBetter {
			arrow = output.SuccessStyle.Render("▲")
		} else {
			arrow = output.ErrorStyle.Render("▲")
		}
	} else if pct < -1 {
		if higherBetter {
			arrow = output.ErrorStyle.Render("▼")
		} else {
			arrow = output.SuccessStyle.Render("▼")
		}
	}
	return fmt.Sprintf("%s %+.1f%%", arrow, pct)
}

var testSummaryCmd = &cobra.Command{
	Use:     "summary",
	Aliases: []string{"stats"},
	Short:   "Aggregate statistics across all completed tests",
	Example: "  kates test summary",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		paged, err := apiClient.ListTests(ctx, "", "DONE", 0, 100)
		if err != nil {
			return cmdErr("Failed to list tests: " + err.Error())
		}

		if len(paged.Content) == 0 {
			output.Hint("No completed tests found.")
			return nil
		}

		output.Banner("Test Summary", fmt.Sprintf("%d completed tests", len(paged.Content)))
		fmt.Println()

		totalRecords := 0.0
		bestThroughput := 0.0
		worstThroughput := math.MaxFloat64
		sumThroughput := 0.0
		sumP99 := 0.0
		throughputCount := 0
		p99Count := 0
		typeCounts := map[string]int{}

		for _, run := range paged.Content {
			typeCounts[run.TestType]++
			for _, r := range run.Results {
				totalRecords += r.RecordsSent
				if r.ThroughputRecordsPerSec > 0 {
					sumThroughput += r.ThroughputRecordsPerSec
					throughputCount++
					if r.ThroughputRecordsPerSec > bestThroughput {
						bestThroughput = r.ThroughputRecordsPerSec
					}
					if r.ThroughputRecordsPerSec < worstThroughput {
						worstThroughput = r.ThroughputRecordsPerSec
					}
				}
				if r.P99LatencyMs > 0 {
					sumP99 += r.P99LatencyMs
					p99Count++
				}
			}
		}

		if worstThroughput == math.MaxFloat64 {
			worstThroughput = 0
		}

		output.SubHeader("Aggregate Metrics")
		output.KeyValue("Total Records", fmtNum(totalRecords))
		if throughputCount > 0 {
			output.KeyValue("Avg Throughput", fmtNum(sumThroughput/float64(throughputCount))+" rec/s")
			output.KeyValue("Best Throughput", fmtNum(bestThroughput)+" rec/s")
			output.KeyValue("Worst Throughput", fmtNum(worstThroughput)+" rec/s")
		}
		if p99Count > 0 {
			output.KeyValue("Avg P99 Latency", fmtFloat(sumP99/float64(p99Count), 2)+" ms")
		}

		failedPaged, _ := apiClient.ListTests(ctx, "", "FAILED", 0, 100)
		failedCount := 0
		if failedPaged != nil {
			failedCount = len(failedPaged.Content)
		}
		totalTests := len(paged.Content) + failedCount
		successRate := float64(len(paged.Content)) / float64(totalTests) * 100
		output.KeyValue("Success Rate", fmt.Sprintf("%.1f%% (%d/%d)", successRate, len(paged.Content), totalTests))

		fmt.Println()
		output.SubHeader("Tests by Type")
		typeRows := make([][]string, 0, len(typeCounts))
		for t, c := range typeCounts {
			typeRows = append(typeRows, []string{t, fmt.Sprintf("%d", c)})
		}
		output.Table([]string{"Type", "Count"}, typeRows)

		return nil
	},
}

var testFlameCmd = &cobra.Command{
	Use:     "flame <id>",
	Aliases: []string{"latency", "hist"},
	Short:   "ASCII latency distribution chart for a test run",
	Example: "  kates test flame 69acdf31",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		run, err := apiClient.GetTest(ctx, args[0])
		if err != nil {
			return cmdErr("Test not found: " + err.Error())
		}

		output.Banner("Latency Distribution", run.TestType+" · "+truncID(run.ID))
		fmt.Println()

		for _, r := range run.Results {
			phase := r.PhaseName
			if phase == "" {
				phase = "main"
			}
			if r.AvgLatencyMs == 0 && r.P99LatencyMs == 0 {
				continue
			}

			output.SubHeader(fmt.Sprintf("Phase: %s [%s]", phase, r.Status))

			maxVal := r.MaxLatencyMs
			if maxVal == 0 {
				maxVal = r.P99LatencyMs * 1.5
			}
			if maxVal == 0 {
				maxVal = 1
			}

			type bucket struct {
				name  string
				value float64
			}

			buckets := []bucket{
				{"Avg ", r.AvgLatencyMs},
				{"P50 ", r.P50LatencyMs},
				{"P95 ", r.P95LatencyMs},
				{"P99 ", r.P99LatencyMs},
				{"Max ", r.MaxLatencyMs},
			}

			barWidth := 40

			for _, b := range buckets {
				if b.value == 0 {
					continue
				}
				filled := int(math.Round(b.value / maxVal * float64(barWidth)))
				if filled < 1 {
					filled = 1
				}
				if filled > barWidth {
					filled = barWidth
				}

				bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)

				var coloredBar string
				ratio := b.value / maxVal
				switch {
				case ratio < 0.5:
					coloredBar = output.SuccessStyle.Render(bar)
				case ratio < 0.8:
					coloredBar = output.WarningStyle.Render(bar)
				default:
					coloredBar = output.ErrorStyle.Render(bar)
				}

				label := output.AccentStyle.Render(b.name)
				val := fmt.Sprintf(" %.3f ms", b.value)
				fmt.Printf("  %s %s%s\n", label, coloredBar, val)
			}
			fmt.Println()

			if r.ThroughputRecordsPerSec > 0 {
				output.KeyValue("Throughput", fmtNum(r.ThroughputRecordsPerSec)+" rec/s")
			}
			if r.RecordsSent > 0 {
				output.KeyValue("Records", fmtNum(r.RecordsSent))
			}
			fmt.Println()
		}
		return nil
	},
}

func init() {
	testCmd.AddCommand(testCompareCmd)
	testCmd.AddCommand(testSummaryCmd)
	testCmd.AddCommand(testFlameCmd)
}
