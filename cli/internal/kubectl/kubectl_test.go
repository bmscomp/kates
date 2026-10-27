package kubectl

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/internal/proc"
)

func TestBuildArgs(t *testing.T) {
	for _, tt := range []struct {
		name   string
		client Client
		args   []string
		want   []string
	}{
		{"context is prepended", Client{Context: "test-ctx"},
			[]string{"get", "pods"}, []string{"--context", "test-ctx", "get", "pods"}},
		{"nothing set adds nothing", Client{},
			[]string{"get", "pods"}, []string{"get", "pods"}},
		{"namespace goes last", Client{Namespace: "kafka"},
			[]string{"get", "pods"}, []string{"get", "pods", "-n", "kafka"}},
		{"both", Client{Namespace: "kafka", Context: "test-ctx"},
			[]string{"get", "pods"}, []string{"--context", "test-ctx", "get", "pods", "-n", "kafka"}},
		{"a call that names its namespace keeps it", Client{Namespace: "kafka"},
			[]string{"get", "pods", "-n", "connect"}, []string{"get", "pods", "-n", "connect"}},
		{"--namespace= form", Client{Namespace: "kafka"},
			[]string{"get", "pods", "--namespace=connect"}, []string{"get", "pods", "--namespace=connect"}},
		{"all namespaces wins", Client{Namespace: "kafka"},
			[]string{"get", "kafkaconnect", "-A"}, []string{"get", "kafkaconnect", "-A"}},
		{"--all-namespaces wins", Client{Namespace: "kafka"},
			[]string{"get", "pods", "--all-namespaces"}, []string{"get", "pods", "--all-namespaces"}},
		{"before the container's command line", Client{Namespace: "database"},
			[]string{"exec", "postgresql-0", "--", "psql", "-n", "x"},
			[]string{"exec", "postgresql-0", "-n", "database", "--", "psql", "-n", "x"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			in := make([]string, len(tt.args), len(tt.args)+4)
			copy(in, tt.args)
			got := tt.client.buildArgs(in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buildArgs(%q) = %q, want %q", tt.args, got, tt.want)
			}
			if !reflect.DeepEqual(in, tt.args) {
				t.Errorf("buildArgs modified its input: %q", in)
			}
		})
	}
}

func TestRunOutputCombined(t *testing.T) {
	ctx := context.Background()
	f := (&proc.Fake{}).On("kubectl get", "  pod-1   Running \n")
	c := NewWith(f, "kafka")

	if out, err := c.Run(ctx, "get", "pods", "--no-headers"); err != nil || out != "pod-1   Running" {
		t.Errorf("Run = %q, %v; want trimmed stdout", out, err)
	}
	if out, err := c.Output(ctx, "get", "pods"); err != nil || string(out) != "  pod-1   Running \n" {
		t.Errorf("Output = %q, %v; want raw stdout", out, err)
	}
	if out, err := c.Combined(ctx, "get", "pods"); err != nil || string(out) != "  pod-1   Running \n" {
		t.Errorf("Combined = %q, %v", out, err)
	}
	want := []string{
		"kubectl get pods --no-headers -n kafka",
		"kubectl get pods -n kafka",
		"kubectl get pods -n kafka",
	}
	if !reflect.DeepEqual(f.Lines(), want) {
		t.Errorf("calls = %q, want %q", f.Lines(), want)
	}

	f = (&proc.Fake{}).Fail("kubectl", "Unable to connect to the server: dial tcp: i/o timeout")
	out, err := NewWith(f, "").Run(ctx, "get", "pods")
	if err == nil || out != "" || !strings.Contains(err.Error(), "Unable to connect") {
		t.Errorf("Run on failure = %q, %v; want the stderr in the error", out, err)
	}
	if New("x").runner != (proc.OS{}) {
		t.Errorf("New does not run kubectl with the OS runner")
	}
}

func TestJSON(t *testing.T) {
	ctx := context.Background()
	f := (&proc.Fake{}).On("kubectl get kafkaconnector", `{"metadata":{"name":"src"},"spec":{"tasksMax":2}}`)
	var got struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Spec struct {
			TasksMax int `json:"tasksMax"`
		} `json:"spec"`
	}
	if err := NewWith(f, "connect").JSON(ctx, &got, "get", "kafkaconnector", "src"); err != nil {
		t.Fatal(err)
	}
	if got.Metadata.Name != "src" || got.Spec.TasksMax != 2 {
		t.Errorf("decoded %+v", got)
	}
	if want := []string{"kubectl get kafkaconnector src -o json -n connect"}; !reflect.DeepEqual(f.Lines(), want) {
		t.Errorf("calls = %q, want %q", f.Lines(), want)
	}

	err := NewWith((&proc.Fake{}).On("kubectl", "No resources found"), "").JSON(ctx, &got, "get", "kafkaconnector", "src", "-n", "x")
	if err == nil || !strings.Contains(err.Error(), "kubectl get kafkaconnector src: decoding its JSON") {
		t.Errorf("JSON of non-JSON: err = %v", err)
	}
	err = NewWith((&proc.Fake{}).Fail("kubectl", `Error from server (NotFound): kafkaconnectors.kafka.strimzi.io "src" not found`), "").JSON(ctx, &got, "get", "kafkaconnector", "src")
	if err == nil || !IsNotFound(err) {
		t.Errorf("JSON of a missing resource: err = %v, IsNotFound false", err)
	}
}

// Exists and CRDExists are true only when kubectl answered; IsNotFound tells
// a missing resource from a failure to ask.
func TestExistsAndNotFound(t *testing.T) {
	ctx := context.Background()
	f := (&proc.Fake{}).
		On("kubectl get crd clusterpolicies.kyverno.io", "clusterpolicies.kyverno.io 2026-01-01").
		Fail("kubectl get crd", `Error from server (NotFound): customresourcedefinitions.apiextensions.k8s.io "x" not found`).
		On("kubectl get deployment kates", "kates 1/1").
		Fail("kubectl", "Unable to connect to the server")
	c := NewWith(f, "kates")

	if !c.CRDExists(ctx, "clusterpolicies.kyverno.io") {
		t.Error("an installed CRD reported absent")
	}
	if c.CRDExists(ctx, "x") {
		t.Error("a missing CRD reported present")
	}
	if !c.Exists(ctx, "deployment", "kates") {
		t.Error("an existing deployment reported absent")
	}
	if c.Exists(ctx, "deployment", "other") {
		t.Error("an unreachable cluster reported the deployment present")
	}
	want := []string{
		"kubectl get crd clusterpolicies.kyverno.io --no-headers -n kates",
		"kubectl get crd x --no-headers -n kates",
		"kubectl get deployment kates --no-headers -n kates",
		"kubectl get deployment other --no-headers -n kates",
	}
	if !reflect.DeepEqual(f.Lines(), want) {
		t.Errorf("calls = %q, want %q", f.Lines(), want)
	}

	_, err := c.Run(ctx, "get", "crd", "x")
	if !IsNotFound(err) {
		t.Errorf("IsNotFound(%v) = false", err)
	}
	_, err = c.Run(ctx, "get", "deployment", "other")
	if IsNotFound(err) {
		t.Errorf("IsNotFound(%v) = true for an unreachable cluster", err)
	}
	if IsNotFound(nil) {
		t.Error("IsNotFound(nil) = true")
	}
}

func TestApply(t *testing.T) {
	ctx := context.Background()
	f := &proc.Fake{}
	c := NewWith(f, "kates")
	if err := c.Apply(ctx, "kind: ConfigMap\n", ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Apply(ctx, "kind: Secret\n", "other"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"kubectl apply -f - -n kates",
		"kubectl apply -f - -n other",
	}
	if !reflect.DeepEqual(f.Lines(), want) {
		t.Errorf("calls = %q, want %q", f.Lines(), want)
	}
	if calls := f.Calls(); calls[0].Stdin != "kind: ConfigMap\n" || calls[1].Stdin != "kind: Secret\n" {
		t.Errorf("manifests on stdin = %q, %q", calls[0].Stdin, calls[1].Stdin)
	}

	f = (&proc.Fake{}).Fail("kubectl apply", "error validating data: unknown field")
	err := NewWith(f, "").Apply(ctx, "kind: Nope", "")
	if err == nil || !strings.HasPrefix(err.Error(), "kubectl apply: ") || !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("Apply on failure: err = %v", err)
	}
}

// The context and namespace reach every call, the OS runner included.
func TestContextAndNamespaceOnEveryCall(t *testing.T) {
	ctx := context.Background()
	f := &proc.Fake{}
	c := NewWith(f, "kafka")
	c.Context = "kind-panda"
	c.Verbose = true
	_, _ = c.Run(ctx, "get", "pods")
	_, _ = c.Output(ctx, "get", "pods")
	_, _ = c.Combined(ctx, "run", "x", "--", "sh")
	_ = c.JSON(ctx, new(map[string]any), "get", "pod", "x")
	_ = c.Apply(ctx, "x", "")
	_ = c.Exists(ctx, "pod", "x")
	_ = c.CRDExists(ctx, "x")
	for _, line := range f.Lines() {
		if !strings.HasPrefix(line, "kubectl --context kind-panda ") || !strings.Contains(line, " -n kafka") {
			t.Errorf("call %q lacks the context or the namespace", line)
		}
	}
	if len(f.Lines()) != 7 {
		t.Errorf("%d calls, want 7", len(f.Lines()))
	}
}
