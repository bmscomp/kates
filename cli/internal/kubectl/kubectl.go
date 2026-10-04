package kubectl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// waitDelay is how long a kubectl command's output may stay open once the
// command's context has ended or kubectl has exited. The context kills
// kubectl, but Wait also waits for kubectl's output pipes to close, and a
// process it started, or the process behind a kubectl wrapper script, can
// hold them open: a lookup given ten seconds then took as long as that
// process ran.
var waitDelay = 2 * time.Second

// command is kubectl with args, ended by ctx.
func command(ctx context.Context, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.WaitDelay = waitDelay
	return cmd
}

// Client wraps kubectl command execution with consistent error handling.
type Client struct {
	// Namespace is the default namespace for commands.
	Namespace string
	// Context is the kubectl context to use (empty for default).
	Context string
	// Verbose prints commands before executing when true.
	Verbose bool
}

// New creates a kubectl client with the given namespace.
func New(namespace string) *Client {
	return &Client{Namespace: namespace}
}

// buildArgs prepends --namespace and --context flags if set.
func (c *Client) buildArgs(args []string) []string {
	var out []string
	if c.Context != "" {
		out = append(out, "--context", c.Context)
	}
	out = append(out, args...)
	return out
}

// Run executes kubectl with the given arguments. Returns combined output and error.
func (c *Client) Run(ctx context.Context, args ...string) (string, error) {
	fullArgs := c.buildArgs(args)
	if c.Verbose {
		fmt.Printf("  → kubectl %s\n", strings.Join(fullArgs, " "))
	}
	cmd := command(ctx, fullArgs)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("kubectl %s: %w\n%s", strings.Join(args[:min(len(args), 3)], " "), err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Output executes kubectl and returns raw stdout bytes.
func (c *Client) Output(ctx context.Context, args ...string) ([]byte, error) {
	fullArgs := c.buildArgs(args)
	if c.Verbose {
		fmt.Printf("  → kubectl %s\n", strings.Join(fullArgs, " "))
	}
	cmd := command(ctx, fullArgs)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("kubectl %s: %w\n%s", strings.Join(args[:min(len(args), 3)], " "), err, string(ee.Stderr))
		}
		return nil, fmt.Errorf("kubectl %s: %w", strings.Join(args[:min(len(args), 3)], " "), err)
	}
	return out, nil
}

// JSON executes kubectl with -o json and unmarshals into result.
func (c *Client) JSON(ctx context.Context, result interface{}, args ...string) error {
	fullArgs := append(c.buildArgs(args), "-o", "json")
	if c.Verbose {
		fmt.Printf("  → kubectl %s\n", strings.Join(fullArgs, " "))
	}
	cmd := command(ctx, fullArgs)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("kubectl %s: %w", strings.Join(args[:min(len(args), 3)], " "), err)
	}
	return json.Unmarshal(out, result)
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

// Apply applies a YAML manifest from stdin.
func (c *Client) Apply(ctx context.Context, yaml string, namespace string) error {
	args := []string{"apply", "-f", "-"}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	fullArgs := c.buildArgs(args)
	cmd := command(ctx, fullArgs)
	cmd.Stdin = strings.NewReader(yaml)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl apply: %w\n%s", err, string(out))
	}
	return nil
}
