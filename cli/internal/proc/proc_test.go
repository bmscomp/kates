package proc

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// stubCommand puts a shell script named name first on PATH for the test.
func stubCommand(t *testing.T, name, body string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shell script")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestOS_OutputAndStdin(t *testing.T) {
	stubCommand(t, "tool", "echo \"args: $*\"\ncat\necho oops >&2\n")
	ctx := context.Background()

	out, err := OS{}.Output(ctx, "tool", "a", "b")
	if err != nil || string(out) != "args: a b\n" {
		t.Errorf("Output = %q, %v; want the stdout alone", out, err)
	}
	out, err = OS{}.OutputStdin(ctx, "from stdin\n", "tool")
	if err != nil || string(out) != "args: \nfrom stdin\n" {
		t.Errorf("OutputStdin = %q, %v; want stdin echoed back", out, err)
	}
	out, err = OS{}.Combined(ctx, "tool")
	if err != nil || !strings.Contains(string(out), "oops") {
		t.Errorf("Combined = %q, %v; want stderr in the output", out, err)
	}
	if err := (OS{}).Run(ctx, "tool"); err != nil {
		t.Errorf("Run = %v", err)
	}
}

// A failed command's error names the command, its leading arguments and its
// standard error; the exit status and the stderr are available typed.
func TestOS_FailureCarriesStderrAndExitCode(t *testing.T) {
	stubCommand(t, "tool", "echo 'partial' \necho 'Error from server (NotFound): pods \"x\" not found' >&2\nexit 3\n")
	ctx := context.Background()

	out, err := OS{}.Output(ctx, "tool", "get", "pod", "x", "-n", "kafka")
	if out != nil {
		t.Errorf("Output on failure = %q, want nil", out)
	}
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("err = %T, want *Error", err)
	}
	if e.ExitCode() != 3 || ExitCode(err) != 3 {
		t.Errorf("exit code = %d / %d, want 3", e.ExitCode(), ExitCode(err))
	}
	if want := `Error from server (NotFound): pods "x" not found`; Stderr(err) != want {
		t.Errorf("Stderr = %q, want %q", Stderr(err), want)
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "tool get pod x: exit status 3: Error from server") || strings.Contains(msg, "kafka") {
		t.Errorf("message = %q, want the command, three arguments, the status and stderr", msg)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Errorf("err does not unwrap to the *exec.ExitError")
	}

	// Combined keeps the output a failed command wrote.
	out, err = OS{}.Combined(ctx, "tool")
	if err == nil || !strings.Contains(string(out), "partial") || !strings.Contains(string(out), "NotFound") {
		t.Errorf("Combined on failure = %q, %v; want both streams and an error", out, err)
	}
	if !strings.Contains(Stderr(err), "partial") {
		t.Errorf("Combined's Error.Stderr = %q, want the combined output", Stderr(err))
	}
}

func TestOS_MissingCommand(t *testing.T) {
	err := OS{}.Run(context.Background(), "kates-no-such-command-"+t.Name())
	if err == nil || ExitCode(err) != -1 || Stderr(err) != "" {
		t.Errorf("err = %v (exit %d, stderr %q); want a start failure", err, ExitCode(err), Stderr(err))
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("err = %v, want it to wrap exec.ErrNotFound", err)
	}
}

// A command whose context ends returns then, even when a process it started
// still holds its output open, as the process behind a kubectl wrapper
// script can. Killing the command alone left Wait waiting for that pipe to
// close, so a lookup given ten seconds took as long as the other process ran.
func TestOS_ReturnsWhenItsContextEnds(t *testing.T) {
	stubCommand(t, "tool", "sleep 30\n")
	defer func(d time.Duration) { WaitDelay = d }(WaitDelay)
	WaitDelay = 100 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := OS{}.Output(ctx, "tool", "get", "kafkaconnect", "-A")
	if err == nil {
		t.Fatal("a command cut off by its context returned no error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Output returned %s after its context ended, not when it ended", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want it to carry the context's error", err)
	}
	if !strings.Contains(err.Error(), "deadline exceeded") {
		t.Errorf("message = %q, want it to say the context ended", err)
	}
}

func TestRewrite(t *testing.T) {
	f := &Fake{}
	r := Rewrite(f, func(name string, args []string) []string {
		if name == "kubectl" {
			return append(append([]string(nil), args...), "--context=kind-x")
		}
		return args
	})
	ctx := context.Background()
	_ = r.Run(ctx, "kubectl", "get", "pods")
	_, _ = r.Output(ctx, "helm", "list")
	_, _ = r.Combined(ctx, "kubectl", "run")
	_, _ = r.OutputStdin(ctx, "yaml", "kubectl", "apply", "-f", "-")

	want := []string{
		"kubectl get pods --context=kind-x",
		"helm list",
		"kubectl run --context=kind-x",
		"kubectl apply -f - --context=kind-x",
	}
	if got := f.Lines(); !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %q, want %q", got, want)
	}
	if f.Calls()[3].Stdin != "yaml" {
		t.Errorf("stdin was not passed through: %+v", f.Calls()[3])
	}
	if u, ok := r.(interface{ Unwrap() Runner }); !ok || u.Unwrap() != Runner(f) {
		t.Errorf("Rewrite does not unwrap to the inner runner")
	}
}

func TestFake_ScriptsMatchByPrefixInOrder(t *testing.T) {
	f := (&Fake{}).
		On("kubectl get pods -n kafka", "kafka pods").
		On("kubectl get pods", "some pods").
		Fail("helm status", "Error: release: not found")
	ctx := context.Background()

	if out, err := f.Output(ctx, "kubectl", "get", "pods", "-n", "kafka", "-o", "json"); err != nil || string(out) != "kafka pods" {
		t.Errorf("specific script: %q, %v", out, err)
	}
	if out, err := f.Output(ctx, "kubectl", "get", "pods"); err != nil || string(out) != "some pods" {
		t.Errorf("general script: %q, %v", out, err)
	}
	if out, err := f.Output(ctx, "kubectl", "get", "podsecuritypolicy"); err != nil || out != nil {
		t.Errorf("a word that only starts with the prefix matched: %q, %v", out, err)
	}
	err := f.Run(ctx, "helm", "status", "x")
	if ExitCode(err) != 1 || Stderr(err) != "Error: release: not found" || !strings.Contains(err.Error(), "not found") {
		t.Errorf("Fail: err = %v (exit %d)", err, ExitCode(err))
	}
	if out, err := f.Combined(ctx, "docker", "ps"); err != nil || out != nil {
		t.Errorf("an unscripted call: %q, %v; want nothing and no error", out, err)
	}

	f.Unmatched = errors.New("unscripted")
	if err := f.Run(ctx, "docker", "ps"); err == nil || !errors.Is(err, f.Unmatched) {
		t.Errorf("with Unmatched set: err = %v", err)
	}

	want := []string{
		"kubectl get pods -n kafka -o json",
		"kubectl get pods",
		"kubectl get podsecuritypolicy",
		"helm status x",
		"docker ps",
		"docker ps",
	}
	if got := f.Lines(); !reflect.DeepEqual(got, want) {
		t.Errorf("Lines = %q, want %q", got, want)
	}
	f.Reset()
	if len(f.Calls()) != 0 {
		t.Errorf("Reset left %d calls", len(f.Calls()))
	}
}

// A Handle reply's plain error reaches the caller as an *Error, so what a
// command sees from the fake is what it sees from the OS runner.
func TestFake_HandleWrapsPlainErrors(t *testing.T) {
	plain := errors.New("boom")
	f := (&Fake{}).Handle("tool", func(c Call) ([]byte, error) { return []byte("out"), plain })
	out, err := f.Combined(context.Background(), "tool", "x")
	var e *Error
	if !errors.As(err, &e) || !errors.Is(err, plain) || e.Name != "tool" || string(out) != "out" {
		t.Errorf("got %q, %v (%T)", out, err, err)
	}
}

func TestString(t *testing.T) {
	if got := String("kubectl", nil); got != "kubectl" {
		t.Errorf("String with no args = %q", got)
	}
	if got := String("kubectl", []string{"get", "pods"}); got != "kubectl get pods" {
		t.Errorf("String = %q", got)
	}
}
