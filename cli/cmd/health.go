package cmd

import (
	"context"
	"fmt"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var healthCmd = &cobra.Command{
	Use:   "health",
	Short: "Show Kates system health and Kafka connectivity",
	RunE: func(cmd *cobra.Command, args []string) error {
		result, err := apiClient.Health(context.Background())
		if err != nil {
			return cmdErr("Failed to check health: " + err.Error())
		}

		if outputMode == "json" {
			output.JSON(result)
			return nil
		}

		output.Banner("Kates Health Dashboard", "System Status: "+result.Status)

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
			var perfRows, tuneRows [][]string
			headers := []string{"Test", "Records", "Partitions", "Producers", "Acks", "Compress"}

			for name, cfg := range result.Tests {
				row := []string{
					name,
					fmt.Sprintf("%d", cfg.NumRecords),
					fmt.Sprintf("%d", cfg.Partitions),
					fmt.Sprintf("%d", cfg.NumProducers),
					cfg.Acks,
					cfg.CompressionType,
				}
				if len(name) > 5 && name[:5] == "tune_" {
					tuneRows = append(tuneRows, row)
				} else {
					perfRows = append(perfRows, row)
				}
			}

			if len(perfRows) > 0 {
				output.SubHeader("Performance Tests")
				output.Table(headers, perfRows)
			}
			if len(tuneRows) > 0 {
				output.SubHeader("Tuning Tests")
				output.Table(headers, tuneRows)
			}
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(healthCmd)
}
