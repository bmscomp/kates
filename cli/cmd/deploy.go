package cmd

import (
	"fmt"

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

	// 3. Cluster Detection (Placeholder for hooking into detect package)
	fmt.Println("\n[3] Running Cluster Introspection (Pre-flight)...")
	fmt.Println("    - Detecting DNS Domain...")
	fmt.Println("    - Detecting Default StorageClass...")
	fmt.Println("    - Detecting Availability Zones...")
	fmt.Println("    - Generating values-detected.yaml...")

	// 4. Execution Plan
	fmt.Println("\n[4] Execution Plan Generated. (Dry-run only for now)")
	fmt.Println("    - Next step: Implement Helm client logic in Go to apply these charts.")

	return nil
}
