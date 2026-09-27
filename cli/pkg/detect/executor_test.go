package detect

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// A failed command's error carries its stderr, and still unwraps to the exit
// error: the secret probe reads an admission webhook's denial from it.
func TestOSExecutor_ErrorCarriesStderr(t *testing.T) {
	out, err := NewOSExecutor().Exec("sh", "-c", "echo partial; echo 'denied by policy restrict-secrets' >&2; exit 3")
	if err == nil {
		t.Fatal("Exec returned no error for a command that exited 3")
	}
	if !strings.HasSuffix(err.Error(), ": denied by policy restrict-secrets") {
		t.Errorf("error = %q, want it to end with the command's stderr", err)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 3 {
		t.Errorf("error = %v, want it to wrap the exit error (code 3)", err)
	}
	if out != "partial" {
		t.Errorf("stdout = %q, want %q", out, "partial")
	}
}

func TestOSExecutor_SuccessIgnoresStderr(t *testing.T) {
	out, err := NewOSExecutor().Exec("sh", "-c", "echo ok; echo warning >&2")
	if err != nil || out != "ok" {
		t.Errorf("Exec = (%q, %v), want (\"ok\", nil)", out, err)
	}
}
