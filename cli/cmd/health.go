package cmd

import (
	"context"
	"fmt"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Show KATES system health and Kafka connectivity",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.Health(context.Background())
		if err != nil {
			return cmdErr("Failed to check health: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		status := mapStrEmpty(result, "status")
		output.Banner("KATES Health Dashboard", "System Status: "+status)

		// Engine
		if eng, ok := result["engine"].(map[string]interface{}); ok {
			output.SubHeader("Engine")
			output.KeyValue("Active Backend", mapStrEmpty(eng, "activeBackend"))
			if backends, ok := eng["availableBackends"].([]interface{}); ok {
				names := make([]string, len(backends))
				for i, b := range backends {
					names[i] = fmt.Sprintf("%v", b)
				}
				output.KeyValue("Available", fmt.Sprintf("%v", names))
			}
		}

		// Kafka
		if kafka, ok := result["kafka"].(map[string]interface{}); ok {
			output.SubHeader("Kafka Cluster")
			output.KeyValue("Status", output.StatusBadge(mapStrEmpty(kafka, "status")))
			output.KeyValue("Bootstrap", mapStrEmpty(kafka, "bootstrapServers"))
			output.KeyValue("Message", mapStrEmpty(kafka, "message"))
		}

		// Test configs as table
		if tests, ok := result["tests"].(map[string]interface{}); ok {
			output.SubHeader("Test Configurations")
			rows := make([][]string, 0, len(tests))
			for name, cfg := range tests {
				if m, ok := cfg.(map[string]interface{}); ok {
					rows = append(rows, []string{
						name,
						fmt.Sprintf("%.0f", numVal(m, "numRecords")),
						fmt.Sprintf("%.0f", numVal(m, "partitions")),
						fmt.Sprintf("%.0f", numVal(m, "numProducers")),
						mapStrEmpty(m, "acks"),
						mapStrEmpty(m, "compressionType"),
					})
				}
			}
			output.Table([]string{"Test", "Records", "Partitions", "Producers", "Acks", "Compress"}, rows)
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(healthCmd)
}
