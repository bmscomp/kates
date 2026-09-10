package cmd

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// TestDeployCommand_MirrorMaker2 checks the loopback mirror is installed as
// its own release, pointed at the primary on both ends, with the resolved
// versions — and that it is registered as a component everywhere the
// wizard, the dashboard and the summary look.
func TestDeployCommand_MirrorMaker2(t *testing.T) {
	defer func() {
		deployTopology = "isolated"
		deployKafkaNS = "kafka"
		deployMM2NS = "kafka"
		deployKafkaName = "krafter"
		deployWithSchemaRegistry = "none"
		deployWithChaos = false
		deployWithMonitoring = false
		deployWithCertManager = false
		deployWithKyverno = false
		deployWithStrimzi = false
		deployWithKafkaConnect = false
		deployWithKafkaUI = false
		deployWithMirrorMaker2 = false
	}()

	deployTopology = "isolated"
	deployKafkaNS = "kafka"
	deployMM2NS = "kafka"
	deployKafkaName = "krafter"
	deployWithSchemaRegistry = "none"
	deployWithChaos = false
	deployWithMonitoring = false
	deployWithCertManager = false
	deployWithKyverno = false
	deployWithStrimzi = false
	deployWithKafkaConnect = false
	deployWithKafkaUI = false
	deployWithMirrorMaker2 = true

	var helmCommands []string
	var mu sync.Mutex
	runExecFn = func(ctx context.Context, name string, args ...string) error { return nil }
	runExecStdinFn = func(ctx context.Context, name string, args []string, stdinData string) error { return nil }
	runHelmFn = func(ctx context.Context, args ...string) error {
		mu.Lock()
		helmCommands = append(helmCommands, "helm "+strings.Join(args, " "))
		mu.Unlock()
		return nil
	}
	isHelmReleaseDeployedFn = func(ctx context.Context, release, namespace string) bool { return false }
	defaultExecutor = &MockExecutor{}

	if err := runDeploy(deployCmd, []string{}); err != nil {
		t.Fatalf("runDeploy failed: %v", err)
	}

	var mm2 string
	for _, c := range helmCommands {
		if strings.Contains(c, "helm upgrade --install mm2 charts/mirror-maker2") {
			mm2 = c
		}
	}
	if mm2 == "" {
		t.Fatalf("MirrorMaker 2 was not installed; helm calls:\n%s", strings.Join(helmCommands, "\n"))
	}
	for _, want := range []string{
		"-n kafka",
		"--set target.clusterName=krafter",
		"--set target.namespace=kafka",
		"--set mirrors[0].source.clusterName=krafter",
		"--set mirrors[0].source.namespace=kafka",
		"--set-string version=4.3.0",
		"--set-string strimziVersion=1.1.0",
	} {
		if !strings.Contains(mm2, want) {
			t.Errorf("mm2 install misses %q:\n%s", want, mm2)
		}
	}

	// The component is part of the plan everywhere it is listed.
	ns := resolveNamespaces()
	if ns.mm2 != "kafka" {
		t.Errorf("mm2 namespace: %q", ns.mm2)
	}
	found := false
	for _, e := range buildSharedEntries(ns) {
		if e.Release == "mm2" && e.Namespace == "kafka" && e.Group == "C" {
			found = true
		}
	}
	if !found {
		t.Error("mm2 missing from the shared component entries")
	}
	if got := countDeploySteps(); got < 4 {
		t.Errorf("step count should include mm2, got %d", got)
	}
}

func TestDeployReviewSummary(t *testing.T) {
	defer func() {
		deployTopology = "isolated"
		deployWithMirrorMaker2 = false
		deployWithKafkaConnect = false
		deployWithKafkaUI = false
		deployOperatorScope = "cluster"
		deployStrimziVersion = ""
		deployKafkaVersion = ""
		deployHA = true
	}()
	deployTopology = "isolated"
	deployKafkaNS = "kafka"
	deployMM2NS = "kafka"
	deployWithMirrorMaker2 = true
	deployWithKafkaConnect = true
	deployWithKafkaUI = true
	deployWithStrimzi = true
	deployOperatorScope = "namespace"
	deployHA = false

	// The components are a grid now, not a comma-joined sentence: assert the
	// facts a reader needs, wherever the layout puts them.
	s := stripStyle(deployReviewSummary())
	for _, want := range []string{
		"namespace-scoped (one operator per Kafka namespace)",
		"single-node",
		"across 3 waves",
		// Every selected component, with the namespace it lands in.
		"Strimzi Operator",
		"MirrorMaker 2",
		"Kafka Connect",
		"PostgreSQL",
		"Kafka UI",
		"connect",
		// And the waves themselves, which are what the grid adds.
		"Operators & CRDs",
		"Core Infrastructure",
		"Applications",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("review misses %q:\n%s", want, s)
		}
	}

	deployOperatorScope = "cluster"
	deployTopology = "single"
	deployNamespace = "kates-stack"
	s = stripStyle(deployReviewSummary())
	if !strings.Contains(s, "mono cluster") || !strings.Contains(s, "everything in kates-stack") {
		t.Errorf("single/mono summary:\n%s", s)
	}
	deployTopology = "isolated"
	deployNamespace = "kates-stack"
}
