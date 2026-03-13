package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/klster/kates-cli/client"
	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var (
	disruptionFile  string
	dryRunMode      bool
	failOnSlaBreach bool
	outputJUnit     string
)

var disruptionCmd = &cobra.Command{
	Use:   "disruption",
	Short: "Kubernetes-aware disruption testing with Kafka intelligence, safety guardrails, and SLA grading",
}

var disruptionRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Execute a disruption plan from a JSON config file",
	Example: `  kates disruption run --config disruption-plan.json
  kates disruption run --config plan.json --dry-run
  kates disruption run --config plan.json --fail-on-sla-breach --output-junit results.xml`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if disruptionFile == "" {
			return cmdErr("--config is required (path to JSON file)")
		}

		data, err := os.ReadFile(disruptionFile)
		if err != nil {
			return cmdErr("Failed to read config file: " + err.Error())
		}

		var plan interface{}
		if err := json.Unmarshal(data, &plan); err != nil {
			return cmdErr("Invalid JSON: " + err.Error())
		}

		if dryRunMode {
			return runDryRun(plan)
		}

		fmt.Println(output.AccentStyle.Render("◉ Running disruption plan..."))

		result, err := apiClient.RunDisruption(context.Background(), plan)
		if err != nil {
			return cmdErr("Disruption test failed: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(result)
			return checkSlaExit(&result.Report)
		}

		output.Header("Disruption Test Results")
		output.KeyValue("Plan", result.Report.PlanName)
		output.KeyValue("Status", output.StatusBadge(result.Report.Status))
		output.KeyValue("ID", result.ID)

		if len(result.Report.ValidationWarnings) > 0 {
			output.SubHeader("Safety Warnings")
			for _, w := range result.Report.ValidationWarnings {
				fmt.Println("  ⚠ " + w)
			}
		}

		renderSlaVerdict(result.Report.SlaVerdict)

		if result.Report.Summary != nil {
			s := result.Report.Summary
			output.SubHeader("Summary")
			output.KeyValue("Steps", fmt.Sprintf("%d/%d passed", s.PassedSteps, s.TotalSteps))
			if s.WorstRecovery != nil {
				output.KeyValue("Worst Recovery", fmt.Sprintf("%v", s.WorstRecovery))
			}
			output.KeyValue("Avg Throughput Impact", fmt.Sprintf("%+.1f%%", s.AvgThroughputDegradation))
			output.KeyValue("Max P99 Spike", fmt.Sprintf("%+.1f%%", s.MaxP99LatencySpike))
			if s.WorstIsrRecovery != nil {
				output.KeyValue("Worst ISR Recovery", fmt.Sprintf("%v", s.WorstIsrRecovery))
			}
			if s.PeakConsumerLag > 0 {
				output.KeyValue("Peak Consumer Lag", fmt.Sprintf("%d", s.PeakConsumerLag))
			}
			if s.SlaViolated {
				output.KeyValue("SLA", output.StatusBadge("VIOLATED"))
			}
		}

		for _, step := range result.Report.StepReports {
			renderStepReport(step)
		}

		if outputJUnit != "" {
			if err := writeJUnitXML(&result.Report, outputJUnit); err != nil {
				fmt.Println("  ⚠ JUnit XML write failed: " + err.Error())
			} else {
				fmt.Println("  ✓ JUnit XML written to " + outputJUnit)
			}
		}

		return checkSlaExit(&result.Report)
	},
}

func runDryRun(plan interface{}) error {
	fmt.Println(output.AccentStyle.Render("◉ Dry-run — validating plan without execution..."))

	result, err := apiClient.RunDryRun(context.Background(), plan)
	if err != nil {
		return cmdErr("Dry-run failed: " + err.Error())
	}

	if outputMode == "json" {
		output.JSON(result)
		return nil
	}

	output.Header("Dry-Run Results")

	if result.WouldSucceed {
		output.KeyValue("Verdict", output.StatusBadge("SAFE"))
	} else {
		output.KeyValue("Verdict", output.StatusBadge("UNSAFE"))
	}
	output.KeyValue("Total Brokers", fmt.Sprintf("%d", result.TotalBrokers))

	if len(result.Errors) > 0 {
		output.SubHeader("Errors")
		for _, e := range result.Errors {
			fmt.Println("  ✗ " + e)
		}
	}

	if len(result.Warnings) > 0 {
		output.SubHeader("Warnings")
		for _, w := range result.Warnings {
			fmt.Println("  ⚠ " + w)
		}
	}

	for _, step := range result.Steps {
		output.SubHeader("Step: " + step.Name + " (" + step.DisruptionType + ")")
		if step.TargetPod != "" {
			output.KeyValue("Target Pod", step.TargetPod)
		}
		if step.ResolvedLeaderId != nil {
			output.KeyValue("Resolved Leader", fmt.Sprintf("broker-%d", *step.ResolvedLeaderId))
		}
		if len(step.AffectedPods) > 0 {
			output.KeyValue("Affected Pods", fmt.Sprintf("%d pods", len(step.AffectedPods)))
			for _, pod := range step.AffectedPods {
				fmt.Println("    • " + pod)
			}
		}
		for _, w := range step.Warnings {
			fmt.Println("    ⚠ " + w)
		}
	}

	return nil
}

func renderSlaVerdict(verdict *client.SlaVerdict) {
	if verdict == nil {
		return
	}

	output.SubHeader("SLA Grade")
	output.KeyValue("Grade", output.StatusBadge(verdict.Grade))
	output.KeyValue("Checks", fmt.Sprintf("%d/%d passed", verdict.PassedChecks, verdict.TotalChecks))

	if len(verdict.Violations) > 0 {
		rows := make([][]string, 0, len(verdict.Violations))
		for _, v := range verdict.Violations {
			name := v.MetricName
			if name == "" {
				name = v.Metric
			}
			rows = append(rows, []string{
				name,
				v.Constraint,
				fmt.Sprintf("%.2f", v.Threshold),
				fmt.Sprintf("%.2f", v.Actual),
				v.Severity,
			})
		}
		output.Table([]string{"Metric", "Constraint", "Threshold", "Actual", "Severity"}, rows)
	}
}

func checkSlaExit(report *client.DisruptionReport) error {
	if !failOnSlaBreach {
		return nil
	}
	if report == nil {
		return nil
	}
	if report.SlaVerdict != nil && report.SlaVerdict.Violated {
		os.Exit(1)
	}
	if report.Summary != nil && report.Summary.SlaViolated {
		os.Exit(1)
	}
	return nil
}

var disruptionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List recent disruption test reports",
	RunE: func(cmd *cobra.Command, args []string) error {
		entries, err := apiClient.DisruptionList(context.Background(), 20)
		if err != nil {
			return cmdErr("Failed to list disruption reports: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(entries)
			return nil
		}

		output.Header("Disruption Reports")

		if len(entries) == 0 {
			fmt.Println("  No disruption reports found")
			return nil
		}

		rows := make([][]string, 0, len(entries))
		for _, e := range entries {
			rows = append(rows, []string{
				e.ID, e.PlanName, e.Status, e.SlaGrade, e.CreatedAt,
			})
		}
		output.Table([]string{"ID", "Plan", "Status", "SLA", "Created"}, rows)

		return nil
	},
}

var disruptionStatusCmd = &cobra.Command{
	Use:   "status [id]",
	Short: "Show disruption test report",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		report, err := apiClient.DisruptionStatus(context.Background(), args[0])
		if err != nil {
			return cmdErr("Failed to get disruption status: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(report)
			return nil
		}

		output.Header("Disruption Report: " + report.PlanName)
		output.KeyValue("Status", output.StatusBadge(report.Status))

		if len(report.ValidationWarnings) > 0 {
			output.SubHeader("Safety Warnings")
			for _, w := range report.ValidationWarnings {
				fmt.Println("  ⚠ " + w)
			}
		}

		renderSlaVerdict(report.SlaVerdict)

		if report.Summary != nil {
			s := report.Summary
			output.SubHeader("Summary")
			output.KeyValue("Steps", fmt.Sprintf("%d/%d passed", s.PassedSteps, s.TotalSteps))
			if s.WorstRecovery != nil {
				output.KeyValue("Worst Recovery", fmt.Sprintf("%v", s.WorstRecovery))
			}
			if s.WorstIsrRecovery != nil {
				output.KeyValue("Worst ISR Recovery", fmt.Sprintf("%v", s.WorstIsrRecovery))
			}
			if s.PeakConsumerLag > 0 {
				output.KeyValue("Peak Consumer Lag", fmt.Sprintf("%d", s.PeakConsumerLag))
			}
		}

		for _, step := range report.StepReports {
			renderStepReport(step)
		}

		return nil
	},
}

var disruptionTimelineCmd = &cobra.Command{
	Use:   "timeline [id]",
	Short: "Show pod event timeline for a disruption test",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		timelines, err := apiClient.DisruptionTimelineData(context.Background(), args[0])
		if err != nil {
			return cmdErr("Failed to get timeline: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(timelines)
			return nil
		}

		output.Header("Pod Event Timeline")

		for _, tl := range timelines {
			output.SubHeader(fmt.Sprintf("Step: %s (%s)", tl.Step, tl.Type))
			output.KeyValue("Time to First Ready", fmt.Sprintf("%v", tl.TimeToFirstReady))
			output.KeyValue("Time to All Ready", fmt.Sprintf("%v", tl.TimeToAllReady))

			if len(tl.Events) > 0 {
				rows := make([][]string, 0, len(tl.Events))
				for _, ev := range tl.Events {
					rows = append(rows, []string{ev.Timestamp, ev.PodName, ev.EventType, ev.Phase})
				}
				output.Table([]string{"Time", "Pod", "Event", "Phase"}, rows)
			}
		}

		return nil
	},
}

var disruptionTypesCmd = &cobra.Command{
	Use:   "types",
	Short: "List available disruption types",
	RunE: func(cmd *cobra.Command, args []string) error {
		types, err := apiClient.DisruptionTypes(context.Background())
		if err != nil {
			return cmdErr("Failed to list disruption types: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(types)
			return nil
		}

		output.Header("Available Disruption Types")

		rows := make([][]string, 0, len(types))
		for _, t := range types {
			rows = append(rows, []string{t.Name, t.Description})
		}
		output.Table([]string{"Type", "Description"}, rows)

		return nil
	},
}

var disruptionKafkaMetricsCmd = &cobra.Command{
	Use:   "kafka-metrics [id]",
	Short: "Show Kafka intelligence metrics (ISR tracking, consumer lag, leader targeting)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		metrics, err := apiClient.DisruptionKafkaMetrics(context.Background(), args[0])
		if err != nil {
			return cmdErr("Failed to get Kafka metrics: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(metrics)
			return nil
		}

		output.Header("Kafka Intelligence Metrics")

		for _, m := range metrics {
			output.SubHeader(fmt.Sprintf("Step: %s (%s)", m.Step, m.DisruptionType))

			if m.TargetedLeaderBrokerId != nil {
				output.KeyValue("Targeted Leader", fmt.Sprintf("broker-%d (auto-resolved)", *m.TargetedLeaderBrokerId))
			}

			if m.Isr != nil {
				output.SubHeader("  ISR Health")
				if m.Isr.TimeToFullIsr != nil {
					output.KeyValue("  Time to Full ISR", fmt.Sprintf("%v", m.Isr.TimeToFullIsr))
				} else {
					output.KeyValue("  Time to Full ISR", output.StatusBadge("NOT RECOVERED"))
				}
				output.KeyValue("  Min ISR Depth", fmt.Sprintf("%d", m.Isr.MinIsrDepth))
				output.KeyValue("  Under-Replicated Peak", fmt.Sprintf("%d partitions", m.Isr.UnderReplicatedPeak))
				output.KeyValue("  Total Partitions", fmt.Sprintf("%d", m.Isr.TotalPartitions))
			}

			if m.Lag != nil {
				output.SubHeader("  Consumer Lag")
				output.KeyValue("  Baseline Lag", fmt.Sprintf("%d", m.Lag.BaselineLag))
				output.KeyValue("  Peak Lag", fmt.Sprintf("%d", m.Lag.PeakLag))
				output.KeyValue("  Lag Spike", fmt.Sprintf("+%d", m.Lag.PeakLag-m.Lag.BaselineLag))
				if m.Lag.TimeToLagRecovery != nil {
					output.KeyValue("  Time to Lag Recovery", fmt.Sprintf("%v", m.Lag.TimeToLagRecovery))
				} else {
					output.KeyValue("  Time to Lag Recovery", output.StatusBadge("NOT RECOVERED"))
				}
			}

			if m.Isr == nil && m.Lag == nil && m.TargetedLeaderBrokerId == nil {
				output.KeyValue("  Intelligence", "No Kafka intelligence configured for this step")
			}
		}

		return nil
	},
}

func renderStepReport(step client.StepReport) {
	output.SubHeader("Step: " + step.StepName)
	output.KeyValue("Type", step.DisruptionType)

	if step.TargetedLeaderBroker != nil {
		output.KeyValue("Targeted Leader", fmt.Sprintf("broker-%d", *step.TargetedLeaderBroker))
	}

	if step.ChaosOutcome != nil {
		output.KeyValue("Verdict", output.StatusBadge(step.ChaosOutcome.Verdict))
		if step.ChaosOutcome.Phase != "" {
			output.KeyValue("Phase", step.ChaosOutcome.Phase)
		}
		if step.ChaosOutcome.FailStep != "" {
			output.KeyValue("Fail Step", step.ChaosOutcome.FailStep)
		}
		if step.ChaosOutcome.ProbeSuccess != "" {
			output.KeyValue("Probe Success", renderProbeGauge(step.ChaosOutcome.ProbeSuccess))
		}
		if step.ChaosOutcome.FailureReason != "" {
			output.KeyValue("Failure", step.ChaosOutcome.FailureReason)
		}
	}
	if step.TimeToFirstReady != nil {
		output.KeyValue("Time to First Ready", fmt.Sprintf("%v", step.TimeToFirstReady))
	}
	if step.TimeToAllReady != nil {
		output.KeyValue("Time to All Ready", fmt.Sprintf("%v", step.TimeToAllReady))
	}
	if step.StrimziRecoveryTime != nil {
		output.KeyValue("Strimzi Recovery", fmt.Sprintf("%v", step.StrimziRecoveryTime))
	}
	if len(step.PodTimeline) > 0 {
		output.KeyValue("Pod Events", fmt.Sprintf("%d events", len(step.PodTimeline)))
	}

	if step.PreDisruptionMetrics != nil && step.PostDisruptionMetrics != nil {
		output.SubHeader("  Prometheus Metrics")
		output.KeyValue("  Pre-Disruption Throughput",
			fmt.Sprintf("%.1f rec/s", step.PreDisruptionMetrics.AvgThroughputRecPerSec))
		output.KeyValue("  Post-Disruption Throughput",
			fmt.Sprintf("%.1f rec/s", step.PostDisruptionMetrics.AvgThroughputRecPerSec))
		if step.PreDisruptionMetrics.P99LatencyMs > 0 || step.PostDisruptionMetrics.P99LatencyMs > 0 {
			output.KeyValue("  Pre-Disruption P99",
				fmt.Sprintf("%.2fms", step.PreDisruptionMetrics.P99LatencyMs))
			output.KeyValue("  Post-Disruption P99",
				fmt.Sprintf("%.2fms", step.PostDisruptionMetrics.P99LatencyMs))
		}
	}

	if len(step.ImpactDeltas) > 0 {
		output.SubHeader("  Impact Deltas")
		for metric, delta := range step.ImpactDeltas {
			output.KeyValue("  "+metric, fmt.Sprintf("%+.1f%%", delta))
		}
	}

	if step.IsrMetrics != nil {
		if step.IsrMetrics.TimeToFullIsr != nil {
			output.KeyValue("Time to Full ISR", fmt.Sprintf("%v", step.IsrMetrics.TimeToFullIsr))
		}
		output.KeyValue("Min ISR Depth", fmt.Sprintf("%d", step.IsrMetrics.MinIsrDepth))
	}
	if step.LagMetrics != nil {
		output.KeyValue("Peak Lag", fmt.Sprintf("%d (baseline: %d)", step.LagMetrics.PeakLag, step.LagMetrics.BaselineLag))
		if step.LagMetrics.TimeToLagRecovery != nil {
			output.KeyValue("Lag Recovery", fmt.Sprintf("%v", step.LagMetrics.TimeToLagRecovery))
		}
	}

	if step.RolledBack {
		output.KeyValue("Rollback", output.StatusBadge("ROLLED BACK"))
		if step.RollbackReason != "" {
			output.KeyValue("Rollback Reason", step.RollbackReason)
		}
	}
}

func init() {
	disruptionRunCmd.Flags().StringVar(&disruptionFile, "config", "", "Path to disruption plan JSON config (required)")
	disruptionRunCmd.Flags().BoolVar(&dryRunMode, "dry-run", false, "Validate plan without executing (checks targets, RBAC, blast radius)")
	disruptionRunCmd.Flags().BoolVar(&failOnSlaBreach, "fail-on-sla-breach", false, "Exit with code 1 if SLA is violated (for CI/CD pipelines)")
	disruptionRunCmd.Flags().StringVar(&outputJUnit, "output-junit", "", "Write JUnit XML report to file (for CI/CD integration)")

	disruptionCmd.AddCommand(disruptionRunCmd)
	disruptionCmd.AddCommand(disruptionListCmd)
	disruptionCmd.AddCommand(disruptionStatusCmd)
	disruptionCmd.AddCommand(disruptionTimelineCmd)
	disruptionCmd.AddCommand(disruptionTypesCmd)
	disruptionCmd.AddCommand(disruptionKafkaMetricsCmd)
	rootCmd.AddCommand(disruptionCmd)
}
