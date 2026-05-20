package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"github.com/klster/kates-cli/output"
	"github.com/klster/kates-cli/pkg/detect"
	"github.com/spf13/cobra"
	"github.com/charmbracelet/huh"
)

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
	deployAppNS              string
	deployChaosNS            string
	deployMonitoringNS       string
	deployWithSchemaRegistry string
	deployWithChaos          bool
	deployWithMonitoring     bool
	deployWithCertManager    bool
	deployWithKyverno        bool
	deployWithSecretManager  bool
	deployInteractive        bool
	deployVerbose            bool
	deployPortForward        bool
)

func init() {
	deployCmd.Flags().StringVar(&deployTopology, "topology", "isolated", "Deployment topology: 'isolated' (separate namespaces) or 'single' (one namespace)")
	deployCmd.Flags().StringVar(&deployNamespace, "namespace", "kates-stack", "Target namespace when topology is 'single'")
	deployCmd.Flags().StringVar(&deployKafkaNS, "kafka-ns", "kafka", "Namespace for Kafka when topology is 'isolated'")
	deployCmd.Flags().StringVar(&deployAppNS, "app-ns", "kates", "Namespace for Kates Backend when topology is 'isolated'")
	deployCmd.Flags().StringVar(&deployChaosNS, "chaos-ns", "litmus", "Namespace for Chaos Engine when topology is 'isolated'")
	deployCmd.Flags().StringVar(&deployMonitoringNS, "monitoring-ns", "monitoring", "Namespace for monitoring components (Jaeger) when topology is 'isolated'")
	
	// Component flags
	deployCmd.Flags().StringVar(&deployWithSchemaRegistry, "with-schema-registry", "none", "Schema Registry to deploy: 'none', 'apicurio', or 'confluent'")
	deployCmd.Flags().BoolVar(&deployWithChaos, "with-chaos", true, "Deploy LitmusChaos engine")
	deployCmd.Flags().BoolVar(&deployWithMonitoring, "with-monitoring", true, "Deploy monitoring components (Prometheus/Grafana/Jaeger)")
	deployCmd.Flags().BoolVar(&deployWithCertManager, "with-cert-manager", true, "Deploy Cert-Manager for TLS certificate management")
	deployCmd.Flags().BoolVar(&deployWithKyverno, "with-kyverno", false, "Deploy Kyverno for cluster policy enforcement")
	deployCmd.Flags().BoolVar(&deployWithSecretManager, "with-secret-manager", false, "Deploy Secret Manager (e.g., External Secrets Operator)")
	deployCmd.Flags().BoolVarP(&deployInteractive, "interactive", "i", false, "Use interactive UI to configure deployment")
	deployCmd.Flags().BoolVar(&deployVerbose, "verbose", false, "Show every kubectl/helm command as it runs")
	deployCmd.Flags().BoolVarP(&deployPortForward, "port-forward", "P", false, "After deploy, start port-forwards for all services and keep running until Ctrl+C")

	rootCmd.AddCommand(deployCmd)
}

func runDeploy(cmd *cobra.Command, args []string) error {
	deployStartTime := time.Now()
	PrintDeployBanner()
	
	if deployInteractive || cmd.Flags().NFlag() == 0 {
		var components []string

		form := huh.NewForm(
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
						huh.NewOption("None", "none"),
						huh.NewOption("Apicurio", "apicurio"),
					).
					Value(&deployWithSchemaRegistry),

				huh.NewMultiSelect[string]().
					Title("Select Additional Components").
					Description("Use space to toggle, enter to confirm").
					Options(
						huh.NewOption("🧪 Litmus Chaos Engine", "chaos").Selected(deployWithChaos),
						huh.NewOption("📊 Monitoring (Grafana + Prometheus)", "monitoring").Selected(deployWithMonitoring),
						huh.NewOption("🔐 Cert-Manager (TLS)", "cert-manager").Selected(deployWithCertManager),
						huh.NewOption("🛡️  Kyverno (Policies)", "kyverno").Selected(deployWithKyverno),
					).
					Value(&components),
			),

			// ── Group 2: Namespace configuration (isolated topology only) ────────
			huh.NewGroup(
				huh.NewInput().
					Title("Kafka Namespace").
					Description("Namespace for Strimzi operator and Kafka cluster").
					Value(&deployKafkaNS),
				huh.NewInput().
					Title("Kates App Namespace").
					Description("Namespace for the Kates backend service").
					Value(&deployAppNS),
				huh.NewInput().
					Title("Monitoring Namespace").
					Description("Namespace for Jaeger and monitoring components").
					Value(&deployMonitoringNS),
				huh.NewInput().
					Title("Chaos Namespace").
					Description("Namespace for Litmus Chaos engine").
					Value(&deployChaosNS),
			).WithHideFunc(func() bool { return deployTopology == "single" }),
		).WithTheme(ThemeKates())

		err := form.Run()
		if err != nil {
			return err
		}

		deployWithChaos = false
		deployWithMonitoring = false
		deployWithCertManager = false
		deployWithKyverno = false

		for _, c := range components {
			switch c {
			case "chaos": deployWithChaos = true
			case "monitoring": deployWithMonitoring = true
			case "cert-manager": deployWithCertManager = true
			case "kyverno": deployWithKyverno = true
			}
		}
	}
	
	// 1. Resolve Topology
	PrintPhaseHeader(1, fmt.Sprintf("Resolving Namespace Topology (%s mode)", deployTopology))
	if deployTopology == "single" {
		PrintPhaseItem(fmt.Sprintf("All components → %s", deployNamespace))
	} else {
		PrintPhaseItem(fmt.Sprintf("Kafka        → %s", deployKafkaNS))
		PrintPhaseItem(fmt.Sprintf("Kates App    → %s", deployAppNS))
		if deployWithMonitoring {
			PrintPhaseItem(fmt.Sprintf("Monitoring   → %s", deployMonitoringNS))
		}
		PrintPhaseItem(fmt.Sprintf("Chaos        → %s", deployChaosNS))
	}

	// 2. Component Selection
	PrintPhaseHeader(2, "Component Selection")
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
				fmt.Println("🚀 Creating Kind cluster via 'make cluster'...")
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
	
	// 4. Execution Plan (Helm)
	PrintPhaseHeader(4, "Executing Deployment Pipeline")
	
	var kafkaNS, appNS, chaosNS, jaegerNS string
	if deployTopology == "single" {
		kafkaNS, appNS, chaosNS, jaegerNS = deployNamespace, deployNamespace, deployNamespace, deployNamespace
	} else {
		kafkaNS, appNS, chaosNS, jaegerNS = deployKafkaNS, deployAppNS, deployChaosNS, deployMonitoringNS
	}
	
	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ---------------------------------------------------------
	// GROUP A: Operators & CRDs (Parallel)
	// ---------------------------------------------------------
	g, gCtx := errgroup.WithContext(ctx)
	
	// Deploy Strimzi Operator
	g.Go(func() error {
		if isHelmReleaseDeployedFn(gCtx, "strimzi-operator", "strimzi-operator") {
			fmt.Println("⏭️  Strimzi Operator already deployed. Skipping.")
			return nil
		}
		fmt.Println("\n🚀 Deploying Strimzi Operator (Namespace: strimzi-operator)...")
		// Create namespace properly
		runExecStdinFn(gCtx, "kubectl", []string{"apply", "-f", "-"}, `apiVersion: v1
kind: Namespace
metadata:
  name: strimzi-operator`)
		err := runHelmFn(gCtx, "upgrade", "--install", "strimzi-operator", "oci://quay.io/strimzi-helm/strimzi-kafka-operator", "--version", "1.0.0", "-n", "strimzi-operator", "--set", "watchAnyNamespace=true", "--set", "replicas=1", "--timeout", "5m", "--wait")
		if err != nil { return err }
		
		fmt.Println("    - Waiting for Strimzi CRDs to be established...")
		return runExecFn(gCtx, "kubectl", "wait", "--for=condition=Established", "crd", "kafkas.kafka.strimzi.io", "--timeout=60s")
	})
	
	// Deploy Cert-Manager
	if deployWithCertManager {
		g.Go(func() error {
			if isHelmReleaseDeployedFn(gCtx, "cert-manager", "cert-manager") {
				fmt.Println("⏭️  Cert-Manager already deployed. Skipping.")
				return nil
			}
			fmt.Printf("\n🚀 Deploying Cert-Manager (Namespace: %s)...\n", "cert-manager")
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
				"--timeout", "10m", "--wait")
			if err != nil {
				return err
			}

			fmt.Println("    - Waiting for Cert-Manager CRDs to be established...")
			if err := runExecFn(gCtx, "kubectl", "wait", "--for=condition=Established",
				"crd", "clusterissuers.cert-manager.io", "--timeout=60s"); err != nil {
				return err
			}

			// On re-deploys the cainjector may hold a stale CA from the previous
			// installation. Restart it to force fresh CA bundle generation and
			// re-injection into the MutatingWebhookConfiguration.
			fmt.Println("    - Restarting Cert-Manager CA injector for fresh CA bundle...")
			runExecFn(gCtx, "kubectl", "rollout", "restart",
				"deployment/cert-manager-cainjector", "-n", "cert-manager")
			runExecFn(gCtx, "kubectl", "rollout", "status",
				"deployment/cert-manager-cainjector", "-n", "cert-manager", "--timeout=90s")

			// The definitive readiness signal: poll the MutatingWebhookConfiguration
			// until caBundle is non-empty. Only then can the API server verify the
			// webhook TLS certificate without x509: certificate signed by unknown authority.
			fmt.Println("    - Polling for Cert-Manager CA bundle injection...")
			const caTimeout = 120 * time.Second
			const caPoll = 3 * time.Second
			caDeadline := time.Now().Add(caTimeout)
			for {
				out, err := exec.CommandContext(gCtx,
					"kubectl", "get",
					"mutatingwebhookconfiguration", "cert-manager-webhook",
					"-o", "jsonpath={.webhooks[0].clientConfig.caBundle}",
				).Output()
				if err == nil && len(strings.TrimSpace(string(out))) > 0 {
					fmt.Println("    - CA bundle injected. Applying ClusterIssuer...")
					break
				}
				if time.Now().After(caDeadline) {
					fmt.Println("    ⚠ CA bundle injection timed out — proceeding with webhook bypass fallback")
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
			fmt.Println("    ⚠ Webhook TLS not verifiable — temporarily setting failurePolicy: Ignore")
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
			fmt.Println("    - ClusterIssuer created. Webhook policy restored.")
			return nil
		})
	}
	
	// Deploy Kyverno
	if deployWithKyverno {
		g.Go(func() error {
			if isHelmReleaseDeployedFn(gCtx, "kyverno", "kyverno") {
				fmt.Println("⏭️  Kyverno already deployed. Skipping.")
				return nil
			}
			fmt.Println("\n🚀 Deploying Kyverno (Namespace: kyverno)...")
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
				"--timeout", "5m", "--wait")
			if err != nil {
				return err
			}

			// Wait for Kyverno CRDs before anything downstream tries to use them.
			fmt.Println("    - Waiting for Kyverno CRDs to be established...")
			return runExecFn(gCtx, "kubectl", "wait", "--for=condition=Established",
				"crd", "clusterpolicies.kyverno.io",
				"--timeout=60s")
		})
	}

	if err := g.Wait(); err != nil {
		output.Error(fmt.Sprintf("Failed during Group A (Operators) deployments: %v", err))
		return err
	}
	
	// Bust Kubernetes Discovery Cache so Helm knows about the newly created CRDs
	fmt.Println("    - Refreshing API server schema cache...")
	if home, err := os.UserHomeDir(); err == nil {
		os.RemoveAll(fmt.Sprintf("%s/.kube/cache/discovery", home))
		os.RemoveAll(fmt.Sprintf("%s/.cache/helm", home))
	}

	// ---------------------------------------------------------
	// GROUP B: Core Infrastructure (Parallel)
	// ---------------------------------------------------------
	g2, g2Ctx := errgroup.WithContext(ctx)

	// Deploy Monitoring (Prometheus + Grafana via charts/monitoring)
	if deployWithMonitoring {
		g2.Go(func() error {
			if isHelmReleaseDeployedFn(g2Ctx, "monitoring", jaegerNS) {
				fmt.Println("⏭️  Monitoring stack already deployed. Skipping.")
				return nil
			}
			fmt.Printf("\n🚀 Deploying Monitoring (Prometheus + Grafana) (Namespace: %s)...\n", jaegerNS)

			// Update chart dependencies (kube-prometheus-stack subchart).
			runHelmFn(g2Ctx, "dependency", "update", "charts/monitoring")

			return runHelmFn(g2Ctx, "upgrade", "--install", "monitoring",
				"charts/monitoring",
				"-n", jaegerNS, "--create-namespace",
				"-f", chartOverlay("charts/monitoring"),
				"--set", "kube-prometheus-stack.global.clusterDomain="+report.Network.ClusterDomain,
				"--timeout", "10m", "--wait")
		})
	}

	
	// Deploy Kafka
	g2.Go(func() error {
		if !isHelmReleaseDeployedFn(g2Ctx, "krafter", kafkaNS) {
			fmt.Printf("\n🚀 Deploying Kafka Cluster (Namespace: %s)...\n", kafkaNS)

			runHelmFn(g2Ctx, "dependency", "update", "charts/kafka-cluster")
			// No --wait: Helm only needs to submit the manifests; the Strimzi
			// operator drives reconciliation asynchronously. waitKafkaReady()
			// below is the real readiness gate.
			clusterDomain := report.Network.ClusterDomain
			if clusterDomain == "" {
				clusterDomain = "cluster.local"
			}
			kafkaArgs := []string{
				"upgrade", "--install", "krafter", "charts/kafka-cluster",
				"-n", kafkaNS, "--create-namespace",
				"-f", valuesFile,
				"--set", "global.clusterDomain=" + clusterDomain,
				"--timeout", "10m",
			}
			if isKind {
				kafkaArgs = append(kafkaArgs, "-f", "charts/kafka-cluster/values-kind.yaml")
			}
			if err := runHelmFn(g2Ctx, kafkaArgs...); err != nil {
				return err
			}
		} else {
			fmt.Println("⏭️  Kafka chart already deployed. Verifying readiness...")
		}


		// Live progress poller — shows broker/controller/EO counts every 6s.
		// waitKafkaReady already confirms Ready=True on the Kafka CR, which
		// Strimzi only sets after the Entity Operator is also healthy.
		// No separate EO wait needed.
		if err := waitKafkaReady(g2Ctx, kafkaNS, 12*time.Minute); err != nil {
			return fmt.Errorf("kafka readiness failed: %w", err)
		}

		fmt.Println("    - Applying Kafka users and topics...")
		applyManifestWithNamespace(g2Ctx, "config/kafka/kafka-users.yaml", kafkaNS)
		applyManifestWithNamespace(g2Ctx, "config/kafka/kafka-topics.yaml", kafkaNS)

		// Wait here (still inside Group B) for the Entity Operator to process the
		// KafkaUser and write the SCRAM Secret. The EO finishes its health-check
		// (making the Kafka CR Ready) before it establishes its admin Kafka
		// connection, so we give it a generous 5-minute window.
		// By blocking Group B here, Group C is guaranteed to find the Secret
		// already populated — no waiting needed in the Kates deploy step.
		fmt.Println("    - Waiting for all KafkaUsers to be provisioned...")
		if err := runExecFn(g2Ctx, "kubectl", "wait",
			"kafkauser", "--all",
			"--for=condition=Ready",
			"--timeout=3m",
			"-n", kafkaNS,
		); err != nil {
			// Non-fatal: Group C will attempt a direct secret read and warn if missing.
			fmt.Printf("    ⚠️  KafkaUsers not all ready after 3m: %v\n", err)
		} else {
			fmt.Println("    ✅ All KafkaUser credentials ready")
		}
		return nil
	})


	if err := g2.Wait(); err != nil {
		output.Error(fmt.Sprintf("Failed during Group B (Core Infra) deployments: %v", err))
		return err
	}

	// ---------------------------------------------------------
	// GROUP C (Apps / Sequential)
	// ---------------------------------------------------------
	// Deploy Schema Registry (if requested)
	if deployWithSchemaRegistry == "apicurio" {
		if !isHelmReleaseDeployedFn(ctx, "apicurio", kafkaNS) {
			fmt.Printf("\n🚀 Deploying Apicurio Schema Registry (Namespace: %s)...\n", kafkaNS)
			if err := runHelmFn(ctx, "upgrade", "--install", "apicurio", "charts/apicurio-registry", "-n", kafkaNS, "--create-namespace", "--timeout", "5m"); err != nil {
				return err
			}
		} else {
			fmt.Println("⏭️  Apicurio already deployed.")
		}
	}
	
	// Deploy Kates
	if !isHelmReleaseDeployedFn(ctx, "kates", appNS) {
		fmt.Printf("\n🚀 Deploying Kates Backend (Namespace: %s)...\n", appNS)
		
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
				fmt.Println("    - Copying Kafka SASL credentials to app namespace...")
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
				fmt.Printf("    ⚠️  Secret 'kates-backend' not found in namespace %s — KafkaUser may not be ready\n", kafkaNS)
			}
		}
		
		bootstrap := fmt.Sprintf("krafter-kafka-bootstrap.%s.svc.%s:9092", kafkaNS, report.Network.ClusterDomain)
		if err := runHelmFn(ctx, "upgrade", "--install", "kates", "charts/kates",
			"-n", appNS, "--create-namespace",
			"-f", valuesFile,
			"-f", chartOverlay("charts/kates"),
			"--set", "kafka.bootstrapServers="+bootstrap,
			"--timeout", "8m", "--wait"); err != nil {
			return err
		}
	} else {
		fmt.Println("⏭️  Kates Backend already deployed.")
	}
	
	// Deploy Chaos
	if deployWithChaos {
		if !isHelmReleaseDeployedFn(ctx, "chaos", chaosNS) {
			fmt.Printf("\n🚀 Deploying Litmus Chaos (Namespace: %s)...\n", chaosNS)
			cleanupStaleClusterResource(ctx, "clusterrole", "litmus", chaosNS)
			cleanupStaleClusterResource(ctx, "clusterrolebinding", "litmus", chaosNS)
			runHelmFn(ctx, "dependency", "update", "charts/kates-chaos")
			if err := runHelmFn(ctx, "upgrade", "--install", "chaos", "charts/kates-chaos",
				"-n", chaosNS, "--create-namespace",
				"-f", valuesFile,
				"-f", chartOverlay("charts/kates-chaos"),
				"--set", "rbac.kafkaNamespace="+kafkaNS,
				"--timeout", "5m", "--wait"); err != nil {
				return err
			}
		} else {
			fmt.Println("⏭️  Litmus Chaos already deployed.")
		}
	}

	// ---------------------------------------------------------
	// Deployment Summary Dashboard
	// ---------------------------------------------------------
	entries := []DeploySummaryEntry{
		{Icon: "☸️", Name: "Strimzi Operator", Release: "strimzi-operator", Namespace: "strimzi-operator", Group: "A"},
	}
	if deployWithCertManager {
		entries = append(entries, DeploySummaryEntry{Icon: "🔐", Name: "Cert-Manager", Release: "cert-manager", Namespace: "cert-manager", Group: "A"})
	}
	if deployWithKyverno {
		entries = append(entries, DeploySummaryEntry{Icon: "🛡️", Name: "Kyverno", Release: "kyverno", Namespace: "kyverno", Group: "A"})
	}
	entries = append(entries, DeploySummaryEntry{Icon: "📨", Name: "Kafka (krafter)", Release: "krafter", Namespace: kafkaNS, Group: "B"})
	if deployWithMonitoring {
		entries = append(entries, DeploySummaryEntry{Icon: "📊", Name: "Monitoring (Prometheus + Grafana)", Release: "monitoring", Namespace: jaegerNS, Group: "B"})
	}
	if deployWithSchemaRegistry == "apicurio" {
		entries = append(entries, DeploySummaryEntry{Icon: "📋", Name: "Apicurio Registry", Release: "apicurio", Namespace: kafkaNS, Group: "C"})
	}
	entries = append(entries, DeploySummaryEntry{Icon: "🚀", Name: "Kates Backend", Release: "kates", Namespace: appNS, Group: "C"})
	if deployWithChaos {
		entries = append(entries, DeploySummaryEntry{Icon: "🧪", Name: "Litmus Chaos", Release: "chaos", Namespace: chaosNS, Group: "C"})
	}

	RenderDeployDashboard(ctx, entries, time.Since(deployStartTime))

	if deployPortForward {
		RunPortForwards(ctx, kafkaNS, appNS, jaegerNS)
	}

	return nil
}

// Helpers

var (
	runExecFn = runExecDefault
	runExecStdinFn = runExecStdinDefault
	runHelmFn = runHelmDefault
	isHelmReleaseDeployedFn = isHelmReleaseDeployedDefault
	defaultExecutor detect.CommandExecutor = detect.NewOSExecutor()
)

var execMutex sync.Mutex

func runExecDefault(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	
	// Prevent interwoven output lines for parallel commands
	execMutex.Lock()
	defer execMutex.Unlock()
	
	if deployVerbose {
		// Show the command being run
		fmt.Printf("    \033[2m$ %s %s\033[0m\n", name, strings.Join(args, " "))
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
			fmt.Printf("    \033[31m%s\033[0m\n", errMsg)
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
		fmt.Printf("    \033[2m$ %s %s\033[0m\n", name, strings.Join(args, " "))
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
			fmt.Printf("    \033[31m%s\033[0m\n", errMsg)
		}
		return runErr
	}
	return nil
}

func runHelmDefault(ctx context.Context, args ...string) error {
	return runExecFn(ctx, "helm", args...)
}

func isHelmReleaseDeployedDefault(ctx context.Context, release, namespace string) bool {
	// Create context with short timeout
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, "helm", "status", release, "-n", namespace)
	return cmd.Run() == nil
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
		fmt.Printf("    - Cleaning stale %s/%s (owned by namespace %q, deploying to %q)\n", kind, name, existingNS, expectedNS)
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
