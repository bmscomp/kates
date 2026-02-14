package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/klster/kates-cli/client"
	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var clusterWatchInterval int

type healthHistory struct {
	statuses       []string
	underRep       []float64
	offline        []float64
	partitions     []float64
	consumerGroups []float64
}

func (h *healthHistory) record(r *client.ClusterHealthReport) {
	h.statuses = append(h.statuses, r.Status)
	h.underRep = append(h.underRep, float64(r.PartitionHealth.UnderReplicated))
	h.offline = append(h.offline, float64(r.PartitionHealth.Offline))
	h.partitions = append(h.partitions, float64(r.Partitions))
	h.consumerGroups = append(h.consumerGroups, float64(r.ConsumerGroups))

	const maxHistory = 30
	if len(h.statuses) > maxHistory {
		h.statuses = h.statuses[1:]
		h.underRep = h.underRep[1:]
		h.offline = h.offline[1:]
		h.partitions = h.partitions[1:]
		h.consumerGroups = h.consumerGroups[1:]
	}
}

var clusterWatchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Live-watch Kafka cluster health (refreshing dashboard)",
	RunE: func(cmd *cobra.Command, args []string) error {
		tick := 0
		hist := &healthHistory{}

		for {
			report, err := apiClient.ClusterCheck(context.Background())
			if err != nil {
				if tick == 0 {
					return cmdErr("Cluster health check failed: " + err.Error())
				}
				output.Error("Refresh failed: " + err.Error())
				tick++
				time.Sleep(time.Duration(clusterWatchInterval) * time.Second)
				continue
			}

			hist.record(report)

			fmt.Print("\033[2J\033[H")

			statusLabel := output.SuccessStyle.Render("● HEALTHY")
			if report.Status == "WARNING" {
				statusLabel = output.WarningStyle.Render("▲ WARNING")
			} else if report.Status == "CRITICAL" {
				statusLabel = output.ErrorStyle.Render("✖ CRITICAL")
			}

			output.Banner("Cluster Watch",
				fmt.Sprintf("Status: %s  │  Cluster: %s  │  Refresh: %ds",
					statusLabel, report.ClusterID, clusterWatchInterval))

			output.SubHeader("Cluster Overview")
			overviewRows := [][]string{
				{"Brokers", fmt.Sprintf("%d", report.Brokers)},
				{"Controller", fmt.Sprintf("Broker %d", report.ControllerID)},
				{"Topics", fmt.Sprintf("%d", report.Topics)},
				{"Partitions", fmt.Sprintf("%d", report.Partitions)},
				{"Consumer Groups", fmt.Sprintf("%d", report.ConsumerGroups)},
			}
			output.Table([]string{"Metric", "Value"}, overviewRows)

			ph := report.PartitionHealth
			output.SubHeader("Partition Health")

			urLabel := fmt.Sprintf("%d", ph.UnderReplicated)
			if ph.UnderReplicated > 0 {
				urLabel = output.ErrorStyle.Render(urLabel)
			}
			offLabel := fmt.Sprintf("%d", ph.Offline)
			if ph.Offline > 0 {
				offLabel = output.ErrorStyle.Render(offLabel)
			}
			healthRows := [][]string{
				{"Under-Replicated", urLabel},
				{"Offline", offLabel},
			}
			output.Table([]string{"Check", "Count"}, healthRows)

			if len(ph.Problems) > 0 {
				output.SubHeader("Active Problems")
				pRows := make([][]string, 0, len(ph.Problems))
				for _, p := range ph.Problems {
					pRows = append(pRows, []string{
						fmt.Sprintf("%v", p["topic"]),
						fmt.Sprintf("%v", p["partition"]),
						output.ErrorStyle.Render(fmt.Sprintf("%v", p["issue"])),
					})
				}
				output.Table([]string{"Topic", "Partition", "Issue"}, pRows)
			}

			if len(hist.underRep) > 1 {
				output.SubHeader("Trend (last 30 polls)")
				fmt.Printf("  Under-Replicated  %s\n", output.SparklineColored(hist.underRep, false))
				fmt.Printf("  Offline           %s\n", output.SparklineColored(hist.offline, false))
				fmt.Printf("  Partitions        %s\n", output.Sparkline(hist.partitions))
			}

			fmt.Printf("\n  %s Refreshing every %ds... (Ctrl+C to stop)\n",
				spinnerFrame(tick),
				clusterWatchInterval,
			)

			tick++
			time.Sleep(time.Duration(clusterWatchInterval) * time.Second)
		}
	},
}

func init() {
	clusterWatchCmd.Flags().IntVar(&clusterWatchInterval, "interval", 5, "Refresh interval in seconds")
	clusterCmd.AddCommand(clusterWatchCmd)
}
