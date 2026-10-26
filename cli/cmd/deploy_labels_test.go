package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// useLabelsFile points --labels-file at path ("" is the optional ./.labels)
// and clears --namespace-label for the test.
func useLabelsFile(t *testing.T, path string) {
	t.Helper()
	origFile, origFlags := namespaceLabelsFile, deployNamespaceLabels
	t.Cleanup(func() { namespaceLabelsFile, deployNamespaceLabels = origFile, origFlags })
	namespaceLabelsFile, deployNamespaceLabels = path, nil
}

// writeLabelsFile writes content to a labels file in a temp dir and returns
// its path.
func writeLabelsFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "team.labels")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestNamespaceLabelsFileFormat(t *testing.T) {
	path := writeLabelsFile(t, "# owner of the cluster\n\n  gov.example.com/product-id = KATES1  \r\n   # indented comment\r\ncyber.example.com/sensitivity=2\r\nteam=\n")
	useLabelsFile(t, path)
	labels, sources, err := namespaceUserLabels()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"gov.example.com/product-id": "KATES1", "cyber.example.com/sensitivity": "2", "team": ""}
	if !reflect.DeepEqual(labels, want) {
		t.Errorf("labels = %v, want %v", labels, want)
	}
	wantSources := map[string]string{"gov.example.com/product-id": path + ":3", "cyber.example.com/sensitivity": path + ":5", "team": path + ":6"}
	if !reflect.DeepEqual(sources, wantSources) {
		t.Errorf("sources = %v, want %v", sources, wantSources)
	}
}

func TestNamespaceLabelsFileErrors(t *testing.T) {
	for _, tc := range []struct{ name, content, want string }{
		{"missing =", "a=b\nteam\n", ":2: invalid namespace label \"team\": expected key=value"},
		{"duplicate key", "a=b\n# x\na=c\n", `:3: label "a" is already set on line 1`},
		{"quoted value", "a=\"b\"\n", ":1: label \"a\": value"},
		{"inline comment", "a=b # owner\n", ":1: label \"a\": value"},
		{"empty key", "=b\n", ":1: label key \"\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeLabelsFile(t, tc.content)
			useLabelsFile(t, path)
			_, _, err := namespaceUserLabels()
			if err == nil || !strings.HasPrefix(err.Error(), path+":") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q prefixed by the path", err, tc.want)
			}
		})
	}
}

// A missing ./.labels is no labels; a file --labels-file names must exist.
func TestNamespaceLabelsFileMissing(t *testing.T) {
	t.Chdir(t.TempDir())
	useLabelsFile(t, "")
	labels, _, err := namespaceUserLabels()
	if err != nil || len(labels) != 0 {
		t.Fatalf("absent ./.labels: labels = %v, err = %v; want none and no error", labels, err)
	}

	if err := os.WriteFile(".labels", []byte("team=payments\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	labels, sources, err := namespaceUserLabels()
	if err != nil || labels["team"] != "payments" || sources["team"] != ".labels:1" {
		t.Fatalf("./.labels: labels = %v, sources = %v, err = %v", labels, sources, err)
	}

	useLabelsFile(t, "missing.labels")
	if _, _, err := namespaceUserLabels(); err == nil || !strings.Contains(err.Error(), "missing.labels") {
		t.Fatalf("explicit missing file: err = %v, want an error naming it", err)
	}
}

func TestValidateNamespaceLabel(t *testing.T) {
	long := func(n int) string { return strings.Repeat("a", n) }
	for _, tc := range []struct {
		key, value string
		ok         bool
	}{
		{"team", "payments", true},
		{"team", "", true},
		{"A_b.c-9", "X_y.z-1", true},
		{long(63), long(63), true},
		{long(253) + "/name", "v", true},
		{"gov.example.com/product-id", "KATES1", true},
		{"pod-security.kubernetes.io/enforce", "restricted", true},

		{long(64), "v", false},
		{"team", long(64), false},
		{long(254) + "/name", "v", false},
		{"/name", "v", false},
		{"Example.com/name", "v", false},
		{"example.com/", "v", false},
		{"a/b/c", "v", false},
		{"-team", "v", false},
		{"team-", "v", false},
		{"team", "-x", false},
		{"team", `"x"`, false},
		{"team", "a#b", false},
		{"team", "a b", false},
		{"kubernetes.io/name", "v", false},
		{"app.kubernetes.io/name", "v", false},
		{"node.kubernetes.io/role", "v", false},
		{"k8s.io/name", "v", false},
		{"x.k8s.io/name", "v", false},
		{"helm.sh/chart", "v", false},
		{"strimzi.io/cluster", "v", false},
		{"kates.io/lab", "v", false},
		{"team.kates.io/lab", "v", false},
		{"x.pod-security.kubernetes.io/enforce", "v", false},
	} {
		err := validateNamespaceLabel(tc.key, tc.value)
		if (err == nil) != tc.ok {
			t.Errorf("validateNamespaceLabel(%q, %q) = %v, want ok=%v", tc.key, tc.value, err, tc.ok)
		}
	}
}

// Every source validates, and every error says which source to fix.
func TestNamespaceLabelErrorsNameTheirSource(t *testing.T) {
	useLabelsFile(t, writeLabelsFile(t, "team=ok\n"))

	t.Setenv(namespaceLabelsEnv, "team=ok,app.kubernetes.io/name=x")
	if _, _, err := namespaceUserLabels(); err == nil || !strings.HasPrefix(err.Error(), "KATES_NAMESPACE_LABELS: ") || !strings.Contains(err.Error(), "reserved") {
		t.Errorf("env: err = %v", err)
	}

	t.Setenv(namespaceLabelsEnv, "")
	deployNamespaceLabels = []string{"bad"}
	if _, _, err := namespaceUserLabels(); err == nil || !strings.HasPrefix(err.Error(), "--namespace-label: ") || !strings.Contains(err.Error(), "expected key=value") {
		t.Errorf("flag: err = %v", err)
	}

	// Empty values, which the variable and the flag used to refuse.
	t.Setenv(namespaceLabelsEnv, "a=")
	deployNamespaceLabels = []string{"b="}
	labels, _, err := namespaceUserLabels()
	if err != nil || labels["a"] != "" || labels["b"] != "" || len(labels) != 3 {
		t.Errorf("empty values: labels = %v, err = %v", labels, err)
	}
}

func TestNamespaceLabelPrecedence(t *testing.T) {
	path := writeLabelsFile(t, "a=file\nb=file\nc=file\n")
	useLabelsFile(t, path)
	t.Setenv(namespaceLabelsEnv, "b=env,c=env,d=env1,d=env2")
	deployNamespaceLabels = []string{"c=flag1", "c=flag2"}

	labels, sources, err := namespaceUserLabels()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"a": "file", "b": "env", "c": "flag2", "d": "env2"}
	wantSources := map[string]string{"a": path + ":1", "b": namespaceLabelsEnv, "c": namespaceLabelFlag, "d": namespaceLabelsEnv}
	if !reflect.DeepEqual(labels, want) || !reflect.DeepEqual(sources, wantSources) {
		t.Errorf("labels = %v, sources = %v; want %v, %v", labels, sources, want, wantSources)
	}

	all, err := deployNamespaceLabelMap()
	if err != nil || all[managedByLabel] != "kates" || len(all) != len(want)+1 {
		t.Errorf("deployNamespaceLabelMap = %v, %v; want the user labels and managed-by", all, err)
	}
}

func TestDeployNamespaceManifest(t *testing.T) {
	got := deployNamespaceManifest("monitoring", map[string]string{
		"gov.example.com/product-id": "KATES1",
		"team":                       "",
		managedByLabel:               "kates",
	})
	want := `apiVersion: v1
kind: Namespace
metadata:
  name: monitoring
  annotations:
    "kates.io/namespace-labels": "gov.example.com/product-id,team"
  labels:
    "app.kubernetes.io/managed-by": "kates"
    "gov.example.com/product-id": "KATES1"
    "team": ""
`
	if got != want {
		t.Errorf("manifest:\n%s\nwant:\n%s", got, want)
	}
	// managed-by alone is not the user's: no annotation.
	if got := deployNamespaceManifest("kafka", map[string]string{managedByLabel: "kates"}); strings.Contains(got, "annotations") {
		t.Errorf("manifest with no user labels has an annotation:\n%s", got)
	}
}

// namespaceKubectl fakes the three kubectl runners ensureDeployNamespace
// uses: get answers with existing (empty is a missing namespace) or fails
// with getErr, create and patch fail with createErr and patchErr. A failed
// patch prints patchOut, an RBAC refusal unless set.
type namespaceKubectl struct {
	existing                    string
	getErr, createErr, patchErr error
	patchOut                    string
	calls                       []string
	manifest                    string
}

func fakeNamespaceKubectl(t *testing.T, existing string) *namespaceKubectl {
	t.Helper()
	k := &namespaceKubectl{existing: existing}
	origOutput, origStdin, origCombined := runExecOutputFn, runExecStdinFn, runExecCombinedFn
	t.Cleanup(func() { runExecOutputFn, runExecStdinFn, runExecCombinedFn = origOutput, origStdin, origCombined })
	runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		k.calls = append(k.calls, name+" "+strings.Join(args, " "))
		return []byte(k.existing), k.getErr
	}
	runExecStdinFn = func(_ context.Context, name string, args []string, stdin string) error {
		k.calls = append(k.calls, name+" "+strings.Join(args, " "))
		k.manifest = stdin
		return k.createErr
	}
	runExecCombinedFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		k.calls = append(k.calls, name+" "+strings.Join(args, " "))
		if k.patchErr != nil {
			if k.patchOut != "" {
				return []byte(k.patchOut), k.patchErr
			}
			return []byte(`Error from server (Forbidden): namespaces "kafka" is forbidden: User "dev" cannot patch resource "namespaces"`), k.patchErr
		}
		return nil, nil
	}
	return k
}

// patch decodes the JSON of the one kubectl patch call.
func (k *namespaceKubectl) patch(t *testing.T) map[string]any {
	t.Helper()
	if len(k.calls) != 2 || !strings.HasPrefix(k.calls[1], "kubectl patch namespace kafka --type merge -p ") {
		t.Fatalf("calls = %q, want the lookup and one merge patch", k.calls)
	}
	var patch map[string]any
	if err := json.Unmarshal([]byte(strings.TrimPrefix(k.calls[1], "kubectl patch namespace kafka --type merge -p ")), &patch); err != nil {
		t.Fatal(err)
	}
	return patch
}

const getKafkaNamespace = "kubectl get namespace kafka --ignore-not-found -o json"

func existingNamespace(labels, annotations map[string]string) string {
	data, _ := json.Marshal(map[string]any{"metadata": map[string]any{"name": "kafka", "labels": labels, "annotations": annotations}})
	return string(data)
}

var kafkaLabels = map[string]string{"gov.example.com/product-id": "KATES1", managedByLabel: "kates"}

func TestEnsureDeployNamespaceCreatesAMissingOneLabelled(t *testing.T) {
	k := fakeNamespaceKubectl(t, "")
	if err := ensureDeployNamespace(context.Background(), "kafka", kafkaLabels); err != nil {
		t.Fatal(err)
	}
	if want := []string{getKafkaNamespace, "kubectl create -f -"}; !reflect.DeepEqual(k.calls, want) {
		t.Fatalf("calls = %q, want %q", k.calls, want)
	}
	for _, want := range []string{"name: kafka", `"app.kubernetes.io/managed-by": "kates"`, `"gov.example.com/product-id": "KATES1"`, `"kates.io/namespace-labels": "gov.example.com/product-id"`} {
		if !strings.Contains(k.manifest, want) {
			t.Errorf("manifest lacks %q:\n%s", want, k.manifest)
		}
	}
}

// Labels already right: no call at all, so an account that may not patch
// namespaces passes. managed-by is not the existing namespace's business.
func TestEnsureDeployNamespaceLeavesMatchingLabelsAlone(t *testing.T) {
	k := fakeNamespaceKubectl(t, existingNamespace(map[string]string{"gov.example.com/product-id": "KATES1", "team": "payments"}, nil))
	if err := ensureDeployNamespace(context.Background(), "kafka", kafkaLabels); err != nil {
		t.Fatal(err)
	}
	if want := []string{getKafkaNamespace}; !reflect.DeepEqual(k.calls, want) {
		t.Fatalf("calls = %q, want only the lookup", k.calls)
	}
}

func TestEnsureDeployNamespacePatchesChangedAndRemovedLabels(t *testing.T) {
	k := fakeNamespaceKubectl(t, existingNamespace(
		map[string]string{
			"gov.example.com/product-id": "OLD",
			"old.example.com/gone":       "x",
			"team":                       "payments", // not Kates': never removed
			managedByLabel:               "helm",     // not Kates' either
		},
		map[string]string{namespaceLabelsAnnotation: "gov.example.com/product-id,old.example.com/gone,never.example.com/set"},
	))
	labels := map[string]string{"gov.example.com/product-id": "KATES1", "cyber.example.com/sensitivity": "2", managedByLabel: "kates"}
	if err := ensureDeployNamespace(context.Background(), "kafka", labels); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"metadata": map[string]any{
		"labels": map[string]any{
			"gov.example.com/product-id":    "KATES1",
			"cyber.example.com/sensitivity": "2",
			"old.example.com/gone":          nil,
		},
		"annotations": map[string]any{namespaceLabelsAnnotation: "cyber.example.com/sensitivity,gov.example.com/product-id"},
	}}
	if got := k.patch(t); !reflect.DeepEqual(got, want) {
		t.Errorf("patch = %v, want %v", got, want)
	}
}

// The last user label gone: removed, and the annotation with it.
func TestEnsureDeployNamespaceDropsTheAnnotationWithTheLastLabel(t *testing.T) {
	k := fakeNamespaceKubectl(t, existingNamespace(
		map[string]string{"gov.example.com/product-id": "KATES1"},
		map[string]string{namespaceLabelsAnnotation: "gov.example.com/product-id"},
	))
	if err := ensureDeployNamespace(context.Background(), "kafka", map[string]string{managedByLabel: "kates"}); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"metadata": map[string]any{
		"labels":      map[string]any{"gov.example.com/product-id": nil},
		"annotations": map[string]any{namespaceLabelsAnnotation: nil},
	}}
	if got := k.patch(t); !reflect.DeepEqual(got, want) {
		t.Errorf("patch = %v, want %v", got, want)
	}
}

func TestEnsureDeployNamespaceForbiddenCreateShowsTheManifest(t *testing.T) {
	k := fakeNamespaceKubectl(t, "")
	k.createErr = errors.New(`exit status 1: Error from server (Forbidden): namespaces is forbidden: User "dev" cannot create resource "namespaces"`)
	err := ensureDeployNamespace(context.Background(), "kafka", kafkaLabels)
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{`cannot create resource "namespaces"`, "may not create namespaces", "cluster admin", "run again", "kind: Namespace", `"gov.example.com/product-id": "KATES1"`, `"kates.io/namespace-labels"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}

	// An admission denial says Forbidden too, but an admin would be refused
	// the same way: its reason is the answer, with no admin instruction.
	for _, msg := range []string{
		"exit status 1: connection refused",
		`exit status 1: Error from server (Forbidden): error when creating "STDIN": admission webhook "validation.gatekeeper.sh" denied the request: you must provide labels: {"owner"}`,
	} {
		k.createErr = errors.New(msg)
		err := ensureDeployNamespace(context.Background(), "kafka", kafkaLabels)
		if err == nil || !strings.HasPrefix(err.Error(), "create namespace kafka: "+msg) || strings.Contains(err.Error(), "cluster admin") {
			t.Errorf("create error %q = %v", msg, err)
		}
	}
}

// --verbose streams kubectl's stderr, and must still hand it back in the
// error, or an RBAC refusal reads as a bare "exit status 1".
func TestRunExecStdinDefaultKeepsStderrWhenVerbose(t *testing.T) {
	orig := deployVerbose
	t.Cleanup(func() { deployVerbose = orig })
	for _, verbose := range []bool{false, true} {
		deployVerbose = verbose
		err := runExecStdinDefault(context.Background(), "sh", []string{"-c", `cat >/dev/null; echo 'User "dev" cannot create resource "namespaces"' >&2; exit 1`}, "kind: Namespace")
		if err == nil || !rbacRefused(err, "create") {
			t.Errorf("verbose=%v: error = %v, want kubectl's stderr in it", verbose, err)
		}
	}
}

func TestEnsureDeployNamespaceLookupFailureWritesNothing(t *testing.T) {
	k := fakeNamespaceKubectl(t, "")
	k.getErr = errors.New("exit status 1: Unable to connect to the server")
	if err := ensureDeployNamespace(context.Background(), "kafka", kafkaLabels); err == nil || !strings.HasPrefix(err.Error(), "check namespace kafka: ") {
		t.Errorf("lookup error = %v", err)
	}
	k.getErr, k.existing = nil, "not json"
	if err := ensureDeployNamespace(context.Background(), "kafka", kafkaLabels); err == nil || !strings.HasPrefix(err.Error(), "check namespace kafka: ") {
		t.Errorf("undecodable namespace = %v", err)
	}
	if len(k.calls) != 2 {
		t.Errorf("calls = %q, want two lookups and no write", k.calls)
	}
}

func TestEnsureDeployNamespaceOtherPatchFailureKeepsKubectlsWords(t *testing.T) {
	for _, out := range []string{
		`Error from server (Conflict): Operation cannot be fulfilled on namespaces "kafka"`,
		`Error from server (Forbidden): admission webhook "validation.gatekeeper.sh" denied the request: you must provide labels: {"owner"}`,
	} {
		k := fakeNamespaceKubectl(t, existingNamespace(map[string]string{"gov.example.com/product-id": "OLD"}, nil))
		k.patchErr, k.patchOut = errors.New("exit status 1"), out
		err := ensureDeployNamespace(context.Background(), "kafka", kafkaLabels)
		if err == nil || !strings.HasPrefix(err.Error(), "patch namespace kafka: ") || !strings.Contains(err.Error(), out) || strings.Contains(err.Error(), "cluster admin") {
			t.Errorf("patch failure %q = %v", out, err)
		}
	}
}

func TestEnsureDeployNamespaceForbiddenPatchShowsTheLabelCommand(t *testing.T) {
	k := fakeNamespaceKubectl(t, existingNamespace(
		map[string]string{"gov.example.com/product-id": "OLD", "old.example.com/gone": "x"},
		map[string]string{namespaceLabelsAnnotation: "gov.example.com/product-id,old.example.com/gone"},
	))
	k.patchErr = errors.New("exit status 1")
	labels := map[string]string{"gov.example.com/product-id": "KATES1", "cyber.example.com/sensitivity": "2", managedByLabel: "kates"}
	err := ensureDeployNamespace(context.Background(), "kafka", labels)
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{`cannot patch resource "namespaces"`, "cluster admin", "run again", "kubectl label namespace kafka cyber.example.com/sensitivity=2 gov.example.com/product-id=KATES1 old.example.com/gone- --overwrite"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}

func TestDeployDryRunListsNamespaceLabels(t *testing.T) {
	out := stripStyle(captureStdout(t, func() {
		renderDeployPreview(uiTestEntries(), map[string]bool{}, nil,
			map[string]string{"a": "1", "b": "2", "c": ""},
			map[string]string{"a": ".labels:3", "b": namespaceLabelsEnv, "c": namespaceLabelFlag})
	}))
	for _, want := range []string{"Namespace labels", "a=1  .labels:3", "b=2  KATES_NAMESPACE_LABELS", "c=  --namespace-label", "app.kubernetes.io/managed-by=kates is added only to the namespaces deploy creates"} {
		if !strings.Contains(out, want) {
			t.Errorf("preview lacks %q:\n%s", want, out)
		}
	}
	if out := stripStyle(captureStdout(t, func() { renderDeployPreview(uiTestEntries(), map[string]bool{}, nil, nil, nil) })); strings.Contains(out, "Namespace labels") {
		t.Errorf("preview without user labels has a labels block:\n%s", out)
	}
}

// A bad label stops kates deploy before it asks the cluster anything.
func TestDeployRefusesABadLabelBeforeTheCluster(t *testing.T) {
	useLabelsFile(t, writeLabelsFile(t, "app.kubernetes.io/name=kates\n"))
	k := fakeNamespaceKubectl(t, "")
	err := runDeploy(deployCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("err = %v, want the reserved-prefix refusal", err)
	}
	if len(k.calls) != 0 {
		t.Errorf("deploy ran %q before refusing", k.calls)
	}
}

func TestLabelsFileFlagOnEveryNamespaceCreator(t *testing.T) {
	for name, cmd := range map[string]*cobra.Command{
		"deploy": deployCmd, "auto": autoCmd, "kyverno apply": kyvernoApplyCmd, "detect": detectCmd,
		"migrate up": migrateUpCmd, "migrate run": migrateRunCmd, "migrate source deploy": migrateSourceDeployCmd,
	} {
		if cmd.Flags().Lookup("labels-file") == nil {
			t.Errorf("kates %s has no --labels-file", name)
		}
	}
}
