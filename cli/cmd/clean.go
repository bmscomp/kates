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
  kates clean            # interactive, with confirmation
  kates clean --force    # skip confirmation prompt`,
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
	// ── Banner ──
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrRed).
		Render("⎈ Kates Clean — Teardown"))
	fmt.Println(lipgloss.NewStyle().Foreground(clrDim).
		Render(strings.Repeat("─", 35)))

	// Helm releases ordered for correct teardown:
	// Apps first → Core infra → Operators LAST (so finalizer controllers can clean up).
	appReleases := []helmRelease{
		{"chaos", "litmus"}, {"chaos", "kates-stack"},
		{"kates", "kates"}, {"kates", "kates-stack"},
		{"apicurio", "kafka"}, {"apicurio", "kates-stack"},
	}
	coreReleases := []helmRelease{
		{"jaeger", "jaeger"}, {"jaeger", "kafka"}, {"jaeger", "kates-stack"},
		{"krafter", "kafka"}, {"krafter", "kates-stack"},
	}
	operatorReleases := []helmRelease{
		{"cert-manager", "cert-manager"},
		{"kyverno", "kyverno"},
		{"strimzi-operator", "strimzi-operator"},
	}
	allReleases := append(append(appReleases, coreReleases...), operatorReleases...)

	managedNamespaces := []string{
		"kates-stack", "kafka", "kates", "litmus",
		"jaeger", "strimzi-operator", "cert-manager", "kyverno",
	}

	// Strimzi CRD resource types that carry finalizers.
	strimziCRDTypes := []string{
		"kafkas.kafka.strimzi.io",
		"kafkatopics.kafka.strimzi.io",
		"kafkausers.kafka.strimzi.io",
		"kafkaconnects.kafka.strimzi.io",
		"kafkabridges.kafka.strimzi.io",
	}

	clusterResources := []struct{ Kind, Name string }{
		{"clusterrole", "kates"}, {"clusterrolebinding", "kates"},
		{"clusterrole", "litmus"}, {"clusterrolebinding", "litmus"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// ── Discovery ──
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Foreground(clrText).Render("  Scanning cluster..."))

	var installed []helmRelease
	for _, r := range allReleases {
		c, cn := context.WithTimeout(ctx, 3*time.Second)
		if exec.CommandContext(c, "helm", "status", r.Name, "-n", r.Namespace).Run() == nil {
			installed = append(installed, r)
		}
		cn()
	}

	var existingNS []string
	for _, ns := range managedNamespaces {
		c, cn := context.WithTimeout(ctx, 3*time.Second)
		if exec.CommandContext(c, "kubectl", "get", "namespace", ns).Run() == nil {
			existingNS = append(existingNS, ns)
		}
		cn()
	}

	if len(installed) == 0 && len(existingNS) == 0 {
		fmt.Println(lipgloss.NewStyle().Foreground(clrGreen).Bold(true).
			Render("  ✓ Cluster is already clean. Nothing to do."))
		fmt.Println()
		return nil
	}

	// ── Show what will be removed ──
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrOrange).
		Render("  The following resources will be removed:"))

	nameStyle := lipgloss.NewStyle().Bold(true).Foreground(clrText)
	dimStyle := lipgloss.NewStyle().Foreground(clrDim)
	bulletStyle := lipgloss.NewStyle().Foreground(clrRed)

	if len(installed) > 0 {
		fmt.Println()
		fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrCyan).Render("  Helm Releases:"))
		for _, r := range installed {
			fmt.Printf("  %s %s %s\n",
				bulletStyle.Render("✖"),
				nameStyle.Render(fmt.Sprintf("%-20s", r.Name)),
				dimStyle.Render(fmt.Sprintf("→ %s", r.Namespace)),
			)
		}
	}

	if len(existingNS) > 0 {
		fmt.Println()
		fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrCyan).Render("  Namespaces:"))
		for _, ns := range existingNS {
			fmt.Printf("  %s %s\n",
				bulletStyle.Render("✖"),
				nameStyle.Render(ns),
			)
		}
	}
	fmt.Println()

	// ── Confirmation ──
	if !cleanForce {
		var confirmed bool
		err := huh.NewConfirm().
			Title("Are you sure you want to delete all Kates resources?").
			Description("This action cannot be undone.").
			Affirmative("Yes, clean everything").
			Negative("Cancel").
			Value(&confirmed).
			WithTheme(ThemeKates()).
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

	okStyle := lipgloss.NewStyle().Foreground(clrGreen).Bold(true)
	errStyle := lipgloss.NewStyle().Foreground(clrRed)

	// ── 1. Delete Strimzi CRs FIRST (while operator is still running) ──
	// The Strimzi operator handles finalizer removal. If we delete it first,
	// finalizers become orphaned and namespaces hang in Terminating forever.
	fmt.Println(lipgloss.NewStyle().Foreground(clrCyan).Bold(true).
		Render("  Step 1: Removing Strimzi custom resources..."))
	for _, crdType := range strimziCRDTypes {
		for _, ns := range []string{"kafka", "kates-stack"} {
			dCtx, dCancel := context.WithTimeout(ctx, 30*time.Second)
			exec.CommandContext(dCtx, "kubectl", "delete", crdType, "--all", "-n", ns, "--ignore-not-found").Run()
			dCancel()
		}
	}
	// Wait briefly for the operator to process finalizer removal.
	time.Sleep(5 * time.Second)

	// ── 2. Strip orphaned finalizers on any remaining stuck resources ──
	fmt.Println(lipgloss.NewStyle().Foreground(clrCyan).Bold(true).
		Render("  Step 2: Stripping orphaned finalizers..."))
	for _, crdType := range strimziCRDTypes {
		for _, ns := range []string{"kafka", "kates-stack"} {
			pCtx, pCancel := context.WithTimeout(ctx, 10*time.Second)
			// Get any remaining resources and patch their finalizers to empty
			out, _ := exec.CommandContext(pCtx, "kubectl", "get", crdType, "-n", ns, "-o", "jsonpath={.items[*].metadata.name}").Output()
			pCancel()
			names := strings.Fields(strings.TrimSpace(string(out)))
			for _, name := range names {
				fCtx, fCancel := context.WithTimeout(ctx, 5*time.Second)
				exec.CommandContext(fCtx, "kubectl", "patch", crdType, name, "-n", ns,
					"--type", "merge", "-p", `{"metadata":{"finalizers":[]}}`).Run()
				fCancel()
			}
		}
	}

	// ── 3. Uninstall Helm releases (apps → core → operators) ──
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Foreground(clrCyan).Bold(true).
		Render("  Step 3: Uninstalling Helm releases..."))
	for _, r := range installed {
		label := fmt.Sprintf("  Uninstalling %-16s from %s", r.Name, r.Namespace)
		fmt.Print(lipgloss.NewStyle().Foreground(clrText).Render(label))

		uCtx, uCancel := context.WithTimeout(ctx, 2*time.Minute)
		out, err := exec.CommandContext(uCtx, "helm", "uninstall", r.Name, "-n", r.Namespace).CombinedOutput()
		uCancel()

		if err != nil {
			fmt.Println(errStyle.Render("  ✖ " + strings.TrimSpace(string(out))))
		} else {
			fmt.Println(okStyle.Render("  ✔"))
		}
	}

	// ── 4. Delete cluster-scoped resources ──
	for _, cr := range clusterResources {
		dCtx, dCancel := context.WithTimeout(ctx, 10*time.Second)
		exec.CommandContext(dCtx, "kubectl", "delete", cr.Kind, cr.Name, "--ignore-not-found").Run()
		dCancel()
	}

	// ── 5. Delete namespaces ──
	if len(existingNS) > 0 {
		fmt.Println()
		fmt.Println(lipgloss.NewStyle().Foreground(clrCyan).Bold(true).
			Render("  Step 4: Deleting namespaces..."))
		for _, ns := range existingNS {
			label := fmt.Sprintf("  Deleting namespace %s", ns)
			fmt.Print(lipgloss.NewStyle().Foreground(clrText).Render(label))

			nCtx, nCancel := context.WithTimeout(ctx, 3*time.Minute)
			err := exec.CommandContext(nCtx, "kubectl", "delete", "namespace", ns, "--ignore-not-found").Run()
			nCancel()

			if err != nil {
				fmt.Println(lipgloss.NewStyle().Foreground(clrOrange).Render("  ⚠ still terminating"))
			} else {
				fmt.Println(okStyle.Render("  ✔"))
			}
		}
	}

	// ── Done ──
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrGreen).
		Render("  ✅ Cluster cleaned successfully."))
	fmt.Println()
	return nil
}

