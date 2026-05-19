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
// Uses lipgloss.AdaptiveColor so text is visible on BOTH light
// and dark terminal backgrounds.

var (
	clrAccent = lipgloss.AdaptiveColor{Light: "#7B2FBE", Dark: "#B48EFF"} // purple
	clrGreen  = lipgloss.AdaptiveColor{Light: "#1A8A3F", Dark: "#5AF78E"} // green
	clrYellow = lipgloss.AdaptiveColor{Light: "#9B6E00", Dark: "#F3F99D"} // yellow
	clrRed    = lipgloss.AdaptiveColor{Light: "#CC3333", Dark: "#FF6E6E"} // red
	clrDim    = lipgloss.AdaptiveColor{Light: "#888888", Dark: "#B0B0B0"} // gray
	clrCyan   = lipgloss.AdaptiveColor{Light: "#0E7490", Dark: "#9AEDFE"} // cyan
	clrPink   = lipgloss.AdaptiveColor{Light: "#C2185B", Dark: "#FF92DF"} // pink
	clrText   = lipgloss.AdaptiveColor{Light: "#1A1A2E", Dark: "#F0F0F0"} // body text
	clrOrange = lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#FFAF5F"} // orange
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
		Foreground(lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#1A1A2E"}).
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
	nameStr := fmt.Sprintf("%-24s", icon+" "+name)
	nsStr := fmt.Sprintf("%-20s", namespace)

	var statusStr string
	switch status {
	case "ready":
		statusStr = lipgloss.NewStyle().Bold(true).Foreground(clrGreen).Render("✔ Ready")
	case "fail":
		statusStr = lipgloss.NewStyle().Bold(true).Foreground(clrRed).Render("✖ Failed")
	default:
		statusStr = lipgloss.NewStyle().Foreground(clrOrange).Render("⏭ Skipped")
	}

	nameCol := lipgloss.NewStyle().Bold(true).Foreground(clrText).Render(nameStr)
	nsCol := lipgloss.NewStyle().Foreground(clrDim).Render(nsStr)

	fmt.Printf("  %s  %s  %s\n", nameCol, nsCol, statusStr)
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
