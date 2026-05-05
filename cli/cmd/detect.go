package cmd

import (
	"context"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/klster/kates-cli/output"
	"github.com/klster/kates-cli/pkg/detect"
	"github.com/spf13/cobra"
)

var detectCmd = &cobra.Command{
	Use:     "detect",
	Aliases: []string{"preflight-cluster", "cluster-check"},
	Short:   "Deep cluster compatibility report for 3-AZ Kafka",
	Example: "  kates detect\n  kates detect -f values.yaml\n  kates detect --output json",
	RunE:    runDetect,
}

var detectValuesFile string

func init() {
	detectCmd.Flags().StringVarP(&detectValuesFile, "values", "f", "", "Path to custom values.yaml for dynamic resource budgeting")
	rootCmd.AddCommand(detectCmd)
}

func runDetect(cmd *cobra.Command, args []string) error {
	executor := detect.NewOSExecutor()
	collector := detect.NewCollector(executor)
	analyzer := detect.NewAnalyzer(executor)

	if err := collector.Preflight(); err != nil {
		output.Error(err.Error())
		return nil
	}

	var reqs detect.ParsedReqs
	if detectValuesFile != "" {
		data, err := os.ReadFile(detectValuesFile)
		if err != nil {
			output.Error(fmt.Sprintf("Failed to read values file: %v", err))
			return nil
		}
		reqs = detect.ParseValuesYAML(data)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var report *detect.DetectReport
	var collectErr error

	// Start collection in background
	go func() {
		report, collectErr = collector.Collect(ctx)
		cancel() // signal spinner to stop
	}()

	// Show spinner while collecting (if not JSON mode)
	if outputMode != "json" {
		p := tea.NewProgram(detect.NewSpinnerModel())
		go func() {
			<-ctx.Done()
			p.Quit()
		}()
		p.Run()
	} else {
		<-ctx.Done()
	}

	if collectErr != nil {
		output.Error(fmt.Sprintf("Cluster introspection failed: %v", collectErr))
		return nil
	}

	// Analyze raw data into final report
	analyzer.Analyze(report, reqs)

	// Render output
	if outputMode == "json" {
		detect.RenderJSON(report)
	} else {
		detect.RenderTUI(report)
	}

	return nil
}
