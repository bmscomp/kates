// Package proc is the one seam through which the CLI runs external commands.
//
// A Runner executes a program by argv, bounded by a context, and reports a
// failure as an *Error carrying the program's standard error. The OS runner is
// the real one; Fake (fake.go) is scripted for tests. internal/helm and
// internal/kubectl are typed wrappers over a Runner, so a command that goes
// through them is testable without a cluster, and a concern such as pinning
// every call to one kube context is a wrapper around one value (Rewrite)
// rather than around each call site.
package proc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Runner executes external commands.
//
// Every method returns an *Error (see Error) when the command could not be
// started, exited non-zero, or was cut off by ctx.
type Runner interface {
	// Run executes name with args, discarding its output.
	Run(ctx context.Context, name string, args ...string) error
	// Output executes name with args and returns its standard output. On
	// failure it returns nil and an error carrying the standard error.
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
	// Combined executes name with args and returns standard output and
	// standard error interleaved, as a terminal would show them. On failure
	// it returns what the command wrote so far alongside the error, since a
	// command such as `kubectl run` reports why it failed on that stream.
	Combined(ctx context.Context, name string, args ...string) ([]byte, error)
	// OutputStdin is Output with stdin fed to the process.
	OutputStdin(ctx context.Context, stdin string, name string, args ...string) ([]byte, error)
}

// WaitDelay is how long a command's output may stay open once the command's
// context has ended or the command has exited. The context kills the
// process, but Wait also waits for its output pipes to close, and a process
// it started, or the process behind a kubectl wrapper script, can hold them
// open: a lookup given ten seconds then took as long as that process ran.
var WaitDelay = 2 * time.Second

// OS is the Runner that executes commands with os/exec.
type OS struct{}

// Run implements Runner.
func (OS) Run(ctx context.Context, name string, args ...string) error {
	_, err := run(ctx, "", name, args, false)
	return err
}

// Output implements Runner.
func (OS) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return run(ctx, "", name, args, false)
}

// Combined implements Runner.
func (OS) Combined(ctx context.Context, name string, args ...string) ([]byte, error) {
	return run(ctx, "", name, args, true)
}

// OutputStdin implements Runner.
func (OS) OutputStdin(ctx context.Context, stdin string, name string, args ...string) ([]byte, error) {
	return run(ctx, stdin, name, args, false)
}

func run(ctx context.Context, stdin, name string, args []string, combined bool) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = WaitDelay
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	if combined {
		cmd.Stderr = &stdout
	} else {
		cmd.Stderr = &stderr
	}
	err := cmd.Run()
	if err == nil {
		return stdout.Bytes(), nil
	}
	e := &Error{Name: name, Args: args, Err: err, ctxErr: ctx.Err()}
	if combined {
		e.Stderr = strings.TrimSpace(stdout.String())
		return stdout.Bytes(), e
	}
	e.Stderr = strings.TrimSpace(stderr.String())
	return nil, e
}

// Error is a command that failed: it could not be started, exited non-zero,
// or was cut off by its context. errors.Is sees the exec error and, when the
// context ended, the context's error too.
type Error struct {
	Name string
	Args []string
	// Stderr is the command's standard error, trimmed. For Combined it is
	// the combined output, since standard error went there.
	Stderr string
	// Err is what os/exec reported: an *exec.ExitError, or the error that
	// kept the command from starting.
	Err error

	ctxErr error
	// code is the exit status when Err is not an *exec.ExitError (a Fake's
	// scripted failure).
	code int
}

// shownArgs is how many leading arguments an error message names: enough
// to say which call failed without a long argv flooding the message.
const shownArgs = 3

func (e *Error) Error() string {
	shown := e.Args
	if len(shown) > shownArgs {
		shown = shown[:shownArgs]
	}
	msg := e.Name
	if len(shown) > 0 {
		msg += " " + strings.Join(shown, " ")
	}
	msg += ": " + e.Err.Error()
	if e.ctxErr != nil {
		msg += " (" + e.ctxErr.Error() + ")"
	}
	if e.Stderr != "" {
		msg += ": " + e.Stderr
	}
	return msg
}

// Unwrap exposes the exec error and the context's error for errors.Is.
func (e *Error) Unwrap() []error {
	if e.ctxErr != nil {
		return []error{e.Err, e.ctxErr}
	}
	return []error{e.Err}
}

// ExitCode is the command's exit status, or -1 when it did not exit on its
// own: it was never started, or a signal ended it.
func (e *Error) ExitCode() int {
	var exit *exec.ExitError
	if errors.As(e.Err, &exit) {
		return exit.ExitCode()
	}
	if e.code != 0 {
		return e.code
	}
	return -1
}

// Stderr is the standard error of the failed command behind err, or "" when
// err is not a command failure or the command wrote nothing there.
func Stderr(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Stderr
	}
	return ""
}

// ExitCode is the exit status of the failed command behind err: -1 when the
// command did not exit on its own, and 0 when err is not a command failure.
func ExitCode(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.ExitCode()
	}
	return 0
}

// Rewrite returns a Runner that passes every call to inner with its arguments
// replaced by rewrite(name, args). It is how one concern, such as pinning
// kubectl and helm to a kube context, is applied to every call through a
// seam without touching the call sites.
func Rewrite(inner Runner, rewrite func(name string, args []string) []string) Runner {
	return rewriter{inner: inner, rewrite: rewrite}
}

type rewriter struct {
	inner   Runner
	rewrite func(name string, args []string) []string
}

func (r rewriter) Run(ctx context.Context, name string, args ...string) error {
	return r.inner.Run(ctx, name, r.rewrite(name, args)...)
}

func (r rewriter) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return r.inner.Output(ctx, name, r.rewrite(name, args)...)
}

func (r rewriter) Combined(ctx context.Context, name string, args ...string) ([]byte, error) {
	return r.inner.Combined(ctx, name, r.rewrite(name, args)...)
}

func (r rewriter) OutputStdin(ctx context.Context, stdin string, name string, args ...string) ([]byte, error) {
	return r.inner.OutputStdin(ctx, stdin, name, r.rewrite(name, args)...)
}

// Unwrap exposes the wrapped Runner, so a test can see through a Rewrite.
func (r rewriter) Unwrap() Runner { return r.inner }

// String describes a call the way a shell line would, for messages and tests.
func String(name string, args []string) string {
	if len(args) == 0 {
		return name
	}
	return fmt.Sprintf("%s %s", name, strings.Join(args, " "))
}
