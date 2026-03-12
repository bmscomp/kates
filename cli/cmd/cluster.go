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
			if rack := c.RackAwareness; rack != nil {
				if key, ok := rack["topologyKey"].(string); ok {
					output.KeyValue("Rack Awareness", key)
				}
			}
			if pdb := c.PodDisruptionBudget; pdb != nil {
				if mu, ok := pdb["maxUnavailable"].(float64); ok {
					output.KeyValue("PDB maxUnavailable", fmt.Sprintf("%d", int(mu)))
				}
			}
		}

		// Kafka Broker Config
		if len(result.KafkaConfig) > 0 {
			output.SubHeader(fmt.Sprintf("Kafka Broker Configuration (%d)", len(result.KafkaConfig)))
			keys := make([]string, 0, len(result.KafkaConfig))
			for k := range result.KafkaConfig {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			rows := make([][]string, 0, len(keys))
			for _, k := range keys {
				rows = append(rows, []string{k, fmt.Sprintf("%v", result.KafkaConfig[k])})
			}
			output.Table([]string{"Property", "Value"}, rows)
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
				if p.StorageClass != "" {
					storage += " [" + p.StorageClass + "]"
				}
				jvm := ""
				if p.JVMOptions != nil {
					xms, _ := p.JVMOptions["-Xms"].(string)
					xmx, _ := p.JVMOptions["-Xmx"].(string)
					if xms != "" && xmx != "" {
						jvm = fmt.Sprintf("%s/%s", xms, xmx)
					}
				}
				sched := ""
				if p.Scheduling != nil {
					if zone, ok := p.Scheduling["zone"].(string); ok {
						sched = "zone=" + zone
					}
					if _, ok := p.Scheduling["affinity"].(bool); ok {
						if sched != "" {
							sched += " "
						}
						sched += "affinity"
					}
				}
				rows = append(rows, []string{
					p.Name,
					p.Role,
					fmt.Sprintf("%d", p.Replicas),
					storage,
					jvm,
					sched,
				})
			}
			output.Table([]string{"Name", "Role", "Replicas", "Storage", "JVM Heap", "Scheduling"}, rows)
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

		// Entity Operator
		if len(result.EntityOperator) > 0 {
			output.SubHeader("Entity Operator")
			renderOperatorComponent(result.EntityOperator, "topicOperator", "Topic Operator")
			renderOperatorComponent(result.EntityOperator, "userOperator", "User Operator")
		}

		// Cruise Control
		if len(result.CruiseControl) > 0 {
			output.SubHeader("Cruise Control")
			if bc, ok := result.CruiseControl["brokerCapacity"].(map[string]interface{}); ok {
				parts := []string{}
				if cpu, ok := bc["cpu"].(string); ok {
					parts = append(parts, "CPU: "+cpu)
				}
				if in, ok := bc["inboundNetwork"].(string); ok {
					parts = append(parts, "In: "+in)
				}
				if out, ok := bc["outboundNetwork"].(string); ok {
					parts = append(parts, "Out: "+out)
				}
				output.KeyValue("Broker Capacity", strings.Join(parts, "  │  "))
			}
			if ar, ok := result.CruiseControl["autoRebalance"].([]interface{}); ok {
				modes := make([]string, 0, len(ar))
				for _, a := range ar {
					if am, ok := a.(map[string]interface{}); ok {
						if m, ok := am["mode"].(string); ok {
							modes = append(modes, m)
						}
					}
				}
				output.KeyValue("Auto Rebalance", strings.Join(modes, ", "))
			}
			renderResources(result.CruiseControl)
		}

		// Kafka Exporter
		if len(result.KafkaExporter) > 0 {
			output.SubHeader("Kafka Exporter")
			if tr, ok := result.KafkaExporter["topicRegex"].(string); ok {
				output.KeyValue("Topic Regex", tr)
			}
			if gr, ok := result.KafkaExporter["groupRegex"].(string); ok {
				output.KeyValue("Group Regex", gr)
			}
			renderResources(result.KafkaExporter)
		}

		// Certificates
		if len(result.Certificates) > 0 {
			output.SubHeader("TLS Certificates")
			for _, caName := range []string{"clusterCa", "clientsCa"} {
				if ca, ok := result.Certificates[caName].(map[string]interface{}); ok {
					label := "Cluster CA"
					if caName == "clientsCa" {
						label = "Clients CA"
					}
					validity := fmt.Sprintf("%v", ca["validityDays"])
					renewal := fmt.Sprintf("%v", ca["renewalDays"])
					policy, _ := ca["certificateExpirationPolicy"].(string)
					output.KeyValue(label, fmt.Sprintf("validity=%sd  renewal=%sd  policy=%s", validity, renewal, policy))
				}
			}
		}

		// Metrics
		if len(result.Metrics) > 0 {
			output.SubHeader("Metrics & Monitoring")
			if km, ok := result.Metrics["kafka"].(map[string]interface{}); ok {
				if t, ok := km["type"].(string); ok {
					output.KeyValue("Kafka Metrics", t)
				}
			}
			if pms, ok := result.Metrics["podMonitors"].([]interface{}); ok {
				names := make([]string, len(pms))
				for i, pm := range pms {
					names[i] = fmt.Sprintf("%v", pm)
				}
				output.KeyValue("PodMonitors", strings.Join(names, ", "))
			}
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
		// Consumer Groups
		if cg := result.ConsumerGroups; cg != nil && cg.Count > 0 {
			output.SubHeader(fmt.Sprintf("Consumer Groups (%d)", cg.Count))
			rows := make([][]string, 0, len(cg.Items))
			for _, item := range cg.Items {
				gid, _ := item["groupId"].(string)
				state, _ := item["state"].(string)
				gtype, _ := item["type"].(string)
				members := fmt.Sprintf("%v", item["members"])
				coord := fmt.Sprintf("%v", item["coordinator"])
				rows = append(rows, []string{gid, state, gtype, members, coord})
			}
			output.Table([]string{"Group ID", "State", "Type", "Members", "Coordinator"}, rows)
		}

		// ACLs
		if acls := result.ACLs; acls != nil && acls.Count > 0 {
			output.SubHeader(fmt.Sprintf("Access Control Lists (%d)", acls.Count))
			rows := make([][]string, 0, len(acls.Items))
			for _, item := range acls.Items {
				principal, _ := item["principal"].(string)
				resType, _ := item["resourceType"].(string)
				resName, _ := item["resourceName"].(string)
				op, _ := item["operation"].(string)
				perm, _ := item["permission"].(string)
				rows = append(rows, []string{principal, resType, resName, op, perm})
			}
			output.Table([]string{"Principal", "Resource", "Name", "Operation", "Permission"}, rows)
		}

		// Log Dirs
		if len(result.LogDirs) > 0 {
			output.SubHeader(fmt.Sprintf("Log Directories (%d)", len(result.LogDirs)))
			rows := make([][]string, 0, len(result.LogDirs))
			for _, d := range result.LogDirs {
				broker := fmt.Sprintf("%v", d["brokerId"])
				path, _ := d["path"].(string)
				sizeMb := fmt.Sprintf("%v MB", d["sizeMb"])
				parts := fmt.Sprintf("%v", d["partitions"])
				rows = append(rows, []string{broker, path, sizeMb, parts})
			}
			output.Table([]string{"Broker", "Path", "Size", "Partitions"}, rows)
		}

		// Feature Flags
		if ff := result.FeatureFlags; ff != nil && ff.Count > 0 {
			output.SubHeader(fmt.Sprintf("Feature Flags (%d)", ff.Count))
			rows := make([][]string, 0, len(ff.Items))
			for _, item := range ff.Items {
				name, _ := item["name"].(string)
				minV := fmt.Sprintf("%v", item["minVersion"])
				maxV := fmt.Sprintf("%v", item["maxVersion"])
				rows = append(rows, []string{name, minV, maxV})
			}
			output.Table([]string{"Feature", "Min Version", "Max Version"}, rows)
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

func renderOperatorComponent(data map[string]interface{}, key string, label string) {
	if comp, ok := data[key].(map[string]interface{}); ok {
		parts := []string{}
		if res, ok := comp["resources"].(map[string]interface{}); ok {
			if req, ok := res["requests"].(map[string]interface{}); ok {
				mem, _ := req["memory"].(string)
				cpu, _ := req["cpu"].(string)
				parts = append(parts, fmt.Sprintf("req=%s/%s", cpu, mem))
			}
			if lim, ok := res["limits"].(map[string]interface{}); ok {
				mem, _ := lim["memory"].(string)
				cpu, _ := lim["cpu"].(string)
				parts = append(parts, fmt.Sprintf("lim=%s/%s", cpu, mem))
			}
		}
		if jvm, ok := comp["jvmOptions"].(map[string]interface{}); ok {
			xms, _ := jvm["-Xms"].(string)
			xmx, _ := jvm["-Xmx"].(string)
			if xms != "" && xmx != "" {
				parts = append(parts, fmt.Sprintf("JVM=%s/%s", xms, xmx))
			}
		}
		if ri, ok := comp["reconciliationIntervalMs"].(float64); ok {
			parts = append(parts, fmt.Sprintf("reconcile=%ds", int(ri)/1000))
		}
		output.KeyValue(label, strings.Join(parts, "  │  "))
	}
}

func renderResources(data map[string]interface{}) {
	if res, ok := data["resources"].(map[string]interface{}); ok {
		parts := []string{}
		if req, ok := res["requests"].(map[string]interface{}); ok {
			mem, _ := req["memory"].(string)
			cpu, _ := req["cpu"].(string)
			parts = append(parts, fmt.Sprintf("req=%s/%s", cpu, mem))
		}
		if lim, ok := res["limits"].(map[string]interface{}); ok {
			mem, _ := lim["memory"].(string)
			cpu, _ := lim["cpu"].(string)
			parts = append(parts, fmt.Sprintf("lim=%s/%s", cpu, mem))
		}
		output.KeyValue("Resources", strings.Join(parts, "  │  "))
	}
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
