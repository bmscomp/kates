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

		output.Banner("KATES Health Dashboard", "System Status: "+result.Status)

		if eng := result.Engine; eng != nil {
			output.SubHeader("Engine")
			output.KeyValue("Active Backend", eng.ActiveBackend)
			if len(eng.AvailableBackends) > 0 {
				output.KeyValue("Available", fmt.Sprintf("%v", eng.AvailableBackends))
			}
		}

		if kafka := result.Kafka; kafka != nil {
			output.SubHeader("Kafka Cluster")
			output.KeyValue("Status", output.StatusBadge(kafka.Status))
			output.KeyValue("Bootstrap", kafka.BootstrapServers)
			output.KeyValue("Message", kafka.Message)
		}

		if len(result.Tests) > 0 {
			output.SubHeader("Test Configurations")
			rows := make([][]string, 0, len(result.Tests))
			for name, cfg := range result.Tests {
				rows = append(rows, []string{
					name,
					fmt.Sprintf("%d", cfg.NumRecords),
					fmt.Sprintf("%d", cfg.Partitions),
					fmt.Sprintf("%d", cfg.NumProducers),
					cfg.Acks,
					cfg.CompressionType,
				})
			}
			output.Table([]string{"Test", "Records", "Partitions", "Producers", "Acks", "Compress"}, rows)
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(healthCmd)
}
