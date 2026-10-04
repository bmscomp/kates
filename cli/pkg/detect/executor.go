package detect

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// CommandExecutor interface allows for mocking shell execution in unit tests.
type CommandExecutor interface {
	Exec(name string, args ...string) (string, error)
	LookPath(file string) (string, error)
}

// OSExecutor is the standard implementation that uses os/exec.
type OSExecutor struct{}

func NewOSExecutor() *OSExecutor {
	return &OSExecutor{}
}

// Exec runs name with args and returns its trimmed stdout. When the command
// fails, the error carries what it wrote to stderr: that is where kubectl says
// why, an admission webhook's denial included.
func (e *OSExecutor) Exec(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	if msg := strings.TrimSpace(stderr.String()); err != nil && msg != "" {
		err = fmt.Errorf("%w: %s", err, msg)
	}
	return strings.TrimSpace(out.String()), err
}

func (e *OSExecutor) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}
