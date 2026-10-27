// Package kubectl is a thin typed wrapper over a proc.Runner for the kubectl
// calls the CLI makes: it adds the context and namespace, prints the command
// when asked, and decodes JSON output.
package kubectl

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bmscomp/kates/cli/internal/proc"
)

// Client runs kubectl through a proc.Runner.
type Client struct {
	// Namespace is the namespace every call carries as -n, unless the call
	// chooses one itself (-n, --namespace, -A, --all-namespaces). It used to
	// be stored and never emitted, so a client made for a namespace ran in
	// the kubeconfig's.
	Namespace string
	// Context is the kubectl context to use (empty for default).
	Context string
	// Verbose prints commands before executing when true.
	Verbose bool

	runner proc.Runner
}

// New creates a kubectl client with the given namespace that runs kubectl
// with the OS runner.
func New(namespace string) *Client {
	return NewWith(proc.OS{}, namespace)
}

// NewWith creates a kubectl client that runs kubectl through runner.
func NewWith(runner proc.Runner, namespace string) *Client {
	return &Client{Namespace: namespace, runner: runner}
}

// namespaceFlags are the arguments by which a call chooses its own
// namespace, or all of them.
var namespaceFlags = map[string]bool{"-n": true, "--namespace": true, "-A": true, "--all-namespaces": true}

// buildArgs prepends --context when set and appends -n when the client has
// a namespace and args choose none. The namespace goes last, or just before
// a "--": what follows `kubectl exec … --` is the container's command line.
func (c *Client) buildArgs(args []string) []string {
	var out []string
	if c.Context != "" {
		out = append(out, "--context", c.Context)
	}
	out = append(out, args...)
	if c.Namespace == "" {
		return out
	}
	end := len(args)
	for i, a := range args {
		if a == "--" {
			end = i
			break
		}
	}
	for _, a := range args[:end] {
		flag, _, _ := strings.Cut(a, "=")
		if namespaceFlags[flag] {
			return out
		}
	}
	prefix := len(out) - len(args)
	pinned := append(out[:prefix+end:prefix+end], "-n", c.Namespace)
	return append(pinned, args[end:]...)
}

func (c *Client) prepare(args []string) []string {
	fullArgs := c.buildArgs(args)
	if c.Verbose {
		fmt.Printf("  → kubectl %s\n", strings.Join(fullArgs, " "))
	}
	return fullArgs
}

// Run executes kubectl with the given arguments and returns its trimmed
// standard output. A failure's error carries kubectl's standard error.
func (c *Client) Run(ctx context.Context, args ...string) (string, error) {
	out, err := c.Output(ctx, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Output executes kubectl and returns raw stdout bytes.
func (c *Client) Output(ctx context.Context, args ...string) ([]byte, error) {
	return c.runner.Output(ctx, "kubectl", c.prepare(args)...)
}

// Combined executes kubectl and returns stdout and stderr interleaved; on
// failure it returns what kubectl wrote alongside the error.
func (c *Client) Combined(ctx context.Context, args ...string) ([]byte, error) {
	return c.runner.Combined(ctx, "kubectl", c.prepare(args)...)
}

// JSON executes kubectl with -o json and unmarshals into result.
func (c *Client) JSON(ctx context.Context, result interface{}, args ...string) error {
	out, err := c.Output(ctx, append(append([]string(nil), args...), "-o", "json")...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(out, result); err != nil {
		return fmt.Errorf("kubectl %s: decoding its JSON: %w", strings.Join(args[:min(len(args), 3)], " "), err)
	}
	return nil
}

// Exists checks if a resource exists (returns true/false without error on not-found).
func (c *Client) Exists(ctx context.Context, kind, name string) bool {
	_, err := c.Run(ctx, "get", kind, name, "--no-headers")
	return err == nil
}

// CRDExists checks if a CRD is installed in the cluster.
func (c *Client) CRDExists(ctx context.Context, crdName string) bool {
	_, err := c.Run(ctx, "get", "crd", crdName, "--no-headers")
	return err == nil
}

// IsNotFound reports whether a failed kubectl call said the resource does not
// exist, as opposed to failing to ask: kubectl writes "(NotFound)" for one and
// something else, such as "Unable to connect to the server", for the other.
func IsNotFound(err error) bool {
	return strings.Contains(proc.Stderr(err), "(NotFound)")
}

// Apply applies a YAML manifest from stdin, in namespace when given, else in
// the client's.
func (c *Client) Apply(ctx context.Context, yaml string, namespace string) error {
	args := []string{"apply", "-f", "-"}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	_, err := c.runner.OutputStdin(ctx, yaml, "kubectl", c.prepare(args)...)
	if err != nil {
		return fmt.Errorf("kubectl apply: %w", err)
	}
	return nil
}
