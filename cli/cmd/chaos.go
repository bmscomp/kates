package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var chaosListLimit int

var chaosCmd = &cobra.Command{
	Use:     "chaos",
	Aliases: []string{"cx"},
	Short:   "Chaos experiment history and probe analysis",
}

var chaosListCmd = &cobra.Command{
	Use:   "list",
	Short: "List recent chaos experiment reports",
	RunE: func(cmd *cobra.Command, args []string) error {
		entries, err := apiClient.DisruptionList(context.Background(), chaosListLimit)
		if err != nil {
			return cmdErr("Failed to list chaos reports: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(entries)
			return nil
		}

		output.Header("Chaos Experiment History")

		if len(entries) == 0 {
			fmt.Println("  No chaos experiments found")
			return nil
		}

		rows := make([][]string, 0, len(entries))
		for _, e := range entries {
			rows = append(rows, []string{
				e.ID,
				e.PlanName,
				output.StatusBadge(e.Status),
				e.SlaGrade,
				e.CreatedAt,
			})
		}
		output.Table([]string{"ID", "Plan Name", "Status", "Grade", "Date"}, rows)

		return nil
	},
}

var chaosShowCmd = &cobra.Command{
	Use:   "show [id]",
	Short: "Show detailed chaos experiment report with probe breakdown",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		report, err := apiClient.DisruptionStatus(context.Background(), args[0])
		if err != nil {
			return cmdErr("Failed to get chaos report: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(report)
			return nil
		}

		output.Header("Chaos Report: " + report.PlanName)
		output.KeyValue("Status", output.StatusBadge(report.Status))

		renderSlaVerdict(report.SlaVerdict)

		if report.Summary != nil {
			s := report.Summary
			output.SubHeader("Summary")
			output.KeyValue("Steps", fmt.Sprintf("%d/%d passed", s.PassedSteps, s.TotalSteps))
			if s.WorstRecovery != nil {
				output.KeyValue("Worst Recovery", fmt.Sprintf("%v", s.WorstRecovery))
			}
			output.KeyValue("Throughput Impact", fmt.Sprintf("%+.1f%%", s.AvgThroughputDegradation))
			output.KeyValue("Max P99 Spike", fmt.Sprintf("%+.1f%%", s.MaxP99LatencySpike))
		}

		for _, step := range report.StepReports {
			renderStepReport(step)
		}

		return nil
	},
}

func renderProbeGauge(probeStr string) string {
	pct, err := strconv.ParseFloat(strings.TrimSuffix(probeStr, "%"), 64)
	if err != nil {
		return probeStr
	}

	totalBars := 20
	filled := int(pct / 100.0 * float64(totalBars))
	if filled < 0 {
		filled = 0
	}
	if filled > totalBars {
		filled = totalBars
	}
	empty := totalBars - filled

	bar := strings.Repeat("█", filled) + strings.Repeat("░", empty)

	var style lipgloss.Style
	switch {
	case pct >= 80:
		style = output.SuccessStyle
	case pct >= 50:
		style = output.WarningStyle
	default:
		style = output.ErrorStyle
	}

	return style.Render(bar) + " " + style.Render(fmt.Sprintf("%.0f%%", pct))
}

func init() {
	chaosListCmd.Flags().IntVar(&chaosListLimit, "limit", 20, "Maximum reports to display")

	chaosCmd.AddCommand(chaosListCmd)
	chaosCmd.AddCommand(chaosShowCmd)
	rootCmd.AddCommand(chaosCmd)
}
