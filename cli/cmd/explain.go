package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
)

var explainCmd = &cobra.Command{
	Use:     "explain <id>",
	Aliases: []string{"why", "interpret"},
	Short:   "Plain-English summary and verdict for a test run",
	Example: `  kates explain 69acdf31
  kates explain 69acdf31 -o json`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		run, err := apiClient.GetTest(ctx, args[0])
		if err != nil {
			return cmdErr("Test not found: " + err.Error())
		}

		res := buildExplainResult(run)
		output.Render(outputMode == "json", res, func() { renderExplain(res) })
		return nil
	},
}

// explainResult is what kates explain concludes about one run. The table
// prints it as prose and -o json prints it as is, so a script reads the same
// verdict and figures a person does.
type explainResult struct {
	RunID           string `json:"runId"`
	TestType        string `json:"testType"`
	Status          string `json:"status"`
	TypeDescription string `json:"typeDescription,omitempty"`
	// Narrative is the prose the table prints under Narrative, one line each.
	Narrative    []string `json:"narrative"`
	Phases       int      `json:"phases"`
	FailedPhases int      `json:"failedPhases"`
	TotalRecords float64  `json:"totalRecords"`
	// The metrics are null when no phase reported them.
	PeakThroughputRecPerSec *float64       `json:"peakThroughputRecPerSec"`
	BestP99LatencyMs        *float64       `json:"bestP99LatencyMs"`
	WorstP99LatencyMs       *float64       `json:"worstP99LatencyMs"`
	Errors                  []explainError `json:"errors,omitempty"`
	// Verdict is HEALTHY, DEGRADED or POOR; VerdictReason says why.
	Verdict       string `json:"verdict"`
	VerdictReason string `json:"verdictReason"`
}

// explainError is one failed phase's error with the hints that match it.
type explainError struct {
	Message string   `json:"message"`
	Hints   []string `json:"hints,omitempty"`
}

func buildExplainResult(run *client.TestRun) explainResult {
	res := explainResult{
		RunID:           run.ID,
		TestType:        run.TestType,
		Status:          run.Status,
		TypeDescription: describeType(run.TestType),
	}

	var peak, bestP99, worstP99 float64
	for _, r := range run.Results {
		res.Phases++
		res.TotalRecords += r.RecordsSent

		peak = max(peak, r.ThroughputRecordsPerSec)
		worstP99 = max(worstP99, r.P99LatencyMs)
		if r.P99LatencyMs > 0 && (bestP99 == 0 || r.P99LatencyMs < bestP99) {
			bestP99 = r.P99LatencyMs
		}

		if strings.ToUpper(r.Status) == "FAILED" {
			res.FailedPhases++
			if r.Error != "" {
				res.Errors = append(res.Errors, explainError{Message: r.Error, Hints: matchHints(r.Error)})
			}
		}
	}

	res.PeakThroughputRecPerSec, res.BestP99LatencyMs, res.WorstP99LatencyMs = reported(peak), reported(bestP99), reported(worstP99)

	switch run.Status {
	case "DONE":
		res.Narrative = append(res.Narrative, fmt.Sprintf("Your %s test completed successfully, processing %s records",
			run.TestType, fmtNum(res.TotalRecords)))
		if peak > 0 {
			res.Narrative = append(res.Narrative, fmt.Sprintf("across %d phases at a peak throughput of %s rec/s.",
				res.Phases, fmtNum(peak)))
		}
		if worstP99 > 0 {
			res.Narrative = append(res.Narrative, fmt.Sprintf("Tail latency (P99) ranged from %s to %s ms.",
				fmtFloat(bestP99, 3), fmtFloat(worstP99, 3)))
		}
	case "FAILED":
		res.Narrative = append(res.Narrative, fmt.Sprintf("Your %s test failed. %d of %d phases encountered errors.",
			run.TestType, res.FailedPhases, res.Phases))
		if res.TotalRecords > 0 {
			res.Narrative = append(res.Narrative, fmt.Sprintf("Before failure, %s records were processed.", fmtNum(res.TotalRecords)))
		}
	default:
		res.Narrative = append(res.Narrative, fmt.Sprintf("Your %s test is currently %s with %d phases.",
			run.TestType, run.Status, res.Phases))
	}

	res.Verdict, res.VerdictReason = computeVerdict(run.Status, peak, worstP99, res.FailedPhases)
	return res
}

func renderExplain(res explainResult) {
	output.Banner("Test Explanation", res.TestType+" · "+truncID(res.RunID))
	fmt.Println()

	if res.TypeDescription != "" {
		output.Hint(res.TypeDescription)
		fmt.Println()
	}

	output.SubHeader("Narrative")
	for _, line := range res.Narrative {
		fmt.Printf("  %s\n", line)
	}
	fmt.Println()

	if len(res.Errors) > 0 {
		output.SubHeader("Root Cause")
		for _, e := range res.Errors {
			output.Error("  " + e.Message)
			for _, hint := range e.Hints {
				output.Hint("  💡 " + hint)
			}
		}
		fmt.Println()
	}

	output.SubHeader("Verdict")
	fmt.Printf("  %s %s — %s\n\n", verdictIcon(res.Verdict), res.Verdict, res.VerdictReason)

	if res.Status == "DONE" && res.PeakThroughputRecPerSec != nil {
		output.SubHeader("Key Metrics")
		output.KeyValue("Records", fmtNum(res.TotalRecords))
		output.KeyValue("Peak Throughput", fmtNum(*res.PeakThroughputRecPerSec)+" rec/s")
		if res.WorstP99LatencyMs != nil {
			output.KeyValue("P99 Latency", fmtFloat(*res.WorstP99LatencyMs, 3)+" ms")
		}
		fmt.Println()
		output.MetricBar("Throughput", *res.PeakThroughputRecPerSec, 100000)
	}

	output.Hint("Full details: kates test get " + res.RunID)
}

// computeVerdict grades a run as HEALTHY, DEGRADED or POOR and says why.
func computeVerdict(status string, throughput, p99 float64, failures int) (verdict, reason string) {
	if status == "FAILED" || failures > 0 {
		return "POOR", "Test failed. Review errors above and re-run after fixing."
	}
	if throughput < 1000 {
		return "DEGRADED", "Very low throughput. Check cluster health or test configuration."
	}
	if p99 > 100 {
		return "DEGRADED", "High tail latency. Consider tuning batch size or linger.ms."
	}
	if throughput > 30000 && p99 < 10 {
		return "HEALTHY", "Excellent performance. Cluster is handling load well."
	}
	return "HEALTHY", "Test completed within acceptable parameters."
}

func verdictIcon(verdict string) string {
	switch verdict {
	case "POOR":
		return output.ErrorStyle.Render("✖")
	case "DEGRADED":
		return output.WarningStyle.Render("⚠")
	default:
		return output.SuccessStyle.Render("✓")
	}
}

func init() {
	rootCmd.AddCommand(explainCmd)
}
