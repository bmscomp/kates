package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var topInterval int

var topCmd = &cobra.Command{
	Use:   "top",
	Short: "Live view of running tests (like kubectl top)",
	RunE: func(cmd *cobra.Command, args []string) error {
		spinner := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		tick := 0

		for {
			fmt.Print("\033[2J\033[H")

			health, _ := apiClient.Health()
			data, err := apiClient.ListTests("", "", 0, 50)
			if err != nil {
				output.Error("Failed to fetch tests: " + err.Error())
				time.Sleep(time.Duration(topInterval) * time.Second)
				tick++
				continue
			}

			var paged struct {
				Content    []map[string]interface{} `json:"content"`
				TotalItems int                      `json:"totalItems"`
			}
			json.Unmarshal(data, &paged)

			// Status bar
			apiStatus := "DOWN"
			kafkaStatus := "DOWN"
			if health != nil {
				apiStatus = strVal(health, "status")
				if k, ok := health["kafka"].(map[string]interface{}); ok {
					kafkaStatus = strVal(k, "status")
				}
			}

			running := 0
			pending := 0
			done := 0
			failed := 0
			for _, t := range paged.Content {
				switch strings.ToUpper(valStr(t, "status")) {
				case "RUNNING":
					running++
				case "PENDING":
					pending++
				case "DONE", "COMPLETED":
					done++
				case "FAILED", "ERROR":
					failed++
				}
			}

			fmt.Printf("  %s API: %s  Kafka: %s  │  %s running  %s pending  %s done  %s failed  │  %s total\n\n",
				output.AccentStyle.Bold(true).Render("KATES TOP"),
				output.StatusBadge(apiStatus),
				output.StatusBadge(kafkaStatus),
				output.AccentStyle.Render(fmt.Sprintf("%d", running)),
				output.WarningStyle.Render(fmt.Sprintf("%d", pending)),
				output.SuccessStyle.Render(fmt.Sprintf("%d", done)),
				coloredCount(failed),
				output.DimStyle.Render(fmt.Sprintf("%d", paged.TotalItems)),
			)

			// Active tests (running + pending)
			activeRows := make([][]string, 0)
			recentRows := make([][]string, 0)
			for _, t := range paged.Content {
				status := strings.ToUpper(valStr(t, "status"))
				row := []string{
					truncID(valStr(t, "id")),
					valStr(t, "testType"),
					status,
					valStr(t, "backend"),
					formatTime(valStr(t, "createdAt")),
				}

				if status == "RUNNING" || status == "PENDING" {
					// Try to get result metrics for running tests
					throughput := ""
					latency := ""
					records := ""
					if results, ok := t["results"].([]interface{}); ok && len(results) > 0 {
						if m, ok := results[len(results)-1].(map[string]interface{}); ok {
							throughput = fmtNum(numVal(m, "throughputRecordsPerSec"))
							latency = fmtFloat(numVal(m, "p99LatencyMs"), 1)
							records = fmtNum(numVal(m, "recordsSent"))
						}
					}
					activeRows = append(activeRows, append(row, records, throughput+" rec/s", latency+" ms"))
				} else {
					recentRows = append(recentRows, row)
				}
			}

			if len(activeRows) > 0 {
				fmt.Println(output.SubHeaderStyle.Render("  ◉ Active Tests"))
				fmt.Println()
				output.Table(
					[]string{"ID", "Type", "Status", "Backend", "Started", "Records", "Throughput", "P99 Lat."},
					activeRows,
				)
			} else {
				fmt.Println(output.DimStyle.Render("  No active tests running"))
				fmt.Println()
			}

			if len(recentRows) > 0 {
				limit := 10
				if len(recentRows) < limit {
					limit = len(recentRows)
				}
				fmt.Println(output.SubHeaderStyle.Render("  ▸ Recent Tests"))
				fmt.Println()
				output.Table(
					[]string{"ID", "Type", "Status", "Backend", "Created"},
					recentRows[:limit],
				)
			}

			fmt.Printf("  %s Refreshing every %ds... (Ctrl+C to stop)\n",
				output.AccentStyle.Render(spinner[tick%len(spinner)]),
				topInterval,
			)

			tick++
			time.Sleep(time.Duration(topInterval) * time.Second)
		}
	},
}

func init() {
	topCmd.Flags().IntVar(&topInterval, "interval", 3, "Refresh interval in seconds")
	rootCmd.AddCommand(topCmd)
}
