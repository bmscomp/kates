package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/klster/kates-cli/output"
	"github.com/klster/kates-cli/pkg/detect"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
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
	deployCmd.Flags().BoolVarP(&deployInteractive, "interactive", "i", false, "Use interactive UI to configure deployment")
	deployCmd.Flags().BoolVar(&deployVerbose, "verbose", false, "Show every kubectl/helm command as it runs")
	deployCmd.Flags().BoolVarP(&deployPortForward, "port-forward", "P", false, "After deploy, start port-forwards for all services and keep running until Ctrl+C")
	deployCmd.Flags().BoolVar(&deployDryRun, "dry-run", false, "Show the deployment plan without executing anything")

	rootCmd.AddCommand(deployCmd)
}

func runDeploy(cmd *cobra.Command, args []string) error {
	deployStartTime := time.Now()
	PrintDeployBanner()

	dl = &DashboardController{}

	if deployInteractive || cmd.Flags().NFlag() == 0 {
		var components []string

		form1 := huh.NewForm(
			// ── Group 1: What to deploy ──────────────────────────────────────────
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Choose Namespace Topology").
					Description("Isolated creates logical boundaries. Single is great for simple local dev.").
					Options(
						huh.NewOption("Isolated Namespaces (kafka, kates, monitoring, litmus)", "isolated"),
						huh.NewOption("Single Namespace (kates-stack)", "single"),
					).
					Value(&deployTopology),

				huh.NewSelect[string]().
					Title("Schema Registry").
					Options(
						huh.NewOption("Apicurio", "apicurio"),
						huh.NewOption("None", "none"),
					).
					Value(&deployWithSchemaRegistry),

				huh.NewConfirm().
					Title("Enable High Availability (Multi-AZ)?").
					Description("Sets replicas=3, min.insync.replicas=2, and enables Topology Spread Constraints").
					Value(&deployHA),

				huh.NewMultiSelect[string]().
					Title("Select Additional Components").
					Description("Use space to toggle, enter to confirm").
					Options(
						huh.NewOption("🦊 Strimzi Operator", "strimzi").Selected(deployWithStrimzi),
						huh.NewOption("🔗 Kafka Connect + PostgreSQL (CDC)", "kafka-connect").Selected(deployWithKafkaConnect),
						huh.NewOption("🧪 Litmus Chaos Engine", "chaos").Selected(deployWithChaos),
						huh.NewOption("📊 Monitoring (Grafana + Prometheus)", "monitoring").Selected(deployWithMonitoring),
						huh.NewOption("🔐 Cert-Manager (TLS)", "cert-manager").Selected(deployWithCertManager),
						huh.NewOption("🛡️  Kyverno (Policies)", "kyverno").Selected(deployWithKyverno),
					).
					Value(&components),
			),
		).WithTheme(ThemeKates())

		if err := form1.Run(); err != nil {
			return err
		}

		// ── Form 2: Namespace configuration ──────────────────────────────────
		// Built after Form 1 completes so that `components` is fully populated.
		// This avoids a huh library issue where MultiSelect values from a
		// previous group aren't reliably available in WithHideFunc closures
		// of subsequent groups within the same form.
		if deployTopology != "single" {
			var nsGroups []*huh.Group

			// Core namespaces (always shown for isolated topology)
			nsGroups = append(nsGroups, huh.NewGroup(
				huh.NewInput().
					Title("Kafka Namespace").
					Description("Namespace for Strimzi operator and Kafka cluster").
					Value(&deployKafkaNS),
				huh.NewInput().
					Title("Kates App Namespace").
					Description("Namespace for the Kates backend service").
					Value(&deployAppNS),
			))

			// Conditional namespace groups based on selected components
			if sliceContains(components, "monitoring") {
				nsGroups = append(nsGroups, huh.NewGroup(
					huh.NewInput().
						Title("Monitoring Namespace").
						Description("Namespace for Jaeger and monitoring components").
						Value(&deployMonitoringNS),
				))
			}

			if sliceContains(components, "chaos") {
				nsGroups = append(nsGroups, huh.NewGroup(
					huh.NewInput().
						Title("Chaos Namespace").
						Description("Namespace for Litmus Chaos engine").
						Value(&deployChaosNS),
				))
			}

			if sliceContains(components, "kafka-connect") {
				nsGroups = append(nsGroups, huh.NewGroup(
					huh.NewInput().
						Title("Kafka Connect Namespace").
						Description("Namespace for Kafka Connect cluster (separate from Kafka)").
						Value(&deployConnectNS),
					huh.NewInput().
						Title("Database Namespace").
						Description("Namespace for PostgreSQL CDC database").
						Value(&deployDbNS),
				))
			}

			form2 := huh.NewForm(nsGroups...).WithTheme(ThemeKates())
			if err := form2.Run(); err != nil {
				return err
			}
		}

		deployWithChaos = false
		deployWithMonitoring = false
		deployWithCertManager = false
		deployWithKyverno = false
		deployWithStrimzi = false

		for _, c := range components {
			switch c {
			case "strimzi":
				deployWithStrimzi = true
			case "kafka-connect":
				deployWithKafkaConnect = true
			case "chaos":
				deployWithChaos = true
			case "monitoring":
				deployWithMonitoring = true
			case "cert-manager":
				deployWithCertManager = true
			case "kyverno":
				deployWithKyverno = true
			}
		}
	}

	// 1. Resolve Topology
	PrintPhaseHeader(1, fmt.Sprintf("Resolving Namespace Topology (%s mode)", deployTopology))
	if deployTopology == "single" {
		PrintPhaseItem(fmt.Sprintf("All components → %s", deployNamespace))
	} else {
		PrintPhaseItem(fmt.Sprintf("%-14s → %s", "Kafka", deployKafkaNS))
		if deployWithKafkaConnect {
			PrintPhaseItem(fmt.Sprintf("%-14s → %s", "Kafka Connect", deployConnectNS))
			PrintPhaseItem(fmt.Sprintf("%-14s → %s", "Database", deployDbNS))
		}
		PrintPhaseItem(fmt.Sprintf("%-14s → %s", "Kates App", deployAppNS))
		if deployWithMonitoring {
			PrintPhaseItem(fmt.Sprintf("%-14s → %s", "Monitoring", deployMonitoringNS))
		}
		PrintPhaseItem(fmt.Sprintf("%-14s → %s", "Chaos", deployChaosNS))
	}

	// 2. Component Selection
	PrintPhaseHeader(2, "Component Selection")
	if deployWithStrimzi {
		PrintPhaseSuccess("Strimzi Operator")
	}
	if deployWithKafkaConnect {
		PrintPhaseSuccess("Kafka Connect + PostgreSQL (CDC)")
	}
	if deployWithSchemaRegistry != "none" {
		PrintPhaseSuccess(fmt.Sprintf("Schema Registry: %s", deployWithSchemaRegistry))
	}
	if deployWithChaos {
		PrintPhaseSuccess("Litmus Chaos Engine")
	}
	if deployWithMonitoring {
		PrintPhaseSuccess("Jaeger (Tracing)")
	}
	if deployWithCertManager {
		PrintPhaseSuccess("Cert-Manager (TLS)")
	}
	if deployWithKyverno {
		PrintPhaseSuccess("Kyverno (Policies)")
	}

	// 3. Cluster Detection
	PrintPhaseHeader(3, "Running Cluster Introspection (Pre-flight)")
	executor := defaultExecutor
	collector := detect.NewCollector(executor)

	if err := collector.Preflight(); err != nil {
		fmt.Println("⚠️  Kubernetes cluster is unreachable.")

		// Check if docker is running
		if dockerCheck := exec.Command("docker", "info"); dockerCheck.Run() == nil {
			fmt.Print("🐳 Docker is running. Would you like to automatically create a local Kind cluster? [y/N]: ")
			var response string
			fmt.Scanln(&response)
			if strings.ToLower(response) == "y" || strings.ToLower(response) == "yes" {
				fmt.Println("📦 Creating Kind cluster via 'make cluster'...")
				cmd := exec.Command("make", "cluster")
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				if err := cmd.Run(); err != nil {
					return fmt.Errorf("failed to create Kind cluster: %w", err)
				}
				fmt.Println("✅ Kind cluster created successfully! Retrying preflight...")

				// Re-run preflight
				if err := collector.Preflight(); err != nil {
					return fmt.Errorf("preflight failed even after cluster creation: %w", err)
				}
			} else {
				return fmt.Errorf("cluster is unreachable and user opted out of auto-creation")
			}
		} else {
			output.Error(fmt.Sprintf("Preflight failed: %v", err))
			return err
		}
	}

	// ── Kind storage bootstrap ────────────────────────────────────────────────
	// Create zone-specific StorageClasses (local-storage-alpha, etc.) BEFORE
	// collector.Collect() runs. This ensures detect's matchStorageClass() finds
	// them and writes the correct storageClass names into values-detected.yaml,
	// so no hardcoded pool overrides are needed in values-kind.yaml.
	if quickDetectKind() {
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

	// Resolve namespaces before building entries
	var kafkaNS, connectNS, appNS, chaosNS, jaegerNS string
	if deployTopology == "single" {
		kafkaNS, connectNS, appNS, chaosNS, jaegerNS = deployNamespace, deployNamespace, deployNamespace, deployNamespace, deployNamespace
	} else {
		kafkaNS, connectNS, appNS, chaosNS, jaegerNS = deployKafkaNS, deployConnectNS, deployAppNS, deployChaosNS, deployMonitoringNS
	}

	// ── Build shared component registry (single source of truth) ──
	var sharedEntries []DeploySummaryEntry
	if deployWithStrimzi {
		sharedEntries = append(sharedEntries, DeploySummaryEntry{Icon: "☸️", Name: "Strimzi Operator", Release: "strimzi-operator", Namespace: "strimzi-operator", Group: "A"})
	}
	if deployWithCertManager {
		sharedEntries = append(sharedEntries, DeploySummaryEntry{Icon: "🔐", Name: "Cert-Manager", Release: "cert-manager", Namespace: "cert-manager", Group: "A"})
	}
	if deployWithKyverno {
		sharedEntries = append(sharedEntries, DeploySummaryEntry{Icon: "🛡️", Name: "Kyverno", Release: "kyverno", Namespace: "kyverno", Group: "A"})
	}
	sharedEntries = append(sharedEntries, DeploySummaryEntry{Icon: "📨", Name: "Kafka (krafter)", Release: "krafter", Namespace: kafkaNS, Group: "B"})
	if deployWithKafkaConnect {
		sharedEntries = append(sharedEntries, DeploySummaryEntry{Icon: "🐘", Name: "PostgreSQL (CDC)", Release: "postgresql", Namespace: deployDbNS, Group: "B"})
		sharedEntries = append(sharedEntries, DeploySummaryEntry{Icon: "🔗", Name: "Kafka Connect", Release: "connect-cluster", Namespace: connectNS, Group: "B"})
	}
	if deployWithMonitoring {
		sharedEntries = append(sharedEntries, DeploySummaryEntry{Icon: "📊", Name: "Monitoring Stack", Release: "monitoring", Namespace: jaegerNS, Group: "B"})
	}
	if deployWithSchemaRegistry == "apicurio" {
		sharedEntries = append(sharedEntries, DeploySummaryEntry{Icon: "📋", Name: "Apicurio Registry", Release: "apicurio", Namespace: kafkaNS, Group: "C"})
	}
	sharedEntries = append(sharedEntries, DeploySummaryEntry{Icon: "📦", Name: "Kates Backend", Release: "kates", Namespace: appNS, Group: "C"})
	if deployWithChaos {
		sharedEntries = append(sharedEntries, DeploySummaryEntry{Icon: "🧪", Name: "Litmus Chaos", Release: "chaos", Namespace: chaosNS, Group: "C"})
	}

	// ── Dry-run: show preview and exit ──
	if deployDryRun {
		dryCtx, dryCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer dryCancel()
		existingReleases := make(map[string]bool)
		for _, e := range sharedEntries {
			key := e.Release + "/" + e.Namespace
			if isHelmReleaseDeployedFn(dryCtx, e.Release, e.Namespace) {
				existingReleases[key] = true
			}
		}
		renderDeployPreview(sharedEntries, existingReleases)
		return nil
	}

	// 4. Execution Plan (Helm)
	PrintPhaseHeader(4, "Executing Deployment Pipeline")

	var totalSteps int
	if deployWithStrimzi {
		totalSteps++
	}
	if deployWithCertManager {
		totalSteps++
	}
	if deployWithKyverno {
		totalSteps++
	}
	totalSteps += 2 // kafka, kafka-users
	if deployWithMonitoring {
		totalSteps++
	}
	if deployWithKafkaConnect {
		totalSteps += 3
	} // postgres, kafka-connect, kafka-connector
	if deployWithSchemaRegistry == "apicurio" {
		totalSteps++
	}
	totalSteps++ // kates
	if deployWithChaos {
		totalSteps++
	}

	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ---------------------------------------------------------
	// DEPLOYMENT DASHBOARD (BUBBLE TEA)
	// ---------------------------------------------------------
	dashboard := NewDeployDashboard(ctx, totalSteps)
	if deployWithStrimzi {
		dashboard.RegisterComponent("strimzi", "Strimzi Operator", "A", Target{"strimzi-operator", "name=strimzi-cluster-operator"})
	}
	if deployWithCertManager {
		dashboard.RegisterComponent("cert-manager", "Cert-Manager", "A", Target{"cert-manager", "app.kubernetes.io/instance=cert-manager"})
	}
	if deployWithKyverno {
		dashboard.RegisterComponent("kyverno", "Kyverno", "A", Target{"kyverno", "app.kubernetes.io/instance=kyverno"})
	}
	dashboard.RegisterComponent("kafka", "Kafka Cluster", "B", Target{kafkaNS, "strimzi.io/cluster=krafter"})
	dashboard.RegisterComponent("kafka-users", "Kafka Users", "B", Target{kafkaNS, "app.kubernetes.io/name=entity-operator"})
	if deployWithMonitoring {
		dashboard.RegisterComponent("monitoring", "Monitoring Stack", "B", Target{jaegerNS, "release=monitoring"})
	}
	if deployWithKafkaConnect {
		dashboard.RegisterComponent("postgres", "PostgreSQL", "B",
			Target{deployDbNS, "app.kubernetes.io/instance=postgresql"})
		dashboard.RegisterComponent("kafka-connect", "Kafka Connect", "B",
			Target{connectNS, "strimzi.io/kind=KafkaConnect"})
		dashboard.RegisterComponent("kafka-connector", "CDC Connector", "B",
			Target{connectNS, "strimzi.io/cluster=connect-cluster"})
	}
	if deployWithSchemaRegistry == "apicurio" {
		dashboard.RegisterComponent("apicurio", "Apicurio Registry", "C", Target{kafkaNS, "app.kubernetes.io/instance=apicurio"})
	}
	dashboard.RegisterComponent("kates", "Kates Backend", "C", Target{appNS, "app.kubernetes.io/instance=kates"})
	if deployWithChaos {
		dashboard.RegisterComponent("chaos", "Litmus Chaos", "C", Target{chaosNS, "app.kubernetes.io/instance=chaos"})
	}

	var pOptions []tea.ProgramOption
	if isTesting {
		pOptions = append(pOptions, tea.WithoutRenderer(), tea.WithInput(nil))
	} else {
		pOptions = append(pOptions, tea.WithAltScreen())
	}
	p := tea.NewProgram(dashboard, pOptions...)
	dl = &DashboardController{p: p}

	var step int32
	advanceStep := func() {
		current := atomic.AddInt32(&step, 1)
		dl.UpdateProgress(int(current), totalSteps)
	}

	var deployErr error
	var finalEntries []DeploySummaryEntry
	var deployElapsed time.Duration
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		defer p.Quit()

		deployErr = func() error {
			// ---------------------------------------------------------
			// GROUP A: Operators & CRDs (Parallel)
			// ---------------------------------------------------------
			deployStrimzi := false
			if deployWithStrimzi {
				deployStrimzi = !isHelmReleaseDeployedFn(ctx, "strimzi-operator", "strimzi-operator")
			}
			deployCertManager := false
			if deployWithCertManager {
				deployCertManager = !isHelmReleaseDeployedFn(ctx, "cert-manager", "cert-manager")
			}
			deployKyverno := false
			if deployWithKyverno {
				deployKyverno = !isHelmReleaseDeployedFn(ctx, "kyverno", "kyverno")
			}

			g, gCtx := errgroup.WithContext(ctx)

			// Deploy Strimzi Operator
			if deployWithStrimzi {
				g.Go(func() error {
					if !deployStrimzi {
						dl.Println("⏭️  Strimzi Operator already deployed. Skipping.")
						dl.FinishComponent("strimzi", true)
						advanceStep()
						return nil
					}
					dl.Println("\n📦 Deploying Strimzi Operator (Namespace: strimzi-operator)...")
					// Create namespace properly
					runExecStdinFn(gCtx, "kubectl", []string{"apply", "-f", "-"}, `apiVersion: v1
kind: Namespace
metadata:
  name: strimzi-operator`)
					clusterDomain := report.Network.ClusterDomain
					if clusterDomain == "" {
						clusterDomain = "cluster.local"
					}
					err := runHelmFn(gCtx, "upgrade", "--install", "strimzi-operator", "oci://quay.io/strimzi-helm/strimzi-kafka-operator", "--version", "1.0.0", "-n", "strimzi-operator",
						"--set", "watchAnyNamespace=true",
						"--set", "kubernetesServiceDnsDomain="+clusterDomain,
						"--set", "replicas=1",
						"--set", "resources.limits.memory=768Mi",
						"--set", "resources.requests.memory=768Mi",
						"--set", "leaderElection.enabled=false",
						"--set", "operationTimeoutMs=900000",
						"--timeout", "5m")
					if err != nil {
						return err
					}
					return nil
				})
			}

			// Deploy Cert-Manager
			if deployWithCertManager {
				g.Go(func() error {
					if !deployCertManager {
						dl.Println("⏭️  Cert-Manager already deployed. Skipping.")
						dl.FinishComponent("cert-manager", true)
						advanceStep()
						return nil
					}
					dl.Printf("\n📦 Deploying Cert-Manager (Namespace: %s)...\n", "cert-manager")
					runHelmFn(gCtx, "repo", "add", "jetstack", "https://charts.jetstack.io")
					runHelmFn(gCtx, "repo", "update", "jetstack")
					// global.clusterDomain ensures cert-manager generates webhook TLS certificates
					// and service references using the actual cluster DNS domain (not always cluster.local).
					// report.Network.ClusterDomain is detected from the live cluster by kates detect.
					clusterDomain := report.Network.ClusterDomain
					if clusterDomain == "" {
						clusterDomain = "cluster.local"
					}
					err := runHelmFn(gCtx, "upgrade", "--install", "cert-manager", "jetstack/cert-manager",
						"--version", "v1.13.3",
						"-n", "cert-manager", "--create-namespace",
						"--set", "installCRDs=true",
						"--set", "startupapicheck.enabled=false",
						"--set", "global.clusterDomain="+clusterDomain,
						"--timeout", "10m")
					if err != nil {
						return err
					}

					dl.Println("    - Waiting for Cert-Manager CRDs to be established...")
					if err := runExecFn(gCtx, "kubectl", "wait", "--for=condition=Established",
						"crd", "clusterissuers.cert-manager.io", "--timeout=180s"); err != nil {
						return err
					}

					// On re-deploys the cainjector may hold a stale CA from the previous
					// installation. Restart it to force fresh CA bundle generation and
					// re-injection into the MutatingWebhookConfiguration.
					dl.Println("    - Restarting Cert-Manager CA injector for fresh CA bundle...")
					runExecFn(gCtx, "kubectl", "rollout", "restart",
						"deployment/cert-manager-cainjector", "-n", "cert-manager")
					runExecFn(gCtx, "kubectl", "rollout", "status",
						"deployment/cert-manager-cainjector", "-n", "cert-manager", "--timeout=90s")

					// The definitive readiness signal: poll the MutatingWebhookConfiguration
					// until caBundle is non-empty. Only then can the API server verify the
					// webhook TLS certificate without x509: certificate signed by unknown authority.
					dl.Println("    - Polling for Cert-Manager CA bundle injection...")
					const caTimeout = 180 * time.Second
					const caPoll = 3 * time.Second
					caDeadline := time.Now().Add(caTimeout)
					for {
						out, err := exec.CommandContext(gCtx,
							"kubectl", "get",
							"mutatingwebhookconfiguration", "cert-manager-webhook",
							"-o", "jsonpath={.webhooks[0].clientConfig.caBundle}",
						).Output()
						if err == nil && len(strings.TrimSpace(string(out))) > 0 {
							dl.Println("    - CA bundle injected. Applying ClusterIssuer...")
							break
						}
						if time.Now().After(caDeadline) {
							dl.Println("    ⚠ CA bundle injection timed out — proceeding with webhook bypass fallback")
							break
						}
						select {
						case <-gCtx.Done():
							return gCtx.Err()
						case <-time.After(caPoll):
						}
					}

					clusterIssuer := `apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: selfsigned-issuer
spec:
  selfSigned: {}`

					// Try applying normally first (3 quick attempts).
					var lastErr error
					for attempt := 1; attempt <= 3; attempt++ {
						if lastErr = runExecStdinFn(gCtx, "kubectl", []string{"apply", "-f", "-"}, clusterIssuer); lastErr == nil {
							return nil
						}
						if attempt < 3 {
							time.Sleep(3 * time.Second)
						}
					}

					// Hard fallback: temporarily set failurePolicy: Ignore so the API server
					// allows the resource creation call through even if webhook TLS is still
					// not verifiable. cert-manager will reconcile the webhook config shortly.
					dl.Println("    ⚠ Webhook TLS not verifiable — temporarily setting failurePolicy: Ignore")
					runExecFn(gCtx, "kubectl", "patch",
						"mutatingwebhookconfiguration", "cert-manager-webhook",
						"--type=json", "-p",
						`[{"op":"replace","path":"/webhooks/0/failurePolicy","value":"Ignore"}]`)
					time.Sleep(2 * time.Second) // let the API server pick up the patch

					applyErr := runExecStdinFn(gCtx, "kubectl", []string{"apply", "-f", "-"}, clusterIssuer)

					// Always restore failurePolicy: Fail regardless of outcome.
					runExecFn(gCtx, "kubectl", "patch",
						"mutatingwebhookconfiguration", "cert-manager-webhook",
						"--type=json", "-p",
						`[{"op":"replace","path":"/webhooks/0/failurePolicy","value":"Fail"}]`)

					if applyErr != nil {
						return fmt.Errorf("failed to apply cert-manager ClusterIssuer: %w", applyErr)
					}
					dl.Println("    - ClusterIssuer created. Webhook policy restored.")
					return nil
				})
			}

			// Deploy Kyverno
			if deployWithKyverno {
				g.Go(func() error {
					if !deployKyverno {
						dl.Println("⏭️  Kyverno already deployed. Skipping.")
						dl.FinishComponent("kyverno", true)
						advanceStep()
						return nil
					}
					dl.Println("\n📦 Deploying Kyverno (Namespace: kyverno)...")
					runHelmFn(gCtx, "repo", "add", "kyverno", "https://kyverno.github.io/kyverno/")
					runHelmFn(gCtx, "repo", "update", "kyverno")

					// Kyverno v3.x splits into 4 controllers; replicaCount=1 is a v2.x flag.
					// Pinned to 3.6.4 — the last stable patch of the 3.6 minor series.
					// Chart versions: https://kyverno.github.io/kyverno/
					// global.clusterDomain ensures Kyverno webhook certificates use the
					// correct cluster DNS domain — same value detected for all other components.
					kyvernoDomain := report.Network.ClusterDomain
					if kyvernoDomain == "" {
						kyvernoDomain = "cluster.local"
					}
					err := runHelmFn(gCtx, "upgrade", "--install", "kyverno", "kyverno/kyverno",
						"--version", "3.6.4",
						"-n", "kyverno", "--create-namespace",
						"--set", "admissionController.replicas=1",
						"--set", "backgroundController.replicas=1",
						"--set", "cleanupController.replicas=1",
						"--set", "reportsController.replicas=1",
						"--set", "global.clusterDomain="+kyvernoDomain,
						"--timeout", "5m")
					if err != nil {
						return err
					}

					// Wait for Kyverno CRDs before anything downstream tries to use them.
					dl.Println("    - Waiting for Kyverno CRDs to be established...")
					return runExecFn(gCtx, "kubectl", "wait", "--for=condition=Established",
						"crd", "clusterpolicies.kyverno.io",
						"--timeout=180s")
				})
			}

			if err := g.Wait(); err != nil {
				return fmt.Errorf("failed during Group A (Operators) deployments: %w", err)
			}

			// ── Sequential Readiness Waits for Group A ──
			if deployWithStrimzi && !isTesting {
				if !deployStrimzi {
					// Already skipped above, but ensure progress isn't blocked
				} else {
					dl.StartComponent("strimzi", 5*time.Minute)
					if err := waitComponentReadySilent(ctx, "strimzi-operator", "name=strimzi-cluster-operator", 5*time.Minute); err != nil {
						output.Error(fmt.Sprintf("Strimzi operator readiness failed: %v", err))
						return err
					}
					dl.FinishComponent("strimzi", true)
					advanceStep()
				}
			}
			if deployWithCertManager && !isTesting {
				if !deployCertManager {
					// already handled
				} else {
					dl.StartComponent("cert-manager", 10*time.Minute)
					if err := waitComponentReadySilent(ctx, "cert-manager", "app.kubernetes.io/instance=cert-manager", 10*time.Minute); err != nil {
						output.Error(fmt.Sprintf("Cert-Manager readiness failed: %v", err))
						return err
					}
					dl.FinishComponent("cert-manager", true)
					advanceStep()
				}
			}
			if deployWithKyverno && !isTesting {
				if !deployKyverno {
					// already handled
				} else {
					dl.StartComponent("kyverno", 5*time.Minute)
					if err := waitComponentReadySilent(ctx, "kyverno", "app.kubernetes.io/instance=kyverno", 5*time.Minute); err != nil {
						output.Error(fmt.Sprintf("Kyverno readiness failed: %v", err))
						return err
					}
					dl.FinishComponent("kyverno", true)
					advanceStep()
				}
			}

			// Bust Kubernetes Discovery Cache so Helm knows about the newly created CRDs
			dl.Println("    - Refreshing API server schema cache...")
			if home, err := os.UserHomeDir(); err == nil {
				os.RemoveAll(fmt.Sprintf("%s/.kube/cache/discovery", home))
				os.RemoveAll(fmt.Sprintf("%s/.cache/helm", home))
			}

			// ---------------------------------------------------------
			// GROUP B: Core Infrastructure (Parallel)
			// ---------------------------------------------------------

			deployKafka := !isHelmReleaseDeployedFn(ctx, "krafter", kafkaNS)
			deployMon := false
			if deployWithMonitoring {
				deployMon = !isHelmReleaseDeployedFn(ctx, "monitoring", jaegerNS)
			}
			deployPG := false
			if deployWithKafkaConnect {
				deployPG = !isHelmReleaseDeployedFn(ctx, "postgresql", deployDbNS)
			}

			if deployKafka {
				dl.Printf("\n📦 Deploying Kafka Cluster (Namespace: %s)...\n", kafkaNS)
			} else {
				dl.Println("⏭️  Kafka Cluster already deployed. Skipping.")
				dl.FinishComponent("kafka", true)
				advanceStep()
				dl.FinishComponent("kafka-users", true)
				advanceStep()
			}

			if deployWithMonitoring {
				if deployMon {
					dl.Printf("\n📦 Deploying Monitoring (Prometheus + Grafana) (Namespace: %s)...\n", jaegerNS)
				} else {
					dl.Println("⏭️  Monitoring stack already deployed. Skipping.")
					dl.FinishComponent("monitoring", true)
					advanceStep()
				}
			}

			if deployWithKafkaConnect {
				if deployPG {
					dl.Printf("\n📦 Deploying PostgreSQL CDC Database (Namespace: %s)...\n", deployDbNS)
				} else {
					dl.Println("⏭️  PostgreSQL already deployed. Skipping.")
					dl.FinishComponent("postgres", true)
					advanceStep()
				}
			}

			g2, g2Ctx := errgroup.WithContext(ctx)

			// Deploy Monitoring (Prometheus + Grafana via charts/monitoring)
			if deployWithMonitoring && deployMon {
				g2.Go(func() error {
					// Update chart dependencies (kube-prometheus-stack subchart).
					runHelmFn(g2Ctx, "dependency", "update", "charts/monitoring")

					return runHelmFn(g2Ctx, "upgrade", "--install", "monitoring",
						"charts/monitoring",
						"-n", jaegerNS, "--create-namespace",
						"-f", chartOverlay("charts/monitoring"),
						"--set", "kube-prometheus-stack.global.clusterDomain="+report.Network.ClusterDomain,
						"--timeout", "10m")
				})
			}

			if deployWithKafkaConnect && deployPG {
				g2.Go(func() error {
					dbNS := deployDbNS

					runExecStdinFn(g2Ctx, "kubectl", []string{"apply", "-f", "-"}, fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s`, dbNS))

					runHelmFn(g2Ctx, "repo", "add", "bitnami", "https://charts.bitnami.com/bitnami")
					runHelmFn(g2Ctx, "repo", "update", "bitnami")

					if err := runHelmFn(g2Ctx, "upgrade", "--install", "postgresql", "bitnami/postgresql",
						"-n", dbNS, "--create-namespace",
						"--set", "auth.postgresPassword=postgres",
						"--set", "auth.username=debezium",
						"--set", "auth.password=debezium",
						"--set", "auth.database=orders",
						"--set", "primary.extendedConfiguration=wal_level = logical\nmax_wal_senders = 10\nmax_replication_slots = 10",
						"--timeout", "5m"); err != nil {
						return err
					}

					return nil
				})
			}

			// Deploy Kafka
			if deployKafka {
				g2.Go(func() error {

					runHelmFn(g2Ctx, "dependency", "update", "charts/kafka-cluster")
					// No --wait: Helm only needs to submit the manifests; the Strimzi
					// operator drives reconciliation asynchronously. waitKafkaReady()
					// below is the real readiness gate.
					clusterDomain := report.Network.ClusterDomain
					if clusterDomain == "" {
						clusterDomain = "cluster.local"
					}
					kafkaArgs := []string{"upgrade", "--install", "krafter", "charts/kafka-cluster", "-n", kafkaNS, "--create-namespace"}

					kafkaArgs = append(kafkaArgs,
						"-f", valuesFile,
						"--set", "global.clusterDomain="+clusterDomain,
						"--set", "networkPolicies.connectNamespace="+connectNS,
						"--timeout", "10m",
					)
					if isKind {
						kafkaArgs = append(kafkaArgs, "-f", "charts/kafka-cluster/values-kind.yaml")
					}

					if deployHA {
						kafkaArgs = append(kafkaArgs,
							"--set", "kafka.replicas=3",
							"--set", "kafka.config.default\\.replication\\.factor=3",
							"--set", "kafka.config.min\\.insync\\.replicas=2",
							"--set", "zookeeper.replicas=3",
						)
					} else {
						kafkaArgs = append(kafkaArgs,
							"--set", "kafka.replicas=1",
							"--set", "kafka.config.default\\.replication\\.factor=1",
							"--set", "kafka.config.min\\.insync\\.replicas=1",
							"--set", "zookeeper.replicas=1",
						)
					}

					// (Values files are already appended above so these overrides take precedence)

					if err := runHelmFn(g2Ctx, kafkaArgs...); err != nil {
						return err
					}

					return nil
				})
			}

			if err := g2.Wait(); err != nil {
				return fmt.Errorf("failed during Group B (Core Infra) deployments: %w", err)
			}

			// ---------------------------------------------------------
			// ── Sequential Readiness Waits for Group B ───────────────
			// ---------------------------------------------------------
			if !isTesting {
				// 1. Kafka Cluster
				if deployKafka {
					dl.StartComponent("kafka", 15*time.Minute)
					if err := waitComponentReadySilent(ctx, kafkaNS, "strimzi.io/cluster=krafter", 15*time.Minute); err != nil {
						return fmt.Errorf("kafka readiness failed: %w", err)
					}
					dl.FinishComponent("kafka", true)
					advanceStep()
				}

				// 2. Monitoring Stack
				if deployWithMonitoring && deployMon {
					dl.StartComponent("monitoring", 10*time.Minute)
					if err := waitComponentReadySilent(ctx, jaegerNS, "release=monitoring", 10*time.Minute); err != nil {
						return fmt.Errorf("monitoring readiness failed: %w", err)
					}
					dl.FinishComponent("monitoring", true)
					advanceStep()
				}

				// 2. PostgreSQL
				if deployWithKafkaConnect {
					if deployPG {
						dl.StartComponent("postgres", 5*time.Minute)
						if err := waitComponentReadySilent(ctx, deployDbNS, "app.kubernetes.io/instance=postgresql", 5*time.Minute); err != nil {
							dl.FinishComponent("postgres", false)
							dl.Printf("    %s PostgreSQL not ready: %v\n", output.WarningStyle.Render("⚠"), err)
						} else {
							dl.FinishComponent("postgres", true)
							// Grant superuser and replication to debezium after DB is ready
							for i := 0; i < 5; i++ {
								err := exec.CommandContext(ctx, "kubectl", "exec", "-n", deployDbNS, "postgresql-0", "--",
									"env", "PGPASSWORD=postgres", "psql", "-U", "postgres", "-c",
									"ALTER ROLE debezium SUPERUSER REPLICATION;").Run()
								if err == nil {
									break
								}
								time.Sleep(2 * time.Second)
							}
						}
						advanceStep()
					}
				}

			}

			dl.Println("    - Applying Kafka users and topics...")
			applyManifestWithNamespace(ctx, "config/kafka/kafka-users.yaml", kafkaNS)
			applyManifestWithNamespace(ctx, "config/kafka/kafka-topics.yaml", kafkaNS)

			if !isTesting {
				dl.Println("    - Waiting for Entity Operator to start...")
				eoDeadline := time.Now().Add(5 * time.Minute)
				for time.Now().Before(eoDeadline) {
					eoOut, _ := exec.CommandContext(ctx,
						"kubectl", "get", "pods", "-n", kafkaNS,
						"-l", "app.kubernetes.io/name=entity-operator",
						"--no-headers",
						"-o", "custom-columns=PHASE:.status.phase",
					).Output()
					if strings.Contains(string(eoOut), "Running") {
						dl.Printf("    %s Entity Operator running\n", output.AccentStyle.Render("✔"))
						break
					}
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(5 * time.Second):
					}
				}

				dl.StartComponent("kafka-users", 8*time.Minute)
				err := waitKafkaUsersReadySilent(ctx, kafkaNS, 8*time.Minute)
				if err != nil {
					dl.Printf("    %s KafkaUsers not all ready after 5m — downstream deploys will retry secret lookup\n", output.WarningStyle.Render("⚠"))
				}
			}

			// 3. Kafka Connect (separate chart — deployed in connectNS)
			if deployWithKafkaConnect {
				// Ensure connect namespace exists (idempotent — won't fail if already present)
				dl.Println("    - Ensuring connect namespace exists...")
				connectNsYaml := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s`, connectNS)
				runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, connectNsYaml)

				connectDomain := report.Network.ClusterDomain
				if connectDomain == "" {
					connectDomain = "cluster.local"
				}
				bootstrap := fmt.Sprintf("krafter-kafka-bootstrap.%s.svc.%s:9092", kafkaNS, connectDomain)

				// Copy kates-connect secret and kafka-metrics ConfigMap from kafka namespace to connect namespace (cross-namespace)
				if connectNS != kafkaNS {
					// Copy kafka-metrics ConfigMap (required by KafkaConnect metricsConfig)
					dl.Println("    - Copying kafka-metrics ConfigMap to connect namespace...")
					metricsData, metricsErr := exec.CommandContext(ctx, "kubectl", "get", "configmap", "kafka-metrics",
						"-n", kafkaNS, "-o", "jsonpath={.data.kafka-metrics-config\\.yml}").Output()
					if metricsErr == nil && len(metricsData) > 0 {
						// Build a clean ConfigMap without Helm ownership annotations
						// to avoid field-manager conflicts on kubectl apply.
						var indented strings.Builder
						for _, line := range strings.Split(string(metricsData), "\n") {
							indented.WriteString("    " + line + "\n")
						}
						metricsYaml := fmt.Sprintf("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: kafka-metrics\n  namespace: %s\ndata:\n  kafka-metrics-config.yml: |\n%s", connectNS, indented.String())
						if err := runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, metricsYaml); err != nil {
							// Fallback: force ownership with server-side apply (handles leftover
							// field-manager conflicts from prior broken deploys)
							dl.Println("    - Retrying with server-side apply (--force-conflicts)...")
							runExecStdinFn(ctx, "kubectl", []string{"apply", "--server-side", "--force-conflicts", "-f", "-"}, metricsYaml)
						}
					} else {
						dl.Printf("    ⚠️  ConfigMap 'kafka-metrics' not found in namespace %s\n", kafkaNS)
					}

					dl.Println("    - Copying kates-connect credentials to connect namespace...")
					pwCmd := exec.CommandContext(ctx, "kubectl", "get", "secret", "kates-connect",
						"-n", kafkaNS, "-o", "jsonpath={.data.password}")
					pwBytes, pwErr := pwCmd.Output()

					if pwErr == nil && len(pwBytes) > 0 {
						connectSecretYaml := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: kates-connect
  namespace: %s
type: Opaque
data:
  password: %s`, connectNS, string(pwBytes))
						runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, connectSecretYaml)
					} else {
						dl.Printf("    ⚠️  Secret 'kates-connect' not found in namespace %s — KafkaUser may not be ready\n", kafkaNS)
					}
				}

				dl.Println("    - Creating PostgreSQL credentials secret for Kafka Connect...")
				pgSecretYaml := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: connect-pg-credentials
  namespace: %s
type: Opaque
stringData:
  password: debezium
  username: debezium`, connectNS)
				runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, pgSecretYaml)

				connectDeployed := isHelmReleaseDeployedFn(ctx, "connect-cluster", connectNS)
				if connectDeployed && !isTesting {
					// Helm says deployed, but verify pods actually exist.
					// A previous deploy may have installed the chart but the
					// workload never started (e.g. missing ConfigMap).
					podCheck, _ := exec.CommandContext(ctx, "kubectl", "get", "pods",
						"-n", connectNS, "-l", "strimzi.io/kind=KafkaConnect",
						"-o", "jsonpath={.items}").Output()
					if string(podCheck) == "[]" || len(strings.TrimSpace(string(podCheck))) == 0 {
						dl.Println("    ⚠️  Kafka Connect release exists but no pods found — upgrading...")
						connectDeployed = false
					}
				}
				if !connectDeployed {
					dl.Printf("\n📦 Deploying Kafka Connect (Namespace: %s)...\n", connectNS)

					registryFQDN := fmt.Sprintf("http://apicurio-apicurio-registry.%s.svc.%s:80/apis/ccompat/v7",
						kafkaNS, connectDomain)

					// Use the same FQDN pattern as the kates backend for bootstrap servers
					bootstrap := fmt.Sprintf("krafter-kafka-bootstrap.%s.svc.%s:9092", kafkaNS, connectDomain)

					connectArgs := []string{"upgrade", "--install", "connect-cluster", "charts/connect-cluster",
						"-n", connectNS, "--create-namespace",
						"-f", chartOverlay("charts/connect-cluster"),
						"--set", "clusterDomain=" + connectDomain,
						"--set", "kafka.namespace=" + kafkaNS,
						"--set", "kafka.bootstrapServers=" + bootstrap,
						"--set", "schemaRegistry.enabled=true",
						"--set", "extraConfig.schema\\.registry\\.url=" + registryFQDN,
						"--set", "databaseEgress[0].namespace=" + deployDbNS,
						"--set", "databaseEgress[0].port=5432",
						"--set", "databaseEgress[0].podSelector.app\\.kubernetes\\.io/name=postgresql",
						"--timeout", "10m",
					}

					// Enable NetworkPolicy on non-Kind clusters (same as kates backend)
					if !isKind {
						connectArgs = append(connectArgs, "--set", "networkPolicy.enabled=true")
					}

					// When Connect is in a different namespace than Kafka, brokers advertise
					// short hostnames (e.g. krafter-brokers-0.krafter-kafka-brokers.kafka.svc)
					// that only resolve within the kafka namespace DNS search domain.
					// Add the kafka namespace to Connect pod DNS search list.
					if connectNS != kafkaNS {
						kafkaSvcDomain := fmt.Sprintf("%s.svc.%s", kafkaNS, connectDomain)
						connectArgs = append(connectArgs,
							"--set", "dnsConfig.searches[0]="+kafkaSvcDomain,
						)
					}

					if deployHA {
						connectArgs = append(connectArgs, "--set", "replicas=3")
					} else {
						connectArgs = append(connectArgs, "--set", "replicas=1")
					}

					monitoringEnabled := deployWithMonitoring
					if monitoringEnabled {
						// Cleverly detect if CRDs are actually present before enabling monitoring on Connect
						out, err := exec.CommandContext(ctx, "kubectl", "get", "crd", "podmonitors.monitoring.coreos.com", "--ignore-not-found").CombinedOutput()
						if err != nil || len(bytes.TrimSpace(out)) == 0 {
							monitoringEnabled = false
							dl.Printf("    ⚠️  Monitoring CRDs not found. Disabling monitoring for Kafka Connect.\n")
						}
					}

					if !monitoringEnabled {
						connectArgs = append(connectArgs,
							"--set", "alerts.enabled=false",
							"--set", "podMonitors.enabled=false",
							"--set", "dashboards.enabled=false",
						)
					}

					if err := runHelmFn(ctx, connectArgs...); err != nil {
						return err
					}
				} else {
					dl.Println("⏭️  Kafka Connect already deployed. Skipping.")
					dl.FinishComponent("kafka-connect", true)
					advanceStep()
				}

				if !isTesting {
					dl.StartComponent("kafka-connect", 15*time.Minute)
					if err := waitComponentReadySilent(ctx, connectNS, "strimzi.io/kind=KafkaConnect", 15*time.Minute); err != nil {
						dl.FinishComponent("kafka-connect", false)
						return fmt.Errorf("Kafka Connect failed to become ready: %w", err)
					}
					dl.FinishComponent("kafka-connect", true)
					advanceStep()
				}

				// Deploy Debezium connector
				checkOut, checkErr := exec.CommandContext(ctx, "kubectl", "get", "kafkaconnector", "debezium-postgres-source", "-n", connectNS, "--no-headers").CombinedOutput()
				if checkErr != nil || strings.Contains(string(checkOut), "not found") {
					dl.Println("    - Deploying Debezium PostgreSQL CDC connector...")
					connectorYaml := fmt.Sprintf(`apiVersion: kafka.strimzi.io/v1
kind: KafkaConnector
metadata:
  name: debezium-postgres-source
  namespace: %s
  labels:
    strimzi.io/cluster: connect-cluster
spec:
  class: io.debezium.connector.postgresql.PostgresConnector
  tasksMax: 1
  autoRestart:
    enabled: true
    maxRestarts: 10
  config:
    database.hostname: postgresql.%s.svc
    database.port: "5432"
    database.user: debezium
    database.password: "${secrets:%s/connect-pg-credentials:password}"
    database.dbname: orders
    topic.prefix: cdc
    schema.include.list: public
    plugin.name: pgoutput
    slot.name: debezium_kates
    heartbeat.interval.ms: "10000"
    snapshot.mode: initial
    decimal.handling.mode: double
    tombstones.on.delete: "true"
    schema.history.internal.kafka.bootstrap.servers: %s
    schema.history.internal.kafka.security.protocol: SASL_PLAINTEXT
    schema.history.internal.kafka.sasl.mechanism: SCRAM-SHA-512
    schema.history.internal.kafka.sasl.jaas.config: "org.apache.kafka.common.security.scram.ScramLoginModule required username=\"kates-connect\" password=\"${secrets:%s/kates-connect:password}\";"
    schema.history.internal.kafka.topic: cdc-schema-history`, connectNS, deployDbNS, connectNS, bootstrap, connectNS)
					if err := runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, connectorYaml); err != nil {
						dl.Printf("    %s Failed to deploy Debezium connector: %v\n", output.WarningStyle.Render("⚠"), err)
						dl.FinishComponent("kafka-connector", false)
					} else {
						dl.Printf("    %s Debezium PostgreSQL CDC connector deployed\n", output.SuccessStyle.Render("✔"))
					}
				} else {
					dl.Printf("    %s Debezium connector already exists — skipping\n", output.SuccessStyle.Render("✔"))
				}

				// Deploy JDBC Sink connector
				sinkCheckOut, sinkCheckErr := exec.CommandContext(ctx, "kubectl", "get", "kafkaconnector", "jdbc-sink-connector", "-n", connectNS, "--no-headers").CombinedOutput()
				if sinkCheckErr != nil || strings.Contains(string(sinkCheckOut), "not found") {
					dl.Println("    - Deploying JDBC Sink connector...")
					sinkYaml := fmt.Sprintf(`apiVersion: kafka.strimzi.io/v1
kind: KafkaConnector
metadata:
  name: jdbc-sink-connector
  namespace: %s
  labels:
    strimzi.io/cluster: connect-cluster
spec:
  class: io.debezium.connector.jdbc.JdbcSinkConnector
  tasksMax: 1
  autoRestart:
    enabled: true
    maxRestarts: 10
  config:
    topics: "test-sink-topic"
    connection.url: "jdbc:postgresql://postgresql.%s.svc:5432/orders"
    connection.username: "${secrets:%s/connect-pg-credentials:username}"
    connection.password: "${secrets:%s/connect-pg-credentials:password}"
    insert.mode: "insert"
    auto.create: "true"
    auto.evolve: "true"`, connectNS, deployDbNS, connectNS, connectNS)
					if err := runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, sinkYaml); err != nil {
						dl.Printf("    %s Failed to deploy JDBC Sink connector: %v\n", output.WarningStyle.Render("⚠"), err)
					} else {
						dl.Printf("    %s JDBC Sink connector deployed\n", output.SuccessStyle.Render("✔"))
					}
				} else {
					dl.Printf("    %s JDBC Sink connector already exists — skipping\n", output.SuccessStyle.Render("✔"))
				}

				if !isTesting {
					dl.StartComponent("kafka-connector", 10*time.Minute)
					if err := waitConnectorReadySilent(ctx, connectNS, 10*time.Minute); err != nil {
						dl.FinishComponent("kafka-connector", false)
						return fmt.Errorf("Kafka Connectors failed to become ready: %w", err)
					}
					dl.FinishComponent("kafka-connector", true)
				}
				advanceStep()
			}

			// ---------------------------------------------------------
			// GROUP C (Apps / Sequential)
			// ---------------------------------------------------------
			// Deploy Schema Registry (if requested)
			if deployWithSchemaRegistry == "apicurio" {
				if !isHelmReleaseDeployedFn(ctx, "apicurio", kafkaNS) {
					dl.Printf("\n📦 Deploying Apicurio Schema Registry (Namespace: %s)...\n", kafkaNS)
					dl.StartComponent("apicurio", 5*time.Minute)
					if err := runHelmFn(ctx, "upgrade", "--install", "apicurio", "charts/apicurio-registry", "-n", kafkaNS, "--create-namespace", "--timeout", "5m"); err != nil {
						dl.FinishComponent("apicurio", false)
						return err
					}
					dl.FinishComponent("apicurio", true)
					advanceStep()
				} else {
					dl.Println("⏭️  Apicurio already deployed.")
					dl.FinishComponent("apicurio", true)
					advanceStep()
				}
			}

			// Deploy Kates
			if !isHelmReleaseDeployedFn(ctx, "kates", appNS) {
				dl.Printf("\n📦 Deploying Kates Backend (Namespace: %s)...\n", appNS)
				dl.StartComponent("kates", 8*time.Minute)

				// Auto-cleanup stale ClusterRole ownership from previous topology switches
				cleanupStaleClusterResource(ctx, "clusterrole", "kates", appNS)
				cleanupStaleClusterResource(ctx, "clusterrolebinding", "kates", appNS)

				if kafkaNS != appNS {
					// Ensure app namespace exists before copying secrets into it.
					nsYaml := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
spec: {}`, appNS)
					runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, nsYaml)

					// The KafkaUser was already waited on in Group B — just read the Secret.
					pwCmd := exec.CommandContext(ctx, "kubectl", "get", "secret", "kates-backend",
						"-n", kafkaNS, "-o", "jsonpath={.data.password}")
					pwBytes, pwErr := pwCmd.Output()

					if pwErr == nil && len(pwBytes) > 0 {
						dl.Println("    - Copying Kafka SASL credentials to app namespace...")
						secretYaml := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: kates-backend
  namespace: %s
type: Opaque
data:
  password: %s`, appNS, string(pwBytes))
						runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, secretYaml)
					} else {
						dl.Printf("    ⚠️  Secret 'kates-backend' not found in namespace %s — KafkaUser may not be ready\n", kafkaNS)
					}
				}

				bootstrap := fmt.Sprintf("krafter-kafka-bootstrap.%s.svc.%s:9092", kafkaNS, report.Network.ClusterDomain)
				dl.Println("    - Waiting for Kates backend pods to become ready (this may take 2-3 minutes)...")
				if err := runHelmFn(ctx, "upgrade", "--install", "kates", "charts/kates",
					"-n", appNS, "--create-namespace",
					"-f", valuesFile,
					"-f", chartOverlay("charts/kates"),
					"--set", "kafka.bootstrapServers="+bootstrap,
					"--set", fmt.Sprintf("monitoring.enabled=%t", deployWithMonitoring),
					"--timeout", "8m"); err != nil {
					return err
				}

				if !isTesting {
					if err := waitComponentReadySilent(ctx, appNS, "app.kubernetes.io/instance=kates", 8*time.Minute); err != nil {
						dl.FinishComponent("kates", false)
						return err
					}
				}
				dl.FinishComponent("kates", true)
				advanceStep()
			} else {
				dl.Println("⏭️  Kates Backend already deployed.")
				dl.FinishComponent("kates", true)
				advanceStep()
			}

			// Deploy Chaos
			if deployWithChaos {
				if !isHelmReleaseDeployedFn(ctx, "chaos", chaosNS) {
					dl.Printf("\n📦 Deploying Litmus Chaos (Namespace: %s)...\n", chaosNS)
					dl.StartComponent("chaos", 5*time.Minute)
					cleanupStaleClusterResource(ctx, "clusterrole", "litmus", chaosNS)
					cleanupStaleClusterResource(ctx, "clusterrolebinding", "litmus", chaosNS)
					runHelmFn(ctx, "dependency", "update", "charts/kates-chaos")
					dl.Println("    - Waiting for Litmus Chaos pods to become ready (this may take a few minutes)...")
					if err := runHelmFn(ctx, "upgrade", "--install", "chaos", "charts/kates-chaos",
						"-n", chaosNS, "--create-namespace",
						"-f", valuesFile,
						"-f", chartOverlay("charts/kates-chaos"),
						"--set", "rbac.kafkaNamespace="+kafkaNS,
						"--timeout", "5m"); err != nil {
						return err
					}

					if !isTesting {
						if err := waitComponentReadySilent(ctx, chaosNS, "app.kubernetes.io/instance=chaos", 5*time.Minute); err != nil {
							dl.FinishComponent("chaos", false)
							return err
						}
					}
					dl.FinishComponent("chaos", true)
					advanceStep()
				} else {
					dl.Println("⏭️  Litmus Chaos already deployed.")
					dl.FinishComponent("chaos", true)
					advanceStep()
				}
			}

			// Use shared entries for summary (single source of truth)
			finalEntries = sharedEntries
			deployElapsed = time.Since(deployStartTime)
			return nil
		}()
	}()

	if _, err := p.Run(); err != nil {
		return err
	}

	<-doneCh // Wait for deployment goroutine to fully exit

	if deployErr == nil && len(finalEntries) > 0 {
		RenderDeployDashboard(ctx, finalEntries, deployElapsed)

		// Automatically sync API key from deployed cluster to active context
		updateActiveContextAPIKey(ctx, appNS)

		if deployPortForward {
			RunPortForwards(ctx, kafkaNS, appNS, jaegerNS)
		}
	}

	return deployErr
}

func updateActiveContextAPIKey(ctx context.Context, appNS string) {
	out, err := exec.CommandContext(ctx, "kubectl", "get", "secret", "kates-api-key", "-n", appNS, "-o", "jsonpath={.data.api-key}").Output()
	if err != nil {
		return
	}
	encoded := strings.TrimSpace(string(out))
	if encoded == "" {
		return
	}
	if decoded, err := base64.StdEncoding.DecodeString(encoded); err == nil {
		apiKey := strings.TrimSpace(string(decoded))
		if apiKey != "" {
			cfg := loadConfig()
			ctxName := cfg.CurrentContext
			if contextFlag != "" {
				ctxName = contextFlag
			}
			if active, ok := cfg.Contexts[ctxName]; ok {
				active.APIKey = apiKey
				cfg.Contexts[ctxName] = active
				_ = saveConfig(cfg)
				dl.Printf("    ✓ Automatically synced API Key to context %q: %s****\n\n", ctxName, apiKey[:4])
			}
		}
	}
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
		dl.Printf("    \033[2m$ %s %s\033[0m\n", name, strings.Join(args, " "))
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
			dl.Printf("    \033[31m%s\033[0m\n", errMsg)
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
		dl.Printf("    \033[2m$ %s %s\033[0m\n", name, strings.Join(args, " "))
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
			dl.Printf("    \033[31m%s\033[0m\n", errMsg)
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

// applyManifestWithNamespace reads a YAML file, strips any hardcoded
// `namespace:` fields from metadata, and applies it to the given namespace.
// This ensures manifests work correctly regardless of deployment topology.
var nsLineRegex = regexp.MustCompile(`(?m)^\s+namespace:\s+\S+\s*$`)

func applyManifestWithNamespace(ctx context.Context, file, namespace string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", file, err)
	}
	// Strip hardcoded namespace lines so -n flag takes effect
	stripped := nsLineRegex.ReplaceAllString(string(data), "")
	return runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-", "-n", namespace}, stripped)
}

func sliceContains(s []string, v string) bool {
	for _, item := range s {
		if item == v {
			return true
		}
	}
	return false
}
