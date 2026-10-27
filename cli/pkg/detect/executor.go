package detect

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// CommandExecutor interface allows for mocking shell execution in unit tests.
type CommandExecutor interface {
	Exec(name string, args ...string) (string, error)
	LookPath(file string) (string, error)
}

// ContextExecutor is a CommandExecutor that stops a command when its
// context ends. The collector runs every command through it when the
// executor has it, so Ctrl-C and a call's timeout kill the kubectl process
// rather than leave it running.
type ContextExecutor interface {
	ExecContext(ctx context.Context, name string, args ...string) (string, error)
}

// DefaultCallTimeout bounds one command the collector runs. A kubectl call
// that hangs (an unreachable API server, an exec into a pod that never
// answers) ends there instead of stalling the whole report.
const DefaultCallTimeout = 30 * time.Second

// OSExecutor is the standard implementation that uses os/exec.
type OSExecutor struct{}

func NewOSExecutor() *OSExecutor {
	return &OSExecutor{}
}

// Exec runs name with args and returns its trimmed stdout. When the command
// fails, the error carries what it wrote to stderr: that is where kubectl says
// why, an admission webhook's denial included.
func (e *OSExecutor) Exec(name string, args ...string) (string, error) {
	return e.ExecContext(context.Background(), name, args...)
}

// ExecContext is Exec with a context: when ctx ends the process is killed,
// and the error says the context ended.
func (e *OSExecutor) ExecContext(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	// A child that keeps the output pipes open (kubectl exec's shell) must
	// not hold Wait past the kill.
	cmd.WaitDelay = 2 * time.Second
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctxErr := ctx.Err(); err != nil && ctxErr != nil {
		err = fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), ctxErr)
	} else if msg := strings.TrimSpace(stderr.String()); err != nil && msg != "" {
		err = fmt.Errorf("%w: %s", err, msg)
	}
	return strings.TrimSpace(out.String()), err
}

func (e *OSExecutor) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

// execContext runs name under ctx, for at most timeout. An executor with
// ExecContext stops the command; any other (a test double, a wrapper that
// pins a kube context) is called in a goroutine that is abandoned when ctx
// ends, so the caller still returns at once.
func execContext(ctx context.Context, e CommandExecutor, timeout time.Duration, name string, args ...string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if ce, ok := e.(ContextExecutor); ok {
		return ce.ExecContext(ctx, name, args...)
	}
	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := e.Exec(name, args...)
		done <- result{out, err}
	}()
	select {
	case r := <-done:
		return r.out, r.err
	case <-ctx.Done():
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), ctx.Err())
	}
}
