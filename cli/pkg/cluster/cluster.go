// Package cluster discovers Kubernetes contexts and, when none is usable,
// works out whether a local Kind cluster can be created instead.
//
// Everything here goes through detect.CommandExecutor rather than os/exec so
// the whole decision tree is testable without a real cluster or a real Docker
// daemon.
package cluster

import (
	"fmt"
	"strings"

	"github.com/bmscomp/kates/cli/pkg/detect"
)

// Context is one kubeconfig context.
type Context struct {
	Name      string
	Cluster   string
	Namespace string
	Current   bool // the kubeconfig's current-context
	Reachable bool // set by CheckReachable; false until then
	// Probed records whether reachability was actually tested. Without it,
	// "unreachable" and "not yet checked" are indistinguishable — the bug that
	// makes a picker silently present dead clusters as live ones.
	Probed bool
}

// DockerState distinguishes the failure modes a user must be told apart:
// a missing binary needs an install, a stopped daemon needs a start.
type DockerState int

const (
	DockerNotInstalled DockerState = iota
	DockerInstalledNotRunning
	DockerRunning
)

func (d DockerState) String() string {
	switch d {
	case DockerRunning:
		return "running"
	case DockerInstalledNotRunning:
		return "installed but not running"
	default:
		return "not installed"
	}
}

// Environment is what we can tell about the local machine's ability to host a
// Kind cluster.
type Environment struct {
	Docker        DockerState
	KindInstalled bool
}

// CanCreateKind reports whether a Kind cluster could actually be created here.
func (e Environment) CanCreateKind() bool {
	return e.Docker == DockerRunning && e.KindInstalled
}

// reachabilityTimeout bounds every reachability probe. kubectl will otherwise
// block on a dead endpoint until its own long default elapses, which would
// stall the picker for seconds per stale context — the common case on a laptop
// with an old kubeconfig.
const reachabilityTimeout = "5s"

// ListContexts returns every context in the kubeconfig, marking the current
// one. Reachability is NOT probed here; call CheckReachable.
//
// An empty list and a nil error is a valid, expected result: it means there is
// no kubeconfig, or it defines no contexts.
func ListContexts(exec detect.CommandExecutor) ([]Context, error) {
	// Custom columns rather than -o name: we want the cluster and namespace
	// too, and get-contexts is the only kubectl call that reports the current
	// context marker.
	out, err := exec.Exec("kubectl", "config", "get-contexts",
		"--no-headers",
		"-o", "name")
	if err != nil {
		// A kubeconfig that does not exist is not an error for our purposes —
		// it is simply "no contexts", which is the state the caller handles.
		if isMissingKubeconfig(err, out) {
			return nil, nil
		}
		return nil, fmt.Errorf("listing kubeconfig contexts: %w", err)
	}

	current, _ := exec.Exec("kubectl", "config", "current-context")
	current = strings.TrimSpace(current)

	var contexts []Context
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		contexts = append(contexts, Context{
			Name:      name,
			Cluster:   contextField(exec, name, "{.context.cluster}"),
			Namespace: contextField(exec, name, "{.context.namespace}"),
			Current:   name == current,
		})
	}
	return contexts, nil
}

// contextField reads one jsonpath field for a named context. A failure here is
// cosmetic — the context is still selectable — so errors degrade to "".
func contextField(exec detect.CommandExecutor, name, jsonpath string) string {
	out, err := exec.Exec("kubectl", "config", "view",
		"-o", fmt.Sprintf("jsonpath={.contexts[?(@.name==%q)]%s}", name, strings.TrimPrefix(jsonpath, "{")))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// isMissingKubeconfig reports whether the error means "no kubeconfig" rather
// than "kubectl is broken". Both surface as a non-zero exit, but only the
// former is a normal state we should handle silently.
func isMissingKubeconfig(err error, out string) bool {
	hay := strings.ToLower(out + " " + err.Error())
	for _, s := range []string{
		"no such file or directory",
		"stat .*kubeconfig",
		"error loading config file",
		"no configuration has been provided",
	} {
		if strings.Contains(hay, s) {
			return true
		}
	}
	return false
}

// CheckReachable probes one context with a bounded timeout and returns a copy
// with Reachable and Probed set.
func CheckReachable(exec detect.CommandExecutor, c Context) Context {
	_, err := exec.Exec("kubectl", "cluster-info",
		"--context", c.Name,
		"--request-timeout", reachabilityTimeout)
	c.Reachable = err == nil
	c.Probed = true
	return c
}

// CheckAllReachable probes every context. It is sequential on purpose: the
// bounded timeout keeps the worst case small, and concurrent kubectl processes
// against a hanging endpoint buy little for the added complexity.
func CheckAllReachable(exec detect.CommandExecutor, contexts []Context) []Context {
	out := make([]Context, 0, len(contexts))
	for _, c := range contexts {
		out = append(out, CheckReachable(exec, c))
	}
	return out
}

// Reachable filters to contexts that were probed and answered.
func Reachable(contexts []Context) []Context {
	var out []Context
	for _, c := range contexts {
		if c.Reachable {
			out = append(out, c)
		}
	}
	return out
}

// ProbeEnvironment determines whether this machine can host a Kind cluster.
//
// The docker-not-installed vs docker-installed-but-stopped distinction is the
// point: the two need different advice, and collapsing them into "docker is
// unavailable" tells the user nothing actionable.
func ProbeEnvironment(exec detect.CommandExecutor) Environment {
	env := Environment{Docker: DockerNotInstalled}

	if _, err := exec.LookPath("docker"); err == nil {
		// `docker info` talks to the daemon; `docker version` alone would
		// succeed against a stopped daemon and misreport it as running.
		if _, err := exec.Exec("docker", "info"); err == nil {
			env.Docker = DockerRunning
		} else {
			env.Docker = DockerInstalledNotRunning
		}
	}

	if _, err := exec.LookPath("kind"); err == nil {
		env.KindInstalled = true
	}

	return env
}
