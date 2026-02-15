package cmd

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/klster/kates-cli/client"
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

		skewCount := 0
		urpCount := 0
		for _, b := range brokers {
			if b.Skewed {
				skewCount++
			}
			urpCount += b.UnderReplicatedPartitions
		}

		subtitle := fmt.Sprintf("Run: %s  │  %d brokers", runID, len(brokers))
		if skewCount > 0 {
			subtitle += fmt.Sprintf("  │  %s", output.ErrorStyle.Render(fmt.Sprintf("%d skewed", skewCount)))
		}
		if urpCount > 0 {
			subtitle += fmt.Sprintf("  │  %s", output.WarningStyle.Render(fmt.Sprintf("%d under-replicated", urpCount)))
		}
		output.Banner("Broker-Correlated Metrics", subtitle)

		rows := make([][]string, 0, len(brokers))
		maxThroughput := 0.0
		for _, b := range brokers {
			if b.Metrics.AvgThroughputRecPerSec > maxThroughput {
				maxThroughput = b.Metrics.AvgThroughputRecPerSec
			}
		}

		for _, b := range brokers {
			rackLabel := b.Rack
			if rackLabel == "" {
				rackLabel = "-"
			}

			brokerLabel := fmt.Sprintf("%d", b.BrokerID)
			if b.IsController {
				brokerLabel = fmt.Sprintf("%d ★", b.BrokerID)
			}

			skewLabel := renderSkew(b.SkewPercent, b.Skewed)

			urpLabel := fmt.Sprintf("%d", b.UnderReplicatedPartitions)
			if b.UnderReplicatedPartitions > 0 {
				urpLabel = output.WarningStyle.Render(fmt.Sprintf("%d ⚠", b.UnderReplicatedPartitions))
			}

			throughputBar := renderThroughputBar(b.Metrics.AvgThroughputRecPerSec, maxThroughput)

			rows = append(rows, []string{
				brokerLabel,
				fmt.Sprintf("%s (%s)", b.Host, rackLabel),
				fmt.Sprintf("%d / %d", b.LeaderPartitions, b.TotalPartitions),
				fmt.Sprintf("%.1f%%", b.LeaderSharePct),
				throughputBar,
				fmt.Sprintf("%.2f ms", b.Metrics.P99LatencyMs),
				skewLabel,
				urpLabel,
			})
		}

		output.Table([]string{
			"Broker", "Host (Rack)", "Leaders / Total",
			"Share", "Throughput", "p99 Latency", "Skew", "URP",
		}, rows)

		output.Divider()
		output.SubHeader("Partition Balance")
		balanceScore := computeBalanceScore(brokers)
		scoreLabel := fmt.Sprintf("%.0f%%", balanceScore)
		if balanceScore >= 90 {
			output.KeyValue("Balance Score", output.SuccessStyle.Render(scoreLabel+" — Excellent"))
		} else if balanceScore >= 70 {
			output.KeyValue("Balance Score", output.WarningStyle.Render(scoreLabel+" — Fair"))
		} else {
			output.KeyValue("Balance Score", output.ErrorStyle.Render(scoreLabel+" — Poor"))
		}
		fmt.Println()

		return nil
	},
}

func renderSkew(pct float64, skewed bool) string {
	label := fmt.Sprintf("%+.1f%%", pct)
	if skewed {
		return output.ErrorStyle.Render(label + " ⚠")
	}
	if math.Abs(pct) > 10 {
		return output.WarningStyle.Render(label)
	}
	return output.DimStyle.Render(label)
}

func renderThroughputBar(value, max float64) string {
	barWidth := 12
	filled := 0
	if max > 0 {
		filled = int((value / max) * float64(barWidth))
		if filled > barWidth {
			filled = barWidth
		}
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
	return fmt.Sprintf("%s %.0f", bar, value)
}

func computeBalanceScore(brokers []client.BrokerMetricsResponse) float64 {
	if len(brokers) == 0 {
		return 100
	}
	idealShare := 100.0 / float64(len(brokers))
	totalDeviation := 0.0
	for _, b := range brokers {
		totalDeviation += math.Abs(b.LeaderSharePct - idealShare)
	}
	maxDeviation := 2 * (100.0 - idealShare)
	if maxDeviation == 0 {
		return 100
	}
	return math.Max(0, 100-(totalDeviation/maxDeviation)*100)
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
				ctrlBadge := ""
				if b.ID == snapshot.ControllerID {
					ctrlBadge = " ★"
				}
				rows = append(rows, []string{
					fmt.Sprintf("%d%s", b.ID, ctrlBadge),
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
