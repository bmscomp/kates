package helm

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/internal/proc"
)

func TestBuildArgs(t *testing.T) {
	c := &Client{Namespace: "kafka"}
	args := c.buildArgs([]string{"status", "krafter"})
	if len(args) != 4 || args[0] != "-n" || args[1] != "kafka" {
		t.Errorf("expected -n kafka prepended, got %v", args)
	}

	c2 := &Client{}
	args2 := c2.buildArgs([]string{"list"})
	if len(args2) != 1 {
		t.Errorf("expected no extra args, got %v", args2)
	}
}

// New runs helm itself; NewWith runs it through the runner it is given, and
// every call carries the namespace.
func TestRun(t *testing.T) {
	f := (&proc.Fake{}).On("helm", "  v3.14.0 \n")
	c := NewWith(f, "kates")
	out, err := c.Run(context.Background(), "version", "--short")
	if err != nil || out != "v3.14.0" {
		t.Errorf("Run = %q, %v; want trimmed stdout", out, err)
	}
	if want := []string{"helm -n kates version --short"}; !reflect.DeepEqual(f.Lines(), want) {
		t.Errorf("calls = %q, want %q", f.Lines(), want)
	}

	f = (&proc.Fake{}).Fail("helm", "Error: Kubernetes cluster unreachable")
	out, err = NewWith(f, "").Run(context.Background(), "list")
	if err == nil || out != "" || !strings.Contains(err.Error(), "cluster unreachable") {
		t.Errorf("Run on failure = %q, %v; want the stderr in the error", out, err)
	}
	if New("x").runner != (proc.OS{}) {
		t.Errorf("New does not run helm with the OS runner")
	}
}

func TestStatus(t *testing.T) {
	for _, tt := range []struct {
		name   string
		script func(*proc.Fake)
		want   string
		errIs  error
		errHas string
	}{
		{"deployed", func(f *proc.Fake) { f.On("helm", `{"info":{"status":"deployed"}}`) }, "deployed", nil, ""},
		{"status is lower-cased", func(f *proc.Fake) { f.On("helm", `{"info":{"status":"Pending-Install"}}`) }, "pending-install", nil, ""},
		{"not found", func(f *proc.Fake) { f.Fail("helm", "Error: release: not found") }, "", ErrNotFound, "krafter"},
		{"not found, other wording", func(f *proc.Fake) { f.Fail("helm", "Error: release not found") }, "", ErrNotFound, ""},
		{"unreachable is an error, not absence", func(f *proc.Fake) { f.Fail("helm", "Error: Kubernetes cluster unreachable") }, "", nil, "cluster unreachable"},
		{"garbage", func(f *proc.Fake) { f.On("helm", "not json") }, "", nil, "helm status krafter"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := &proc.Fake{}
			tt.script(f)
			got, err := NewWith(f, "kafka").Status(context.Background(), "krafter")
			if got != tt.want {
				t.Errorf("Status = %q, want %q", got, tt.want)
			}
			if tt.errIs != nil && !errors.Is(err, tt.errIs) {
				t.Errorf("err = %v, want it to wrap %v", err, tt.errIs)
			}
			if tt.errIs == nil && tt.errHas == "" && err != nil {
				t.Errorf("err = %v, want nil", err)
			}
			if tt.errHas != "" && (err == nil || !strings.Contains(err.Error(), tt.errHas)) {
				t.Errorf("err = %v, want it to mention %q", err, tt.errHas)
			}
			if want := []string{"helm -n kafka status krafter -o json"}; !reflect.DeepEqual(f.Lines(), want) {
				t.Errorf("calls = %q, want %q", f.Lines(), want)
			}
		})
	}
}

// IsDeployed tells an absent release (false, nil) from a cluster that could
// not be asked (false, err). It used to answer false to both.
func TestIsDeployed(t *testing.T) {
	ctx := context.Background()
	ok, err := NewWith((&proc.Fake{}).On("helm", `{"info":{"status":"deployed"}}`), "").IsDeployed(ctx, "kates")
	if !ok || err != nil {
		t.Errorf("deployed: %v, %v", ok, err)
	}
	ok, err = NewWith((&proc.Fake{}).On("helm", `{"info":{"status":"failed"}}`), "").IsDeployed(ctx, "kates")
	if ok || err != nil {
		t.Errorf("failed release: %v, %v; want false, nil", ok, err)
	}
	ok, err = NewWith((&proc.Fake{}).Fail("helm", "Error: release: not found"), "").IsDeployed(ctx, "kates")
	if ok || err != nil {
		t.Errorf("absent: %v, %v; want false, nil", ok, err)
	}
	ok, err = NewWith((&proc.Fake{}).Fail("helm", "Error: Kubernetes cluster unreachable"), "").IsDeployed(ctx, "kates")
	if ok || err == nil {
		t.Errorf("unreachable: %v, %v; want false and the error", ok, err)
	}
}

func TestList(t *testing.T) {
	ctx := context.Background()
	const rows = `[{"name":"kates","namespace":"kates","revision":"3","updated":"2026-10-01","status":"deployed","chart":"kates-1.31.0","app_version":"1.31.0"},
		{"name":"strimzi-operator","namespace":"strimzi-operator","status":"failed","chart":"strimzi-kafka-operator-0.45.0"}]`

	f := (&proc.Fake{}).On("helm list -A -a -o json", rows)
	got, err := NewWith(f, "").List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []Release{
		{Name: "kates", Namespace: "kates", Revision: "3", Updated: "2026-10-01", Status: "deployed", Chart: "kates-1.31.0", AppVersion: "1.31.0"},
		{Name: "strimzi-operator", Namespace: "strimzi-operator", Status: "failed", Chart: "strimzi-kafka-operator-0.45.0"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("List = %+v, want %+v", got, want)
	}

	// With a namespace: -n, no -A; every state either way.
	f = (&proc.Fake{}).On("helm -n kafka list -a -o json", "[]")
	got, err = NewWith(f, "kafka").List(ctx)
	if err != nil || len(got) != 0 {
		t.Errorf("List in a namespace = %v, %v", got, err)
	}
	if want := []string{"helm -n kafka list -a -o json"}; !reflect.DeepEqual(f.Lines(), want) {
		t.Errorf("calls = %q, want %q", f.Lines(), want)
	}

	// Nothing at all is no releases, not a decode error.
	if got, err := NewWith((&proc.Fake{}).On("helm", "\n"), "").List(ctx); err != nil || len(got) != 0 {
		t.Errorf("List of empty output = %v, %v", got, err)
	}
	if _, err := NewWith((&proc.Fake{}).On("helm", "<html>"), "").List(ctx); err == nil || !strings.Contains(err.Error(), "helm list") {
		t.Errorf("List of garbage: err = %v", err)
	}
	if _, err := NewWith((&proc.Fake{}).Fail("helm", "Error: Kubernetes cluster unreachable"), "").List(ctx); err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("List when unreachable: err = %v", err)
	}
}

func TestDependencyBuild(t *testing.T) {
	f := &proc.Fake{}
	if err := NewWith(f, "").DependencyBuild(context.Background(), "charts/kates"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"helm dependency build charts/kates"}; !reflect.DeepEqual(f.Lines(), want) {
		t.Errorf("calls = %q, want %q", f.Lines(), want)
	}
	f = (&proc.Fake{}).Fail("helm", "Error: no repository definition for https://charts.example")
	if err := NewWith(f, "").DependencyBuild(context.Background(), "charts/kates"); err == nil || !strings.Contains(err.Error(), "no repository definition") {
		t.Errorf("err = %v", err)
	}
}

// Verbose prints the command helm is about to run.
func TestVerbose(t *testing.T) {
	c := NewWith((&proc.Fake{}).On("helm", "ok"), "kafka")
	c.Verbose = true
	if out, err := c.Run(context.Background(), "list"); err != nil || out != "ok" {
		t.Errorf("Run = %q, %v", out, err)
	}
}
