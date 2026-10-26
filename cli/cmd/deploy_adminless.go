package cmd

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/bmscomp/kates/cli/pkg/strimzi"
)

// The admin-less profile: what kates deploy installs for an account that is
// not cluster-admin. Kafka, Kafka Connect with its PostgreSQL, MirrorMaker 2,
// Apicurio, Kafka UI and the Kates backend (rbac.scope=namespace) go into
// namespaces the account already has, as --topology and the --*-ns flags place
// them — one per component by default, or one for all — and every step that
// needs the cluster (Strimzi, cert-manager, Kyverno, the monitoring stack,
// Litmus, node introspection, StorageClasses, namespace creation) is left out.
// A cluster admin must already run a Strimzi operator that watches the
// namespaces; its version is the one the Kafka resources follow.

var deployAdminless bool

// deployAdminlessScrape is admin-less monitoring, a wizard choice offered
// only where the monitoring.coreos.com API exists: the charts' PodMonitors,
// ServiceMonitors and rules, for a Prometheus already on the cluster. Its
// selectors decide whether it scrapes these namespaces.
var deployAdminlessScrape bool

// clusterAdminFn says whether the account may do everything everywhere, which
// is what the full profile needs. Tests stub the kubectl read it makes.
var clusterAdminFn = isClusterAdmin

// isClusterAdmin asks the API server. ok is false when the answer is neither
// yes nor no (the cluster did not answer), so the caller keeps the full
// profile and lets its own preflight report the real problem.
func isClusterAdmin(ctx context.Context) (admin, ok bool) {
	// kubectl prints "no" and exits 1 when refused, so the error is not the answer.
	out, _ := runExecOutputFn(ctx, "kubectl", "auth", "can-i", "*", "*", "--all-namespaces")
	switch strings.TrimSpace(string(out)) {
	case "yes":
		return true, true
	case "no":
		return false, true
	}
	return false, false
}

// profileFormFn asks a cluster admin, in the wizard, which profile to install.
var profileFormFn = runProfileForm

func runProfileForm() error {
	return huh.NewForm(huh.NewGroup(
		huh.NewSelect[bool]().
			Title("Install profile").
			Description("Admin-less installs into namespaces that already exist, on a Strimzi operator already watching them.").
			Options(
				huh.NewOption("Full — operators, CRDs, Litmus Chaos, monitoring (cluster admin)", false),
				huh.NewOption("Admin-less — Kafka, Connect, MirrorMaker 2, Apicurio, Kafka UI, Kates; no chaos engineering", true),
			).
			Value(&deployAdminless),
	)).WithTheme(ThemeKates()).WithAccessible(accessibleUI()).Run()
}

// The flags the profile leaves out: each installs something cluster-scoped.
var adminOnlyDeployFlags = []string{"with-strimzi", "with-cert-manager", "with-kyverno", "with-secret-manager", "with-chaos", "with-monitoring"}

// applyAdminlessProfile sets the deploy flags to the admin-less stack. A flag
// the user set to turn on a cluster-scoped component is refused rather than
// dropped; the stack's own components follow the user's flags when set.
func applyAdminlessProfile(ctx context.Context, cmd *cobra.Command) error {
	for _, name := range adminOnlyDeployFlags {
		if f := cmd.Flags().Lookup(name); f != nil && f.Changed && f.Value.String() == "true" {
			return fmt.Errorf("--%s needs cluster admin, so it cannot go with the admin-less profile", name)
		}
	}
	deployAdminless = true
	deployWithStrimzi, deployWithCertManager, deployWithKyverno, deployWithSecretManager = false, false, false, false
	deployWithChaos, deployWithMonitoring = false, false
	for name, v := range map[string]*bool{
		"with-kafka-connect": &deployWithKafkaConnect,
		"with-mirror-maker2": &deployWithMirrorMaker2,
		"with-kafka-ui":      &deployWithKafkaUI,
	} {
		if !cmd.Flags().Changed(name) {
			*v = true
		}
	}

	// Set whatever the topology: the wizard may still switch to single.
	if !cmd.Flags().Changed("namespace") {
		if ns := kubeContextNamespace(ctx); ns != "" {
			deployNamespace = ns
		}
	}
	if deployTopology == "single" {
		// PostgreSQL has its own namespace flag even in single topology.
		if !cmd.Flags().Changed("db-ns") {
			deployDbNS = deployNamespace
		}
	}
	return nil
}

// kubeContextNamespace is the namespace the kube context names — usually the
// one a team account was given.
func kubeContextNamespace(ctx context.Context) string {
	out, _ := runExecOutputFn(ctx, "kubectl", "config", "view", "--minify", "-o", "jsonpath={..namespace}")
	ns := strings.TrimSpace(string(out))
	if ns == "default" {
		return ""
	}
	return ns
}

// adminlessSuggestion is what kates deploy says to an account that is not
// cluster-admin.
func adminlessSuggestion(kubeContext string) string {
	ns := kubeContextNamespace(context.Background())
	if ns == "" {
		ns = "<your-namespace>"
	}
	return fmt.Sprintf(`This account is not cluster-admin on %s, so the full profile (Strimzi, cert-manager,
monitoring, Litmus, several namespaces) cannot be installed.

The admin-less profile installs Kafka, Kafka Connect, MirrorMaker 2, Apicurio, Kafka UI
and the Kates backend into namespaces you already have, one per component:

  kates deploy --adminless --kafka-ns <ns> --connect-ns <ns> --db-ns <ns> --app-ns <ns>

or all in one:

  kates deploy --adminless --topology single --namespace %s

It needs a Strimzi operator, installed by a cluster admin, that watches the Kafka,
Connect and MirrorMaker 2 namespaces.`, kubeContext, ns)
}

// The resources every admin-less component creates in its namespace.
var adminlessCoreGrants = []string{
	"deployments.apps",
	"statefulsets.apps",
	"services",
	"secrets",
	"configmaps",
	"serviceaccounts",
	"networkpolicies.networking.k8s.io",
	"roles.rbac.authorization.k8s.io",
	"rolebindings.rbac.authorization.k8s.io",
}

// adminlessGrants is what the account must be able to create, per namespace:
// the core resources everywhere, and Strimzi's where each one is made.
func adminlessGrants(ns resolvedNamespaces) map[string][]string {
	grants := map[string][]string{}
	add := func(namespace string, resources ...string) {
		grants[namespace] = append(grants[namespace], resources...)
	}
	for _, namespace := range selectedDeployNamespaces(&deployContext{ns: ns}) {
		add(namespace, adminlessCoreGrants...)
	}
	add(ns.kafka, "kafkas.kafka.strimzi.io", "kafkanodepools.kafka.strimzi.io", "kafkatopics.kafka.strimzi.io", "kafkausers.kafka.strimzi.io")
	if deployWithKafkaConnect {
		add(ns.connect, "kafkaconnects.kafka.strimzi.io", "kafkaconnectors.kafka.strimzi.io", "kafkatopics.kafka.strimzi.io")
	}
	if deployWithMirrorMaker2 {
		add(ns.mm2, "kafkamirrormaker2s.kafka.strimzi.io")
	}
	if deployAdminlessScrape {
		for _, namespace := range adminlessStrimziNamespaces(ns) {
			add(namespace, "podmonitors.monitoring.coreos.com", "prometheusrules.monitoring.coreos.com")
		}
		add(ns.app, "servicemonitors.monitoring.coreos.com", "prometheusrules.monitoring.coreos.com")
	}
	for namespace, resources := range grants {
		slices.Sort(resources)
		grants[namespace] = slices.Compact(resources)
	}
	return grants
}

// execOutputRunner runs strimzi's reads through runExecOutputFn, so the deploy
// tests' cluster stubs answer them too.
type execOutputRunner struct{}

func (execOutputRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	out, err := runExecOutputFn(ctx, name, args...)
	return strings.TrimSpace(string(out)), err
}

// adminlessStrimziNamespaces are the namespaces a Strimzi operator must watch:
// where the Kafka, Kafka Connect and MirrorMaker 2 resources are made.
func adminlessStrimziNamespaces(ns resolvedNamespaces) []string {
	names := []string{ns.kafka}
	if deployWithKafkaConnect {
		names = append(names, ns.connect)
	}
	if deployWithMirrorMaker2 {
		names = append(names, ns.mm2)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// strimziInstallSuggestion is what a cluster admin runs to install the
// platform's pinned Strimzi in its own namespace watching exactly the
// admin-less namespaces.
func strimziInstallSuggestion(watch []string) string {
	pin := "the Strimzi version this platform pins"
	if v, err := pinnedStrimziVersion(chartRoot); err == nil {
		pin = "Strimzi " + v + ", the version this platform pins"
	}
	return fmt.Sprintf(`Ask a cluster admin to install %s, in its own namespace strimzi-operator,
watching %s:

  helm upgrade --install strimzi-operator %s -n strimzi-operator --create-namespace \
    --set strimzi-kafka-operator.createAggregateRoles=true \
    --set strimzi-kafka-operator.watchAnyNamespace=false \
    --set 'strimzi-kafka-operator.watchNamespaces={%s}'`, pin, strings.Join(watch, ", "), chartRef("strimzi-operator"), strings.Join(watch, ","))
}

// adminlessOperatorAdvice says what a cluster admin could do with the
// installed operator, which this account cannot change: upgrade it to the
// pin, or replace one kates did not install with kates' own.
func adminlessOperatorAdvice(op strimzi.Operator, pin string, watch []string) string {
	cmp, err := compareVersions(op.Version, pin)
	switch {
	case err != nil:
		return "Kafka follows its window."
	case cmp > 0:
		return fmt.Sprintf("Newer than this platform's pin (%s); Kafka follows its window.", pin)
	case cmp == 0:
		return "The version this platform pins."
	case op.Release != "":
		return fmt.Sprintf(`Older than this platform's pin (%s); Kafka follows its window. A cluster admin upgrades it with:

  helm upgrade %s %s -n %s --reuse-values --set strimziVersion=%s`, pin, op.Release, chartRef("strimzi-operator"), op.Namespace, pin)
	}
	return fmt.Sprintf(`Older than this platform's pin (%s) and not installed by kates; Kafka follows its window.
A cluster admin upgrades it with the tool that installed it, or removes it and installs kates' own.
%s`, pin, strimziInstallSuggestion(watch))
}

// leftOutComponent is a component the admin-less profile does not install.
type leftOutComponent struct{ name, why string }

// adminlessLeftOut is what the admin-less profile does not install, and why,
// so the wizard and the plan name it instead of leaving it out unseen.
// prometheus says whether the cluster has a Prometheus operator to scrape for,
// scrape whether the wizard's PodMonitors for it were chosen.
func adminlessLeftOut(prometheus, scrape bool) []leftOutComponent {
	monitoring := "needs cluster admin, and the cluster has no Prometheus operator for this stack's PodMonitors"
	switch {
	case scrape:
		monitoring = "needs cluster admin; the cluster's own Prometheus scrapes this stack instead"
	case prometheus:
		monitoring = "needs cluster admin; the wizard's admin-less Monitoring option adds PodMonitors for the cluster's own Prometheus instead"
	}
	return []leftOutComponent{
		{"Strimzi Operator", "its CRDs and cluster roles need cluster admin; Kafka runs on the operator a cluster admin installed"},
		{"Cert-Manager, Kyverno", "cluster-wide CRDs and admission webhooks need cluster admin"},
		{"Litmus Chaos", "its CRDs and cluster roles need cluster admin, so this Kates runs no chaos experiments"},
		{"Monitoring (Prometheus, Grafana)", monitoring},
	}
}

// describeLeftOut is the left-out components, one per line.
func describeLeftOut(components []leftOutComponent) string {
	lines := make([]string, len(components))
	for i, c := range components {
		lines[i] = c.name + " — " + c.why
	}
	return strings.Join(lines, "\n")
}

// prometheusOperatorAPI says whether the cluster serves PodMonitors, which
// admin-less monitoring creates for a Prometheus already there.
func prometheusOperatorAPI(ctx context.Context) bool {
	out, _ := runExecOutputFn(ctx, "kubectl", "api-resources", "--api-group=monitoring.coreos.com", "-o", "name")
	return slices.Contains(strings.Fields(string(out)), "podmonitors.monitoring.coreos.com")
}

// describeStrimziOperator is one operator as the prerequisites list it.
func describeStrimziOperator(op strimzi.Operator) string {
	watches := "cluster-wide"
	if op.Scope != strimzi.ScopeCluster {
		watches = "watching " + strings.Join(op.Watches, ", ")
	}
	return fmt.Sprintf("Strimzi %s in namespace %s, %s", op.Version, op.Namespace, watches)
}

// checkAdminlessStrimzi fails when Strimzi is missing, with the install a
// cluster admin runs, or when the operators the account can see leave one of
// the namespaces unwatched: its Kafka resources would never be reconciled.
func checkAdminlessStrimzi(ctx context.Context, ns resolvedNamespaces) error {
	watch := adminlessStrimziNamespaces(ns)
	out, _ := runExecOutputFn(ctx, "kubectl", "api-resources", "--api-group=kafka.strimzi.io", "-o", "name")
	if !slices.Contains(strings.Fields(string(out)), "kafkas.kafka.strimzi.io") {
		return fmt.Errorf("no Strimzi operator is installed on this cluster, and installing it needs cluster admin.\n%s", strimziInstallSuggestion(watch))
	}

	// A namespace account may not list pods everywhere: the CRDs are then all
	// it can see, and the operator's watch list stays the admin's to confirm.
	ops, err := strimzi.InstalledOperators(ctx, execOutputRunner{})
	if err != nil || len(ops) == 0 {
		PrintPhaseItem(fmt.Sprintf("Strimzi: installed, but this account cannot see its operator; it must watch %s", strings.Join(watch, ", ")))
		return nil
	}
	for _, op := range ops {
		PrintPhaseItem(describeStrimziOperator(op))
	}
	var unwatched []string
	for _, namespace := range watch {
		if !slices.ContainsFunc(ops, func(op strimzi.Operator) bool { return operatorWatches(&op, namespace) }) {
			unwatched = append(unwatched, namespace)
		}
	}
	if len(unwatched) == 0 {
		return nil
	}
	op := ops[0]
	if p := primaryOperator(ops, ns.kafka); p != nil {
		op = *p
	}
	fix := fmt.Sprintf("Ask a cluster admin to add %s to STRIMZI_NAMESPACE on Deployment %s/%s.", strings.Join(unwatched, ", "), op.Namespace, op.Name)
	if op.Release != "" {
		// The release namespace is always watched, so the list leaves it out.
		list := slices.DeleteFunc(append(slices.Clone(op.Watches), unwatched...), func(n string) bool { return n == op.Namespace })
		slices.Sort(list)
		fix = fmt.Sprintf(`Ask a cluster admin to add them to its watched namespaces:

  helm upgrade %s %s -n %s --reuse-values \
    --set 'strimzi-kafka-operator.watchNamespaces={%s}'`, op.Release, chartRef("strimzi-operator"), op.Namespace, strings.Join(slices.Compact(list), ","))
	}
	return fmt.Errorf("the installed Strimzi operator does not watch %s, so its Kafka resources there would never be reconciled.\nInstalled: %s.\n%s",
		strings.Join(unwatched, ", "), describeStrimziOperator(op), fix)
}

// checkAdminlessPrerequisites fails before anything is installed when Strimzi
// is missing or does not watch the namespaces, or the account may not create
// what the stack needs.
func checkAdminlessPrerequisites(ctx context.Context, ns resolvedNamespaces) error {
	if err := checkAdminlessStrimzi(ctx, ns); err != nil {
		return err
	}

	grants := adminlessGrants(ns)
	var missing []string
	for _, namespace := range slices.Sorted(maps.Keys(grants)) {
		var refused []string
		for _, resource := range grants[namespace] {
			answer, _ := runExecOutputFn(ctx, "kubectl", "auth", "can-i", "create", resource, "-n", namespace)
			if strings.TrimSpace(string(answer)) != "yes" {
				refused = append(refused, resource)
			}
		}
		if len(refused) > 0 {
			missing = append(missing, namespace+": "+strings.Join(refused, ", "))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf(`this account may not create what the admin-less stack needs (or the namespace does not exist):
  %s

The built-in admin role in a namespace covers the core resources. For Strimzi's, a cluster
admin either sets strimzi-kafka-operator.createAggregateRoles=true on the Strimzi release
(it adds them to admin and edit), or binds a ClusterRole that grants them there`, strings.Join(missing, "\n  "))
}

// The reads the kates chart's Kafka-reader Role grants in the Kafka namespace
// (charts/kates/templates/rbac.yaml), which a conformity check needs. RBAC lets
// an account create a Role only for permissions it holds, so the install
// creates it only when the account holds all of them.
var conformityReaderGrants = [][2]string{
	{"get", "kafkas.kafka.strimzi.io"},
	{"list", "kafkanodepools.kafka.strimzi.io"},
	{"list", "pods"},
	{"list", "persistentvolumeclaims"},
	{"list", "podmonitors.monitoring.coreos.com"},
	{"list", "endpointslices.discovery.k8s.io"},
}

// adminlessConformityMissing holds the reads the account lacks in the Kafka
// namespace; when it is not empty Kates installs without the Role.
var adminlessConformityMissing []string

func missingConformityGrants(ctx context.Context, kafkaNS string) []string {
	var missing []string
	for _, g := range conformityReaderGrants {
		answer, _ := runExecOutputFn(ctx, "kubectl", "auth", "can-i", g[0], g[1], "-n", kafkaNS)
		if strings.TrimSpace(string(answer)) != "yes" {
			missing = append(missing, g[0]+" "+g[1])
		}
	}
	return missing
}

// conformityRoleHandoff tells the user how an admin turns conformity checks on
// when the install had to leave the Role out.
func conformityRoleHandoff(ns resolvedNamespaces, missing []string) string {
	return fmt.Sprintf(`Conformity checks are off: Kates needs a read-only Role in namespace %s, and this account
may not create it because it lacks: %s.
Everything else works. To turn conformity checks on, an account that holds those reads there
(a cluster admin) runs:

  helm template kates %s -n %s --set rbac.scope=namespace \
    --set networkPolicy.kafka.namespace=%s --show-only templates/rbac.yaml | kubectl apply -f -`,
		ns.kafka, strings.Join(missing, ", "), chartRef("kates"), ns.app, ns.kafka)
}
