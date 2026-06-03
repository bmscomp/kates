package cmd

import (
	"fmt"

	"github.com/klster/kates-cli/output"
)

// renderDeployPreview displays a dry-run preview of what would be deployed.
func renderDeployPreview(entries []DeploySummaryEntry, existingReleases map[string]bool) {
	fmt.Println()
	output.Header("🔍 Deployment Preview (Dry Run)")
	fmt.Println()

	groupNames := map[string]string{
		"A": "Operators & CRDs",
		"B": "Core Infrastructure",
		"C": "Applications",
	}
	groupOrder := []string{"A", "B", "C"}

	var deployCount, skipCount int
	var namespaces = make(map[string]bool)

	for _, group := range groupOrder {
		var groupEntries []DeploySummaryEntry
		for _, e := range entries {
			if e.Group == group {
				groupEntries = append(groupEntries, e)
			}
		}
		if len(groupEntries) == 0 {
			continue
		}

		output.SubHeader(fmt.Sprintf("Group %s — %s", group, groupNames[group]))

		for _, e := range groupEntries {
			namespaces[e.Namespace] = true
			releaseKey := e.Release + "/" + e.Namespace
			if existingReleases[releaseKey] {
				skipCount++
				fmt.Printf("  %s  %-28s → %-18s %s\n",
					e.Icon,
					e.Name,
					output.DimStyle.Render(e.Namespace),
					output.DimStyle.Render("(already deployed, skip)"),
				)
			} else {
				deployCount++
				fmt.Printf("  %s  %-28s → %-18s %s\n",
					e.Icon,
					e.Name,
					output.AccentStyle.Render(e.Namespace),
					output.SuccessStyle.Render("(will deploy)"),
				)
			}
		}
		fmt.Println()
	}

	output.Divider()
	fmt.Printf("  Total: %s to deploy, %s to skip, across %s\n\n",
		output.AccentStyle.Render(fmt.Sprintf("%d components", deployCount)),
		output.DimStyle.Render(fmt.Sprintf("%d components", skipCount)),
		output.AccentStyle.Render(fmt.Sprintf("%d namespaces", len(namespaces))),
	)

	if deployCount > 0 {
		estimate := "~5-8 minutes"
		if deployCount > 5 {
			estimate = "~10-15 minutes"
		}
		fmt.Printf("  %s Estimated time: %s\n", output.DimStyle.Render("⏱"), estimate)
	} else {
		fmt.Printf("  %s Nothing to deploy — all components are up to date.\n", output.SuccessStyle.Render("✔"))
	}

	fmt.Printf("\n  %s\n\n", output.DimStyle.Render("Run without --dry-run to execute the deployment."))
}
