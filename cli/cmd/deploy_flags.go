package cmd

import "github.com/spf13/cobra"

// The topology and namespace flags place the stack. deploy, deploy status,
// clean and kyverno apply all take them, and each used to declare its own
// copy: the sets drifted (clean had no --ui-ns, so a Kafka UI deployed with
// it was never uninstalled) and deploy status wrote into deploy's globals.
// One registration keeps the names and defaults identical; a test asserts
// the four commands expose the same set.

// deployScopeVars are the variables the scope flags bind. deploy, deploy
// status and kyverno apply share deploy's package globals; clean has its own.
type deployScopeVars struct {
	Topology, Namespace, KafkaNS, ConnectNS, DbNS, AppNS, ChaosNS, MonitoringNS, KafkaUINS, MM2NS *string
}

// deployGlobalScope binds the flags to deploy's package-level variables.
func deployGlobalScope() deployScopeVars {
	return deployScopeVars{
		Topology: &deployTopology, Namespace: &deployNamespace,
		KafkaNS: &deployKafkaNS, ConnectNS: &deployConnectNS, DbNS: &deployDbNS,
		AppNS: &deployAppNS, ChaosNS: &deployChaosNS, MonitoringNS: &deployMonitoringNS,
		KafkaUINS: &deployKafkaUINS, MM2NS: &deployMM2NS,
	}
}

type deployScopeConfig struct {
	vars          deployScopeVars
	topologyValue string
	topologyUsage string
}

type deployScopeOption func(*deployScopeConfig)

// scopeInto binds the flags to vars instead of deploy's globals.
func scopeInto(vars deployScopeVars) deployScopeOption {
	return func(c *deployScopeConfig) { c.vars = vars }
}

// scopeTopologyDefault sets --topology's default and usage: clean's empty
// default means "both topologies".
func scopeTopologyDefault(value, usage string) deployScopeOption {
	return func(c *deployScopeConfig) { c.topologyValue, c.topologyUsage = value, usage }
}

// deployScopeFlagNames is the set addDeployScopeFlags registers, for the test
// that checks every command has it.
var deployScopeFlagNames = []string{"topology", "namespace", "kafka-ns", "connect-ns", "db-ns", "app-ns", "chaos-ns", "monitoring-ns", "ui-ns", "mm2-ns", "kafka-name"}

// addDeployScopeFlags registers the topology and namespace flags on cmd.
func addDeployScopeFlags(cmd *cobra.Command, opts ...deployScopeOption) {
	c := deployScopeConfig{
		vars:          deployGlobalScope(),
		topologyValue: "isolated",
		topologyUsage: "Deployment topology: 'isolated' (separate namespaces) or 'single' (one namespace)",
	}
	for _, o := range opts {
		o(&c)
	}
	v := c.vars
	f := cmd.Flags()
	f.StringVar(v.Topology, "topology", c.topologyValue, c.topologyUsage)
	f.StringVar(v.Namespace, "namespace", "kates-stack", "Target namespace when topology is 'single'")
	f.StringVar(v.KafkaNS, "kafka-ns", "kafka", "Namespace for Kafka when topology is 'isolated'")
	f.StringVar(v.ConnectNS, "connect-ns", "connect", "Namespace for Kafka Connect when topology is 'isolated'")
	f.StringVar(v.DbNS, "db-ns", "database", "Namespace for PostgreSQL Database when topology is 'isolated'")
	f.StringVar(v.AppNS, "app-ns", "kates", "Namespace for Kates Backend when topology is 'isolated'")
	f.StringVar(v.ChaosNS, "chaos-ns", "litmus", "Namespace for Chaos Engine when topology is 'isolated'")
	f.StringVar(v.MonitoringNS, "monitoring-ns", "monitoring", "Namespace for monitoring components (Jaeger) when topology is 'isolated'")
	f.StringVar(v.KafkaUINS, "ui-ns", "kafka", "Namespace for Kafka UI when topology is 'isolated'")
	f.StringVar(v.MM2NS, "mm2-ns", "kafka", "Namespace for MirrorMaker 2 when topology is 'isolated'")
	// The Kafka release is named by --kafka-name everywhere; clean already
	// bound deploy's variable, so it stays shared.
	f.StringVar(&deployKafkaName, "kafka-name", "krafter", "Name of the primary Kafka cluster")
}
