package podrun

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// call is one recorded Runner invocation.
type call struct {
	name  string
	args  []string
	stdin string
}

// key is the command line the fake keys its canned answers on.
func (c call) key() string {
	return c.name + " " + strings.Join(c.args, " ")
}

// FakeRunner serves canned stdout keyed by the full command line ("kubectl
// -n ns exec …"), records every call with its stdin, and fails on any command
// it was not told about — an unexpected command is a bug, not a no-op.
type FakeRunner struct {
	t       *testing.T
	outputs map[string]string
	errors  map[string]error
	calls   []call
}

func newFakeRunner(t *testing.T) *FakeRunner {
	return &FakeRunner{t: t, outputs: map[string]string{}, errors: map[string]error{}}
}

// on registers stdout for a command line.
func (f *FakeRunner) on(cmdline, stdout string) *FakeRunner {
	f.outputs[cmdline] = stdout
	return f
}

// fail registers an error for a command line.
func (f *FakeRunner) fail(cmdline string, err error) *FakeRunner {
	f.errors[cmdline] = err
	return f
}

// Run implements Runner.
func (f *FakeRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	return f.serve(call{name: name, args: args})
}

// RunInput implements Runner.
func (f *FakeRunner) RunInput(_ context.Context, stdin string, name string, args ...string) (string, error) {
	return f.serve(call{name: name, args: args, stdin: stdin})
}

func (f *FakeRunner) serve(c call) (string, error) {
	f.calls = append(f.calls, c)
	k := c.key()
	if err, ok := f.errors[k]; ok {
		return "", err
	}
	out, ok := f.outputs[k]
	if !ok {
		return "", fmt.Errorf("FakeRunner: unexpected command %q", k)
	}
	return out, nil
}

// keys returns every recorded command line, in order.
func (f *FakeRunner) keys() []string {
	out := make([]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = c.key()
	}
	return out
}

func (f *FakeRunner) assertCalls(want ...string) {
	f.t.Helper()
	got := f.keys()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		f.t.Errorf("calls:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func TestExecRunnerRunAndRunInput(t *testing.T) {
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat not on PATH")
	}
	ctx := context.Background()
	var r ExecRunner

	out, err := r.RunInput(ctx, "hello, pod\n", "cat")
	if err != nil {
		t.Fatalf("RunInput: %v", err)
	}
	if out != "hello, pod\n" {
		t.Fatalf("RunInput echoed %q", out)
	}

	out, err = r.Run(ctx, "cat", "/dev/null")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "" {
		t.Fatalf("Run printed %q", out)
	}
}

func TestExecRunnerErrorCarriesStderr(t *testing.T) {
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat not on PATH")
	}
	var r ExecRunner
	_, err := r.Run(context.Background(), "cat", "/definitely/not/a/file", "second", "third", "fourth")
	if err == nil {
		t.Fatal("expected an error")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error %v does not wrap the exit error", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "No such file") && !strings.Contains(msg, "not a file") {
		t.Errorf("error %q does not carry stderr", msg)
	}
	// The command is named by its first three arguments only; what follows
	// the colon is the exec error and then stderr.
	if !strings.HasPrefix(msg, "cat /definitely/not/a/file second third: exit status") {
		t.Errorf("error %q does not name the command by its first three arguments", msg)
	}
}

func TestExecRunnerMissingBinary(t *testing.T) {
	var r ExecRunner
	_, err := r.Run(context.Background(), "kates-no-such-binary-podrun")
	if err == nil {
		t.Fatal("expected an error for a missing binary")
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("error %v does not wrap exec.ErrNotFound", err)
	}
}
