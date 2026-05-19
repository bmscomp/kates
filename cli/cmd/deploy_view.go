package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// ─── Color Palette (Dracula) ────────────────────────────────

var (
	colorPrimary = lipgloss.Color("#BD93F9") // purple
	colorSuccess = lipgloss.Color("#50FA7B") // green
	colorWarning = lipgloss.Color("#FFB86C") // orange
	colorError   = lipgloss.Color("#FF5555") // red
	colorMuted   = lipgloss.Color("#6272A4") // gray-blue
	colorCyan    = lipgloss.Color("#8BE9FD") // cyan
	colorPink    = lipgloss.Color("#FF79C6") // pink
	colorFg      = lipgloss.Color("#F8F8F2") // foreground
)

// ─── Styles ─────────────────────────────────────────────────

var (
	bannerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorFg).
			Background(colorPrimary).
			Padding(0, 1)

	groupHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorCyan)

	componentNameStyle = lipgloss.NewStyle().
				Foreground(colorFg).
				Width(22)

	namespaceStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Width(18)

	statusReadyStyle = lipgloss.NewStyle().
				Foreground(colorSuccess).
				Bold(true)

	statusSkipStyle = lipgloss.NewStyle().
			Foreground(colorWarning)

	statusFailStyle = lipgloss.NewStyle().
			Foreground(colorError).
			Bold(true)

	separatorStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	phaseStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPink)

	elapsedStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Italic(true)
)

// ─── Dashboard Rendering ────────────────────────────────────

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
	fmt.Println(bannerStyle.Render(" ⎈ Kates Deployment Summary ") + "  " + elapsedStyle.Render(fmt.Sprintf("(%s)", elapsed.Round(time.Second))))

	// Group entries
	groups := map[string][]DeploySummaryEntry{"A": {}, "B": {}, "C": {}}
	for _, e := range entries {
		groups[e.Group] = append(groups[e.Group], e)
	}
	groupNames := map[string]string{
		"A": "Operators & CRDs",
		"B": "Core Infrastructure",
		"C": "Applications",
	}

	for _, g := range []string{"A", "B", "C"} {
		if len(groups[g]) == 0 {
			continue
		}
		fmt.Println()
		fmt.Println(groupHeaderStyle.Render(fmt.Sprintf("  Group %s — %s", g, groupNames[g])))
		fmt.Println(separatorStyle.Render("  " + strings.Repeat("─", 58)))
		for _, e := range groups[g] {
			status := getComponentStatus(ctx, e.Release, e.Namespace)
			renderDashboardRow(e.Icon, e.Name, e.Namespace, status)
		}
	}

	// Footer
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(colorSuccess).Render("  ✅ All components deployed successfully!"))

	// Hints
	fmt.Println(lipgloss.NewStyle().Foreground(colorMuted).Italic(true).Render("  Quick commands:"))
	for _, h := range []string{
		"kubectl get pods -A",
		"helm list -A",
		"kates health",
	} {
		fmt.Println(lipgloss.NewStyle().Foreground(colorCyan).Render("    $ " + h))
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

func renderDashboardRow(icon, name, namespace, status string) {
	var statusCell string
	switch status {
	case "ready":
		statusCell = statusReadyStyle.Render("✅ Ready")
	case "skip":
		statusCell = statusSkipStyle.Render("⏭️  Skipped")
	case "fail":
		statusCell = statusFailStyle.Render("✖  Failed")
	default:
		statusCell = statusSkipStyle.Render("◌ Unknown")
	}

	nameCell := componentNameStyle.Render(icon + " " + name)
	nsCell := namespaceStyle.Render(namespace)

	fmt.Println("  " + lipgloss.JoinHorizontal(lipgloss.Top, nameCell, nsCell, statusCell))
}

// ─── Phase Logging ──────────────────────────────────────────

// PrintPhaseHeader prints a styled phase header.
func PrintPhaseHeader(number int, title string) {
	fmt.Println()
	fmt.Println(phaseStyle.Render(fmt.Sprintf("[%d] %s", number, title)))
}

// PrintPhaseItem prints a styled sub-item within a phase.
func PrintPhaseItem(text string) {
	fmt.Println(lipgloss.NewStyle().Foreground(colorFg).Render("  • " + text))
}

// PrintPhaseSuccess prints a styled success message within a phase.
func PrintPhaseSuccess(text string) {
	fmt.Println(lipgloss.NewStyle().Foreground(colorSuccess).Render("  ✓ " + text))
}

// PrintPhaseWarn prints a styled warning message within a phase.
func PrintPhaseWarn(text string) {
	fmt.Println(lipgloss.NewStyle().Foreground(colorWarning).Render("  ⚠ " + text))
}

// PrintDeployBanner prints the initial deploy banner.
func PrintDeployBanner() {
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(colorPrimary).Render("⎈ Kates Unified Orchestrator"))
	fmt.Println(separatorStyle.Render(strings.Repeat("─", 40)))
}
