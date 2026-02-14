package cmd

import (
	"context"
	"fmt"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var clusterGroupsCmd = &cobra.Command{
	Use:   "groups",
	Short: "List Kafka consumer groups",
	RunE: func(cmd *cobra.Command, args []string) error {
		groups, err := apiClient.ConsumerGroups(context.Background())
		if err != nil {
			return cmdErr("Failed to list consumer groups: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(groups)
			return nil
		}

		output.Header("Consumer Groups")
		if len(groups) == 0 {
			output.Hint("No consumer groups found.")
			return nil
		}

		rows := make([][]string, 0, len(groups))
		for _, g := range groups {
			rows = append(rows, []string{
				g.GroupID,
				output.StatusBadge(g.State),
				fmt.Sprintf("%d", g.Members),
			})
		}
		output.Table([]string{"Group ID", "State", "Members"}, rows)
		return nil
	},
}

var clusterGroupDescribeCmd = &cobra.Command{
	Use:   "describe [group-id]",
	Short: "Show consumer group detail with per-partition lag",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		detail, err := apiClient.ConsumerGroupDetail(context.Background(), args[0])
		if err != nil {
			return cmdErr("Failed to describe consumer group: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(detail)
			return nil
		}

		output.Banner("Consumer Group: "+detail.GroupID,
			fmt.Sprintf("State: %s  │  Members: %d  │  Total Lag: %s",
				detail.State, detail.Members, fmtNum(float64(detail.TotalLag))))

		if len(detail.Offsets) > 0 {
			output.SubHeader("Partition Offsets")
			rows := make([][]string, 0, len(detail.Offsets))
			for _, o := range detail.Offsets {
				lagStr := fmtNum(float64(o.Lag))
				if o.Lag > 0 {
					lagStr = output.ErrorStyle.Render(lagStr)
				}
				rows = append(rows, []string{
					o.Topic,
					fmt.Sprintf("%d", o.Partition),
					fmtNum(float64(o.CurrentOffset)),
					fmtNum(float64(o.EndOffset)),
					lagStr,
				})
			}
			output.Table([]string{"Topic", "Partition", "Current", "End", "Lag"}, rows)
		} else {
			output.Hint("No committed offsets for this group.")
		}

		return nil
	},
}

func init() {
	clusterGroupsCmd.AddCommand(clusterGroupDescribeCmd)
	clusterCmd.AddCommand(clusterGroupsCmd)
}
