package cmd

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/bmscomp/kates/cli/pkg/cluster"
	"github.com/bmscomp/kates/cli/pkg/theme"
)

// The cluster gate runs before anything in `kates deploy` that assumes a
// reachable cluster. It answers one question — which cluster are we deploying
// to? — and, when there is no answer, offers to build one.
//
// Seams for tests. These mirror the runExecFn/runHelmFn style already used by
// deploy_test.go so the gate can be driven without a cluster, Docker, or a TTY.
var (
	listContextsFn      = cluster.ListContexts
	checkAllReachableFn = cluster.CheckAllReachable
	probeEnvironmentFn  = cluster.ProbeEnvironment
	createKindFn        = cluster.CreateKind
	kindClusterExistsFn = cluster.KindClusterExists
	configExistsFn      = cluster.ConfigExists

	// interactiveFn reports whether we may prompt. Overridden in tests.
	interactiveFn = IsInteractive

	// confirmFn asks a yes/no question. Overridden in tests.
	confirmFn = confirmPrompt

	// pickContextFn presents the picker. Overridden in tests.
	pickContextFn = pickContext

	// resolveClusterFn is the gate as runDeploy sees it. The deploy
	// orchestration tests stub this out — they assume a cluster and are not
	// testing gate behavior, which cluster_gate_test.go covers directly.
	resolveClusterFn = resolveCluster
)

// The interactivity guard lives in interactive.go (IsInteractive) — shared by
// every interactive surface in the CLI, not just this gate.

// styles reused from the repo's lipgloss vocabulary.
var (
	gateTitle  = lipgloss.NewStyle().Bold(true)
	gateDim    = lipgloss.NewStyle().Foreground(theme.Muted)
	gateOK     = lipgloss.NewStyle().Foreground(theme.Success)
	gateWarn   = lipgloss.NewStyle().Foreground(theme.Warning)
	gateErr    = lipgloss.NewStyle().Foreground(theme.Error)
	gateAccent = lipgloss.NewStyle().Foreground(theme.Accent)
)

// resolveCluster is the gate. It returns the context name to deploy into, or an
// error explaining precisely what is missing.
func resolveCluster() (string, error) {
	contexts, err := listContextsFn(defaultExecutor)
	if err != nil {
		return "", fmt.Errorf("could not read kubeconfig: %w", err)
	}

	if len(contexts) > 0 {
		fmt.Println(gateDim.Render(fmt.Sprintf("  Probing %s…", plural(len(contexts), "context", "contexts"))))
		contexts = checkAllReachableFn(defaultExecutor, contexts)
	}

	env := probeEnvironmentFn(defaultExecutor)
	decision := cluster.Resolve(contexts, env)

	switch decision.Outcome {
	case cluster.UseContext:
		c := decision.Candidates[0]
		fmt.Println("  " + gateOK.Render("✓") + " Cluster: " + gateAccent.Render(c.Name) + gateDim.Render(describeContext(c)))
		return c.Name, nil

	case cluster.ChooseContext:
		if !interactiveFn() {
			// Never guess. Picking silently is how you deploy to prod by
			// accident.
			return "", fmt.Errorf(
				"%d reachable clusters found (%s) — cannot choose without a terminal.\n"+
					"  Select one first:  kubectl config use-context <name>",
				len(decision.Candidates), strings.Join(contextNames(decision.Candidates), ", "))
		}
		return pickContextFn(decision.Candidates)

	case cluster.OfferKind:
		return offerKind(decision)

	case cluster.NeedKind:
		printNoCluster(decision)
		return "", fmt.Errorf("Docker is running but kind is not installed.\n" +
			"  Install it:  brew install kind   (or see https://kind.sigs.k8s.io/docs/user/quick-start/#installation)\n" +
			"  Then re-run: kates deploy")

	case cluster.NeedDockerRunning:
		printNoCluster(decision)
		return "", fmt.Errorf("Docker is installed but the daemon is not running.\n" +
			"  Start Docker Desktop (or `sudo systemctl start docker`), then re-run: kates deploy")

	default: // NeedDocker
		printNoCluster(decision)
		return "", fmt.Errorf("no Docker and no reachable Kubernetes cluster.\n" +
			"  Kates needs one of:\n" +
			"    • a reachable Kubernetes cluster (check: kubectl cluster-info)\n" +
			"    • Docker, so a local 3-zone kind cluster can be created for you\n" +
			"  Install Docker: https://docs.docker.com/get-docker/")
	}
}

// offerKind asks whether to build the local 3-AZ cluster, then builds it.
func offerKind(d cluster.Decision) (string, error) {
	printNoCluster(d)

	// A dry run must not build a cluster. --dry-run is checked much later in
	// runDeploy, so without this the plan preview would create real
	// infrastructure on the way to telling you it changes nothing.
	if deployDryRun {
		fmt.Println("  " + gateDim.Render(fmt.Sprintf(
			"would offer to create a local %d-zone kind cluster (skipped: --dry-run)",
			len(cluster.KindZones))))
		return "", fmt.Errorf("no reachable Kubernetes cluster to plan against.\n" +
			"  Re-run without --dry-run to create a local kind cluster.")
	}

	if !interactiveFn() {
		return "", fmt.Errorf("no reachable Kubernetes cluster.\n"+
			"  Docker and kind are available — a local %d-zone cluster can be created.\n"+
			"  Re-run in a terminal to be prompted, or create it directly:\n"+
			"    kind create cluster --config %s --name %s",
			len(cluster.KindZones), cluster.DefaultKindConfig, cluster.DefaultKindClusterName)
	}

	// The topology config is repo-relative. Offering to build a cluster we
	// cannot describe would fail inside `kind create` with a bare "no such
	// file"; say so now instead.
	if !configExistsFn(cluster.DefaultKindConfig) {
		return "", fmt.Errorf("no reachable Kubernetes cluster, and the kind topology %s is not readable from here.\n"+
			"  The %d-zone config lives in the kates repo — run from the repo root, or create the cluster yourself:\n"+
			"    kind create cluster --config <path-to>/%s --name %s",
			cluster.DefaultKindConfig, len(cluster.KindZones),
			cluster.DefaultKindConfig, cluster.DefaultKindClusterName)
	}

	if kindClusterExistsFn(defaultExecutor, cluster.DefaultKindClusterName) {
		// The cluster exists but nothing was reachable — recreating would
		// silently destroy it, so stop and let a human decide.
		return "", fmt.Errorf("a kind cluster named %q already exists but is not reachable.\n"+
			"  Inspect it:  kubectl cluster-info --context kind-%s\n"+
			"  Or remove it and re-run: kind delete cluster --name %s",
			cluster.DefaultKindClusterName, cluster.DefaultKindClusterName, cluster.DefaultKindClusterName)
	}

	fmt.Println()
	fmt.Println("  " + gateTitle.Render("Create a local Kubernetes cluster?"))
	fmt.Println("  " + gateDim.Render(fmt.Sprintf("kind · %s · zones %s · takes a few minutes",
		cluster.DefaultKindClusterName, strings.Join(cluster.KindZones, "/"))))
	fmt.Println()

	ok, err := confirmFn("Create it now?")
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("no cluster to deploy to, and kind cluster creation was declined")
	}

	fmt.Println()
	err = createKindFn(defaultExecutor, cluster.KindOptions{
		Progress: func(step string) {
			fmt.Println("  " + gateAccent.Render("→") + " " + step)
		},
	})
	if err != nil {
		return "", fmt.Errorf("creating the kind cluster: %w", err)
	}

	// kind names the context kind-<cluster>.
	ctxName := "kind-" + cluster.DefaultKindClusterName
	fmt.Println("  " + gateOK.Render("✓") + " Cluster ready: " + gateAccent.Render(ctxName))
	return ctxName, nil
}

// printNoCluster explains what was found. It distinguishes "no contexts at all"
// from "contexts exist but none answered" — a stale kubeconfig is the common
// laptop case and reporting it as "no clusters found" is simply untrue.
func printNoCluster(d cluster.Decision) {
	fmt.Println()
	if len(d.Unreachable) > 0 {
		fmt.Println("  " + gateWarn.Render("!") + " " +
			gateTitle.Render(fmt.Sprintf("%s configured, none reachable",
				plural(len(d.Unreachable), "context", "contexts"))))
		for _, c := range d.Unreachable {
			fmt.Println("    " + gateErr.Render("✗") + " " + c.Name + gateDim.Render(describeContext(c)))
		}
	} else {
		fmt.Println("  " + gateWarn.Render("!") + " " + gateTitle.Render("No Kubernetes context configured"))
	}
}

func describeContext(c cluster.Context) string {
	var bits []string
	if c.Cluster != "" && c.Cluster != c.Name {
		bits = append(bits, c.Cluster)
	}
	if c.Namespace != "" {
		bits = append(bits, "ns:"+c.Namespace)
	}
	if len(bits) == 0 {
		return ""
	}
	return "  (" + strings.Join(bits, " · ") + ")"
}

func contextNames(cs []cluster.Context) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
