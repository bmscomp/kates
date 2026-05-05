package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/klster/kates-cli/output"
	"github.com/klster/kates-cli/pkg/detect"
	"github.com/spf13/cobra"
)

var detectCmd = &cobra.Command{
	Use:     "detect",
	Aliases: []string{"preflight-cluster", "cluster-check"},
	Short:   "Deep cluster compatibility report for 3-AZ Kafka",
	Long: `Introspects the current Kubernetes cluster and produces a detailed
compatibility report for deploying a 3-AZ Kafka cluster with Strimzi.

Exit codes:
  0  — compatible (or compatible with warnings when --fail-on-warning is not set)
  1  — compatible but warnings detected (only with --fail-on-warning)
  2  — incompatible (check failures detected)`,
	Example: `  kates detect
  kates detect -f values.yaml
  kates detect --output json
  kates detect --fail-on-warning          # CI/CD gate: exit 1 on warnings
  kates detect --fail-on-error --quiet    # CI/CD gate: minimal output`,
	RunE: runDetect,
}

var (
	detectValuesFile string
	failOnWarning    bool
	failOnError      bool
	quietMode        bool
	outputFile       string
)

func init() {
	detectCmd.Flags().StringVarP(&detectValuesFile, "values", "f", "", "Path to custom values.yaml for dynamic resource budgeting")
	detectCmd.Flags().BoolVar(&failOnWarning, "fail-on-warning", false, "Exit with code 1 if warnings are detected (for CI/CD)")
	detectCmd.Flags().BoolVar(&failOnError, "fail-on-error", false, "Exit with code 2 if compatibility checks fail (for CI/CD)")
	detectCmd.Flags().BoolVar(&quietMode, "quiet", false, "Only print the verdict, not the full report")
	detectCmd.Flags().StringVar(&outputFile, "output-file", "", "Write report to file (supports .md, .json)")
	rootCmd.AddCommand(detectCmd)
}

func runDetect(cmd *cobra.Command, args []string) error {
	executor := detect.NewOSExecutor()
	collector := detect.NewCollector(executor)
	analyzer := detect.NewAnalyzer(executor)

	if err := collector.Preflight(); err != nil {
		output.Error(err.Error())
		if failOnError {
			os.Exit(2)
		}
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

	// Show spinner while collecting (if not JSON or quiet mode)
	if outputMode != "json" && !quietMode {
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
		if failOnError {
			os.Exit(2)
		}
		return nil
	}

	// Analyze raw data into final report
	analyzer.Analyze(report, reqs)

	// Render output
	switch {
	case outputMode == "json":
		detect.RenderJSON(report)
	case outputMode == "markdown" || outputMode == "md":
		detect.RenderMarkdown(report, os.Stdout)
	case quietMode:
		// Quiet mode: only print verdict summary
		if report.Verdict.Fails > 0 {
			output.Error(fmt.Sprintf("INCOMPATIBLE: %d check(s) failed, %d warning(s)", report.Verdict.Fails, report.Verdict.Warns))
		} else if report.Verdict.Warns > 0 {
			output.Warn(fmt.Sprintf("PARTIAL: compatible with %d warning(s)", report.Verdict.Warns))
		} else {
			output.Success("COMPATIBLE: cluster can run a 3-AZ Kafka deployment")
		}
	default:
		detect.RenderTUI(report)
	}

	// Write to file if requested
	if outputFile != "" {
		f, err := os.Create(outputFile)
		if err != nil {
			output.Error(fmt.Sprintf("Failed to create output file: %v", err))
		} else {
			defer f.Close()
			if strings.HasSuffix(outputFile, ".json") {
				detect.RenderJSONTo(report, f)
			} else {
				detect.RenderMarkdown(report, f)
			}
			output.Success(fmt.Sprintf("Report written to %s", outputFile))
		}
	}

	// CI/CD exit codes
	if failOnError && report.Verdict.Fails > 0 {
		os.Exit(2)
	}
	if failOnWarning && report.Verdict.Warns > 0 {
		os.Exit(1)
	}

	return nil
}
