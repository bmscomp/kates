package cmd

import (
	"context"
	"fmt"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var reportBrokersCmd = &cobra.Command{
	Use:   "brokers [run-id]",
	Short: "Show per-broker metric breakdown for a test run",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		runID := args[0]

		brokers, err := apiClient.ReportBrokers(context.Background(), runID)
		if err != nil {
			return cmdErr("Failed to get broker metrics: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(brokers)
			return nil
		}

		if len(brokers) == 0 {
			output.Header("Broker Metrics")
			output.Hint("No broker metrics available. Run may lack a topic spec or cluster was unreachable.")
			return nil
		}

		output.Banner("Broker-Correlated Metrics",
			fmt.Sprintf("Run: %s  │  %d brokers", runID, len(brokers)))

		rows := make([][]string, 0, len(brokers))
		for _, b := range brokers {
			rackLabel := b.Rack
			if rackLabel == "" {
				rackLabel = "-"
			}

			skewFlag := ""
			if b.Skewed {
				skewFlag = output.ErrorStyle.Render("⚠ SKEW")
			}

			rows = append(rows, []string{
				fmt.Sprintf("%d", b.BrokerID),
				fmt.Sprintf("%s (%s)", b.Host, rackLabel),
				fmt.Sprintf("%d / %d", b.LeaderPartitions, b.TotalPartitions),
				fmt.Sprintf("%.1f%%", b.LeaderSharePct),
				fmt.Sprintf("%.1f rec/s", b.Metrics.AvgThroughputRecPerSec),
				fmt.Sprintf("%.2f ms", b.Metrics.P99LatencyMs),
				skewFlag,
			})
		}

		output.Table([]string{
			"Broker", "Host (Rack)", "Leaders / Total",
			"Share", "Throughput", "p99 Latency", "Skew",
		}, rows)

		return nil
	},
}

var reportSnapshotCmd = &cobra.Command{
	Use:   "snapshot [run-id]",
	Short: "Show cluster topology captured at test time",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		runID := args[0]

		snapshot, err := apiClient.ReportSnapshot(context.Background(), runID)
		if err != nil {
			return cmdErr("Failed to get cluster snapshot: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(snapshot)
			return nil
		}

		output.Banner("Cluster Snapshot",
			fmt.Sprintf("Run: %s  │  Cluster: %s", runID, snapshot.ClusterID))

		output.SubHeader("Overview")
		output.KeyValue("Broker Count", fmt.Sprintf("%d", snapshot.BrokerCount))
		output.KeyValue("Controller ID", fmt.Sprintf("%d", snapshot.ControllerID))

		if len(snapshot.Brokers) > 0 {
			output.SubHeader(fmt.Sprintf("Brokers (%d)", len(snapshot.Brokers)))
			rows := make([][]string, 0, len(snapshot.Brokers))
			for _, b := range snapshot.Brokers {
				rack := b.Rack
				if rack == "" {
					rack = "-"
				}
				rows = append(rows, []string{
					fmt.Sprintf("%d", b.ID),
					b.Host,
					fmt.Sprintf("%d", b.Port),
					rack,
				})
			}
			output.Table([]string{"ID", "Host", "Port", "Rack"}, rows)
		}

		if len(snapshot.Leaders) > 0 {
			output.SubHeader(fmt.Sprintf("Partition Leaders (%d)", len(snapshot.Leaders)))
			rows := make([][]string, 0, len(snapshot.Leaders))
			for _, l := range snapshot.Leaders {
				rows = append(rows, []string{
					l.Topic,
					fmt.Sprintf("%d", l.Partition),
					fmt.Sprintf("%d", l.LeaderID),
					fmt.Sprintf("%v", l.Replicas),
					fmt.Sprintf("%v", l.ISR),
				})
			}
			output.Table([]string{"Topic", "Partition", "Leader", "Replicas", "ISR"}, rows)
		}

		return nil
	},
}

func init() {
	reportCmd.AddCommand(reportBrokersCmd)
	reportCmd.AddCommand(reportSnapshotCmd)
}
