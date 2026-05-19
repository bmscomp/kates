package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
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
	// Column widths (in terminal cells).
	const nameColWidth = 22
	const nsColWidth = 22
	const statusColWidth = 12

	// Build icon+name string and pad to fixed visual width.
	// Emojis are typically 2 cells wide, so we account for that.
	raw := icon + " " + name
	visualLen := visualWidth(raw)
	pad := ""
	if visualLen < nameColWidth {
		pad = strings.Repeat(" ", nameColWidth-visualLen)
	}

	// Namespace column, padded.
	nsPad := ""
	if len(namespace) < nsColWidth {
		nsPad = strings.Repeat(" ", nsColWidth-len(namespace))
	}

	// Status column.
	var statusStr string
	switch status {
	case "ready":
		statusStr = lipgloss.NewStyle().Bold(true).Foreground(clrGreen).Render("✔ Ready")
	case "fail":
		statusStr = lipgloss.NewStyle().Bold(true).Foreground(clrRed).Render("✖ Failed")
	default:
		statusStr = lipgloss.NewStyle().Foreground(clrOrange).Render("⏭ Skipped")
	}

	nameCol := lipgloss.NewStyle().Bold(true).Foreground(clrText).Render(raw) + pad
	nsCol := lipgloss.NewStyle().Foreground(clrDim).Render(namespace) + nsPad

	fmt.Printf("  %s%s%s\n", nameCol, nsCol, statusStr)
}

// visualWidth estimates the display width of a string in terminal cells.
// ASCII chars = 1 cell, emoji/CJK = 2 cells.
func visualWidth(s string) int {
	w := 0
	for _, r := range s {
		if r > 0x1F00 { // emoji and symbols above this range are typically 2 cells
			w += 2
		} else {
			w += 1
		}
	}
	return w
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
		blue      = lipgloss.Color("#2563EB") // our accent
		navy      = lipgloss.Color("#1D4ED8") // royal blue
		slate     = lipgloss.Color("#1E293B") // dark text
		gray      = lipgloss.Color("#6B7280") // descriptions
		lightGray = lipgloss.Color("#D1D5DB") // borders
		green     = lipgloss.Color("#16A34A") // selected items
		red       = lipgloss.Color("#DC2626") // errors
		white     = lipgloss.Color("#FFFFFF") // button text
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
