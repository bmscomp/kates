package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/theme"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

// advisorRule is one recommendation. Severity is HIGH, MED or OK.
type advisorRule struct {
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Detail   string `json:"detail,omitempty"`
	Fix      string `json:"fix,omitempty"`
	Evidence string `json:"evidence,omitempty"`
}

// advisorResult is the advisor's answer for one run, which the table prints
// and -o json prints as is.
//
// A run or report that cannot be found is an answer, not an error: the
// command has always exited 0 for it, and scripts may rely on that. Status
// tells the cases apart, so a JSON reader does not take a missing report for
// a clean bill.
type advisorResult struct {
	RunID string `json:"runId"`
	// Status is ANALYZED, RUN_NOT_FOUND or REPORT_NOT_READY.
	Status          string        `json:"status"`
	Message         string        `json:"message,omitempty"`
	Recommendations []advisorRule `json:"recommendations"`
}

const (
	advisorAnalyzed       = "ANALYZED"
	advisorRunNotFound    = "RUN_NOT_FOUND"
	advisorReportNotReady = "REPORT_NOT_READY"
)

var (
	advisorApply bool

	// advTitleStyle renders the "Configuration Advisor" banner.
	advTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.OnColor).
			Background(theme.Secondary).
			Padding(0, 1)

	advHighStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.Error)

	advMedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.Warning)

	advOkStyle = lipgloss.NewStyle().
			Foreground(theme.Success)

	advDimStyle = lipgloss.NewStyle().
			Foreground(theme.Muted)
)

var advisorCmd = &cobra.Command{
	Use:   "advisor <run-id>",
	Short: "Analyze test results and recommend configuration improvements",
	Long: `Runs a rule engine against a completed test run's results and
cluster topology to generate actionable tuning recommendations.

Rules cover batching, compression, acks, partitions, replication,
linger timing, record sizing, and consumer/producer balance.

With -o json it prints the recommendations as JSON, with a status of
ANALYZED, RUN_NOT_FOUND or REPORT_NOT_READY. A run or report that is not
found exits 0, as it does with the table.`,
	Example: `  kates advisor abc123
  kates advisor abc123 --apply
  kates advisor abc123 -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		res, err := adviseRun(context.Background(), args[0])
		if err != nil {
			return err
		}
		output.Render(outputMode == "json", res, func() { renderAdvisor(res) })
		return nil
	},
}

// adviseRun fetches a run and its report and runs the rules over them.
func adviseRun(ctx context.Context, id string) (advisorResult, error) {
	res := advisorResult{RunID: id, Recommendations: []advisorRule{}}

	run, err := apiClient.GetTest(ctx, id)
	if err != nil {
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "Not Found") {
			res.Status = advisorRunNotFound
			res.Message = "Test run not found: " + id
			return res, nil
		}
		return res, fmt.Errorf("failed to fetch test run: %w", err)
	}

	report, err := apiClient.Report(ctx, id)
	if err != nil {
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") {
			res.Status = advisorReportNotReady
			res.Message = "Report not available yet for run: " + truncAdvisorID(id)
			return res, nil
		}
		return res, fmt.Errorf("failed to fetch report: %w", err)
	}

	res.Status = advisorAnalyzed
	res.Recommendations = append(res.Recommendations, analyzeRun(run, report)...)
	return res, nil
}

func renderAdvisor(res advisorResult) {
	switch res.Status {
	case advisorRunNotFound:
		fmt.Println()
		fmt.Println(advHighStyle.Render("  ✖ " + res.Message))
		fmt.Println()
		fmt.Println(advDimStyle.Render("  Suggestions:"))
		fmt.Println(advDimStyle.Render("    • Verify the run ID — use 'kates test list' to see available runs"))
		fmt.Println(advDimStyle.Render("    • Run IDs are UUID format, e.g. 3fa85f64-5717-4562-b3fc-2c963f66afa6"))
		fmt.Println()
		return
	case advisorReportNotReady:
		fmt.Println()
		fmt.Println(advMedStyle.Render("  ⏳ " + res.Message))
		fmt.Println(advDimStyle.Render("    The test may still be running. Wait for completion and try again."))
		fmt.Println()
		return
	}

	fmt.Println(advTitleStyle.Width(60).Render(
		fmt.Sprintf("  Configuration Advisor  ·  Run %s", truncAdvisorID(res.RunID)),
	))
	fmt.Println()

	if len(res.Recommendations) == 0 {
		fmt.Println(advOkStyle.Render("  ✓ No recommendations — configuration looks optimal"))
		return
	}

	for _, r := range res.Recommendations {
		var badge string
		switch r.Severity {
		case "HIGH":
			badge = advHighStyle.Render("⚡ HIGH")
		case "MED":
			badge = advMedStyle.Render("📊 MED ")
		case "OK":
			badge = advOkStyle.Render("✓  OK  ")
		}

		fmt.Printf("  %s  %s\n", badge, r.Title)
		if r.Fix != "" {
			fmt.Printf("           → %s\n", advOkStyle.Render(r.Fix))
		}
		if r.Evidence != "" {
			fmt.Printf("           %s\n", advDimStyle.Render("Evidence: "+r.Evidence))
		}
		fmt.Println()
	}

	if advisorApply {
		output.Hint("Use the recommendations above to update your scenario YAML")
	}
}

func analyzeRun(run *client.TestRun, report *client.Report) []advisorRule {
	var rules []advisorRule

	if run.Spec == nil || len(run.Results) == 0 {
		return rules
	}
	spec := run.Spec

	var avgThroughput, avgP99 float64
	for _, r := range run.Results {
		avgThroughput += r.ThroughputRecordsPerSec
		avgP99 += r.P99LatencyMs
	}
	avgThroughput /= float64(len(run.Results))
	avgP99 /= float64(len(run.Results))

	if spec.BatchSize > 0 && spec.BatchSize <= 16384 && avgThroughput > 10000 {
		rules = append(rules, advisorRule{
			Severity: "HIGH",
			Title:    fmt.Sprintf("batch.size=%d is leaving throughput on the table", spec.BatchSize),
			Fix:      "Try batch.size=65536 for improved batching efficiency",
			Evidence: fmt.Sprintf("current throughput: %s rec/s, estimated gain: ~30-50%%", fmtAdvisorNum(avgThroughput)),
		})
	}

	if spec.LingerMs == 0 && avgThroughput > 5000 {
		rules = append(rules, advisorRule{
			Severity: "HIGH",
			Title:    "linger.ms=0 causes excessive small-batch sends",
			Fix:      "Set linger.ms=10 to coalesce batches and reduce request count",
			Evidence: "zero linger forces immediate sends, increasing network overhead",
		})
	}

	if (spec.Acks == "1" || spec.Acks == "0") && spec.ReplicationFactor >= 3 {
		severity := "MED"
		if spec.Acks == "0" {
			severity = "HIGH"
		}
		rules = append(rules, advisorRule{
			Severity: severity,
			Title:    fmt.Sprintf("acks=%s with replicationFactor=%d risks data loss", spec.Acks, spec.ReplicationFactor),
			Fix:      "Use acks=all for data durability with high replication",
			Evidence: fmt.Sprintf("replication=%d provides redundancy, but acks=%s bypasses it", spec.ReplicationFactor, spec.Acks),
		})
	}

	if spec.CompressionType == "none" || spec.CompressionType == "" {
		if avgThroughput > 20000 {
			rules = append(rules, advisorRule{
				Severity: "HIGH",
				Title:    "No compression detected — high bandwidth usage",
				Fix:      "Use compression=lz4 for best throughput or zstd for best ratio",
				Evidence: fmt.Sprintf("%s rec/s uncompressed wastes ~30-60%% network bandwidth", fmtAdvisorNum(avgThroughput)),
			})
		} else {
			rules = append(rules, advisorRule{
				Severity: "MED",
				Title:    "No compression — consider enabling for network efficiency",
				Fix:      "compression=lz4 adds negligible CPU overhead",
			})
		}
	} else if spec.CompressionType == "gzip" {
		rules = append(rules, advisorRule{
			Severity: "MED",
			Title:    "gzip compression has highest CPU overhead",
			Fix:      "Switch to lz4 for 3-5x faster compression at similar ratios",
			Evidence: "gzip is best for cold storage, lz4/zstd for streaming",
		})
	} else {
		rules = append(rules, advisorRule{
			Severity: "OK",
			Title:    fmt.Sprintf("compression=%s is optimal for this workload", spec.CompressionType),
		})
	}

	if spec.Partitions > 0 && spec.ParallelProducers > 0 {
		ratio := float64(spec.Partitions) / float64(spec.ParallelProducers)
		if ratio < 2 {
			rules = append(rules, advisorRule{
				Severity: "MED",
				Title:    fmt.Sprintf("partitions=%d with %d producers limits parallelism", spec.Partitions, spec.ParallelProducers),
				Fix:      fmt.Sprintf("Increase partitions to %d (4× producers) for better distribution", spec.ParallelProducers*4),
				Evidence: fmt.Sprintf("partition:producer ratio is %.1f (recommended: ≥ 4)", ratio),
			})
		} else {
			rules = append(rules, advisorRule{
				Severity: "OK",
				Title:    fmt.Sprintf("partitions=%d matches producer count well", spec.Partitions),
			})
		}
	}

	if spec.RecordSizeBytes > 0 && spec.RecordSizeBytes < 256 && avgThroughput > 50000 {
		rules = append(rules, advisorRule{
			Severity: "MED",
			Title:    fmt.Sprintf("recordSize=%dB is small — high per-record overhead", spec.RecordSizeBytes),
			Fix:      "Batch application records or increase record size to reduce overhead",
			Evidence: "small records amplify per-message metadata costs",
		})
	}

	if avgP99 > 100 && spec.BatchSize > 65536 {
		rules = append(rules, advisorRule{
			Severity: "MED",
			Title:    fmt.Sprintf("p99=%.0fms with large batch.size=%d — try reducing", avgP99, spec.BatchSize),
			Fix:      "Reduce batch.size or linger.ms to trade throughput for latency",
			Evidence: "large batches increase fill time, raising tail latency",
		})
	}

	return rules
}

func truncAdvisorID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func fmtAdvisorNum(v float64) string {
	if v >= 1_000_000 {
		return fmt.Sprintf("%.1fM", v/1_000_000)
	}
	if v >= 1_000 {
		return fmt.Sprintf("%.1fK", v/1_000)
	}
	return fmt.Sprintf("%.0f", v)
}

func init() {
	advisorCmd.Flags().BoolVar(&advisorApply, "apply", false, "Generate a tuned scenario YAML from recommendations")
	rootCmd.AddCommand(advisorCmd)
	registerAnalysisCompletions()
}

func analyzeResults(results []client.PhaseResult) (avgThroughput, avgP99 float64) {
	if len(results) == 0 {
		return
	}
	for _, r := range results {
		avgThroughput += r.ThroughputRecordsPerSec
		avgP99 += r.P99LatencyMs
	}
	avgThroughput /= float64(len(results))
	avgP99 /= float64(len(results))
	return
}
