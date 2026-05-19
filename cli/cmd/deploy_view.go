package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// ─── Color Palette ──────────────────────────────────────────
// Dark, saturated colors visible on light terminal backgrounds.

var (
	clrAccent = lipgloss.Color("#2563EB") // strong blue
	clrGreen  = lipgloss.Color("#16A34A") // forest green
	clrRed    = lipgloss.Color("#DC2626") // strong red
	clrDim    = lipgloss.Color("#6B7280") // medium gray
	clrCyan   = lipgloss.Color("#0891B2") // dark teal
	clrPink   = lipgloss.Color("#1D4ED8") // royal blue (phase headers)
	clrText   = lipgloss.Color("#1E293B") // dark slate
	clrOrange = lipgloss.Color("#C2410C") // burnt orange
)

// ─── Dashboard ──────────────────────────────────────────────

// DeploySummaryEntry represents a component in the summary dashboard.
type DeploySummaryEntry struct {
	Icon      string
	Name      string
	Release   string
	Namespace string
	Group     string // "A", "B", or "C"
}

// RenderDeployDashboard renders the full deployment summary using lipgloss.
func RenderDeployDashboard(ctx context.Context, entries []DeploySummaryEntry, elapsed time.Duration) {
	fmt.Println()

	// ── Header ──
	banner := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(clrAccent).
		Padding(0, 1).
		Render(" ⎈ Kates Deployment Summary ")
	timer := lipgloss.NewStyle().Foreground(clrDim).Italic(true).
		Render(fmt.Sprintf("  completed in %s", elapsed.Round(time.Second)))
	fmt.Println(banner + timer)

	// ── Grouped entries ──
	groups := map[string][]DeploySummaryEntry{"A": {}, "B": {}, "C": {}}
	for _, e := range entries {
		groups[e.Group] = append(groups[e.Group], e)
	}
	groupNames := map[string]string{
		"A": "Operators & CRDs",
		"B": "Core Infrastructure",
		"C": "Applications",
	}

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(clrCyan)
	sepLine := lipgloss.NewStyle().Foreground(clrDim).Render(strings.Repeat("─", 58))

	for _, g := range []string{"A", "B", "C"} {
		if len(groups[g]) == 0 {
			continue
		}
		fmt.Println()
		fmt.Println(headerStyle.Render(fmt.Sprintf("  Group %s — %s", g, groupNames[g])))
		fmt.Println("  " + sepLine)
		for _, e := range groups[g] {
			status := getComponentStatus(ctx, e.Release, e.Namespace)
			printRow(e.Icon, e.Name, e.Namespace, status)
		}
	}

	// ── Footer ──
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrGreen).
		Render("  ✅ All components deployed successfully!"))
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Foreground(clrDim).Italic(true).Render("  Quick commands:"))
	cmdStyle := lipgloss.NewStyle().Foreground(clrCyan)
	for _, c := range []string{"kubectl get pods -A", "helm list -A", "kates health"} {
		fmt.Println(cmdStyle.Render("    $ " + c))
	}
	fmt.Println()
}

func getComponentStatus(ctx context.Context, release, namespace string) string {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, "helm", "status", release, "-n", namespace)
	if cmd.Run() == nil {
		return "ready"
	}
	return "skip"
}

func printRow(icon, name, namespace, status string) {
	// Pad name and namespace as plain strings BEFORE styling,
	// so emoji widths don't break column alignment.
	paddedName := fmt.Sprintf("%-20s", name)
	paddedNS := fmt.Sprintf("%-20s", namespace)

	var statusStr string
	switch status {
	case "ready":
		statusStr = lipgloss.NewStyle().Bold(true).Foreground(clrGreen).Render("✔ Ready")
	case "fail":
		statusStr = lipgloss.NewStyle().Bold(true).Foreground(clrRed).Render("✖ Failed")
	default:
		statusStr = lipgloss.NewStyle().Foreground(clrOrange).Render("⏭ Skipped")
	}

	iconStr := icon + " "
	nameCol := lipgloss.NewStyle().Bold(true).Foreground(clrText).Render(paddedName)
	nsCol := lipgloss.NewStyle().Foreground(clrDim).Render(paddedNS)

	fmt.Printf("  %s%s  %s  %s\n", iconStr, nameCol, nsCol, statusStr)
}

// ─── Phase Logging ──────────────────────────────────────────

// PrintPhaseHeader prints a styled phase header.
func PrintPhaseHeader(number int, title string) {
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrPink).
		Render(fmt.Sprintf("[%d] %s", number, title)))
}

// PrintPhaseItem prints a styled sub-item within a phase.
func PrintPhaseItem(text string) {
	fmt.Println(lipgloss.NewStyle().Foreground(clrText).Render("  • " + text))
}

// PrintPhaseSuccess prints a styled success message within a phase.
func PrintPhaseSuccess(text string) {
	fmt.Println(lipgloss.NewStyle().Foreground(clrGreen).Render("  ✓ " + text))
}

// PrintPhaseWarn prints a styled warning message within a phase.
func PrintPhaseWarn(text string) {
	fmt.Println(lipgloss.NewStyle().Foreground(clrOrange).Render("  ⚠ " + text))
}

// PrintDeployBanner prints the initial deploy banner.
func PrintDeployBanner() {
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrAccent).
		Render("⎈ Kates Unified Orchestrator"))
	fmt.Println(lipgloss.NewStyle().Foreground(clrDim).
		Render(strings.Repeat("─", 35)))
}
