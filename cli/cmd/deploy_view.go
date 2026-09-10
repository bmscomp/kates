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
	g := output.Glyphs()
	stats := summarise(entries)
	fmt.Println()

	// ── Header ──
	title := " Kates deployment summary "
	if !output.ASCII() {
		title = " ⎈ Kates deployment summary "
	}
	banner := lipgloss.NewStyle().
		Bold(true).
		Foreground(theme.OnDark).
		Background(clrAccent).
		Padding(0, 1).
		Render(title)
	timer := lipgloss.NewStyle().Foreground(clrDim).Italic(true).
		Render(fmt.Sprintf("  %s wall clock", elapsed.Round(time.Second)))
	fmt.Println(banner + timer)

	// ── Grouped entries ──
	groups := map[string][]DeploySummaryEntry{"A": {}, "B": {}, "C": {}}
	for _, e := range entries {
		groups[e.Group] = append(groups[e.Group], e)
	}

	sepLine := uiRule()

	// One layout for every terminal: runewidth-measured padding, with the icon
	// column normalised by iconCell. The TTY path used CHA cursor-column jumps
	// — which broke the moment output was captured, and meant TTY and non-TTY
	// rows could disagree about columns.
	//
	// The TIME column is new and is the point of the table: the wall clock
	// tells you the deploy was slow, and only per-component times tell you
	// which part of it was.
	colHeaders := lipgloss.NewStyle().Bold(true).Foreground(clrDim).
		Render("  " + padCell("COMPONENT", summaryNameWidth) + padCell("NAMESPACE", summaryNsWidth) +
			padCell("STATUS", summaryStatusWidth) + "TIME")

	for _, gr := range []string{"A", "B", "C"} {
		if len(groups[gr]) == 0 {
			continue
		}
		fmt.Println()
		// The same wave header as the plan's grid: mark, name, purpose, in the
		// wave's colour. Three screens speaking one vocabulary is the point —
		// the row you saw in the plan is the row you find here.
		fmt.Println(
			lipgloss.NewStyle().Bold(true).Foreground(waveColor(gr)).
				Render(fmt.Sprintf("  %s %s", waveMark(gr), componentGroupNames[gr])) +
				lipgloss.NewStyle().Foreground(clrDim).Render("  "+waveTitles[gr]))
		fmt.Println(colHeaders)
		fmt.Println("  " + sepLine)
		for _, e := range groups[gr] {
			// The status RECORDED during the deploy, not a fresh helm query.
			// Re-querying ran `helm status` per row (up to 5s each) and could
			// contradict what the deploy just reported — a component that
			// failed mid-apply can still hold a "deployed" helm release.
			printRow(e, stats.MaxDuration)
		}
	}

	// ── Footer ──
	fmt.Println()

	switch {
	case stats.Failed > 0:
		fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrRed).
			Render(fmt.Sprintf("  %s %s deployed, %s failed",
				g.Warn, plural(stats.Deployed, "component", "components"),
				plural(stats.Failed, "component", "components"))))
		for _, e := range entries {
			if classifyStatus(e.Status) != "failed" {
				continue
			}
			fmt.Printf("     %s %s%s\n",
				lipgloss.NewStyle().Foreground(clrRed).Render(g.Cross),
				lipgloss.NewStyle().Bold(true).Render(e.Name),
				lipgloss.NewStyle().Foreground(clrDim).Render(errSuffix(e.Error)))
			// A failure the summary cannot explain still deserves the command
			// that would explain it.
			fmt.Printf("       %s\n",
				lipgloss.NewStyle().Foreground(clrCyan).Render("$ "+failureHint(e)))
		}
	case stats.Deployed == 0 && stats.Skipped > 0:
		// The state the old footer lied about: nothing was deployed because
		// everything already was. "8 deployed successfully!" over a column of
		// skips contradicted the table two lines above it.
		fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrGreen).
			Render(fmt.Sprintf("  %s Everything was already deployed — %s unchanged",
				g.Check, plural(stats.Skipped, "component", "components"))))
	case stats.Skipped > 0:
		fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrGreen).
			Render(fmt.Sprintf("  %s %s deployed, %d already present",
				g.Check, plural(stats.Deployed, "component", "components"), stats.Skipped)))
	default:
		fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrGreen).
			Render(fmt.Sprintf("  %s %s deployed",
				g.Check, plural(stats.Deployed, "component", "components"))))
	}

	// Where the time went, when there is a meaningful answer. Below a few
	// seconds nothing was slow and the line would be noise.
	if stats.MaxDuration >= 5*time.Second && stats.Slowest.Name != "" {
		fmt.Println(lipgloss.NewStyle().Foreground(clrDim).
			Render(fmt.Sprintf("    slowest: %s (%s of %s)",
				stats.Slowest.Name, humanDuration(stats.MaxDuration), elapsed.Round(time.Second))))
	}

	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Foreground(clrDim).Italic(true).Render("  Next:"))
	cmdStyle := lipgloss.NewStyle().Foreground(clrCyan)
	for _, c := range nextSteps(entries) {
		fmt.Println(cmdStyle.Render("    $ " + c))
	}
	fmt.Println()
}

// errSuffix renders an error tail only when there is one, so a failure with no
// captured message does not print a bare colon.
func errSuffix(err string) string {
	if strings.TrimSpace(err) == "" {
		return ""
	}
	return ": " + firstLine(err)
}

const (
	summaryNameWidth   = 30
	summaryNsWidth     = 18
	summaryStatusWidth = 18
	summaryBarWidth    = 8
)

// markEntryStatuses records each component's outcome from the same predicate
// the skip logic uses: already present → "skipped", absent → about to be
// "deployed". Strimzi is the exception — it is reconciled unconditionally
// (its pre-upgrade hook keeps the CRDs current), so work happens every run.
func markEntryStatuses(entries []DeploySummaryEntry, present func(release, namespace string) bool) {
	for i := range entries {
		if entries[i].Release == "strimzi-operator" {
			entries[i].Status = "deployed"
			continue
		}
		if present(entries[i].Release, entries[i].Namespace) {
			entries[i].Status = "skipped"
		} else {
			entries[i].Status = "deployed"
		}
	}
}

// classifyStatus is the ONE interpretation of a recorded component status.
// The table renderer and the footer counter both use it — they previously
// each had their own switch, and the empty string meant "Skipped" to one and
// "deployed" to the other.
func classifyStatus(s string) string {
	switch s {
	case "deployed", "skipped", "failed":
		return s
	default:
		return "unknown"
	}
}

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

// printRow renders one summary row from the status recorded during deploy,
// plus how long that component took and a bar of it relative to the slowest.
func printRow(e DeploySummaryEntry, max time.Duration) {
	g := output.Glyphs()
	// The icon and the name are padded separately: iconCell owns the two-cell
	// glyph plus its gap, padCell owns the text. Composing them first and
	// measuring the result once works only if runewidth agrees with the
	// terminal about the emoji, which is a bet deploy_ui.go declines to make.
	cell := iconCell(e.Icon) + padCell(e.Name, summaryNameWidth-iconCellWidth())
	nameCol := lipgloss.NewStyle().Bold(true).Foreground(clrText).Render(cell)
	nsCol := lipgloss.NewStyle().Foreground(clrDim).Render(padCell(e.Namespace, summaryNsWidth))

	var status string
	switch classifyStatus(e.Status) {
	case "deployed":
		status = lipgloss.NewStyle().Bold(true).Foreground(clrGreen).Render(g.Check + " deployed")
	case "failed":
		status = lipgloss.NewStyle().Bold(true).Foreground(clrRed).Render(g.Cross + " failed")
	case "skipped":
		// Dim, not warning-orange: a component that was already running is a
		// fine state, not a caution.
		status = lipgloss.NewStyle().Foreground(clrDim).Render(g.Ring + " already present")
	default:
		// A status nothing recorded is a bug worth seeing, not disguising as
		// either success or a skip.
		status = lipgloss.NewStyle().Foreground(clrOrange).Render(g.Warn + " unknown")
	}
	// Pad the RAW status text, then style: ANSI is zero-width and must not
	// enter the column arithmetic.
	statusCol := padCell(stripStyle(status), summaryStatusWidth)
	statusCol = strings.Replace(statusCol, stripStyle(status), status, 1)

	timeCol := lipgloss.NewStyle().Foreground(clrDim).Render(padCell(humanDuration(e.Duration), 7))
	fmt.Printf("  %s%s%s%s%s\n", nameCol, nsCol, statusCol, timeCol, durationBar(e.Duration, max, summaryBarWidth))
}

// stripStyle removes ANSI sequences so a styled cell can be measured and
// padded by its visible text.
func stripStyle(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEscape = true
		case inEscape && (r == 'm' || r == 'K'):
			inEscape = false
		case !inEscape:
			b.WriteRune(r)
		}
	}
	return b.String()
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

// deployPhaseCount is how many phases runDeploy prints. It is a constant
// because the sequence is: cluster, pre-flight, versions, topology,
// components, pipeline — the same six every run, whatever is selected. Saying
// "3 of 6" instead of "3" is the difference between a list and progress.
const deployPhaseCount = 6

// PrintPhaseHeader prints a phase header with its place in the run, and a
// rule that runs out to the layout width so every phase begins at the same
// visual weight regardless of how long its title is.
func PrintPhaseHeader(number int, title string) {
	fmt.Println()
	counter := fmt.Sprintf("%d/%d", number, deployPhaseCount)
	// counter + two spaces + title + one space, then rule to the edge.
	used := visualWidth(counter) + 2 + visualWidth(title) + 1
	fill := uiWidth() - used
	if fill < 3 {
		fill = 3
	}
	fmt.Printf("%s  %s %s\n",
		lipgloss.NewStyle().Foreground(clrDim).Render(counter),
		lipgloss.NewStyle().Bold(true).Foreground(clrPink).Render(title),
		lipgloss.NewStyle().Foreground(clrDim).Render(strings.Repeat(output.Glyphs().Rule, fill)))
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

// PrintDeployBanner prints the initial deploy banner: what is about to run,
// which build of the CLI is running it, and where. The version and context
// are there because "it did something different this time" is nearly always
// one of those two having changed.
func PrintDeployBanner() {
	g := output.Glyphs()
	fmt.Println()
	mark := "kates deploy"
	if !output.ASCII() {
		mark = "⎈ kates deploy"
	}
	line := lipgloss.NewStyle().Bold(true).Foreground(clrAccent).Render(mark)
	if sub := bannerSubtitle(); sub != "" {
		line += lipgloss.NewStyle().Foreground(clrDim).Render("   " + sub)
	}
	fmt.Println(line)
	fmt.Println(lipgloss.NewStyle().Foreground(clrDim).
		Render(strings.Repeat(g.HeavyRule, uiWidth())))
}

// bannerSubtitle is the one-line context: CLI version and target cluster,
// each omitted when unknown rather than printed as "unknown".
func bannerSubtitle() string {
	parts := []string{}
	if Version != "" && Version != "dev" {
		parts = append(parts, Version)
	}
	if ctxName := currentContextName(); ctxName != "" {
		parts = append(parts, ctxName)
	}
	return strings.Join(parts, "  ·  ")
}

// currentContextName returns the kubeconfig context this deploy will target,
// best-effort: a banner is not worth an error path.
func currentContextName() string {
	out, err := runExecOutputFn(context.Background(), "kubectl", "config", "current-context")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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
