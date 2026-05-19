package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"

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
	executor := detect.NewOSExecutor()
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
	
	// Deploy Cert-Manager
	if deployWithCertManager {
		fmt.Printf("\n🚀 Deploying Cert-Manager (Namespace: %s)...\n", kafkaNS)
		runHelm("repo", "add", "jetstack", "https://charts.jetstack.io")
		runHelm("repo", "update", "jetstack")
		runHelm("upgrade", "--install", "cert-manager", "jetstack/cert-manager", "--version", "v1.13.3", "-n", kafkaNS, "--create-namespace", "--set", "crds.enabled=true", "--timeout", "5m", "--wait")
		
		fmt.Println("    - Creating self-signed ClusterIssuer...")
		clusterIssuer := `apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: selfsigned-issuer
spec:
  selfSigned: {}`
		runExecStdin("kubectl", []string{"apply", "-f", "-"}, clusterIssuer)
	}
	
	// Deploy Kyverno
	if deployWithKyverno {
		fmt.Println("\n🚀 Deploying Kyverno (Namespace: kyverno)...")
		runHelm("repo", "add", "kyverno", "https://kyverno.github.io/kyverno/")
		runHelm("repo", "update", "kyverno")
		runHelm("upgrade", "--install", "kyverno", "kyverno/kyverno", "-n", "kyverno", "--create-namespace", "--set", "replicaCount=1", "--timeout", "5m", "--wait")
	}
	
	// Deploy Kafka
	fmt.Printf("\n🚀 Deploying Kafka Cluster (Namespace: %s)...\n", kafkaNS)
	runHelm("dependency", "update", "charts/kafka-cluster")
	runHelm("upgrade", "--install", "krafter", "charts/kafka-cluster", "-n", kafkaNS, "--create-namespace", "-f", valuesFile, "--timeout", "10m", "--wait")
	
	// Wait for Kafka to be Ready
	fmt.Println("    - Waiting for Kafka cluster to be ready (this may take a few minutes)...")
	runExec("kubectl", "wait", "kafka/krafter", "--for=condition=Ready", "--timeout=600s", "-n", kafkaNS)
	
	// Wait for Entity Operator
	fmt.Println("    - Waiting for Entity Operator...")
	runExec("kubectl", "wait", "deployment", "-l", "app.kubernetes.io/name=entity-operator", "--for=condition=Available", "--timeout=180s", "-n", kafkaNS)
	
	// Apply Users and Topics
	fmt.Println("    - Applying Kafka users and topics...")
	runExec("kubectl", "apply", "-f", "config/kafka/kafka-users.yaml", "-n", kafkaNS)
	runExec("kubectl", "apply", "-f", "config/kafka/kafka-topics.yaml", "-n", kafkaNS)
	
	// Deploy Schema Registry (if requested)
	if deployWithSchemaRegistry == "apicurio" {
		fmt.Printf("\n🚀 Deploying Apicurio Schema Registry (Namespace: %s)...\n", kafkaNS)
		runHelm("upgrade", "--install", "apicurio", "charts/apicurio-registry", "-n", kafkaNS, "--create-namespace", "--timeout", "5m")
	}
	
	// Deploy Monitoring (Jaeger)
	if deployWithMonitoring {
		fmt.Printf("\n🚀 Deploying Jaeger (Namespace: %s)...\n", kafkaNS)
		runHelm("repo", "add", "jaegertracing", "https://jaegertracing.github.io/helm-charts")
		runHelm("repo", "update", "jaegertracing")
		runHelm("upgrade", "--install", "jaeger", "jaegertracing/jaeger", "--version", "3.0.1", "-n", kafkaNS, "--create-namespace", "-f", "config/monitoring/jaeger-values.yaml", "--timeout", "5m")
		
		fmt.Println("    - Patching health probes for Jaeger v2...")
		runExec("kubectl", "patch", "deployment", "jaeger", "-n", kafkaNS, "--type=json", "-p", `[{"op": "replace", "path": "/spec/template/spec/containers/0/livenessProbe", "value": {"httpGet": {"path": "/", "port": 16686}, "initialDelaySeconds": 10, "periodSeconds": 15, "failureThreshold": 5}},{"op": "replace", "path": "/spec/template/spec/containers/0/readinessProbe", "value": {"httpGet": {"path": "/", "port": 16686}, "initialDelaySeconds": 5, "periodSeconds": 10, "failureThreshold": 3}}]`)
	}
	
	// Deploy Kates
	fmt.Printf("\n🚀 Deploying Kates Backend (Namespace: %s)...\n", appNS)
	runHelm("upgrade", "--install", "kates", "charts/kates", "-n", appNS, "--create-namespace", "-f", valuesFile, "--timeout", "5m", "--wait")
	
	// Deploy Chaos
	if deployWithChaos {
		fmt.Printf("\n🚀 Deploying Litmus Chaos (Namespace: %s)...\n", chaosNS)
		runHelm("dependency", "update", "charts/kates-chaos")
		runHelm("upgrade", "--install", "chaos", "charts/kates-chaos", "-n", chaosNS, "--create-namespace", "-f", valuesFile, "--timeout", "5m", "--wait")
	}

	fmt.Println("\n✅ Deployment Complete! ⎈ Happy Helming! ⎈")
	return nil
}

func runExec(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		output.Error(fmt.Sprintf("Command failed: %s %v", name, args))
		os.Exit(1)
	}
}

func runExecStdin(name string, args []string, stdinData string) {
	cmd := exec.Command(name, args...)
	
	stdin, err := cmd.StdinPipe()
	if err != nil {
		output.Error(fmt.Sprintf("Failed to get stdin pipe: %v", err))
		os.Exit(1)
	}
	
	go func() {
		defer stdin.Close()
		stdin.Write([]byte(stdinData))
	}()
	
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		output.Error(fmt.Sprintf("Command failed: %s %v", name, args))
		os.Exit(1)
	}
}

func runHelm(args ...string) {
	runExec("helm", args...)
}
