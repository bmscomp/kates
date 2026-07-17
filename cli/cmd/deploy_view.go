package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/theme"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
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
	Group     string        // "A", "B", or "C"
	Status    string        // "deployed", "skipped", "failed"
	Error     string        // error message if failed
	Duration  time.Duration // per-component deploy time
}

// RenderDeployDashboard renders the full deployment summary using lipgloss.
func RenderDeployDashboard(ctx context.Context, entries []DeploySummaryEntry, elapsed time.Duration) {
	fmt.Println()

	// ── Header ──
	banner := lipgloss.NewStyle().
		Bold(true).
		Foreground(theme.OnDark).
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

	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(clrCyan)
	// Width comes from the one shared helper; the -4 margin and 80 cap are
	// this view's layout choices, not a third opinion on terminal size.
	termW := output.TermWidth() - 4
	if termW > 80 {
		termW = 80
	}
	sepLine := lipgloss.NewStyle().Foreground(clrDim).Render(strings.Repeat("─", termW))

	// One layout for every terminal: runewidth-measured padding. The TTY path
	// used CHA cursor-column jumps — which broke the moment output was
	// captured, and meant TTY and non-TTY rows could disagree about columns.
	colHeaders := lipgloss.NewStyle().Bold(true).Foreground(clrDim).
		Render("  " + padCell("COMPONENT", summaryNameWidth) + padCell("NAMESPACE", summaryNsWidth) + "STATUS")

	for _, g := range []string{"A", "B", "C"} {
		if len(groups[g]) == 0 {
			continue
		}
		fmt.Println()
		fmt.Println(headerStyle.Render(fmt.Sprintf("  Group %s — %s", g, componentGroupNames[g])))
		fmt.Println(colHeaders)
		fmt.Println("  " + sepLine)
		for _, e := range groups[g] {
			// The status RECORDED during the deploy, not a fresh helm query.
			// Re-querying ran `helm status` per row (up to 5s each) and could
			// contradict what the deploy just reported — a component that
			// failed mid-apply can still hold a "deployed" helm release.
			printRow(e.Icon, e.Name, e.Namespace, e.Status)
		}
	}

	// ── Footer ──
	fmt.Println()

	var deployedCount, skippedCount, failedCount int
	for _, e := range entries {
		switch e.Status {
		case "failed":
			failedCount++
		case "skipped":
			skippedCount++
		default:
			deployedCount++
		}
	}

	if failedCount > 0 {
		fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrRed).
			Render(fmt.Sprintf("  ⚠️  %d deployed, %d failed", deployedCount, failedCount)))
		for _, e := range entries {
			if e.Status == "failed" && e.Error != "" {
				fmt.Printf("     %s %s: %s\n",
					lipgloss.NewStyle().Foreground(clrRed).Render("┖"),
					e.Name,
					lipgloss.NewStyle().Foreground(clrDim).Render(e.Error))
			}
		}
	} else {
		fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrGreen).
			Render(fmt.Sprintf("  ✅ %d components deployed successfully!", deployedCount)))
	}

	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Foreground(clrDim).Italic(true).Render("  ⏭  Next steps:"))
	cmdStyle := lipgloss.NewStyle().Foreground(clrCyan)
	for _, c := range []string{"kates status", "kates kafka connect test", "kates deploy -P"} {
		fmt.Println(cmdStyle.Render("    $ " + c))
	}
	fmt.Println()
}

const (
	summaryNameWidth = 28
	summaryNsWidth   = 18
)

// padCell pads a raw (unstyled) cell to a display width, measured with
// runewidth so emoji and CJK count their true two cells. Padding happens
// BEFORE styling: ANSI sequences are zero-width and must not enter the math.
func padCell(s string, w int) string {
	gap := w - visualWidth(s)
	if gap < 1 {
		gap = 1
	}
	return s + strings.Repeat(" ", gap)
}

// printRow renders one summary row from the status recorded during deploy
// ("deployed", "skipped", "failed").
func printRow(icon, name, namespace, status string) {
	g := output.Glyphs()
	nameCol := lipgloss.NewStyle().Bold(true).Foreground(clrText).Render(padCell(icon+" "+name, summaryNameWidth))
	nsCol := lipgloss.NewStyle().Foreground(clrDim).Render(padCell(namespace, summaryNsWidth))

	var statusStr string
	switch status {
	case "deployed":
		statusStr = lipgloss.NewStyle().Bold(true).Foreground(clrGreen).Render(g.Check + " Deployed")
	case "failed":
		statusStr = lipgloss.NewStyle().Bold(true).Foreground(clrRed).Render(g.Cross + " Failed")
	default:
		statusStr = lipgloss.NewStyle().Foreground(clrOrange).Render(g.Ring + " Skipped")
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

// deployPhase numbers the phase headers of one runDeploy invocation. A counter
// instead of literals: the literals drifted to [1], [1], [2], [3], [4] after a
// reorder, and nothing noticed until a user did.
var deployPhase int

// resetDeployPhases starts the numbering over; call at the top of runDeploy.
func resetDeployPhases() { deployPhase = 0 }

// nextDeployPhase returns the next phase number.
func nextDeployPhase() int { deployPhase++; return deployPhase }

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
