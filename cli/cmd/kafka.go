package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var kafkaCmd = &cobra.Command{
	Use:   "kafka",
	Short: "Interactive Kafka client — inspect and interact with your cluster",
	Long: strings.TrimSpace(`
kates kafka is a full-featured interactive Kafka client.

Inspect brokers, topics, consumer groups, and partition health.
Produce records to any topic, or tail records like a log viewer.
All output is table-formatted with colour-coded health indicators.
`),
}

var kafkaBrokersCmd = &cobra.Command{
	Use:   "brokers",
	Short: "List brokers with ID, host, port, rack, and controller status",
	RunE: func(cmd *cobra.Command, args []string) error {
		info, err := apiClient.KafkaBrokers(context.Background())
		if err != nil {
			return cmdErr("Failed to get brokers: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(info)
			return nil
		}

		clusterLabel := "Kafka Cluster"
		if info.ClusterID != "" {
			clusterLabel += "  " + dimStyle.Render(info.ClusterID)
		}
		output.Banner(clusterLabel, fmt.Sprintf("%v brokers", info.BrokerCount))

		if len(info.Brokers) == 0 {
			output.Hint("No brokers found.")
			return nil
		}

		controllerID := ""
		if info.Controller != nil {
			controllerID = fmt.Sprintf("%v", info.Controller.ID)
		}

		rows := make([][]string, 0, len(info.Brokers))
		for _, b := range info.Brokers {
			idStr := fmt.Sprintf("%v", b.ID)
			role := ""
			if idStr == controllerID {
				role = leaderStyle.Render("★ CONTROLLER")
			} else {
				role = dimStyle.Render("follower")
			}
			rack := fmt.Sprintf("%v", b.Rack)
			if rack == "<nil>" || rack == "" {
				rack = "-"
			}
			rows = append(rows, []string{idStr, b.Host, fmt.Sprintf("%v", b.Port), rack, role})
		}
		output.Table([]string{"ID", "Host", "Port", "Rack / AZ", "Role"}, rows)
		return nil
	},
}

var kafkaTopicsCmd = &cobra.Command{
	Use:   "topics",
	Short: "List all topics with partition, replication, and ISR health",
	RunE: func(cmd *cobra.Command, args []string) error {
		filterFlag, _ := cmd.Flags().GetString("filter")

		topics, err := apiClient.KafkaTopics(context.Background())
		if err != nil {
			return cmdErr("Failed to list topics: " + err.Error())
		}

		if filterFlag != "" {
			filtered := topics[:0]
			for _, t := range topics {
				if strings.Contains(strings.ToLower(t.Name), strings.ToLower(filterFlag)) {
					filtered = append(filtered, t)
				}
			}
			topics = filtered
		}

		if outputMode == "json" {
			output.JSON(topics)
			return nil
		}

		label := fmt.Sprintf("Kafka Topics (%d)", len(topics))
		if filterFlag != "" {
			label += dimStyle.Render("  filter: " + filterFlag)
		}
		output.Header(label)

		if len(topics) == 0 {
			output.Hint("No topics found.")
			return nil
		}

		rows := make([][]string, 0, len(topics))
		for _, t := range topics {
			isrHealth := healthyBadge("✓ HEALTHY")
			ur := fmt.Sprintf("%v", t.UnderReplicated)
			if ur != "0" && ur != "<nil>" {
				isrHealth = warnBadge("⚠ " + ur + " under-replicated")
			}
			internalLabel := ""
			if t.Internal {
				internalLabel = dimStyle.Render("(internal)")
			}
			rows = append(rows, []string{
				t.Name,
				internalLabel,
				fmt.Sprintf("%d", t.Partitions),
				fmt.Sprintf("%d", t.ReplicationFactor),
				isrHealth,
			})
		}
		output.Table([]string{"Topic", "Type", "Partitions", "Rep. Factor", "ISR Health"}, rows)
		return nil
	},
}

var kafkaTopicCmd = &cobra.Command{
	Use:   "topic <name>",
	Short: "Describe a topic — partitions, ISR, offsets, and configuration",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		detail, err := apiClient.KafkaTopicDetail(context.Background(), args[0])
		if err != nil {
			return cmdErr("Failed to describe topic: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(detail)
			return nil
		}

		name, _ := detail["name"].(string)
		internal, _ := detail["internal"].(bool)
		internalLabel := ""
		if internal {
			internalLabel = "  " + dimStyle.Render("(internal)")
		}
		partitions := fmt.Sprintf("%v", detail["partitions"])
		rf := fmt.Sprintf("%v", detail["replicationFactor"])
		output.Banner("Topic: "+name+internalLabel,
			fmt.Sprintf("Partitions: %s  │  Replication Factor: %s", partitions, rf))

		if configs, ok := detail["configs"].(map[string]interface{}); ok && len(configs) > 0 {
			output.SubHeader("Configuration")
			configRows := make([][]string, 0, len(configs))
			for k, v := range configs {
				configRows = append(configRows, []string{k, fmt.Sprintf("%v", v)})
			}
			output.Table([]string{"Config", "Value"}, configRows)
		}

		if piRaw, ok := detail["partitionInfo"].([]interface{}); ok && len(piRaw) > 0 {
			underReplicated := 0
			for _, p := range piRaw {
				if pm, ok := p.(map[string]interface{}); ok {
					if ur, _ := pm["underReplicated"].(bool); ur {
						underReplicated++
					}
				}
			}

			piLabel := fmt.Sprintf("Partitions (%d)", len(piRaw))
			if underReplicated > 0 {
				piLabel += "  — " + output.ErrorStyle.Render(fmt.Sprintf("%d under-replicated", underReplicated))
			}
			output.SubHeader(piLabel)

			rows := make([][]string, 0, len(piRaw))
			for _, p := range piRaw {
				pm, ok := p.(map[string]interface{})
				if !ok {
					continue
				}
				ur := ""
				if underRepl, _ := pm["underReplicated"].(bool); underRepl {
					ur = warnBadge("⚠ YES")
				} else {
					ur = healthyBadge("✓")
				}
				rows = append(rows, []string{
					fmt.Sprintf("%v", pm["partition"]),
					fmt.Sprintf("%v", pm["leader"]),
					fmt.Sprintf("%v", pm["replicas"]),
					fmt.Sprintf("%v", pm["isr"]),
					ur,
				})
			}
			output.Table([]string{"Partition", "Leader", "Replicas", "ISR", "Health"}, rows)
		}
		return nil
	},
}

var kafkaGroupsCmd = &cobra.Command{
	Use:   "groups",
	Short: "List consumer groups with state, members, and lag summary",
	RunE: func(cmd *cobra.Command, args []string) error {
		groups, err := apiClient.KafkaGroups(context.Background())
		if err != nil {
			return cmdErr("Failed to list consumer groups: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(groups)
			return nil
		}

		output.Header(fmt.Sprintf("Consumer Groups (%d)", len(groups)))
		if len(groups) == 0 {
			output.Hint("No consumer groups found.")
			return nil
		}

		rows := make([][]string, 0, len(groups))
		for _, g := range groups {
			state := fmt.Sprintf("%v", g["state"])
			stateLabel := state
			switch strings.ToUpper(state) {
			case "STABLE":
				stateLabel = healthyBadge("● STABLE")
			case "EMPTY":
				stateLabel = dimStyle.Render("○ EMPTY")
			case "DEAD", "DEAD_MEMBER":
				stateLabel = errorBadge("✖ DEAD")
			}

			members := fmt.Sprintf("%v", g["members"])
			rows = append(rows, []string{fmt.Sprintf("%v", g["groupId"]), stateLabel, members})
		}
		output.Table([]string{"Group ID", "State", "Members"}, rows)
		return nil
	},
}

var kafkaGroupCmd = &cobra.Command{
	Use:   "group <id>",
	Short: "Describe a consumer group with per-partition offsets and lag",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		detail, err := apiClient.KafkaGroupDetail(context.Background(), args[0])
		if err != nil {
			return cmdErr("Failed to describe consumer group: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(detail)
			return nil
		}

		groupID := fmt.Sprintf("%v", detail["groupId"])
		state := strings.ToUpper(fmt.Sprintf("%v", detail["state"]))
		members := fmt.Sprintf("%v", detail["members"])
		totalLag := fmt.Sprintf("%v", detail["totalLag"])

		stateLabel := state
		if state == "STABLE" {
			stateLabel = healthyBadge("● STABLE")
		} else if state == "EMPTY" {
			stateLabel = dimStyle.Render("○ EMPTY")
		} else {
			stateLabel = warnBadge("⚠ " + state)
		}

		output.Banner("Group: "+groupID,
			fmt.Sprintf("State: %s  │  Members: %s  │  Total Lag: %s", stateLabel, members, highlightLag(totalLag)))

		if offsets, ok := detail["offsets"].([]interface{}); ok && len(offsets) > 0 {
			output.SubHeader(fmt.Sprintf("Partition Offsets (%d)", len(offsets)))
			rows := make([][]string, 0, len(offsets))
			for _, o := range offsets {
				om, ok := o.(map[string]interface{})
				if !ok {
					continue
				}
				lag := fmt.Sprintf("%v", om["lag"])
				rows = append(rows, []string{
					fmt.Sprintf("%v", om["topic"]),
					fmt.Sprintf("%v", om["partition"]),
					fmt.Sprintf("%v", om["currentOffset"]),
					fmt.Sprintf("%v", om["endOffset"]),
					highlightLag(lag),
				})
			}
			output.Table([]string{"Topic", "Partition", "Current Offset", "End Offset", "Lag"}, rows)
		}
		return nil
	},
}

var (
	consumeOffset string
	consumeLimit  int
	consumeFollow bool
)

var kafkaConsumeCmd = &cobra.Command{
	Use:   "consume <topic>",
	Short: "Fetch records from a topic (latest N records, or tail with --follow / -f)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		topic := args[0]

		if consumeFollow {
			return consumeTail(topic)
		}

		records, err := apiClient.KafkaConsume(context.Background(), topic, consumeOffset, consumeLimit)
		if err != nil {
			return cmdErr("Failed to consume from topic: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(records)
			return nil
		}

		output.Banner(fmt.Sprintf("Topic: %s", topic),
			fmt.Sprintf("%d records  │  offset: %s", len(records), consumeOffset))

		if len(records) == 0 {
			output.Hint("No records found. Try --offset earliest to read from the beginning.")
			return nil
		}

		rows := make([][]string, 0, len(records))
		for _, r := range records {
			key := fmt.Sprintf("%v", r.Key)
			if key == "<nil>" {
				key = dimStyle.Render("(null)")
			}
			value := fmt.Sprintf("%v", r.Value)
			if len(value) > 80 {
				value = value[:77] + "..."
			}
			rows = append(rows, []string{
				fmt.Sprintf("%v", r.Partition),
				fmt.Sprintf("%v", r.Offset),
				formatMs(r.Timestamp),
				key,
				value,
			})
		}
		output.Table([]string{"Part.", "Offset", "Timestamp", "Key", "Value"}, rows)
		return nil
	},
}

var (
	produceKey   string
	produceValue string
)

var kafkaProduceCmd = &cobra.Command{
	Use:   "produce <topic>",
	Short: "Produce a record to a topic (from flag or stdin)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		topic := args[0]
		value := produceValue

		if value == "" {
			fmt.Print(promptStyle.Render("Value (or pipe via stdin): "))
			scanner := bufio.NewScanner(os.Stdin)
			if scanner.Scan() {
				value = scanner.Text()
			}
		}

		if value == "" {
			return cmdErr("No value provided. Use --value or pipe via stdin.")
		}

		meta, err := apiClient.KafkaProduce(context.Background(), topic, produceKey, value)
		if err != nil {
			return cmdErr("Failed to produce record: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(meta)
			return nil
		}

		output.Success(fmt.Sprintf(
			"Produced to %s — partition %v, offset %v",
			leaderStyle.Render(meta.Topic),
			meta.Partition,
			meta.Offset,
		))
		return nil
	},
}

func consumeTail(topic string) error {
	output.SubHeader(fmt.Sprintf("Tailing %s  (Ctrl+C to stop)", topic))
	fmt.Println()
	seen := map[string]bool{}
	for {
		records, err := apiClient.KafkaConsume(context.Background(), topic, "latest", 20)
		if err != nil {
			return cmdErr("Consume error: " + err.Error())
		}
		for _, r := range records {
			key := fmt.Sprintf("%v+%v", r.Partition, r.Offset)
			if seen[key] {
				continue
			}
			seen[key] = true
			keyStr := dimStyle.Render(fmt.Sprintf("[p%v @%v]", r.Partition, r.Offset))
			valueStr := fmt.Sprintf("%v", r.Value)
			fmt.Printf("%s %s\n", keyStr, valueStr)
		}
	}
}

var (
	leaderStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	dimStyle    = lipgloss.NewStyle().Faint(true)
	promptStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
)

func healthyBadge(s string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("82")).Render(s)
}

func warnBadge(s string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render(s)
}

func errorBadge(s string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(s)
}

func highlightLag(lag string) string {
	if lag == "0" || lag == "0.0" || lag == "0.000000e+00" {
		return healthyBadge("0")
	}
	return warnBadge(lag)
}

func formatMs(ts interface{}) string {
	if ts == nil {
		return "-"
	}
	ms := fmt.Sprintf("%v", ts)
	if len(ms) >= 13 {
		return ms[:10] + "…"
	}
	return ms
}

func init() {
	kafkaTopicsCmd.Flags().String("filter", "", "Filter topics by substring match")

	kafkaConsumeCmd.Flags().StringVar(&consumeOffset, "offset", "latest", "Offset to start from: latest or earliest")
	kafkaConsumeCmd.Flags().IntVar(&consumeLimit, "limit", 20, "Maximum number of records to fetch")
	kafkaConsumeCmd.Flags().BoolVarP(&consumeFollow, "follow", "f", false, "Tail the topic (like tail -f)")

	kafkaProduceCmd.Flags().StringVar(&produceKey, "key", "", "Record key (optional)")
	kafkaProduceCmd.Flags().StringVarP(&produceValue, "value", "v", "", "Record value to produce")

	kafkaCmd.AddCommand(kafkaBrokersCmd)
	kafkaCmd.AddCommand(kafkaTopicsCmd)
	kafkaCmd.AddCommand(kafkaTopicCmd)
	kafkaCmd.AddCommand(kafkaGroupsCmd)
	kafkaCmd.AddCommand(kafkaGroupCmd)
	kafkaCmd.AddCommand(kafkaConsumeCmd)
	kafkaCmd.AddCommand(kafkaProduceCmd)
	rootCmd.AddCommand(kafkaCmd)
}
