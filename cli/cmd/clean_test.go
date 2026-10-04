package cmd

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func init() {
	cleanSleepFn = func(time.Duration) {}
	// No test lists or stops a real process. runClean used to run pkill for
	// real, so `go test ./...` ended every kubectl port-forward on the
	// machine.
	listProcessesFn = func() (map[int]string, error) { return nil, nil }
	stopProcessFn = func(pid int) error { return fmt.Errorf("tests never stop process %d", pid) }
}

// currentContextReply answers `kubectl config current-context` for the clean
// stubs below, and the lists clean reads to see what else uses a CRD, with
// no objects.
func currentContextReply(name string, args []string) ([]byte, bool) {
	if name == "kubectl" && len(args) >= 2 && args[0] == "config" && args[1] == "current-context" {
		return []byte("kind-test\n"), true
	}
	if name == "kubectl" && len(args) >= 4 && args[0] == "get" && args[2] == "-A" && args[3] == "-o" {
		return []byte(`{"items":[]}`), true
	}
	return nil, false
}

func TestCleanCommand_SingleTopology(t *testing.T) {
	// Reset/configure flags
	cleanForce = true
	cleanVerbose = false
	cleanTopology = "single"
	cleanNamespace = "kates-single-test"

	var executedCommands []string
	var mu sync.Mutex

	// Mock cleanRunFn to simulate that everything exists
	cleanRunFn = func(ctx context.Context, name string, args ...string) error {
		cmdStr := name + " " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		return nil
	}

	cleanRunOutputFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		cmdStr := name + " " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		if out, ok := currentContextReply(name, args); ok {
			return out, nil
		}
		if name == "helm" && args[0] == "list" {
			return []byte(`[{"name":"chaos","namespace":"kates-single-test"},{"name":"kates","namespace":"kates-single-test"},{"name":"apicurio","namespace":"kates-single-test"},{"name":"jaeger","namespace":"kates-single-test"},{"name":"krafter","namespace":"kates-single-test"},{"name":"monitoring","namespace":"kates-single-test"},{"name":"postgresql","namespace":"kates-single-test"}]`), nil
		}
		if name == "kubectl" && args[0] == "get" && args[1] == "namespaces" {
			return []byte("kates-single-test"), nil
		}
		if name == "kubectl" && args[0] == "get" && args[1] == "crd" {
			return []byte("kafkas.kafka.strimzi.io"), nil
		}
		if name == "kubectl" && args[0] == "get" && strings.HasPrefix(args[1], "kafkas") {
			// Mock finding one stuck CR name "my-kafka"
			return []byte("my-kafka"), nil
		}
		return []byte(""), nil
	}

	// Restore default runners after test
	defer func() {
		cleanRunFn = cleanRunDefault
		cleanRunOutputFn = cleanRunOutputDefault
	}()

	err := runClean(cleanCmd, []string{})
	if err != nil {
		t.Fatalf("runClean failed: %v", err)
	}

	// Verify executed commands contains single-topology elements
	foundSingleNSDelete := false
	foundIsolatedNSDelete := false
	foundSingleHelmUninstall := false
	foundIsolatedHelmUninstall := false

	mu.Lock()
	cmds := make([]string, len(executedCommands))
	copy(cmds, executedCommands)
	mu.Unlock()

	for _, cmd := range cmds {
		if strings.Contains(cmd, "kubectl delete namespace kates-single-test") {
			foundSingleNSDelete = true
		}
		if strings.Contains(cmd, "kubectl delete namespace kates-isolated-test") {
			foundIsolatedNSDelete = true
		}
		if strings.Contains(cmd, "helm uninstall kates -n kates-single-test") {
			foundSingleHelmUninstall = true
		}
		if strings.Contains(cmd, "helm uninstall kates -n kates-isolated-test") {
			foundIsolatedHelmUninstall = true
		}
	}

	if !foundSingleNSDelete {
		t.Error("Expected kates-single-test namespace to be deleted")
	}
	if foundIsolatedNSDelete {
		t.Error("Did not expect isolated namespaces to be deleted under single topology")
	}
	if !foundSingleHelmUninstall {
		t.Error("Expected kates release in single-topology namespace to be uninstalled")
	}
	if foundIsolatedHelmUninstall {
		t.Error("Did not expect isolated-topology releases to be uninstalled")
	}

	foundCRDDelete := false
	for _, cmd := range cmds {
		if strings.Contains(cmd, "kubectl delete crd") && strings.Contains(cmd, "kafkas.kafka.strimzi.io") {
			foundCRDDelete = true
		}
	}
	if !foundCRDDelete {
		t.Error("Expected kafkas.kafka.strimzi.io CRD to be deleted")
	}
}

func TestCleanCommand_IsolatedTopology(t *testing.T) {
	// Reset/configure flags
	cleanForce = true
	cleanVerbose = false
	cleanTopology = "isolated"
	cleanKafkaNS = "kafka-iso-test"
	cleanAppNS = "app-iso-test"
	cleanChaosNS = "chaos-iso-test"
	cleanMonitoringNS = "monitoring-iso-test"

	var executedCommands []string
	var mu sync.Mutex

	cleanRunFn = func(ctx context.Context, name string, args ...string) error {
		cmdStr := name + " " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		return nil
	}

	cleanRunOutputFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		cmdStr := name + " " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		if out, ok := currentContextReply(name, args); ok {
			return out, nil
		}
		if name == "helm" && args[0] == "list" {
			return []byte(`[{"name":"chaos","namespace":"chaos-iso-test"},{"name":"kates","namespace":"app-iso-test"},{"name":"apicurio","namespace":"kafka-iso-test"}]`), nil
		}
		if name == "kubectl" && args[0] == "get" && args[1] == "namespaces" {
			return []byte("chaos-iso-test app-iso-test kafka-iso-test"), nil
		}
		if name == "kubectl" && args[0] == "get" && args[1] == "crd" {
			return []byte(""), nil
		}
		return []byte(""), nil
	}

	defer func() {
		cleanRunFn = cleanRunDefault
		cleanRunOutputFn = cleanRunOutputDefault
	}()

	err := runClean(cleanCmd, []string{})
	if err != nil {
		t.Fatalf("runClean failed: %v", err)
	}

	// Verify that the isolated namespaces and releases are uninstalled, but NOT the single namespace
	foundAppNSDelete := false
	foundKafkaNSDelete := false
	foundSingleNSDelete := false
	foundAppHelmUninstall := false

	mu.Lock()
	cmds := make([]string, len(executedCommands))
	copy(cmds, executedCommands)
	mu.Unlock()

	for _, cmd := range cmds {
		if strings.Contains(cmd, "kubectl delete namespace app-iso-test") {
			foundAppNSDelete = true
		}
		if strings.Contains(cmd, "kubectl delete namespace kafka-iso-test") {
			foundKafkaNSDelete = true
		}
		if strings.Contains(cmd, "kubectl delete namespace kates-stack") {
			foundSingleNSDelete = true
		}
		if strings.Contains(cmd, "helm uninstall kates -n app-iso-test") {
			foundAppHelmUninstall = true
		}
	}

	if !foundAppNSDelete {
		t.Error("Expected app-iso-test namespace to be deleted")
	}
	if !foundKafkaNSDelete {
		t.Error("Expected kafka-iso-test namespace to be deleted")
	}
	if foundSingleNSDelete {
		t.Error("Did not expect single namespace to be deleted under isolated topology")
	}
	if !foundAppHelmUninstall {
		t.Error("Expected kates release in isolated-topology app namespace to be uninstalled")
	}
}

func TestCleanCommand_DefaultBothTopology(t *testing.T) {
	// Reset/configure flags
	cleanForce = true
	cleanVerbose = false
	cleanTopology = "" // both
	cleanNamespace = "kates-single-def"
	cleanKafkaNS = "kafka-iso-def"
	cleanAppNS = "app-iso-def"
	cleanChaosNS = "chaos-iso-def"
	cleanMonitoringNS = "monitoring-iso-def"

	var executedCommands []string
	var mu sync.Mutex

	cleanRunFn = func(ctx context.Context, name string, args ...string) error {
		cmdStr := name + " " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		return nil
	}

	cleanRunOutputFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		cmdStr := name + " " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		if out, ok := currentContextReply(name, args); ok {
			return out, nil
		}
		if name == "helm" && args[0] == "list" {
			return []byte(`[{"name":"kates","namespace":"kates-single-def"},{"name":"kates","namespace":"app-iso-def"}]`), nil
		}
		if name == "kubectl" && args[0] == "get" && args[1] == "namespaces" {
			return []byte("kates-single-def app-iso-def"), nil
		}
		if name == "kubectl" && args[0] == "get" && args[1] == "crd" {
			return []byte(""), nil
		}
		return []byte(""), nil
	}

	defer func() {
		cleanRunFn = cleanRunDefault
		cleanRunOutputFn = cleanRunOutputDefault
	}()

	err := runClean(cleanCmd, []string{})
	if err != nil {
		t.Fatalf("runClean failed: %v", err)
	}

	// Verify both single and isolated environments are cleaned
	foundSingleNSDelete := false
	foundIsolatedNSDelete := false

	mu.Lock()
	cmds := make([]string, len(executedCommands))
	copy(cmds, executedCommands)
	mu.Unlock()

	for _, cmd := range cmds {
		if strings.Contains(cmd, "kubectl delete namespace kates-single-def") {
			foundSingleNSDelete = true
		}
		if strings.Contains(cmd, "kubectl delete namespace app-iso-def") {
			foundIsolatedNSDelete = true
		}
	}

	if !foundSingleNSDelete {
		t.Error("Expected single-topology namespace to be deleted when topology is not specified")
	}
	if !foundIsolatedNSDelete {
		t.Error("Expected isolated-topology namespace to be deleted when topology is not specified")
	}
}

func TestCleanCommand_AlreadyClean(t *testing.T) {
	cleanForce = true
	cleanVerbose = false
	cleanTopology = "single"
	cleanNamespace = "already-clean-ns"

	// Mock cleanRunFn to simulate nothing exists (returns error for status/get namespace)
	cleanRunFn = func(ctx context.Context, name string, args ...string) error {
		return fmt.Errorf("not found")
	}

	cleanRunOutputFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if out, ok := currentContextReply(name, args); ok {
			return out, nil
		}
		return nil, fmt.Errorf("not found")
	}

	defer func() {
		cleanRunFn = cleanRunDefault
		cleanRunOutputFn = cleanRunOutputDefault
	}()

	err := runClean(cleanCmd, []string{})
	if err != nil {
		t.Fatalf("runClean failed: %v", err)
	}
	// The command should exit with nil and print already clean message
}

// cleanFixture is a cluster with the single topology installed in
// kates-single-test, on context kind-test. It records every kubectl and helm
// call, serves the process table procs, and records the PIDs stopped.
type cleanFixture struct {
	mu       sync.Mutex
	calls    []string
	procs    map[int]string
	listed   bool
	stopped  []int
	contexts []string // what `kubectl config current-context` answers, in turn

	helmList   string            // what `helm list -A -o json` answers
	namespaces string            // what the namespace list holds
	crds       string            // the CRDs on the cluster
	objects    map[string]string // `kubectl get <kinds> -A -o json` by kinds; {"items":[]} otherwise
	clusterObj map[string]string // `kubectl get <kind> <name> -o json` by "kind/name"; "" otherwise
	failList   bool              // the object lists fail
}

func newCleanFixture(t *testing.T) *cleanFixture {
	t.Helper()
	f := &cleanFixture{
		contexts:   []string{"kind-test"},
		helmList:   `[{"name":"kates","namespace":"kates-single-test"}]`,
		namespaces: "default kates-single-test prod",
		crds:       "kafkas.kafka.strimzi.io",
		objects:    map[string]string{},
		clusterObj: map[string]string{},
	}

	prevRun, prevOutput := cleanRunFn, cleanRunOutputFn
	prevList, prevStop := listProcessesFn, stopProcessFn
	prevInteractive, prevConfirm := interactiveAllowedFn, confirmFn
	prevForce, prevVerbose, prevTopology, prevNS := cleanForce, cleanVerbose, cleanTopology, cleanNamespace
	t.Cleanup(func() {
		cleanRunFn, cleanRunOutputFn = prevRun, prevOutput
		listProcessesFn, stopProcessFn = prevList, prevStop
		interactiveAllowedFn, confirmFn = prevInteractive, prevConfirm
		cleanForce, cleanVerbose, cleanTopology, cleanNamespace = prevForce, prevVerbose, prevTopology, prevNS
	})

	cleanForce, cleanVerbose, cleanTopology, cleanNamespace = false, false, "single", "kates-single-test"
	interactiveAllowedFn = func() bool { return false }
	confirmFn = func(string) (bool, error) {
		t.Error("asked to confirm")
		return false, nil
	}
	cleanRunFn = func(ctx context.Context, name string, args ...string) error {
		f.record(name, args)
		return nil
	}
	cleanRunOutputFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		f.record(name, args)
		switch {
		case name == "kubectl" && len(args) >= 2 && args[0] == "config" && args[1] == "current-context":
			f.mu.Lock()
			defer f.mu.Unlock()
			answer := f.contexts[0]
			if len(f.contexts) > 1 {
				f.contexts = f.contexts[1:]
			}
			return []byte(answer + "\n"), nil
		case name == "helm" && args[0] == "list":
			return []byte(f.helmList), nil
		case name == "kubectl" && args[0] == "get" && args[1] == "namespaces":
			return []byte(f.namespaces), nil
		case name == "kubectl" && args[0] == "get" && args[1] == "crd":
			return []byte(f.crds), nil
		case name == "kubectl" && args[0] == "get" && len(args) > 3 && args[2] == "-A":
			if f.failList {
				return nil, fmt.Errorf("the server is currently unable to handle the request")
			}
			if out, ok := f.objects[args[1]]; ok {
				return []byte(out), nil
			}
			return []byte(`{"items":[]}`), nil
		case name == "kubectl" && args[0] == "get" && len(args) > 3 && args[3] == "-o":
			return []byte(f.clusterObj[args[1]+"/"+args[2]]), nil
		}
		return nil, nil
	}
	listProcessesFn = func() (map[int]string, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.listed = true
		return f.procs, nil
	}
	stopProcessFn = func(pid int) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.stopped = append(f.stopped, pid)
		return nil
	}
	return f
}

func (f *cleanFixture) record(name string, args []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
}

// changed returns the calls that change something: everything but reads.
func (f *cleanFixture) changed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		fields := strings.Fields(c)
		if len(fields) < 2 {
			continue
		}
		switch fields[1] {
		case "get", "list", "status", "config":
			continue
		}
		out = append(out, c)
	}
	return out
}

// Declining the prompt leaves everything as it was. The port-forwards used to
// be stopped before the prompt, so declining did not bring them back.
func TestClean_DeclineChangesNothing(t *testing.T) {
	f := newCleanFixture(t)
	f.procs = map[int]string{101: "kubectl port-forward service/kates 8080:8080 -n kates-single-test"}
	interactiveAllowedFn = func() bool { return true }
	var asked string
	confirmFn = func(q string) (bool, error) { asked = q; return false, nil }

	_, _, err := commandOutput(t, func() error { return runClean(cleanCmd, nil) })
	if _, ok := err.(*silentErr); !ok {
		t.Errorf("err = %v (%T), want a silentErr: declining exits 1", err, err)
	}
	if !strings.Contains(asked, "kind-test") {
		t.Errorf("the question %q does not name the cluster", asked)
	}
	if c := f.changed(); len(c) != 0 {
		t.Errorf("declined, yet ran %v", c)
	}
	if f.listed || len(f.stopped) != 0 {
		t.Errorf("declined, yet looked for port-forwards (stopped %v)", f.stopped)
	}
}

// Without a terminal the prompt is not answered for you; it used to go to
// huh, which needs one.
func TestClean_NoTerminalNeedsYes(t *testing.T) {
	f := newCleanFixture(t)

	_, stderr, err := commandOutput(t, func() error { return runClean(cleanCmd, nil) })
	if err == nil || !strings.Contains(stderr, "--yes") {
		t.Errorf("err = %v, stderr %q; want a refusal that names --yes", err, stderr)
	}
	if c := f.changed(); len(c) != 0 {
		t.Errorf("refused, yet ran %v", c)
	}
}

// Every call carries the context read at the start, even when kubectl's
// current context moves while clean runs.
func TestClean_PinsEveryCallToTheCluster(t *testing.T) {
	f := newCleanFixture(t)
	f.contexts = []string{"kind-test", "prod-cluster"}
	cleanForce = true

	commandStdout(t, func() error { return runClean(cleanCmd, nil) })

	f.mu.Lock()
	calls := append([]string(nil), f.calls...)
	f.mu.Unlock()
	if len(calls) < 5 {
		t.Fatalf("only %d calls: %v", len(calls), calls)
	}
	for _, c := range calls[1:] {
		want := "--context=kind-test"
		if strings.HasPrefix(c, "helm ") {
			want = "--kube-context=kind-test"
		}
		if !strings.Contains(c, want) {
			t.Errorf("%q does not carry %s", c, want)
		}
	}
}

func TestClean_NoCurrentContext(t *testing.T) {
	f := newCleanFixture(t)
	f.contexts = []string{""}
	cleanForce = true

	_, stderr, err := commandOutput(t, func() error { return runClean(cleanCmd, nil) })
	if err == nil || !strings.Contains(stderr, "use-context") {
		t.Errorf("err = %v, stderr %q; want an error that says how to choose a cluster", err, stderr)
	}
	if len(f.calls) != 1 {
		t.Errorf("ran more than the context lookup: %v", f.calls)
	}
}

// Only forwards into the namespaces being deleted, on this cluster, are
// stopped: not the user's own forwards elsewhere, and not an editor whose
// command line happens to hold "kates" and "ports".
func TestClean_StopsOnlyForwardsIntoDeletedNamespaces(t *testing.T) {
	f := newCleanFixture(t)
	f.procs = map[int]string{
		101: "kubectl port-forward service/kates 8080:8080 -n kates-single-test",
		102: "kubectl port-forward svc/orders-db 5432:5432 -n prod",
		103: "/usr/local/bin/kubectl --context=other port-forward svc/kates 8080:8080 -n kates-single-test",
		104: "kubectl port-forward svc/grafana 3000:80 --namespace=kates-single-test --context=kind-test",
		105: "vim /Users/me/codes/kates/cli/cmd/ports.go",
		106: "kates ports",
		107: "kubectl port-forward svc/kates 8080:8080",
	}
	cleanForce = true

	commandStdout(t, func() error { return runClean(cleanCmd, nil) })

	f.mu.Lock()
	stopped := append([]int(nil), f.stopped...)
	f.mu.Unlock()
	sort.Ints(stopped)
	if want := []int{101, 104}; !reflect.DeepEqual(stopped, want) {
		t.Errorf("stopped %v, want %v", stopped, want)
	}
}

func TestParsePortForward(t *testing.T) {
	for _, tt := range []struct {
		cmdLine string
		want    portForwardProc
		ok      bool
	}{
		{"kubectl port-forward svc/kates 8080:8080 -n kates", portForwardProc{Namespace: "kates"}, true},
		{"kubectl port-forward -n=kafka svc/x 1:1", portForwardProc{Namespace: "kafka"}, true},
		{"kubectl port-forward -nkafka svc/x 1:1", portForwardProc{Namespace: "kafka"}, true},
		{"kubectl --namespace kafka --context kind-panda port-forward svc/x 1:1", portForwardProc{Namespace: "kafka", Context: "kind-panda"}, true},
		{"/opt/homebrew/bin/kubectl port-forward svc/x 1:1 --context=eks-prod", portForwardProc{Context: "eks-prod"}, true},
		{"kubectl get pods -n kafka", portForwardProc{Namespace: "kafka"}, false},
		{"kates ports", portForwardProc{}, false},
		{"kubectl-port-forward-wrapper port-forward svc/x 1:1", portForwardProc{}, false},
	} {
		got, ok := parsePortForward(tt.cmdLine)
		if ok != tt.ok || got != tt.want {
			t.Errorf("parsePortForward(%q) = %+v, %v; want %+v, %v", tt.cmdLine, got, ok, tt.want, tt.ok)
		}
	}
}

// ran reports whether any recorded call contains every one of parts.
func (f *cleanFixture) ran(parts ...string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		all := true
		for _, p := range parts {
			if !strings.Contains(c, p) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// A Certificate in another namespace keeps cert-manager: its release, its
// namespace and its CRDs. Deleting the CRDs would delete that Certificate;
// clean used to do it, and to uninstall a cert-manager that kates deploy
// had found already installed and left alone.
func TestClean_KeepsAnOperatorUsedElsewhere(t *testing.T) {
	f := newCleanFixture(t)
	f.helmList = `[{"name":"kates","namespace":"kates-single-test"},{"name":"cert-manager","namespace":"cert-manager"},
		{"name":"strimzi-operator","namespace":"strimzi-operator"}]`
	f.namespaces = "default kates-single-test cert-manager strimzi-operator web"
	f.crds = "kafkas.kafka.strimzi.io certificates.cert-manager.io issuers.cert-manager.io clusterissuers.cert-manager.io"
	f.objects["certificates.cert-manager.io,issuers.cert-manager.io,clusterissuers.cert-manager.io"] = `{"items":[
		{"kind":"Certificate","metadata":{"name":"web-tls","namespace":"web"}},
		{"kind":"Certificate","metadata":{"name":"kafka-tls","namespace":"kates-single-test"}},
		{"kind":"ClusterIssuer","metadata":{"name":"selfsigned-issuer"}}]}`
	f.objects["kafkas.kafka.strimzi.io"] = `{"items":[
		{"kind":"Kafka","metadata":{"name":"krafter","namespace":"kates-single-test"}}]}`
	cleanForce = true

	stdout := commandStdout(t, func() error { return runClean(cleanCmd, nil) })

	if f.ran("helm uninstall cert-manager") || f.ran("delete namespace cert-manager") || f.ran("delete crd", "certificates.cert-manager.io") {
		t.Errorf("removed cert-manager, which a Certificate in web uses:\n%s", strings.Join(f.calls, "\n"))
	}
	if !f.ran("helm uninstall strimzi-operator") || !f.ran("delete crd", "kafkas.kafka.strimzi.io") {
		t.Errorf("kept Strimzi, which only the stack uses:\n%s", strings.Join(f.calls, "\n"))
	}
	if !strings.Contains(string(stdout), "cert-manager operator, its namespace and its CRDs") ||
		!strings.Contains(string(stdout), "1 Certificate in web") {
		t.Errorf("the list does not say what was kept and why:\n%s", stdout)
	}
}

// A namespace that also holds a release kates does not install is kept; the
// stack's releases in it are still uninstalled.
func TestClean_KeepsANamespaceWithAnotherRelease(t *testing.T) {
	f := newCleanFixture(t)
	f.helmList = `[{"name":"kates","namespace":"kates-single-test"},{"name":"orders","namespace":"kates-single-test"}]`
	cleanForce = true

	stdout := commandStdout(t, func() error { return runClean(cleanCmd, nil) })

	if f.ran("delete namespace kates-single-test") {
		t.Error("deleted a namespace that holds the orders release")
	}
	if !f.ran("helm uninstall kates -n kates-single-test") {
		t.Error("did not uninstall the stack's own release")
	}
	if !strings.Contains(string(stdout), "kates does not install: orders") {
		t.Errorf("the list does not name the other release:\n%s", stdout)
	}
}

// When the objects cannot be listed, nothing says the CRDs are unused, so
// they stay.
func TestClean_KeepsWhatItCannotCheck(t *testing.T) {
	f := newCleanFixture(t)
	f.failList = true
	cleanForce = true

	stdout := commandStdout(t, func() error { return runClean(cleanCmd, nil) })

	if f.ran("delete crd") {
		t.Error("deleted CRDs whose use it could not check")
	}
	if !strings.Contains(string(stdout), "could not list its objects") {
		t.Errorf("the list does not say why the CRDs stay:\n%s", stdout)
	}
}

// The kates and litmus ClusterRoles go by name only when no release outside
// the stack installed them.
func TestClean_KeepsAClusterRoleAnotherReleaseOwns(t *testing.T) {
	f := newCleanFixture(t)
	f.clusterObj["clusterrole/kates"] = `{"kind":"ClusterRole","metadata":{"name":"kates"}}`
	f.clusterObj["clusterrole/litmus"] = `{"kind":"ClusterRole","metadata":{"name":"litmus",
		"annotations":{"meta.helm.sh/release-namespace":"platform-chaos"}}}`
	cleanForce = true

	stdout := commandStdout(t, func() error { return runClean(cleanCmd, nil) })

	if !f.ran("delete clusterrole kates") {
		t.Error("did not delete the kates ClusterRole, which no release owns")
	}
	if f.ran("delete clusterrole litmus") || f.ran("delete clusterrolebinding") {
		t.Errorf("deleted a cluster-scoped object it should keep or that is not there:\n%s", strings.Join(f.calls, "\n"))
	}
	if !strings.Contains(string(stdout), "in platform-chaos") {
		t.Errorf("the list does not say who owns the litmus ClusterRole:\n%s", stdout)
	}
}

func TestGroupInUse_CountsOnlyObjectsOutsideTheStack(t *testing.T) {
	prev := cleanRunOutputFn
	t.Cleanup(func() { cleanRunOutputFn = prev })
	var reply string
	cleanRunOutputFn = func(context.Context, string, ...string) ([]byte, error) { return []byte(reply), nil }

	kyverno := cleanCRDGroups()[2]
	present := map[string]bool{"clusterpolicies.kyverno.io": true, "policies.kyverno.io": true}
	deleting := map[string]bool{"kates": true}
	for _, tt := range []struct {
		name, items string
		inUse       bool
	}{
		{"none", ``, false},
		{"a policy in a namespace being deleted", `{"kind":"Policy","metadata":{"name":"p","namespace":"kates"}}`, false},
		{"a policy in the operator's namespace", `{"kind":"Policy","metadata":{"name":"p","namespace":"kyverno"}}`, false},
		{"a ClusterPolicy a stack release installed", `{"kind":"ClusterPolicy","metadata":{"name":"p","annotations":{"meta.helm.sh/release-namespace":"kates"}}}`, false},
		{"a ClusterPolicy from elsewhere", `{"kind":"ClusterPolicy","metadata":{"name":"p","annotations":{"meta.helm.sh/release-namespace":"platform"}}}`, true},
		{"a ClusterPolicy applied by hand", `{"kind":"ClusterPolicy","metadata":{"name":"require-labels"}}`, true},
		{"a policy in another namespace", `{"kind":"Policy","metadata":{"name":"p","namespace":"payments"}}`, true},
	} {
		reply = `{"items":[` + tt.items + `]}`
		why := groupInUse(context.Background(), kyverno, present, deleting)
		if (why != "") != tt.inUse {
			t.Errorf("%s: groupInUse = %q, want in use %v", tt.name, why, tt.inUse)
		}
	}
}

// kubectl ignores -n for a cluster-scoped kind, so clean's per-namespace
// `delete <kind> --all -n <ns>` deleted every ClusterIssuer and every
// ClusterPolicy on the cluster.
func TestClean_DeletesNoClusterScopedKindWithAll(t *testing.T) {
	f := newCleanFixture(t)
	f.crds = "certificates.cert-manager.io issuers.cert-manager.io clusterissuers.cert-manager.io clusterpolicies.kyverno.io policies.kyverno.io"
	cleanForce = true

	commandStdout(t, func() error { return runClean(cleanCmd, nil) })

	for _, kind := range []string{"clusterissuers.cert-manager.io", "clusterpolicies.kyverno.io"} {
		if f.ran("delete "+kind, "--all") || f.ran("get "+kind+" -n") || f.ran("patch "+kind) {
			t.Errorf("ran a per-namespace call on cluster-scoped %s:\n%s", kind, strings.Join(f.calls, "\n"))
		}
	}
	if !f.ran("delete certificates.cert-manager.io --all -n kates-single-test") {
		t.Errorf("did not delete the Certificates in the namespace being deleted:\n%s", strings.Join(f.calls, "\n"))
	}
}
