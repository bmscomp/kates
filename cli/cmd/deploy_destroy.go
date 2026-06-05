package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/klster/kates-cli/output"
)

func runDestroy(ctx context.Context, entries []DeploySummaryEntry, deleteNamespaces bool) error {
	deployForceUpgrade = false // ensure we check actual deployment status

	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrRed).Render("🗑️  Kates Stack Teardown"))
	fmt.Println(lipgloss.NewStyle().Foreground(clrDim).Render(strings.Repeat("─", 35)))
	fmt.Println()

	if !deployDestroyYes {
		var confirm bool
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title("Are you sure you want to tear down the Kates stack?").
					Description("This will uninstall all deployed components.").
					Value(&confirm),
			),
		)
		if err := form.Run(); err != nil || !confirm {
			fmt.Println("Teardown cancelled.")
			os.Exit(0)
		}
	}

	// Reverse entries (C -> B -> A)
	for i := len(entries)/2 - 1; i >= 0; i-- {
		opp := len(entries) - 1 - i
		entries[i], entries[opp] = entries[opp], entries[i]
	}

	var removed, failed, skipped int
	uniqueNamespaces := make(map[string]bool)

	for _, e := range entries {
		uniqueNamespaces[e.Namespace] = true

		if isHelmReleaseDeployedFn(ctx, e.Release, e.Namespace) {
			prefix := lipgloss.NewStyle().Foreground(clrAccent).Render("  ◓")
			fmt.Printf("%s Uninstalling %s (%s)...\n", prefix, e.Name, e.Release)

			err := runHelmFn(ctx, "uninstall", e.Release, "-n", e.Namespace)
			if err != nil {
				fmt.Printf("  %s Failed to uninstall %s\n", output.ErrorStyle.Render("✖"), e.Name)
				failed++
			} else {
				fmt.Printf("  %s Uninstalled %s\n", output.SuccessStyle.Render("✔"), e.Name)
				removed++
			}
		} else {
			fmt.Printf("  %s Skipped %s (not deployed)\n", output.DimStyle.Render("○"), e.Name)
			skipped++
		}
	}

	if deleteNamespaces {
		fmt.Println()
		fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrCyan).Render("Deleting namespaces..."))
		for ns := range uniqueNamespaces {
			if ns == "default" || ns == "kube-system" || ns == "kube-public" || ns == "kube-node-lease" {
				continue
			}
			fmt.Printf("  Deleting namespace %s...\n", ns)
			err := runExecFn(ctx, "kubectl", "delete", "namespace", ns, "--ignore-not-found")
			if err != nil {
				fmt.Printf("  %s Failed to delete namespace %s\n", output.ErrorStyle.Render("✖"), ns)
			} else {
				fmt.Printf("  %s Deleted namespace %s\n", output.SuccessStyle.Render("✔"), ns)
			}
		}
	}

	fmt.Println()
	fmt.Printf("  Summary: %s components removed, %s failed, %s skipped\n\n",
		output.SuccessStyle.Render(fmt.Sprintf("%d", removed)),
		output.ErrorStyle.Render(fmt.Sprintf("%d", failed)),
		output.DimStyle.Render(fmt.Sprintf("%d", skipped)),
	)

	return nil
}
