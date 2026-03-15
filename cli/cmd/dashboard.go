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
			if panelW < 38 {
				panelW = 38
			}

			titleBar := lipgloss.NewStyle().
				Bold(true).
				Foreground(output.HeaderColor).
				Background(output.Surface).
				Width(w).
				Padding(0, 1).
				Render(fmt.Sprintf(
					"  Kates Dashboard  %s  %s",
					output.DimStyle.Render("│"),
					time.Now().Format("15:04:05"),
				))
			fmt.Println(titleBar)
			fmt.Println()

			healthContent := &strings.Builder{}
			if healthErr != nil {
				healthContent.WriteString(output.ErrorStyle.Render("  ✖ API unreachable"))
			} else {
				healthContent.WriteString(fmt.Sprintf("  %-10s %s\n",
					output.DimStyle.Render("API"),
					output.StatusBadge(health.Status)))
				if kafka := health.Kafka; kafka != nil {
					healthContent.WriteString(fmt.Sprintf("  %-10s %s\n",
						output.DimStyle.Render("Kafka"),
						output.StatusBadge(kafka.Status)))
					healthContent.WriteString(fmt.Sprintf("  %-10s %s\n",
						output.DimStyle.Render("Brokers"),
						output.LightStyle.Render(kafka.BootstrapServers)))
				}
				if eng := health.Engine; eng != nil {
					healthContent.WriteString(fmt.Sprintf("  %-10s %s\n",
						output.DimStyle.Render("Engine"),
						output.AccentStyle.Render(eng.ActiveBackend)))
				}
				healthContent.WriteString(fmt.Sprintf("  %-10s %s",
					output.DimStyle.Render("Configs"),
					output.LightStyle.Render(fmt.Sprintf("%d test configs loaded", len(health.Tests)))))
			}

			var totalItems int
			var counts TestCounts
			if paged != nil {
				totalItems = paged.TotalItems
				counts = CountStatuses(paged.Content)
			}

			summaryContent := &strings.Builder{}
			summaryContent.WriteString(fmt.Sprintf("  %s %-11s %s %s\n",
				output.AccentStyle.Bold(true).Render(fmt.Sprintf("%3d", counts.Running)),
				output.LightStyle.Render("Running"),
				output.WarningStyle.Render(fmt.Sprintf("%3d", counts.Pending)),
				output.LightStyle.Render("Pending"),
			))
			summaryContent.WriteString(fmt.Sprintf("  %s %-11s %s %s\n",
				output.SuccessStyle.Render(fmt.Sprintf("%3d", counts.Done)),
				output.LightStyle.Render("Done"),
				coloredCount(counts.Failed),
				output.LightStyle.Render("Failed"),
			))
			summaryContent.WriteString(fmt.Sprintf("  %s\n",
				output.DimStyle.Render(strings.Repeat("·", panelW-6)),
			))
			summaryContent.WriteString(fmt.Sprintf("  %s %s",
				output.LightStyle.Bold(true).Render(fmt.Sprintf("%3d", totalItems)),
				output.DimStyle.Render("Total tests"),
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

					line := fmt.Sprintf("  %s %-12s %s",
						output.AccentStyle.Render("◉"),
						output.LightStyle.Bold(true).Render(t.TestType),
						output.DimStyle.Render(truncID(t.ID)),
					)
					if len(t.Results) > 0 {
						r := t.Results[len(t.Results)-1]
						latestThroughput += r.ThroughputRecordsPerSec
						latestLatency = r.P99LatencyMs
						line += fmt.Sprintf("  │ %s rec  %s  %s p99",
							output.LightStyle.Render(fmtNum(r.RecordsSent)),
							output.SuccessStyle.Render(fmtNum(r.ThroughputRecordsPerSec)+" rec/s"),
							output.WarningStyle.Render(fmtFloat(r.P99LatencyMs, 1)+" ms"),
						)
					}
					activeContent.WriteString(line + "\n")
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
					icon := output.SuccessStyle.Render("✓")
					if status == "FAILED" || status == "ERROR" {
						icon = output.ErrorStyle.Render("✖")
					}
					recentContent.WriteString(fmt.Sprintf("  %s %-12s %s  %s  %s\n",
						icon,
						output.LightStyle.Render(t.TestType),
						output.DimStyle.Render(truncID(t.ID)),
						output.StatusBadge(status),
						output.DimStyle.Render(formatTime(t.CreatedAt)),
					))
				}
			}
			if recentCount == 0 {
				recentContent.WriteString(output.DimStyle.Render("  No completed tests"))
			}

			activePanel := output.Panel("Active Tests", activeContent.String(), panelW)
			recentPanel := output.Panel("Recent Completed", recentContent.String(), panelW)
			row2 := lipgloss.JoinHorizontal(lipgloss.Top, activePanel, "  ", recentPanel)
			fmt.Println(row2)
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
				row3 := lipgloss.JoinHorizontal(lipgloss.Top, sp1, "  ", sp2)
				fmt.Println(row3)
				fmt.Println()
			}

			clusterContent := &strings.Builder{}
			check, checkErr := apiClient.ClusterCheck(context.Background())
			if checkErr == nil && check != nil {
				clusterContent.WriteString(fmt.Sprintf("  %-16s %s\n",
					output.DimStyle.Render("Brokers"),
					output.LightStyle.Render(fmt.Sprintf("%d", check.Brokers))))
				clusterContent.WriteString(fmt.Sprintf("  %-16s %s\n",
					output.DimStyle.Render("Topics"),
					output.LightStyle.Render(fmt.Sprintf("%d", check.Topics))))
				isrIcon := output.SuccessStyle.Render("✓")
				if check.PartitionHealth.UnderReplicated > 0 {
					isrIcon = output.ErrorStyle.Render("✖")
				}
				clusterContent.WriteString(fmt.Sprintf("  %-16s %s %s\n",
					output.DimStyle.Render("ISR Health"),
					isrIcon,
					output.DimStyle.Render(fmt.Sprintf("%d under-replicated", check.PartitionHealth.UnderReplicated))))
				clusterContent.WriteString(fmt.Sprintf("  %-16s %s",
					output.DimStyle.Render("Controller"),
					output.AccentStyle.Render(fmt.Sprintf("broker-%d", check.ControllerID))))
			} else {
				clusterContent.WriteString(output.DimStyle.Render("  Cluster data unavailable"))
			}

			quickContent := &strings.Builder{}
			quickContent.WriteString(fmt.Sprintf("  %s  kates test create --type LOAD\n", output.DimStyle.Render("▸")))
			quickContent.WriteString(fmt.Sprintf("  %s  kates benchmark\n", output.DimStyle.Render("▸")))
			quickContent.WriteString(fmt.Sprintf("  %s  kates gate --min-grade B\n", output.DimStyle.Render("▸")))
			quickContent.WriteString(fmt.Sprintf("  %s  kates test cleanup", output.DimStyle.Render("▸")))

			cp := output.Panel("Cluster Detail", clusterContent.String(), panelW)
			qp := output.Panel("Quick Commands", quickContent.String(), panelW)
			row4 := lipgloss.JoinHorizontal(lipgloss.Top, cp, "  ", qp)
			fmt.Println(row4)
			fmt.Println()

			fmt.Printf("  %s Refreshing every %ds... (Ctrl+C to stop)\n",
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
