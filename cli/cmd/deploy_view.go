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

var (
	colorPrimary   = lipgloss.Color("#BD93F9") // purple
	colorSuccess   = lipgloss.Color("#50FA7B") // green
	colorWarning   = lipgloss.Color("#FFB86C") // orange
	colorError     = lipgloss.Color("#FF5555") // red
	colorMuted     = lipgloss.Color("#6272A4") // gray-blue
	colorCyan      = lipgloss.Color("#8BE9FD") // cyan
	colorPink      = lipgloss.Color("#FF79C6") // pink
	colorFg        = lipgloss.Color("#F8F8F2") // foreground
	colorBg        = lipgloss.Color("#282A36") // background
	colorCurrentBg = lipgloss.Color("#44475A") // current line bg
)

// ─── Styles ─────────────────────────────────────────────────

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			PaddingLeft(1)

	bannerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorFg).
			Background(colorPrimary).
			Padding(0, 2).
			MarginBottom(1)

	groupHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(colorCyan).
				PaddingLeft(2)

	rowStyle = lipgloss.NewStyle().
			PaddingLeft(2)

	componentNameStyle = lipgloss.NewStyle().
				Foreground(colorFg).
				Width(24)

	namespaceStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Width(20)

	statusReadyStyle = lipgloss.NewStyle().
				Foreground(colorSuccess).
				Bold(true).
				Width(14)

	statusSkipStyle = lipgloss.NewStyle().
			Foreground(colorWarning).
			Width(14)

	statusFailStyle = lipgloss.NewStyle().
			Foreground(colorError).
			Bold(true).
			Width(14)

	separatorStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorPrimary).
			Padding(1, 2)

	hintStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Italic(true).
			PaddingLeft(2).
			MarginTop(1)

	phaseStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPink).
			PaddingLeft(1)

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
	// Header
	fmt.Println()
	header := bannerStyle.Render("⎈  Kates Deployment Summary")
	fmt.Println(header)
	fmt.Println()

	elapsedStr := elapsedStyle.Render(fmt.Sprintf("Total time: %s", elapsed.Round(time.Second)))
	fmt.Println(elapsedStr)
	fmt.Println()

	// Group entries
	groups := map[string][]DeploySummaryEntry{
		"A": {},
		"B": {},
		"C": {},
	}
	for _, e := range entries {
		groups[e.Group] = append(groups[e.Group], e)
	}

	groupNames := map[string]string{
		"A": "Operators & CRDs",
		"B": "Core Infrastructure",
		"C": "Applications",
	}

	sep := separatorStyle.Render(strings.Repeat("─", 62))

	for _, g := range []string{"A", "B", "C"} {
		if len(groups[g]) == 0 {
			continue
		}

		groupLabel := groupHeaderStyle.Render(fmt.Sprintf("Group %s — %s", g, groupNames[g]))
		fmt.Println(groupLabel)
		fmt.Println(separatorStyle.Render("  " + strings.Repeat("─", 60)))

		for _, e := range groups[g] {
			status := getComponentStatus(ctx, e.Release, e.Namespace)
			renderDashboardRow(e.Icon, e.Name, e.Namespace, status)
		}
		fmt.Println()
	}

	_ = sep

	// Footer
	fmt.Println(lipgloss.NewStyle().
		Bold(true).
		Foreground(colorSuccess).
		PaddingLeft(2).
		Render("✅ All components deployed successfully!"))
	fmt.Println()

	// Hints
	hints := []string{
		"kubectl get pods -A | grep -E '(kafka|kates|jaeger|litmus|cert-manager|strimzi)'",
		"helm list -A",
		"kates health",
	}
	fmt.Println(hintStyle.Render("Quick commands:"))
	for _, h := range hints {
		fmt.Println(lipgloss.NewStyle().
			Foreground(colorCyan).
			PaddingLeft(4).
			Render("$ " + h))
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

	fmt.Println(rowStyle.Render(
		lipgloss.JoinHorizontal(lipgloss.Top, nameCell, nsCell, statusCell),
	))
}

// ─── Phase Logging ──────────────────────────────────────────

// PrintPhaseHeader prints a styled phase header.
func PrintPhaseHeader(number int, title string) {
	label := phaseStyle.Render(fmt.Sprintf("[%d] %s", number, title))
	fmt.Println()
	fmt.Println(label)
}

// PrintPhaseItem prints a styled sub-item within a phase.
func PrintPhaseItem(text string) {
	fmt.Println(lipgloss.NewStyle().
		Foreground(colorFg).
		PaddingLeft(4).
		Render("• " + text))
}

// PrintPhaseSuccess prints a styled success message within a phase.
func PrintPhaseSuccess(text string) {
	fmt.Println(lipgloss.NewStyle().
		Foreground(colorSuccess).
		PaddingLeft(4).
		Render("✓ " + text))
}

// PrintPhaseWarn prints a styled warning message within a phase.
func PrintPhaseWarn(text string) {
	fmt.Println(lipgloss.NewStyle().
		Foreground(colorWarning).
		PaddingLeft(4).
		Render("⚠ " + text))
}

// PrintDeployBanner prints the initial deploy banner.
func PrintDeployBanner() {
	banner := lipgloss.NewStyle().
		Bold(true).
		Foreground(colorPrimary).
		Render("⎈  Kates Unified Orchestrator")
	fmt.Println()
	fmt.Println(banner)
	fmt.Println(separatorStyle.Render(strings.Repeat("─", 45)))
}
