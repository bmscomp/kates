package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/bmscomp/kates/cli/pkg/detect"
	"github.com/charmbracelet/huh"
)

// runInteractiveForms runs the interactive huh forms (Form 1: component selection,
// Form 2: namespace configuration) and updates the global deploy* flag variables.
func runInteractiveForms() error {
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
		),

		// ── Group 2: Components ──────────────────────────────────────────────
		// On its own page so the whole list fits a 24-line terminal: a huh
		// group scrolls per focused field, not per option, so a long
		// multi-select squeezed under three other fields could hide its
		// last entries until the cursor reached them.
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Select Components").
				Description("Space toggles, enter confirms. Kafka and the Kates backend are always deployed.").
				Options(
					huh.NewOption("🦊 Strimzi Operator", "strimzi").Selected(deployWithStrimzi),
					huh.NewOption("🔗 Kafka Connect + PostgreSQL (CDC)", "kafka-connect").Selected(deployWithKafkaConnect),
					huh.NewOption("💻 Kafka UI (Dashboard)", "kafka-ui").Selected(deployWithKafkaUI),
					huh.NewOption("🔁 MirrorMaker 2 (loopback mirror of the primary)", "mirror-maker2").Selected(deployWithMirrorMaker2),
					huh.NewOption("🧪 Litmus Chaos Engine", "chaos").Selected(deployWithChaos),
					huh.NewOption("📊 Monitoring (Grafana + Prometheus)", "monitoring").Selected(deployWithMonitoring),
					huh.NewOption("🔐 Cert-Manager (TLS)", "cert-manager").Selected(deployWithCertManager),
					huh.NewOption("🚦 Kyverno (Policies)", "kyverno").Selected(deployWithKyverno),
				).
				Value(&components),
		),
	).WithTheme(ThemeKates())

	if err := form1.Run(); err != nil {
		return err
	}

	// ── Form: Versions and scope ─────────────────────────────────────────
	// The Kafka options are a function of the operator chosen above them:
	// pick 1.0.1 and the list is its window, pick 1.1.0 and it is 1.1.0's.
	// Nothing here is a table in Go — the options come from the charts.
	if err := runVersionPickerForm(); err != nil {
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

		if sliceContains(components, "mirror-maker2") {
			nsGroups = append(nsGroups, huh.NewGroup(
				huh.NewInput().
					Title("MirrorMaker 2 Namespace").
					Description("Namespace for the loopback mirror (the Kafka namespace is the simplest: the kates-mm2 credential lives there)").
					Value(&deployMM2NS),
			))
		}

		if sliceContains(components, "kafka-ui") {
			nsGroups = append(nsGroups, huh.NewGroup(
				huh.NewInput().
					Title("Kafka UI Namespace").
					Description("Namespace for the Kafka UI dashboard").
					Value(&deployKafkaUINS),
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
	deployWithKafkaUI = false
	deployWithMirrorMaker2 = false

	for _, c := range components {
		switch c {
		case "strimzi":
			deployWithStrimzi = true
		case "kafka-connect":
			deployWithKafkaConnect = true
		case "kafka-ui":
			deployWithKafkaUI = true
		case "mirror-maker2":
			deployWithMirrorMaker2 = true
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

	// ── Review ───────────────────────────────────────────────────────────
	// Everything chosen, in one screen, before a single Helm call: the
	// versions as they will resolve, the components, the namespaces.
	return runDeployReviewForm()
}

// printTopologyResolution prints the resolved namespace topology (Phase 1).
func printTopologyResolution() {
	PrintPhaseHeader(nextDeployPhase(), fmt.Sprintf("Resolving Namespace Topology (%s mode)", deployTopology))
	if deployTopology == "single" {
		PrintPhaseItem(fmt.Sprintf("All components → %s", deployNamespace))
	} else {
		PrintPhaseItem(fmt.Sprintf("%-14s → %s", "Kafka", deployKafkaNS))
		if deployWithKafkaConnect {
			PrintPhaseItem(fmt.Sprintf("%-14s → %s", "Kafka Connect", deployConnectNS))
			PrintPhaseItem(fmt.Sprintf("%-14s → %s", "Database", deployDbNS))
		}
		PrintPhaseItem(fmt.Sprintf("%-14s → %s", "Kates App", deployAppNS))
		if deployWithKafkaUI {
			PrintPhaseItem(fmt.Sprintf("%-14s → %s", "Kafka UI", deployKafkaUINS))
		}
		if deployWithMonitoring {
			PrintPhaseItem(fmt.Sprintf("%-14s → %s", "Monitoring", deployMonitoringNS))
		}
		PrintPhaseItem(fmt.Sprintf("%-14s → %s", "Chaos", deployChaosNS))
	}
}

// printComponentSelection prints the selected components (Phase 2).
func printComponentSelection() {
	PrintPhaseHeader(nextDeployPhase(), "Component Selection")
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
	if deployWithKafkaUI {
		PrintPhaseSuccess("Kafka UI (Dashboard)")
	}
	if deployWithMirrorMaker2 {
		PrintPhaseSuccess("MirrorMaker 2 (loopback mirror)")
	}
}

// resolvedNamespaces holds the resolved namespace values for each component group.
type resolvedNamespaces struct {
	kafka   string
	connect string
	app     string
	chaos   string
	jaeger  string
	kafkaUI string
	mm2     string
}

// resolveNamespaces resolves namespace values based on the selected topology.
func resolveNamespaces() resolvedNamespaces {
	if deployTopology == "single" {
		return resolvedNamespaces{
			kafka:   deployNamespace,
			connect: deployNamespace,
			app:     deployNamespace,
			chaos:   deployNamespace,
			jaeger:  deployNamespace,
			kafkaUI: deployNamespace,
			mm2:     deployNamespace,
		}
	}
	return resolvedNamespaces{
		kafka:   deployKafkaNS,
		connect: deployConnectNS,
		app:     deployAppNS,
		chaos:   deployChaosNS,
		jaeger:  deployMonitoringNS,
		kafkaUI: deployKafkaUINS,
		mm2:     deployMM2NS,
	}
}

// buildSharedEntries builds the shared component registry (single source of truth)
// used by both the deploy preview and the final summary dashboard.
func buildSharedEntries(ns resolvedNamespaces) []DeploySummaryEntry {
	var entries []DeploySummaryEntry
	if deployWithStrimzi {
		entries = append(entries, DeploySummaryEntry{Icon: "🦊", Name: "Strimzi Operator", Release: "strimzi-operator", Namespace: "strimzi-operator", Group: "A"})
	}
	if deployWithCertManager {
		entries = append(entries, DeploySummaryEntry{Icon: "🔐", Name: "Cert-Manager", Release: "cert-manager", Namespace: "cert-manager", Group: "A"})
	}
	if deployWithKyverno {
		entries = append(entries, DeploySummaryEntry{Icon: "🚦", Name: "Kyverno", Release: "kyverno", Namespace: "kyverno", Group: "A"})
	}
	entries = append(entries, DeploySummaryEntry{Icon: "📨", Name: "Kafka (" + deployKafkaName + ")", Release: deployKafkaName, Namespace: ns.kafka, Group: "B"})
	if deployWithKafkaConnect {
		entries = append(entries, DeploySummaryEntry{Icon: "🐘", Name: "PostgreSQL", Release: "postgresql", Namespace: deployDbNS, Group: "B"})
		entries = append(entries, DeploySummaryEntry{Icon: "🔗", Name: "Kafka Connect", Release: "connect-cluster", Namespace: ns.connect, Group: "B"})
	}
	if deployWithMonitoring {
		entries = append(entries, DeploySummaryEntry{Icon: "📊", Name: "Monitoring", Release: "monitoring", Namespace: ns.jaeger, Group: "B"})
	}
	if deployWithSchemaRegistry == "apicurio" {
		entries = append(entries, DeploySummaryEntry{Icon: "📋", Name: "Apicurio", Release: "apicurio", Namespace: ns.kafka, Group: "C"})
	}
	entries = append(entries, DeploySummaryEntry{Icon: "📦", Name: "Kates Backend", Release: "kates", Namespace: ns.app, Group: "C"})
	if deployWithKafkaUI {
		entries = append(entries, DeploySummaryEntry{Icon: "💻", Name: "Kafka UI", Release: "kafka-ui", Namespace: ns.kafkaUI, Group: "C"})
	}
	if deployWithMirrorMaker2 {
		entries = append(entries, DeploySummaryEntry{Icon: "🔁", Name: "MirrorMaker 2", Release: "mm2", Namespace: ns.mm2, Group: "C"})
	}
	if deployWithChaos {
		entries = append(entries, DeploySummaryEntry{Icon: "🧪", Name: "Litmus Chaos", Release: "chaos", Namespace: ns.chaos, Group: "C"})
	}
	return entries
}

// countDeploySteps calculates the total number of deployment steps based on
// the selected components.
func countDeploySteps() int {
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
	if deployWithKafkaUI {
		totalSteps++
	}
	if deployWithMirrorMaker2 {
		totalSteps++
	}
	if deployWithChaos {
		totalSteps++
	}
	return totalSteps
}

// registerDashboardComponents registers all selected components with the
// deployment dashboard for progress tracking.
func registerDashboardComponents(dashboard *deployDashboardModel, ns resolvedNamespaces) {
	if deployWithStrimzi {
		dashboard.RegisterComponent("strimzi", "Strimzi Operator", "A", Target{"strimzi-operator", "name=strimzi-cluster-operator"})
	}
	if deployWithCertManager {
		dashboard.RegisterComponent("cert-manager", "Cert-Manager", "A", Target{"cert-manager", "app.kubernetes.io/instance=cert-manager"})
	}
	if deployWithKyverno {
		dashboard.RegisterComponent("kyverno", "Kyverno", "A", Target{"kyverno", "app.kubernetes.io/instance=kyverno"})
	}
	dashboard.RegisterComponent("kafka", "Kafka Cluster", "B", Target{ns.kafka, "strimzi.io/cluster=" + deployKafkaName})
	dashboard.RegisterComponent("kafka-users", "Kafka Users", "B", Target{ns.kafka, "app.kubernetes.io/name=entity-operator"})
	if deployWithMonitoring {
		dashboard.RegisterComponent("monitoring", "Monitoring Stack", "B", Target{ns.jaeger, "release=monitoring"})
	}
	if deployWithKafkaConnect {
		dashboard.RegisterComponent("postgres", "PostgreSQL", "B",
			Target{deployDbNS, "app.kubernetes.io/instance=postgresql"})
		dashboard.RegisterComponent("kafka-connect", "Kafka Connect", "B",
			Target{ns.connect, "strimzi.io/kind=KafkaConnect"})
		dashboard.RegisterComponent("kafka-connector", "CDC Connector", "B",
			Target{ns.connect, "strimzi.io/cluster=connect-cluster"})
	}
	if deployWithSchemaRegistry == "apicurio" {
		dashboard.RegisterComponent("apicurio", "Apicurio Registry", "C", Target{ns.kafka, "app.kubernetes.io/instance=apicurio"})
	}
	dashboard.RegisterComponent("kates", "Kates Backend", "C", Target{ns.app, "app.kubernetes.io/instance=kates"})
	if deployWithKafkaUI {
		dashboard.RegisterComponent("kafka-ui", "Kafka UI", "C", Target{ns.kafkaUI, "app.kubernetes.io/name=kafka-ui"})
	}
	if deployWithMirrorMaker2 {
		dashboard.RegisterComponent("mirror-maker2", "MirrorMaker 2", "C", Target{ns.mm2, "strimzi.io/kind=KafkaMirrorMaker2"})
	}
	if deployWithChaos {
		dashboard.RegisterComponent("chaos", "Litmus Chaos", "C", Target{ns.chaos, "app.kubernetes.io/instance=chaos"})
	}
}

// runDeployDryRun shows the deployment preview and exits without deploying.
func runDeployDryRun(sharedEntries []DeploySummaryEntry) error {
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

// deployContext holds the shared state passed between deployment phases.
type deployContext struct {
	ctx          context.Context
	ns           resolvedNamespaces
	valuesFile   string
	isKind       bool
	report       *detect.DetectReport
	chartOverlay func(string) string
	fileExists   func(string) bool
	advanceStep  func()
	// versions is the resolved operator/Kafka plan; primary the cluster it
	// describes. Both replace the "krafter"/pinned-version literals.
	versions *versionPlan
	primary  primaryCluster
	// kyvernoInstalled is set by Group A when it installs Kyverno in this
	// run, so Group B can wire its scrape once the monitoring CRDs exist
	// (deploy_monitoring.go). A Kyverno that was already there is left
	// alone, like every other existing release.
	kyvernoInstalled bool
}
