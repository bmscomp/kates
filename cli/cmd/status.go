package cmd

import (
	"context"
	"fmt"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Quick one-line status of KATES and running tests",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := loadConfig()
		ctxName := cfg.CurrentContext
		ctx := activeContext(cfg)

		// Health check
		health, err := apiClient.Health(context.Background())
		if err != nil {
			fmt.Printf("  %s %s │ %s │ %s\n",
				output.ErrorStyle.Render("✖"),
				output.LightStyle.Render(ctxName),
				output.DimStyle.Render(ctx.URL),
				output.ErrorStyle.Render("unreachable"),
			)
			return nil
		}

		status := mapStrEmpty(health, "status")

		// Count brokers
		brokerCount := "?"
		if bc := mapStrEmpty(health, "brokerCount"); bc != "" && bc != "0" {
			brokerCount = bc
		}
		if kafka, ok := health["kafka"].(map[string]interface{}); ok {
			if mapStrEmpty(kafka, "status") == "UP" {
				brokerCount = "✓"
			}
		}

		// Count running tests
		running := 0
		done := 0
		failed := 0
		data, err := apiClient.ListTests(context.Background(), "", "", 0, 100)
		if err == nil {
			paged, parseErr := ParsePaged(data)
			if parseErr == nil {
				counts := CountStatuses(paged.Content)
				running = counts.Running + counts.Pending
				done = counts.Done
				failed = counts.Failed
			}
		}

		// Count test configs
		configCount := 0
		if tests, ok := health["tests"].(map[string]interface{}); ok {
			configCount = len(tests)
		}

		fmt.Printf("  %s %s │ %s │ Kafka %s │ %d configs │ %s running │ %s done │ %s failed\n",
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
