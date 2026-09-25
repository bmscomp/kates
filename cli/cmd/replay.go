package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
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
		// MCPRun keeps both specs as the backend sent them.
		original, err := apiClient.MCPRun(ctx, args[0])
		if err != nil {
			return cmdErr("Test not found: " + err.Error())
		}
		spec, err := replaySpec(original)
		if err != nil {
			return cmdErr("Cannot read the spec of test " + truncID(original.ID) + ": " + err.Error())
		}

		req := &client.RerunTestRequest{
			TestType: original.TestType,
			Backend:  original.Backend,
			Spec:     spec,
		}

		output.Hint(fmt.Sprintf("Replaying %s test %s...", original.TestType, truncID(original.ID)))
		result, err := apiClient.RerunTest(ctx, req)
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
			status, err := pollUntilDone(result.ID)
			if err != nil {
				return cmdErr("Lost track of test " + truncID(result.ID) + ": " + err.Error())
			}
			if isFailedStatus(status) {
				return cmdErr("test " + truncID(result.ID) + " finished " + status +
					" — details: kates test get " + result.ID)
			}
		} else {
			output.Hint("Track progress: kates test watch " + result.ID)
		}
		return nil
	},
}

// replayNeverUsed are the spec fields a backend that did not keep the request
// accepted and dropped when it merged the spec; it stored them at their Java
// defaults whatever the request said.
var replayNeverUsed = []string{
	"consumerGroup", "targetThroughput", "fetchMinBytes", "fetchMaxWaitMs",
	"enableIdempotence", "enableTransactions", "enableCrc",
}

// replaySpec is the spec a replay sends: the request the run was started
// with, which the backend merges with the type's defaults as it did the first
// time. The merged spec is not a request, and used to be sent as one: it asked
// for what the defaults had filled in, including fields the type cannot use,
// which the backend refuses. A run stored before the backend kept the request
// has only the merged spec; it is sent without the fields that backend never
// used, and so asks for what the run did.
func replaySpec(run *client.MCPRun) (json.RawMessage, error) {
	if requested := bytes.TrimSpace(run.RequestedSpec); len(requested) > 0 && !bytes.Equal(requested, []byte("null")) {
		return requested, nil
	}
	merged := bytes.TrimSpace(run.Spec)
	if len(merged) == 0 || bytes.Equal(merged, []byte("null")) {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(merged, &fields); err != nil {
		return nil, err
	}
	for _, name := range replayNeverUsed {
		delete(fields, name)
	}
	return json.Marshal(fields)
}

func init() {
	replayCmd.Flags().BoolVar(&replayWait, "wait", false, "Wait for test to complete")
	rootCmd.AddCommand(replayCmd)
}
