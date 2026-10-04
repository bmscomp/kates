package cluster

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fakeExec scripts command results by matching a prefix of the joined command
// line, so a test states only what it cares about.
type fakeExec struct {
	// responses maps a command-line prefix to its output/error.
	responses map[string]fakeResult
	// missing are binaries LookPath should fail for.
	missing map[string]bool
	// calls records every command, for asserting on flags we depend on.
	calls []string
}

type fakeResult struct {
	out string
	err error
}

func newFake() *fakeExec {
	return &fakeExec{responses: map[string]fakeResult{}, missing: map[string]bool{}}
}

func (f *fakeExec) on(prefix, out string, err error) *fakeExec {
	f.responses[prefix] = fakeResult{out: out, err: err}
	return f
}

func (f *fakeExec) absent(bin string) *fakeExec {
	f.missing[bin] = true
	return f
}

func (f *fakeExec) Exec(name string, args ...string) (string, error) {
	line := strings.TrimSpace(name + " " + strings.Join(args, " "))
	f.calls = append(f.calls, line)
	// Longest matching prefix wins, so a specific stub beats a general one.
	best, bestLen := fakeResult{}, -1
	for prefix, res := range f.responses {
		if strings.HasPrefix(line, prefix) && len(prefix) > bestLen {
			best, bestLen = res, len(prefix)
		}
	}
	if bestLen == -1 {
		return "", nil
	}
	return best.out, best.err
}

func (f *fakeExec) LookPath(file string) (string, error) {
	if f.missing[file] {
		return "", errors.New("not found in $PATH")
	}
	return "/usr/local/bin/" + file, nil
}

func (f *fakeExec) called(substr string) bool {
	for _, c := range f.calls {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

// ── ListContexts ───────────────────────────────────────────────────────────

func TestListContexts_MarksCurrent(t *testing.T) {
	f := newFake().
		on("kubectl config get-contexts", "kind-panda\nprod-eu\n", nil).
		on("kubectl config current-context", "prod-eu\n", nil)

	got, err := ListContexts(f)
	if err != nil {
		t.Fatalf("ListContexts: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 contexts, got %d: %+v", len(got), got)
	}
	if got[0].Name != "kind-panda" || got[0].Current {
		t.Errorf("kind-panda should not be current: %+v", got[0])
	}
	if got[1].Name != "prod-eu" || !got[1].Current {
		t.Errorf("prod-eu should be current: %+v", got[1])
	}
}

// The jsonpath sent to kubectl must have balanced braces. A doubled closing
// brace comes back in kubectl's output and renders as "kind-panda} · ns:}".
func TestListContexts_JsonpathBracesBalanced(t *testing.T) {
	f := newFake().
		on("kubectl config get-contexts", "kind-panda\n", nil).
		on("kubectl config current-context", "kind-panda\n", nil)

	if _, err := ListContexts(f); err != nil {
		t.Fatalf("ListContexts: %v", err)
	}

	sawView := false
	for _, call := range f.calls {
		if !strings.Contains(call, "config view") {
			continue
		}
		sawView = true
		opens := strings.Count(call, "{")
		closes := strings.Count(call, "}")
		if opens != closes {
			t.Errorf("unbalanced jsonpath braces (%d open, %d close): %s", opens, closes, call)
		}
		if strings.Contains(call, "}}") {
			t.Errorf("doubled closing brace in jsonpath: %s", call)
		}
	}
	if !sawView {
		t.Fatal("expected a kubectl config view call for context fields")
	}
}

// A machine with no kubeconfig is a normal state the flow handles, not an
// error to propagate.
func TestListContexts_NoKubeconfigIsEmptyNotError(t *testing.T) {
	f := newFake().on("kubectl config get-contexts", "error loading config file",
		errors.New("exit status 1"))

	got, err := ListContexts(f)
	if err != nil {
		t.Fatalf("missing kubeconfig should not error, got: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no contexts, got %+v", got)
	}
}

// A genuinely broken kubectl must NOT be silently swallowed as "no contexts",
// or we would offer to build a Kind cluster over a real but broken setup.
func TestListContexts_RealFailurePropagates(t *testing.T) {
	f := newFake().on("kubectl config get-contexts", "permission denied",
		errors.New("exit status 1"))

	if _, err := ListContexts(f); err == nil {
		t.Fatal("a real kubectl failure must propagate, not read as 'no contexts'")
	}
}

// ── Reachability ───────────────────────────────────────────────────────────

func TestCheckReachable_BoundsTheProbe(t *testing.T) {
	f := newFake().on("kubectl cluster-info", "Kubernetes control plane is running", nil)

	got := CheckReachable(f, Context{Name: "kind-panda"})
	if !got.Reachable || !got.Probed {
		t.Fatalf("want reachable+probed, got %+v", got)
	}
	// Without a bounded timeout the picker stalls on every dead context.
	if !f.called("--request-timeout") {
		t.Errorf("reachability probe must pass --request-timeout; calls: %v", f.calls)
	}
	if !f.called("--context kind-panda") {
		t.Errorf("probe must target the named context; calls: %v", f.calls)
	}
}

func TestCheckReachable_Unreachable(t *testing.T) {
	f := newFake().on("kubectl cluster-info", "connection refused", errors.New("exit status 1"))

	got := CheckReachable(f, Context{Name: "stale"})
	if got.Reachable {
		t.Fatal("want unreachable")
	}
	if !got.Probed {
		t.Fatal("Probed must be true even when unreachable — otherwise 'not checked' and 'dead' are indistinguishable")
	}
}

// ── ProbeEnvironment ───────────────────────────────────────────────────────

func TestProbeEnvironment(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(*fakeExec)
		wantDock DockerState
		wantKind bool
	}{
		{
			name:     "docker running, kind present",
			setup:    func(f *fakeExec) { f.on("docker info", "Server Version: 27.0", nil) },
			wantDock: DockerRunning,
			wantKind: true,
		},
		{
			// The distinction that matters: installed-but-stopped needs "start
			// Docker", not "install Docker".
			name:     "docker installed but daemon stopped",
			setup:    func(f *fakeExec) { f.on("docker info", "Cannot connect to the Docker daemon", errors.New("exit 1")) },
			wantDock: DockerInstalledNotRunning,
			wantKind: true,
		},
		{
			name:     "docker not installed",
			setup:    func(f *fakeExec) { f.absent("docker") },
			wantDock: DockerNotInstalled,
			wantKind: true,
		},
		{
			name: "docker running, kind absent",
			setup: func(f *fakeExec) {
				f.on("docker info", "Server Version: 27.0", nil)
				f.absent("kind")
			},
			wantDock: DockerRunning,
			wantKind: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake()
			tt.setup(f)
			env := ProbeEnvironment(f)
			if env.Docker != tt.wantDock {
				t.Errorf("Docker = %v, want %v", env.Docker, tt.wantDock)
			}
			if env.KindInstalled != tt.wantKind {
				t.Errorf("KindInstalled = %v, want %v", env.KindInstalled, tt.wantKind)
			}
		})
	}
}

// `docker version` succeeds against a stopped daemon; `docker info` does not.
// Probing with the wrong one reports a dead daemon as running.
func TestProbeEnvironment_UsesDockerInfoNotVersion(t *testing.T) {
	f := newFake().on("docker info", "Server Version: 27.0", nil)
	ProbeEnvironment(f)
	if !f.called("docker info") {
		t.Errorf("must probe the daemon with `docker info`; calls: %v", f.calls)
	}
}

// ── Resolve ────────────────────────────────────────────────────────────────

func ctx(name string, reachable bool) Context {
	return Context{Name: name, Reachable: reachable, Probed: true}
}

func TestResolve(t *testing.T) {
	dockerReady := Environment{Docker: DockerRunning, KindInstalled: true}

	tests := []struct {
		name     string
		contexts []Context
		env      Environment
		want     Outcome
	}{
		{
			name:     "one reachable context proceeds without asking",
			contexts: []Context{ctx("kind-panda", true)},
			env:      dockerReady,
			want:     UseContext,
		},
		{
			name:     "several reachable contexts require a choice",
			contexts: []Context{ctx("kind-panda", true), ctx("prod-eu", true)},
			env:      dockerReady,
			want:     ChooseContext,
		},
		{
			// A reachable cluster always wins; never offer to build one over it.
			name:     "one reachable among unreachable still proceeds",
			contexts: []Context{ctx("stale", false), ctx("kind-panda", true)},
			env:      dockerReady,
			want:     UseContext,
		},
		{
			name:     "no contexts at all, docker+kind ready",
			contexts: nil,
			env:      dockerReady,
			want:     OfferKind,
		},
		{
			// The stale-kubeconfig case the user's description does not cover.
			name:     "contexts exist but none reachable, docker+kind ready",
			contexts: []Context{ctx("stale", false), ctx("older", false)},
			env:      dockerReady,
			want:     OfferKind,
		},
		{
			name:     "docker running but kind missing",
			contexts: nil,
			env:      Environment{Docker: DockerRunning, KindInstalled: false},
			want:     NeedKind,
		},
		{
			name:     "docker installed but stopped",
			contexts: nil,
			env:      Environment{Docker: DockerInstalledNotRunning, KindInstalled: true},
			want:     NeedDockerRunning,
		},
		{
			name:     "no docker at all",
			contexts: nil,
			env:      Environment{Docker: DockerNotInstalled},
			want:     NeedDocker,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Resolve(tt.contexts, tt.env)
			if got.Outcome != tt.want {
				t.Errorf("Outcome = %v, want %v (reason: %s)", got.Outcome, tt.want, got.Reason())
			}
		})
	}
}

// "3 contexts, none reachable" must not be reported as "no clusters found".
func TestResolve_KeepsUnreachableForHonestMessaging(t *testing.T) {
	d := Resolve([]Context{ctx("a", false), ctx("b", false)},
		Environment{Docker: DockerRunning, KindInstalled: true})

	if len(d.Unreachable) != 2 {
		t.Fatalf("want 2 unreachable retained, got %d", len(d.Unreachable))
	}
	if !strings.Contains(d.Reason(), "none reachable") {
		t.Errorf("reason should distinguish stale contexts from no contexts, got: %q", d.Reason())
	}
}

// ── Kind bootstrap ─────────────────────────────────────────────────────────

func TestCreateKind_UsesThreeZoneConfigAndUntaints(t *testing.T) {
	f := newFake().
		on("kind create cluster", "", nil).
		on("kubectl wait", "", nil).
		on("kubectl get nodes", "alpha sigma gamma", nil).
		on("kubectl taint", "", nil)

	if err := CreateKind(f, KindOptions{}); err != nil {
		t.Fatalf("CreateKind: %v", err)
	}

	if !f.called("--config " + DefaultKindConfig) {
		t.Errorf("must create from the 3-AZ config %s; calls: %v", DefaultKindConfig, f.calls)
	}
	if !f.called("--name " + DefaultKindClusterName) {
		t.Errorf("must use cluster name %s; calls: %v", DefaultKindClusterName, f.calls)
	}
	if !f.called("kubectl wait --for=condition=Ready node --all") {
		t.Errorf("must wait for node readiness; calls: %v", f.calls)
	}
	// Without the untaint, alpha (control-plane) cannot schedule Kafka and the
	// cluster silently has 2 usable zones, not 3.
	for _, node := range []string{"alpha", "sigma", "gamma"} {
		if !f.called("kubectl taint nodes " + node) {
			t.Errorf("must untaint %s so all three zones schedule; calls: %v", node, f.calls)
		}
	}
}

func TestCreateKind_PropagatesCreateFailure(t *testing.T) {
	f := newFake().on("kind create cluster", "port 6443 already in use", errors.New("exit 1"))

	err := CreateKind(f, KindOptions{})
	if err == nil {
		t.Fatal("want error when kind create fails")
	}
	// The underlying reason must survive; "failed to create cluster" alone is
	// not actionable.
	if !strings.Contains(err.Error(), "already in use") {
		t.Errorf("error should carry kind's output, got: %v", err)
	}
}

// Untainting a node that carries no taint exits non-zero. That is a success for
// our purposes and must not fail the whole bootstrap.
func TestCreateKind_TolerantOfAbsentTaint(t *testing.T) {
	f := newFake().
		on("kind create cluster", "", nil).
		on("kubectl wait", "", nil).
		on("kubectl get nodes", "alpha sigma gamma", nil).
		on("kubectl taint", `taint "node-role.kubernetes.io/control-plane" not found`, errors.New("exit 1"))

	if err := CreateKind(f, KindOptions{}); err != nil {
		t.Fatalf("an absent taint is not a failure, got: %v", err)
	}
}

func TestCreateKind_ReportsProgress(t *testing.T) {
	f := newFake().
		on("kind create cluster", "", nil).
		on("kubectl wait", "", nil).
		on("kubectl get nodes", "alpha", nil)

	var steps []string
	err := CreateKind(f, KindOptions{Progress: func(s string) { steps = append(steps, s) }})
	if err != nil {
		t.Fatalf("CreateKind: %v", err)
	}
	if len(steps) < 3 {
		t.Errorf("want progress for create/wait/untaint, got %d: %v", len(steps), steps)
	}
	joined := strings.Join(steps, " | ")
	for _, zone := range KindZones {
		if !strings.Contains(joined, zone) {
			t.Errorf("progress should name the zones it builds (%s missing): %s", zone, joined)
		}
	}
}

func TestKindClusterExists(t *testing.T) {
	f := newFake().on("kind get clusters", "panda\nother\n", nil)
	if !KindClusterExists(f, "panda") {
		t.Error("panda should be reported as existing")
	}
	if KindClusterExists(f, "absent") {
		t.Error("absent should not be reported as existing")
	}
}

func TestEnvironment_CanCreateKind(t *testing.T) {
	tests := []struct {
		env  Environment
		want bool
	}{
		{Environment{Docker: DockerRunning, KindInstalled: true}, true},
		{Environment{Docker: DockerRunning, KindInstalled: false}, false},
		{Environment{Docker: DockerInstalledNotRunning, KindInstalled: true}, false},
		{Environment{Docker: DockerNotInstalled, KindInstalled: true}, false},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%v/kind=%v", tt.env.Docker, tt.env.KindInstalled), func(t *testing.T) {
			if got := tt.env.CanCreateKind(); got != tt.want {
				t.Errorf("CanCreateKind() = %v, want %v", got, tt.want)
			}
		})
	}
}
