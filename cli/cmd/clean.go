package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove all Kates-managed resources and namespaces",
	Long: `Tears down the entire Kates stack by uninstalling all Helm releases
and deleting all managed namespaces. This is the inverse of 'kates deploy'.

Examples:
  # Interactive clean (asks for confirmation)
  kates clean

  # Force clean without confirmation
  kates clean --force

  # Clean only specific topology
  kates clean --topology isolated`,
	RunE: runClean,
}

var (
	cleanForce    bool
	cleanTopology string
)

func init() {
	cleanCmd.Flags().BoolVar(&cleanForce, "force", false, "Skip confirmation prompt")
	cleanCmd.Flags().StringVar(&cleanTopology, "topology", "", "Topology to clean: 'isolated' or 'single'. If empty, cleans both.")
	rootCmd.AddCommand(cleanCmd)
}

// helmRelease pairs a release name with its namespace.
type helmRelease struct {
	Name      string
	Namespace string
}

func runClean(cmd *cobra.Command, args []string) error {
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrRed).
		Render("⎈ Kates Clean — Teardown"))
	fmt.Println(lipgloss.NewStyle().Foreground(clrDim).
		Render(strings.Repeat("─", 35)))

	// All possible Helm releases across both topologies.
	releases := []helmRelease{
		// Group C (reverse order)
		{"chaos", "litmus"},
		{"chaos", "kates-stack"},
		{"kates", "kates"},
		{"kates", "kates-stack"},
		{"apicurio", "kafka"},
		{"apicurio", "kates-stack"},
		// Group B
		{"krafter", "kafka"},
		{"krafter", "kates-stack"},
		{"jaeger", "jaeger"},
		{"jaeger", "kafka"},
		{"jaeger", "kates-stack"},
		// Group A
		{"cert-manager", "cert-manager"},
		{"kyverno", "kyverno"},
		{"strimzi-operator", "strimzi-operator"},
	}

	// All managed namespaces.
	managedNamespaces := []string{
		"kates-stack",
		"kafka",
		"kates",
		"litmus",
		"jaeger",
		"strimzi-operator",
		"cert-manager",
		"kyverno",
	}

	// Cluster-scoped resources that need explicit deletion.
	clusterResources := []struct{ Kind, Name string }{
		{"clusterrole", "kates"},
		{"clusterrolebinding", "kates"},
		{"clusterrole", "litmus"},
		{"clusterrolebinding", "litmus"},
	}

	// Discover what's actually installed.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Foreground(clrWhite).Render("  Scanning for installed releases..."))

	var installedReleases []helmRelease
	for _, r := range releases {
		checkCtx, checkCancel := context.WithTimeout(ctx, 3*time.Second)
		out := exec.CommandContext(checkCtx, "helm", "status", r.Name, "-n", r.Namespace)
		if out.Run() == nil {
			installedReleases = append(installedReleases, r)
		}
		checkCancel()
	}

	var existingNamespaces []string
	for _, ns := range managedNamespaces {
		checkCtx, checkCancel := context.WithTimeout(ctx, 3*time.Second)
		out := exec.CommandContext(checkCtx, "kubectl", "get", "namespace", ns)
		if out.Run() == nil {
			existingNamespaces = append(existingNamespaces, ns)
		}
		checkCancel()
	}

	if len(installedReleases) == 0 && len(existingNamespaces) == 0 {
		fmt.Println(lipgloss.NewStyle().Foreground(clrGreen).Render("  ✓ Nothing to clean — cluster is already clean."))
		fmt.Println()
		return nil
	}

	// Show what will be removed.
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrYellow).
		Render("  The following resources will be removed:"))
	fmt.Println()

	if len(installedReleases) > 0 {
		fmt.Println(lipgloss.NewStyle().Foreground(clrWhite).Bold(true).Render("  Helm Releases:"))
		for _, r := range installedReleases {
			fmt.Printf("    %s  %s\n",
				lipgloss.NewStyle().Foreground(clrRed).Render("✖"),
				lipgloss.NewStyle().Foreground(clrWhite).Render(
					fmt.Sprintf("%-20s (namespace: %s)", r.Name, r.Namespace)),
			)
		}
	}

	if len(existingNamespaces) > 0 {
		fmt.Println()
		fmt.Println(lipgloss.NewStyle().Foreground(clrWhite).Bold(true).Render("  Namespaces:"))
		for _, ns := range existingNamespaces {
			fmt.Printf("    %s  %s\n",
				lipgloss.NewStyle().Foreground(clrRed).Render("✖"),
				lipgloss.NewStyle().Foreground(clrWhite).Render(ns),
			)
		}
	}
	fmt.Println()

	// Confirmation.
	if !cleanForce {
		var confirmed bool
		err := huh.NewConfirm().
			Title("Are you sure you want to delete all Kates resources?").
			Description("This action cannot be undone.").
			Affirmative("Yes, clean everything").
			Negative("Cancel").
			Value(&confirmed).
			WithTheme(huh.ThemeDracula()).
			Run()
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Println(lipgloss.NewStyle().Foreground(clrDim).Render("  Cancelled."))
			return nil
		}
	}

	fmt.Println()

	// 1. Uninstall Helm releases (reverse deploy order).
	for _, r := range installedReleases {
		label := fmt.Sprintf("  Uninstalling %s from %s...", r.Name, r.Namespace)
		fmt.Print(lipgloss.NewStyle().Foreground(clrWhite).Render(label))

		unCtx, unCancel := context.WithTimeout(ctx, 2*time.Minute)
		out, err := exec.CommandContext(unCtx, "helm", "uninstall", r.Name, "-n", r.Namespace).CombinedOutput()
		unCancel()

		if err != nil {
			fmt.Println(lipgloss.NewStyle().Foreground(clrRed).Render(" ✖ " + strings.TrimSpace(string(out))))
		} else {
			fmt.Println(lipgloss.NewStyle().Foreground(clrGreen).Render(" ✔"))
		}
	}

	// 2. Delete cluster-scoped resources.
	for _, cr := range clusterResources {
		delCtx, delCancel := context.WithTimeout(ctx, 10*time.Second)
		exec.CommandContext(delCtx, "kubectl", "delete", cr.Kind, cr.Name, "--ignore-not-found").Run()
		delCancel()
	}

	// 3. Delete namespaces.
	fmt.Println()
	for _, ns := range existingNamespaces {
		label := fmt.Sprintf("  Deleting namespace %s...", ns)
		fmt.Print(lipgloss.NewStyle().Foreground(clrWhite).Render(label))

		nsCtx, nsCancel := context.WithTimeout(ctx, 90*time.Second)
		err := exec.CommandContext(nsCtx, "kubectl", "delete", "namespace", ns, "--ignore-not-found").Run()
		nsCancel()

		if err != nil {
			fmt.Println(lipgloss.NewStyle().Foreground(clrYellow).Render(" ⚠ timeout (may still be deleting)"))
		} else {
			fmt.Println(lipgloss.NewStyle().Foreground(clrGreen).Render(" ✔"))
		}
	}

	// Done.
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrGreen).
		Render("  ✅ Cluster cleaned successfully."))
	fmt.Println()
	return nil
}
