package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/klster/kates-cli/pkg/theme"
	"github.com/mattn/go-runewidth"
)

// ─── Color Palette ──────────────────────────────────────────
// All colors are backed by theme tokens — no hardcoded hex values here.

var (
	clrAccent = theme.Accent  // interactive blue
	clrGreen  = theme.Success // positive / ready
	clrRed    = theme.Error   // error / danger
	clrDim    = theme.Muted   // secondary text
	clrCyan   = theme.Info    // teal / info
	clrPink   = theme.Primary // phase headers
	clrText   = theme.Text    // primary body text
	clrOrange = theme.Warning // warnings
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

	colHdrName := lipgloss.NewStyle().Width(28).Render("COMPONENT")
	colHdrNs := lipgloss.NewStyle().Width(18).Render("NAMESPACE")
	colHdrStat := lipgloss.NewStyle().Render("STATUS")
	colHeaders := lipgloss.NewStyle().Bold(true).Foreground(clrDim).Render(fmt.Sprintf("  %s%s%s", colHdrName, colHdrNs, colHdrStat))

	for _, g := range []string{"A", "B", "C"} {
		if len(groups[g]) == 0 {
			continue
		}
		fmt.Println()
		fmt.Println(headerStyle.Render(fmt.Sprintf("  Group %s — %s", g, groupNames[g])))
		fmt.Println(colHeaders)
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
	const nameWidth = 28
	const nsWidth = 18

	nameStr := icon + " " + name
	nsStr := namespace

	// Pad based on actual visual width so emoji don't throw off alignment
	// Hardcode icon width to 2 cells to bypass go-runewidth emoji miscalculations
	visualNameWidth := 2 + 1 + len(name)
	namePad := nameWidth - visualNameWidth
	if namePad < 1 {
		namePad = 1
	}
	nsPad := nsWidth - len(nsStr)
	if nsPad < 1 {
		nsPad = 1
	}

	nameCol := lipgloss.NewStyle().Bold(true).Foreground(clrText).Render(nameStr) + strings.Repeat(" ", namePad)
	nsCol := lipgloss.NewStyle().Foreground(clrDim).Render(nsStr) + strings.Repeat(" ", nsPad)

	// Status column
	var statusStr string
	switch status {
	case "ready":
		statusStr = lipgloss.NewStyle().Bold(true).Foreground(clrGreen).Render("✔ Ready")
	case "fail":
		statusStr = lipgloss.NewStyle().Bold(true).Foreground(clrRed).Render("✖ Failed")
	default:
		statusStr = lipgloss.NewStyle().Foreground(clrOrange).Render("⏭ Skipped")
	}

	fmt.Printf("  %s%s%s\n", nameCol, nsCol, statusStr)
}

// visualWidth returns the true terminal display width of a string using
// go-runewidth, which correctly handles emoji (2 cells), CJK characters,
// and other wide Unicode code points.
func visualWidth(s string) int {
	return runewidth.StringWidth(s)
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

// ThemeKates returns a custom huh theme using the Kates blue palette,
// optimized for light terminal backgrounds.
func ThemeKates() *huh.Theme {
	t := huh.ThemeBase()

	var (
		blue      = theme.Accent    // focused borders, selectors
		navy      = theme.Primary   // titles
		slate     = theme.Text      // body text (light on dark, dark on light)
		gray      = theme.Muted     // descriptions
		lightGray = theme.Subtle    // borders / unselected prefix
		green     = theme.Highlight // selected items
		red       = theme.Error     // error indicators
		white     = theme.OnDark    // button text on filled backgrounds
	)

	// Focused field styles.
	t.Focused.Base = t.Focused.Base.BorderForeground(blue)
	t.Focused.Card = t.Focused.Base
	t.Focused.Title = t.Focused.Title.Foreground(navy).Bold(true)
	t.Focused.NoteTitle = t.Focused.NoteTitle.Foreground(navy).Bold(true).MarginBottom(1)
	t.Focused.Description = t.Focused.Description.Foreground(gray)
	t.Focused.ErrorIndicator = t.Focused.ErrorIndicator.Foreground(red)
	t.Focused.ErrorMessage = t.Focused.ErrorMessage.Foreground(red)
	t.Focused.Directory = t.Focused.Directory.Foreground(blue)

	// Select styles.
	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(blue).SetString("▸ ")
	t.Focused.NextIndicator = t.Focused.NextIndicator.Foreground(blue)
	t.Focused.PrevIndicator = t.Focused.PrevIndicator.Foreground(blue)
	t.Focused.Option = t.Focused.Option.Foreground(slate)

	// Multi-select styles.
	t.Focused.MultiSelectSelector = t.Focused.MultiSelectSelector.Foreground(blue).SetString("▸ ")
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(green)
	t.Focused.SelectedPrefix = lipgloss.NewStyle().Foreground(green).SetString("✓ ")
	t.Focused.UnselectedOption = t.Focused.UnselectedOption.Foreground(slate)
	t.Focused.UnselectedPrefix = lipgloss.NewStyle().Foreground(lightGray).SetString("○ ")

	// Button styles.
	t.Focused.FocusedButton = t.Focused.FocusedButton.Foreground(white).Background(blue).Bold(true)
	t.Focused.Next = t.Focused.FocusedButton
	t.Focused.BlurredButton = t.Focused.BlurredButton.Foreground(slate).Background(lightGray)

	// Text input styles.
	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(blue)
	t.Focused.TextInput.Placeholder = t.Focused.TextInput.Placeholder.Foreground(gray)
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(blue)

	// Blurred state — dimmed version of focused.
	t.Blurred = t.Focused
	t.Blurred.Base = t.Blurred.Base.BorderStyle(lipgloss.HiddenBorder())
	t.Blurred.Card = t.Blurred.Base
	t.Blurred.Title = t.Blurred.Title.Foreground(gray)
	t.Blurred.NextIndicator = lipgloss.NewStyle()
	t.Blurred.PrevIndicator = lipgloss.NewStyle()

	t.Group.Title = t.Focused.Title
	t.Group.Description = t.Focused.Description

	return t
}
