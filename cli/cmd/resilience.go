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

var resilienceFile string

var resilienceCmd = &cobra.Command{
	Use:   "resilience",
	Short: "Run combined performance + chaos resilience tests",
}

var resilienceRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Execute a resilience test from a JSON config file",
	Example: `  kates resilience run --config resilience-test.json

  # Example resilience-test.json:
  {
    "testRequest": { "testType": "LOAD", "spec": { "records": 100000 } },
    "chaosSpec": { "experimentName": "kafka-pod-kill", "targetNamespace": "kafka" },
    "steadyStateSec": 30
  }`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if resilienceFile == "" {
			return cmdErr("--config is required (path to JSON file)")
		}

		data, err := os.ReadFile(resilienceFile)
		if err != nil {
			return cmdErr("Failed to read config file: " + err.Error())
		}

		var req interface{}
		if err := json.Unmarshal(data, &req); err != nil {
			return cmdErr("Invalid JSON: " + err.Error())
		}

		fmt.Println(output.AccentStyle.Render("◉ Running resilience test..."))

		result, err := apiClient.Resilience(context.Background(), req)
		if err != nil {
			return cmdErr("Resilience test failed: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		output.Header("Resilience Test Results")
		output.KeyValue("Status", output.StatusBadge(result.Status))

		if chaos := result.ChaosOutcome; chaos != nil {
			output.SubHeader("Chaos Outcome")
			output.KeyValue("Experiment", chaos.ExperimentName)
			output.KeyValue("Verdict", output.StatusBadge(chaos.Verdict))
			output.KeyValue("Duration", chaos.ChaosDuration)
			if chaos.Phase != "" {
				output.KeyValue("Phase", chaos.Phase)
			}
			if chaos.FailStep != "" {
				output.KeyValue("Fail Step", chaos.FailStep)
			}
			if chaos.ProbeSuccess != "" {
				output.KeyValue("Probe Success", renderProbeGauge(chaos.ProbeSuccess))
			}
			if chaos.FailureReason != "" {
				output.KeyValue("Failure Reason", chaos.FailureReason)
			}
		}

		if len(result.ImpactDeltas) > 0 {
			output.SubHeader("Impact Analysis (% change)")
			rows := make([][]string, 0, len(result.ImpactDeltas))
			for metric, v := range result.ImpactDeltas {
				marker := ""
				if v > 10 {
					marker = "▲"
				} else if v < -10 {
					marker = "▼"
				}
				rows = append(rows, []string{metric, fmt.Sprintf("%+.1f%%", v), marker})
			}
			output.Table([]string{"Metric", "Change", ""}, rows)
		}

		showSummary := func(label string, s *client.ReportSummary) {
			if s != nil {
				output.SubHeader(label)
				output.KeyValue("Throughput (rec/s)", fmt.Sprintf("%.1f", s.AvgThroughputRecPerSec))
				output.KeyValue("P99 Latency (ms)", fmt.Sprintf("%.2f", s.P99LatencyMs))
				output.KeyValue("Error Rate", fmt.Sprintf("%.4f%%", s.ErrorRate*100))
			}
		}
		showSummary("Pre-Chaos Baseline", result.PreChaosSummary)
		showSummary("Post-Chaos Impact", result.PostChaosSummary)

		return nil
	},
}

func init() {
	resilienceRunCmd.Flags().StringVar(&resilienceFile, "config", "", "Path to resilience test JSON config (required)")

	resilienceCmd.AddCommand(resilienceRunCmd)
	rootCmd.AddCommand(resilienceCmd)
}
