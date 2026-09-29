package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// procRunner is the one seam through which the version, operator and
// migration commands run helm and kubectl. It satisfies both
// strimzi.Runner (Run) and podrun.Runner (Run + RunInput), so a single value
// is handed to every package that needs a process, and a single fake replaces
// it in tests.
//
// Why not the deploy_* injection variables: those return only an error or raw
// bytes and are shaped around the deploy dashboard. The packages behind this
// seam parse stdout and want stderr in the error, which is what Run gives
// them.
type procRunner struct{}

// Run executes name with args and returns trimmed stdout. On failure the
// error carries the command's stderr, so callers can show why kubectl or
// helm refused without a second call.
func (procRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	out, err := runProc(ctx, "", name, args...)
	return strings.TrimSpace(out), err
}

// RunInput is Run with data on stdin — `kubectl apply -f -`, `kubectl exec
// -i … -- tee`, the console producer — except that it returns stdout as the
// process wrote it, which is what podrun.Runner promises. podrun.WriteFile
// compares tee's echo with what it wrote, byte for byte: trimmed, the echo of
// any file ending in a newline, every client.properties among them, came back
// one byte short, and `kates migrate verify` failed before its first record.
func (procRunner) RunInput(ctx context.Context, stdin string, name string, args ...string) (string, error) {
	return runProc(ctx, stdin, name, args...)
}

// runProcFn is the injectable implementation behind procRunner. Tests replace
// it to script helm/kubectl output without a cluster.
var runProcFn = runProcDefault

func runProc(ctx context.Context, stdin, name string, args ...string) (string, error) {
	return runProcFn(ctx, stdin, name, args...)
}

func runProcDefault(ctx context.Context, stdin, name string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		c.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		shown := args
		if len(shown) > 4 {
			shown = shown[:4]
		}
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(shown, " "), err, msg)
	}
	return stdout.String(), nil
}

// defaultRunner is the process runner every non-deploy command uses.
var defaultRunner procRunner
