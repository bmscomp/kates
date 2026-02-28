package cmd

import (
	"context"
	"fmt"
	"sort"

	"github.com/klster/kates-cli/client"
	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var (
	diffFrom string
	diffTo   string
)

var clusterDiffCmd = &cobra.Command{
	Use:   "diff",
	Short: "Compare Kafka cluster state between two contexts",
	Long:  "Fetches topics and consumer groups from two named contexts and displays additions, removals, and configuration differences.",
	Example: `  kates diff --from local --to staging
  kates diff --from dev --to prod -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if diffFrom == "" || diffTo == "" {
			return cmdErr("Both --from and --to context names are required")
		}

		cfg := loadConfig()

		fromCtx, ok := cfg.Contexts[diffFrom]
		if !ok {
			return cmdErr(fmt.Sprintf("Context '%s' not found", diffFrom))
		}
		toCtx, ok := cfg.Contexts[diffTo]
		if !ok {
			return cmdErr(fmt.Sprintf("Context '%s' not found", diffTo))
		}

		clientFrom := client.New(fromCtx.URL)
		clientTo := client.New(toCtx.URL)

		topicsFrom, err := clientFrom.KafkaTopics(context.Background())
		if err != nil {
			return cmdErr(fmt.Sprintf("Failed to list topics from %s: %s", diffFrom, err))
		}
		topicsTo, err := clientTo.KafkaTopics(context.Background())
		if err != nil {
			return cmdErr(fmt.Sprintf("Failed to list topics from %s: %s", diffTo, err))
		}

		fromMap := topicMap(topicsFrom)
		toMap := topicMap(topicsTo)

		var added, removed []string
		var changed []topicDelta

		for name, t := range toMap {
			if _, ok := fromMap[name]; !ok {
				added = append(added, name)
			} else {
				f := fromMap[name]
				if f.Partitions != t.Partitions || f.ReplicationFactor != t.ReplicationFactor {
					changed = append(changed, topicDelta{
						Name:   name,
						FromP:  f.Partitions,
						ToP:    t.Partitions,
						FromRF: f.ReplicationFactor,
						ToRF:   t.ReplicationFactor,
					})
				}
			}
		}
		for name := range fromMap {
			if _, ok := toMap[name]; !ok {
				removed = append(removed, name)
			}
		}
		sort.Strings(added)
		sort.Strings(removed)

		if outputMode == "json" {
			output.JSON(map[string]interface{}{
				"added":   added,
				"removed": removed,
				"changed": changed,
			})
			return nil
		}

		output.Banner("Cluster Diff",
			fmt.Sprintf("%s → %s", diffFrom, diffTo))

		if len(added) == 0 && len(removed) == 0 && len(changed) == 0 {
			output.Success("Clusters are identical in topic configuration")
			return nil
		}

		if len(added) > 0 {
			output.SubHeader(fmt.Sprintf("Topics only in %s (%d)", diffTo, len(added)))
			rows := make([][]string, 0, len(added))
			for _, name := range added {
				t := toMap[name]
				rows = append(rows, []string{"+ " + name, fmt.Sprintf("%d", t.Partitions), fmt.Sprintf("%d", t.ReplicationFactor)})
			}
			output.Table([]string{"Topic", "Partitions", "RF"}, rows)
		}

		if len(removed) > 0 {
			output.SubHeader(fmt.Sprintf("Topics only in %s (%d)", diffFrom, len(removed)))
			rows := make([][]string, 0, len(removed))
			for _, name := range removed {
				t := fromMap[name]
				rows = append(rows, []string{"- " + name, fmt.Sprintf("%d", t.Partitions), fmt.Sprintf("%d", t.ReplicationFactor)})
			}
			output.Table([]string{"Topic", "Partitions", "RF"}, rows)
		}

		if len(changed) > 0 {
			output.SubHeader(fmt.Sprintf("Configuration Differences (%d)", len(changed)))
			rows := make([][]string, 0, len(changed))
			for _, d := range changed {
				pChange := ""
				if d.FromP != d.ToP {
					pChange = fmt.Sprintf("%d → %d", d.FromP, d.ToP)
				}
				rfChange := ""
				if d.FromRF != d.ToRF {
					rfChange = fmt.Sprintf("%d → %d", d.FromRF, d.ToRF)
				}
				rows = append(rows, []string{d.Name, pChange, rfChange})
			}
			output.Table([]string{"Topic", "Partitions", "Rep. Factor"}, rows)
		}

		return nil
	},
}

type topicDelta struct {
	Name   string `json:"name"`
	FromP  int    `json:"fromPartitions"`
	ToP    int    `json:"toPartitions"`
	FromRF int    `json:"fromRF"`
	ToRF   int    `json:"toRF"`
}

func topicMap(topics []client.KafkaTopic) map[string]client.KafkaTopic {
	m := make(map[string]client.KafkaTopic, len(topics))
	for _, t := range topics {
		m[t.Name] = t
	}
	return m
}

func init() {
	clusterDiffCmd.Flags().StringVar(&diffFrom, "from", "", "Source context name")
	clusterDiffCmd.Flags().StringVar(&diffTo, "to", "", "Target context name")
	rootCmd.AddCommand(clusterDiffCmd)
}
