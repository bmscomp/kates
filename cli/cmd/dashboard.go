package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var dashInterval int

var dashboardCmd = &cobra.Command{
	Use:     "dashboard",
	Aliases: []string{"dash"},
	Short:   "Full-screen monitoring dashboard with live metrics",
	RunE: func(cmd *cobra.Command, args []string) error {
		tick := 0
		throughputHistory := make([]float64, 0, 30)
		latencyHistory := make([]float64, 0, 30)

		for {
			fmt.Print("\033[2J\033[H")

			health, healthErr := apiClient.Health(context.Background())
			paged, _ := apiClient.ListTests(context.Background(), "", "", 0, 50)

			w := termWidth()
			panelW := (w / 2) - 2
			if panelW < 35 {
				panelW = 35
			}

			titleBar := lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#C4B5FD")).
				Background(lipgloss.Color("#1F2937")).
				Width(w).
				Padding(0, 1).
				Render(fmt.Sprintf(
					"  KATES Dashboard  %s  %s",
					output.DimStyle.Render("│"),
					time.Now().Format("15:04:05"),
				))
			fmt.Println(titleBar)
			fmt.Println()

			healthContent := &strings.Builder{}
			if healthErr != nil {
				healthContent.WriteString(output.ErrorStyle.Render("  API unreachable"))
			} else {
				healthContent.WriteString(fmt.Sprintf("  API     %s\n", output.StatusBadge(health.Status)))
				if kafka := health.Kafka; kafka != nil {
					healthContent.WriteString(fmt.Sprintf("  Kafka   %s\n", output.StatusBadge(kafka.Status)))
					healthContent.WriteString(fmt.Sprintf("  Server  %s", output.DimStyle.Render(kafka.BootstrapServers)))
				}
				if eng := health.Engine; eng != nil {
					healthContent.WriteString(fmt.Sprintf("\n  Engine  %s", output.AccentStyle.Render(eng.ActiveBackend)))
				}
			}

			var totalItems int
			if paged != nil {
				totalItems = paged.TotalItems
			}

			summaryContent := &strings.Builder{}
			var counts TestCounts
			if paged != nil {
				counts = CountStatuses(paged.Content)
			}
			summaryContent.WriteString(fmt.Sprintf("  %s  Running   %s  Pending\n",
				output.AccentStyle.Bold(true).Render(fmt.Sprintf("%3d", counts.Running)),
				output.WarningStyle.Render(fmt.Sprintf("%3d", counts.Pending)),
			))
			summaryContent.WriteString(fmt.Sprintf("  %s  Done      %s  Failed\n",
				output.SuccessStyle.Render(fmt.Sprintf("%3d", counts.Done)),
				coloredCount(counts.Failed),
			))
			summaryContent.WriteString(fmt.Sprintf("  %s  Total",
				output.LightStyle.Render(fmt.Sprintf("%3d", totalItems)),
			))

			panel1 := output.Panel("System Health", healthContent.String(), panelW)
			panel2 := output.Panel("Test Summary", summaryContent.String(), panelW)
			row1 := lipgloss.JoinHorizontal(lipgloss.Top, panel1, "  ", panel2)
			fmt.Println(row1)
			fmt.Println()

			activeContent := &strings.Builder{}
			activeCount := 0
			var latestThroughput, latestLatency float64
			if paged != nil {
				for _, t := range paged.Content {
					status := strings.ToUpper(t.Status)
					if status != "RUNNING" && status != "PENDING" {
						continue
					}
					activeCount++

					activeContent.WriteString(fmt.Sprintf("  %s %s %s",
						output.AccentStyle.Render("◉"),
						output.LightStyle.Bold(true).Render(t.TestType),
						output.DimStyle.Render(truncID(t.ID)),
					))
					if len(t.Results) > 0 {
						r := t.Results[len(t.Results)-1]
						latestThroughput += r.ThroughputRecordsPerSec
						latestLatency = r.P99LatencyMs
						activeContent.WriteString(fmt.Sprintf("  │  %s rec  %s  %s p99",
							output.LightStyle.Render(fmtNum(r.RecordsSent)),
							output.SuccessStyle.Render(fmtNum(r.ThroughputRecordsPerSec)+" rec/s"),
							output.WarningStyle.Render(fmtFloat(r.P99LatencyMs, 1)+" ms"),
						))
					}
					activeContent.WriteString("\n")
				}
			}
			if activeCount == 0 {
				activeContent.WriteString(output.DimStyle.Render("  No active tests"))
			}

			if latestThroughput > 0 {
				throughputHistory = append(throughputHistory, latestThroughput)
			}
			if latestLatency > 0 {
				latencyHistory = append(latencyHistory, latestLatency)
			}
			if len(throughputHistory) > 30 {
				throughputHistory = throughputHistory[len(throughputHistory)-30:]
			}
			if len(latencyHistory) > 30 {
				latencyHistory = latencyHistory[len(latencyHistory)-30:]
			}

			activePanel := output.Panel("Active Tests", activeContent.String(), w-4)
			fmt.Println(activePanel)
			fmt.Println()

			if len(throughputHistory) > 1 || len(latencyHistory) > 1 {
				sparkContent1 := &strings.Builder{}
				if len(throughputHistory) > 1 {
					sparkContent1.WriteString(fmt.Sprintf("  %s\n", output.SparklineColored(throughputHistory, true)))
					latest := throughputHistory[len(throughputHistory)-1]
					sparkContent1.WriteString(fmt.Sprintf("  Current: %s rec/s", output.SuccessStyle.Render(fmtNum(latest))))
				} else {
					sparkContent1.WriteString(output.DimStyle.Render("  Collecting data..."))
				}

				sparkContent2 := &strings.Builder{}
				if len(latencyHistory) > 1 {
					sparkContent2.WriteString(fmt.Sprintf("  %s\n", output.SparklineColored(latencyHistory, false)))
					latest := latencyHistory[len(latencyHistory)-1]
					sparkContent2.WriteString(fmt.Sprintf("  Current: %s ms", output.WarningStyle.Render(fmtFloat(latest, 1))))
				} else {
					sparkContent2.WriteString(output.DimStyle.Render("  Collecting data..."))
				}

				sp1 := output.Panel("Throughput ↗", sparkContent1.String(), panelW)
				sp2 := output.Panel("P99 Latency ↘", sparkContent2.String(), panelW)
				row2 := lipgloss.JoinHorizontal(lipgloss.Top, sp1, "  ", sp2)
				fmt.Println(row2)
				fmt.Println()
			}

			recentContent := &strings.Builder{}
			recentCount := 0
			if paged != nil {
				for _, t := range paged.Content {
					status := strings.ToUpper(t.Status)
					if status == "RUNNING" || status == "PENDING" {
						continue
					}
					if recentCount >= 5 {
						break
					}
					recentCount++
					emoji := output.SuccessStyle.Render("✓")
					if status == "FAILED" || status == "ERROR" {
						emoji = output.ErrorStyle.Render("✖")
					}
					recentContent.WriteString(fmt.Sprintf("  %s %s %s %s  %s\n",
						emoji,
						output.LightStyle.Render(padLeftN(t.TestType, 10)),
						output.DimStyle.Render(truncID(t.ID)),
						output.StatusBadge(status),
						output.DimStyle.Render(formatTime(t.CreatedAt)),
					))
				}
			}
			if recentCount == 0 {
				recentContent.WriteString(output.DimStyle.Render("  No completed tests"))
			}
			recentPanel := output.Panel("Recent Completed", recentContent.String(), w-4)
			fmt.Println(recentPanel)

			fmt.Printf("\n  %s Refreshing every %ds... (Ctrl+C to stop)\n",
				spinnerFrame(tick),
				dashInterval,
			)

			tick++
			time.Sleep(time.Duration(dashInterval) * time.Second)
		}
	},
}

func init() {
	dashboardCmd.Flags().IntVar(&dashInterval, "interval", 3, "Refresh interval in seconds")
	rootCmd.AddCommand(dashboardCmd)
}
