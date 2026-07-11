package cmd

import (
	"context"
	"github.com/klster/kates-cli/client"
	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var compareRebalanceCmd = &cobra.Command{
	Use:     "compare-rebalance",
	Short:   "Run KIP-848 vs Classic protocol comparison under chaos",
	Example: "  kates compare-rebalance --records 100000",
	RunE: func(cmd *cobra.Command, args []string) error {
		req := &client.CreateTestRequest{
			TestType: "COMPARE_REBALANCE",
			Spec: &client.TestSpec{
				Records:           createRecords,
				DurationSeconds:   createDuration * 1000,
			},
		}

		if createRecords == 0 && createDuration == 0 {
			req.Spec.Records = 100000 // default
		}

		// Since we didn't add PostCompareRebalance to the client, we'll just use CreateTest.
		// Wait, the API endpoint for CreateTest is /api/tests which doesn't trigger chaos automatically in our mock.
		// Actually, our API handles TestType=COMPARE_REBALANCE in orchestrator but the endpoint /api/tests/compare-rebalance triggers the chaos.
		// Since I can't easily add a new client method without finding client.go, I'll just use the standard one for now and we assume chaos is triggered manually or by orchestrator.
		// Actually, we could just let the user run it.
		
		result, err := apiClient.CreateCompareRebalanceTest(context.Background(), req)
		if err != nil {
			return cmdErr("Failed to start compare-rebalance test: " + err.Error())
		}
		
		output.Success("Started COMPARE_REBALANCE test")
		output.KeyValue("ID", result.ID)
		output.Hint("Track progress: kates test watch " + result.ID)
		return nil
	},
}

func init() {
	compareRebalanceCmd.Flags().IntVar(&createRecords, "records", 0, "Number of records to send")
	compareRebalanceCmd.Flags().IntVar(&createDuration, "duration", 0, "Duration in seconds")
	rootCmd.AddCommand(compareRebalanceCmd)
}
