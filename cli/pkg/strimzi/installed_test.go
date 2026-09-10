package strimzi

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

const imagesEnv110 = `4.2.0=quay.io/strimzi/kafka:1.1.0-kafka-4.2.0\n4.2.1=quay.io/strimzi/kafka:1.1.0-kafka-4.2.1\n4.3.0=quay.io/strimzi/kafka:1.1.0-kafka-4.3.0\n`
const imagesEnv101 = `4.1.0=quay.io/strimzi/kafka:1.0.1-kafka-4.1.0\n4.1.1=quay.io/strimzi/kafka:1.0.1-kafka-4.1.1\n4.1.2=quay.io/strimzi/kafka:1.0.1-kafka-4.1.2\n4.2.0=quay.io/strimzi/kafka:1.0.1-kafka-4.2.0\n`

// operatorPod renders one item of "kubectl get pods -A -l
// strimzi.io/kind=cluster-operator -o json".
func operatorPod(namespace, deployment, hash, suffix string) string {
	return fmt.Sprintf(`{
  "apiVersion": "v1", "kind": "Pod",
  "metadata": {
    "name": "%[2]s-%[3]s-%[4]s", "namespace": "%[1]s",
    "labels": {"name": "strimzi-cluster-operator", "pod-template-hash": "%[3]s", "strimzi.io/kind": "cluster-operator"},
    "ownerReferences": [{"apiVersion": "apps/v1", "kind": "ReplicaSet", "name": "%[2]s-%[3]s", "controller": true}]
  },
  "spec": {"containers": [{"name": "strimzi-cluster-operator", "image": "quay.io/strimzi/operator:x"}]}
}`, namespace, deployment, hash, suffix)
}

func podList(pods ...string) string {
	return `{"apiVersion": "v1", "kind": "List", "items": [` + strings.Join(pods, ",") + `], "metadata": {"resourceVersion": ""}}`
}

// operatorDeployment renders "kubectl get deployment -o json" for a Cluster
// Operator; namespaceEnv is the JSON of the STRIMZI_NAMESPACE env entry
// without its name.
func operatorDeployment(namespace, name, image, namespaceEnv, imagesEnv string) string {
	env := `{"name": "STRIMZI_FULL_RECONCILIATION_INTERVAL_MS", "value": "120000"}`
	if namespaceEnv != "" {
		env = `{"name": "STRIMZI_NAMESPACE", ` + namespaceEnv + `}, ` + env
	}
	if imagesEnv != "" {
		env += `, {"name": "STRIMZI_KAFKA_IMAGES", "value": "` + imagesEnv + `"}`
	}
	return fmt.Sprintf(`{
  "apiVersion": "apps/v1", "kind": "Deployment",
  "metadata": {"name": "%s", "namespace": "%s", "labels": {"app": "strimzi", "component": "deployment"}},
  "spec": {
    "selector": {"matchLabels": {"name": "strimzi-cluster-operator", "strimzi.io/kind": "cluster-operator"}},
    "template": {
      "metadata": {"labels": {"name": "strimzi-cluster-operator", "strimzi.io/kind": "cluster-operator"}},
      "spec": {"containers": [{"name": "strimzi-cluster-operator", "image": "%s", "env": [%s]}]}
    }
  }
}`, name, namespace, image, env)
}

const helmListJSON = `[
{"name":"kates","namespace":"kates-stack","revision":"2","status":"deployed","chart":"kates-0.4.0","app_version":"0.4.0"},
{"name":"strimzi-operator","namespace":"strimzi-operator","revision":"3","status":"deployed","chart":"strimzi-operator-0.1.0","app_version":"1.1.0"},
{"name":"strimzi-operator","namespace":"kafka-legacy41","revision":"1","status":"deployed","chart":"strimzi-operator-0.1.0","app_version":"1.0.1"},
{"name":"kafka","namespace":"kafka","revision":"1","status":"deployed","chart":"kafka-cluster-0.9.0","app_version":"4.3.0"}
]`

func TestInstalledOperators(t *testing.T) {
	ctx := context.Background()
	listPods := "kubectl get pods -A -l strimzi.io/kind=cluster-operator -o json"
	helmList := "helm list -A -o json"

	t.Run("cluster-wide primary, co-located additional, foreign", func(t *testing.T) {
		r := newFakeRunner(t)
		r.outputs[listPods] = podList(
			operatorPod("strimzi-operator", "strimzi-cluster-operator", "5d8f9c7b6", "abcde"),
			operatorPod("kafka-legacy41", "strimzi-cluster-operator", "7f6d5c4b3", "zzzzz"),
			operatorPod("vendor-ops", "strimzi-cluster-operator", "1a2b3c4d5", "qqqqq"),
		)
		r.outputs["kubectl get deployment -n strimzi-operator strimzi-cluster-operator -o json"] =
			operatorDeployment("strimzi-operator", "strimzi-cluster-operator", "quay.io/strimzi/operator:1.1.0", `"value": "*"`, imagesEnv110)
		r.outputs["kubectl get deployment -n kafka-legacy41 strimzi-cluster-operator -o json"] =
			operatorDeployment("kafka-legacy41", "strimzi-cluster-operator", "quay.io/strimzi/operator:1.0.1", `"valueFrom": {"fieldRef": {"apiVersion": "v1", "fieldPath": "metadata.namespace"}}`, imagesEnv101)
		r.outputs["kubectl get deployment -n vendor-ops strimzi-cluster-operator -o json"] =
			operatorDeployment("vendor-ops", "strimzi-cluster-operator", "registry.vendor.example:5000/strimzi/operator:1.0.0", `"value": "team-a, team-b,"`, imagesEnv101)
		r.outputs[helmList] = helmListJSON

		ops, err := InstalledOperators(ctx, r)
		if err != nil {
			t.Fatalf("InstalledOperators: %v", err)
		}
		r.assertCalls(
			listPods,
			"kubectl get deployment -n strimzi-operator strimzi-cluster-operator -o json",
			"kubectl get deployment -n kafka-legacy41 strimzi-cluster-operator -o json",
			"kubectl get deployment -n vendor-ops strimzi-cluster-operator -o json",
			helmList,
		)
		if len(ops) != 3 {
			t.Fatalf("ops = %+v", ops)
		}
		legacy, primary, vendor := ops[0], ops[1], ops[2]

		if primary.Namespace != "strimzi-operator" || primary.Name != "strimzi-cluster-operator" || primary.Version != "1.1.0" ||
			primary.Image != "quay.io/strimzi/operator:1.1.0" || primary.Scope != ScopeCluster || !slices.Equal(primary.Watches, []string{"*"}) ||
			primary.Window.String() != "4.2.0 4.2.1 4.3.0" || primary.Release != "strimzi-operator" || primary.AppVersion != "1.1.0" || primary.IsForeign() {
			t.Errorf("primary = %+v", primary)
		}
		if legacy.Namespace != "kafka-legacy41" || legacy.Version != "1.0.1" || legacy.Scope != ScopeNamespaces ||
			!slices.Equal(legacy.Watches, []string{"kafka-legacy41"}) || legacy.Window.String() != "4.1.0 4.1.1 4.1.2 4.2.0" ||
			legacy.Release != "strimzi-operator" || legacy.AppVersion != "1.0.1" {
			t.Errorf("legacy = %+v", legacy)
		}
		if vendor.Namespace != "vendor-ops" || vendor.Version != "1.0.0" || vendor.Scope != ScopeNamespaces ||
			!slices.Equal(vendor.Watches, []string{"team-a", "team-b"}) || !vendor.IsForeign() || vendor.AppVersion != "" {
			t.Errorf("vendor = %+v", vendor)
		}
		if got := Shape(ops); got != ShapeMixed {
			t.Errorf("Shape = %q", got)
		}
	})

	t.Run("no operator", func(t *testing.T) {
		r := newFakeRunner(t)
		r.outputs[listPods] = podList()
		ops, err := InstalledOperators(ctx, r)
		if err != nil {
			t.Fatalf("InstalledOperators: %v", err)
		}
		if len(ops) != 0 {
			t.Errorf("ops = %+v", ops)
		}
		r.assertCalls(listPods)
		if got := Shape(ops); got != ShapeNone {
			t.Errorf("Shape = %q", got)
		}
	})

	t.Run("rolling update: two pods, one Deployment", func(t *testing.T) {
		r := newFakeRunner(t)
		r.outputs[listPods] = podList(
			operatorPod("strimzi-operator", "strimzi-cluster-operator", "5d8f9c7b6", "abcde"),
			operatorPod("strimzi-operator", "strimzi-cluster-operator", "6e9a0d8c7", "fghij"),
		)
		r.outputs["kubectl get deployment -n strimzi-operator strimzi-cluster-operator -o json"] =
			operatorDeployment("strimzi-operator", "strimzi-cluster-operator", "quay.io/strimzi/operator:1.1.0", `"value": "*"`, imagesEnv110)
		r.outputs[helmList] = `[]`
		ops, err := InstalledOperators(ctx, r)
		if err != nil {
			t.Fatalf("InstalledOperators: %v", err)
		}
		if len(ops) != 1 || !ops[0].IsForeign() {
			t.Errorf("ops = %+v", ops)
		}
		if got := Shape(ops); got != ShapeCluster {
			t.Errorf("Shape = %q", got)
		}
	})

	t.Run("pods without a ReplicaSet owner are ignored", func(t *testing.T) {
		r := newFakeRunner(t)
		bare := `{"metadata": {"name": "bare", "namespace": "x", "labels": {"strimzi.io/kind": "cluster-operator"}}}`
		r.outputs[listPods] = podList(bare)
		ops, err := InstalledOperators(ctx, r)
		if err != nil || len(ops) != 0 {
			t.Fatalf("ops = %+v, err = %v", ops, err)
		}
	})

	t.Run("errors", func(t *testing.T) {
		r := newFakeRunner(t)
		r.errors[listPods] = errors.New("kubectl: connection refused")
		if _, err := InstalledOperators(ctx, r); err == nil || !strings.Contains(err.Error(), "connection refused") {
			t.Errorf("pods error = %v", err)
		}

		r = newFakeRunner(t)
		r.outputs[listPods] = `{"items": [`
		if _, err := InstalledOperators(ctx, r); err == nil {
			t.Error("bad pod json: want error")
		}

		r = newFakeRunner(t)
		r.outputs[listPods] = podList(operatorPod("strimzi-operator", "strimzi-cluster-operator", "5d8f9c7b6", "abcde"))
		r.errors["kubectl get deployment -n strimzi-operator strimzi-cluster-operator -o json"] = errors.New("not found")
		if _, err := InstalledOperators(ctx, r); err == nil || !strings.Contains(err.Error(), "strimzi-operator/strimzi-cluster-operator") {
			t.Errorf("deployment error = %v", err)
		}

		r = newFakeRunner(t)
		r.outputs[listPods] = podList(operatorPod("strimzi-operator", "strimzi-cluster-operator", "5d8f9c7b6", "abcde"))
		r.outputs["kubectl get deployment -n strimzi-operator strimzi-cluster-operator -o json"] =
			operatorDeployment("strimzi-operator", "strimzi-cluster-operator", "quay.io/strimzi/operator:1.1.0", `"value": "*"`, imagesEnv110)
		r.errors[helmList] = errors.New("helm: boom")
		if _, err := InstalledOperators(ctx, r); err == nil || !strings.Contains(err.Error(), "boom") {
			t.Errorf("helm error = %v", err)
		}

		r.errors = map[string]error{}
		r.outputs[helmList] = `nope`
		if _, err := InstalledOperators(ctx, r); err == nil {
			t.Error("bad helm json: want error")
		}
	})
}

func TestParseOperatorDeployment(t *testing.T) {
	t.Run("watch list", func(t *testing.T) {
		op, err := ParseOperatorDeployment([]byte(operatorDeployment("strimzi-operator", "strimzi-cluster-operator", "quay.io/strimzi/operator:1.1.0", `"value": "kafka,connect"`, imagesEnv110)))
		if err != nil {
			t.Fatal(err)
		}
		if op.Scope != ScopeNamespaces || !slices.Equal(op.Watches, []string{"kafka", "connect"}) {
			t.Errorf("op = %+v", op)
		}
	})
	t.Run("no STRIMZI_NAMESPACE means own namespace", func(t *testing.T) {
		op, err := ParseOperatorDeployment([]byte(operatorDeployment("ops", "strimzi-cluster-operator", "quay.io/strimzi/operator:1.1.0", "", imagesEnv110)))
		if err != nil {
			t.Fatal(err)
		}
		if op.Scope != ScopeNamespaces || !slices.Equal(op.Watches, []string{"ops"}) {
			t.Errorf("op = %+v", op)
		}
	})
	t.Run("no image map leaves the window empty", func(t *testing.T) {
		op, err := ParseOperatorDeployment([]byte(operatorDeployment("ops", "strimzi-cluster-operator", "quay.io/strimzi/operator:1.1.0", `"value": "*"`, "")))
		if err != nil {
			t.Fatal(err)
		}
		if len(op.Window) != 0 || op.Scope != ScopeCluster {
			t.Errorf("op = %+v", op)
		}
	})
	t.Run("container found by env when renamed", func(t *testing.T) {
		j := strings.Replace(operatorDeployment("ops", "op", "quay.io/strimzi/operator:1.1.0", `"value": "*"`, imagesEnv110), `"name": "strimzi-cluster-operator", "image"`, `"name": "operator", "image"`, 1)
		op, err := ParseOperatorDeployment([]byte(j))
		if err != nil {
			t.Fatal(err)
		}
		if op.Version != "1.1.0" || len(op.Window) != 3 {
			t.Errorf("op = %+v", op)
		}
	})
	t.Run("no operator container", func(t *testing.T) {
		j := `{"metadata": {"name": "x", "namespace": "y"}, "spec": {"template": {"spec": {"containers": [{"name": "sidecar", "image": "busybox"}]}}}}`
		if _, err := ParseOperatorDeployment([]byte(j)); err == nil {
			t.Error("want error")
		}
	})
	t.Run("bad json", func(t *testing.T) {
		if _, err := ParseOperatorDeployment([]byte(`{`)); err == nil {
			t.Error("want error")
		}
	})
}

func TestShape(t *testing.T) {
	cluster := Operator{Scope: ScopeCluster}
	namespaced := Operator{Scope: ScopeNamespaces}
	tests := []struct {
		ops  []Operator
		want ScopeShape
	}{
		{nil, ShapeNone},
		{[]Operator{cluster}, ShapeCluster},
		{[]Operator{namespaced}, ShapeNamespaces},
		{[]Operator{namespaced, namespaced}, ShapeNamespaces},
		{[]Operator{cluster, namespaced}, ShapeMixed},
		{[]Operator{cluster, cluster}, ShapeCluster},
	}
	for _, tt := range tests {
		if got := Shape(tt.ops); got != tt.want {
			t.Errorf("Shape(%d ops) = %q, want %q", len(tt.ops), got, tt.want)
		}
	}
}

func TestUpgradeKind(t *testing.T) {
	tests := []struct {
		installed, requested, want string
		wantErr                    bool
	}{
		{"1.1.0", "1.1.0", UpgradeSame, false},
		{"1.0.1", "1.1.0", UpgradeUpgrade, false},
		{"1.1.0", "1.0.1", UpgradeDowngrade, false},
		{"0.51.0", "1.0.0", UpgradeUpgrade, false},
		{"1.1.0", "1.1.1", UpgradeUpgrade, false},
		{"1.1.0", "latest", "", true},
		{"", "1.1.0", "", true},
	}
	for _, tt := range tests {
		got, err := UpgradeKind(tt.installed, tt.requested)
		if tt.wantErr {
			if err == nil {
				t.Errorf("UpgradeKind(%q, %q) = %q, want error", tt.installed, tt.requested, got)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("UpgradeKind(%q, %q) = %q, %v; want %q", tt.installed, tt.requested, got, err, tt.want)
		}
	}
}

func TestStoredVersions(t *testing.T) {
	ctx := context.Background()
	labelled := "kubectl get crd -l app=strimzi -o json"
	all := "kubectl get crd -o json"
	crd := func(name string, stored ...string) string {
		return fmt.Sprintf(`{"metadata": {"name": "%s"}, "status": {"storedVersions": ["%s"]}}`, name, strings.Join(stored, `","`))
	}

	t.Run("labelled", func(t *testing.T) {
		r := newFakeRunner(t)
		r.outputs[labelled] = `{"items": [` + crd("kafkas.kafka.strimzi.io", "v1") + `,` + crd("strimzipodsets.core.strimzi.io", "v1beta2", "v1") + `,` + crd("other.example.com", "v9") + `]}`
		got, err := StoredVersions(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		r.assertCalls(labelled)
		if len(got) != 2 || !slices.Equal(got["kafkas.kafka.strimzi.io"], []string{"v1"}) || !slices.Equal(got["strimzipodsets.core.strimzi.io"], []string{"v1beta2", "v1"}) {
			t.Errorf("stored = %v", got)
		}
	})

	t.Run("fallback to every strimzi.io CRD", func(t *testing.T) {
		r := newFakeRunner(t)
		r.outputs[labelled] = `{"items": []}`
		r.outputs[all] = `{"items": [` + crd("kafkas.kafka.strimzi.io", "v1beta2") + `,` + crd("certificates.cert-manager.io", "v1") + `]}`
		got, err := StoredVersions(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		r.assertCalls(labelled, all)
		if len(got) != 1 || !slices.Equal(got["kafkas.kafka.strimzi.io"], []string{"v1beta2"}) {
			t.Errorf("stored = %v", got)
		}
	})

	t.Run("no CRDs at all", func(t *testing.T) {
		r := newFakeRunner(t)
		r.outputs[labelled] = `{"items": []}`
		r.outputs[all] = `{"items": []}`
		got, err := StoredVersions(ctx, r)
		if err != nil || len(got) != 0 {
			t.Errorf("stored = %v, err = %v", got, err)
		}
	})

	t.Run("errors", func(t *testing.T) {
		r := newFakeRunner(t)
		r.errors[labelled] = errors.New("forbidden")
		if _, err := StoredVersions(ctx, r); err == nil {
			t.Error("want error")
		}
		r = newFakeRunner(t)
		r.outputs[labelled] = `{"items": [}`
		if _, err := StoredVersions(ctx, r); err == nil {
			t.Error("bad json: want error")
		}
	})
}

func TestExecRunner(t *testing.T) {
	ctx := context.Background()
	var r ExecRunner
	out, err := r.Run(ctx, "go", "env", "GOOS")
	if err != nil {
		t.Skipf("go not runnable here: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Errorf("stdout = %q", out)
	}
	_, err = r.Run(ctx, "go", "env", "-nonsense-flag")
	if err == nil || !strings.Contains(err.Error(), "go env -nonsense-flag") {
		t.Errorf("err = %v", err)
	}
	if _, err := r.Run(ctx, "kates-no-such-binary-xyz"); err == nil {
		t.Error("missing binary: want error")
	}
	if _, err := RunnerFunc(func(context.Context, string, ...string) (string, error) { return "ok", nil }).Run(ctx, "x"); err != nil {
		t.Error(err)
	}
}
