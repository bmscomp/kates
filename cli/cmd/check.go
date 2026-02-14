package cmd

import (
	"context"
	"fmt"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var clusterCheckCmd = &cobra.Command{
	Use:   "check",
	Short: "Run comprehensive Kafka cluster health check",
	RunE: func(cmd *cobra.Command, args []string) error {
		report, err := apiClient.ClusterCheck(context.Background())
		if err != nil {
			return cmdErr("Cluster health check failed: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(report)
			return nil
		}

		statusLabel := output.SuccessStyle.Render("● HEALTHY")
		if report.Status == "WARNING" {
			statusLabel = output.WarningStyle.Render("▲ WARNING")
		} else if report.Status == "CRITICAL" {
			statusLabel = output.ErrorStyle.Render("✖ CRITICAL")
		}

		output.Banner("Cluster Health Check",
			fmt.Sprintf("Status: %s  │  Cluster: %s", statusLabel, report.ClusterID))

		output.SubHeader("Overview")
		rows := [][]string{
			{"Brokers", fmt.Sprintf("%d", report.Brokers)},
			{"Controller", fmt.Sprintf("Broker %d", report.ControllerID)},
			{"Topics", fmt.Sprintf("%d", report.Topics)},
			{"Partitions", fmt.Sprintf("%d", report.Partitions)},
			{"Consumer Groups", fmt.Sprintf("%d", report.ConsumerGroups)},
		}
		output.Table([]string{"Metric", "Value"}, rows)

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
			output.SubHeader("Problems")
			pRows := make([][]string, 0, len(ph.Problems))
			for _, p := range ph.Problems {
				topic := fmt.Sprintf("%v", p["topic"])
				partition := fmt.Sprintf("%v", p["partition"])
				issue := fmt.Sprintf("%v", p["issue"])
				pRows = append(pRows, []string{topic, partition, output.ErrorStyle.Render(issue)})
			}
			output.Table([]string{"Topic", "Partition", "Issue"}, pRows)
		}

		return nil
	},
}

func init() {
	clusterCmd.AddCommand(clusterCheckCmd)
}
