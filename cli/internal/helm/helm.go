// Package helm is a thin typed wrapper over a proc.Runner for the helm calls
// the CLI makes: it adds the namespace, prints the command when asked, and
// parses the outputs that have a shape.
package helm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/bmscomp/kates/cli/internal/proc"
)

// ErrNotFound is wrapped by the error Status returns for a release helm does
// not know.
var ErrNotFound = errors.New("release not found")

// Client runs helm through a proc.Runner.
type Client struct {
	// Namespace is the namespace every call carries as -n, when set.
	Namespace string
	// Verbose prints commands before executing when true.
	Verbose bool

	runner proc.Runner
}

// New creates a helm client with the given namespace that runs helm with
// the OS runner.
func New(namespace string) *Client {
	return NewWith(proc.OS{}, namespace)
}

// NewWith creates a helm client that runs helm through runner.
func NewWith(runner proc.Runner, namespace string) *Client {
	return &Client{Namespace: namespace, runner: runner}
}

// buildArgs prepends -n when a namespace is set.
func (c *Client) buildArgs(args []string) []string {
	if c.Namespace != "" {
		return append([]string{"-n", c.Namespace}, args...)
	}
	return args
}

// output runs helm with args, printing the command first when Verbose.
func (c *Client) output(ctx context.Context, args []string) ([]byte, error) {
	fullArgs := c.buildArgs(args)
	if c.Verbose {
		fmt.Printf("  → helm %s\n", strings.Join(fullArgs, " "))
	}
	return c.runner.Output(ctx, "helm", fullArgs...)
}

// Run executes helm with the given arguments and returns its trimmed
// standard output. A failure's error carries helm's standard error.
func (c *Client) Run(ctx context.Context, args ...string) (string, error) {
	out, err := c.output(ctx, args)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Status returns the status of a release ("deployed", "failed",
// "pending-install", …) as helm reports it, in lower case. A release helm
// does not know is an error wrapping ErrNotFound; any other failure is
// returned as is. It used to answer every failure with ("", nil), so a
// cluster that was unreachable looked like a release that was absent.
func (c *Client) Status(ctx context.Context, release string) (string, error) {
	out, err := c.output(ctx, []string{"status", release, "-o", "json"})
	if err != nil {
		if isNotFound(err) {
			return "", fmt.Errorf("helm release %q: %w", release, ErrNotFound)
		}
		return "", err
	}
	var status struct {
		Info struct {
			Status string `json:"status"`
		} `json:"info"`
	}
	if err := json.Unmarshal(out, &status); err != nil {
		return "", fmt.Errorf("helm status %s: %w", release, err)
	}
	return strings.ToLower(status.Info.Status), nil
}

// isNotFound reports whether a failed helm call said the release does not
// exist, in the words helm uses: "Error: release: not found".
func isNotFound(err error) bool {
	stderr := strings.ToLower(proc.Stderr(err))
	return strings.Contains(stderr, "release: not found") || strings.Contains(stderr, "release not found")
}

// IsDeployed reports whether release exists with status "deployed". A
// release helm does not know is (false, nil); a failure to ask is an error,
// so a caller can tell an absent release from an unreachable cluster.
func (c *Client) IsDeployed(ctx context.Context, release string) (bool, error) {
	status, err := c.Status(ctx, release)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return status == "deployed", nil
}

// Release is one row of `helm list -o json`.
type Release struct {
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	Revision   string `json:"revision"`
	Updated    string `json:"updated"`
	Status     string `json:"status"`
	Chart      string `json:"chart"`
	AppVersion string `json:"app_version"`
}

// List returns the releases helm knows, in every state (-a: a failed or
// pending release is still a release): those in the client's namespace, or
// in every namespace when it has none.
func (c *Client) List(ctx context.Context) ([]Release, error) {
	args := []string{"list", "-a", "-o", "json"}
	if c.Namespace == "" {
		args = []string{"list", "-A", "-a", "-o", "json"}
	}
	out, err := c.output(ctx, args)
	if err != nil {
		return nil, err
	}
	var releases []Release
	if len(strings.TrimSpace(string(out))) == 0 {
		return releases, nil
	}
	if err := json.Unmarshal(out, &releases); err != nil {
		return nil, fmt.Errorf("helm list: %w", err)
	}
	return releases, nil
}

// DependencyBuild runs helm dependency build for a chart directory.
func (c *Client) DependencyBuild(ctx context.Context, chartDir string) error {
	_, err := c.Run(ctx, "dependency", "build", chartDir)
	return err
}
