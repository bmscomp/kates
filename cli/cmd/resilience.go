package cmd

import (
	"encoding/json"
	"context"
	"fmt"
	"os"

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
			output.Error("--config is required (path to JSON file)")
			return nil
		}

		data, err := os.ReadFile(resilienceFile)
		if err != nil {
			output.Error("Failed to read config file: " + err.Error())
			return nil
		}

		var req map[string]interface{}
		if err := json.Unmarshal(data, &req); err != nil {
			output.Error("Invalid JSON: " + err.Error())
			return nil
		}

		fmt.Println(output.AccentStyle.Render("◉ Running resilience test..."))

		result, err := apiClient.Resilience(context.Background(), req)
		if err != nil {
			output.Error("Resilience test failed: " + err.Error())
			return nil
		}

		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		output.Header("Resilience Test Results")
		output.KeyValue("Status", output.StatusBadge(mapStr(result, "status")))

		// Chaos outcome
		if chaos, ok := result["chaosOutcome"].(map[string]interface{}); ok {
			output.SubHeader("Chaos Outcome")
			output.KeyValue("Experiment", mapStr(chaos, "experimentName"))
			output.KeyValue("Verdict", output.StatusBadge(mapStr(chaos, "verdict")))
			output.KeyValue("Duration", mapStr(chaos, "chaosDuration"))
			if reason := mapStr(chaos, "failureReason"); reason != "—" {
				output.KeyValue("Failure Reason", reason)
			}
		}

		// Impact deltas
		if deltas, ok := result["impactDeltas"].(map[string]interface{}); ok {
			output.SubHeader("Impact Analysis (% change)")
			rows := make([][]string, 0)
			for metric, val := range deltas {
				if v, ok := val.(float64); ok {
					marker := ""
					if v > 10 {
						marker = "▲"
					} else if v < -10 {
						marker = "▼"
					}
					rows = append(rows, []string{metric, fmt.Sprintf("%+.1f%%", v), marker})
				}
			}
			output.Table([]string{"Metric", "Change", ""}, rows)
		}

		// Pre/post summaries
		showSummary := func(label string, key string) {
			if s, ok := result[key].(map[string]interface{}); ok {
				output.SubHeader(label)
				output.KeyValue("Throughput (rec/s)", fmt.Sprintf("%.1f", numVal(s, "avgThroughputRecPerSec")))
				output.KeyValue("P99 Latency (ms)", fmt.Sprintf("%.2f", numVal(s, "p99LatencyMs")))
				output.KeyValue("Error Rate", fmt.Sprintf("%.4f%%", numVal(s, "errorRate")*100))
			}
		}
		showSummary("Pre-Chaos Baseline", "preChaosSummary")
		showSummary("Post-Chaos Impact", "postChaosSummary")

		return nil
	},
}

func init() {
	resilienceRunCmd.Flags().StringVar(&resilienceFile, "config", "", "Path to resilience test JSON config (required)")

	resilienceCmd.AddCommand(resilienceRunCmd)
	rootCmd.AddCommand(resilienceCmd)
}
