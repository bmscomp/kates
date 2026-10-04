// Package podrun runs the Kafka command-line tools inside a long-lived client
// pod on the cluster's own network, driven through kubectl with argv only.
//
// It replaces the scripts' `kubectl run --rm -i … /bin/sh -c "…"` pattern.
// Two rules hold for everything in this package:
//
//   - No shell program is ever composed. Every process is an argv, every pod
//     command is an argv, and the corpus a producer needs arrives on stdin
//     from Go. The single `/bin/sh -c` in the package is the client pod's own
//     entrypoint (a trap around `sleep infinity`), a constant that nothing
//     user-controlled reaches.
//   - No credential appears on a command line, in a pod spec, or in a log.
//     Passwords reach the pod either through a Secret-backed environment
//     variable (Pod.SecretEnv, rendered as valueFrom.secretKeyRef) or through
//     a properties file written with WriteFile from a value the caller read
//     with `kubectl get secret -o jsonpath` and never printed. See the
//     package-level contract on SASLProperties.
//
// All process execution goes through the Runner interface, so the whole
// package — and everything built on it — is testable without a cluster.
package podrun

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Runner executes an external command (kubectl, in this package) and returns
// its standard output. A failed command returns an error that carries the
// command's standard error; stdout is discarded on failure.
type Runner interface {
	// Run executes name with args and no standard input.
	Run(ctx context.Context, name string, args ...string) (stdout string, err error)
	// RunInput executes name with args, feeding stdin to the process.
	RunInput(ctx context.Context, stdin string, name string, args ...string) (stdout string, err error)
}

// ExecRunner is the Runner that executes commands with os/exec.
type ExecRunner struct{}

// Run executes name with args and returns its standard output. On failure the
// error names the command (its first three arguments only, never more, so a
// long argv does not flood a message) and carries its standard error.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	return run(ctx, nil, name, args...)
}

// RunInput is Run with stdin supplied to the process.
func (ExecRunner) RunInput(ctx context.Context, stdin string, name string, args ...string) (string, error) {
	return run(ctx, strings.NewReader(stdin), name, args...)
}

func run(ctx context.Context, stdin *strings.Reader, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", commandError(name, args, err, stderr.String())
	}
	return stdout.String(), nil
}

// commandError builds the error a failed process returns: the command's name
// and leading arguments, the exec error, and the trimmed standard error when
// there is one.
func commandError(name string, args []string, err error, stderr string) error {
	shown := args
	if len(shown) > 3 {
		shown = shown[:3]
	}
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		return fmt.Errorf("%s %s: %w", name, strings.Join(shown, " "), err)
	}
	return fmt.Errorf("%s %s: %w: %s", name, strings.Join(shown, " "), err, msg)
}
