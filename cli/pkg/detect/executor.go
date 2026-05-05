package detect

import (
	"bytes"
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

func (e *OSExecutor) Exec(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

func (e *OSExecutor) LookPath(file string) (string, error) {
	return exec.LookPath(file)
}
