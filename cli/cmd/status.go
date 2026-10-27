package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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

// reachable reports whether the API answered.
func (r statusResult) reachable() bool { return r.Status != "unreachable" }

var (
	statusWatch    bool
	statusInterval int
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Quick one-line status of Kates and running tests",
	Example: `  kates status
  kates status --watch --interval 5
  kates status --watch -o json | jq -c .`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := commandContext(cmd)
		if statusWatch {
			if err := checkInterval(statusInterval); err != nil {
				return couldNotTell(err.Error())
			}
			return watchStatus(ctx, time.Duration(statusInterval)*time.Second, IsInteractive())
		}

		res := fetchStatus(ctx)
		if !res.reachable() && ctx.Err() != nil {
			return errInterrupted
		}
		if outputMode == "json" {
			output.JSON(res)
		} else {
			fmt.Fprintln(output.Out, statusLine(res))
		}
		if !res.reachable() {
			// An API that cannot be reached is a failure: this used to exit 0,
			// so a script checking the status never saw it.
			if outputMode == "json" {
				return &exitError{code: 1}
			}
			return cmdErr("Kates API unreachable at " + res.URL + ": " + res.Error)
		}
		return nil
	},
}

// fetchStatus asks the API for its health and the first 100 runs.
func fetchStatus(ctx context.Context) statusResult {
	// The URL the requests go to: --url, KATES_URL or the port fallback may
	// have moved it off the context's own.
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
		res.Status = "unreachable"
		if err != nil {
			res.Error = err.Error()
		}
		return res
	}

	res.Status = health.Status
	res.Kafka = "DOWN"
	if health.Kafka != nil && health.Kafka.Status == "UP" {
		res.Kafka = "UP"
	}
	if paged != nil {
		counts := CountStatuses(paged.Content)
		res.RunningTests = counts.Running + counts.Pending
		res.DoneTests = counts.Done
		res.FailedTests = counts.Failed
	}
	res.Configs = len(health.Tests)
	return res
}

// statusLine is the one-line form of res, without a newline.
func statusLine(res statusResult) string {
	if !res.reachable() {
		return fmt.Sprintf("  %s %s │ %s │ %s",
			output.ErrorStyle.Render("✖"),
			output.LightStyle.Render(res.Context),
			output.DimStyle.Render(res.URL),
			output.ErrorStyle.Render("unreachable"),
		)
	}
	brokerCount := "?"
	if res.Kafka == "UP" {
		brokerCount = "✓"
	}
	return fmt.Sprintf("  %s %s │ %s │ %s │ Kafka %s │ %d configs │ %s running │ %s done │ %s failed",
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
}

// watchStatus polls the status every interval until ctx ends, which Ctrl-C
// does: stopping a watch is what the user asked for, so it returns nil and
// kates exits 0, whatever the last status was.
//
// In a terminal (redraw) it redraws the line on a cleared screen each poll.
// Anywhere else it prints a line only when the status changed, so a log
// holds the changes and nothing else: the time and the one-line form, or
// under -o json one compact object per line (NDJSON). An API that cannot be
// reached is a status like any other here, not the end of the watch.
func watchStatus(ctx context.Context, interval time.Duration, redraw bool) error {
	jsonOut := outputMode == "json"
	var last statusResult
	for first := true; ; first = false {
		res := fetchStatus(ctx)
		if ctx.Err() != nil {
			return nil
		}
		switch {
		case jsonOut:
			if first || res != last {
				data, err := json.Marshal(res)
				if err != nil {
					return err
				}
				fmt.Fprintln(output.Out, string(data))
			}
		case redraw:
			output.ClearFrame(true)
			fmt.Fprintln(output.Out, statusLine(res))
			fmt.Fprintln(output.Out, output.DimStyle.Render(fmt.Sprintf("\n  Every %s · %s · Ctrl-C to stop",
				interval, time.Now().Format("15:04:05"))))
		case first || res != last:
			fmt.Fprintln(output.Out, output.DimStyle.Render(time.Now().Format("15:04:05"))+statusLine(res))
		}
		last = res
		if !sleepCtx(ctx, interval) {
			return nil
		}
	}
}

func init() {
	statusCmd.Flags().BoolVarP(&statusWatch, "watch", "w", false, "Keep polling and show the status as it changes; Ctrl-C stops")
	statusCmd.Flags().IntVar(&statusInterval, "interval", 5, "Seconds between polls under --watch")
	// The watch stops on Ctrl-C through its context, and exits 0.
	interruptible(statusCmd)
	rootCmd.AddCommand(statusCmd)
}
