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
	autoClusterName    string
	autoReleaseName    string
	autoNamespace      string
	autoReservePct     float64
	autoChartDir       string
	autoDryRun         bool
	autoBaseValues     string
	autoSkipMonitoring bool
	autoStandalone     bool
)

var autoCmd = &cobra.Command{
	Use:   "auto",
	Short: "Auto-detect cluster configuration and deploy Kafka",
	Long: `Auto-detects the Kubernetes cluster configuration, generates optimal
values.yaml based on capacity and topology, and deploys the kafka-cluster
Helm chart in a single step.

Use --standalone to deploy on a cluster where the Strimzi operator is
already installed (skips operator subchart and monitoring dependencies).`,
	RunE: runAuto,
}

func init() {
	autoCmd.Flags().StringVar(&autoClusterName, "cluster-name", "krafter", "Kafka cluster name")
	autoCmd.Flags().StringVar(&autoReleaseName, "release-name", "kafka-cluster", "Helm release name")
	autoCmd.Flags().StringVarP(&autoNamespace, "namespace", "n", "kafka", "Kubernetes namespace for all deployed charts")
	autoCmd.Flags().Float64Var(&autoReservePct, "reserve", 0.30, "Reserve percentage of cluster resources (0.30 = 30% reserved, 70% for Kafka)")
	autoCmd.Flags().StringVar(&autoChartDir, "chart-dir", "./charts/kafka-cluster", "Path to the kafka-cluster Helm chart directory")
	autoCmd.Flags().BoolVar(&autoDryRun, "dry-run", false, "Preview the Helm installation without executing it")
	autoCmd.Flags().StringVarP(&autoBaseValues, "values", "f", "", "Path to base values.yaml to merge with detected values")
	autoCmd.Flags().BoolVar(&autoSkipMonitoring, "skip-monitoring", false, "Skip deploying the monitoring stack (Prometheus + Grafana)")
	autoCmd.Flags().BoolVar(&autoStandalone, "standalone", false, "Standalone mode: no operator subchart, no monitoring (operator must be pre-installed)")
	rootCmd.AddCommand(autoCmd)
}

func runAuto(cmd *cobra.Command, args []string) error {
	output.Header("Kates Auto-Deploy")

	// Standalone mode implies skip-monitoring
	if autoStandalone {
		autoSkipMonitoring = true
		output.Hint("🔧 Standalone mode: operator subchart disabled, monitoring disabled")

		// Verify Strimzi operator is pre-installed (check all namespaces)
		operatorFound := false

		// Check 1: look for running operator pod across all namespaces
		checkCmd := exec.Command("kubectl", "get", "pods", "-A",
			"-l", "strimzi.io/kind=cluster-operator", "--no-headers")
		out, err := checkCmd.Output()
		if err == nil && strings.Contains(string(out), "Running") {
			operatorFound = true
			// Extract the namespace from the first column
			fields := strings.Fields(strings.Split(string(out), "\n")[0])
			if len(fields) > 0 {
				output.Success(fmt.Sprintf("Strimzi operator is running (namespace: %s)", fields[0]))
			}
		}

		// Check 2: fallback — verify the Kafka CRD exists (operator installed but pod label may differ)
		if !operatorFound {
			crdCmd := exec.Command("kubectl", "get", "crd", "kafkas.kafka.strimzi.io", "--no-headers")
			if crdOut, crdErr := crdCmd.Output(); crdErr == nil && len(crdOut) > 0 {
				operatorFound = true
				output.Success("Strimzi CRDs detected — operator is installed")
			}
		}

		if !operatorFound {
			output.Error("Strimzi operator not found in any namespace")
			output.Hint("Install it: make strimzi-install")
			os.Exit(1)
		}
	}
	
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

	// 3. Helm Build Dependencies Phase
	output.Header("Helm Deployment")
	if _, err := os.Stat(filepath.Join(autoChartDir, "Chart.yaml")); os.IsNotExist(err) {
		return fmt.Errorf("Helm chart not found at %s. Please run this command from the repository root or provide --chart-dir", autoChartDir)
	}

	output.Hint(fmt.Sprintf("📦 Building Helm dependencies for %s...", autoChartDir))
	depCmd := exec.Command("helm", "dependency", "update", autoChartDir)
	depCmd.Stdout = os.Stdout
	depCmd.Stderr = os.Stderr
	if err := depCmd.Run(); err != nil {
		return fmt.Errorf("helm dependency update failed: %w", err)
	}

	// 4. Helm Deploy Phase
	output.Hint(fmt.Sprintf("📌 Target namespace: %s", autoNamespace))
	helmArgs := []string{
		"upgrade", "--install", autoReleaseName, autoChartDir,
		"--namespace", autoNamespace, "--create-namespace",
		"-f", valuesPath,
		"--force-conflicts",
		"--timeout", "10m", "--wait",
		"--debug",
	}

	// Standalone mode: layer the standalone overlay to disable monitoring
	if autoStandalone {
		standaloneOverlay := filepath.Join(autoChartDir, "values-standalone.yaml")
		if _, err := os.Stat(standaloneOverlay); err == nil {
			helmArgs = append(helmArgs, "-f", standaloneOverlay)
			output.Hint(fmt.Sprintf("📄 Layering standalone overlay: %s", standaloneOverlay))
		} else {
			// Fallback: inject overrides directly
			output.Warn("values-standalone.yaml not found — injecting overrides via --set")
			helmArgs = append(helmArgs,
				"--set", "strimziOperator.enabled=false",
				"--set", "kafka.metricsConfig.enabled=false",
				"--set", "kafkaExporter.enabled=false",
				"--set", "cruiseControl.enabled=false",
				"--set", "alerts.enabled=false",
				"--set", "podMonitors.enabled=false",
				"--set", "dashboards.enabled=false",
				"--set", "crdUpgrade.enabled=false",
			)
		}
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

	// 5. Monitoring Deployment Phase
	if !autoSkipMonitoring {
		monitoringChartDir := filepath.Join(filepath.Dir(autoChartDir), "monitoring")
		if _, err := os.Stat(filepath.Join(monitoringChartDir, "Chart.yaml")); os.IsNotExist(err) {
			output.Warn(fmt.Sprintf("Monitoring chart not found at %s — skipping monitoring deployment", monitoringChartDir))
		} else {
			output.Header("Monitoring Deployment")

			// Determine the values file: prefer values-generic.yaml for real clusters
			monitoringValues := filepath.Join(monitoringChartDir, "values-generic.yaml")
			if _, err := os.Stat(monitoringValues); os.IsNotExist(err) {
				monitoringValues = filepath.Join(monitoringChartDir, "values.yaml")
			}

			output.Hint(fmt.Sprintf("📦 Building Helm dependencies for %s...", monitoringChartDir))
			monDepCmd := exec.Command("helm", "dependency", "update", monitoringChartDir)
			monDepCmd.Stdout = os.Stdout
			monDepCmd.Stderr = os.Stderr
			if err := monDepCmd.Run(); err != nil {
				output.Error(fmt.Sprintf("Monitoring helm dependency update failed: %v", err))
				output.Warn("Continuing without monitoring — Kafka cluster is deployed successfully")
			} else {
				monHelmArgs := []string{
					"upgrade", "--install", "monitoring", monitoringChartDir,
					"--namespace", autoNamespace, "--create-namespace",
					"-f", monitoringValues,
					"--timeout", "10m", "--wait",
				}

				if autoDryRun {
					monHelmArgs = append(monHelmArgs, "--dry-run")
				}

				output.Hint(fmt.Sprintf("📊 Executing: helm %s", strings.Join(monHelmArgs, " ")))
				monDeployCmd := exec.Command("helm", monHelmArgs...)
				monDeployCmd.Stdout = os.Stdout
				monDeployCmd.Stderr = os.Stderr
				if err := monDeployCmd.Run(); err != nil {
					output.Error(fmt.Sprintf("Monitoring deployment failed: %v", err))
					output.Warn("Kafka cluster is deployed successfully but monitoring failed — you can retry with: make monitoring-generic")
				} else {
					output.Success("✅ Monitoring stack (Prometheus + Grafana) deployed successfully!")
				}
			}
		}
	} else {
		output.Hint("⏭️  Skipping monitoring deployment (--skip-monitoring)")
	}

	return nil
}
