package cmd

import (
	"context"
	"fmt"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
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

		output.Render(outputMode == "json", report, func() {
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
				{"Controller", controllerLabel(report)},
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
		})

		return nil
	},
}

// controllerLabel names a cluster's active controller. On KRaft that is the
// metadata quorum's leader: the check's controllerId comes from
// DescribeCluster, which a KRaft broker answers with any live broker.
func controllerLabel(r *client.ClusterHealthReport) string {
	switch q := r.KraftQuorum; {
	case q == nil:
		return fmt.Sprintf("Broker %d", r.ControllerID)
	case !q.HasLeader:
		return output.ErrorStyle.Render(output.Glyphs().Cross + " no KRaft leader")
	default:
		return fmt.Sprintf("KRaft leader %d", q.LeaderID)
	}
}

func init() {
	clusterCmd.AddCommand(clusterCheckCmd)
}
