package cmd

import (
	"context"
	"fmt"

	"github.com/klster/kates-cli/client"
	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var replayWait bool

var replayCmd = &cobra.Command{
	Use:     "replay <id>",
	Short:   "Re-run a previous test with the same parameters",
	Example: "  kates replay 69acdf31 --wait",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		original, err := apiClient.GetTest(ctx, args[0])
		if err != nil {
			return cmdErr("Test not found: " + err.Error())
		}

		req := &client.CreateTestRequest{
			TestType: original.TestType,
			Backend:  original.Backend,
			Spec:     original.Spec,
		}

		output.Hint(fmt.Sprintf("Replaying %s test %s...", original.TestType, truncID(original.ID)))
		result, err := apiClient.CreateTest(ctx, req)
		if err != nil {
			return cmdErr("Failed to create test: " + err.Error())
		}

		output.Success(fmt.Sprintf("Created %s → %s", truncID(original.ID), truncID(result.ID)))
		output.KeyValue("ID", result.ID)
		output.KeyValue("Type", result.TestType)
		if !replayWait {
			output.KeyValue("Status", output.StatusBadge(result.Status))
		}

		if replayWait {
			fmt.Println()
			pollUntilDone(result.ID)
		} else {
			output.Hint("Track progress: kates test watch " + result.ID)
		}
		return nil
	},
}

func init() {
	replayCmd.Flags().BoolVar(&replayWait, "wait", false, "Wait for test to complete")
	rootCmd.AddCommand(replayCmd)
}
