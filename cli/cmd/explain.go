package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var explainCmd = &cobra.Command{
	Use:     "explain <id>",
	Aliases: []string{"why", "interpret"},
	Short:   "Plain-English summary and verdict for a test run",
	Example: "  kates explain 69acdf31",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		run, err := apiClient.GetTest(ctx, args[0])
		if err != nil {
			return cmdErr("Test not found: " + err.Error())
		}

		output.Banner("Test Explanation", run.TestType+" · "+truncID(run.ID))
		fmt.Println()

		desc := describeType(run.TestType)
		if desc != "" {
			output.Hint(desc)
			fmt.Println()
		}

		totalRecords := 0.0
		bestThroughput := 0.0
		worstThroughput := 0.0
		bestP99 := 0.0
		worstP99 := 0.0
		phaseCount := 0
		failedPhases := 0
		var errors []string

		for _, r := range run.Results {
			phaseCount++
			totalRecords += r.RecordsSent

			if r.ThroughputRecordsPerSec > bestThroughput {
				bestThroughput = r.ThroughputRecordsPerSec
			}
			if r.ThroughputRecordsPerSec > 0 && (worstThroughput == 0 || r.ThroughputRecordsPerSec < worstThroughput) {
				worstThroughput = r.ThroughputRecordsPerSec
			}
			if r.P99LatencyMs > worstP99 {
				worstP99 = r.P99LatencyMs
			}
			if r.P99LatencyMs > 0 && (bestP99 == 0 || r.P99LatencyMs < bestP99) {
				bestP99 = r.P99LatencyMs
			}

			if strings.ToUpper(r.Status) == "FAILED" {
				failedPhases++
				if r.Error != "" {
					errors = append(errors, r.Error)
				}
			}
		}

		output.SubHeader("Narrative")

		if run.Status == "DONE" {
			fmt.Printf("  Your %s test completed successfully, processing %s records\n",
				run.TestType, fmtNum(totalRecords))
			if bestThroughput > 0 {
				fmt.Printf("  across %d phases at a peak throughput of %s rec/s.\n",
					phaseCount, fmtNum(bestThroughput))
			}
			if worstP99 > 0 {
				fmt.Printf("  Tail latency (P99) ranged from %s to %s ms.\n",
					fmtFloat(bestP99, 3), fmtFloat(worstP99, 3))
			}
		} else if run.Status == "FAILED" {
			fmt.Printf("  Your %s test failed. %d of %d phases encountered errors.\n",
				run.TestType, failedPhases, phaseCount)
			if totalRecords > 0 {
				fmt.Printf("  Before failure, %s records were processed.\n", fmtNum(totalRecords))
			}
		} else {
			fmt.Printf("  Your %s test is currently %s with %d phases.\n",
				run.TestType, run.Status, phaseCount)
		}
		fmt.Println()

		if len(errors) > 0 {
			output.SubHeader("Root Cause")
			for _, e := range errors {
				output.Error("  " + e)
				for _, hint := range matchHints(e) {
					output.Hint("  💡 " + hint)
				}
			}
			fmt.Println()
		}

		verdict, verdictIcon := computeVerdict(run.Status, bestThroughput, worstP99, failedPhases)

		output.SubHeader("Verdict")
		fmt.Printf("  %s %s\n\n", verdictIcon, verdict)

		if run.Status == "DONE" && bestThroughput > 0 {
			output.SubHeader("Key Metrics")
			output.KeyValue("Records", fmtNum(totalRecords))
			output.KeyValue("Peak Throughput", fmtNum(bestThroughput)+" rec/s")
			if worstP99 > 0 {
				output.KeyValue("P99 Latency", fmtFloat(worstP99, 3)+" ms")
			}
			fmt.Println()
			output.MetricBar("Throughput", bestThroughput, 100000)
		}

		output.Hint("Full details: kates test get " + run.ID)
		return nil
	},
}

func computeVerdict(status string, throughput, p99 float64, failures int) (string, string) {
	if status == "FAILED" || failures > 0 {
		return "POOR — Test failed. Review errors above and re-run after fixing.", output.ErrorStyle.Render("✖")
	}
	if throughput < 1000 {
		return "DEGRADED — Very low throughput. Check cluster health or test configuration.", output.WarningStyle.Render("⚠")
	}
	if p99 > 100 {
		return "DEGRADED — High tail latency. Consider tuning batch size or linger.ms.", output.WarningStyle.Render("⚠")
	}
	if throughput > 30000 && p99 < 10 {
		return "HEALTHY — Excellent performance. Cluster is handling load well.", output.SuccessStyle.Render("✓")
	}
	return "HEALTHY — Test completed within acceptable parameters.", output.SuccessStyle.Render("✓")
}

func init() {
	rootCmd.AddCommand(explainCmd)
}
