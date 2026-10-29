package cmd

import (
	"context"
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/huh"

	"github.com/bmscomp/kates/cli/pkg/cluster"
)

// The admin-less kind lab. With no reachable cluster, the gate offers two
// kind clusters: panda, where kates deploys as cluster admin, and an
// admin-less one (fanon by default) that stands in for a cluster a team
// shares. On that one, kind's own admin account plays the cluster admin
// once: it makes the team's namespaces, installs the operators a team
// cannot (Kyverno, the Strimzi operator watching those namespaces, the
// monitoring stack — the list is the user's) and a ServiceAccount with the
// admin role in those namespaces only. The deploy then runs as that account,
// through a kube context named after the cluster, on the admin-less profile.

// The operators the admin installs on an admin-less kind cluster.
var adminlessKindOperators = []string{"kyverno", "strimzi", "monitoring"}

// DefaultAdminlessKindName is the admin-less kind cluster's default name.
const DefaultAdminlessKindName = "fanon"

// kindChoice is what the gate builds when no cluster answers.
type kindChoice struct {
	adminless bool
	name      string
	operators []string
}

// kindChoiceFn asks which kind cluster to build. Overridden in tests.
var kindChoiceFn = runKindChoiceForm

func runKindChoiceForm() (kindChoice, error) {
	c := kindChoice{name: DefaultAdminlessKindName, operators: slices.Clone(adminlessKindOperators)}
	if err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[bool]().
			Title("Which local cluster?").
			Options(
				huh.NewOption("Admin — kind cluster "+cluster.DefaultKindClusterName+", kates deploys as cluster admin", false),
				huh.NewOption("Admin-less — kates deploys as an account with no cluster admin", true),
			).
			Value(&c.adminless),
	)).WithTheme(ThemeKates()).WithAccessible(accessibleUI()).Run(); err != nil {
		return c, err
	}
	if !c.adminless {
		return kindChoice{name: cluster.DefaultKindClusterName}, nil
	}
	err := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Cluster name").
			Description("Empty for "+DefaultAdminlessKindName+".").
			Value(&c.name).
			Validate(func(s string) error {
				if s == cluster.DefaultKindClusterName {
					return fmt.Errorf("name it anything but %q", cluster.DefaultKindClusterName)
				}
				return nil
			}),
		huh.NewMultiSelect[string]().
			Title("Installed by the cluster admin").
			Description("The admin-less deploy needs Strimzi; without it, it stops at its prerequisites.").
			Options(
				huh.NewOption("Kyverno", "kyverno").Selected(true),
				huh.NewOption("Strimzi operator", "strimzi").Selected(true),
				huh.NewOption("Monitoring (Prometheus, Grafana)", "monitoring").Selected(true),
			).
			Value(&c.operators),
	)).WithTheme(ThemeKates()).WithAccessible(accessibleUI()).Run()
	if c.name == "" {
		c.name = DefaultAdminlessKindName
	}
	return c, err
}

// bootstrapAdminlessKindFn is the admin's part. Overridden in tests.
var bootstrapAdminlessKindFn = bootstrapAdminlessKind

// adminlessTeamNamespaces are the namespaces the deploy's flags place the
// stack in, which the admin makes and the team account is given.
func adminlessTeamNamespaces() []string {
	names := []string{deployNamespace}
	if deployTopology != "single" {
		names = []string{deployKafkaNS, deployConnectNS, deployDbNS, deployAppNS, deployKafkaUINS, deployMM2NS}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// adminlessTeamManifest is the team's namespaces, its ServiceAccount (in
// home) with a long-lived token, and the built-in admin role in each
// namespace, which a ClusterRole extends to the monitoring.coreos.com
// resources: the stack's PodMonitors, and conformity's reads of them.
func adminlessTeamManifest(account, home string, namespaces []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: %s-monitoring
  labels:
    rbac.authorization.k8s.io/aggregate-to-admin: "true"
rules:
- apiGroups: [monitoring.coreos.com]
  resources: [podmonitors, servicemonitors, prometheusrules]
  verbs: [get, list, watch, create, update, patch, delete]
`, account)
	for _, ns := range namespaces {
		fmt.Fprintf(&b, "---\napiVersion: v1\nkind: Namespace\nmetadata:\n  name: %s\n", ns)
	}
	fmt.Fprintf(&b, `---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: %[1]s
  namespace: %[2]s
---
apiVersion: v1
kind: Secret
type: kubernetes.io/service-account-token
metadata:
  name: %[1]s-token
  namespace: %[2]s
  annotations:
    kubernetes.io/service-account.name: %[1]s
`, account, home)
	for _, ns := range namespaces {
		fmt.Fprintf(&b, `---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: %[1]s-admin
  namespace: %[2]s
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: admin
subjects:
- kind: ServiceAccount
  name: %[1]s
  namespace: %[3]s
`, account, ns, home)
	}
	return b.String()
}

// bootstrapAdminlessKind does, as kind's admin, what a cluster admin does for
// a team, then returns the team's kube context, named after the cluster. It
// refuses that context if it can do everything everywhere.
func bootstrapAdminlessKind(name string, operators []string) (string, error) {
	ctx := context.Background()
	adminCtx, teamCtx, account := "kind-"+name, name, "kates-team"
	namespaces := adminlessTeamNamespaces()
	progress := func(step string) { fmt.Println("  " + gateAccent.Render("→") + " " + step) }

	// The account lives where Kates does: the kube context's namespace.
	home := deployAppNS
	if deployTopology == "single" {
		home = deployNamespace
	}
	token, err := adminlessKindAsAdmin(ctx, adminCtx, account, home, namespaces, operators, progress)
	if err != nil {
		return "", err
	}

	progress("Writing kube context " + teamCtx + " for " + account)
	user := name + "-" + account
	for _, args := range [][]string{
		{"config", "set-credentials", user, "--token=" + token},
		{"config", "set-context", teamCtx, "--cluster=" + adminCtx, "--user=" + user, "--namespace=" + home},
	} {
		if out, err := runExecCombinedFn(ctx, "kubectl", args...); err != nil {
			return "", fmt.Errorf("kubectl %s: %w: %s", args[1], err, strings.TrimSpace(string(out)))
		}
	}

	// The point of the cluster: the account kates deploys as is no admin.
	out, _ := runExecOutputFn(ctx, "kubectl", "--context="+teamCtx, "auth", "can-i", "*", "*", "--all-namespaces")
	if answer := strings.TrimSpace(string(out)); answer != "no" {
		return "", fmt.Errorf("context %s should not be cluster admin, but kubectl auth can-i '*' '*' --all-namespaces answered %q", teamCtx, answer)
	}
	fmt.Println("  " + gateOK.Render("✓") + " " + teamCtx + " is not cluster admin: admin in " + strings.Join(namespaces, ", ") + " only")
	deployAdminless = true
	return teamCtx, nil
}

// adminlessKindAsAdmin is the cluster admin's part, on adminCtx: the team's
// namespaces and account, then the operators. It returns the account's token.
func adminlessKindAsAdmin(ctx context.Context, adminCtx, account, home string, namespaces, operators []string, progress func(string)) (string, error) {
	defer pinKubeContext(adminCtx)()

	progress("Creating the team's namespaces " + strings.Join(namespaces, ", ") + " and its account " + account)
	if err := runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, adminlessTeamManifest(account, home, namespaces)); err != nil {
		return "", fmt.Errorf("creating the team's namespaces and account: %w", err)
	}
	for _, op := range adminlessKindOperators {
		if !slices.Contains(operators, op) {
			continue
		}
		progress("Installing " + op + " as the cluster admin")
		if err := installAdminlessKindOperator(ctx, op, namespaces); err != nil {
			return "", fmt.Errorf("installing %s: %w", op, err)
		}
	}
	return serviceAccountToken(ctx, account, home)
}

// installAdminlessKindOperator installs one operator as deploy does on a kind
// cluster, plus what a shared cluster needs: Strimzi watching only the
// team's namespaces with its roles aggregated into admin, and Kyverno
// honouring PolicyExceptions in any namespace.
func installAdminlessKindOperator(ctx context.Context, op string, namespaces []string) error {
	switch op {
	case "strimzi":
		dir := chartRootPath(operatorChartDir)
		if err := helmDependency(ctx, "build", dir); err != nil {
			return err
		}
		return runHelmFn(ctx, "upgrade", "--install", "strimzi-operator", dir, "-n", "strimzi-operator", "--create-namespace",
			"-f", chartOverlayFor(true, dir),
			"--set", "strimzi-kafka-operator.createAggregateRoles=true",
			"--set", "strimzi-kafka-operator.watchAnyNamespace=false",
			"--set", "strimzi-kafka-operator.watchNamespaces={"+strings.Join(namespaces, ",")+"}",
			"--wait", "--timeout", "10m")
	case "kyverno":
		chart, err := thirdPartyChartArgs(ctx, "kyverno", true)
		if err != nil {
			return err
		}
		return runHelmFn(ctx, append(append([]string{"upgrade", "--install", "kyverno"}, chart...),
			"-n", "kyverno", "--create-namespace",
			"--set", "admissionController.replicas=1",
			"--set", "backgroundController.replicas=1",
			"--set", "cleanupController.replicas=1",
			"--set", "reportsController.replicas=1",
			"--set", "features.policyExceptions.enabled=true",
			"--set-string", "features.policyExceptions.namespace=*",
			"--wait", "--timeout", "10m")...)
	case "monitoring":
		dir := chartRootPath("charts/monitoring")
		if err := helmDependency(ctx, "update", dir); err != nil {
			return err
		}
		return runHelmFn(ctx, "upgrade", "--install", "monitoring", dir, "-n", "monitoring", "--create-namespace",
			"-f", chartOverlayFor(true, dir),
			"--set", "chaosAlerts.enabled=false",
			"--wait", "--timeout", "10m")
	}
	return fmt.Errorf("unknown operator %q", op)
}

// serviceAccountToken waits for the token controller to fill the account's
// token Secret.
func serviceAccountToken(ctx context.Context, account, namespace string) (string, error) {
	for range 30 {
		out, _ := runExecOutputFn(ctx, "kubectl", "get", "secret", account+"-token", "-n", namespace, "-o", "jsonpath={.data.token}")
		if enc := strings.TrimSpace(string(out)); enc != "" {
			token, err := base64.StdEncoding.DecodeString(enc)
			return string(token), err
		}
		time.Sleep(time.Second)
	}
	return "", fmt.Errorf("the token of ServiceAccount %s/%s was never issued", namespace, account)
}
