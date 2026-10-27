//go:build windows

package cmd

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// errNoProcessList keeps kates ports and kates clean building on Windows,
// where there is no ps and a port-forward's command line is not read: they
// then stop no forward, as stopPortForwards does when ps fails.
var errNoProcessList = errors.New("listing kubectl port-forward processes is not supported on Windows")

func listProcesses() (map[int]string, error) { return nil, errNoProcessList }

// stopProcess ends a process; Windows has no SIGTERM to ask with.
func stopProcess(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

// detachProcess starts cmd in a new process group, so a forward survives
// after `kates ports` exits.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
