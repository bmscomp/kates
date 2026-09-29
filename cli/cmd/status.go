package cmd

import (
	"context"
	"fmt"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Quick one-line status of Kates and running tests",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := loadConfig()
		ctxName := cfg.CurrentContext
		ctx := activeContext(cfg)

		var health *client.HealthResponse
		var paged *client.PagedTests

		g, gctx := errgroup.WithContext(context.Background())

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
			fmt.Fprintf(output.Out, "  %s %s │ %s │ %s\n",
				output.ErrorStyle.Render("✖"),
				output.LightStyle.Render(ctxName),
				output.DimStyle.Render(ctx.URL),
				output.ErrorStyle.Render("unreachable"),
			)
			return nil
		}

		status := health.Status

		brokerCount := "?"
		if health.Kafka != nil && health.Kafka.Status == "UP" {
			brokerCount = "✓"
		}

		running := 0
		done := 0
		failed := 0
		if paged != nil {
			counts := CountStatuses(paged.Content)
			running = counts.Running + counts.Pending
			done = counts.Done
			failed = counts.Failed
		}

		configCount := len(health.Tests)

		fmt.Fprintf(output.Out, "  %s %s │ %s │ Kafka %s │ %d configs │ %s running │ %s done │ %s failed\n",
			output.SuccessStyle.Render("✓"),
			output.LightStyle.Bold(true).Render(ctxName),
			output.StatusBadge(status),
			output.DimStyle.Render(brokerCount),
			configCount,
			output.AccentStyle.Render(fmt.Sprintf("%d", running)),
			output.SuccessStyle.Render(fmt.Sprintf("%d", done)),
			coloredCount(failed),
		)

		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
