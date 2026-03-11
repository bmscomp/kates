package cmd

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

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

var clusterTopologyCmd = &cobra.Command{
	Use:   "topology",
	Short: "Show full cluster topology including KRaft controllers and broker node pools",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.ClusterTopology(context.Background())
		if err != nil {
			if strings.Contains(err.Error(), "503") || strings.Contains(err.Error(), "Service Unavailable") {
				return cmdErr("Cluster topology is only available when the Kates backend is deployed on Kubernetes with access to Strimzi CRDs.")
			}
			return cmdErr("Failed to get cluster topology: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		kraftLabel := ""
		if result.KraftMode {
			kraftLabel = "KRaft Mode"
		}
		output.Banner("Kafka Cluster Topology",
			fmt.Sprintf("Cluster: %s  │  Kafka %s  │  %s", result.ClusterName, result.KafkaVersion, kraftLabel))

		if len(result.NodePools) > 0 {
			output.SubHeader(fmt.Sprintf("Node Pools (%d)", len(result.NodePools)))
			rows := make([][]string, 0, len(result.NodePools))
			for _, p := range result.NodePools {
				rows = append(rows, []string{
					p.Name,
					p.Role,
					fmt.Sprintf("%d", p.Replicas),
					fmt.Sprintf("%s %s", p.StorageSize, p.StorageType),
				})
			}
			output.Table([]string{"Name", "Role", "Replicas", "Storage"}, rows)
		}

		var controllers, brokers [][]string
		for _, n := range result.Nodes {
			leader := ""
			if n.IsQuorumLeader {
				leader = "★"
			}
			row := []string{
				fmt.Sprintf("%d", n.ID),
				n.Host,
				fmt.Sprintf("%d", n.Port),
				n.Rack,
				n.Pool,
				n.Status,
				leader,
			}
			if n.Role == "controller" {
				controllers = append(controllers, row)
			} else {
				brokers = append(brokers, row)
			}
		}

		if len(controllers) > 0 {
			leaderLabel := ""
			if result.ControllerQuorumLeader >= 0 {
				leaderLabel = fmt.Sprintf("   Quorum Leader: %d", result.ControllerQuorumLeader)
			}
			output.SubHeader(fmt.Sprintf("Controllers (%d)%s", len(controllers), leaderLabel))
			output.Table([]string{"ID", "Host", "Port", "Rack / AZ", "Pool", "Status", ""}, controllers)
		}

		if len(brokers) > 0 {
			output.SubHeader(fmt.Sprintf("Brokers (%d)", len(brokers)))
			output.Table([]string{"ID", "Host", "Port", "Rack / AZ", "Pool", "Status", ""}, brokers)
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

		output.Banner(fmt.Sprintf("Broker %d — Configuration", id),
			fmt.Sprintf("%d non-default entries", len(configs)))
		if len(configs) == 0 {
			output.Hint("All configs are at default values.")
			return nil
		}

		type entry struct {
			name     string
			value    string
			readOnly bool
		}
		groups := make(map[string][]entry)
		var sourceOrder []string
		for _, c := range configs {
			src := c.Source
			if src == "" {
				src = "DEFAULT"
			}
			if _, exists := groups[src]; !exists {
				sourceOrder = append(sourceOrder, src)
			}
			groups[src] = append(groups[src], entry{c.Name, c.Value, c.ReadOnly})
		}

		sort.Strings(sourceOrder)

		for _, src := range sourceOrder {
			entries := groups[src]
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].name < entries[j].name
			})

			configEntries := make([]output.ConfigEntry, 0, len(entries))
			for _, e := range entries {
				suffix := ""
				if e.readOnly {
					suffix = "🔒"
				}
				configEntries = append(configEntries, output.ConfigEntry{
					Key:    e.name,
					Value:  e.value,
					Suffix: suffix,
				})
			}
			output.ConfigList(src, configEntries)
		}

		fmt.Println()
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
	clusterCmd.AddCommand(clusterTopologyCmd)
	clusterCmd.AddCommand(clusterTopicsCmd)
	clusterCmd.AddCommand(clusterBrokerCmd)
	clusterTopicsCmd.AddCommand(clusterTopicDescribeCmd)
	rootCmd.AddCommand(clusterCmd)
}
