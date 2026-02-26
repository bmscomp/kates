package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/klster/kates-cli/client"
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
	Long:  "Execute a tuning test that sweeps a configuration parameter.\nAvailable types: TUNE_REPLICATION, TUNE_ACKS, TUNE_BATCHING, TUNE_COMPRESSION, TUNE_PARTITIONS",
	Args:  cobra.ExactArgs(1),
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
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		report, err := apiClient.ReportTuning(context.Background(), args[0])
		if err != nil {
			return fmt.Errorf("tuning report: %w", err)
		}

		fmt.Println(tuneHeader.Render(fmt.Sprintf("  Tuning Report — %s", report.TestType)))
		fmt.Printf("  Parameter: %s\n\n", report.ParameterName)

		maxLabel := 10
		for _, s := range report.Steps {
			if len(s.Label) > maxLabel {
				maxLabel = len(s.Label)
			}
		}

		headerFmt := fmt.Sprintf("  %%-%ds  %%12s  %%12s  %%12s  %%s\n", maxLabel)
		rowFmt := fmt.Sprintf("  %%-%ds  %%12s  %%12s  %%12s  %%s\n", maxLabel)

		fmt.Printf(headerFmt, "CONFIG", "THROUGHPUT", "P99 LATENCY", "ERROR RATE", "VERDICT")
		fmt.Printf(headerFmt, strings.Repeat("─", maxLabel), "────────────", "────────────", "────────────", "───────")

		worstIdx := findWorstStep(report.Steps)

		for _, step := range report.Steps {
			throughput := metricStr(step.Metrics, "avgThroughputRecPerSec", "rec/s")
			p99 := metricStr(step.Metrics, "p99LatencyMs", "ms")
			errRate := metricStr(step.Metrics, "errorRate", "%")

			verdict := ""
			if step.StepIndex == report.BestStepIndex {
				verdict = tuneBest.Render("★ BEST")
			} else if step.StepIndex == worstIdx {
				verdict = tuneWorst.Render("▼ WORST")
			}

			fmt.Printf(rowFmt, step.Label, throughput, p99, errRate, verdict)
		}

		fmt.Println()
		if report.Recommendation != "" {
			fmt.Println(tuneStyle.Render("  💡 " + report.Recommendation))
		}
		return nil
	},
}

var tuneTypesCmd = &cobra.Command{
	Use:   "types",
	Short: "List available tuning tests",
	RunE: func(cmd *cobra.Command, args []string) error {
		types, err := apiClient.TuningTypes(context.Background())
		if err != nil {
			return fmt.Errorf("list tuning types: %w", err)
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

func metricStr(metrics map[string]float64, key, unit string) string {
	if metrics == nil {
		return "—"
	}
	v, ok := metrics[key]
	if !ok {
		return "—"
	}
	if unit == "rec/s" {
		return fmt.Sprintf("%.0f %s", v, unit)
	}
	if unit == "%" {
		return fmt.Sprintf("%.3f%s", v*100, unit)
	}
	return fmt.Sprintf("%.1f %s", v, unit)
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
