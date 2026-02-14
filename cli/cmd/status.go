package cmd

import (
	"encoding/json"
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
		health, err := apiClient.Health()
		if err != nil {
			fmt.Printf("  %s %s │ %s │ %s\n",
				output.ErrorStyle.Render("✖"),
				output.LightStyle.Render(ctxName),
				output.DimStyle.Render(ctx.URL),
				output.ErrorStyle.Render("unreachable"),
			)
			return nil
		}

		status := strVal(health, "status")

		// Count brokers
		brokerCount := "?"
		if bc := strVal(health, "brokerCount"); bc != "" && bc != "0" {
			brokerCount = bc
		}
		if kafka, ok := health["kafka"].(map[string]interface{}); ok {
			if strVal(kafka, "status") == "UP" {
				brokerCount = "✓"
			}
		}

		// Count running tests
		running := 0
		done := 0
		failed := 0
		data, err := apiClient.ListTests("", "", 0, 100)
		if err == nil {
			var paged struct {
				Content []map[string]interface{} `json:"content"`
			}
			if json.Unmarshal(data, &paged) == nil {
				for _, t := range paged.Content {
					switch valStr(t, "status") {
					case "RUNNING", "PENDING":
						running++
					case "DONE", "COMPLETED":
						done++
					case "FAILED", "ERROR":
						failed++
					}
				}
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

func coloredCount(n int) string {
	if n > 0 {
		return output.ErrorStyle.Render(fmt.Sprintf("%d", n))
	}
	return output.DimStyle.Render("0")
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
