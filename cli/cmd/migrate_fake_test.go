package cmd

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmscomp/kates/cli/output"
)

// fakeProc scripts runProcFn — the one seam every migrate command runs
// helm, kubectl, docker and kind through. Rules are matched against the
// command line ("kubectl -n kafka get kafka krafter …"), the last one
// registered first, so a test overrides a shared script by adding a rule
// after it. Every invocation is recorded with its stdin. A command no rule
// covers is an error, so a test that forgot to script a call fails loudly
// rather than passing on an empty answer.
type fakeProc struct {
	mu    sync.Mutex
	rules []fakeRule
	calls []fakeCall
}

type fakeRule struct {
	match func(cmdline string) bool
	fn    func(args []string, stdin string) (string, error)
}

type fakeCall struct {
	name  string
	args  []string
	stdin string
}

func (c fakeCall) line() string { return c.name + " " + strings.Join(c.args, " ") }

// newFakeProc installs a fake for the duration of the test, with the
// waits and the clock faked too: every migrateSleep advances a virtual
// clock, so a 600-second poll loop takes microseconds and still expires.
func newFakeProc(t *testing.T) *fakeProc {
	t.Helper()
	f := &fakeProc{}
	origRun, origSleep, origNow := runProcFn, migrateSleepFn, migrateNowFn
	runProcFn = f.run
	var clock time.Duration
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	migrateSleepFn = func(ctx context.Context, d time.Duration) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		clock += d
		return nil
	}
	migrateNowFn = func() time.Time { return base.Add(clock) }
	t.Cleanup(func() {
		runProcFn, migrateSleepFn, migrateNowFn = origRun, origSleep, origNow
	})
	return f
}

func (f *fakeProc) run(_ context.Context, stdin, name string, args ...string) (string, error) {
	call := fakeCall{name: name, args: args, stdin: stdin}
	f.mu.Lock()
	f.calls = append(f.calls, call)
	rules := f.rules
	f.mu.Unlock()
	line := call.line()
	// The last rule registered wins, so a test overrides a shared script by
	// adding a rule after it.
	for i := len(rules) - 1; i >= 0; i-- {
		if rules[i].match(line) {
			return rules[i].fn(args, stdin)
		}
	}
	return "", fmt.Errorf("fakeProc: unexpected command %q", line)
}

// on answers every command line starting with prefix with out.
func (f *fakeProc) on(prefix, out string) *fakeProc {
	return f.handle(prefix, func([]string, string) (string, error) { return out, nil })
}

// contains answers every command line containing sub with out.
func (f *fakeProc) contains(sub, out string) *fakeProc {
	return f.rule(func(l string) bool { return strings.Contains(l, sub) }, func([]string, string) (string, error) { return out, nil })
}

// fail makes every command line starting with prefix fail with msg.
func (f *fakeProc) fail(prefix, msg string) *fakeProc {
	return f.handle(prefix, func([]string, string) (string, error) { return "", fmt.Errorf("%s", msg) })
}

// handle answers every command line starting with prefix through fn.
func (f *fakeProc) handle(prefix string, fn func(args []string, stdin string) (string, error)) *fakeProc {
	return f.rule(func(l string) bool { return strings.HasPrefix(l, prefix) }, fn)
}

func (f *fakeProc) rule(match func(string) bool, fn func([]string, string) (string, error)) *fakeProc {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rules = append(f.rules, fakeRule{match: match, fn: fn})
	return f
}

// lines returns every recorded command line, in order.
func (f *fakeProc) lines() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		out = append(out, c.line())
	}
	return out
}

// find returns the recorded calls whose line starts with prefix.
func (f *fakeProc) find(prefix string) []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeCall
	for _, c := range f.calls {
		if strings.HasPrefix(c.line(), prefix) {
			out = append(out, c)
		}
	}
	return out
}

// hasLine reports whether a command line starting with prefix was run.
func (f *fakeProc) hasLine(prefix string) bool { return len(f.find(prefix)) > 0 }

// indexOf returns the position of the first command line starting with
// prefix, or -1.
func (f *fakeProc) indexOf(prefix string) int { return f.indexAfter(prefix, -1) }

// indexAfter returns the position of the first command line starting with
// prefix after position after, or -1.
func (f *fakeProc) indexAfter(prefix string, after int) int {
	for i, l := range f.lines() {
		if i > after && strings.HasPrefix(l, prefix) {
			return i
		}
	}
	return -1
}

// The cluster the fakes describe: a cluster-wide Strimzi 1.1.0 in
// strimzi-operator with the window 4.2.0 4.2.1 4.3.0, a Ready 4.3.0 krafter
// in kafka with one broker pool of one broker, the kates-mm2 Secret, on kind.
const (
	fakeOperatorPods = `{"items":[{"metadata":{"name":"strimzi-cluster-operator-7d9f8b6c5-abcde","namespace":"strimzi-operator",
	  "labels":{"strimzi.io/kind":"cluster-operator","pod-template-hash":"7d9f8b6c5"},
	  "ownerReferences":[{"kind":"ReplicaSet","name":"strimzi-cluster-operator-7d9f8b6c5"}]}}]}`
	fakeOperatorDeployment = `{"metadata":{"name":"strimzi-cluster-operator","namespace":"strimzi-operator"},
	  "spec":{"template":{"spec":{"containers":[{"name":"strimzi-cluster-operator","image":"quay.io/strimzi/operator:1.1.0",
	  "env":[{"name":"STRIMZI_NAMESPACE","value":"*"},
	         {"name":"STRIMZI_KAFKA_IMAGES","value":"4.2.0=quay.io/strimzi/kafka:1.1.0-kafka-4.2.0\n4.2.1=quay.io/strimzi/kafka:1.1.0-kafka-4.2.1\n4.3.0=quay.io/strimzi/kafka:1.1.0-kafka-4.3.0\n"}]}]}}}}`
	fakeHelmList = `[{"name":"strimzi-operator","namespace":"strimzi-operator","chart":"strimzi-operator-1.1.0","app_version":"1.1.0"},
	  {"name":"krafter","namespace":"kafka","chart":"kafka-cluster-1.0.0","app_version":"4.3.0"}]`
	fakeKafkaCR = `{"metadata":{"name":"krafter","namespace":"kafka"},"spec":{"kafka":{"version":"4.3.0"}},
	  "status":{"kafkaVersion":"4.3.0","conditions":[{"type":"Ready","status":"True"}]}}`
	fakeNodePools = `{"items":[{"metadata":{"name":"brokers"},"spec":{"replicas":1,"roles":["broker"]}},
	  {"metadata":{"name":"controllers"},"spec":{"replicas":1,"roles":["controller"]}}]}`
)

// scriptCluster scripts the discovery calls of the fake cluster.
func scriptCluster(f *fakeProc, kind bool) {
	f.on("kubectl get pods -A -l strimzi.io/kind=cluster-operator -o json", fakeOperatorPods)
	f.on("kubectl get deployment -n strimzi-operator strimzi-cluster-operator -o json", fakeOperatorDeployment)
	f.on("helm list -A -o json", fakeHelmList)
	f.on("kubectl -n kafka get kafka krafter --ignore-not-found -o json", fakeKafkaCR)
	f.on("kubectl -n kafka get kafkanodepool -l strimzi.io/cluster=krafter -o json", fakeNodePools)
	f.on("kubectl -n kafka get secret kates-mm2 --ignore-not-found -o jsonpath={.metadata.name}", "kates-mm2")
	kindnet := ""
	if kind {
		kindnet = "kindnet   3   3   3   3   3   kubernetes.io/os=linux   10d"
	}
	f.on("kubectl get daemonset kindnet -n kube-system --ignore-not-found --no-headers", kindnet)
}

// scriptPrerequisites scripts the scripts' phase 1.
func scriptPrerequisites(f *fakeProc) {
	f.on("kubectl cluster-info", "Kubernetes control plane is running")
	f.on("kubectl config current-context", "kind-panda")
	f.on("kubectl get crd kafkamirrormaker2s.kafka.strimzi.io -o name", "customresourcedefinition.apiextensions.k8s.io/kafkamirrormaker2s.kafka.strimzi.io")
	f.on("kubectl -n kafka wait kafka/krafter --for=condition=Ready", "kafka.kafka.strimzi.io/krafter condition met")
	f.on("kubectl -n kafka get kafkamirrormaker2 -l kates.io/lab -o json", `{"items":[]}`)
}

// mirrorCRJSON renders a KafkaMirrorMaker2 object with the given connector
// states.
func mirrorCRJSON(name, sourceState, checkpointState string) string {
	return fmt.Sprintf(`{"metadata":{"name":%q,"generation":2},"spec":{},"status":{"observedGeneration":2,"replicas":1,
	  "conditions":[{"type":"Ready","status":"True"}],
	  "connectors":[{"name":"source->target.MirrorSourceConnector","connector":{"state":%q},"tasks":[{"id":0,"state":%q}]},
	                {"name":"source->target.MirrorCheckpointConnector","connector":{"state":%q},"tasks":[{"id":0,"state":%q}]}]}}`,
		name, sourceState, sourceState, checkpointState, checkpointState)
}

// scriptPods scripts the podrun calls for a client pod: absent at first,
// applied, Ready, and tee echoing what it wrote.
func scriptPods(f *fakeProc) {
	f.rule(func(l string) bool {
		return strings.HasPrefix(l, "kubectl -n ") && strings.Contains(l, " get pod kates-migrate-") && strings.Contains(l, "jsonpath={.status.phase}")
	}, func([]string, string) (string, error) { return "", nil })
	f.rule(func(l string) bool { return strings.HasPrefix(l, "kubectl -n ") && strings.HasSuffix(l, " apply -f -") },
		func(_ []string, stdin string) (string, error) { return "created", nil })
	f.rule(func(l string) bool {
		return strings.HasPrefix(l, "kubectl -n ") && strings.Contains(l, " wait --for=condition=Ready pod/")
	},
		func([]string, string) (string, error) { return "condition met", nil })
	f.rule(func(l string) bool {
		return strings.HasPrefix(l, "kubectl -n ") && strings.Contains(l, " exec -i ") && strings.HasSuffix(l, " -- tee /tmp/client.properties")
	},
		func(_ []string, stdin string) (string, error) { return stdin, nil })
	f.rule(func(l string) bool {
		return strings.HasPrefix(l, "kubectl -n ") && strings.Contains(l, " delete pod kates-migrate-")
	},
		func([]string, string) (string, error) { return "", nil })
	f.on("kubectl -n kafka get secret kates-mm2 -o jsonpath={.data.password}", base64.StdEncoding.EncodeToString([]byte("s3cret")))
}

// migrateTestRepo builds the repository skeleton the commands look for —
// the charts' Chart.yaml files, the overlays and versions.env — in a temp
// dir and makes it the working directory, so generated files land there.
func migrateTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"versions.env":                                "STRIMZI_VERSION=\"1.1.0\"\nSTRIMZI_KAFKA_VERSION=\"1.1.0-kafka-4.3.0\"\nKAFKA_IMAGE=\"quay.io/strimzi/kafka:${STRIMZI_KAFKA_VERSION}\"\n",
		"charts/mirror-maker2/Chart.yaml":             "apiVersion: v2\nname: mirror-maker2\nversion: 1.0.0\n",
		"charts/mirror-maker2/values-kind.yaml":       "replicas: 1\n",
		"charts/mirror-maker2/values-migrate-2x.yaml": "replicas: 1\n",
		"charts/mirror-maker2/values-migrate-3x.yaml": "replicas: 1\n",
		"charts/mirror-maker2/values-migrate-4x.yaml": "replicas: 1\n",
		"charts/mirror-maker2/values-cutover.yaml":    "cutover:\n  enabled: true\n",
		"charts/legacy-kafka/Chart.yaml":              "apiVersion: v2\nname: legacy-kafka\nversion: 1.0.0\n",
		"charts/legacy-kafka/values-kafka-2x.yaml":    "kafka:\n  version: \"2.8.2\"\n",
		"charts/legacy-kafka/values-kafka-3x.yaml":    "kafka:\n  version: \"3.9.1\"\n",
		"Dockerfile.legacy-kafka":                     "FROM scratch\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	return dir
}

// captureOutput redirects the output layer for a test and sets the mode.
// It returns stdout; stderr (progress lines, errors) is captured
// separately and shown only when the test fails.
func captureOutput(t *testing.T, mode string) *strings.Builder {
	t.Helper()
	origMode := outputMode
	origOut, origErr := output.Out, output.Err
	var stdout, stderr strings.Builder
	output.Out, output.Err = &stdout, &stderr
	outputMode = mode
	t.Cleanup(func() {
		output.Out, output.Err = origOut, origErr
		outputMode = origMode
		if t.Failed() && stderr.Len() > 0 {
			t.Logf("stderr:\n%s", stderr.String())
		}
	})
	return &stdout
}

// newPairFlags returns the flags of `up --from <from>… --yes` with defaults;
// several versions are the fan-in a repeated --from asks for.
func newPairFlags(from ...string) *migratePairFlags {
	return &migratePairFlags{
		From: from, SourceProvider: "auto", Policy: "identity", Messages: 200,
		TargetCluster: "krafter", TargetNamespace: "kafka", Registry: "ghcr.io/bmscomp", Timeout: 600, Yes: true,
	}
}
