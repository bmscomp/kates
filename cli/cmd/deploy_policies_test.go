package cmd

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// usePoliciesFile points --policies-file at a file holding content in a temp
// dir, and kubectl's current context at env.
func usePoliciesFile(t *testing.T, content, env string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".policies")
	if content != "" {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	origPath, origOutput := policiesFilePath, runExecOutputFn
	t.Cleanup(func() { policiesFilePath, runExecOutputFn = origPath, origOutput })
	policiesFilePath = path
	runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "kubectl" && strings.Join(args, " ") == "config current-context" {
			return []byte(env + "\n"), nil
		}
		t.Fatalf("unexpected %s %q", name, args)
		return nil, nil
	}
	return path
}

func TestPoliciesFileIsRefusedWhenItIsWrong(t *testing.T) {
	for _, c := range []struct{ content, want string }{
		{"environments:\n  prod:\n    podLabels:\n      kubernetes.io/team: a\n", "environments.prod.podLabels"},
		{"environments:\n  prod:\n    namespaceLabels:\n      team: \"not ok\"\n", "environments.prod.namespaceLabels"},
		{"environments:\n  prod:\n    values:\n      nginx: {}\n", "values.nginx: not a chart kates deploy installs"},
		{"environments:\n  prod:\n    podLabel:\n      team: a\n", "podLabel"},
	} {
		usePoliciesFile(t, c.content, "prod")
		if _, err := readPoliciesDoc(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: err %v, want %q", c.content, err, c.want)
		}
	}
}

// A --policies-file must exist; the default ./.policies need not.
func TestPoliciesFileNamedMustExist(t *testing.T) {
	usePoliciesFile(t, "", "prod")
	if _, err := readPoliciesDoc(); err == nil {
		t.Error("a missing --policies-file was read as empty")
	}
	policiesFilePath = ""
	t.Chdir(t.TempDir())
	if doc, err := readPoliciesDoc(); err != nil || doc.Environments != nil {
		t.Errorf("missing ./.policies: %+v, %v", doc, err)
	}
}

// Pod labels go under every value that labels the chart's pods; one the
// chart's values already set there is kept.
func TestPolicyChartValuesLabelEveryPod(t *testing.T) {
	p := deployPolicies{
		PodLabels: map[string]string{"team": "payments", "tier": "data"},
		Values: map[string]map[string]any{"monitoring": {
			"podLabels":             map[string]any{"team": "observability"},
			"kube-prometheus-stack": map[string]any{"grafana": map[string]any{"replicas": 1}},
			"global":                map[string]any{"imageRegistry": "registry.example"},
		}},
	}
	got := p.chartValues("monitoring")
	if got["global"].(map[string]any)["imageRegistry"] != "registry.example" {
		t.Errorf("values lost: %v", got)
	}
	if pl := got["podLabels"].(map[string]any); pl["team"] != "observability" || pl["tier"] != "data" {
		t.Errorf("podLabels = %v", pl)
	}
	for _, key := range chartPodLabelKeys("monitoring")[1:] {
		m := got
		for _, part := range strings.Split(key, ".") {
			m = m[part].(map[string]any)
		}
		if m["team"] != "payments" || m["tier"] != "data" {
			t.Errorf("%s = %v", key, m)
		}
	}
	if gr := got["kube-prometheus-stack"].(map[string]any)["grafana"].(map[string]any); gr["replicas"] != 1 {
		t.Errorf("grafana values lost: %v", gr)
	}
	// The policies' own values are untouched.
	if _, ok := p.Values["monitoring"]["kube-prometheus-stack"].(map[string]any)["commonLabels"]; ok {
		t.Error("chartValues wrote into the policies' values")
	}
	if got := (deployPolicies{}).chartValues("kates"); len(got) != 0 {
		t.Errorf("empty policies: %v", got)
	}
}

func TestDeployPolicyValuesFilesOnlyForChartsTheySet(t *testing.T) {
	dir := t.TempDir()
	args, err := writeDeployPolicyValues(deployPolicies{Values: map[string]map[string]any{"kates": {"replicas": 2}}},
		filepath.Join(dir, "values-detected.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{"kates": {"-f", filepath.Join(dir, "kates-policies.yaml")}}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	info, err := os.Stat(want["kates"][1])
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("values file: %v, %v", info, err)
	}

	args, err = writeDeployPolicyValues(deployPolicies{PodLabels: map[string]string{"team": "a"}}, filepath.Join(dir, "v.yaml"))
	if err != nil || len(args) != len(deployCharts) {
		t.Errorf("pod labels reach %d of %d charts: %v", len(args), len(deployCharts), err)
	}
}

// Saving one environment keeps the others, and the file is its owner's only.
func TestSaveDeployPoliciesKeepsOtherEnvironments(t *testing.T) {
	path := usePoliciesFile(t, "environments:\n  staging:\n    podLabels:\n      team: staging\n", "prod")
	if _, err := saveDeployPolicies("prod", deployPolicies{PodLabels: map[string]string{"team": "prod"}}); err != nil {
		t.Fatal(err)
	}
	doc, err := readPoliciesDoc()
	if err != nil {
		t.Fatal(err)
	}
	if doc.Environments["staging"].PodLabels["team"] != "staging" || doc.Environments["prod"].PodLabels["team"] != "prod" {
		t.Errorf("environments = %+v", doc.Environments)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode().Perm())
	}
}

// .labels, then the current context's namespaceLabels, then the env, then
// the flag.
func TestNamespaceLabelsFromPoliciesOfTheCurrentContext(t *testing.T) {
	labelsPath := writeLabelsFile(t, "a=file\nb=file\n")
	useLabelsFile(t, labelsPath)
	policies := usePoliciesFile(t, "environments:\n  prod:\n    namespaceLabels:\n      b: policies\n      c: policies\n  dev:\n    namespaceLabels:\n      d: dev\n", "prod")
	deployNamespaceLabels = []string{"c=flag"}

	labels, sources, err := namespaceUserLabels()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"a": "file", "b": "policies", "c": "flag"}
	if !reflect.DeepEqual(labels, want) {
		t.Errorf("labels = %v, want %v", labels, want)
	}
	if sources["b"] != policies+": environments.prod" {
		t.Errorf("source of b = %q", sources["b"])
	}
}
