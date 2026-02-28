package cmd

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

type costModel struct {
	name         string
	storagePerGB float64
	networkIn    float64
	networkOut   float64
	brokerHourly float64
}

var costModels = map[string]costModel{
	"aws":       {name: "AWS (us-east-1)", storagePerGB: 0.10, networkIn: 0.00, networkOut: 0.09, brokerHourly: 0.48},
	"azure":     {name: "Azure (East US)", storagePerGB: 0.045, networkIn: 0.00, networkOut: 0.087, brokerHourly: 0.52},
	"gcp":       {name: "GCP (us-central1)", storagePerGB: 0.04, networkIn: 0.00, networkOut: 0.12, brokerHourly: 0.45},
	"confluent": {name: "Confluent Cloud", storagePerGB: 0.10, networkIn: 0.00, networkOut: 0.11, brokerHourly: 1.20},
}

var (
	costCloud      string
	costProducers  int
	costRecords    int
	costRecordSize int
	costDuration   int
	costBrokers    int
	costReplicas   int

	costTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#7C3AED")).
			Padding(0, 1)

	costLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6B7280")).
			Width(18)

	costValueStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#06B6D4"))

	costTotalStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#22C55E"))
)

var costCmd = &cobra.Command{
	Use:   "cost",
	Short: "Estimate cloud costs for test configurations",
}

var costEstimateCmd = &cobra.Command{
	Use:   "estimate",
	Short: "Estimate resource costs for a test run",
	Example: `  kates cost estimate --records 1000000 --record-size 1024 --duration 60
  kates cost estimate --producers 16 --records 5000000 --cloud gcp
  kates cost estimate --brokers 6 --duration 3600 --cloud confluent`,
	RunE: func(cmd *cobra.Command, args []string) error {
		model, ok := costModels[strings.ToLower(costCloud)]
		if !ok {
			return cmdErr("unknown cloud provider: " + costCloud + " (use: aws, azure, gcp, confluent)")
		}

		dataGB := float64(costRecords) * float64(costRecordSize) / (1024 * 1024 * 1024)
		storageGB := dataGB * float64(costReplicas)
		networkInGB := dataGB
		networkOutGB := dataGB * float64(costReplicas-1)
		durationHours := float64(costDuration) / 3600.0
		if durationHours < 1.0/3600 {
			durationHours = 1.0 / 3600
		}

		storageCost := storageGB * model.storagePerGB
		networkInCost := networkInGB * model.networkIn
		networkOutCost := networkOutGB * model.networkOut
		brokerCost := float64(costBrokers) * durationHours * model.brokerHourly
		total := storageCost + networkInCost + networkOutCost + brokerCost

		fmt.Println(costTitleStyle.Width(50).Render("  Cost Estimate · " + model.name))
		fmt.Println()

		printCostRow("Storage", storageGB, model.storagePerGB, storageCost)
		printCostRow("Network In", networkInGB, model.networkIn, networkInCost)
		printCostRow("Network Out", networkOutGB, model.networkOut, networkOutCost)
		fmt.Printf("  %s %s\n",
			costLabelStyle.Render("Broker Hours"),
			costValueStyle.Render(fmt.Sprintf(
				"%d × %.1fh × $%.2f/h    $%.2f",
				costBrokers, durationHours, model.brokerHourly, brokerCost)))
		fmt.Println("  " + strings.Repeat("─", 46))
		fmt.Printf("  %s %s\n\n",
			lipgloss.NewStyle().Bold(true).Width(18).Render("Total"),
			costTotalStyle.Render(fmt.Sprintf("$%.2f/run", total)))

		return nil
	},
}

func printCostRow(label string, gb, rate, cost float64) {
	fmt.Printf("  %s %s\n",
		costLabelStyle.Render(label),
		costValueStyle.Render(fmt.Sprintf(
			"%.1f GB × $%.2f/GB    $%.2f",
			math.Max(gb, 0), rate, cost)))
}

func init() {
	costEstimateCmd.Flags().StringVar(&costCloud, "cloud", "aws", "Cloud provider: aws, azure, gcp, confluent")
	costEstimateCmd.Flags().IntVar(&costProducers, "producers", 4, "Number of producers")
	costEstimateCmd.Flags().IntVar(&costRecords, "records", 100000, "Number of records")
	costEstimateCmd.Flags().IntVar(&costRecordSize, "record-size", 512, "Record size in bytes")
	costEstimateCmd.Flags().IntVar(&costDuration, "duration", 300, "Duration in seconds")
	costEstimateCmd.Flags().IntVar(&costBrokers, "brokers", 3, "Number of brokers")
	costEstimateCmd.Flags().IntVar(&costReplicas, "replicas", 3, "Replication factor")
	costCmd.AddCommand(costEstimateCmd)
	rootCmd.AddCommand(costCmd)
}
