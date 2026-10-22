package cmd

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/spf13/cobra"
)

// The admin-less profile: what kates deploy installs for an account that is
// not cluster-admin. Kafka, Kafka Connect with its PostgreSQL, MirrorMaker 2,
// Apicurio, Kafka UI and the Kates backend (rbac.scope=namespace) go into
// namespaces the account already has, as --topology and the --*-ns flags place
// them — one per component by default, or one for all — and every step that
// needs the cluster (Strimzi, cert-manager, Kyverno, monitoring, Litmus,
// node introspection, StorageClasses, namespace creation) is left out. A
// cluster admin must already run a Strimzi operator that watches the namespaces.

var deployAdminless bool

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

	if deployTopology == "single" {
		if !cmd.Flags().Changed("namespace") {
			if ns := kubeContextNamespace(ctx); ns != "" {
				deployNamespace = ns
			}
		}
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
	for namespace, resources := range grants {
		slices.Sort(resources)
		grants[namespace] = slices.Compact(resources)
	}
	return grants
}

// checkAdminlessPrerequisites fails before anything is installed when Strimzi
// is missing or the account may not create what the stack needs.
func checkAdminlessPrerequisites(ctx context.Context, ns resolvedNamespaces) error {
	out, _ := runExecOutputFn(ctx, "kubectl", "api-resources", "--api-group=kafka.strimzi.io", "-o", "name")
	if !slices.Contains(strings.Fields(string(out)), "kafkas.kafka.strimzi.io") {
		return fmt.Errorf(`no Strimzi operator is installed on this cluster, and installing it needs cluster admin.
Ask a cluster admin to install the operator watching namespace %s, for example:

  helm upgrade --install strimzi-operator charts/strimzi-operator -n strimzi-operator --create-namespace \
    --set strimzi-kafka-operator.createAggregateRoles=true`, ns.kafka)
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
(a cluster admin) runs, from this repository:

  helm template kates charts/kates -n %s --set rbac.scope=namespace \
    --set networkPolicy.kafka.namespace=%s --show-only templates/rbac.yaml | kubectl apply -f -`,
		ns.kafka, strings.Join(missing, ", "), ns.app, ns.kafka)
}
