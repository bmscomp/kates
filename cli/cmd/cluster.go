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

		// Banner
		clusterName, kafkaVersion, kraftLabel := "unknown", "unknown", ""
		if c := result.Cluster; c != nil {
			clusterName = c.Name
			kafkaVersion = c.KafkaVersion
			if c.KraftMode {
				kraftLabel = "KRaft Mode"
			}
		}
		output.Banner("Kafka Cluster Topology",
			fmt.Sprintf("Cluster: %s  │  Kafka %s  │  %s", clusterName, kafkaVersion, kraftLabel))

		// Kubernetes
		if k := result.Kubernetes; k != nil {
			output.SubHeader("Kubernetes Platform")
			output.KeyValue("Version", k.GitVersion)
			output.KeyValue("Platform", k.Platform)
			output.KeyValue("Nodes", fmt.Sprintf("%d", k.NodeCount))
			if len(k.Nodes) > 0 {
				rows := make([][]string, 0, len(k.Nodes))
				for _, n := range k.Nodes {
					name, _ := n["name"].(string)
					role, _ := n["role"].(string)
					kubelet, _ := n["kubeletVersion"].(string)
					arch, _ := n["arch"].(string)
					runtime, _ := n["containerRuntime"].(string)
					ready, _ := n["ready"].(bool)
					readyStr := "✗"
					if ready {
						readyStr = "✓"
					}
					rows = append(rows, []string{name, role, kubelet, arch, runtime, readyStr})
				}
				output.Table([]string{"Node", "Role", "Kubelet", "Arch", "Runtime", "Ready"}, rows)
			}
		}

		// Strimzi
		if s := result.Strimzi; s != nil && len(s) > 0 {
			output.SubHeader("Strimzi Operator")
			if v, ok := s["version"].(string); ok {
				output.KeyValue("Version", v)
			}
			if img, ok := s["operatorImage"].(string); ok {
				output.KeyValue("Image", img)
			}
			statusParts := []string{}
			for _, key := range []string{"operatorReady", "entityOperatorReady", "cruiseControlReady", "kafkaExporterReady"} {
				label := strings.TrimSuffix(key, "Ready")
				label = strings.ReplaceAll(label, "operator", "Operator")
				label = strings.ReplaceAll(label, "entityOperator", "Entity Operator")
				label = strings.ReplaceAll(label, "cruiseControl", "Cruise Control")
				label = strings.ReplaceAll(label, "kafkaExporter", "Kafka Exporter")
				if v, ok := s[key].(bool); ok {
					icon := "✗"
					if v {
						icon = "✓"
					}
					statusParts = append(statusParts, fmt.Sprintf("  %s %s", icon, label))
				}
			}
			if len(statusParts) > 0 {
				output.KeyValue("Components", "")
				for _, p := range statusParts {
					fmt.Println(p)
				}
			}
		}

		// Cluster details
		if c := result.Cluster; c != nil {
			output.SubHeader("Kafka Cluster")
			output.KeyValue("Cluster ID", c.ClusterID)
			output.KeyValue("Namespace", c.Namespace)
			output.KeyValue("Brokers", fmt.Sprintf("%d", c.BrokerCount))
			if c.ControllerQuorumLeader >= 0 {
				output.KeyValue("Quorum Leader", fmt.Sprintf("Node %d", c.ControllerQuorumLeader))
			}
			readyStr := "✗ Not Ready"
			if c.Ready {
				readyStr = "✓ Ready"
			}
			output.KeyValue("Status", readyStr)

			if len(c.Listeners) > 0 {
				rows := make([][]string, 0, len(c.Listeners))
				for _, l := range c.Listeners {
					name, _ := l["name"].(string)
					lType, _ := l["type"].(string)
					port := fmt.Sprintf("%v", l["port"])
					tls := fmt.Sprintf("%v", l["tls"])
					auth, _ := l["authType"].(string)
					rows = append(rows, []string{name, lType, port, tls, auth})
				}
				output.Table([]string{"Listener", "Type", "Port", "TLS", "Auth"}, rows)
			}

			if auth := c.Authorization; auth != nil {
				if t, ok := auth["type"].(string); ok {
					output.KeyValue("Authorization", t)
				}
				if su, ok := auth["superUsers"].([]interface{}); ok && len(su) > 0 {
					parts := make([]string, len(su))
					for i, u := range su {
						parts[i] = fmt.Sprintf("%v", u)
					}
					output.KeyValue("Super Users", strings.Join(parts, ", "))
				}
			}
		}

		// Node Pools
		if len(result.NodePools) > 0 {
			output.SubHeader(fmt.Sprintf("Node Pools (%d)", len(result.NodePools)))
			rows := make([][]string, 0, len(result.NodePools))
			for _, p := range result.NodePools {
				storage := p.StorageSize
				if p.StorageType != "" {
					storage = fmt.Sprintf("%s (%s)", p.StorageSize, p.StorageType)
				}
				rows = append(rows, []string{
					p.Name,
					p.Role,
					fmt.Sprintf("%d", p.Replicas),
					storage,
				})
			}
			output.Table([]string{"Name", "Role", "Replicas", "Storage"}, rows)
		}

		// Nodes
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
				n.K8sNode,
				n.Status,
				leader,
			}
			if n.Role == "controller" {
				controllers = append(controllers, row)
			} else {
				brokers = append(brokers, row)
			}
		}

		headers := []string{"ID", "Host", "Port", "Rack", "Pool", "K8s Node", "Status", ""}
		if len(controllers) > 0 {
			output.SubHeader(fmt.Sprintf("Controllers (%d)", len(controllers)))
			output.Table(headers, controllers)
		}
		if len(brokers) > 0 {
			output.SubHeader(fmt.Sprintf("Brokers (%d)", len(brokers)))
			output.Table(headers, brokers)
		}

		// Topics
		if t := result.Topics; t != nil && t.Count > 0 {
			output.SubHeader(fmt.Sprintf("Managed Topics (%d)", t.Count))
			rows := make([][]string, 0, len(t.Items))
			for _, item := range t.Items {
				name, _ := item["name"].(string)
				partitions := fmt.Sprintf("%v", item["partitions"])
				replicas := fmt.Sprintf("%v", item["replicas"])
				rows = append(rows, []string{name, partitions, replicas})
			}
			output.Table([]string{"Topic", "Partitions", "Replicas"}, rows)
		}

		// Users
		if u := result.Users; u != nil && u.Count > 0 {
			output.SubHeader(fmt.Sprintf("Kafka Users (%d)", u.Count))
			rows := make([][]string, 0, len(u.Items))
			for _, item := range u.Items {
				name, _ := item["name"].(string)
				authType, _ := item["authType"].(string)
				aclType, _ := item["aclType"].(string)
				ready, _ := item["ready"].(bool)
				readyStr := "✗"
				if ready {
					readyStr = "✓"
				}
				if aclType == "" {
					aclType = "superUser"
				}
				rows = append(rows, []string{name, authType, aclType, readyStr})
			}
			output.Table([]string{"User", "Auth", "ACL", "Ready"}, rows)
		}

		// Connect
		if len(result.Connect) > 0 {
			output.SubHeader(fmt.Sprintf("Kafka Connect (%d)", len(result.Connect)))
			for _, c := range result.Connect {
				name, _ := c["name"].(string)
				output.KeyValue("Name", name)
				if r, ok := c["replicas"].(float64); ok {
					output.KeyValue("Replicas", fmt.Sprintf("%d", int(r)))
				}
			}
		}

		// MirrorMaker2
		if len(result.MirrorMaker) > 0 {
			output.SubHeader(fmt.Sprintf("MirrorMaker2 (%d)", len(result.MirrorMaker)))
			for _, m := range result.MirrorMaker {
				name, _ := m["name"].(string)
				output.KeyValue("Name", name)
			}
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
