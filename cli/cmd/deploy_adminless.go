package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"
)

// The admin-less profile: what kates deploy installs for an account that is
// not cluster-admin. Everything goes into one namespace the account already
// has — Kafka, Kafka Connect with its PostgreSQL, MirrorMaker 2, Apicurio,
// Kafka UI and the Kates backend (rbac.scope=namespace) — and every step that
// needs the cluster (Strimzi, cert-manager, Kyverno, monitoring, Litmus,
// node introspection, StorageClasses, namespace creation) is left out. A
// cluster admin must already run a Strimzi operator that watches the namespace.

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
	if f := cmd.Flags().Lookup("topology"); f != nil && f.Changed && f.Value.String() != "single" {
		return errors.New("the admin-less profile installs into one namespace; drop --topology or set it to single")
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

	deployTopology = "single"
	if !cmd.Flags().Changed("namespace") {
		if ns := kubeContextNamespace(ctx); ns != "" {
			deployNamespace = ns
		}
	}
	// PostgreSQL has its own namespace flag even in single topology.
	deployDbNS = deployNamespace
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
and the Kates backend into one namespace you already have:

  kates deploy --adminless --namespace %s

It needs a Strimzi operator, installed by a cluster admin, that watches that namespace.`, kubeContext, ns)
}

// The resources the admin-less stack creates in its namespace.
var adminlessRequiredGrants = []string{
	"kafkas.kafka.strimzi.io",
	"kafkanodepools.kafka.strimzi.io",
	"kafkatopics.kafka.strimzi.io",
	"kafkausers.kafka.strimzi.io",
	"kafkaconnects.kafka.strimzi.io",
	"kafkaconnectors.kafka.strimzi.io",
	"kafkamirrormaker2s.kafka.strimzi.io",
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

// checkAdminlessPrerequisites fails before anything is installed when Strimzi
// is missing or the account may not create what the stack needs.
func checkAdminlessPrerequisites(ctx context.Context, namespace string) error {
	out, _ := runExecOutputFn(ctx, "kubectl", "api-resources", "--api-group=kafka.strimzi.io", "-o", "name")
	if !slices.Contains(strings.Fields(string(out)), "kafkas.kafka.strimzi.io") {
		return fmt.Errorf(`no Strimzi operator is installed on this cluster, and installing it needs cluster admin.
Ask a cluster admin to install the operator watching namespace %s, for example:

  helm upgrade --install strimzi-operator charts/strimzi-operator -n strimzi-operator --create-namespace \
    --set strimzi-kafka-operator.createAggregateRoles=true`, namespace)
	}

	var missing []string
	for _, resource := range adminlessRequiredGrants {
		answer, _ := runExecOutputFn(ctx, "kubectl", "auth", "can-i", "create", resource, "-n", namespace)
		if strings.TrimSpace(string(answer)) != "yes" {
			missing = append(missing, resource)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf(`this account may not create these in namespace %s:
  %s

The built-in admin role covers the core ones. For Strimzi's, a cluster admin either sets
strimzi-kafka-operator.createAggregateRoles=true on the Strimzi release (it adds them to
admin and edit), or binds a ClusterRole that grants them in %s`, namespace, strings.Join(missing, "\n  "), namespace)
}
