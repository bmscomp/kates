package cmd

import (
	"fmt"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

// statusResult is what kates status prints under -o json. Kafka is "UP" or
// "DOWN"; the run counts come from the first 100 runs. Error says why the API
// could not be reached, with Status "unreachable".
type statusResult struct {
	Context      string `json:"context"`
	URL          string `json:"url"`
	Status       string `json:"status"`
	Kafka        string `json:"kafka,omitempty"`
	Configs      int    `json:"configs"`
	RunningTests int    `json:"runningTests"`
	DoneTests    int    `json:"doneTests"`
	FailedTests  int    `json:"failedTests"`
	Error        string `json:"error,omitempty"`
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Quick one-line status of Kates and running tests",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := commandContext(cmd)
		jsonOut := outputMode == "json"
		// The URL the requests go to: --url, KATES_URL or the port fallback
		// may have moved it off the context's own.
		url := apiURL
		if url == "" {
			url = apiClient.BaseURL
		}
		res := statusResult{Context: activeContext, URL: url}

		var health *client.HealthResponse
		var paged *client.PagedTests

		g, gctx := errgroup.WithContext(ctx)

		g.Go(func() error {
			var err error
			health, err = apiClient.Health(gctx)
			return err
		})

		g.Go(func() error {
			var err error
			paged, err = apiClient.ListTests(gctx, "", "", 0, 100)
			return err
		})

		if err := g.Wait(); err != nil || health == nil {
			// An API that cannot be reached is a failure: this used to exit 0,
			// so a script checking the status never saw it.
			res.Status = "unreachable"
			if err != nil {
				res.Error = err.Error()
			}
			if jsonOut {
				output.JSON(res)
				return &exitError{code: 1}
			}
			fmt.Fprintf(output.Out, "  %s %s │ %s │ %s\n",
				output.ErrorStyle.Render("✖"),
				output.LightStyle.Render(res.Context),
				output.DimStyle.Render(res.URL),
				output.ErrorStyle.Render("unreachable"),
			)
			return cmdErr("Kates API unreachable at " + res.URL + ": " + res.Error)
		}

		res.Status = health.Status

		brokerCount := "?"
		res.Kafka = "DOWN"
		if health.Kafka != nil && health.Kafka.Status == "UP" {
			brokerCount = "✓"
			res.Kafka = "UP"
		}

		if paged != nil {
			counts := CountStatuses(paged.Content)
			res.RunningTests = counts.Running + counts.Pending
			res.DoneTests = counts.Done
			res.FailedTests = counts.Failed
		}

		res.Configs = len(health.Tests)

		output.Render(jsonOut, res, func() {
			fmt.Fprintf(output.Out, "  %s %s │ %s │ %s │ Kafka %s │ %d configs │ %s running │ %s done │ %s failed\n",
				output.SuccessStyle.Render("✓"),
				output.LightStyle.Bold(true).Render(res.Context),
				output.DimStyle.Render(res.URL),
				output.StatusBadge(res.Status),
				output.DimStyle.Render(brokerCount),
				res.Configs,
				output.AccentStyle.Render(fmt.Sprintf("%d", res.RunningTests)),
				output.SuccessStyle.Render(fmt.Sprintf("%d", res.DoneTests)),
				coloredCount(res.FailedTests),
			)
		})

		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
