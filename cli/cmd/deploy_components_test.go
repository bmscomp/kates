package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/pkg/detect"
	"github.com/bmscomp/kates/cli/pkg/strimzi"
)

// TestSyncActiveContextKey checks the key kates deploy stores after a deploy.
// It follows the rule kates ports follows: the Secret's key goes only into a
// context without a key, or in place of a key kates stored there. It used to
// overwrite any key, an MCP context's agent-scoped one included.
func TestSyncActiveContextKey(t *testing.T) {
	const (
		adminKey = "admin-key-from-secret"
		agentKey = "agent-scoped-key"
	)
	// Key-sources are salted: a context whose key-source must stay as it was
	// shares this one string with its want.
	oldSource := secretKeySource("old")
	tests := []struct {
		name        string
		contexts    map[string]Context
		current     string
		flag        string // --context or KATES_CONTEXT
		secretErr   error
		wantOutcome deployKeyOutcome
		wantTarget  string   // the context the outcome is about
		wantCtx     *Context // that context afterwards, when it exists
		freshSource bool     // wantCtx's key-source is one kates just wrote for its key
		wantReport  string
	}{
		{
			name:        "stores the key in a context without one",
			contexts:    map[string]Context{"local": {URL: "http://localhost:8080", Output: "table"}},
			current:     "local",
			wantOutcome: deployKeyStored, wantTarget: "local",
			wantCtx: &Context{URL: "http://localhost:8080", Output: "table", APIKey: adminKey}, freshSource: true,
			wantReport: `API key from Secret kates-api-key (namespace kates) stored in context "local"`,
		},
		{
			name:        "replaces a key it stored before",
			contexts:    map[string]Context{"local": {URL: "u", APIKey: "old", KeySource: oldSource}},
			current:     "local",
			wantOutcome: deployKeyStored, wantTarget: "local",
			wantCtx: &Context{URL: "u", APIKey: adminKey}, freshSource: true,
			wantReport: `stored in context "local"`,
		},
		{
			name: "keeps an agent-scoped key in the current MCP context",
			contexts: map[string]Context{
				"mcp": {URL: "http://localhost:8080", Output: "json", APIKey: agentKey},
			},
			current:     "mcp",
			wantOutcome: deployKeyKept, wantTarget: "mcp",
			wantCtx:    &Context{URL: "http://localhost:8080", Output: "json", APIKey: agentKey},
			wantReport: `Context "mcp" keeps the API key it holds`,
		},
		{
			name:        "keeps a key typed over one it stored",
			contexts:    map[string]Context{"local": {URL: "u", APIKey: agentKey, KeySource: oldSource}},
			current:     "local",
			wantOutcome: deployKeyKept, wantTarget: "local",
			wantCtx:    &Context{URL: "u", APIKey: agentKey, KeySource: oldSource},
			wantReport: `Context "local" keeps the API key it holds`,
		},
		{
			name:        "leaves a context that already holds the Secret's key as it is",
			contexts:    map[string]Context{"local": {URL: "u", APIKey: adminKey}},
			current:     "local",
			wantOutcome: deployKeyPresent, wantTarget: "local",
			wantCtx:    &Context{URL: "u", APIKey: adminKey},
			wantReport: `Context "local" already holds the API key`,
		},
		{
			name: "writes the context --context names, not the current one",
			contexts: map[string]Context{
				"mcp": {URL: "u", APIKey: agentKey},
				"lab": {URL: "http://lab"},
			},
			current: "mcp", flag: "lab",
			wantOutcome: deployKeyStored, wantTarget: "lab",
			wantCtx: &Context{URL: "http://lab", APIKey: adminKey}, freshSource: true,
			wantReport: `stored in context "lab"`,
		},
		{
			name:     "a --context that does not exist stores nothing",
			contexts: map[string]Context{"local": {URL: "u"}},
			current:  "local", flag: "missing",
			wantOutcome: deployKeyNoContext, wantTarget: "missing",
			wantReport: `Context "missing" does not exist`,
		},
		{
			name:        "an unreadable Secret stores nothing",
			contexts:    map[string]Context{"local": {URL: "u"}},
			current:     "local",
			secretErr:   errors.New(`secrets "kates-api-key" is forbidden`),
			wantOutcome: deployKeySecretUnread,
			wantReport:  `No API key from Secret kates-api-key (namespace kates): secrets "kates-api-key" is forbidden`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			origExec, origFlag := runExecOutputFn, contextFlag
			t.Cleanup(func() { runExecOutputFn, contextFlag = origExec, origFlag })
			contextFlag = tt.flag
			runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
				if tt.secretErr != nil {
					return nil, tt.secretErr
				}
				return []byte(base64.StdEncoding.EncodeToString([]byte(adminKey))), nil
			}
			mustSaveConfig(t, Config{CurrentContext: tt.current, Contexts: tt.contexts})
			before := loadConfig()

			result := syncActiveContextKey(context.Background(), "kates")
			if result.SaveErr != nil {
				t.Fatalf("SaveErr = %v", result.SaveErr)
			}
			after := loadConfig()

			if result.Key != tt.wantOutcome {
				t.Errorf("outcome = %v, want %v", result.Key, tt.wantOutcome)
			}
			if result.Context != tt.wantTarget {
				t.Errorf("context = %q, want %q", result.Context, tt.wantTarget)
			}
			if after.CurrentContext != before.CurrentContext {
				t.Errorf("current context = %q, want it unchanged (%q)", after.CurrentContext, before.CurrentContext)
			}
			for name, want := range before.Contexts {
				if name == tt.wantTarget && tt.wantCtx != nil {
					want = *tt.wantCtx
					if tt.freshSource {
						got := after.Contexts[name]
						if !keySourceMatches(got.KeySource, got.APIKey) {
							t.Errorf("context %q key-source = %q, want one kates wrote for its key", name, got.KeySource)
						}
						want.KeySource = got.KeySource
					}
				}
				if got := after.Contexts[name]; !reflect.DeepEqual(got, want) {
					t.Errorf("context %q = %+v, want %+v", name, got, want)
				}
			}

			var buf bytes.Buffer
			printDeployKeySync(&buf, result, "kates")
			out := stripAnsi(buf.String())
			if !strings.Contains(out, tt.wantReport) {
				t.Errorf("report lacks %q:\n%s", tt.wantReport, out)
			}
			// The report used to print the key's first four characters.
			if strings.Contains(out, adminKey[:4]) || strings.Contains(out, agentKey[:4]) {
				t.Errorf("report shows part of a key:\n%s", out)
			}
		})
	}
}

func TestEnsureKubernetesNoProxy(t *testing.T) {
	tests := []struct {
		name          string
		inputNoProxy  string
		clusterDomain string
		want          string
	}{
		{
			name:          "adds kubernetes service names and loopback entries",
			inputNoProxy:  "example.com,localhost",
			clusterDomain: "cluster.local",
			want:          "example.com,localhost,127.0.0.1,::1,kubernetes,kubernetes.default,kubernetes.default.svc,kubernetes.default.svc.cluster.local",
		},
		{
			name:          "keeps existing entries and avoids case-insensitive duplicates",
			inputNoProxy:  "LOCALHOST,kubernetes.default.svc.cluster.local",
			clusterDomain: "cluster.local",
			want:          "LOCALHOST,kubernetes.default.svc.cluster.local,127.0.0.1,::1,kubernetes,kubernetes.default,kubernetes.default.svc",
		},
		{
			name:          "uses cluster.local when domain is empty",
			inputNoProxy:  "",
			clusterDomain: "",
			want:          "localhost,127.0.0.1,::1,kubernetes,kubernetes.default,kubernetes.default.svc,kubernetes.default.svc.cluster.local",
		},
		{
			name:          "respects non-default cluster domain",
			inputNoProxy:  "",
			clusterDomain: "corp.internal",
			want:          "localhost,127.0.0.1,::1,kubernetes,kubernetes.default,kubernetes.default.svc,kubernetes.default.svc.corp.internal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ensureKubernetesNoProxy(tt.inputNoProxy, tt.clusterDomain)
			if got != tt.want {
				t.Fatalf("ensureKubernetesNoProxy(%q, %q) = %q, want %q", tt.inputNoProxy, tt.clusterDomain, got, tt.want)
			}
		})
	}
}

func TestDetectDeployProxySettingsLoopbackHandling(t *testing.T) {
	for _, key := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(key, "")
	}
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:9000")
	t.Setenv("HTTPS_PROXY", "http://localhost:9000")

	remote := detectDeployProxySettings(false)
	if remote.http != "" || remote.https != "" {
		t.Fatalf("remote cluster must not receive developer loopback proxy URLs: %+v", remote)
	}

	kind := detectDeployProxySettings(true)
	if kind.http != "http://host.docker.internal:9000" || kind.https != "http://host.docker.internal:9000" {
		t.Fatalf("kind cluster proxy URLs were not rewritten: %+v", kind)
	}
}

func TestDeployNamespaceLabelMap(t *testing.T) {
	orig := deployNamespaceLabels
	t.Cleanup(func() { deployNamespaceLabels = orig })

	t.Setenv("KATES_NAMESPACE_LABELS", "gov.example.com/app-name=kates,gov.example.com/product-id=KATES1")
	deployNamespaceLabels = []string{
		"cyber.example.com/sensitivity=2",
		"gov.example.com/product-id=OVR123",
	}

	labels, err := deployNamespaceLabelMap()
	if err != nil {
		t.Fatalf("deployNamespaceLabelMap: %v", err)
	}
	want := map[string]string{
		"app.kubernetes.io/managed-by": "kates",
		"gov.example.com/app-name":        "kates",
		"gov.example.com/product-id":      "OVR123",
		"cyber.example.com/sensitivity":   "2",
	}
	if !reflect.DeepEqual(labels, want) {
		t.Fatalf("labels = %v, want %v", labels, want)
	}

	deployNamespaceLabels = []string{"bad"}
	if _, err := deployNamespaceLabelMap(); err == nil || !strings.Contains(err.Error(), "expected key=value") {
		t.Fatalf("invalid label error = %v, want key=value", err)
	}
}

func TestDeployNamespaceManifestIncludesLabels(t *testing.T) {
	got := deployNamespaceManifest("monitoring", map[string]string{
		"gov.example.com/product-id":      "KATES1",
		"cyber.example.com/sensitivity":   "2",
		"app.kubernetes.io/managed-by": "kates",
	})
	for _, want := range []string{
		"kind: Namespace",
		"name: monitoring",
		"    app.kubernetes.io/managed-by: \"kates\"",
		"    cyber.example.com/sensitivity: \"2\"",
		"    gov.example.com/product-id: \"KATES1\"",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("namespace manifest lacks %q:\n%s", want, got)
		}
	}
}

func TestEnsureDeployNamespaceLabelsExistingNamespaceWithoutApply(t *testing.T) {
	origOutput, origStdin, origCombined := runExecOutputFn, runExecStdinFn, runExecCombinedFn
	t.Cleanup(func() {
		runExecOutputFn, runExecStdinFn, runExecCombinedFn = origOutput, origStdin, origCombined
	})

	var calls []string
	runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return []byte("kafka"), nil
	}
	runExecStdinFn = func(_ context.Context, name string, args []string, _ string) error {
		t.Fatalf("existing namespace must not use stdin apply/create path: %s %s", name, strings.Join(args, " "))
		return nil
	}
	runExecCombinedFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil, nil
	}

	err := ensureDeployNamespace(context.Background(), "kafka", map[string]string{
		"gov.example.com/product-id":      "KATES1",
		"app.kubernetes.io/managed-by": "kates",
	})
	if err != nil {
		t.Fatalf("ensureDeployNamespace returned error: %v", err)
	}

	want := []string{
		"kubectl get namespace kafka --ignore-not-found -o jsonpath={.metadata.name}",
		"kubectl label namespace kafka app.kubernetes.io/managed-by=kates gov.example.com/product-id=KATES1 --overwrite",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestEnsureDeployNamespaceCreatesMissingNamespaceWithLabels(t *testing.T) {
	origOutput, origStdin, origCombined := runExecOutputFn, runExecStdinFn, runExecCombinedFn
	t.Cleanup(func() {
		runExecOutputFn, runExecStdinFn, runExecCombinedFn = origOutput, origStdin, origCombined
	})

	var calls []string
	var createdManifest string
	runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	runExecStdinFn = func(_ context.Context, name string, args []string, stdinData string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		createdManifest = stdinData
		return nil
	}
	runExecCombinedFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		t.Fatalf("missing namespace must be created from a labelled manifest, not labelled afterwards: %s %s", name, strings.Join(args, " "))
		return nil, nil
	}

	err := ensureDeployNamespace(context.Background(), "connect", map[string]string{
		"gov.example.com/product-id":      "KATES1",
		"app.kubernetes.io/managed-by": "kates",
	})
	if err != nil {
		t.Fatalf("ensureDeployNamespace returned error: %v", err)
	}

	want := []string{
		"kubectl get namespace connect --ignore-not-found -o jsonpath={.metadata.name}",
		"kubectl create -f -",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if !strings.Contains(createdManifest, "name: connect") || !strings.Contains(createdManifest, "gov.example.com/product-id: \"KATES1\"") {
		t.Fatalf("create manifest missing namespace labels:\n%s", createdManifest)
	}
}

func TestRunKatesHelmUpgradeRecoversFromPostgresImmutableStatefulSet(t *testing.T) {
	origHelm, origCombined := runHelmFn, runExecCombinedFn
	t.Cleanup(func() {
		runHelmFn, runExecCombinedFn = origHelm, origCombined
	})

	helmCalls := 0
	deletedStatefulSet := false
	runHelmFn = func(_ context.Context, args ...string) error {
		helmCalls++
		if helmCalls == 1 {
			return errors.New(`exit status 1: Error: server-side apply failed for object kates/kates-postgresql apps/v1, Kind=StatefulSet: StatefulSet.apps "kates-postgresql" is invalid: spec: Forbidden: updates to statefulset spec for fields other than 'replicas' are forbidden`)
		}
		return nil
	}
	runExecCombinedFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		got := name + " " + strings.Join(args, " ")
		want := "kubectl delete statefulset kates-postgresql -n kates --cascade=orphan --ignore-not-found"
		if got != want {
			t.Fatalf("delete command = %q, want %q", got, want)
		}
		deletedStatefulSet = true
		return nil, nil
	}

	err := runKatesHelmUpgrade(context.Background(), "kates", "upgrade", "--install", "kates", "charts/kates")
	if err != nil {
		t.Fatalf("runKatesHelmUpgrade returned error: %v", err)
	}
	if helmCalls != 2 {
		t.Fatalf("helm calls = %d, want 2", helmCalls)
	}
	if !deletedStatefulSet {
		t.Fatal("expected immutable StatefulSet recovery delete")
	}
}

func TestRunKatesHelmUpgradeDoesNotRecoverUnrelatedHelmErrors(t *testing.T) {
	origHelm, origCombined := runHelmFn, runExecCombinedFn
	t.Cleanup(func() {
		runHelmFn, runExecCombinedFn = origHelm, origCombined
	})

	helmCalls := 0
	runHelmFn = func(_ context.Context, args ...string) error {
		helmCalls++
		return errors.New("exit status 1: values failed schema validation")
	}
	runExecCombinedFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		t.Fatalf("unexpected recovery command: %s %s", name, strings.Join(args, " "))
		return nil, nil
	}

	err := runKatesHelmUpgrade(context.Background(), "kates", "upgrade", "--install", "kates", "charts/kates")
	if err == nil {
		t.Fatal("expected unrelated Helm error")
	}
	if helmCalls != 1 {
		t.Fatalf("helm calls = %d, want 1", helmCalls)
	}
}

func TestPrepareEmptyPreV1StrimziReplacement(t *testing.T) {
	origCombined := runExecCombinedFn
	t.Cleanup(func() { runExecCombinedFn = origCombined })

	var calls []string
	runExecCombinedFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil, nil
	}

	vp := &versionPlan{
		ReplaceEmptyPreV1CRDs: true,
		ReplaceCRDs:           []string{"kafkabridges.kafka.strimzi.io", "kafkas.kafka.strimzi.io"},
		primaryOp: &strimzi.Operator{
			Namespace:   "strimzi-operator",
			Name:        "strimzi-cluster-operator",
			HelmRelease: "strimzi-operator",
		},
	}
	if err := prepareEmptyPreV1StrimziReplacement(context.Background(), vp); err != nil {
		t.Fatalf("prepareEmptyPreV1StrimziReplacement returned error: %v", err)
	}
	want := []string{
		"helm uninstall strimzi-operator -n strimzi-operator --wait --timeout 5m",
		"kubectl label crd kafkabridges.kafka.strimzi.io kafkas.kafka.strimzi.io allowDeletion=true --overwrite",
		"kubectl delete crd kafkabridges.kafka.strimzi.io kafkas.kafka.strimzi.io --ignore-not-found --timeout=120s",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}

	calls = nil
	vp.primaryOp.HelmRelease = ""
	if err := prepareEmptyPreV1StrimziReplacement(context.Background(), vp); err == nil || !strings.Contains(err.Error(), "not owned by a discoverable Helm release") {
		t.Fatalf("want ownership error, got %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("unexpected commands without a Helm owner: %v", calls)
	}

	vp.primaryOp = nil
	if err := prepareEmptyPreV1StrimziReplacement(context.Background(), vp); err != nil {
		t.Fatalf("operator-free CRD replacement should delete CRDs only: %v", err)
	}
	want = []string{
		"kubectl label crd kafkabridges.kafka.strimzi.io kafkas.kafka.strimzi.io allowDeletion=true --overwrite",
		"kubectl delete crd kafkabridges.kafka.strimzi.io kafkas.kafka.strimzi.io --ignore-not-found --timeout=120s",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("operator-free calls = %v, want %v", calls, want)
	}
}

func TestApplyLocalStrimziCRDs(t *testing.T) {
	origRead, origStdin, origCombined := readStrimziCRDBundleFn, runExecStdinFn, runExecCombinedFn
	t.Cleanup(func() {
		readStrimziCRDBundleFn, runExecStdinFn, runExecCombinedFn = origRead, origStdin, origCombined
	})

	const chartPath = "/cache/strimzi-kafka-operator-1.2.0.tgz"
	const bundle = "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\n"
	var calls []string
	readStrimziCRDBundleFn = func(path string) (string, []string, error) {
		if path != chartPath {
			t.Fatalf("chart path = %q, want %q", path, chartPath)
		}
		return bundle, strimzi.RequiredCRDNames(), nil
	}
	runExecStdinFn = func(_ context.Context, name string, args []string, stdinData string) error {
		if stdinData != bundle {
			t.Fatalf("stdin bundle = %q, want %q", stdinData, bundle)
		}
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}
	runExecCombinedFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return []byte("customresourcedefinition.apiextensions.k8s.io/kafkas.kafka.strimzi.io condition met"), nil
	}

	if err := applyLocalStrimziCRDs(context.Background(), &versionPlan{ChartPath: chartPath}); err != nil {
		t.Fatalf("applyLocalStrimziCRDs returned error: %v", err)
	}

	wantWaitArgs := []string{"wait", "--for=condition=Established"}
	for _, crd := range strimzi.RequiredCRDNames() {
		wantWaitArgs = append(wantWaitArgs, "crd/"+crd)
	}
	wantWaitArgs = append(wantWaitArgs, "--timeout=180s")
	want := []string{
		"kubectl apply --server-side --force-conflicts -f -",
		"kubectl " + strings.Join(wantWaitArgs, " "),
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestApplyLocalStrimziCRDsRejectsMissingRequiredCRDs(t *testing.T) {
	origRead, origStdin, origCombined := readStrimziCRDBundleFn, runExecStdinFn, runExecCombinedFn
	t.Cleanup(func() {
		readStrimziCRDBundleFn, runExecStdinFn, runExecCombinedFn = origRead, origStdin, origCombined
	})

	var calls []string
	readStrimziCRDBundleFn = func(string) (string, []string, error) {
		return "kind: CustomResourceDefinition\n", []string{"kafkas.kafka.strimzi.io"}, nil
	}
	runExecStdinFn = func(_ context.Context, name string, args []string, _ string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}
	runExecCombinedFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil, nil
	}

	err := applyLocalStrimziCRDs(context.Background(), &versionPlan{ChartPath: "/cache/strimzi.tgz"})
	if err == nil || !strings.Contains(err.Error(), "does not contain required Strimzi CRDs") {
		t.Fatalf("error = %v, want missing required CRDs", err)
	}
	if len(calls) != 0 {
		t.Fatalf("kubectl should not run when bundle is incomplete: %v", calls)
	}
}

func TestDeployGroupAAppliesLocalStrimziCRDsAndDisablesHook(t *testing.T) {
	origRead, origStdin, origCombined, origHelm := readStrimziCRDBundleFn, runExecStdinFn, runExecCombinedFn, runHelmFn
	t.Cleanup(func() {
		readStrimziCRDBundleFn, runExecStdinFn, runExecCombinedFn, runHelmFn = origRead, origStdin, origCombined, origHelm
	})
	setDeployFlags(t, func() {
		deployWithStrimzi = true
		deployWithCertManager = false
		deployWithKyverno = false
	})

	const chartPath = "/cache/strimzi-kafka-operator-1.2.0.tgz"
	readStrimziCRDBundleFn = func(path string) (string, []string, error) {
		if path != chartPath {
			t.Fatalf("chart path = %q, want %q", path, chartPath)
		}
		return fakeStrimziCRDBundle(), strimzi.RequiredCRDNames(), nil
	}

	var stdinCalls, helmCalls []string
	runExecStdinFn = func(_ context.Context, name string, args []string, _ string) error {
		stdinCalls = append(stdinCalls, name+" "+strings.Join(args, " "))
		return nil
	}
	runExecCombinedFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		stdinCalls = append(stdinCalls, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	runHelmFn = func(_ context.Context, args ...string) error {
		helmCalls = append(helmCalls, strings.Join(args, " "))
		return nil
	}

	err := deployGroupA(&deployContext{
		ctx: context.Background(),
		report: &detect.DetectReport{
			Network: detect.NetworkInfo{ClusterDomain: "cluster.test"},
		},
		chartOverlay: func(dir string) string { return dir + "/values-generic.yaml" },
		advanceStep:  func() {},
		versions: &versionPlan{
			Scope:          scopeCluster,
			StrimziVersion: "1.2.0",
			ChartPath:      chartPath,
			ChartDir:       "/cache/wrapper/1.2.0",
		},
	})
	if err != nil {
		t.Fatalf("deployGroupA returned error: %v", err)
	}

	if !sliceContains(stdinCalls, "kubectl apply --server-side --force-conflicts -f -") {
		t.Fatalf("local Strimzi CRD apply did not run; calls = %v", stdinCalls)
	}
	if len(helmCalls) != 1 {
		t.Fatalf("helm calls = %v, want one Strimzi upgrade", helmCalls)
	}
	upgrade := helmCalls[0]
	for _, want := range []string{
		"upgrade --install strimzi-operator /cache/wrapper/1.2.0",
		"--set crdUpgrade.enabled=false",
		"--set strimziVersion=1.2.0",
		"--set strimzi-kafka-operator.kubernetesServiceDnsDomain=cluster.test",
	} {
		if !strings.Contains(upgrade, want) {
			t.Fatalf("helm upgrade lacks %q:\n%s", want, upgrade)
		}
	}
}

func TestIsKyvernoAvailable(t *testing.T) {
	origOutput := runExecOutputFn
	t.Cleanup(func() { runExecOutputFn = origOutput })

	runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if got := name + " " + strings.Join(args, " "); got != "kubectl -n kyverno get deploy -l app.kubernetes.io/part-of=kyverno -o json" {
			t.Fatalf("command = %q", got)
		}
		return []byte(`{"items":[
			{"metadata":{"name":"kyverno-admission-controller"},"spec":{"replicas":3},"status":{"readyReplicas":3}},
			{"metadata":{"name":"kyverno-background-controller"},"spec":{"replicas":2},"status":{"readyReplicas":2}},
			{"metadata":{"name":"kyverno-cleanup-controller"},"spec":{"replicas":2},"status":{"readyReplicas":2}},
			{"metadata":{"name":"kyverno-reports-controller"},"spec":{"replicas":2},"status":{"readyReplicas":2}}
		]}`), nil
	}
	if !isKyvernoAvailable(context.Background()) {
		t.Fatal("expected Kyverno to be available")
	}

	runExecOutputFn = func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"items":[
			{"metadata":{"name":"kyverno-admission-controller"},"spec":{"replicas":1},"status":{"readyReplicas":1}}
		]}`), nil
	}
	if isKyvernoAvailable(context.Background()) {
		t.Fatal("missing controllers must not count as available")
	}
}

func TestPrometheusOperatorCRDsAvailable(t *testing.T) {
	origOutput := runExecOutputFn
	t.Cleanup(func() { runExecOutputFn = origOutput })

	var got string
	runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		got = name + " " + strings.Join(args, " ")
		return []byte("crds are present"), nil
	}
	if !prometheusOperatorCRDsAvailable(context.Background()) {
		t.Fatal("expected Prometheus Operator CRDs to be available")
	}
	wantArgs := []string{"get", "crd"}
	wantArgs = append(wantArgs, prometheusOperatorCRDNames...)
	if want := "kubectl " + strings.Join(wantArgs, " "); got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}

	runExecOutputFn = func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("not found")
	}
	if prometheusOperatorCRDsAvailable(context.Background()) {
		t.Fatal("expected missing CRDs to be unavailable")
	}
}

func TestIsKafkaClusterReady(t *testing.T) {
	origOutput := runExecOutputFn
	t.Cleanup(func() { runExecOutputFn = origOutput })

	var got string
	runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		got = name + " " + strings.Join(args, " ")
		return []byte("True"), nil
	}

	if !isKafkaClusterReady(context.Background(), "kafka", "krafter") {
		t.Fatal("expected Kafka CR to be ready")
	}
	want := `kubectl get kafka krafter -n kafka -o jsonpath={.status.conditions[?(@.type=="Ready")].status}`
	if got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}

	runExecOutputFn = func(context.Context, string, ...string) ([]byte, error) {
		return []byte("False"), nil
	}
	if isKafkaClusterReady(context.Background(), "kafka", "krafter") {
		t.Fatal("expected Kafka CR without Ready=True to be not ready")
	}

	runExecOutputFn = func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("not found")
	}
	if isKafkaClusterReady(context.Background(), "kafka", "krafter") {
		t.Fatal("expected missing Kafka CR to be not ready")
	}
}

func TestKafkaConnectNeedsReconcile(t *testing.T) {
	origOutput := runExecOutputFn
	t.Cleanup(func() { runExecOutputFn = origOutput })

	ready := `{
		"spec": {
			"bootstrapServers": "krafter-kafka-bootstrap.kafka.svc.taz-tls-int-1.local:9092",
			"config": {"schema.registry.url": "http://registry"},
			"template": {"pod": {"dnsConfig": {"searches": ["kafka.svc.taz-tls-int-1.local"]}}}
		},
		"status": {"conditions": [{"type": "Ready", "status": "True"}]}
	}`
	runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		got := name + " " + strings.Join(args, " ")
		want := "kubectl get kafkaconnect connect-cluster -n connect -o json"
		if got != want {
			t.Fatalf("command = %q, want %q", got, want)
		}
		return []byte(ready), nil
	}
	needs, reason := kafkaConnectNeedsReconcile(context.Background(), "connect", "connect-cluster",
		"krafter-kafka-bootstrap.kafka.svc.taz-tls-int-1.local:9092", "kafka.svc.taz-tls-int-1.local")
	if needs || reason != "" {
		t.Fatalf("ready matching Connect should not reconcile: needs=%v reason=%q", needs, reason)
	}

	for _, tt := range []struct {
		name   string
		json   string
		reason string
	}{
		{
			name:   "not ready",
			json:   strings.Replace(ready, `"status": "True"`, `"status": "False"`, 1),
			reason: "not Ready",
		},
		{
			name:   "stale bootstrap",
			json:   strings.Replace(ready, "taz-tls-int-1.local:9092", "cluster.local:9092", 1),
			reason: "bootstrap",
		},
		{
			name:   "stale dns search",
			json:   strings.Replace(ready, `"searches": ["kafka.svc.taz-tls-int-1.local"]`, `"searches": ["kafka.svc.cluster.local"]`, 1),
			reason: "DNS search",
		},
		{
			name: "dotted config became nested map",
			json: `{
				"spec": {
					"bootstrapServers": "krafter-kafka-bootstrap.kafka.svc.taz-tls-int-1.local:9092",
					"config": {"schema": {"registry": {"url": "http://registry"}}},
					"template": {"pod": {"dnsConfig": {"searches": ["kafka.svc.taz-tls-int-1.local"]}}}
				},
				"status": {"conditions": [{"type": "Ready", "status": "False"}]}
			}`,
			reason: "nested map",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runExecOutputFn = func(context.Context, string, ...string) ([]byte, error) {
				return []byte(tt.json), nil
			}
			needs, reason := kafkaConnectNeedsReconcile(context.Background(), "connect", "connect-cluster",
				"krafter-kafka-bootstrap.kafka.svc.taz-tls-int-1.local:9092", "kafka.svc.taz-tls-int-1.local")
			if !needs || !strings.Contains(reason, tt.reason) {
				t.Fatalf("needs=%v reason=%q, want reason containing %q", needs, reason, tt.reason)
			}
		})
	}
}

func TestDeployKafkaConnectStackReconcilesExistingReleaseWhenKafkaConnectNotReady(t *testing.T) {
	origHelm, origOutput, origStdin, origCombined, origDeployed, origTesting := runHelmFn, runExecOutputFn, runExecStdinFn, runExecCombinedFn, isHelmReleaseDeployedFn, isTesting
	origDBNS, origHA, origMonitoring := deployDbNS, deployHA, deployWithMonitoring
	t.Cleanup(func() {
		runHelmFn, runExecOutputFn, runExecStdinFn, runExecCombinedFn, isHelmReleaseDeployedFn, isTesting = origHelm, origOutput, origStdin, origCombined, origDeployed, origTesting
		deployDbNS, deployHA, deployWithMonitoring = origDBNS, origHA, origMonitoring
	})

	isTesting = false
	deployDbNS = "database"
	deployHA = true
	deployWithMonitoring = true

	isHelmReleaseDeployedFn = func(_ context.Context, release, namespace string) bool {
		return release == "connect-cluster" && namespace == "connect-blue"
	}
	runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		joined := name + " " + strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "get secret kates-connect") && strings.Contains(joined, "jsonpath={.data.password}"):
			return []byte(base64.StdEncoding.EncodeToString([]byte("pw"))), nil
		case strings.Contains(joined, "get secret kates-connect") && strings.Contains(joined, "jsonpath={.data.sasl\\.jaas\\.config}"):
			return nil, nil
		case strings.Contains(joined, "get kafkaconnect connect-cluster -n connect-blue") && strings.Contains(joined, "jsonpath={.status.conditions"):
			return []byte("True"), nil
		case strings.Contains(joined, "get kafkaconnect connect-cluster -n connect-blue -o json"):
			return []byte(`{
				"spec": {
					"bootstrapServers": "krafter-kafka-bootstrap.kafka.svc.cluster.local:9092",
					"config": {"schema": {"registry": {"url": "http://registry"}}},
					"template": {"pod": {"dnsConfig": {"searches": ["kafka.svc.cluster.local"]}}}
				},
				"status": {"conditions": [{"type": "Ready", "status": "False"}]}
			}`), nil
		case strings.Contains(joined, "get kafkaconnector -n connect-blue -l strimzi.io/cluster=connect-cluster"):
			return []byte("True,True,"), nil
		default:
			return nil, nil
		}
	}
	runExecStdinFn = func(context.Context, string, []string, string) error {
		return nil
	}
	runExecCombinedFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if strings.Contains(name+" "+strings.Join(args, " "), "get kafkaconnector") {
			return []byte("exists"), nil
		}
		return nil, nil
	}

	var helmCalls []string
	runHelmFn = func(_ context.Context, args ...string) error {
		helmCalls = append(helmCalls, strings.Join(args, " "))
		return nil
	}

	err := deployKafkaConnectStack(&deployContext{
		ctx: context.Background(),
		ns:  resolvedNamespaces{kafka: "kafka", connect: "connect-blue"},
		report: &detect.DetectReport{
			Network: detect.NetworkInfo{ClusterDomain: "taz-tls-int-1.local"},
		},
		chartOverlay: func(dir string) string { return dir + "/values-generic.yaml" },
		advanceStep:  func() {},
		versions:     &versionPlan{KafkaVersion: "4.3.1"},
		primary:      primaryCluster{Name: "krafter", Namespace: "kafka"},
	})
	if err != nil {
		t.Fatalf("deployKafkaConnectStack returned error: %v", err)
	}

	if !sliceContainsPrefix(helmCalls, "dependency build charts/connect-cluster") {
		t.Fatalf("Connect chart dependencies were not built; helm calls = %v", helmCalls)
	}
	var upgrade string
	for _, call := range helmCalls {
		if strings.HasPrefix(call, "upgrade --install connect-cluster") {
			upgrade = call
			break
		}
	}
	if upgrade == "" {
		t.Fatalf("Connect release was not reconciled; helm calls = %v", helmCalls)
	}
	for _, want := range []string{
		"-n connect-blue",
		"--reset-values",
		"--set-string clusterDomain=taz-tls-int-1.local",
		"--set kafka.namespace=kafka",
		"--set-string kafka.bootstrapServers=krafter-kafka-bootstrap.kafka.svc.taz-tls-int-1.local:9092",
		"--set-string extraConfig.schema\\.registry\\.url=http://apicurio-apicurio-registry.kafka.svc.taz-tls-int-1.local:80/apis/ccompat/v7",
		"--set-string dnsConfig.searches[0]=kafka.svc.taz-tls-int-1.local",
		"--set-string version=4.3.1",
		"--set monitoring.podMonitor.enabled=true",
	} {
		if !strings.Contains(upgrade, want) {
			t.Fatalf("Connect helm upgrade lacks %q:\n%s", want, upgrade)
		}
	}
}

func TestDeployGroupBReconcilesKafkaWhenReleaseExistsButCRNotReady(t *testing.T) {
	origHelm, origOutput, origDeployed := runHelmFn, runExecOutputFn, isHelmReleaseDeployedFn
	t.Cleanup(func() {
		runHelmFn, runExecOutputFn, isHelmReleaseDeployedFn = origHelm, origOutput, origDeployed
	})
	setDeployFlags(t, func() {
		deployWithKafkaConnect = false
		deployWithMonitoring = false
		deployHA = true
	})

	isHelmReleaseDeployedFn = func(context.Context, string, string) bool {
		return true
	}
	runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		got := name + " " + strings.Join(args, " ")
		if got != `kubectl get kafka krafter -n kafka -o jsonpath={.status.conditions[?(@.type=="Ready")].status}` {
			t.Fatalf("unexpected read command: %q", got)
		}
		return []byte("False"), nil
	}

	var helmCalls []string
	runHelmFn = func(_ context.Context, args ...string) error {
		helmCalls = append(helmCalls, strings.Join(args, " "))
		return nil
	}

	err := deployGroupB(&deployContext{
		ctx:        context.Background(),
		ns:         resolvedNamespaces{kafka: "kafka", connect: "connect", jaeger: "monitoring"},
		valuesFile: ".build/values-detected.yaml",
		report: &detect.DetectReport{
			Network: detect.NetworkInfo{ClusterDomain: "cluster.local"},
		},
		advanceStep: func() {},
		versions:    stubVersionPlan(versionOptions{}),
		primary: primaryCluster{
			Name:      "krafter",
			Namespace: "kafka",
		},
	})
	if err != nil {
		t.Fatalf("deployGroupB returned error: %v", err)
	}

	if !sliceContainsPrefix(helmCalls, "dependency update charts/kafka-cluster") {
		t.Fatalf("Kafka chart dependencies were not updated; helm calls = %v", helmCalls)
	}
	if !sliceContainsPrefix(helmCalls, "upgrade --install krafter charts/kafka-cluster -n kafka") {
		t.Fatalf("Kafka release was not reconciled; helm calls = %v", helmCalls)
	}
}

func TestCleanupStaleKatesChartDependenciesRemovesArtifactsWhenChartHasNoDependencies(t *testing.T) {
	chartDir := t.TempDir()
	writeTestFile(t, filepath.Join(chartDir, "Chart.yaml"), "apiVersion: v2\nname: kates\n")
	writeTestFile(t, filepath.Join(chartDir, "Chart.lock"), "dependencies:\n- name: postgresql\n")
	writeTestFile(t, filepath.Join(chartDir, "charts", "postgresql-16.7.27.tgz"), "stale")

	if err := cleanupStaleKatesChartDependencies(chartDir); err != nil {
		t.Fatalf("cleanupStaleKatesChartDependencies returned error: %v", err)
	}
	for _, path := range []string{filepath.Join(chartDir, "Chart.lock"), filepath.Join(chartDir, "charts")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists after cleanup", path)
		}
	}
}

func TestCleanupStaleKatesChartDependenciesKeepsDeclaredDependencies(t *testing.T) {
	chartDir := t.TempDir()
	writeTestFile(t, filepath.Join(chartDir, "Chart.yaml"), "apiVersion: v2\nname: kates\ndependencies:\n- name: postgresql\n")
	writeTestFile(t, filepath.Join(chartDir, "Chart.lock"), "dependencies:\n- name: postgresql\n")
	writeTestFile(t, filepath.Join(chartDir, "charts", "postgresql-16.7.27.tgz"), "kept")

	if err := cleanupStaleKatesChartDependencies(chartDir); err != nil {
		t.Fatalf("cleanupStaleKatesChartDependencies returned error: %v", err)
	}
	for _, path := range []string{filepath.Join(chartDir, "Chart.lock"), filepath.Join(chartDir, "charts", "postgresql-16.7.27.tgz")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s missing after cleanup with declared dependencies: %v", path, err)
		}
	}
}

func writeTestFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func sliceContainsPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

// A sibling branch that fails cancels the deploy group's context while the
// ClusterIssuer goes in past the cert-manager webhook. The restore ran on that
// context, so its patches never ran and the webhook stayed fail-open.
func TestRestoreCertMgrWebhookPolicyOutlivesCancel(t *testing.T) {
	origExec, origDL := runExecFn, dl
	t.Cleanup(func() { runExecFn, dl = origExec, origDL })
	dl = &DashboardController{}
	var patched []string
	runExecFn = func(ctx context.Context, name string, args ...string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Errorf("restore %v runs with no deadline", args)
		}
		patched = append(patched, strings.Join(args, " "))
		return nil
	}

	gCtx, cancel := context.WithCancel(context.Background())
	cancel()
	restoreCertMgrWebhookPolicy(gCtx, []string{"mutatingwebhookconfiguration", "validatingwebhookconfiguration"})

	if len(patched) != 2 {
		t.Fatalf("patched %d webhook configs, want both: %q", len(patched), patched)
	}
	for i, kind := range []string{"mutatingwebhookconfiguration", "validatingwebhookconfiguration"} {
		if !strings.HasPrefix(patched[i], "patch "+kind+" cert-manager-webhook") || !strings.Contains(patched[i], `"value":"Fail"`) {
			t.Errorf("patch %d = %q, want failurePolicy Fail on %s", i, patched[i], kind)
		}
	}
}
