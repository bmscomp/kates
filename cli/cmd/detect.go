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
  kates detect --fail-on-error --quiet    # CI/CD gate: minimal output
  kates detect --generate-values          # print generated values.yaml to stdout
  kates detect --generate-values --values-output values.yaml
  kates detect --generate-values -f base.yaml --values-output values.yaml`,
	RunE: runDetect,
}

var (
	detectValuesFile string
	failOnWarning    bool
	failOnError      bool
	quietMode        bool
	outputFile       string
	generateValues   bool
	valuesOutput     string
	clusterName      string
	dryRun           bool
)

func init() {
	detectCmd.Flags().StringVarP(&detectValuesFile, "values", "f", "", "Path to custom/base values.yaml (for budgeting or merge base)")
	detectCmd.Flags().BoolVar(&failOnWarning, "fail-on-warning", false, "Exit with code 1 if warnings are detected (for CI/CD)")
	detectCmd.Flags().BoolVar(&failOnError, "fail-on-error", false, "Exit with code 2 if compatibility checks fail (for CI/CD)")
	detectCmd.Flags().BoolVar(&quietMode, "quiet", false, "Only print the verdict, not the full report")
	detectCmd.Flags().StringVar(&outputFile, "output-file", "", "Write report to file (supports .md, .json)")
	detectCmd.Flags().BoolVar(&generateValues, "generate-values", false, "Generate a Helm values.yaml from detected cluster config")
	detectCmd.Flags().StringVar(&valuesOutput, "values-output", "", "Write generated values to file (default: stdout)")
	detectCmd.Flags().StringVar(&clusterName, "cluster-name", "krafter", "Kafka cluster name for generated values")
	detectCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview values to stdout without writing a file")
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

	// ── Generate values mode ────────────────────────────────────────────────
	if generateValues {
		if dryRun || valuesOutput == "" {
			// Dry-run or no output file: print to stdout
			if detectValuesFile != "" {
				baseData, err := os.ReadFile(detectValuesFile)
				if err != nil {
					output.Error(fmt.Sprintf("Failed to read base values: %v", err))
					return nil
				}
				detect.RenderValuesFromBase(report, clusterName, baseData, os.Stdout)
			} else {
				detect.RenderValues(report, clusterName, os.Stdout)
			}
			return nil
		}

		// Write to file
		f, err := os.Create(valuesOutput)
		if err != nil {
			output.Error(fmt.Sprintf("Failed to create values file: %v", err))
			return nil
		}
		defer f.Close()

		if detectValuesFile != "" {
			baseData, err := os.ReadFile(detectValuesFile)
			if err != nil {
				output.Error(fmt.Sprintf("Failed to read base values: %v", err))
				return nil
			}
			detect.RenderValuesFromBase(report, clusterName, baseData, f)
		} else {
			detect.RenderValues(report, clusterName, f)
		}
		output.Success(fmt.Sprintf("Values written to %s", valuesOutput))
		output.Hint(fmt.Sprintf("Deploy with: helm upgrade --install %s charts/kafka-cluster -n kafka -f %s --timeout 10m --wait", clusterName, valuesOutput))
		return nil
	}

	// ── Standard report mode ────────────────────────────────────────────────
	switch {
	case outputMode == "json":
		detect.RenderJSON(report)
	case outputMode == "markdown" || outputMode == "md":
		detect.RenderMarkdown(report, os.Stdout)
	case quietMode:
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

	// Write report to file if requested
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
