package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"github.com/klster/kates-cli/output"
	"github.com/klster/kates-cli/pkg/detect"
	"github.com/spf13/cobra"
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
	deployWithSchemaRegistry string
	deployWithChaos          bool
	deployWithMonitoring     bool
	deployWithCertManager    bool
	deployWithKyverno        bool
	deployWithSecretManager  bool
)

func init() {
	deployCmd.Flags().StringVar(&deployTopology, "topology", "isolated", "Deployment topology: 'isolated' (separate namespaces) or 'single' (one namespace)")
	deployCmd.Flags().StringVar(&deployNamespace, "namespace", "kates-stack", "Target namespace when topology is 'single'")
	deployCmd.Flags().StringVar(&deployKafkaNS, "kafka-ns", "kafka", "Namespace for Kafka when topology is 'isolated'")
	deployCmd.Flags().StringVar(&deployAppNS, "app-ns", "kates", "Namespace for Kates Backend when topology is 'isolated'")
	deployCmd.Flags().StringVar(&deployChaosNS, "chaos-ns", "litmus", "Namespace for Chaos Engine when topology is 'isolated'")
	
	// Component flags
	deployCmd.Flags().StringVar(&deployWithSchemaRegistry, "with-schema-registry", "none", "Schema Registry to deploy: 'none', 'apicurio', or 'confluent'")
	deployCmd.Flags().BoolVar(&deployWithChaos, "with-chaos", true, "Deploy LitmusChaos engine")
	deployCmd.Flags().BoolVar(&deployWithMonitoring, "with-monitoring", true, "Deploy monitoring components (Prometheus/Grafana/Jaeger)")
	deployCmd.Flags().BoolVar(&deployWithCertManager, "with-cert-manager", true, "Deploy Cert-Manager for TLS certificate management")
	deployCmd.Flags().BoolVar(&deployWithKyverno, "with-kyverno", false, "Deploy Kyverno for cluster policy enforcement")
	deployCmd.Flags().BoolVar(&deployWithSecretManager, "with-secret-manager", false, "Deploy Secret Manager (e.g., External Secrets Operator)")

	rootCmd.AddCommand(deployCmd)
}

func runDeploy(cmd *cobra.Command, args []string) error {
	fmt.Println("🚀 Initializing Kates Unified Orchestrator...")
	
	// 1. Resolve Topology
	fmt.Printf("\n[1] Resolving Namespace Topology (%s mode)...\n", deployTopology)
	if deployTopology == "single" {
		fmt.Printf("    - All components will be deployed to namespace: %s\n", deployNamespace)
	} else {
		fmt.Printf("    - Kafka Namespace: %s\n", deployKafkaNS)
		fmt.Printf("    - Kates App Namespace: %s\n", deployAppNS)
		fmt.Printf("    - Chaos Namespace: %s\n", deployChaosNS)
	}

	// 2. Component Selection
	fmt.Println("\n[2] Component Selection...")
	fmt.Printf("    - Schema Registry: %s\n", deployWithSchemaRegistry)
	fmt.Printf("    - Chaos Engine: %v\n", deployWithChaos)
	fmt.Printf("    - Monitoring: %v\n", deployWithMonitoring)
	fmt.Printf("    - Cert-Manager: %v\n", deployWithCertManager)
	fmt.Printf("    - Kyverno: %v\n", deployWithKyverno)
	fmt.Printf("    - Secret Manager: %v\n", deployWithSecretManager)

	// 3. Cluster Detection
	fmt.Println("\n[3] Running Cluster Introspection (Pre-flight)...")
	executor := defaultExecutor
	collector := detect.NewCollector(executor)
	
	if err := collector.Preflight(); err != nil {
		output.Error(fmt.Sprintf("Preflight failed: %v", err))
		return err
	}
	
	report, err := collector.Collect(context.Background())
	if err != nil {
		output.Error(fmt.Sprintf("Introspection failed: %v", err))
		return err
	}
	
	analyzer := detect.NewAnalyzer(executor)
	analyzer.Analyze(report, detect.ParsedReqs{})
	
	fmt.Println("    - Generating values-detected.yaml...")
	valuesFile := ".build/values-detected.yaml"
	os.MkdirAll(".build", 0755)
	
	f, err := os.Create(valuesFile)
	if err != nil {
		return fmt.Errorf("failed to create values file: %v", err)
	}
	defer f.Close()
	
	detect.RenderValuesWithReserve(report, "krafter", 0.30, f)
	
	// 4. Execution Plan (Helm)
	fmt.Println("\n[4] Executing Deployment Pipeline...")
	
	var kafkaNS, appNS, chaosNS string
	if deployTopology == "single" {
		kafkaNS, appNS, chaosNS = deployNamespace, deployNamespace, deployNamespace
	} else {
		kafkaNS, appNS, chaosNS = deployKafkaNS, deployAppNS, deployChaosNS
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
		err := runHelmFn(gCtx, "upgrade", "--install", "strimzi-operator", "oci://quay.io/strimzi-helm/strimzi-kafka-operator", "--version", "0.40.0", "-n", "strimzi-operator", "--set", "watchAnyNamespace=true", "--set", "replicas=1", "--timeout", "5m", "--wait")
		if err != nil { return err }
		
		fmt.Println("    - Waiting for Strimzi CRDs to be established...")
		return runExecFn(gCtx, "kubectl", "wait", "--for=condition=Established", "crd", "kafkas.kafka.strimzi.io", "--timeout=60s")
	})
	
	// Deploy Cert-Manager
	if deployWithCertManager {
		g.Go(func() error {
			if isHelmReleaseDeployedFn(gCtx, "cert-manager", kafkaNS) {
				fmt.Println("⏭️  Cert-Manager already deployed. Skipping.")
				return nil
			}
			fmt.Printf("\n🚀 Deploying Cert-Manager (Namespace: %s)...\n", kafkaNS)
			runHelmFn(gCtx, "repo", "add", "jetstack", "https://charts.jetstack.io")
			runHelmFn(gCtx, "repo", "update", "jetstack")
			err := runHelmFn(gCtx, "upgrade", "--install", "cert-manager", "jetstack/cert-manager", "--version", "v1.13.3", "-n", kafkaNS, "--create-namespace", "--set", "crds.enabled=true", "--set", "startupapicheck.enabled=false", "--timeout", "10m", "--wait")
			if err != nil { return err }
			
			clusterIssuer := `apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: selfsigned-issuer
spec:
  selfSigned: {}`
			return runExecStdinFn(gCtx, "kubectl", []string{"apply", "-f", "-"}, clusterIssuer)
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
			return runHelmFn(gCtx, "upgrade", "--install", "kyverno", "kyverno/kyverno", "-n", "kyverno", "--create-namespace", "--set", "replicaCount=1", "--timeout", "5m", "--wait")
		})
	}

	if err := g.Wait(); err != nil {
		output.Error(fmt.Sprintf("Failed during Group A (Operators) deployments: %v", err))
		return err
	}

	// ---------------------------------------------------------
	// GROUP B: Core Infrastructure (Parallel)
	// ---------------------------------------------------------
	g2, g2Ctx := errgroup.WithContext(ctx)

	// Deploy Monitoring (Jaeger)
	if deployWithMonitoring {
		g2.Go(func() error {
			if isHelmReleaseDeployedFn(g2Ctx, "jaeger", kafkaNS) {
				fmt.Println("⏭️  Jaeger already deployed. Skipping.")
				return nil
			}
			fmt.Printf("\n🚀 Deploying Jaeger (Namespace: %s)...\n", kafkaNS)
			runHelmFn(g2Ctx, "repo", "add", "jaegertracing", "https://jaegertracing.github.io/helm-charts")
			runHelmFn(g2Ctx, "repo", "update", "jaegertracing")
			err := runHelmFn(g2Ctx, "upgrade", "--install", "jaeger", "jaegertracing/jaeger", "--version", "3.0.1", "-n", kafkaNS, "--create-namespace", "-f", "config/monitoring/jaeger-values.yaml", "--timeout", "5m")
			if err != nil { return err }
			
			// Patch health probes natively without exiting on error immediately
			runExecFn(g2Ctx, "kubectl", "patch", "deployment", "jaeger", "-n", kafkaNS, "--type=json", "-p", `[{"op": "replace", "path": "/spec/template/spec/containers/0/livenessProbe", "value": {"httpGet": {"path": "/", "port": 16686}, "initialDelaySeconds": 10, "periodSeconds": 15, "failureThreshold": 5}},{"op": "replace", "path": "/spec/template/spec/containers/0/readinessProbe", "value": {"httpGet": {"path": "/", "port": 16686}, "initialDelaySeconds": 5, "periodSeconds": 10, "failureThreshold": 3}}]`)
			return nil
		})
	}
	
	// Deploy Kafka
	g2.Go(func() error {
		if !isHelmReleaseDeployedFn(g2Ctx, "krafter", kafkaNS) {
			fmt.Printf("\n🚀 Deploying Kafka Cluster (Namespace: %s)...\n", kafkaNS)
			runHelmFn(g2Ctx, "dependency", "update", "charts/kafka-cluster")
			err := runHelmFn(g2Ctx, "upgrade", "--install", "krafter", "charts/kafka-cluster", "-n", kafkaNS, "--create-namespace", "-f", valuesFile, "--timeout", "10m", "--wait")
			if err != nil { return err }
		} else {
			fmt.Println("⏭️  Kafka chart already deployed. Verifying readiness...")
		}
		
		fmt.Println("    - Waiting for Kafka cluster to be ready...")
		if err := runExecFn(g2Ctx, "kubectl", "wait", "kafka/krafter", "--for=condition=Ready", "--timeout=600s", "-n", kafkaNS); err != nil {
			return fmt.Errorf("kafka readiness failed: %w", err)
		}
		
		fmt.Println("    - Waiting for Entity Operator...")
		if err := runExecFn(g2Ctx, "kubectl", "wait", "deployment", "-l", "app.kubernetes.io/name=entity-operator", "--for=condition=Available", "--timeout=180s", "-n", kafkaNS); err != nil {
			return fmt.Errorf("entity operator readiness failed: %w", err)
		}
		
		fmt.Println("    - Applying Kafka users and topics...")
		runExecFn(g2Ctx, "kubectl", "apply", "-f", "config/kafka/kafka-users.yaml", "-n", kafkaNS)
		runExecFn(g2Ctx, "kubectl", "apply", "-f", "config/kafka/kafka-topics.yaml", "-n", kafkaNS)
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
		if err := runHelmFn(ctx, "upgrade", "--install", "kates", "charts/kates", "-n", appNS, "--create-namespace", "-f", valuesFile, "--timeout", "5m", "--wait"); err != nil {
			return err
		}
	} else {
		fmt.Println("⏭️  Kates Backend already deployed.")
	}
	
	// Deploy Chaos
	if deployWithChaos {
		if !isHelmReleaseDeployedFn(ctx, "chaos", chaosNS) {
			fmt.Printf("\n🚀 Deploying Litmus Chaos (Namespace: %s)...\n", chaosNS)
			runHelmFn(ctx, "dependency", "update", "charts/kates-chaos")
			if err := runHelmFn(ctx, "upgrade", "--install", "chaos", "charts/kates-chaos", "-n", chaosNS, "--create-namespace", "-f", valuesFile, "--timeout", "5m", "--wait"); err != nil {
				return err
			}
		} else {
			fmt.Println("⏭️  Litmus Chaos already deployed.")
		}
	}

	fmt.Println("\n✅ Deployment Complete! ⎈ Happy Helming! ⎈")
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
	
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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
	
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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
