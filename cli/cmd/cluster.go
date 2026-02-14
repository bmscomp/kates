package cmd

import (
	"context"
	"fmt"
	"strconv"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var clusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Kafka cluster information and topics",
}

var clusterInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show Kafka cluster metadata",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.ClusterInfo(context.Background())
		if err != nil {
			return cmdErr("Failed to get cluster info: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		output.Banner("Kafka Cluster", "Cluster ID: "+result.ClusterID)

		output.SubHeader("Overview")
		output.KeyValue("Broker Count", fmt.Sprintf("%v", result.BrokerCount))

		if ctrl := result.Controller; ctrl != nil {
			output.SubHeader("Controller")
			output.KeyValue("Node ID", fmt.Sprintf("%v", ctrl.ID))
			output.KeyValue("Host", ctrl.Host)
			output.KeyValue("Port", fmt.Sprintf("%v", ctrl.Port))
			output.KeyValue("Rack / AZ", ctrl.Rack)
		}

		if len(result.Brokers) > 0 {
			output.SubHeader(fmt.Sprintf("Brokers (%d)", len(result.Brokers)))
			rows := make([][]string, 0, len(result.Brokers))
			for _, b := range result.Brokers {
				isCtrl := ""
				if result.Controller != nil && fmt.Sprintf("%v", b.ID) == fmt.Sprintf("%v", result.Controller.ID) {
					isCtrl = "★"
				}
				rows = append(rows, []string{
					fmt.Sprintf("%v", b.ID),
					b.Host,
					fmt.Sprintf("%v", b.Port),
					b.Rack,
					isCtrl,
				})
			}
			output.Table([]string{"ID", "Host", "Port", "Rack / AZ", "Role"}, rows)
		}

		return nil
	},
}

var clusterTopicsCmd = &cobra.Command{
	Use:   "topics",
	Short: "List all Kafka topics",
	RunE: func(cmd *cobra.Command, args []string) error {
		topics, err := apiClient.Topics(context.Background())
		if err != nil {
			return cmdErr("Failed to list topics: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(topics)
			return nil
		}

		output.Header("Kafka Topics")
		if len(topics) == 0 {
			output.Hint("No topics found.")
			return nil
		}

		rows := make([]string, len(topics))
		for i, t := range topics {
			rows[i] = fmt.Sprintf("  %d. %s", i+1, t)
		}
		output.Table([]string{"#", "Topic Name"}, func() [][]string {
			r := make([][]string, len(topics))
			for i, t := range topics {
				r[i] = []string{fmt.Sprintf("%d", i+1), t}
			}
			return r
		}())
		return nil
	},
}

var clusterTopicDescribeCmd = &cobra.Command{
	Use:   "describe [topic-name]",
	Short: "Show detailed topic metadata, configs, and partition health",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		detail, err := apiClient.TopicDetail(context.Background(), args[0])
		if err != nil {
			return cmdErr("Failed to describe topic: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(detail)
			return nil
		}

		internalLabel := ""
		if detail.Internal {
			internalLabel = "  (internal)"
		}
		output.Banner("Topic: "+detail.Name+internalLabel,
			fmt.Sprintf("Partitions: %d  │  Replication Factor: %d", detail.Partitions, detail.ReplicationFactor))

		if len(detail.Configs) > 0 {
			output.SubHeader("Configuration")
			configRows := make([][]string, 0, len(detail.Configs))
			for k, v := range detail.Configs {
				configRows = append(configRows, []string{k, v})
			}
			output.Table([]string{"Config", "Value"}, configRows)
		}

		if len(detail.PartitionInfo) > 0 {
			underReplicated := 0
			for _, p := range detail.PartitionInfo {
				if p.UnderReplicated {
					underReplicated++
				}
			}

			label := fmt.Sprintf("Partitions (%d)", len(detail.PartitionInfo))
			if underReplicated > 0 {
				label += fmt.Sprintf("  — %s", output.ErrorStyle.Render(fmt.Sprintf("%d under-replicated", underReplicated)))
			}
			output.SubHeader(label)

			rows := make([][]string, 0, len(detail.PartitionInfo))
			for _, p := range detail.PartitionInfo {
				replicaStr := fmt.Sprintf("%v", p.Replicas)
				isrStr := fmt.Sprintf("%v", p.ISR)
				urFlag := ""
				if p.UnderReplicated {
					urFlag = output.ErrorStyle.Render("⚠ YES")
				}

				rows = append(rows, []string{
					fmt.Sprintf("%d", p.Partition),
					fmt.Sprintf("%d", p.Leader),
					replicaStr,
					isrStr,
					urFlag,
				})
			}
			output.Table([]string{"Partition", "Leader", "Replicas", "ISR", "Under-Replicated"}, rows)
		}

		return nil
	},
}

var clusterBrokerConfigsCmd = &cobra.Command{
	Use:   "configs [broker-id]",
	Short: "Show non-default configuration for a broker",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.Atoi(args[0])
		if err != nil {
			return cmdErr("Broker ID must be a number")
		}

		configs, err := apiClient.BrokerConfigs(context.Background(), id)
		if err != nil {
			return cmdErr("Failed to get broker configs: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(configs)
			return nil
		}

		output.Header(fmt.Sprintf("Broker %d — Configuration", id))
		if len(configs) == 0 {
			output.Hint("All configs are at default values.")
			return nil
		}

		rows := make([][]string, 0, len(configs))
		for _, c := range configs {
			roFlag := ""
			if c.ReadOnly {
				roFlag = "✓"
			}
			rows = append(rows, []string{c.Name, c.Value, c.Source, roFlag})
		}
		output.Table([]string{"Config", "Value", "Source", "RO"}, rows)
		return nil
	},
}

var clusterBrokerCmd = &cobra.Command{
	Use:   "broker",
	Short: "Broker-level commands",
}

func init() {
	clusterBrokerCmd.AddCommand(clusterBrokerConfigsCmd)
	clusterCmd.AddCommand(clusterInfoCmd)
	clusterCmd.AddCommand(clusterTopicsCmd)
	clusterCmd.AddCommand(clusterBrokerCmd)
	clusterTopicsCmd.AddCommand(clusterTopicDescribeCmd)
	rootCmd.AddCommand(clusterCmd)
}
