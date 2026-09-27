package cmd

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestWithKubeContext(t *testing.T) {
	for _, tt := range []struct {
		name        string
		kubeContext string
		cmd         string
		args        []string
		want        []string
	}{
		{"kubectl gets --context last", "kind-panda", "kubectl",
			[]string{"get", "pods", "-n", "kafka"},
			[]string{"get", "pods", "-n", "kafka", "--context=kind-panda"}},
		{"helm gets --kube-context last", "kind-panda", "helm",
			[]string{"upgrade", "--install", "kates", "charts/kates", "-n", "kates"},
			[]string{"upgrade", "--install", "kates", "charts/kates", "-n", "kates", "--kube-context=kind-panda"}},
		{"before the container's command line", "kind-panda", "kubectl",
			[]string{"exec", "-n", "database", "postgresql-0", "--", "psql", "-c", "select 1"},
			[]string{"exec", "-n", "database", "postgresql-0", "--context=kind-panda", "--", "psql", "-c", "select 1"}},
		{"port-forward stays next to kubectl", "kind-panda", "kubectl",
			[]string{"port-forward", "service/kates", "8080:8080", "-n", "kates"},
			[]string{"port-forward", "service/kates", "8080:8080", "-n", "kates", "--context=kind-panda"}},
		{"an EKS ARN is one argument", "arn:aws:eks:eu-west-1:123456789012:cluster/prod", "kubectl",
			[]string{"get", "nodes"},
			[]string{"get", "nodes", "--context=arn:aws:eks:eu-west-1:123456789012:cluster/prod"}},
		{"no context leaves args alone", "", "kubectl",
			[]string{"get", "pods"},
			[]string{"get", "pods"}},
		{"other commands are left alone", "kind-panda", "kind",
			[]string{"load", "docker-image", "kates:native-local", "--name", "panda"},
			[]string{"load", "docker-image", "kates:native-local", "--name", "panda"}},
		{"a context already chosen wins", "kind-panda", "kubectl",
			[]string{"--context", "prod-eu", "cluster-info"},
			[]string{"--context", "prod-eu", "cluster-info"}},
		{"a context already chosen wins (= form)", "kind-panda", "helm",
			[]string{"list", "--kube-context=prod-eu"},
			[]string{"list", "--kube-context=prod-eu"}},
		{"--context after -- belongs to the container", "kind-panda", "kubectl",
			[]string{"exec", "pod", "--", "tool", "--context=x"},
			[]string{"exec", "pod", "--context=kind-panda", "--", "tool", "--context=x"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			in := append([]string(nil), tt.args...)
			got := withKubeContext(tt.kubeContext, tt.cmd, in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("withKubeContext(%q, %q, %q)\n got %q\nwant %q", tt.kubeContext, tt.cmd, tt.args, got, tt.want)
			}
			if !reflect.DeepEqual(in, tt.args) {
				t.Errorf("withKubeContext modified its input: %q", in)
			}
		})
	}
}

// Pinning wraps what the seams hold, so a stub installed before the pin still
// gets every call, with the flag; unpinning puts back exactly what was there.
func TestPinKubeContext_WrapsTheSeamsAndRestoresThem(t *testing.T) {
	origExec, origStdin, origHelm := runExecFn, runExecStdinFn, runHelmFn
	origOutput, origCombined := runExecOutputFn, runExecCombinedFn
	origProc, origExecutor := runProcFn, defaultExecutor
	t.Cleanup(func() {
		runExecFn, runExecStdinFn, runHelmFn = origExec, origStdin, origHelm
		runExecOutputFn, runExecCombinedFn = origOutput, origCombined
		runProcFn, defaultExecutor = origProc, origExecutor
	})

	var got []string
	note := func(name string, args []string) { got = append(got, name+" "+strings.Join(args, " ")) }
	runExecFn = func(_ context.Context, name string, args ...string) error { note(name, args); return nil }
	runExecStdinFn = func(_ context.Context, name string, args []string, _ string) error { note(name, args); return nil }
	runHelmFn = func(_ context.Context, args ...string) error { note("helm", args); return nil }
	runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		note(name, args)
		return nil, nil
	}
	runExecCombinedFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		note(name, args)
		return nil, nil
	}
	runProcFn = func(_ context.Context, _, name string, args ...string) (string, error) {
		note(name, args)
		return "", nil
	}
	executor := &recordingExecutor{}
	defaultExecutor = executor

	unpin := pinKubeContext("kind-panda")
	ctx := context.Background()
	_ = runExecFn(ctx, "kubectl", "rollout", "status", "deploy/kates")
	_ = runExecStdinFn(ctx, "kubectl", []string{"apply", "-f", "-"}, "kind: Namespace")
	_ = runHelmFn(ctx, "upgrade", "--install", "kates", "charts/kates")
	_, _ = runExecOutputFn(ctx, "kubectl", "get", "secret", "kates-api-key")
	_, _ = runExecCombinedFn(ctx, "kubectl", "get", "kafkaconnector", "x")
	_, _ = defaultRunner.Run(ctx, "helm", "list", "-A")
	_, _ = defaultExecutor.Exec("kubectl", "cluster-info")
	_, _ = runExecOutputFn(ctx, "docker", "image", "inspect", "kates:native-local")
	unpin()

	want := []string{
		"kubectl rollout status deploy/kates --context=kind-panda",
		"kubectl apply -f - --context=kind-panda",
		"helm upgrade --install kates charts/kates --kube-context=kind-panda",
		"kubectl get secret kates-api-key --context=kind-panda",
		"kubectl get kafkaconnector x --context=kind-panda",
		"helm list -A --kube-context=kind-panda",
		"docker image inspect kates:native-local",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("calls through the pinned seams:\n got %q\nwant %q", got, want)
	}
	if calls := executor.commands(); len(calls) != 1 || calls[0] != "kubectl cluster-info --context=kind-panda" {
		t.Errorf("introspection calls = %q, want the one call pinned", calls)
	}

	// Unpinned: the same seams, the same stubs, no flag.
	got = nil
	_ = runHelmFn(ctx, "list")
	_, _ = runExecOutputFn(ctx, "kubectl", "get", "pods")
	if want := []string{"helm list", "kubectl get pods"}; !reflect.DeepEqual(got, want) {
		t.Errorf("after unpin: got %q, want %q", got, want)
	}
	if defaultExecutor != executor {
		t.Errorf("after unpin defaultExecutor is %T, want the executor that was there before", defaultExecutor)
	}
}

func TestPortForwardArgs(t *testing.T) {
	t.Cleanup(func() { portsKubeContext = "" })
	spec := portForwardSpec{Resource: "service/kates", Namespace: "kates", Remote: 8080, Local: 18080}

	portsKubeContext = ""
	if got, want := strings.Join(portForwardArgs(spec), " "), "port-forward service/kates 18080:8080 -n kates"; got != want {
		t.Errorf("kates ports: %q, want %q", got, want)
	}

	// kates deploy --port-forward forwards from the cluster it deployed to.
	// The forward outlives kates, so the context is on its command line, and
	// the line still reads "kubectl port-forward", which the cleanup of old
	// forwards matches on.
	portsKubeContext = "kind-panda"
	line := "kubectl " + strings.Join(portForwardArgs(spec), " ")
	if want := "kubectl port-forward service/kates 18080:8080 -n kates --context=kind-panda"; line != want {
		t.Errorf("deploy --port-forward: %q, want %q", line, want)
	}
}
