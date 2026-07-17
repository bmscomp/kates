package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/detect"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

var isTesting = false

var deployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Deploy the Kates stack (Kafka, Kates, Chaos, Schema Registry)",
	Long: `Deploys the entire Kates stack using the detected cluster configuration.
Supports deploying into a single namespace or isolated namespaces.

Examples:
  # Deploy everything into a single namespace (dev mode)
  kates deploy --topology single --namespace kates-stack

  # Deploy components into isolated namespaces (production mode)
  kates deploy --topology isolated --kafka-ns kafka-system --app-ns kates-app --chaos-ns litmus-system

  # Deploy with Apicurio Schema Registry
  kates deploy --with-schema-registry apicurio`,
	RunE: runDeploy,
}

var (
	deployTopology           string
	deployNamespace          string
	deployKafkaNS            string
	deployConnectNS          string
	deployDbNS               string
	deployAppNS              string
	deployChaosNS            string
	deployMonitoringNS       string
	deployKafkaUINS          string
	deployWithSchemaRegistry string
	deployHA                 bool
	deployWithChaos          bool
	deployWithMonitoring     bool
	deployWithCertManager    bool
	deployWithKyverno        bool
	deployWithSecretManager  bool
	deployWithStrimzi        bool
	deployWithKafkaConnect   bool
	deployInteractive        bool
	deployVerbose            bool
	deployPortForward        bool
	deployDryRun             bool
	deployWithKafkaUI        bool
	deployYes                bool
)

func init() {
	deployCmd.Flags().StringVar(&deployTopology, "topology", "isolated", "Deployment topology: 'isolated' (separate namespaces) or 'single' (one namespace)")
	deployCmd.Flags().StringVar(&deployNamespace, "namespace", "kates-stack", "Target namespace when topology is 'single'")
	deployCmd.Flags().StringVar(&deployKafkaNS, "kafka-ns", "kafka", "Namespace for Kafka when topology is 'isolated'")
	deployCmd.Flags().StringVar(&deployConnectNS, "connect-ns", "connect", "Namespace for Kafka Connect when topology is 'isolated'")
	deployCmd.Flags().StringVar(&deployDbNS, "db-ns", "database", "Namespace for PostgreSQL Database when topology is 'isolated'")
	deployCmd.Flags().StringVar(&deployAppNS, "app-ns", "kates", "Namespace for Kates Backend when topology is 'isolated'")
	deployCmd.Flags().StringVar(&deployChaosNS, "chaos-ns", "litmus", "Namespace for Chaos Engine when topology is 'isolated'")
	deployCmd.Flags().StringVar(&deployMonitoringNS, "monitoring-ns", "monitoring", "Namespace for monitoring components (Jaeger) when topology is 'isolated'")
	deployCmd.Flags().StringVar(&deployKafkaUINS, "ui-ns", "kafka", "Namespace for Kafka UI when topology is 'isolated'")

	// Component flags
	deployCmd.Flags().StringVar(&deployWithSchemaRegistry, "with-schema-registry", "apicurio", "Schema Registry to deploy: 'none', 'apicurio', or 'confluent'")
	deployCmd.Flags().BoolVar(&deployHA, "ha", true, "Enable High Availability (Multi-AZ)")
	deployCmd.Flags().BoolVar(&deployWithChaos, "with-chaos", true, "Deploy LitmusChaos engine")
	deployCmd.Flags().BoolVar(&deployWithMonitoring, "with-monitoring", true, "Deploy monitoring components (Prometheus/Grafana/Jaeger)")
	deployCmd.Flags().BoolVar(&deployWithCertManager, "with-cert-manager", true, "Deploy Cert-Manager for TLS certificate management")
	deployCmd.Flags().BoolVar(&deployWithKyverno, "with-kyverno", false, "Deploy Kyverno for cluster policy enforcement")
	deployCmd.Flags().BoolVar(&deployWithSecretManager, "with-secret-manager", false, "Deploy Secret Manager (e.g., External Secrets Operator)")
	deployCmd.Flags().BoolVar(&deployWithStrimzi, "with-strimzi", true, "Deploy Strimzi Operator")
	deployCmd.Flags().BoolVar(&deployWithKafkaConnect, "with-kafka-connect", false, "Deploy Kafka Connect with PostgreSQL CDC (Debezium)")
	deployCmd.Flags().BoolVar(&deployWithKafkaUI, "with-kafka-ui", true, "Deploy Kafka UI for browser-based cluster monitoring")
	deployCmd.Flags().BoolVarP(&deployInteractive, "interactive", "i", false, "Use interactive UI to configure deployment")
	deployCmd.Flags().BoolVar(&deployVerbose, "verbose", false, "Show every kubectl/helm command as it runs")
	deployCmd.Flags().BoolVarP(&deployPortForward, "port-forward", "P", false, "After deploy, start port-forwards for all services and keep running until Ctrl+C")
	deployCmd.Flags().BoolVar(&deployDryRun, "dry-run", false, "Show the deployment plan without executing anything")
	deployCmd.Flags().BoolVarP(&deployYes, "yes", "y", false, "Assume yes and never prompt (fails instead of asking)")

	rootCmd.AddCommand(deployCmd)
}

func runDeploy(cmd *cobra.Command, args []string) error {
	deployStartTime := time.Now()
	PrintDeployBanner()
	resetDeployPhases()

	dl = &DashboardController{}

	// ── Target cluster ───────────────────────────────────────────────────
	// This runs FIRST, before the wizard. There is no point asking eight
	// questions about a deployment and only then discovering there is nowhere
	// to deploy it. When nothing is reachable this offers to build the local
	// 3-zone kind cluster; when several are reachable it asks rather than
	// guessing.
	PrintPhaseHeader(nextDeployPhase(), "Selecting Target Cluster")
	if _, err := resolveClusterFn(); err != nil {
		return err
	}

	// ── Interactive Forms ────────────────────────────────────────────────
	// Guarded by isInteractive: the forms open /dev/tty directly, so without a
	// terminal they fail with "could not open a new TTY" rather than falling
	// back to the flag defaults. Piped or scripted runs should just use the
	// defaults.
	if (deployInteractive || cmd.Flags().NFlag() == 0) && IsInteractive() {
		if err := runInteractiveForms(); err != nil {
			return err
		}
	}

	// Resolve Topology
	printTopologyResolution()

	// Component Selection
	printComponentSelection()

	executor := defaultExecutor
	PrintPhaseHeader(nextDeployPhase(), "Running Cluster Introspection (Pre-flight)")
	collector := detect.NewCollector(executor)
	if err := collector.Preflight(); err != nil {
		output.Error(fmt.Sprintf("Preflight failed: %v", err))
		return err
	}

	// ── Kind storage bootstrap ────────────────────────────────────────────────
	// Create zone-specific StorageClasses (local-storage-alpha, etc.) BEFORE
	// collector.Collect() runs. This ensures detect's matchStorageClass() finds
	// them and writes the correct storageClass names into values-detected.yaml,
	// so no hardcoded pool overrides are needed in values-kind.yaml.
	// A dry run must not write StorageClasses into the cluster. Introspection
	// below stays (it is read-only and the plan needs its data); this is the
	// one pre-plan step that mutates.
	if quickDetectKind() && !deployDryRun {
		PrintPhaseItem("Kind cluster detected — bootstrapping zone StorageClasses...")
		if err := setupKindStorageClasses(context.Background()); err != nil {
			output.Warn(fmt.Sprintf("StorageClass bootstrap warning: %v", err))
		}
	}

	report, err := collector.Collect(context.Background())
	if err != nil {
		output.Error(fmt.Sprintf("Introspection failed: %v", err))
		return err
	}

	analyzer := detect.NewAnalyzer(executor)
	analyzer.Analyze(report, detect.ParsedReqs{})

	PrintPhaseItem("Generating values-detected.yaml...")
	valuesFile := ".build/values-detected.yaml"
	os.MkdirAll(".build", 0755)

	f, err := os.Create(valuesFile)
	if err != nil {
		return fmt.Errorf("failed to create values file: %v", err)
	}
	defer f.Close()

	detect.RenderValuesWithReserve(report, "krafter", 0.30, f)

	// Detect cluster type once — used by every Helm call below to pick the
	// right values overlay (values-kind.yaml vs values-generic.yaml).
	isKind := report.Network.CNI == "kindnet" || report.Provider == "kind"

	// chartOverlay returns the path to the appropriate environment values file
	// for a given chart directory. Falls back to generic when the file doesn't
	// exist for that chart (e.g. kafka-cluster has no values-generic.yaml).
	chartOverlay := func(chartDir string) string {
		if isKind {
			return chartDir + "/values-kind.yaml"
		}
		return chartDir + "/values-generic.yaml"
	}

	fileExists := func(path string) bool {
		_, err := os.Stat(path)
		return err == nil
	}

	// Resolve namespaces before building entries
	ns := resolveNamespaces()

	// ── Build shared component registry (single source of truth) ──
	sharedEntries := buildSharedEntries(ns)

	// ── Dry-run: show preview and exit ──
	if deployDryRun {
		return runDeployDryRun(sharedEntries)
	}

	// 4. Execution Plan (Helm)
	PrintPhaseHeader(nextDeployPhase(), "Executing Deployment Pipeline")

	totalSteps := countDeploySteps()

	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ---------------------------------------------------------
	// DEPLOYMENT DASHBOARD (BUBBLE TEA)
	// ---------------------------------------------------------
	dashboard := NewDeployDashboard(ctx, totalSteps)
	registerDashboardComponents(&dashboard, ns)

	// Only build a bubbletea program where one can actually render. With p=nil
	// the DashboardController falls back to plain fmt printing — that fallback
	// existed all along; the wiring just never used it. Previously a piped or
	// headless deploy launched an alt-screen program into the pipe, dumping
	// escape sequences into logs.
	var p *tea.Program
	if isTesting {
		p = tea.NewProgram(dashboard, tea.WithoutRenderer(), tea.WithInput(nil))
	} else if IsInteractive() {
		p = tea.NewProgram(dashboard, tea.WithAltScreen())
	}
	dl = &DashboardController{p: p}

	var step int32
	advanceStep := func() {
		current := atomic.AddInt32(&step, 1)
		dl.UpdateProgress(int(current), totalSteps)
	}

	dc := &deployContext{
		ctx:          ctx,
		ns:           ns,
		valuesFile:   valuesFile,
		isKind:       isKind,
		report:       report,
		chartOverlay: chartOverlay,
		fileExists:   fileExists,
		advanceStep:  advanceStep,
	}

	var deployErr error
	var finalEntries []DeploySummaryEntry
	var deployElapsed time.Duration
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		defer func() {
			if p != nil {
				p.Quit()
			}
		}()

		deployErr = func() error {
			if err := deployGroupA(dc); err != nil {
				return err
			}

			if err := deployGroupB(dc); err != nil {
				return err
			}

			if err := deployGroupC(dc); err != nil {
				return err
			}

			// Use shared entries for summary (single source of truth)
			finalEntries = sharedEntries
			deployElapsed = time.Since(deployStartTime)
			return nil
		}()
	}()

	if p != nil {
		if _, err := p.Run(); err != nil {
			// The dashboard is cosmetic; the deploy goroutine is real work.
			// Returning here used to orphan it mid-helm — the process exited
			// while installs were in flight, leaving releases half-applied.
			// Wait it out, then report the more consequential error.
			<-doneCh
			if deployErr != nil {
				return deployErr
			}
			return err
		}
	}

	<-doneCh // Wait for deployment goroutine to fully exit

	if deployErr != nil {
		os.WriteFile("deploy-error.log", []byte(deployErr.Error()), 0644)
	}

	if deployErr == nil && len(finalEntries) > 0 {
		RenderDeployDashboard(ctx, finalEntries, deployElapsed)

		// Automatically sync API key from deployed cluster to active context
		updateActiveContextAPIKey(ctx, ns.app)

		if deployPortForward {
			RunPortForwards(ctx, ns.kafka, ns.app, ns.jaeger)
		}
	}

	return deployErr
}

// Helpers

var (
	runExecFn                                      = runExecDefault
	runExecStdinFn                                 = runExecStdinDefault
	runHelmFn                                      = runHelmDefault
	isHelmReleaseDeployedFn                        = isHelmReleaseDeployedDefault
	defaultExecutor         detect.CommandExecutor = detect.NewOSExecutor()
)

var execMutex sync.Mutex

func runExecDefault(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)

	// Prevent interwoven output lines for parallel commands
	execMutex.Lock()
	defer execMutex.Unlock()

	if deployVerbose {
		// Show the command being run
		dl.Printf("    %s\n", output.DimStyle.Render("$ "+name+" "+strings.Join(args, " ")))
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	// Non-verbose: capture output, show stderr only on error
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		// Show the error details so failures are never silent
		errMsg := strings.TrimSpace(errBuf.String())
		if errMsg == "" {
			errMsg = strings.TrimSpace(outBuf.String())
		}
		if errMsg != "" {
			dl.Printf("    %s\n", output.ErrorStyle.Render(errMsg))
			return fmt.Errorf("%w: %s", err, errMsg)
		}
		return err
	}
	return nil
}

func runExecStdinDefault(ctx context.Context, name string, args []string, stdinData string) error {
	cmd := exec.CommandContext(ctx, name, args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdin pipe: %w", err)
	}

	go func() {
		defer stdin.Close()
		stdin.Write([]byte(stdinData))
	}()

	execMutex.Lock()
	defer execMutex.Unlock()

	if deployVerbose {
		dl.Printf("    %s\n", output.DimStyle.Render("$ "+name+" "+strings.Join(args, " ")))
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if runErr := cmd.Run(); runErr != nil {
		errMsg := strings.TrimSpace(errBuf.String())
		if errMsg == "" {
			errMsg = strings.TrimSpace(outBuf.String())
		}
		if errMsg != "" {
			dl.Printf("    %s\n", output.ErrorStyle.Render(errMsg))
			return fmt.Errorf("%w: %s", runErr, errMsg)
		}
		return runErr
	}
	return nil
}

func runHelmDefault(ctx context.Context, args ...string) error {
	return runExecFn(ctx, "helm", args...)
}

func isHelmReleaseDeployedDefault(ctx context.Context, release, namespace string) bool {
	// Check that the release exists AND is in "deployed" status.
	// helm status exits 0 even for failed releases, so we must inspect
	// the actual status to avoid skipping re-installation of broken releases.
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(checkCtx, "helm", "status", release, "-n", namespace, "-o", "json").Output()
	if err != nil {
		return false
	}
	// Look for "status":"deployed" in the JSON output.
	// Helm may output with or without spaces after colons, so check both.
	s := string(out)
	return strings.Contains(s, `"status":"deployed"`) || strings.Contains(s, `"status": "deployed"`)
}

// cleanupStaleClusterResource removes a cluster-scoped resource if it belongs
// to a Helm release in a different namespace, preventing ownership conflicts
// when switching deployment topologies.
func cleanupStaleClusterResource(ctx context.Context, kind, name, expectedNS string) {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, "kubectl", "get", kind, name, "-o", "jsonpath={.metadata.annotations.meta\\.helm\\.sh/release-namespace}")
	out, err := cmd.Output()
	if err != nil {
		return // resource doesn't exist, nothing to clean
	}
	existingNS := strings.TrimSpace(string(out))
	if existingNS != "" && existingNS != expectedNS {
		dl.Printf("    - Cleaning stale %s/%s (owned by namespace %q, deploying to %q)\n", kind, name, existingNS, expectedNS)
		delCtx, delCancel := context.WithTimeout(ctx, 10*time.Second)
		defer delCancel()
		exec.CommandContext(delCtx, "kubectl", "delete", kind, name, "--ignore-not-found").Run()
	}
}

func sliceContains(s []string, v string) bool {
	for _, item := range s {
		if item == v {
			return true
		}
	}
	return false
}
