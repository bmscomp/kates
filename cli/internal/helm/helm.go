package helm

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Client wraps helm command execution with consistent error handling.
type Client struct {
	// Namespace is the default namespace for helm operations.
	Namespace string
	// Verbose prints commands before executing when true.
	Verbose bool
}

// New creates a helm client with the given namespace.
func New(namespace string) *Client {
	return &Client{Namespace: namespace}
}

// buildArgs prepends --namespace if set.
func (c *Client) buildArgs(args []string) []string {
	if c.Namespace != "" {
		return append([]string{"-n", c.Namespace}, args...)
	}
	return args
}

// Run executes helm with the given arguments. Returns combined output and error.
func (c *Client) Run(ctx context.Context, args ...string) (string, error) {
	fullArgs := c.buildArgs(args)
	if c.Verbose {
		fmt.Printf("  → helm %s\n", strings.Join(fullArgs, " "))
	}
	cmd := exec.CommandContext(ctx, "helm", fullArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("helm %s: %w\n%s", strings.Join(args[:min(len(args), 3)], " "), err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Status checks the status of a helm release. Returns empty string if not found.
func (c *Client) Status(ctx context.Context, release string) (string, error) {
	out, err := c.Run(ctx, "status", release, "--short")
	if err != nil {
		return "", nil // release not found is not an error
	}
	return out, nil
}

// DependencyBuild runs helm dependency build for a chart directory.
func (c *Client) DependencyBuild(ctx context.Context, chartDir string) error {
	_, err := c.Run(ctx, "dependency", "build", chartDir)
	return err
}

// Install installs a helm chart with the given values.
func (c *Client) Install(ctx context.Context, release, chart string, setValues map[string]string, extraArgs ...string) error {
	args := []string{"install", release, chart}
	for k, v := range setValues {
		args = append(args, "--set", fmt.Sprintf("%s=%s", k, v))
	}
	args = append(args, extraArgs...)
	_, err := c.Run(ctx, args...)
	return err
}

// Upgrade upgrades a helm release with the given values.
func (c *Client) Upgrade(ctx context.Context, release, chart string, setValues map[string]string, extraArgs ...string) error {
	args := []string{"upgrade", release, chart}
	for k, v := range setValues {
		args = append(args, "--set", fmt.Sprintf("%s=%s", k, v))
	}
	args = append(args, extraArgs...)
	_, err := c.Run(ctx, args...)
	return err
}

// Uninstall removes a helm release.
func (c *Client) Uninstall(ctx context.Context, release string) error {
	_, err := c.Run(ctx, "uninstall", release)
	return err
}

// Test runs helm test for a release.
func (c *Client) Test(ctx context.Context, release string, extraArgs ...string) error {
	args := append([]string{"test", release}, extraArgs...)
	_, err := c.Run(ctx, args...)
	return err
}

// IsDeployed returns true if a release exists and is deployed.
func (c *Client) IsDeployed(ctx context.Context, release string) bool {
	_, err := c.Run(ctx, "status", release)
	return err == nil
}
