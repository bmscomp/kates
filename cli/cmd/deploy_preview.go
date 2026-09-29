package cmd

import (
	"fmt"
	"strings"

	"github.com/bmscomp/kates/cli/output"
	"github.com/charmbracelet/lipgloss"
)

// renderDeployPreview shows what `kates deploy` would do, and nothing else
// happens afterwards. It shares its column layout with the final summary
// (deploy_ui.go) on purpose: the preview and the result should be readable as
// the same table, so a component that moved between them is obvious.
func renderDeployPreview(entries []DeploySummaryEntry, existingReleases map[string]bool) {
	g := output.Glyphs()
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrAccent).Render("  Deployment plan") +
		lipgloss.NewStyle().Foreground(clrDim).Render("   dry run, nothing will be installed"))
	fmt.Println("  " + uiRule())

	var deployCount, skipCount int
	var willCreate []DeploySummaryEntry
	for _, e := range entries {
		if existingReleases[e.Release+"/"+e.Namespace] {
			skipCount++
			continue
		}
		deployCount++
		willCreate = append(willCreate, e)
	}

	// The grid, not a list: at eleven components the shape of the deployment
	// — which wave, which namespace, what is new — is something you scan.
	fmt.Println()
	fmt.Print(componentGrid(entries, gridOptions{
		Width:    uiWidth(),
		Planned:  true,
		Existing: existingReleases,
		Indent:   "  ",
	}))

	// ── Footer ──
	fmt.Println()
	fmt.Println("  " + uiRule())

	namespaces := namespaceList(willCreate)
	switch {
	case deployCount == 0:
		fmt.Printf("  %s Nothing to do — every component is already deployed.\n",
			lipgloss.NewStyle().Foreground(clrGreen).Render(g.Check))
	default:
		fmt.Printf("  %s to deploy, %s already present\n",
			lipgloss.NewStyle().Bold(true).Foreground(clrAccent).
				Render(plural(deployCount, "component", "components")),
			lipgloss.NewStyle().Foreground(clrDim).Render(fmt.Sprintf("%d", skipCount)))
		// Namespaces by name rather than by count: "8 namespaces" is a number
		// nobody can check, and the list is what tells you a typo in --kafka-ns
		// is about to create `kafk`.
		fmt.Printf("  %s %s\n",
			lipgloss.NewStyle().Foreground(clrDim).Render("namespaces:"),
			strings.Join(namespaces, "  "))
	}

	fmt.Println()
	if deployCount > 0 {
		fmt.Printf("  %s\n",
			lipgloss.NewStyle().Foreground(clrDim).Italic(true).
				Render("Run the same command without --dry-run to apply it."))
	}
	// What a dry run did before this printed: the cluster gate and pre-flight
	// introspection, both reading only. It skipped the kind StorageClass
	// bootstrap and the introspection probes that create a namespace, a
	// Secret or pods (detect.Collector.ReadOnly). Its writes are local:
	// .build/values-detected.yaml, and any Strimzi chart it caches.
	fmt.Printf("  %s\n\n",
		lipgloss.NewStyle().Foreground(clrDim).Italic(true).
			Render("Nothing was created in the cluster and no Helm release was touched."))
}
