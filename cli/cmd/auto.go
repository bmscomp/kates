package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/klster/kates-cli/output"
	"github.com/klster/kates-cli/pkg/detect"
	"github.com/spf13/cobra"
)

var (
	autoClusterName string
	autoReleaseName string
	autoReservePct  float64
	autoChartDir    string
	autoDryRun      bool
	autoBaseValues  string
)

var autoCmd = &cobra.Command{
	Use:   "auto",
	Short: "Auto-detect cluster configuration and deploy Kafka",
	Long: `Auto-detects the Kubernetes cluster configuration, generates optimal
values.yaml based on capacity and topology, and deploys the kafka-cluster
Helm chart in a single step.`,
	RunE: runAuto,
}

func init() {
	autoCmd.Flags().StringVar(&autoClusterName, "cluster-name", "krafter", "Kafka cluster name")
	autoCmd.Flags().StringVar(&autoReleaseName, "release-name", "kafka-cluster", "Helm release name")
	autoCmd.Flags().Float64Var(&autoReservePct, "reserve", 0.30, "Reserve percentage of cluster resources (0.30 = 30% reserved, 70% for Kafka)")
	autoCmd.Flags().StringVar(&autoChartDir, "chart-dir", "./charts/kafka-cluster", "Path to the kafka-cluster Helm chart directory")
	autoCmd.Flags().BoolVar(&autoDryRun, "dry-run", false, "Preview the Helm installation without executing it")
	autoCmd.Flags().StringVarP(&autoBaseValues, "values", "f", "", "Path to base values.yaml to merge with detected values")
	rootCmd.AddCommand(autoCmd)
}

func runAuto(cmd *cobra.Command, args []string) error {
	output.Header("Kates Auto-Deploy")
	
	executor := detect.NewOSExecutor()
	collector := detect.NewCollector(executor)
	
	if err := collector.Preflight(); err != nil {
		output.Error(fmt.Sprintf("Preflight failed: %v", err))
		os.Exit(1)
	}

	// 1. Detect Phase
	output.Hint("🔍 Auto-detecting cluster configuration...")
	reqs := detect.ParsedReqs{} // No manual overrides in auto mode
	report, collectErr := collector.Collect(context.Background())
	if collectErr != nil {
		output.Error(fmt.Sprintf("Cluster introspection failed: %v", collectErr))
		os.Exit(2)
	}

	analyzer := detect.NewAnalyzer(executor)
	analyzer.Analyze(report, reqs)

	capGen := detect.NewValuesGeneratorWithReserve(report, autoClusterName, autoReservePct)
	report.Capacity = capGen.Cap

	// Print capacity info so the user sees what's happening
	detect.RenderTUI(report)

	// 2. Generate Phase
	output.Header("Generate Configuration")
	
	tmpDir := ".build"
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return fmt.Errorf("failed to create %s directory: %w", tmpDir, err)
	}
	
	valuesPath := filepath.Join(tmpDir, "values-detected.yaml")
	f, err := os.Create(valuesPath)
	if err != nil {
		return fmt.Errorf("failed to create values file: %w", err)
	}

	if autoBaseValues != "" {
		baseData, err := os.ReadFile(autoBaseValues)
		if err != nil {
			f.Close()
			return fmt.Errorf("failed to read base values: %w", err)
		}
		detect.RenderValuesFromBaseWithReserve(report, autoClusterName, baseData, autoReservePct, f)
	} else {
		detect.RenderValuesWithReserve(report, autoClusterName, autoReservePct, f)
	}
	f.Close()
	output.Success(fmt.Sprintf("Configuration generated: %s", valuesPath))

	// Check if controller SC is missing (all SCs zone-specific, no default)
	genVals := capGen.Generate()
	if genVals.Controllers.Storage.Class == "" {
		output.Warn("⚠ No cross-zone StorageClass found for controllers!")
		output.Warn("  All detected StorageClasses are zone-specific.")
		output.Warn("  Controllers need a StorageClass that provisions PVs in any zone.")
		output.Warn("  → Create a default StorageClass or pass --values with controllers.storage.class set.")
	}

	// 3. Helm Build Dependencies Phase
	output.Header("Helm Deployment")
	if _, err := os.Stat(filepath.Join(autoChartDir, "Chart.yaml")); os.IsNotExist(err) {
		return fmt.Errorf("Helm chart not found at %s. Please run this command from the repository root or provide --chart-dir", autoChartDir)
	}

	output.Hint(fmt.Sprintf("📦 Building Helm dependencies for %s...", autoChartDir))
	depCmd := exec.Command("helm", "dependency", "build", autoChartDir)
	depCmd.Stdout = os.Stdout
	depCmd.Stderr = os.Stderr
	if err := depCmd.Run(); err != nil {
		return fmt.Errorf("helm dependency build failed: %w", err)
	}

	// 4. Helm Deploy Phase
	helmArgs := []string{
		"upgrade", "--install", autoReleaseName, autoChartDir,
		"--namespace", "kafka", "--create-namespace",
		"-f", valuesPath,
		"--force-conflicts",
		"--timeout", "10m", "--wait",
	}

	if autoDryRun {
		helmArgs = append(helmArgs, "--dry-run")
		output.Warn("Running in dry-run mode (no changes will be applied)")
	}

	output.Hint(fmt.Sprintf("🚀 Executing: helm %s", strings.Join(helmArgs, " ")))
	
	deployCmd := exec.Command("helm", helmArgs...)
	deployCmd.Stdout = os.Stdout
	deployCmd.Stderr = os.Stderr
	if err := deployCmd.Run(); err != nil {
		return fmt.Errorf("helm upgrade failed: %w", err)
	}

	output.Success("✅ Kafka cluster successfully deployed with auto-detected configuration!")
	return nil
}
