package strimzi

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
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

// operatorView is what TestInstalledOperators asserts of one Operator: every
// field, the window as a string and whether kates owns it.
type operatorView struct {
	Namespace, Name, Version, Image, Scope string
	Watches                                []string
	Window                                 string
	Release, AppVersion                    string
	HelmRelease, HelmChart                 string
	Foreign                                bool
}

func viewOperators(ops []Operator) []operatorView {
	out := make([]operatorView, 0, len(ops))
	for _, o := range ops {
		out = append(out, operatorView{
			Namespace: o.Namespace, Name: o.Name, Version: o.Version, Image: o.Image, Scope: o.Scope,
			Watches: o.Watches, Window: o.Window.String(),
			Release: o.Release, AppVersion: o.AppVersion,
			HelmRelease: o.HelmRelease, HelmChart: o.HelmChart,
			Foreign: o.IsForeign(),
		})
	}
	return out
}

const (
	listOperatorPods  = "kubectl get pods -A -l strimzi.io/kind=cluster-operator -o json"
	helmListAll       = "helm list -A -o json"
	getPrimaryDeploy  = "kubectl get deployment -n strimzi-operator strimzi-cluster-operator -o json"
	getLegacy41Deploy = "kubectl get deployment -n kafka-legacy41 strimzi-cluster-operator -o json"
	getVendorDeploy   = "kubectl get deployment -n vendor-ops strimzi-cluster-operator -o json"
)

// installedCase is one cluster InstalledOperators reads: what each command
// prints or fails with, and what it must report.
type installedCase struct {
	name    string
	outputs map[string]string
	errs    map[string]error
	// wantErr is a substring the error must contain; anyErr accepts any error.
	wantErr string
	anyErr  bool
	// wantCalls, when set, is the exact command sequence.
	wantCalls []string
	want      []operatorView
	wantShape ScopeShape
}

func installedOperatorCases() []installedCase {
	primaryPod := operatorPod("strimzi-operator", "strimzi-cluster-operator", "5d8f9c7b6", "abcde")
	primaryDeploy := operatorDeployment("strimzi-operator", "strimzi-cluster-operator", "quay.io/strimzi/operator:1.1.0", `"value": "*"`, imagesEnv110)
	primary := operatorView{
		Namespace: "strimzi-operator", Name: "strimzi-cluster-operator", Version: "1.1.0",
		Image: "quay.io/strimzi/operator:1.1.0", Scope: ScopeCluster, Watches: []string{"*"},
		Window: "4.2.0 4.2.1 4.3.0",
	}
	kated := primary
	kated.Release, kated.AppVersion = "strimzi-operator", "1.1.0"
	kated.HelmRelease, kated.HelmChart = "strimzi-operator", "strimzi-operator-0.1.0"
	foreign := primary
	foreign.Foreign = true
	upstream := primary
	upstream.Version, upstream.Image, upstream.Window = "0.48.0", "quay.io/strimzi/operator:0.48.0", "4.1.0 4.1.1 4.1.2 4.2.0"
	upstream.HelmRelease, upstream.HelmChart, upstream.Foreign = "strimzi-operator", "strimzi-kafka-operator-0.48.0", true
	return []installedCase{
		{
			name: "cluster-wide primary, co-located additional, foreign",
			outputs: map[string]string{
				listOperatorPods: podList(
					primaryPod,
					operatorPod("kafka-legacy41", "strimzi-cluster-operator", "7f6d5c4b3", "zzzzz"),
					operatorPod("vendor-ops", "strimzi-cluster-operator", "1a2b3c4d5", "qqqqq"),
				),
				getPrimaryDeploy:  primaryDeploy,
				getLegacy41Deploy: operatorDeployment("kafka-legacy41", "strimzi-cluster-operator", "quay.io/strimzi/operator:1.0.1", `"valueFrom": {"fieldRef": {"apiVersion": "v1", "fieldPath": "metadata.namespace"}}`, imagesEnv101),
				getVendorDeploy:   operatorDeployment("vendor-ops", "strimzi-cluster-operator", "registry.vendor.example:5000/strimzi/operator:1.0.0", `"value": "team-a, team-b,"`, imagesEnv101),
				helmListAll:       helmListJSON,
			},
			wantCalls: []string{listOperatorPods, getPrimaryDeploy, getLegacy41Deploy, getVendorDeploy, helmListAll},
			want: []operatorView{
				{
					Namespace: "kafka-legacy41", Name: "strimzi-cluster-operator", Version: "1.0.1",
					Image: "quay.io/strimzi/operator:1.0.1", Scope: ScopeNamespaces, Watches: []string{"kafka-legacy41"},
					Window: "4.1.0 4.1.1 4.1.2 4.2.0", Release: "strimzi-operator", AppVersion: "1.0.1",
					HelmRelease: "strimzi-operator", HelmChart: "strimzi-operator-0.1.0",
				},
				kated,
				{
					Namespace: "vendor-ops", Name: "strimzi-cluster-operator", Version: "1.0.0",
					Image: "registry.vendor.example:5000/strimzi/operator:1.0.0", Scope: ScopeNamespaces, Watches: []string{"team-a", "team-b"},
					Window: "4.1.0 4.1.1 4.1.2 4.2.0", Foreign: true,
				},
			},
			wantShape: ShapeMixed,
		},
		{
			name:      "no operator",
			outputs:   map[string]string{listOperatorPods: podList()},
			wantCalls: []string{listOperatorPods},
			want:      []operatorView{},
			wantShape: ShapeNone,
		},
		{
			name: "upstream Helm release is discoverable but still foreign to kates",
			outputs: map[string]string{
				listOperatorPods: podList(primaryPod),
				getPrimaryDeploy: operatorDeployment("strimzi-operator", "strimzi-cluster-operator", "quay.io/strimzi/operator:0.48.0", `"value": "*"`, imagesEnv101),
				helmListAll:      `[{"name":"strimzi-operator","namespace":"strimzi-operator","chart":"strimzi-kafka-operator-0.48.0","app_version":"0.48.0"}]`,
			},
			want: []operatorView{upstream},
		},
		{
			name: "rolling update: two pods, one Deployment",
			outputs: map[string]string{
				listOperatorPods: podList(primaryPod, operatorPod("strimzi-operator", "strimzi-cluster-operator", "6e9a0d8c7", "fghij")),
				getPrimaryDeploy: primaryDeploy,
				helmListAll:      `[]`,
			},
			want:      []operatorView{foreign},
			wantShape: ShapeCluster,
		},
		{
			name: "pods without a ReplicaSet owner are ignored",
			outputs: map[string]string{
				listOperatorPods: podList(`{"metadata": {"name": "bare", "namespace": "x", "labels": {"strimzi.io/kind": "cluster-operator"}}}`),
			},
			want: []operatorView{},
		},
		{
			name:    "pods error",
			errs:    map[string]error{listOperatorPods: errors.New("kubectl: connection refused")},
			wantErr: "connection refused",
		},
		{
			name:    "bad pod json",
			outputs: map[string]string{listOperatorPods: `{"items": [`},
			anyErr:  true,
		},
		{
			name:    "deployment error",
			outputs: map[string]string{listOperatorPods: podList(primaryPod)},
			errs:    map[string]error{getPrimaryDeploy: errors.New("not found")},
			wantErr: "strimzi-operator/strimzi-cluster-operator",
		},
		{
			name:    "helm error",
			outputs: map[string]string{listOperatorPods: podList(primaryPod), getPrimaryDeploy: primaryDeploy},
			errs:    map[string]error{helmListAll: errors.New("helm: boom")},
			wantErr: "boom",
		},
		{
			name:    "bad helm json",
			outputs: map[string]string{listOperatorPods: podList(primaryPod), getPrimaryDeploy: primaryDeploy, helmListAll: `nope`},
			anyErr:  true,
		},
	}
}

func TestInstalledOperators(t *testing.T) {
	for _, tt := range installedOperatorCases() {
		t.Run(tt.name, func(t *testing.T) {
			r := newFakeRunner(t)
			maps.Copy(r.outputs, tt.outputs)
			maps.Copy(r.errors, tt.errs)
			ops, err := InstalledOperators(context.Background(), r)
			if tt.anyErr || tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("InstalledOperators error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("InstalledOperators: %v", err)
			}
			if tt.wantCalls != nil {
				r.assertCalls(tt.wantCalls...)
			}
			if got := viewOperators(ops); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ops =\n%+v\nwant\n%+v", got, tt.want)
			}
			if tt.wantShape != "" {
				if got := Shape(ops); got != tt.wantShape {
					t.Errorf("Shape = %q, want %q", got, tt.wantShape)
				}
			}
		})
	}
}

func TestResourceInstances(t *testing.T) {
	ctx := context.Background()

	t.Run("lists instances for CRD names", func(t *testing.T) {
		r := newFakeRunner(t)
		r.outputs["kubectl get kafkas.kafka.strimzi.io -A -o json --ignore-not-found"] = `{"items":[{"kind":"Kafka","metadata":{"namespace":"kafka","name":"krafter"}}]}`
		r.outputs["kubectl get kafkatopics.kafka.strimzi.io -A -o json --ignore-not-found"] = `{"items":[]}`

		got, err := ResourceInstances(ctx, r, []string{"kafkatopics.kafka.strimzi.io", "kafkas.kafka.strimzi.io"})
		if err != nil {
			t.Fatal(err)
		}
		r.assertCalls(
			"kubectl get kafkas.kafka.strimzi.io -A -o json --ignore-not-found",
			"kubectl get kafkatopics.kafka.strimzi.io -A -o json --ignore-not-found",
		)
		if len(got) != 1 || got[0].CRD != "kafkas.kafka.strimzi.io" || got[0].Ref() != "kafka/Kafka/krafter" {
			t.Errorf("instances = %+v", got)
		}
	})

	t.Run("empty output means no instances", func(t *testing.T) {
		r := newFakeRunner(t)
		r.outputs["kubectl get kafkas.kafka.strimzi.io -A -o json --ignore-not-found"] = ""
		got, err := ResourceInstances(ctx, r, []string{"kafkas.kafka.strimzi.io"})
		if err != nil || len(got) != 0 {
			t.Errorf("instances = %+v, err = %v", got, err)
		}
	})

	t.Run("errors", func(t *testing.T) {
		r := newFakeRunner(t)
		r.errors["kubectl get kafkas.kafka.strimzi.io -A -o json --ignore-not-found"] = errors.New("forbidden")
		if _, err := ResourceInstances(ctx, r, []string{"kafkas.kafka.strimzi.io"}); err == nil || !strings.Contains(err.Error(), "forbidden") {
			t.Errorf("want list error, got %v", err)
		}

		r = newFakeRunner(t)
		r.outputs["kubectl get kafkas.kafka.strimzi.io -A -o json --ignore-not-found"] = `{"items":[}`
		if _, err := ResourceInstances(ctx, r, []string{"kafkas.kafka.strimzi.io"}); err == nil {
			t.Error("bad json: want error")
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
