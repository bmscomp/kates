package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var dashInterval int

var dashboardCmd = &cobra.Command{
	Use:     "dashboard",
	Aliases: []string{"dash"},
	Short:   "Full-screen monitoring dashboard with live metrics",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkInterval(dashInterval); err != nil {
			return err
		}
		tick := 0
		throughputHistory := make([]float64, 0, 30)
		latencyHistory := make([]float64, 0, 30)

		for {
			output.ClearFrame(IsInteractive())

			health, healthErr := apiClient.Health(context.Background())
			paged, listErr := apiClient.ListTests(context.Background(), "", "", 0, 50)

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

			summaryContent := dashSummaryContent(paged, listErr, panelW-6)

			panel1 := output.Panel("System Health", healthContent.String(), panelW)
			panel2 := output.Panel("Test Summary", summaryContent, panelW)
			row1 := lipgloss.JoinHorizontal(lipgloss.Top, panel1, "  ", panel2)
			fmt.Println(row1)
			fmt.Println()

			activeContent, recentContent, latestThroughput, latestLatency := dashRunsContent(paged, listErr)

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

			activePanel := output.Panel("Active Tests", activeContent, panelW)
			recentPanel := output.Panel("Recent Completed", recentContent, panelW)
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

			check, checkErr := apiClient.ClusterCheck(context.Background())
			clusterContent := dashClusterContent(check, checkErr)

			quickContent := &strings.Builder{}
			quickContent.WriteString(fmt.Sprintf("  %s  kates test create --type LOAD\n", output.DimStyle.Render("▸")))
			quickContent.WriteString(fmt.Sprintf("  %s  kates benchmark\n", output.DimStyle.Render("▸")))
			quickContent.WriteString(fmt.Sprintf("  %s  kates gate --min-grade B\n", output.DimStyle.Render("▸")))
			quickContent.WriteString(fmt.Sprintf("  %s  kates test cleanup", output.DimStyle.Render("▸")))

			cp := output.Panel("Cluster Detail", clusterContent, panelW)
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

// dashSummaryContent is the Test Summary panel. A run list the API did not
// return is unknown, not empty, so every count reads — rather than 0.
func dashSummaryContent(paged *client.PagedTests, err error, ruleWidth int) string {
	unknown := output.DimStyle.Render(fmt.Sprintf("%3s", "—"))
	running, pending, done, total := unknown, unknown, unknown, unknown
	failed := output.DimStyle.Render("—")
	if err == nil && paged != nil {
		counts := CountStatuses(paged.Content)
		running = output.AccentStyle.Bold(true).Render(fmt.Sprintf("%3d", counts.Running))
		pending = output.WarningStyle.Render(fmt.Sprintf("%3d", counts.Pending))
		done = output.SuccessStyle.Render(fmt.Sprintf("%3d", counts.Done))
		failed = coloredCount(counts.Failed)
		total = output.LightStyle.Bold(true).Render(fmt.Sprintf("%3d", paged.TotalItems))
	}

	summaryContent := &strings.Builder{}
	fmt.Fprintf(summaryContent, "  %s %-11s %s %s\n",
		running,
		output.LightStyle.Render("Running"),
		pending,
		output.LightStyle.Render("Pending"),
	)
	fmt.Fprintf(summaryContent, "  %s %-11s %s %s\n",
		done,
		output.LightStyle.Render("Done"),
		failed,
		output.LightStyle.Render("Failed"),
	)
	fmt.Fprintf(summaryContent, "  %s\n",
		output.DimStyle.Render(strings.Repeat("·", ruleWidth)),
	)
	fmt.Fprintf(summaryContent, "  %s %s",
		total,
		output.DimStyle.Render("Total tests"),
	)
	return summaryContent.String()
}

// dashActive reports whether a run with this upper-case status has yet to
// end. A STOPPING run has not: it is still active, never completed.
func dashActive(status string) bool {
	return status == "RUNNING" || status == "PENDING" || status == "STOPPING"
}

// dashRunsContent is the Active Tests and Recent Completed panels, plus the
// summed throughput and the last p99 of the active runs for the sparklines.
// A run list the API did not return is unknown, not empty.
func dashRunsContent(paged *client.PagedTests, err error) (active, recent string, throughput, p99 float64) {
	if err != nil || paged == nil {
		unavailable := output.DimStyle.Render("  Run list unavailable")
		return unavailable, unavailable, 0, 0
	}
	g := output.Glyphs()
	activeContent := &strings.Builder{}
	activeCount := 0
	var latestThroughput, latestLatency float64
	for _, t := range paged.Content {
		status := strings.ToUpper(t.Status)
		if !dashActive(status) {
			continue
		}
		activeCount++

		line := fmt.Sprintf("  %s %-12s %s",
			output.AccentStyle.Render("◉"),
			output.LightStyle.Bold(true).Render(t.TestType),
			output.DimStyle.Render(truncID(t.ID)),
		)
		if status != "RUNNING" {
			line += "  " + output.DimStyle.Render(strings.ToLower(status))
		}
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
	if activeCount == 0 {
		activeContent.WriteString(output.DimStyle.Render("  No active tests"))
	}
	recentContent := &strings.Builder{}
	recentCount := 0
	for _, t := range paged.Content {
		status := strings.ToUpper(t.Status)
		if dashActive(status) {
			continue
		}
		if recentCount >= 5 {
			break
		}
		recentCount++
		icon := output.DimStyle.Render(g.Ring)
		switch status {
		case "DONE", "COMPLETED":
			icon = output.SuccessStyle.Render(g.Check)
		case "FAILED", "ERROR":
			icon = output.ErrorStyle.Render(g.Cross)
		}
		fmt.Fprintf(recentContent, "  %s %-12s %s  %s  %s\n",
			icon,
			output.LightStyle.Render(t.TestType),
			output.DimStyle.Render(truncID(t.ID)),
			output.StatusBadge(status),
			output.DimStyle.Render(formatTime(t.CreatedAt)),
		)
	}
	if recentCount == 0 {
		recentContent.WriteString(output.DimStyle.Render("  No completed tests"))
	}
	return activeContent.String(), recentContent.String(), latestThroughput, latestLatency
}

// dashClusterContent is the Cluster Detail panel. Its verdict is the check's
// own status: the API makes a cluster CRITICAL for offline partitions or a
// KRaft quorum with no leader, and WARNING for under-replicated partitions.
func dashClusterContent(check *client.ClusterHealthReport, err error) string {
	if err != nil || check == nil {
		return output.DimStyle.Render("  Cluster data unavailable")
	}
	g := output.Glyphs()
	// Pad the plain label, then style it: padding a styled string counts its
	// escape bytes and pads nothing.
	row := func(label, value string) string {
		return "  " + output.DimStyle.Render(fmt.Sprintf("%-10s", label)) + " " + value
	}

	ph := check.PartitionHealth
	offline := output.ErrorStyle.Render(fmt.Sprintf("%s %d offline", g.Cross, ph.Offline))
	underReplicated := output.WarningStyle.Render(fmt.Sprintf("%s %d under-replicated", g.Warn, ph.UnderReplicated))
	rows := []string{
		row("Health", clusterVerdict(check.Status)),
		row("Brokers", fmt.Sprintf("%d", check.Brokers)),
		row("Topics", fmt.Sprintf("%d", check.Topics)),
	}
	switch {
	case ph.Offline > 0 && ph.UnderReplicated > 0:
		// Two rows: on one line the pair overflows an 80-column panel.
		rows = append(rows, row("Partitions", offline), row("", underReplicated))
	case ph.Offline > 0:
		rows = append(rows, row("Partitions", offline))
	case ph.UnderReplicated > 0:
		rows = append(rows, row("Partitions", underReplicated))
	default:
		rows = append(rows, row("Partitions", output.SuccessStyle.Render(g.Check+" all in sync")))
	}

	controller := output.AccentStyle.Render(fmt.Sprintf("broker-%d", check.ControllerID))
	if q := check.KraftQuorum; q != nil && !q.HasLeader {
		controller = output.ErrorStyle.Render(g.Cross + " no KRaft leader")
	}
	return strings.Join(append(rows, row("Controller", controller)), "\n")
}

// clusterVerdict is a /api/cluster/check status in the words kates cluster
// watch uses for it.
func clusterVerdict(status string) string {
	g := output.Glyphs()
	switch strings.ToUpper(status) {
	case "HEALTHY":
		return output.SuccessStyle.Render(g.Check + " HEALTHY")
	case "WARNING":
		return output.WarningStyle.Render(g.Warn + " WARNING")
	case "CRITICAL":
		return output.ErrorStyle.Render(g.Cross + " CRITICAL")
	}
	return output.DimStyle.Render(g.Ring + " " + status)
}

func init() {
	dashboardCmd.Flags().IntVar(&dashInterval, "interval", 3, "Refresh interval in seconds")
	rootCmd.AddCommand(dashboardCmd)
}
