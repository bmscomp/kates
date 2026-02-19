package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var scenarioDiffCmd = &cobra.Command{
	Use:     "scenario-diff <scenario.yaml> <test-id>",
	Aliases: []string{"sdiff"},
	Short:   "Compare a scenario YAML against a completed test run to detect config drift",
	Example: `  kates scenario-diff scenario.yaml 69acdf31`,
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		scenarioPath := args[0]
		testID := args[1]

		data, err := os.ReadFile(scenarioPath)
		if err != nil {
			return cmdErr("Failed to read scenario file: " + err.Error())
		}

		var scenario map[string]interface{}
		if err := yaml.Unmarshal(data, &scenario); err != nil {
			return cmdErr("Failed to parse YAML: " + err.Error())
		}

		ctx := context.Background()
		run, err := apiClient.GetTest(ctx, testID)
		if err != nil {
			return cmdErr("Test not found: " + err.Error())
		}

		output.Banner("Scenario Diff", fmt.Sprintf("%s vs %s", scenarioPath, truncID(testID)))
		fmt.Println()

		diffs := 0

		scenarioType, _ := scenario["type"].(string)
		if scenarioType != "" && !strings.EqualFold(scenarioType, run.TestType) {
			printScenarioDiff("Type", scenarioType, run.TestType)
			diffs++
		}

		scenarioBackend, _ := scenario["backend"].(string)
		if scenarioBackend != "" && !strings.EqualFold(scenarioBackend, run.Backend) {
			printScenarioDiff("Backend", scenarioBackend, run.Backend)
			diffs++
		}

		if spec, ok := scenario["spec"].(map[string]interface{}); ok && run.Spec != nil {
			diffs += diffSpecField(spec, "numRecords", run.Spec.Records, "Records")
			diffs += diffSpecField(spec, "numProducers", run.Spec.ParallelProducers, "Producers")
			diffs += diffSpecField(spec, "recordSize", run.Spec.RecordSizeBytes, "Record Size")
			diffs += diffSpecField(spec, "durationMs", run.Spec.DurationSeconds, "Duration (ms)")
			diffs += diffSpecField(spec, "partitions", run.Spec.Partitions, "Partitions")
			diffs += diffSpecField(spec, "replicationFactor", run.Spec.ReplicationFactor, "Replication Factor")
			diffs += diffSpecField(spec, "batchSize", run.Spec.BatchSize, "Batch Size")
			diffs += diffSpecField(spec, "lingerMs", run.Spec.LingerMs, "Linger Ms")

			if compressionType, ok := spec["compressionType"].(string); ok && compressionType != "" {
				if !strings.EqualFold(compressionType, run.Spec.CompressionType) {
					printScenarioDiff("Compression", compressionType, run.Spec.CompressionType)
					diffs++
				}
			}
			if acks, ok := spec["acks"].(string); ok && acks != "" {
				if !strings.EqualFold(acks, run.Spec.Acks) {
					printScenarioDiff("Acks", acks, run.Spec.Acks)
					diffs++
				}
			}
		}

		fmt.Println()
		if diffs == 0 {
			output.Success("No configuration drift — scenario matches test run.")
		} else {
			output.Warn(fmt.Sprintf("⚠ %d configuration difference(s) detected.", diffs))
		}

		return nil
	},
}

func printScenarioDiff(field, expected, actual string) {
	fmt.Printf("  %s\n", output.AccentStyle.Render(field+":"))
	fmt.Printf("    %s %s\n", output.ErrorStyle.Render("- scenario:"), expected)
	fmt.Printf("    %s %s\n", output.SuccessStyle.Render("+ actual:  "), actual)
}

func diffSpecField(spec map[string]interface{}, key string, actual int, label string) int {
	val, ok := spec[key]
	if !ok {
		return 0
	}

	var expected int
	switch v := val.(type) {
	case int:
		expected = v
	case float64:
		expected = int(v)
	default:
		return 0
	}

	if expected == 0 || expected == actual {
		return 0
	}

	printScenarioDiff(label, fmt.Sprintf("%d", expected), fmt.Sprintf("%d", actual))
	return 1
}

func init() {
	rootCmd.AddCommand(scenarioDiffCmd)
}
