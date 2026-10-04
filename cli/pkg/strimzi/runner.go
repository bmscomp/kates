// Package strimzi discovers Strimzi operator charts and installations: the
// chart catalogue, pulled chart tarballs and what they say about themselves
// (Kafka window, CRD API versions, image tag), the per-version wrapper chart
// the CLI installs through, and the Cluster Operators running on a cluster.
//
// Nothing here executes a process directly: helm and kubectl are reached
// through the Runner interface so that callers can inject a fake. Functions
// return data and errors; they print nothing.
package strimzi

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Runner executes an external command (helm, kubectl) and returns its
// standard output. A failed command returns an error that carries the
// command's standard error.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

// RunnerFunc adapts a function to the Runner interface.
type RunnerFunc func(ctx context.Context, name string, args ...string) (string, error)

// Run calls f.
func (f RunnerFunc) Run(ctx context.Context, name string, args ...string) (string, error) {
	return f(ctx, name, args...)
}

// ExecRunner is the Runner that executes commands with os/exec.
type ExecRunner struct{}

// Run executes name with args and returns its standard output. On failure the
// error names the command and carries its standard error.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		shown := args
		if len(shown) > 3 {
			shown = shown[:3]
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return "", fmt.Errorf("%s %s: %w", name, strings.Join(shown, " "), err)
		}
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(shown, " "), err, msg)
	}
	return stdout.String(), nil
}
