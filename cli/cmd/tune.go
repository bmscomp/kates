package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var tuneStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
var tuneBest = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
var tuneWorst = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
var tuneHeader = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))

var tuneCmd = &cobra.Command{
	Use:   "tune",
	Short: "Configuration & tuning tests",
	Long:  "Run parameter sweep tests to find optimal Kafka configuration",
}

var tuneRunCmd = &cobra.Command{
	Use:   "run <type>",
	Short: "Run a tuning test",
	Long: `Execute a tuning test that sweeps a configuration parameter.
Available types: TUNE_REPLICATION, TUNE_ACKS, TUNE_BATCHING, TUNE_COMPRESSION, TUNE_PARTITIONS

With -o json it prints the run the backend created, as kates test create does.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		testType := strings.ToUpper(args[0])
		if !strings.HasPrefix(testType, "TUNE_") {
			testType = "TUNE_" + testType
		}

		req := &client.CreateTestRequest{
			TestType: testType,
		}
		run, err := apiClient.CreateTest(ctx, req)
		if err != nil {
			return fmt.Errorf("create tuning test: %w", err)
		}

		if outputMode == "json" {
			output.JSON(run)
			return nil
		}
		fmt.Println(tuneStyle.Render("⚙ Tuning test submitted"))
		fmt.Printf("  Type:   %s\n", testType)
		fmt.Printf("  Run ID: %s\n", run.ID)
		fmt.Println()
		fmt.Printf("  View results: kates tune report %s\n", run.ID)
		return nil
	},
}

var tuneReportCmd = &cobra.Command{
	Use:   "report <run-id>",
	Short: "Show tuning comparison report",
	Long: `Show the tuning report of a run: each step's configuration, throughput,
p99 latency and error rate, and which step the backend ranks best.

With -o json it prints the same rows as JSON; a metric the step has no value
for is null.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		report, err := apiClient.ReportTuning(context.Background(), args[0])
		if err != nil {
			return fmt.Errorf("tuning report: %w", err)
		}
		res := buildTuneReport(args[0], report)
		output.Render(outputMode == "json", res, func() { renderTuneReport(res) })
		return nil
	},
}

// tuneReportResult is the comparison kates tune report prints: the table
// shows it, -o json prints it as is.
type tuneReportResult struct {
	RunID          string        `json:"runId"`
	TestType       string        `json:"testType"`
	ParameterName  string        `json:"parameterName"`
	BestStepIndex  int           `json:"bestStepIndex"`
	Recommendation string        `json:"recommendation,omitempty"`
	Steps          []tuneStepRow `json:"steps"`
}

// tuneStepRow is one step. Verdict is BEST for the step the backend ranks
// best, WORST for the one with the lowest throughput, and empty otherwise.
type tuneStepRow struct {
	StepIndex              int      `json:"stepIndex"`
	Label                  string   `json:"label"`
	AvgThroughputRecPerSec *float64 `json:"avgThroughputRecPerSec"`
	P99LatencyMs           *float64 `json:"p99LatencyMs"`
	ErrorRate              *float64 `json:"errorRate"`
	Verdict                string   `json:"verdict,omitempty"`
}

func buildTuneReport(runID string, report *client.TuningReport) tuneReportResult {
	res := tuneReportResult{
		RunID:          runID,
		TestType:       report.TestType,
		ParameterName:  report.ParameterName,
		BestStepIndex:  report.BestStepIndex,
		Recommendation: report.Recommendation,
		Steps:          make([]tuneStepRow, 0, len(report.Steps)),
	}
	worstIdx := findWorstStep(report.Steps)
	for i, step := range report.Steps {
		row := tuneStepRow{
			StepIndex:              step.StepIndex,
			Label:                  step.Label,
			AvgThroughputRecPerSec: metricValue(step.Metrics, "avgThroughputRecPerSec"),
			P99LatencyMs:           metricValue(step.Metrics, "p99LatencyMs"),
			ErrorRate:              metricValue(step.Metrics, "errorRate"),
		}
		// findWorstStep returns a position in Steps, not a StepIndex.
		if step.StepIndex == report.BestStepIndex {
			row.Verdict = "BEST"
		} else if i == worstIdx {
			row.Verdict = "WORST"
		}
		res.Steps = append(res.Steps, row)
	}
	return res
}

func renderTuneReport(res tuneReportResult) {
	fmt.Println(tuneHeader.Render(fmt.Sprintf("  Tuning Report — %s", res.TestType)))
	fmt.Printf("  Parameter: %s\n\n", res.ParameterName)

	maxLabel := 10
	for _, s := range res.Steps {
		if len(s.Label) > maxLabel {
			maxLabel = len(s.Label)
		}
	}

	headerFmt := fmt.Sprintf("  %%-%ds  %%12s  %%12s  %%12s  %%s\n", maxLabel)
	rowFmt := fmt.Sprintf("  %%-%ds  %%12s  %%12s  %%12s  %%s\n", maxLabel)

	fmt.Printf(headerFmt, "CONFIG", "THROUGHPUT", "P99 LATENCY", "ERROR RATE", "VERDICT")
	fmt.Printf(headerFmt, strings.Repeat("─", maxLabel), "────────────", "────────────", "────────────", "───────")

	for _, step := range res.Steps {
		verdict := ""
		switch step.Verdict {
		case "BEST":
			verdict = tuneBest.Render("★ BEST")
		case "WORST":
			verdict = tuneWorst.Render("▼ WORST")
		}
		fmt.Printf(rowFmt, step.Label,
			metricStr(step.AvgThroughputRecPerSec, "rec/s"),
			metricStr(step.P99LatencyMs, "ms"),
			metricStr(step.ErrorRate, "%"),
			verdict)
	}

	fmt.Println()
	if res.Recommendation != "" {
		fmt.Println(tuneStyle.Render("  💡 " + res.Recommendation))
	}
}

var tuneTypesCmd = &cobra.Command{
	Use:   "types",
	Short: "List available tuning tests",
	RunE: func(cmd *cobra.Command, args []string) error {
		types, err := apiClient.TuningTypes(context.Background())
		if err != nil {
			return fmt.Errorf("list tuning types: %w", err)
		}

		if outputMode == "json" {
			output.JSON(types)
			return nil
		}
		fmt.Println(tuneHeader.Render("  Available Tuning Tests"))
		fmt.Println()
		for _, t := range types {
			fmt.Printf("  %s\n", tuneBest.Render(t.Type))
			fmt.Printf("    Parameter: %s\n", t.Parameter)
			fmt.Printf("    Steps:     %d\n", t.Steps)
			fmt.Printf("    %s\n\n", t.Description)
		}
		return nil
	},
}

// metricValue returns a step's metric, or nil when the step has none, which
// the table shows as a dash and JSON as null.
func metricValue(metrics map[string]float64, key string) *float64 {
	v, ok := metrics[key]
	if !ok {
		return nil
	}
	return &v
}

func metricStr(v *float64, unit string) string {
	if v == nil {
		return "—"
	}
	if unit == "rec/s" {
		return fmt.Sprintf("%.0f %s", *v, unit)
	}
	if unit == "%" {
		return fmt.Sprintf("%.3f%s", *v*100, unit)
	}
	return fmt.Sprintf("%.1f %s", *v, unit)
}

func findWorstStep(steps []client.TuningStep) int {
	worst := 0
	worstVal := -1.0
	for i, s := range steps {
		if s.Metrics != nil {
			v := s.Metrics["avgThroughputRecPerSec"]
			if worstVal < 0 || v < worstVal {
				worstVal = v
				worst = i
			}
		}
	}
	return worst
}

func init() {
	tuneCmd.AddCommand(tuneRunCmd, tuneReportCmd, tuneTypesCmd)
	rootCmd.AddCommand(tuneCmd)
}
