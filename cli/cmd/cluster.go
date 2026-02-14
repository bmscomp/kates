package cmd

import (
	"context"
	"fmt"

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

		output.Banner("Kafka Cluster", "Cluster ID: "+mapStr(result, "clusterId"))

		output.SubHeader("Overview")
		output.KeyValue("Broker Count", mapStr(result, "brokerCount"))

		// Controller
		if ctrl, ok := result["controller"].(map[string]interface{}); ok {
			output.SubHeader("Controller")
			output.KeyValue("Node ID", mapStr(ctrl, "id"))
			output.KeyValue("Host", mapStr(ctrl, "host"))
			output.KeyValue("Port", mapStr(ctrl, "port"))
			output.KeyValue("Rack / AZ", mapStr(ctrl, "rack"))
		}

		// Brokers
		if brokers, ok := result["brokers"].([]interface{}); ok && len(brokers) > 0 {
			output.SubHeader(fmt.Sprintf("Brokers (%d)", len(brokers)))
			rows := make([][]string, 0, len(brokers))
			for _, b := range brokers {
				if bm, ok := b.(map[string]interface{}); ok {
					isCtrl := ""
					if ctrl, ok := result["controller"].(map[string]interface{}); ok {
						if mapStr(bm, "id") == mapStr(ctrl, "id") {
							isCtrl = "★"
						}
					}
					rows = append(rows, []string{
						mapStr(bm, "id"),
						mapStr(bm, "host"),
						mapStr(bm, "port"),
						mapStr(bm, "rack"),
						isCtrl,
					})
				}
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

		rows := make([][]string, len(topics))
		for i, t := range topics {
			rows[i] = []string{fmt.Sprintf("%d", i+1), t}
		}
		output.Table([]string{"#", "Topic Name"}, rows)
		return nil
	},
}

func init() {
	clusterCmd.AddCommand(clusterInfoCmd)
	clusterCmd.AddCommand(clusterTopicsCmd)
	rootCmd.AddCommand(clusterCmd)
}
