package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/detect"
	tea "github.com/charmbracelet/bubbletea"
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
	reservePct       float64
	benchStorage     bool
	benchNetwork     bool
	benchDNS         bool
)

func init() {
	detect.ChartRef = chartRef
	detectCmd.Flags().StringVarP(&detectValuesFile, "values", "f", "", "Path to custom/base values.yaml (for budgeting or merge base)")
	detectCmd.Flags().BoolVar(&failOnWarning, "fail-on-warning", false, "Exit with code 1 if warnings are detected (for CI/CD)")
	detectCmd.Flags().BoolVar(&failOnError, "fail-on-error", false, "Exit with code 2 if compatibility checks fail (for CI/CD)")
	detectCmd.Flags().BoolVar(&quietMode, "quiet", false, "Only print the verdict, not the full report")
	detectCmd.Flags().StringVar(&outputFile, "output-file", "", "Write report to file (supports .md, .json, .pdf)")
	detectCmd.Flags().BoolVar(&generateValues, "generate-values", false, "Generate a Helm values.yaml from detected cluster config")
	detectCmd.Flags().StringVar(&valuesOutput, "values-output", "", "Write generated values to file (default: stdout)")
	detectCmd.Flags().StringVar(&clusterName, "cluster-name", "krafter", "Kafka cluster name for generated values")
	detectCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview values to stdout without writing a file")
	detectCmd.Flags().Float64Var(&reservePct, "reserve", 0.30, "Reserve percentage of cluster resources (0.30 = 30% reserved, 70% for Kafka)")
	detectCmd.Flags().BoolVar(&benchStorage, "bench-storage", false, "Run active live storage benchmarking using ephemeral fio pods (takes ~30s)")
	detectCmd.Flags().BoolVar(&benchNetwork, "bench-network", false, "Run active network throughput sweeps between AZs using iperf3 (~10s)")
	detectCmd.Flags().BoolVar(&benchDNS, "bench-dns", false, "Run active CoreDNS latency and throttling sweeps using parallel DNS queries (~5s)")
	addLabelsFileFlag(detectCmd)
	// Ctrl-C stops the collection, kills its kubectl calls and deletes the
	// probe namespaces, rather than ending kates with them left behind.
	interruptible(detectCmd)
	rootCmd.AddCommand(detectCmd)
}

func runDetect(cmd *cobra.Command, args []string) error {
	namespaceLabels, _, err := namespaceUserLabels()
	if err != nil {
		return err
	}
	executor := detect.NewOSExecutor()
	collector := detect.NewCollector(executor)
	collector.NamespaceLabels = namespaceLabels
	collector.BenchStorage = benchStorage
	collector.BenchNetwork = benchNetwork
	collector.BenchDNS = benchDNS
	analyzer := detect.NewAnalyzer(executor)
	ctx := commandContext(cmd)

	// Exit codes go through exitError, not os.Exit: Execute exits with the
	// code after RunE returns, so deferred Close and cancel calls run.
	if err := collector.PreflightContext(ctx); err != nil {
		output.Error(err.Error())
		if failOnError {
			return &exitError{code: 2}
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

	report, collectErr := collectReport(ctx, collector)
	if errors.Is(collectErr, context.Canceled) {
		interruptNote("Stopped: cluster introspection was interrupted; the probe namespaces it created are being deleted.")
		return errInterrupted
	}
	if collectErr != nil {
		output.Error(fmt.Sprintf("Cluster introspection failed: %v", collectErr))
		if failOnError {
			return &exitError{code: 2}
		}
		return nil
	}

	if report == nil {
		output.Error("Cluster introspection returned no data")
		return fmt.Errorf("nil report from collector")
	}

	// Analyze raw data into final report
	analyzer.Analyze(report, reqs)

	// Compute capacity budget for TUI display and values generation
	capGen := detect.NewValuesGeneratorWithReserve(report, clusterName, reservePct)
	report.Capacity = capGen.Cap

	if generateValues {
		writeGeneratedValues(report)
		return nil
	}

	// ── Standard report mode ────────────────────────────────────────────────
	switch {
	case outputMode == "json":
		detect.RenderJSON(report)
	case outputMode == "markdown" || outputMode == "md":
		detect.RenderMarkdown(report, os.Stdout)
	case quietMode:
		printDetectVerdict(report)
	default:
		detect.RenderTUI(report)
	}

	if outputFile != "" {
		writeDetectReportFile(report, outputFile)
	}

	// CI/CD exit codes.
	if failOnError && report.Verdict.Fails > 0 {
		return &exitError{code: 2}
	}
	if failOnWarning && report.Verdict.Warns > 0 {
		return &exitError{code: 1}
	}

	return nil
}

// collectReport runs the collector, behind a spinner unless the output is
// JSON or quiet. The collection stops, its kubectl calls killed, when ctx
// ends (SIGINT, SIGTERM) or when the user stops the spinner (Ctrl-C in a
// terminal reaches the spinner as a key, not a signal); Collect then
// returns context.Canceled.
func collectReport(ctx context.Context, collector *detect.Collector) (*detect.DetectReport, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var p *tea.Program
	if outputMode != "json" && !quietMode {
		p = tea.NewProgram(detect.NewSpinnerModel())
		collector.OnProgress = func(msg string) {
			p.Send(detect.ProgressMsg(msg))
		}
	}

	var report *detect.DetectReport
	var err error
	done := make(chan struct{})
	go func() {
		report, err = collector.Collect(ctx)
		close(done) // happens-after the assignments above
	}()

	if p != nil {
		go func() {
			<-done
			p.Quit()
		}()
		_, _ = p.Run()
		// The spinner ends when the collection is done, or when the user
		// stopped it: then this stops the collection.
		cancel()
	}
	<-done
	return report, err
}

// printDetectVerdict is the --quiet report: the verdict line only, in the
// words every other view uses.
func printDetectVerdict(report *detect.DetectReport) {
	v := detect.VerdictOf(report.Verdict)
	msg := v.Label + ": " + v.Summary
	switch v.Status {
	case detect.StatusFail:
		output.Error(msg)
	case detect.StatusWarn:
		output.Warn(msg)
	default:
		output.Success(msg)
	}
}

// writeGeneratedValues is --generate-values: the values file, on stdout or
// in --values-output, merged over the -f base when there is one.
func writeGeneratedValues(report *detect.DetectReport) {
	var baseData []byte
	if detectValuesFile != "" {
		data, err := os.ReadFile(detectValuesFile)
		if err != nil {
			output.Error(fmt.Sprintf("Failed to read base values: %v", err))
			return
		}
		baseData = data
	}
	render := func(w io.Writer) error {
		if baseData != nil {
			return detect.RenderValuesFromBaseWithReserve(report, clusterName, baseData, reservePct, w)
		}
		return detect.RenderValuesWithReserve(report, clusterName, reservePct, w)
	}

	if dryRun || valuesOutput == "" {
		if err := render(os.Stdout); err != nil {
			output.Error(fmt.Sprintf("Failed to render values: %v", err))
		}
		return
	}
	if err := writeFileWith(valuesOutput, render); err != nil {
		output.Error(fmt.Sprintf("Failed to write values file: %v", err))
		return
	}
	output.Success(fmt.Sprintf("Values written to %s", valuesOutput))
	output.Hint(fmt.Sprintf("Deploy with: helm upgrade --install %s %s -n kafka -f %s --timeout 10m --wait", clusterName, chartRef("kafka-cluster"), valuesOutput))
}

// writeDetectReportFile is --output-file: the report in the format the
// extension names, Markdown for an unknown one, and Markdown plus PDF for
// a path without an extension.
func writeDetectReportFile(report *detect.DetectReport, path string) {
	markdown := func(w io.Writer) error {
		detect.RenderMarkdown(report, w)
		return nil
	}
	write := func(path string, render func(io.Writer) error) {
		if err := writeFileWith(path, render); err != nil {
			output.Error(fmt.Sprintf("Failed to write report %s: %v", path, err))
			return
		}
		output.Success(fmt.Sprintf("Report written to %s", path))
	}
	writePDF := func(path string) {
		if err := detect.RenderPDF(report, path); err != nil {
			output.Error(fmt.Sprintf("Failed to write PDF report: %v", err))
			return
		}
		output.Success(fmt.Sprintf("Report written to %s", path))
	}

	switch strings.ToLower(filepath.Ext(path)) {
	case ".pdf":
		writePDF(path)
	case ".json":
		write(path, func(w io.Writer) error {
			detect.RenderJSONTo(report, w)
			return nil
		})
	case ".html", ".htm":
		write(path, func(w io.Writer) error { return detect.RenderHTML(report, w) })
	case "":
		write(path+".md", markdown)
		writePDF(path + ".pdf")
	default: // .md, .markdown, and anything else
		write(path, markdown)
	}
}

// writeFileWith creates path and renders into it, and reports a failed
// close: a full disk shows up there.
func writeFileWith(path string, render func(io.Writer) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := render(f); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
