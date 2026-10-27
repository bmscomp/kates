//go:build !windows

package cmd

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// listProcesses runs ps. -A and -o pid=,args= mean the same on macOS and
// Linux.
func listProcesses() (map[int]string, error) {
	out, err := exec.Command("ps", "-A", "-o", "pid=", "-o", "args=").Output()
	if err != nil {
		return nil, err
	}
	procs := map[int]string{}
	for _, line := range strings.Split(string(out), "\n") {
		pidField, cmdLine, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		pid, err := strconv.Atoi(pidField)
		if err != nil {
			continue
		}
		procs[pid] = strings.TrimSpace(cmdLine)
	}
	return procs, nil
}

// stopProcess asks a process to exit.
func stopProcess(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

// detachProcess puts cmd in its own process group, so a forward survives
// after `kates ports` exits and the shell returns to its prompt.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
