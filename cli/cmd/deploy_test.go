package cmd

import (
	"context"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/bmscomp/kates/cli/pkg/kafkaversion"
	"github.com/bmscomp/kates/cli/pkg/strimzi"
)

func init() {
	isTesting = true
	// These tests exercise deploy orchestration and assume a cluster already
	// exists. The cluster gate is a real precondition of runDeploy, but its own
	// behavior (picking, the kind offer, the blocked states) is covered directly
	// in cluster_gate_test.go — stubbing it here keeps that concern in one place.
	// stubClusterRead reports the same context as kubectl's current one, so
	// these runs never switch it.
	resolveClusterFn = func() (string, error) { return "test-context", nil }

	// Cluster READS are stubbed for the whole package. Without this they run
	// real kubectl: on a machine with no cluster every lookup fails, the deploy
	// paths fall into their readiness wait loops, and the Kafka Connect tests
	// sat there for the full two-minute secret deadline before failing. The
	// canned answers below are the "everything is already healthy" case, which
	// is what the orchestration tests are about — the waiting itself is not.
	runExecOutputFn = stubClusterRead
	runExecCombinedFn = stubClusterRead

	// Version resolution reads chart files and the cluster; the orchestration
	// tests get the pinned answer directly (its own logic is covered in
	// deploy_versions_test.go).
	resolveVersionPlanFn = func(_ context.Context, _ strimzi.Runner, o versionOptions) (*versionPlan, error) {
		return stubVersionPlan(o), nil
	}
}

// stubVersionPlan is the "pin, fresh cluster, newest Kafka" plan.
func stubVersionPlan(o versionOptions) *versionPlan {
	window := kafkaversion.NewWindow(kafkaversion.MustParse("4.2.0"), kafkaversion.MustParse("4.2.1"), kafkaversion.MustParse("4.3.0"))
	scope := o.Scope
	if scope == "" {
		scope = scopeCluster
	}
	vp := &versionPlan{
		Scope:          scope,
		StrimziVersion: "1.1.0",
		StrimziSource:  "pinned",
		Pinned:         true,
		PinnedVersion:  "1.1.0",
		ChartDir:       "charts/strimzi-operator",
		Window:         window.Strings(),
		KafkaVersion:   "4.3.0",
		KafkaDefaulted: o.KafkaVersion == "",
		Metadata:       "4.2",
		OperatorAction: "install",
		kafka:          kafkaversion.MustParse("4.3.0"),
	}
	if o.KafkaVersion != "" && o.KafkaVersion != "latest" {
		vp.kafka = kafkaversion.MustParse(o.KafkaVersion)
		vp.KafkaVersion = o.KafkaVersion
		vp.Metadata = kafkaversion.MetadataFor(vp.kafka, window)
	}
	if scope == scopeNamespace {
		vp.Watch = o.watchList()
	}
	return vp
}

// stubClusterRead answers the cluster queries the deploy paths make, in the
// shape each caller parses. Anything unrecognised returns empty output and no
// error, i.e. "nothing there", which is the safe default for a probe.
func stubClusterRead(_ context.Context, name string, args ...string) ([]byte, error) {
	joined := name + " " + strings.Join(args, " ")

	switch {
	// The context the stubbed cluster gate returns: already current.
	case joined == "kubectl config current-context":
		return []byte("test-context\n"), nil
	// Secrets are read for their base64 data field; a non-empty value ends the
	// wait loops immediately.
	case strings.Contains(joined, "get secret"):
		return []byte("dGVzdC1wYXNzd29yZA=="), nil
	// CRD establishment checks look for "True".
	case strings.Contains(joined, "get crd") && strings.Contains(joined, "Established"):
		return []byte("True"), nil
	case strings.Contains(joined, "get pods") && strings.Contains(joined, "entity-operator"):
		return []byte("Running"), nil
	// A non-empty items array means the workload exists, so no repair path runs.
	case strings.Contains(joined, "get pods") && strings.Contains(joined, "jsonpath={.items}"):
		return []byte(`[{"metadata":{"name":"connect-0"}}]`), nil
	case strings.Contains(joined, "get configmap"):
		return []byte("lowercaseOutputName: true"), nil
	default:
		return []byte(""), nil
	}
}

type MockExecutor struct{}

func (m *MockExecutor) LookPath(file string) (string, error) {
	return "/usr/local/bin/" + file, nil
}

func (m *MockExecutor) Exec(name string, args ...string) (string, error) {
	// Return mock JSON for get nodes so detection doesn't fail
	if name == "kubectl" && len(args) > 1 && args[0] == "get" && args[1] == "nodes" {
		return `{"items":[{"metadata":{"name":"node1"},"status":{"capacity":{"cpu":"4","memory":"8192Mi"}}}]}`, nil
	}
	if name == "kubectl" && len(args) > 0 && args[0] == "cluster-info" {
		return "Kubernetes control plane is running at https://127.0.0.1:6443", nil
	}
	return "", nil
}

func TestDeployCommand_SingleTopology(t *testing.T) {
	// Reset flags before testing
	deployTopology = "single"
	deployNamespace = "kates-test"
	deployWithSchemaRegistry = "none"
	deployWithChaos = false
	deployWithMonitoring = false
	deployWithCertManager = false
	deployWithKyverno = false
	deployWithStrimzi = false
	deployWithKafkaConnect = false

	var executedCommands []string
	var mu sync.Mutex

	// Mock functions
	runExecFn = func(ctx context.Context, name string, args ...string) error {
		cmdStr := name + " " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		return nil
	}
	runExecStdinFn = func(ctx context.Context, name string, args []string, stdinData string) error {
		return nil
	}
	runHelmFn = func(ctx context.Context, args ...string) error {
		cmdStr := "helm " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		return nil
	}
	isHelmReleaseDeployedFn = func(ctx context.Context, release, namespace string) bool {
		return false
	}

	defaultExecutor = &MockExecutor{}

	// Mock the cluster detection dependencies by overriding valuesFile creation or ensuring it succeeds
	// In runDeploy it creates .build/values-detected.yaml. We just run it and let it create the file.

	_ = deployCmd.Flags().Set("topology", deployTopology)
	err := runDeploy(deployCmd, []string{})
	if err != nil {
		t.Fatalf("runDeploy failed: %v", err)
	}

	// Verify that the commands executed successfully and used the correct namespace
	foundKafka := false
	foundKates := false

	for _, cmd := range executedCommands {
		if strings.Contains(cmd, "helm upgrade --install krafter") {
			foundKafka = true
			if !strings.Contains(cmd, "-n kates-test") {
				t.Errorf("Expected Kafka to be deployed in kates-test namespace, got: %s", cmd)
			}
		}
		if strings.Contains(cmd, "helm upgrade --install kates") {
			foundKates = true
			if !strings.Contains(cmd, "-n kates-test") {
				t.Errorf("Expected Kates to be deployed in kates-test namespace, got: %s", cmd)
			}
		}
	}

	if !foundKafka {
		t.Error("Kafka deployment command was not executed")
	}
	if !foundKates {
		t.Error("Kates deployment command was not executed")
	}
}

func TestDeployCommand_IsolatedTopology(t *testing.T) {
	deployTopology = "isolated"
	deployKafkaNS = "kafka-sys"
	deployConnectNS = "connect-sys"
	deployAppNS = "app-sys"
	deployChaosNS = "chaos-sys"
	deployWithSchemaRegistry = "apicurio"
	deployWithChaos = true
	deployWithMonitoring = false
	deployWithCertManager = false
	deployWithKyverno = false
	deployWithStrimzi = false
	deployWithKafkaConnect = true

	var executedCommands []string
	var mu sync.Mutex

	runExecFn = func(ctx context.Context, name string, args ...string) error {
		cmdStr := name + " " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		return nil
	}
	runExecStdinFn = func(ctx context.Context, name string, args []string, stdinData string) error {
		return nil
	}
	runHelmFn = func(ctx context.Context, args ...string) error {
		cmdStr := "helm " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		return nil
	}
	isHelmReleaseDeployedFn = func(ctx context.Context, release, namespace string) bool {
		return false
	}

	_ = deployCmd.Flags().Set("topology", deployTopology)
	err := runDeploy(deployCmd, []string{})
	if err != nil {
		t.Fatalf("runDeploy failed: %v", err)
	}

	foundKafka, foundKates, foundChaos, foundSchema, foundPostgres, foundKafkaConnect := false, false, false, false, false, false

	for _, cmd := range executedCommands {
		if strings.Contains(cmd, "helm upgrade --install krafter") {
			foundKafka = true
			if !strings.Contains(cmd, "-n kafka-sys") {
				t.Errorf("Expected Kafka to be deployed in kafka-sys namespace, got: %s", cmd)
			}
		}
		if strings.Contains(cmd, "helm upgrade --install connect-cluster") {
			foundKafkaConnect = true
			if !strings.Contains(cmd, "-n connect-sys") {
				t.Errorf("Expected Connect to be deployed in connect-sys namespace, got: %s", cmd)
			}
		}
		if strings.Contains(cmd, "helm upgrade --install kates") {
			foundKates = true
			if !strings.Contains(cmd, "-n app-sys") {
				t.Errorf("Expected Kates to be deployed in app-sys namespace, got: %s", cmd)
			}
		}
		if strings.Contains(cmd, "helm upgrade --install chaos") {
			foundChaos = true
			if !strings.Contains(cmd, "-n chaos-sys") {
				t.Errorf("Expected Chaos to be deployed in chaos-sys namespace, got: %s", cmd)
			}
		}
		if strings.Contains(cmd, "helm upgrade --install apicurio") {
			foundSchema = true
			if !strings.Contains(cmd, "-n kafka-sys") {
				t.Errorf("Expected Apicurio to be deployed in kafka-sys namespace, got: %s", cmd)
			}
		}
		if strings.Contains(cmd, "helm upgrade --install postgresql bitnami/postgresql") {
			foundPostgres = true
			if !strings.Contains(cmd, "-n database") {
				t.Errorf("Expected Postgres to be deployed in database namespace, got: %s", cmd)
			}
		}
	}

	if !foundKafka || !foundKates || !foundChaos || !foundSchema || !foundPostgres || !foundKafkaConnect {
		t.Errorf("Missing expected commands. Kafka: %v, Kates: %v, Chaos: %v, Schema: %v, Postgres: %v, Connect: %v", foundKafka, foundKates, foundChaos, foundSchema, foundPostgres, foundKafkaConnect)
	}
}

func TestDeployCommand_Idempotency(t *testing.T) {
	deployTopology = "single"
	deployNamespace = "test-ns"
	deployWithSchemaRegistry = "apicurio"
	deployWithChaos = true
	deployWithMonitoring = true
	deployWithCertManager = true
	deployWithKyverno = true
	deployWithStrimzi = true
	deployWithKafkaConnect = false

	var executedCommands []string
	var mu sync.Mutex

	runExecFn = func(ctx context.Context, name string, args ...string) error {
		cmdStr := name + " " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		return nil
	}
	runExecStdinFn = func(ctx context.Context, name string, args []string, stdinData string) error {
		return nil
	}
	runHelmFn = func(ctx context.Context, args ...string) error {
		cmdStr := "helm " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		return nil
	}
	isHelmReleaseDeployedFn = func(ctx context.Context, release, namespace string) bool {
		// Mock everything as already deployed
		return true
	}

	defaultExecutor = &MockExecutor{}

	_ = deployCmd.Flags().Set("topology", deployTopology)
	err := runDeploy(deployCmd, []string{})
	if err != nil {
		t.Fatalf("runDeploy failed: %v", err)
	}

	// Everything already deployed => no `helm upgrade`, with TWO deliberate
	// exceptions: the Strimzi operator and the Kates backend are always
	// reconciled.
	//
	// Skipping is not idempotency — `helm upgrade --install` converges, which is
	// the real thing. Skipping strimzi-operator when its release exists means
	// charts/strimzi-operator's pre-upgrade CRD hook never fires on an existing
	// cluster, so the CRDs freeze at whatever version first installed them while
	// the API server silently prunes fields the newer operator needs.
	//
	// The backend was skipped the same way, and the cost was worse: a release
	// record says a helm install once succeeded, not that anything runs. A
	// backend stuck in ImagePullBackOff satisfied the check, so deploy called
	// it done and no re-run could repair it — and a change in the values an
	// install should get never reached an existing release.
	reconciled := map[string]bool{}
	for _, cmd := range executedCommands {
		if !strings.Contains(cmd, "helm upgrade") {
			continue
		}
		switch {
		case strings.Contains(cmd, "strimzi-operator"):
			reconciled["strimzi-operator"] = true
		case strings.Contains(cmd, "--install kates charts/kates"):
			reconciled["kates"] = true
		default:
			t.Errorf("Expected no helm upgrade commands to run due to idempotency, got: %s", cmd)
		}
	}
	for _, want := range []string{"strimzi-operator", "kates"} {
		if !reconciled[want] {
			t.Errorf("Expected %s to be reconciled unconditionally, but no helm upgrade ran for it", want)
		}
	}
}

func TestDeployCommand_KafkaConnectParameters(t *testing.T) {
	// Save and restore global flags
	defer func() {
		deployTopology = "isolated"
		deployNamespace = "kates-stack"
		deployKafkaNS = "kafka"
		deployDbNS = "database"
		deployAppNS = "kates"
		deployChaosNS = "litmus"
		deployWithSchemaRegistry = "none"
		deployWithChaos = false
		deployWithMonitoring = false
		deployWithCertManager = false
		deployWithKyverno = false
		deployWithStrimzi = false
		deployWithKafkaConnect = false
	}()

	deployTopology = "isolated"
	deployKafkaNS = "kafka"
	deployDbNS = "database"
	deployAppNS = "kates"
	deployChaosNS = "litmus"
	deployWithSchemaRegistry = "none"
	deployWithChaos = false
	deployWithMonitoring = false
	deployWithCertManager = false
	deployWithKyverno = false
	deployWithStrimzi = false
	deployWithKafkaConnect = true

	var executedCommands []string
	var stdinCommands []string
	var mu sync.Mutex

	runExecFn = func(ctx context.Context, name string, args ...string) error {
		cmdStr := name + " " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		return nil
	}
	runExecStdinFn = func(ctx context.Context, name string, args []string, stdinData string) error {
		cmdStr := name + " " + strings.Join(args, " ")
		mu.Lock()
		stdinCommands = append(stdinCommands, cmdStr+"|"+stdinData)
		mu.Unlock()
		return nil
	}
	runHelmFn = func(ctx context.Context, args ...string) error {
		cmdStr := "helm " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		return nil
	}
	isHelmReleaseDeployedFn = func(ctx context.Context, release, namespace string) bool {
		return false
	}

	defaultExecutor = &MockExecutor{}

	_ = deployCmd.Flags().Set("topology", deployTopology)
	err := runDeploy(deployCmd, []string{})
	if err != nil {
		t.Fatalf("runDeploy failed: %v", err)
	}

	// 1. Verify connect-cluster helm command includes ALL connect-specific sets
	foundKafkaConnect := false
	for _, cmd := range executedCommands {
		if !strings.Contains(cmd, "helm upgrade --install connect-cluster") {
			continue
		}
		foundKafkaConnect = true

		connectSets := []string{
			"schemaRegistry.enabled=true",
			"networkPolicy.egress.databases[0].namespace=database",
		}
		for _, expected := range connectSets {
			if !strings.Contains(cmd, expected) {
				t.Errorf("Expected Connect helm command to include %q, got: %s", expected, cmd)
			}
		}
	}
	if !foundKafkaConnect {
		t.Error("Connect deployment command (connect-cluster) was not executed")
	}

	// 2. Verify PostgreSQL is deployed with the correct namespace (database)
	foundPostgres := false
	for _, cmd := range executedCommands {
		if strings.Contains(cmd, "helm upgrade --install postgresql bitnami/postgresql") {
			foundPostgres = true
			if !strings.Contains(cmd, "-n database") {
				t.Errorf("Expected PostgreSQL to be deployed in 'database' namespace, got: %s", cmd)
			}
		}
	}
	if !foundPostgres {
		t.Error("PostgreSQL deployment command was not executed")
	}

	// 3. Verify the connect-pg-credentials Secret creation kubectl command is invoked
	foundPgSecret := false
	for _, cmd := range stdinCommands {
		if strings.Contains(cmd, "kubectl") && strings.Contains(cmd, "connect-pg-credentials") {
			foundPgSecret = true
		}
	}
	if !foundPgSecret {
		t.Error("connect-pg-credentials Secret creation command was not invoked")
	}
}

func TestDeployCommand_KafkaConnectIdempotent(t *testing.T) {
	// Save and restore global flags
	defer func() {
		deployTopology = "isolated"
		deployNamespace = "kates-stack"
		deployKafkaNS = "kafka"
		deployDbNS = "database"
		deployAppNS = "kates"
		deployChaosNS = "litmus"
		deployWithSchemaRegistry = "none"
		deployWithChaos = false
		deployWithMonitoring = false
		deployWithCertManager = false
		deployWithKyverno = false
		deployWithStrimzi = false
		deployWithKafkaConnect = false
	}()

	deployTopology = "isolated"
	deployKafkaNS = "kafka"
	deployDbNS = "database"
	deployAppNS = "kates"
	deployChaosNS = "litmus"
	deployWithSchemaRegistry = "none"
	deployWithChaos = false
	deployWithMonitoring = false
	deployWithCertManager = false
	deployWithKyverno = false
	deployWithStrimzi = false
	deployWithKafkaConnect = true

	var executedCommands []string
	var stdinCommands []string
	var mu sync.Mutex

	runExecFn = func(ctx context.Context, name string, args ...string) error {
		cmdStr := name + " " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		return nil
	}
	runExecStdinFn = func(ctx context.Context, name string, args []string, stdinData string) error {
		cmdStr := name + " " + strings.Join(args, " ")
		mu.Lock()
		stdinCommands = append(stdinCommands, cmdStr+"|"+stdinData)
		mu.Unlock()
		return nil
	}
	runHelmFn = func(ctx context.Context, args ...string) error {
		cmdStr := "helm " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		return nil
	}
	isHelmReleaseDeployedFn = func(ctx context.Context, release, namespace string) bool {
		// All releases are already deployed
		return true
	}

	defaultExecutor = &MockExecutor{}

	_ = deployCmd.Flags().Set("topology", deployTopology)
	err := runDeploy(deployCmd, []string{})
	if err != nil {
		t.Fatalf("runDeploy failed: %v", err)
	}

	// When everything is already deployed, no helm upgrade should be called —
	// except for the two components that are reconciled unconditionally
	// because a release record is not evidence that anything is running (see
	// TestDeployCommand_Idempotency).
	for _, cmd := range executedCommands {
		if !strings.Contains(cmd, "helm upgrade") {
			continue
		}
		if strings.Contains(cmd, "strimzi-operator") || strings.Contains(cmd, "--install kates charts/kates") {
			continue
		}
		t.Errorf("Expected no helm upgrade commands due to idempotency, got: %s", cmd)
	}

	// Specifically, PostgreSQL should NOT be deployed
	for _, cmd := range executedCommands {
		if strings.Contains(cmd, "helm upgrade --install postgresql") {
			t.Error("PostgreSQL should not be redeployed when already deployed (idempotency)")
		}
	}

	// The connect-pg-credentials secret should still be created (it's applied idempotently via kubectl apply)
	// but no helm upgrade for kafka/postgresql should run
	for _, cmd := range executedCommands {
		if strings.Contains(cmd, "helm upgrade --install krafter") {
			t.Error("Kafka chart should not be redeployed when already deployed (idempotency)")
		}
	}
}

// threeZoneNodesJSON is a node list whose zones make the inter-AZ latency
// probe apply.
const threeZoneNodesJSON = `{"items":[
  {"metadata":{"name":"node-a","labels":{"topology.kubernetes.io/zone":"alpha"}},"status":{"allocatable":{"cpu":"4","memory":"16Gi"}}},
  {"metadata":{"name":"node-b","labels":{"topology.kubernetes.io/zone":"beta"}},"status":{"allocatable":{"cpu":"4","memory":"16Gi"}}},
  {"metadata":{"name":"node-c","labels":{"topology.kubernetes.io/zone":"gamma"}},"status":{"allocatable":{"cpu":"4","memory":"16Gi"}}}
]}`

// recordingExecutor is a three-zone cluster where every command succeeds,
// and it keeps each command. A write probe that runs against it gets far
// enough to be seen: its namespace is "created", and no running pod is
// found, so the cluster-domain lookup reaches its dns-detect pod.
type recordingExecutor struct {
	mu    sync.Mutex
	calls []string
}

func (e *recordingExecutor) LookPath(file string) (string, error) {
	return "/usr/local/bin/" + file, nil
}

func (e *recordingExecutor) Exec(name string, args ...string) (string, error) {
	e.mu.Lock()
	e.calls = append(e.calls, name+" "+strings.Join(args, " "))
	e.mu.Unlock()
	// HasPrefix: a deploy appends --context to every kubectl call.
	if name == "kubectl" && strings.HasPrefix(strings.Join(args, " "), "get nodes -o json") {
		return threeZoneNodesJSON, nil
	}
	return "", nil
}

func (e *recordingExecutor) commands() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.calls...)
}

// clusterWrite matches a kubectl or helm verb that creates, changes or
// deletes something, wherever the command sits (the storage bench applies
// its PVC through `sh -c`).
var clusterWrite = regexp.MustCompile(`\b(kubectl\s+(create|run|label|annotate|delete|apply|patch|replace|scale|set|expose|taint|cordon|drain|edit)|helm\s+(install|upgrade|uninstall|delete|rollback))\b`)

// kates deploy --dry-run previews; it must not write to the cluster to do
// it. Introspection used to: its second stage created a namespace and a
// Secret to audit Secret creation, prober pods in every zone, and a
// dns-detect pod when no running pod could be read.
func TestDeployDryRun_WritesNothingToTheCluster(t *testing.T) {
	origExec, origStdin, origHelm := runExecFn, runExecStdinFn, runHelmFn
	origOutput, origCombined := runExecOutputFn, runExecCombinedFn
	origDeployed, origExecutor := isHelmReleaseDeployedFn, defaultExecutor
	t.Cleanup(func() {
		runExecFn, runExecStdinFn, runHelmFn = origExec, origStdin, origHelm
		runExecOutputFn, runExecCombinedFn = origOutput, origCombined
		isHelmReleaseDeployedFn, defaultExecutor = origDeployed, origExecutor
	})

	// runExecFn, runExecStdinFn and runHelmFn are deploy's write paths: a
	// dry run has no business in any of them. runExecOutputFn and
	// runExecCombinedFn are its reads.
	var mu sync.Mutex
	var writes, reads []string
	record := func(to *[]string, name string, args []string) {
		mu.Lock()
		*to = append(*to, name+" "+strings.Join(args, " "))
		mu.Unlock()
	}
	runExecFn = func(_ context.Context, name string, args ...string) error {
		record(&writes, name, args)
		return nil
	}
	runExecStdinFn = func(_ context.Context, name string, args []string, _ string) error {
		record(&writes, name, args)
		return nil
	}
	runHelmFn = func(_ context.Context, args ...string) error {
		record(&writes, "helm", args)
		return nil
	}
	runExecOutputFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		record(&reads, name, args)
		return stubClusterRead(ctx, name, args...)
	}
	runExecCombinedFn = runExecOutputFn
	isHelmReleaseDeployedFn = func(context.Context, string, string) bool { return false }
	detectExec := &recordingExecutor{}
	defaultExecutor = detectExec

	setDeployFlags(t, func() {
		deployTopology, deployNamespace = "single", "kates-test"
		deployWithSchemaRegistry = "apicurio"
		deployWithChaos, deployWithMonitoring, deployWithCertManager = true, true, true
		deployWithKyverno, deployWithStrimzi, deployWithKafkaConnect = false, true, false
		deployWithKafkaUI, deployWithMirrorMaker2, deployDryRun = true, false, true
	})
	// The dry run still writes .build/values-detected.yaml; keep it out of
	// the package directory.
	t.Chdir(t.TempDir())

	var err error
	out := captureStdout(t, func() { err = runDeploy(deployCmd, []string{}) })
	if err != nil {
		t.Fatalf("runDeploy --dry-run: %v\n%s", err, out)
	}

	introspection := detectExec.commands()
	// A dry run introspects the cluster the gate chose, by name.
	if len(introspection) == 0 || !sliceContains(introspection, "kubectl get nodes -o json --context=test-context") {
		t.Fatalf("introspection never ran, so nothing was checked; commands: %v", introspection)
	}
	for _, c := range writes {
		t.Errorf("a dry run took a write path: %s", c)
	}
	for _, c := range append(introspection, reads...) {
		if clusterWrite.MatchString(c) {
			t.Errorf("a dry run wrote to the cluster: %s", c)
		}
	}
	if _, err := os.Stat(".build/values-detected.yaml"); err != nil {
		t.Errorf("the dry run should still write its values file: %v", err)
	}
	if !strings.Contains(out, "Deployment plan") {
		t.Errorf("no preview was printed:\n%s", out)
	}
}

// TestRunDeploy_PinsEveryCallToTheChosenCluster runs a deploy with every
// component on and records each kubectl and helm call at every seam a call can
// take: the exec seams, helm, the version resolution's runner, and the
// introspection's executor. Partway through, kubectl's current context moves
// to another cluster, as it does when `kind create cluster` runs in another
// terminal. Every call must still resolve to the cluster the gate chose
// ("test-context", from the stub in init), because each one names it.
func TestRunDeploy_PinsEveryCallToTheChosenCluster(t *testing.T) {
	origExec, origStdin, origHelm := runExecFn, runExecStdinFn, runHelmFn
	origOutput, origCombined, origDeployed := runExecOutputFn, runExecCombinedFn, isHelmReleaseDeployedFn
	origProc, origExecutor, origVersions := runProcFn, defaultExecutor, resolveVersionPlanFn
	t.Cleanup(func() {
		runExecFn, runExecStdinFn, runHelmFn = origExec, origStdin, origHelm
		runExecOutputFn, runExecCombinedFn, isHelmReleaseDeployedFn = origOutput, origCombined, origDeployed
		runProcFn, defaultExecutor, resolveVersionPlanFn = origProc, origExecutor, origVersions
	})
	setDeployFlags(t, func() {
		deployTopology, deployWithSchemaRegistry = "isolated", "apicurio"
		deployWithChaos, deployWithMonitoring, deployWithCertManager = true, true, true
		deployWithKyverno, deployWithStrimzi, deployWithKafkaConnect = true, true, true
		deployWithKafkaUI, deployWithMirrorMaker2, deployDryRun = true, true, false
	})
	t.Chdir(t.TempDir()) // .build/values-detected.yaml

	type call struct {
		seam, name string
		args       []string
	}
	var (
		mu      sync.Mutex
		calls   []call
		current = "test-context"
	)
	record := func(seam, name string, args []string) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, call{seam, name, append([]string(nil), args...)})
		// The first chart install is the moment the current context moves.
		if name == "helm" && len(args) > 0 && args[0] == "upgrade" {
			current = "kind-other"
		}
	}

	runExecFn = func(_ context.Context, name string, args ...string) error {
		record("exec", name, args)
		return nil
	}
	runExecStdinFn = func(_ context.Context, name string, args []string, _ string) error {
		record("stdin", name, args)
		return nil
	}
	runHelmFn = func(_ context.Context, args ...string) error {
		record("helm", "helm", args)
		return nil
	}
	read := func(seam string) func(context.Context, string, ...string) ([]byte, error) {
		return func(ctx context.Context, name string, args ...string) ([]byte, error) {
			record(seam, name, args)
			// cert-manager waits for its webhooks' CA bundles.
			if strings.Contains(strings.Join(args, " "), "caBundle") {
				return []byte("LS0t"), nil
			}
			return stubClusterRead(ctx, name, args...)
		}
	}
	runExecOutputFn, runExecCombinedFn = read("output"), read("combined")
	// The real check, so the release lookups go through the output seam.
	isHelmReleaseDeployedFn = isHelmReleaseDeployedDefault
	runProcFn = func(_ context.Context, _, name string, args ...string) (string, error) {
		record("runner", name, args)
		return "", nil
	}
	resolveVersionPlanFn = func(ctx context.Context, r strimzi.Runner, o versionOptions) (*versionPlan, error) {
		// What the real resolution asks the cluster first.
		_, _ = r.Run(ctx, "kubectl", "get", "kafka", "-A", "-o", "json")
		return stubVersionPlan(o), nil
	}
	introspection := &recordingExecutor{}
	defaultExecutor = introspection

	if err := runDeploy(deployCmd, []string{}); err != nil {
		t.Fatalf("runDeploy: %v", err)
	}
	for _, line := range introspection.commands() {
		fields := strings.Fields(line)
		record("introspection", fields[0], fields[1:])
	}

	// clusterOf is where kubectl or helm sends a call: the context its flag
	// names, else kubectl's current context.
	clusterOf := func(c call) (string, int) {
		flag := kubeContextFlag(c.name) + "="
		cluster, n := current, 0
		for _, a := range c.args {
			if a == "--" {
				break
			}
			if strings.HasPrefix(a, flag) {
				cluster, n = strings.TrimPrefix(a, flag), n+1
			}
		}
		return cluster, n
	}

	seams := map[string]int{}
	var all []string
	for _, c := range calls {
		if kubeContextFlag(c.name) == "" {
			continue
		}
		// kubectl config reads and writes the kubeconfig, not a cluster: the
		// gate's own look at the current context, before anything is pinned.
		if c.name == "kubectl" && len(c.args) > 0 && c.args[0] == "config" {
			continue
		}
		seams[c.seam]++
		line := c.seam + ": " + c.name + " " + strings.Join(c.args, " ")
		all = append(all, line)
		if cluster, n := clusterOf(c); cluster != "test-context" || n != 1 {
			t.Errorf("went to %q (%d context flags), want test-context once: %s", cluster, n, line)
		}
	}
	if current != "kind-other" {
		t.Fatal("the current context never moved, so the test proves nothing")
	}

	// The paths the fix has to reach, so the loop above checked them.
	for _, seam := range []string{"exec", "stdin", "helm", "output", "combined", "runner", "introspection"} {
		if seams[seam] == 0 {
			t.Errorf("no kubectl or helm call went through the %s seam", seam)
		}
	}
	joined := strings.Join(all, "\n")
	for _, want := range []string{
		"helm status",                            // the release lookups
		"helm upgrade --install monitoring",      // monitoring
		"helm upgrade kyverno kyverno/kyverno",   // Kyverno's scrape, after monitoring
		"helm upgrade --install connect-cluster", // Kafka Connect
		"kubectl get kafkaconnector",             // its connectors
		"kubectl get secret kates-api-key",       // the API key stored after the deploy
		"kubectl get daemonset kindnet",          // the kind check before introspection
		"introspection: kubectl cluster-info",    // introspection
		"runner: kubectl get kafka -A",           // version resolution
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("no call matching %q was recorded", want)
		}
	}
	if t.Failed() {
		t.Logf("calls:\n%s", joined)
	}
}
