package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var tuneStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
var tuneBest = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
var tuneHeader = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))

var tuneCmd = &cobra.Command{
	Use:   "tune",
	Short: "Configuration & tuning tests",
	Long:  "Run tuning tests, each of which measures one Kafka configuration",
}

var tuneRunCmd = &cobra.Command{
	Use:   "run <type>",
	Short: "Run a tuning test",
	Long: `Execute a tuning test: one produce run with a single configuration, the
backend's defaults for the type. It measures that one setting and compares
none; to compare settings, use kates lab.
Available types: TUNE_REPLICATION, TUNE_ACKS, TUNE_BATCHING, TUNE_COMPRESSION, TUNE_PARTITIONS

With -o json it prints the run the backend created, as kates test create does.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		testType := strings.ToUpper(args[0])
		if !strings.HasPrefix(testType, "TUNE_") {
			testType = "TUNE_" + testType
		}

		req := &client.CreateTestRequest{
			TestType: testType,
		}
		run, err := apiClient.CreateTest(ctx, req)
		if err != nil {
			return fmt.Errorf("create tuning test: %w", err)
		}

		if outputMode == "json" {
			output.JSON(run)
			return nil
		}
		fmt.Println(tuneStyle.Render("⚙ Tuning test submitted"))
		fmt.Printf("  Type:   %s\n", testType)
		fmt.Printf("  Run ID: %s\n", run.ID)
		fmt.Println()
		fmt.Printf("  View results: kates report show %s\n", run.ID)
		return nil
	},
}

var tuneTypesCmd = &cobra.Command{
	Use:   "types",
	Short: "List available tuning tests",
	RunE: func(cmd *cobra.Command, args []string) error {
		types, err := apiClient.TuningTypes(context.Background())
		if err != nil {
			return fmt.Errorf("list tuning types: %w", err)
		}

		if outputMode == "json" {
			output.JSON(types)
			return nil
		}
		fmt.Println(tuneHeader.Render("  Available Tuning Tests"))
		fmt.Println()
		for _, t := range types {
			fmt.Printf("  %s\n", tuneBest.Render(t.Type))
			fmt.Printf("    Parameter: %s\n", t.Parameter)
			fmt.Printf("    Steps:     %d\n", t.Steps)
			fmt.Printf("    %s\n\n", t.Description)
		}
		return nil
	},
}

func init() {
	tuneCmd.AddCommand(tuneRunCmd, tuneTypesCmd)
	rootCmd.AddCommand(tuneCmd)
}
