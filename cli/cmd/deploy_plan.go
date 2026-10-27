package cmd

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bmscomp/kates/cli/pkg/components"
	"github.com/bmscomp/kates/cli/pkg/detect"
	"github.com/charmbracelet/huh"
)

// runInteractiveForms runs the interactive huh forms (Form 1: component selection,
// Form 2: namespace configuration) and updates the global deploy* flag variables.
func runInteractiveForms() error {
	var picked []string
	if err := runComponentForm(&picked); err != nil {
		return err
	}

	// ── Form 2: Namespace configuration ──────────────────────────────────
	// Built after Form 1 completes so that `picked` is fully populated.
	// This avoids a huh library issue where MultiSelect values from a
	// previous group aren't reliably available in WithHideFunc closures
	// of subsequent groups within the same form.
	if err := runNamespaceForm(picked); err != nil {
		return err
	}

	applyPickedComponents(picked)

	// ── Form: Versions and scope ─────────────────────────────────────────
	// The Kafka options are a function of the operator chosen above them:
	// pick 1.0.1 and the list is its window, pick 1.1.0 and it is 1.1.0's.
	// Nothing here is a table in Go — the options come from the charts.
	// After the namespaces: the installed operator it offers to keep is the
	// one watching the Kafka namespace chosen there.
	if err := runVersionPickerForm(); err != nil {
		return err
	}

	// ── Form: Already installed ──────────────────────────────────────────
	if err := runInstalledReleasesForm(); err != nil {
		return err
	}

	// ── Review ───────────────────────────────────────────────────────────
	// Everything chosen, in one screen, before a single Helm call: the
	// versions as they will resolve, the components, the namespaces.
	return runDeployReviewForm()
}

// runComponentForm is Form 1: the topology, the schema registry, HA and
// the components, which it stores in picked.
func runComponentForm(picked *[]string) error {
	// What is already installed, beside each component it belongs to.
	hintCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	hints := installedHints(hintCtx)
	cancel()
	option := func(label, value string, on bool) huh.Option[string] {
		if hint := hints[value]; hint != "" {
			label += "  · " + hint
		}
		return huh.NewOption(label, value).Selected(on)
	}

	var leftOut []leftOutComponent
	isolated := "Isolated Namespaces (kafka, kates, monitoring, litmus)"
	componentOptions := []huh.Option[string]{
		option("🦊 Strimzi Operator", "strimzi", deployWithStrimzi),
		option("🔗 Kafka Connect + PostgreSQL (CDC)", "kafka-connect", deployWithKafkaConnect),
		option("💻 Kafka UI (Dashboard)", "kafka-ui", deployWithKafkaUI),
		option("🔁 MirrorMaker 2 (loopback mirror of the primary)", "mirror-maker2", deployWithMirrorMaker2),
		option("🧪 Litmus Chaos Engine", "chaos", deployWithChaos),
		option("📊 Monitoring (Grafana + Prometheus)", "monitoring", deployWithMonitoring),
		option("🔐 Cert-Manager (TLS)", "cert-manager", deployWithCertManager),
		option("🚦 Kyverno (Policies)", "kyverno", deployWithKyverno),
	}
	if deployAdminless {
		// Only what installs into a namespace: no operators, no chaos
		// engineering, and monitoring only as the scrape of a Prometheus
		// already on the cluster.
		isolated = "Isolated Namespaces (existing ones: kafka, connect, db, kates)"
		componentOptions = []huh.Option[string]{componentOptions[1], componentOptions[2], componentOptions[3]}
		prometheus := prometheusOperatorAPI(context.Background())
		if prometheus {
			componentOptions = append(componentOptions,
				huh.NewOption("📊 Monitoring (PodMonitors and rules for the cluster's Prometheus)", "monitoring").Selected(deployAdminlessScrape))
		}
		leftOut = adminlessLeftOut(prometheus, false)
	}
	componentFields := []huh.Field{
		huh.NewMultiSelect[string]().
			Title("Select Components").
			Description("Space toggles, enter confirms. Kafka and the Kates backend are always deployed. After · is what is installed now.").
			Options(componentOptions...).
			Value(picked),
	}
	if len(leftOut) > 0 {
		componentFields = append(componentFields, huh.NewNote().Title("Not installed admin-less").Description(describeLeftOut(leftOut)))
	}

	return huh.NewForm(
		// ── Group 1: What to deploy ──────────────────────────────────────────
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Choose Namespace Topology").
				Description("Isolated creates logical boundaries. Single is great for simple local dev.").
				Options(
					huh.NewOption(isolated, "isolated"),
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
		huh.NewGroup(componentFields...),
	).WithTheme(ThemeKates()).WithAccessible(accessibleUI()).Run()
}

// runNamespaceForm is Form 2: the namespaces for the topology and the
// components picked. Single and admin-less asks for the one namespace;
// single otherwise asks nothing.
func runNamespaceForm(picked []string) error {
	if deployTopology == "single" && deployAdminless {
		form2 := huh.NewForm(huh.NewGroup(
			huh.NewInput().
				Title("Namespace").
				Description("An existing namespace this account may write to; everything goes there").
				Value(&deployNamespace),
		)).WithTheme(ThemeKates()).WithAccessible(accessibleUI())
		return form2.Run()
	}
	if deployTopology == "single" {
		return nil
	}
	// Two pages at most: the namespaces every deploy needs, then the
	// chosen components'. A huh group scrolls to the focused field, so
	// the second page fits any terminal however many components are on.
	nsGroups := []*huh.Group{huh.NewGroup(
		huh.NewInput().
			Title("Kafka Namespace").
			Description("Namespace for Strimzi operator and Kafka cluster").
			Value(&deployKafkaNS),
		huh.NewInput().
			Title("Kates App Namespace").
			Description("Namespace for the Kates backend service").
			Value(&deployAppNS),
	)}
	if optional := componentNamespaceFields(picked); len(optional) > 0 {
		nsGroups = append(nsGroups, huh.NewGroup(optional...))
	}

	form2 := huh.NewForm(nsGroups...).WithTheme(ThemeKates()).WithAccessible(accessibleUI())
	return form2.Run()
}

// componentNamespaceFields is a namespace input for each picked component
// that has a namespace of its own.
func componentNamespaceFields(picked []string) []huh.Field {
	var optional []huh.Field
	if slices.Contains(picked, "monitoring") && !deployAdminless {
		optional = append(optional, huh.NewInput().
			Title("Monitoring Namespace").
			Description("Namespace for the Prometheus and Grafana stack").
			Value(&deployMonitoringNS))
	}
	if slices.Contains(picked, "chaos") {
		optional = append(optional, huh.NewInput().
			Title("Chaos Namespace").
			Description("Namespace for Litmus Chaos engine").
			Value(&deployChaosNS))
	}
	if slices.Contains(picked, "kafka-connect") {
		optional = append(optional,
			huh.NewInput().
				Title("Kafka Connect Namespace").
				Description("Namespace for Kafka Connect cluster (separate from Kafka)").
				Value(&deployConnectNS),
			huh.NewInput().
				Title("Database Namespace").
				Description("Namespace for PostgreSQL CDC database").
				Value(&deployDbNS))
	}
	if slices.Contains(picked, "mirror-maker2") {
		optional = append(optional, huh.NewInput().
			Title("MirrorMaker 2 Namespace").
			Description("Namespace for the loopback mirror (the Kafka namespace is the simplest: the kates-mm2 credential lives there)").
			Value(&deployMM2NS))
	}
	if slices.Contains(picked, "kafka-ui") {
		optional = append(optional, huh.NewInput().
			Title("Kafka UI Namespace").
			Description("Namespace for the Kafka UI dashboard").
			Value(&deployKafkaUINS))
	}
	return optional
}

// applyPickedComponents sets the deployWith* flags from the components
// picked in Form 1: exactly those are on. Monitoring admin-less is the scrape
// of the cluster's Prometheus rather than the stack.
func applyPickedComponents(picked []string) {
	deployWithKafkaConnect = false
	deployWithChaos = false
	deployWithMonitoring = false
	deployWithCertManager = false
	deployWithKyverno = false
	deployWithStrimzi = false
	deployWithKafkaUI = false
	deployWithMirrorMaker2 = false
	deployAdminlessScrape = false

	for _, c := range picked {
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
			if deployAdminless {
				deployAdminlessScrape = true
			} else {
				deployWithMonitoring = true
			}
		case "cert-manager":
			deployWithCertManager = true
		case "kyverno":
			deployWithKyverno = true
		}
	}
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
		if deployWithChaos {
			PrintPhaseItem(fmt.Sprintf("%-14s → %s", "Chaos", deployChaosNS))
		}
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
		PrintPhaseSuccess("Monitoring (Prometheus + Grafana)")
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
	if deployAdminlessScrape {
		PrintPhaseSuccess("Monitoring (PodMonitors and rules for the cluster's Prometheus)")
	}
	if deployAdminless {
		for _, c := range adminlessLeftOut(deployAdminlessScrape || prometheusOperatorAPI(context.Background()), deployAdminlessScrape) {
			PrintPhaseItem("Not installed: " + c.name + " — " + c.why)
		}
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

// deployPlacement is where this deploy puts each component: the resolved
// namespaces, the database on --db-ns under both topologies (an existing
// database is never moved), and the primary's name.
func deployPlacement(ns resolvedNamespaces) components.Placement {
	return components.Placement{
		Kafka: ns.kafka, Connect: ns.connect, Database: deployDbNS, App: ns.app,
		Chaos: ns.chaos, Monitoring: ns.jaeger, KafkaUI: ns.kafkaUI, MM2: ns.mm2,
		KafkaName: deployKafkaName,
	}
}

// componentSelected says whether the flags (or the wizard) turned a component
// of the table on for this deploy. Kafka, its users and Kates are always in.
func componentSelected(id string) bool {
	switch id {
	case "strimzi":
		return deployWithStrimzi
	case "cert-manager":
		return deployWithCertManager
	case "kyverno":
		return deployWithKyverno
	case "monitoring":
		return deployWithMonitoring
	case "postgres", "kafka-connect", "kafka-connector":
		return deployWithKafkaConnect
	case "apicurio":
		return deployWithSchemaRegistry == "apicurio"
	case "kafka-ui":
		return deployWithKafkaUI
	case "mirror-maker2":
		return deployWithMirrorMaker2
	case "chaos":
		return deployWithChaos
	case "kafka", "kafka-users", "kates":
		return true
	}
	return false
}

// selectedComponents is the component table filtered to this deploy, in
// install order.
func selectedComponents() []components.Component {
	var selected []components.Component
	for _, c := range components.All() {
		if componentSelected(c.ID) {
			selected = append(selected, c)
		}
	}
	return selected
}

// entryName is the deploy views' name for a component: shorter than the
// dashboard's, and the Kafka row carries the cluster's name.
func entryName(c components.Component) string {
	switch c.ID {
	case "kafka":
		return "Kafka (" + deployKafkaName + ")"
	case "monitoring":
		return "Monitoring"
	case "apicurio":
		return "Apicurio"
	}
	return c.Name
}

// buildSharedEntries builds the shared component registry (single source of truth)
// used by both the deploy preview and the final summary dashboard: one row per
// release the component table says this deploy installs.
func buildSharedEntries(ns resolvedNamespaces) []DeploySummaryEntry {
	placement := deployPlacement(ns)
	var entries []DeploySummaryEntry
	for _, c := range selectedComponents() {
		if !c.Installs() {
			continue
		}
		entries = append(entries, DeploySummaryEntry{
			Icon: c.Icon, Name: entryName(c), Release: c.ReleaseName(placement),
			Namespace: c.Namespace(placement), Group: c.Group,
		})
	}
	return entries
}

// countDeploySteps is the number of dashboard steps: every selected
// component, the release-less ones (Kafka users, the CDC connectors) included.
func countDeploySteps() int {
	return len(selectedComponents())
}

// dashboardSelector is the pod selector the dashboard watches for a
// component: the table's, with the Kafka cluster's name filled in.
func dashboardSelector(c components.Component) string {
	if strings.Contains(c.Selector, "%s") {
		return fmt.Sprintf(c.Selector, deployKafkaName)
	}
	return c.Selector
}

// registerDashboardComponents registers all selected components with the
// deployment dashboard for progress tracking.
func registerDashboardComponents(dashboard *deployDashboardModel, ns resolvedNamespaces) {
	placement := deployPlacement(ns)
	for _, c := range selectedComponents() {
		dashboard.RegisterComponent(c.ID, c.Name, c.Group, Target{c.Namespace(placement), dashboardSelector(c)})
	}
}

// runDeployDryRun shows the deployment preview and exits without deploying.
func runDeployDryRun(sharedEntries []DeploySummaryEntry, labels, labelSources map[string]string) error {
	dryCtx, dryCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer dryCancel()
	existingReleases := make(map[string]bool)
	for _, e := range sharedEntries {
		key := e.Release + "/" + e.Namespace
		if releaseKept(dryCtx, e.Release, e.Namespace) {
			existingReleases[key] = true
		}
	}
	installed := installedSummary(installedRows(dryCtx, sharedEntries, deployedReleases(dryCtx, sharedEntries)))
	renderDeployPreview(sharedEntries, existingReleases, installed, labels, labelSources)
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
	// installedNow holds the components this run installed, as opposed to
	// found there, by id (deploy_scheduler.go). wireKyvernoScrape reads it:
	// a Kyverno that was already there is left alone, like every other
	// existing release.
	installedMu  sync.Mutex
	installedNow map[string]bool
}
