package cmd

import (
	"context"
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/klster/kates-cli/client"
	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

type advisorRule struct {
	Severity string
	Title    string
	Detail   string
	Fix      string
	Evidence string
}

var (
	advisorApply bool

	advTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#7C3AED")).
			Padding(0, 1)

	advHighStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#EF4444"))

	advMedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#F59E0B"))

	advOkStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#22C55E"))

	advDimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6B7280"))
)

var advisorCmd = &cobra.Command{
	Use:   "advisor <run-id>",
	Short: "Analyze test results and recommend configuration improvements",
	Long: `Runs a rule engine against a completed test run's results and
cluster topology to generate actionable tuning recommendations.

Rules cover batching, compression, acks, partitions, replication,
linger timing, record sizing, and consumer/producer balance.`,
	Example: `  kates advisor abc123
  kates advisor abc123 --apply
  kates advisor abc123 -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]

		run, err := apiClient.GetTest(context.Background(), id)
		if err != nil {
			return err
		}

		report, err := apiClient.Report(context.Background(), id)
		if err != nil {
			return err
		}

		rules := analyzeRun(run, report)

		fmt.Println(advTitleStyle.Width(60).Render(
			fmt.Sprintf("  Configuration Advisor  ·  Run %s", truncAdvisorID(id)),
		))
		fmt.Println()

		if len(rules) == 0 {
			fmt.Println(advOkStyle.Render("  ✓ No recommendations — configuration looks optimal"))
			return nil
		}

		for _, r := range rules {
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

		return nil
	},
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
