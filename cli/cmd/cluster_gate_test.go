package cmd

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/bmscomp/kates/cli/pkg/cluster"
	"github.com/bmscomp/kates/cli/pkg/detect"
)

// stubGate replaces every seam and restores them on cleanup, so each test states
// only the state it cares about and cannot leak into the next.
type stubGate struct {
	contexts   []cluster.Context
	listErr    error
	env        cluster.Environment
	tty        bool
	confirm    bool
	confirmErr error
	picked     string
	pickErr    error

	// kindExists simulates a pre-existing kind cluster. Left false, tests are
	// independent of whatever clusters the developer's machine happens to have.
	kindExists bool
	// configMissing simulates running outside the repo, where the kind topology
	// config is not readable.
	configMissing bool

	createCalled  bool
	createErr     error
	pickCalled    bool
	confirmCalled bool
}

func (s *stubGate) install(t *testing.T) {
	t.Helper()

	origList, origReach, origProbe, origCreate := listContextsFn, checkAllReachableFn, probeEnvironmentFn, createKindFn
	origInteractive, origConfirm, origPick := interactiveFn, confirmFn, pickContextFn
	origExists := kindClusterExistsFn

	listContextsFn = func(detect.CommandExecutor) ([]cluster.Context, error) {
		return s.contexts, s.listErr
	}
	// Reachability is decided by the fixture, not probed.
	checkAllReachableFn = func(_ detect.CommandExecutor, c []cluster.Context) []cluster.Context { return c }
	probeEnvironmentFn = func(detect.CommandExecutor) cluster.Environment { return s.env }
	createKindFn = func(_ detect.CommandExecutor, o cluster.KindOptions) error {
		s.createCalled = true
		if o.Progress != nil {
			o.Progress("stub")
		}
		return s.createErr
	}
	interactiveFn = func() bool { return s.tty }
	confirmFn = func(string) (bool, error) {
		s.confirmCalled = true
		return s.confirm, s.confirmErr
	}
	pickContextFn = func([]cluster.Context) (string, error) {
		s.pickCalled = true
		return s.picked, s.pickErr
	}
	kindClusterExistsFn = func(detect.CommandExecutor, string) bool { return s.kindExists }
	origConfig := configExistsFn
	configExistsFn = func(string) bool { return !s.configMissing }

	t.Cleanup(func() {
		listContextsFn, checkAllReachableFn, probeEnvironmentFn, createKindFn = origList, origReach, origProbe, origCreate
		interactiveFn, confirmFn, pickContextFn = origInteractive, origConfirm, origPick
		kindClusterExistsFn = origExists
		configExistsFn = origConfig
	})
}

func reachable(name string) cluster.Context {
	return cluster.Context{Name: name, Reachable: true, Probed: true}
}

// reachableCurrent is a reachable context that kubectl already points at.
func reachableCurrent(name string) cluster.Context {
	c := reachable(name)
	c.Current = true
	return c
}
func unreachable(name string) cluster.Context {
	return cluster.Context{Name: name, Reachable: false, Probed: true}
}

var dockerReady = cluster.Environment{Docker: cluster.DockerRunning, KindInstalled: true}

// ── The wiring ─────────────────────────────────────────────────────────────

// The gate must actually gate: if it fails, deploy stops before touching
// anything. Without this, deploy_test.go's stub could mask a broken wiring.
func TestRunDeploy_AbortsWhenClusterGateFails(t *testing.T) {
	orig := resolveClusterFn
	t.Cleanup(func() { resolveClusterFn = orig })

	var helmCalled bool
	origHelm := runHelmFn
	runHelmFn = func(_ context.Context, _ ...string) error { helmCalled = true; return nil }
	t.Cleanup(func() { runHelmFn = origHelm })

	resolveClusterFn = func() (string, error) {
		return "", errors.New("no reachable Kubernetes cluster")
	}

	err := runDeploy(deployCmd, []string{})
	if err == nil {
		t.Fatal("deploy must abort when no cluster can be resolved")
	}
	if !strings.Contains(err.Error(), "no reachable") {
		t.Errorf("the gate's reason must reach the user, got: %v", err)
	}
	if helmCalled {
		t.Error("deploy must not run helm when there is no cluster to deploy to")
	}
}

// fakeKubeconfig stands in for kubectl's current context. `kubectl config
// use-context` moves it, and every command records where it pointed when the
// command ran — which is where a real helm or kubectl call would have gone.
type fakeKubeconfig struct {
	mu      sync.Mutex
	current string
	ran     []targetedCmd
}

type targetedCmd struct{ cmd, context string }

func (k *fakeKubeconfig) run(name string, args ...string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.ran = append(k.ran, targetedCmd{name + " " + strings.Join(args, " "), k.current})
	if name == "kubectl" && len(args) == 3 && args[0] == "config" && args[1] == "use-context" {
		k.current = args[2]
	}
}

// install routes cluster writes and the current-context read through k; every
// other read gets the package's canned answers.
func (k *fakeKubeconfig) install(t *testing.T) {
	t.Helper()
	origOut, origExec := runExecOutputFn, runExecFn
	t.Cleanup(func() { runExecOutputFn, runExecFn = origOut, origExec })

	runExecOutputFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "kubectl" && strings.Join(args, " ") == "config current-context" {
			k.mu.Lock()
			defer k.mu.Unlock()
			if k.current == "" {
				return nil, errors.New("error: current-context is not set")
			}
			return []byte(k.current + "\n"), nil
		}
		return stubClusterRead(ctx, name, args...)
	}
	runExecFn = func(_ context.Context, name string, args ...string) error {
		k.run(name, args...)
		return nil
	}
}

func (k *fakeKubeconfig) commands() []targetedCmd {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]targetedCmd(nil), k.ran...)
}

// The picker's answer must decide where the deploy goes. It used to be thrown
// away: runDeploy discarded the gate's context name, and every helm and kubectl
// call went to kubectl's current context — so picking another cluster in the
// picker still deployed into the one kubectl pointed at.
func TestRunDeploy_DeploysToThePickedCluster(t *testing.T) {
	s := &stubGate{
		contexts: []cluster.Context{reachableCurrent("kind-panda"), reachable("prod-eu")},
		env:      dockerReady, tty: true, picked: "prod-eu",
	}
	s.install(t)
	k := &fakeKubeconfig{current: "kind-panda"}
	k.install(t)

	origGate, origStdin, origHelm := resolveClusterFn, runExecStdinFn, runHelmFn
	origDeployed, origExecutor := isHelmReleaseDeployedFn, defaultExecutor
	t.Cleanup(func() {
		resolveClusterFn, runExecStdinFn, runHelmFn = origGate, origStdin, origHelm
		isHelmReleaseDeployedFn, defaultExecutor = origDeployed, origExecutor
	})
	resolveClusterFn = resolveCluster // the real gate, with the stubbed picker
	runExecStdinFn = func(_ context.Context, name string, args []string, _ string) error {
		k.run(name, args...)
		return nil
	}
	runHelmFn = func(_ context.Context, args ...string) error {
		k.run("helm", args...)
		return nil
	}
	isHelmReleaseDeployedFn = func(context.Context, string, string) bool { return false }
	defaultExecutor = &MockExecutor{}

	setDeployFlags(t, func() {
		deployTopology, deployNamespace = "single", "kates-test"
		deployWithSchemaRegistry = "none"
		deployWithChaos, deployWithMonitoring, deployWithCertManager = false, false, false
		deployWithKyverno, deployWithStrimzi, deployWithKafkaConnect = false, false, false
		deployWithKafkaUI, deployWithMirrorMaker2, deployDryRun = false, false, false
	})

	var err error
	out := captureStdout(t, func() { err = runDeploy(deployCmd, []string{}) })
	if err != nil {
		t.Fatalf("runDeploy: %v", err)
	}
	if !s.pickCalled {
		t.Fatal("two reachable clusters must present the picker")
	}

	var helm int
	for _, c := range k.commands() {
		if !strings.HasPrefix(c.cmd, "helm ") {
			continue
		}
		helm++
		if c.context != "prod-eu" {
			t.Errorf("%q ran against %q, want the picked prod-eu", c.cmd, c.context)
		}
	}
	if helm == 0 {
		t.Fatal("the deploy ran no helm commands; nothing was checked")
	}
	if k.current != "prod-eu" {
		t.Errorf("kubectl is left pointing at %q, want prod-eu, the cluster just deployed to", k.current)
	}

	// The banner names the cluster deployed to, not the one kubectl pointed
	// at before the picker.
	var banner string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "kates deploy") {
			banner = line
			break
		}
	}
	if !strings.Contains(banner, "prod-eu") || strings.Contains(banner, "kind-panda") {
		t.Errorf("banner should name prod-eu, got %q", banner)
	}
	// Switching kubectl outlives the deploy, so it has to be said, with the
	// way back.
	if !strings.Contains(out, "kubectl config use-context kind-panda") {
		t.Errorf("the switch should be announced with the command to undo it; output:\n%s", out)
	}
}

// setDeployFlags sets deploy's flag variables for one test and restores them
// after, so this file's runDeploy cannot change what deploy_test.go runs.
func setDeployFlags(t *testing.T, set func()) {
	t.Helper()
	topology, namespace, registry := deployTopology, deployNamespace, deployWithSchemaRegistry
	chaos, monitoring, certManager := deployWithChaos, deployWithMonitoring, deployWithCertManager
	kyverno, strimzi, connect := deployWithKyverno, deployWithStrimzi, deployWithKafkaConnect
	ui, mm2, dry := deployWithKafkaUI, deployWithMirrorMaker2, deployDryRun
	t.Cleanup(func() {
		deployTopology, deployNamespace, deployWithSchemaRegistry = topology, namespace, registry
		deployWithChaos, deployWithMonitoring, deployWithCertManager = chaos, monitoring, certManager
		deployWithKyverno, deployWithStrimzi, deployWithKafkaConnect = kyverno, strimzi, connect
		deployWithKafkaUI, deployWithMirrorMaker2, deployDryRun = ui, mm2, dry
	})
	set()
}

// ── Pointing kubectl at the target ─────────────────────────────────────────

func TestUseTargetContext_SwitchesAndSaysHowToGoBack(t *testing.T) {
	k := &fakeKubeconfig{current: "kind-panda"}
	k.install(t)

	var err error
	out := captureStdout(t, func() { err = useTargetContext("prod-eu") })
	if err != nil {
		t.Fatalf("useTargetContext: %v", err)
	}
	if k.current != "prod-eu" {
		t.Errorf("kubectl points at %q, want prod-eu", k.current)
	}
	if !strings.Contains(out, "kubectl config use-context kind-panda") {
		t.Errorf("should say how to switch back, got:\n%s", out)
	}
}

// The common case — the cluster chosen is the one kubectl points at — must
// not touch the kubeconfig.
func TestUseTargetContext_LeavesTheCurrentContextAlone(t *testing.T) {
	k := &fakeKubeconfig{current: "kind-panda"}
	k.install(t)

	if err := useTargetContext("kind-panda"); err != nil {
		t.Fatalf("useTargetContext: %v", err)
	}
	if ran := k.commands(); len(ran) != 0 {
		t.Errorf("nothing to switch, but ran %v", ran)
	}
}

func TestUseTargetContext_NoCurrentContextStillSwitches(t *testing.T) {
	k := &fakeKubeconfig{}
	k.install(t)

	var err error
	captureStdout(t, func() { err = useTargetContext("prod-eu") })
	if err != nil {
		t.Fatalf("useTargetContext: %v", err)
	}
	if k.current != "prod-eu" {
		t.Errorf("kubectl points at %q, want prod-eu", k.current)
	}
}

// A switch outlives the run, and a dry run changes nothing. Planning against
// the current context instead would preview the wrong cluster, so it refuses.
func TestUseTargetContext_DryRunRefusesToSwitch(t *testing.T) {
	origDry := deployDryRun
	deployDryRun = true
	t.Cleanup(func() { deployDryRun = origDry })

	k := &fakeKubeconfig{current: "kind-panda"}
	k.install(t)

	err := useTargetContext("prod-eu")
	if err == nil {
		t.Fatal("a dry run must not switch kubectl to another cluster")
	}
	if !strings.Contains(err.Error(), "kubectl config use-context prod-eu") {
		t.Errorf("error should give the command, got: %v", err)
	}
	if ran := k.commands(); len(ran) != 0 {
		t.Errorf("a dry run ran %v", ran)
	}
}

// ── The happy path ─────────────────────────────────────────────────────────

func TestResolveCluster_SingleContextNeverPrompts(t *testing.T) {
	s := &stubGate{contexts: []cluster.Context{reachableCurrent("kind-panda")}, env: dockerReady, tty: true}
	s.install(t)

	got, err := resolveCluster()
	if err != nil {
		t.Fatalf("resolveCluster: %v", err)
	}
	if got != "kind-panda" {
		t.Errorf("got %q, want kind-panda", got)
	}
	// The 90% case: one cluster, no question asked.
	if s.pickCalled || s.confirmCalled {
		t.Error("a single reachable cluster must not prompt")
	}
}

func TestResolveCluster_MultipleContextsPrompt(t *testing.T) {
	s := &stubGate{
		contexts: []cluster.Context{reachable("kind-panda"), reachable("prod-eu")},
		env:      dockerReady, tty: true, picked: "prod-eu",
	}
	s.install(t)

	got, err := resolveCluster()
	if err != nil {
		t.Fatalf("resolveCluster: %v", err)
	}
	if !s.pickCalled {
		t.Error("several reachable clusters must present the picker")
	}
	if got != "prod-eu" {
		t.Errorf("got %q, want the picked context", got)
	}
}

// ── The only reachable cluster is not the current one ──────────────────────

// kubectl points at a cluster that did not answer, and one other cluster did.
// That one is what is left, not what anyone chose: with a VPN down it can be
// production. Deploying there switches kubectl to it, so the gate asks.
func onlyReachableElsewhere() []cluster.Context {
	return []cluster.Context{
		{Name: "kind-panda", Current: true, Probed: true},
		reachable("prod-eu"),
	}
}

func TestResolveCluster_OnlyReachableButNotCurrentAsks(t *testing.T) {
	s := &stubGate{contexts: onlyReachableElsewhere(), env: dockerReady, tty: true, confirm: true}
	s.install(t)

	got, err := resolveCluster()
	if err != nil {
		t.Fatalf("resolveCluster: %v", err)
	}
	if !s.confirmCalled {
		t.Error("must ask before deploying to a cluster kubectl does not point at")
	}
	if got != "prod-eu" {
		t.Errorf("got %q, want prod-eu", got)
	}
}

func TestResolveCluster_OnlyReachableButNotCurrentDeclined(t *testing.T) {
	s := &stubGate{contexts: onlyReachableElsewhere(), env: dockerReady, tty: true, confirm: false}
	s.install(t)

	if _, err := resolveCluster(); err == nil {
		t.Fatal("declining must stop the deploy")
	}
}

func TestResolveCluster_OnlyReachableButNotCurrentNonTTYFails(t *testing.T) {
	s := &stubGate{contexts: onlyReachableElsewhere(), env: dockerReady, tty: false}
	s.install(t)

	_, err := resolveCluster()
	if err == nil {
		t.Fatal("must fail rather than switch clusters without asking")
	}
	if s.confirmCalled {
		t.Error("must not prompt without a terminal — it would hang")
	}
	for _, want := range []string{"kind-panda", "kubectl config use-context prod-eu"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

// ── The non-TTY guard: the load-bearing safety property ────────────────────

// Ambiguity without a terminal must FAIL, never guess. Silently picking is how
// you deploy to prod by accident.
func TestResolveCluster_MultipleContextsNonTTYFailsRatherThanGuessing(t *testing.T) {
	s := &stubGate{
		contexts: []cluster.Context{reachable("kind-panda"), reachable("prod-eu")},
		env:      dockerReady, tty: false,
	}
	s.install(t)

	_, err := resolveCluster()
	if err == nil {
		t.Fatal("must fail when several clusters are reachable and we cannot ask")
	}
	if s.pickCalled {
		t.Error("must not launch a TUI without a terminal — it would hang")
	}
	// The error has to tell the user how to resolve it.
	if !strings.Contains(err.Error(), "use-context") {
		t.Errorf("error should say how to choose, got: %v", err)
	}
}

func TestResolveCluster_OfferKindNonTTYFailsWithoutPrompting(t *testing.T) {
	s := &stubGate{contexts: nil, env: dockerReady, tty: false}
	s.install(t)

	_, err := resolveCluster()
	if err == nil {
		t.Fatal("must fail when there is no cluster and we cannot ask")
	}
	if s.confirmCalled {
		t.Error("must not prompt without a terminal — it would hang")
	}
	if s.createCalled {
		t.Error("must never create a cluster without explicit consent")
	}
	// It should still tell the user what it would have done.
	if !strings.Contains(err.Error(), "kind create cluster") {
		t.Errorf("error should give the manual command, got: %v", err)
	}
}

// IsInteractive is the single guard everything else relies on.
func TestIsInteractive_FalseUnderTestYesAndPlain(t *testing.T) {
	origTesting, origYes, origPlain := isTesting, deployYes, plainOutput
	t.Cleanup(func() { isTesting, deployYes, plainOutput = origTesting, origYes, origPlain })

	isTesting, deployYes, plainOutput = true, false, false
	if IsInteractive() {
		t.Error("isTesting must disable prompting — deploy_test.go calls runDeploy directly")
	}

	isTesting, deployYes, plainOutput = false, true, false
	if IsInteractive() {
		t.Error("--yes must disable prompting")
	}

	// --plain is a statement that a machine is reading the output; forms and
	// TUIs have no place in it.
	isTesting, deployYes, plainOutput = false, false, true
	if IsInteractive() {
		t.Error("--plain must disable prompting")
	}
}

// TERM=dumb is the terminal declaring it cannot do cursor addressing.
func TestIsInteractive_FalseOnDumbTerm(t *testing.T) {
	origTesting, origYes, origPlain := isTesting, deployYes, plainOutput
	t.Cleanup(func() { isTesting, deployYes, plainOutput = origTesting, origYes, origPlain })
	isTesting, deployYes, plainOutput = false, false, false
	t.Setenv("TERM", "dumb")

	if interactiveAllowed() {
		t.Error("TERM=dumb must disable prompting")
	}
}

// ── The kind offer ─────────────────────────────────────────────────────────

func TestResolveCluster_OfferKindAcceptedCreates(t *testing.T) {
	s := &stubGate{contexts: nil, env: dockerReady, tty: true, confirm: true}
	s.install(t)

	got, err := resolveCluster()
	if err != nil {
		t.Fatalf("resolveCluster: %v", err)
	}
	if !s.confirmCalled {
		t.Error("must ask before creating a cluster")
	}
	if !s.createCalled {
		t.Error("accepting must create the cluster")
	}
	// kind names the context kind-<name>; deploying to the wrong name would fail.
	if got != "kind-"+cluster.DefaultKindClusterName {
		t.Errorf("got %q, want kind-%s", got, cluster.DefaultKindClusterName)
	}
}

func TestResolveCluster_OfferKindDeclinedCreatesNothing(t *testing.T) {
	s := &stubGate{contexts: nil, env: dockerReady, tty: true, confirm: false}
	s.install(t)

	_, err := resolveCluster()
	if err == nil {
		t.Fatal("declining leaves no cluster to deploy to — must error")
	}
	if s.createCalled {
		t.Error("declining must not create anything")
	}
}

func TestResolveCluster_KindCreateFailurePropagates(t *testing.T) {
	s := &stubGate{
		contexts: nil, env: dockerReady, tty: true, confirm: true,
		createErr: errors.New("port 6443 already in use"),
	}
	s.install(t)

	_, err := resolveCluster()
	if err == nil {
		t.Fatal("want error when creation fails")
	}
	if !strings.Contains(err.Error(), "already in use") {
		t.Errorf("underlying cause must survive, got: %v", err)
	}
}

// A kind cluster that exists but does not answer is a broken cluster, not an
// absent one. Recreating would silently destroy it, so we stop and let a human
// decide.
func TestResolveCluster_ExistingUnreachableKindIsNotClobbered(t *testing.T) {
	s := &stubGate{
		contexts: nil, env: dockerReady, tty: true, confirm: true,
		kindExists: true,
	}
	s.install(t)

	_, err := resolveCluster()
	if err == nil {
		t.Fatal("want an error rather than silently recreating a broken cluster")
	}
	if s.createCalled {
		t.Error("must NOT recreate over an existing kind cluster — that destroys it")
	}
	if !strings.Contains(err.Error(), "kind delete cluster") {
		t.Errorf("error should offer the way out, got: %v", err)
	}
}

// A plan preview must not build real infrastructure. --dry-run is checked far
// later in runDeploy, so the gate has to refuse on its own.
func TestResolveCluster_DryRunNeverCreatesACluster(t *testing.T) {
	origDry := deployDryRun
	deployDryRun = true
	t.Cleanup(func() { deployDryRun = origDry })

	s := &stubGate{contexts: nil, env: dockerReady, tty: true, confirm: true}
	s.install(t)

	_, err := resolveCluster()
	if err == nil {
		t.Fatal("a dry run with no cluster should report, not proceed")
	}
	if s.createCalled {
		t.Error("--dry-run must NEVER create a kind cluster")
	}
	if s.confirmCalled {
		t.Error("--dry-run must not even ask — there is nothing to consent to")
	}
}

// The kind topology config is repo-relative, so an installed binary run from
// elsewhere cannot read it. Offering anyway fails inside `kind create` with a
// bare "no such file".
func TestResolveCluster_MissingTopologyConfigIsExplained(t *testing.T) {
	s := &stubGate{
		contexts: nil, env: dockerReady, tty: true, confirm: true,
		configMissing: true,
	}
	s.install(t)

	_, err := resolveCluster()
	if err == nil {
		t.Fatal("want an error when the topology config is unreadable")
	}
	if s.createCalled {
		t.Error("must not attempt creation without the topology config")
	}
	if !strings.Contains(err.Error(), cluster.DefaultKindConfig) {
		t.Errorf("error should name the missing config, got: %v", err)
	}
}

// ── The blocked states (R3) ────────────────────────────────────────────────

// Each state needs its own advice: installing Docker, starting Docker, and
// installing kind are three different actions.
func TestResolveCluster_BlockedStatesGiveDistinctAdvice(t *testing.T) {
	tests := []struct {
		name     string
		env      cluster.Environment
		wantHint string
	}{
		{
			name:     "no docker at all",
			env:      cluster.Environment{Docker: cluster.DockerNotInstalled},
			wantHint: "get-docker",
		},
		{
			name:     "docker installed but stopped",
			env:      cluster.Environment{Docker: cluster.DockerInstalledNotRunning, KindInstalled: true},
			wantHint: "not running",
		},
		{
			name:     "docker running but kind missing",
			env:      cluster.Environment{Docker: cluster.DockerRunning, KindInstalled: false},
			wantHint: "kind is not installed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &stubGate{contexts: nil, env: tt.env, tty: true}
			s.install(t)

			_, err := resolveCluster()
			if err == nil {
				t.Fatal("want an error explaining what is missing")
			}
			if !strings.Contains(err.Error(), tt.wantHint) {
				t.Errorf("error should mention %q, got: %v", tt.wantHint, err)
			}
			if s.createCalled || s.confirmCalled {
				t.Error("must not offer to create a cluster that cannot be created")
			}
		})
	}
}

// The stale-kubeconfig case: contexts exist but none answer. This must not be
// reported as "no clusters found", and must still offer kind.
func TestResolveCluster_ContextsExistButNoneReachable(t *testing.T) {
	s := &stubGate{
		contexts: []cluster.Context{unreachable("stale"), unreachable("older")},
		env:      dockerReady, tty: true, confirm: true,
	}
	s.install(t)

	got, err := resolveCluster()
	if err != nil {
		t.Fatalf("resolveCluster: %v", err)
	}
	if !s.createCalled {
		t.Error("unreachable contexts should still lead to the kind offer")
	}
	if got != "kind-"+cluster.DefaultKindClusterName {
		t.Errorf("got %q", got)
	}
}

// A broken kubectl must surface, not be mistaken for "no clusters".
func TestResolveCluster_KubeconfigErrorPropagates(t *testing.T) {
	s := &stubGate{listErr: errors.New("permission denied"), env: dockerReady, tty: true}
	s.install(t)

	_, err := resolveCluster()
	if err == nil {
		t.Fatal("a kubeconfig read failure must surface")
	}
	if s.createCalled {
		t.Error("must not create a cluster because kubectl is broken")
	}
}

// Phase numbers are generated, not literals — a reorder can no longer produce
// the [1], [1], [2], [3], [4] sequence users saw.
func TestDeployPhaseCounter(t *testing.T) {
	resetDeployPhases()
	for want := 1; want <= 5; want++ {
		if got := nextDeployPhase(); got != want {
			t.Fatalf("phase %d, want %d", got, want)
		}
	}
	resetDeployPhases()
	if got := nextDeployPhase(); got != 1 {
		t.Fatalf("after reset, phase = %d, want 1", got)
	}
}
