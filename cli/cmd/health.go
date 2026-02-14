package cmd

import (
	"fmt"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Show KATES system health and Kafka connectivity",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.Health()
		if err != nil {
			output.Error("Failed to check health: " + err.Error())
			return nil
		}

		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		status := strVal(result, "status")
		output.Banner("KATES Health Dashboard", "System Status: "+status)

		// Engine
		if eng, ok := result["engine"].(map[string]interface{}); ok {
			output.SubHeader("Engine")
			output.KeyValue("Active Backend", strVal(eng, "activeBackend"))
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
			output.KeyValue("Status", output.StatusBadge(strVal(kafka, "status")))
			output.KeyValue("Bootstrap", strVal(kafka, "bootstrapServers"))
			output.KeyValue("Message", strVal(kafka, "message"))
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
						strVal(m, "acks"),
						strVal(m, "compressionType"),
					})
				}
			}
			output.Table([]string{"Test", "Records", "Partitions", "Producers", "Acks", "Compress"}, rows)
		}

		return nil
	},
}

func strVal(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

func init() {
	rootCmd.AddCommand(healthCmd)
}
