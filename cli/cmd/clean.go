package cmd

import (
	"context"
	"fmt"
	"os"
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
	cleanVerbose  bool
	cleanTopology string
)

func init() {
	cleanCmd.Flags().BoolVar(&cleanForce, "force", false, "Skip confirmation prompt")
	cleanCmd.Flags().BoolVarP(&cleanVerbose, "verbose", "v", false, "Show full command output during cleanup")
	cleanCmd.Flags().StringVar(&cleanTopology, "topology", "", "Topology to clean: 'isolated' or 'single'. If empty, cleans both.")
	rootCmd.AddCommand(cleanCmd)
}

// helmRelease pairs a release name with its namespace.
type helmRelease struct {
	Name      string
	Namespace string
}

// cleanRun runs a command, printing it and its output when verbose is on.
func cleanRun(ctx context.Context, name string, args ...string) error {
	if cleanVerbose {
		fmt.Printf("    \033[2m$ %s %s\033[0m\n", name, strings.Join(args, " "))
	}
	cmd := exec.CommandContext(ctx, name, args...)
	if cleanVerbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd.Run()
}

// cleanRunOutput runs a command and returns its combined output.
func cleanRunOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	if cleanVerbose {
		fmt.Printf("    \033[2m$ %s %s\033[0m\n", name, strings.Join(args, " "))
	}
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if cleanVerbose && len(out) > 0 {
		fmt.Print(string(out))
	}
	return out, err
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
			cleanRun(dCtx, "kubectl", "delete", crdType, "--all", "-n", ns, "--ignore-not-found")
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
			out, _ := cleanRunOutput(pCtx, "kubectl", "get", crdType, "-n", ns, "-o", "jsonpath={.items[*].metadata.name}")
			pCancel()
			names := strings.Fields(strings.TrimSpace(string(out)))
			for _, name := range names {
				fCtx, fCancel := context.WithTimeout(ctx, 5*time.Second)
				cleanRun(fCtx, "kubectl", "patch", crdType, name, "-n", ns,
					"--type", "merge", "-p", `{"metadata":{"finalizers":[]}}`)
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
		out, err := cleanRunOutput(uCtx, "helm", "uninstall", r.Name, "-n", r.Namespace)
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
		cleanRun(dCtx, "kubectl", "delete", cr.Kind, cr.Name, "--ignore-not-found")
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
			err := cleanRun(nCtx, "kubectl", "delete", "namespace", ns, "--ignore-not-found")
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

