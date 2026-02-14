package cmd

import (
	"context"
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
		tick := 0

		for {
			fmt.Print("\033[2J\033[H")

			health, _ := apiClient.Health(context.Background())
			paged, err := apiClient.ListTests(context.Background(), "", "", 0, 50)
			if err != nil {
				output.Error("Failed to fetch tests: " + err.Error())
				time.Sleep(time.Duration(topInterval) * time.Second)
				tick++
				continue
			}

			apiStatus := "DOWN"
			kafkaStatus := "DOWN"
			if health != nil {
				apiStatus = health.Status
				if health.Kafka != nil {
					kafkaStatus = health.Kafka.Status
				}
			}

			counts := CountStatuses(paged.Content)

			fmt.Printf("  %s API: %s  Kafka: %s  │  %s running  %s pending  %s done  %s failed  │  %s total\n\n",
				output.AccentStyle.Bold(true).Render("KATES TOP"),
				output.StatusBadge(apiStatus),
				output.StatusBadge(kafkaStatus),
				output.AccentStyle.Render(fmt.Sprintf("%d", counts.Running)),
				output.WarningStyle.Render(fmt.Sprintf("%d", counts.Pending)),
				output.SuccessStyle.Render(fmt.Sprintf("%d", counts.Done)),
				coloredCount(counts.Failed),
				output.DimStyle.Render(fmt.Sprintf("%d", paged.TotalItems)),
			)

			activeRows := make([][]string, 0)
			recentRows := make([][]string, 0)
			for _, t := range paged.Content {
				status := strings.ToUpper(t.Status)
				row := []string{
					truncID(t.ID),
					t.TestType,
					status,
					t.Backend,
					formatTime(t.CreatedAt),
				}

				if status == "RUNNING" || status == "PENDING" {
					throughput := ""
					latency := ""
					records := ""
					if len(t.Results) > 0 {
						r := t.Results[len(t.Results)-1]
						throughput = fmtNum(r.ThroughputRecordsPerSec)
						latency = fmtFloat(r.P99LatencyMs, 1)
						records = fmtNum(r.RecordsSent)
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
				spinnerFrame(tick),
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
