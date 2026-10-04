package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Stopping port-forwards by what they forward.
//
// kates clean used to stop forwards with `pkill -f "kubectl port-forward"`,
// which ends every forward on the machine, including ones to other clusters
// that kates did not start. It also sent SIGTERM to every process whose
// command line held both "ports" and "kates": an editor open on
// cli/cmd/ports.go, or a pager on docs/ports.md in the checkout. It did all
// that before its prompt, so declining the prompt did not undo it.
// stopPortForwards reads each kubectl port-forward's own command line, and
// the caller says which ones to stop.

// portForwardProc is a running `kubectl port-forward`, as ps shows it.
type portForwardProc struct {
	PID       int
	Namespace string // its -n or --namespace; "" when it gives none
	Context   string // its --context; "" when it gives none
}

var (
	// listProcessesFn returns each process's command line by PID.
	// Overridden in tests, which must never stop a real process.
	listProcessesFn = listProcesses
	// stopProcessFn asks a process to exit.
	stopProcessFn = func(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }
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

// parsePortForward reads cmdLine as a kubectl port-forward command line and
// reports whether it is one.
func parsePortForward(cmdLine string) (p portForwardProc, ok bool) {
	fields := strings.Fields(cmdLine)
	if len(fields) < 2 || filepath.Base(fields[0]) != "kubectl" {
		return p, false
	}
	// value returns the flag's value, from "--flag=value" or the next field.
	value := func(i *int, f, name string) (string, bool) {
		if v, ok := strings.CutPrefix(f, name+"="); ok {
			return v, true
		}
		if f == name && *i+1 < len(fields) {
			*i++
			return fields[*i], true
		}
		return "", false
	}
	for i := 1; i < len(fields); i++ {
		f := fields[i]
		if f == "port-forward" {
			ok = true
			continue
		}
		if v, found := value(&i, f, "--namespace"); found {
			p.Namespace = v
		} else if v, found := value(&i, f, "-n"); found {
			p.Namespace = v
		} else if v, found := strings.CutPrefix(f, "-n"); found && v != "" && !strings.HasPrefix(f, "--") {
			p.Namespace = v // -nkafka
		} else if v, found := value(&i, f, "--context"); found {
			p.Context = v
		}
	}
	return p, ok
}

// stopPortForwards stops each kubectl port-forward that match accepts, and
// returns how many it stopped.
func stopPortForwards(match func(portForwardProc) bool) int {
	procs, err := listProcessesFn()
	if err != nil {
		return 0
	}
	stopped := 0
	for pid, cmdLine := range procs {
		if pid == os.Getpid() {
			continue
		}
		p, ok := parsePortForward(cmdLine)
		if !ok {
			continue
		}
		p.PID = pid
		if match(p) && stopProcessFn(pid) == nil {
			stopped++
		}
	}
	return stopped
}
