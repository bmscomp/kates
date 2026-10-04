package cmd

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/theme"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// Shared presentation for everything `kates deploy` prints: the icon column,
// the rules, durations, and the closing suggestions. It exists because the
// preview, the live dashboard and the final summary each used to lay out the
// same rows their own way, and the columns disagreed.

// ─── Icons ──────────────────────────────────────────────────

// Every component icon must be two display cells wide in every terminal, so
// that the padding below is arithmetic rather than a guess.
//
// The rule that keeps that true: only use code points whose Unicode
// Emoji_Presentation property is Yes. Those are drawn as colour emoji with no
// variation selector, and go-runewidth reports 2 for them — the terminal and
// the CLI agree without either being told anything.
//
// Glyphs that default to TEXT presentation are banned: ☸ (U+2638), 🛡
// (U+1F6E1), 🖥 (U+1F5A5) and the rest of the pictographs Unicode kept narrow
// for legacy reasons. Appending U+FE0F is supposed to widen them, and this
// file used to do exactly that — but terminals disagree with each other and
// with runewidth about the result, so the name column landed one cell left on
// exactly those rows and the icon touched the name. There is no width table
// that fixes it; not needing one does. TestComponentIconsAreUnambiguous
// enforces the rule against textPresentationRunes below.
//
// asciiIcons replace the pictographs when the terminal has asked for ASCII.
var asciiIcons = map[string]string{
	"🦊": "op", "🔐": "tls", "🚦": "pol", "📨": "kfk", "🐘": "pg",
	"🔗": "cnx", "📊": "mon", "📋": "reg", "📦": "app", "💻": "ui",
	"🔁": "mm2", "🧪": "chs",
}

// textPresentationRunes are the code points a terminal may legitimately draw
// one cell wide. Component icons may not be drawn from this set; it exists so
// the test can say why rather than just fail.
var textPresentationRunes = map[rune]string{
	'☸': "U+2638 wheel of dharma",
	'⎈': "U+2388 helm symbol",
	'⚙': "U+2699 gear",
	'⚠': "U+26A0 warning",
	'✔': "U+2714 heavy check",
	'🛡': "U+1F6E1 shield",
	'🖥': "U+1F5A5 desktop computer",
	'🗂': "U+1F5C2 card index dividers",
	'🏷': "U+1F3F7 label",
	'🕹': "U+1F579 joystick",
	'🛠': "U+1F6E0 hammer and wrench",
	'🗄': "U+1F5C4 file cabinet",
}

// deployIcon returns the icon to print for a component, and the number of
// display cells it will occupy. Callers pad to iconCellWidth using the
// returned width rather than measuring the string themselves.
func deployIcon(icon string) (string, int) {
	if output.ASCII() {
		if a, ok := asciiIcons[icon]; ok {
			return a, runewidth.StringWidth(a)
		}
		return "*", 1
	}
	return icon, runewidth.StringWidth(icon)
}

// iconCellWidth is the width of the icon column in display cells: two cells
// for an emoji plus a space, or three for an ASCII tag plus a space. It is a
// function because the ASCII tags are three characters and would otherwise
// touch the component name ("tlsCert-Manager").
func iconCellWidth() int {
	if output.ASCII() {
		return 4
	}
	return 3
}

// iconCell renders an icon padded to exactly iconCellWidth cells, so every
// component name in every view starts at the same column.
// The pad is never zero: if a terminal ever draws an icon wider than we
// measured, the row shifts right by a cell, which is survivable — an icon
// glued to the component name is not.
func iconCell(icon string) string {
	glyph, w := deployIcon(icon)
	pad := iconCellWidth() - w
	if pad < 1 {
		pad = 1
	}
	return glyph + strings.Repeat(" ", pad)
}

// ─── Layout ─────────────────────────────────────────────────

// uiWidth is the width the deploy views lay out to: the terminal, minus a
// margin, capped so a very wide window does not produce an unreadable
// full-screen rule.
func uiWidth() int {
	w := output.TermWidth() - 4
	if w > 84 {
		w = 84
	}
	if w < 40 {
		w = 40
	}
	return w
}

// uiRule is a horizontal rule of the current layout width.
func uiRule() string {
	return lipgloss.NewStyle().Foreground(clrDim).
		Render(strings.Repeat(output.Glyphs().Rule, uiWidth()))
}

// ─── Component grid ─────────────────────────────────────────

// Waves are what the groups actually are: A is installed first and everything
// waits on it, B is the data plane, C is what talks to it. Naming them "Group
// A" told the reader a letter; naming them a wave tells them the order.
var waveTitles = map[string]string{
	"A": "installed first — everything below waits on these",
	"B": "the data plane",
	"C": "what talks to it",
}

// waveColor gives each wave its own hue so a component can be placed at a
// glance. These are non-semantic here on purpose: green and red are reserved
// for outcome, and a grid that used them for grouping would read as a table
// of passes and failures.
func waveColor(group string) lipgloss.TerminalColor {
	switch group {
	case "A":
		return theme.Info // teal
	case "B":
		return theme.Accent // blue
	default:
		return theme.Secondary // violet
	}
}

// waveMark numbers a wave. The circled digits carry the order without a word.
func waveMark(group string) string {
	if output.ASCII() {
		return map[string]string{"A": "1.", "B": "2.", "C": "3."}[group]
	}
	return map[string]string{"A": "①", "B": "②", "C": "③"}[group]
}

// gridOptions tunes the component grid for the screen it appears on.
type gridOptions struct {
	// Width is the space the grid may use.
	Width int
	// Planned renders the pre-deploy wording ("will deploy" / "already
	// present") instead of the post-deploy one.
	Planned bool
	// Existing marks releases that are already installed, for Planned grids.
	Existing map[string]bool
	// Indent is prefixed to every line, for embedding in a form.
	Indent string
}

// componentGrid lays the components out as coloured chips, wave by wave, in
// as many columns as the width allows.
//
// It replaces a comma-joined sentence that wrapped into an unreadable run-on
// at eleven components — the shape of a deployment is a thing you scan, not a
// thing you read. Grouping by wave and colouring by wave means "what is being
// installed, in what order, into which namespace" is one glance.
func componentGrid(entries []DeploySummaryEntry, opt gridOptions) string {
	if len(entries) == 0 {
		return ""
	}
	width := opt.Width
	if width <= 0 {
		width = uiWidth()
	}

	nameW, nsW := chipDimensions(entries)
	chipW := chipWidth(nameW, nsW)
	cols := (width - len(opt.Indent)) / (chipW + 2)
	if cols < 1 {
		cols = 1
	}
	if cols > 3 {
		cols = 3
	}

	var b strings.Builder
	g := output.Glyphs()
	for wi, group := range []string{"A", "B", "C"} {
		var wave []DeploySummaryEntry
		for _, e := range entries {
			if e.Group == group {
				wave = append(wave, e)
			}
		}
		if len(wave) == 0 {
			continue
		}
		if wi > 0 {
			b.WriteString("\n")
		}
		// Wave header: mark, name, and what the wave is for.
		b.WriteString(opt.Indent)
		b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(waveColor(group)).
			Render(fmt.Sprintf("%s %s", waveMark(group), componentGroupNames[group])))
		b.WriteString(lipgloss.NewStyle().Foreground(clrDim).
			Render("  " + waveTitles[group]))
		b.WriteString("\n")

		for i, e := range wave {
			if i%cols == 0 {
				b.WriteString(opt.Indent + "  ")
			}
			last := i%cols == cols-1 || i == len(wave)-1
			chip := componentChip(e, nameW, nsW, group, opt)
			if last {
				// No trailing padding at the end of a line: it is invisible
				// until someone selects the text or diffs the output.
				b.WriteString(strings.TrimRight(chip, " ") + "\n")
			} else {
				b.WriteString(chip + "  ")
			}
		}
	}

	if opt.Planned {
		b.WriteString("\n" + opt.Indent)
		b.WriteString(lipgloss.NewStyle().Foreground(clrDim).Render(
			fmt.Sprintf("%s new    %s already present", g.Check, g.Ring)))
		b.WriteString("\n")
	}
	return b.String()
}

// chipDimensions sizes the name and namespace columns to the widest entry, so
// nothing truncates, with a cap that keeps two columns fitting a normal
// terminal. The grid and its tests derive the layout from this one place.
func chipDimensions(entries []DeploySummaryEntry) (nameW, nsW int) {
	for _, e := range entries {
		if w := runewidth.StringWidth(e.Name); w > nameW {
			nameW = w
		}
		if w := runewidth.StringWidth(e.Namespace); w > nsW {
			nsW = w
		}
	}
	if nameW > 22 {
		nameW = 22
	}
	return nameW, nsW
}

// chipWidth is the display width every chip occupies: mark, space, icon cell,
// name, space, namespace.
func chipWidth(nameW, nsW int) int {
	return 1 + 1 + iconCellWidth() + nameW + 1 + nsW
}

// componentChip renders one component as a fixed-width cell: a status mark,
// the icon, the name in the wave's colour, and the namespace dimmed.
func componentChip(e DeploySummaryEntry, nameW, nsW int, group string, opt gridOptions) string {
	g := output.Glyphs()

	name := e.Name
	if runewidth.StringWidth(name) > nameW {
		name = runewidth.Truncate(name, nameW, "…")
	}

	// The status mark is the leftmost thing in the chip, because "is this new"
	// is the question the preview exists to answer.
	mark := " "
	nameStyle := lipgloss.NewStyle().Bold(true).Foreground(waveColor(group))
	nsStyle := lipgloss.NewStyle().Foreground(clrDim)

	switch {
	case opt.Planned && opt.Existing[e.Release+"/"+e.Namespace]:
		mark = lipgloss.NewStyle().Foreground(clrDim).Render(g.Ring)
		// An already-present component is context, not the news: dim the whole
		// chip so the eye lands on what is about to change.
		nameStyle = lipgloss.NewStyle().Foreground(clrDim)
	case opt.Planned:
		mark = lipgloss.NewStyle().Foreground(clrGreen).Render(g.Check)
	default:
		switch classifyStatus(e.Status) {
		case "failed":
			mark = lipgloss.NewStyle().Foreground(clrRed).Render(g.Cross)
			nameStyle = lipgloss.NewStyle().Bold(true).Foreground(clrRed)
		case "skipped":
			mark = lipgloss.NewStyle().Foreground(clrDim).Render(g.Ring)
			nameStyle = lipgloss.NewStyle().Foreground(clrDim)
		default:
			mark = lipgloss.NewStyle().Foreground(clrGreen).Render(g.Check)
		}
	}

	// padExact, not padCell: padCell keeps a minimum one-space gap so table
	// columns never touch, which in a grid makes a cell that happens to be
	// exactly the column width one cell wider than its neighbours — and every
	// chip to its right shifts.
	return fmt.Sprintf("%s %s%s %s",
		mark,
		iconCell(e.Icon),
		nameStyle.Render(padExact(name, nameW)),
		nsStyle.Render(padExact(e.Namespace, nsW)))
}

// padExact pads (or truncates) to exactly w display cells.
func padExact(s string, w int) string {
	width := runewidth.StringWidth(s)
	if width > w {
		return runewidth.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", w-width)
}

// ─── Durations ──────────────────────────────────────────────

// humanDuration formats a component's elapsed time for a table cell: short,
// fixed-ish width, and never "4m12.381s".
func humanDuration(d time.Duration) string {
	switch {
	case d <= 0:
		return "-"
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	default:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
}

// durationBar draws d relative to the slowest component, so the row that cost
// the wall-clock time is visible without reading the numbers. The scale is
// the deploy's own maximum: this answers "what took the time", which is the
// question after a slow deploy, rather than "is this fast", which needs a
// baseline the CLI does not have.
func durationBar(d, max time.Duration, width int) string {
	if d <= 0 || max <= 0 || width <= 0 {
		return strings.Repeat(" ", width)
	}
	filled := int(float64(width) * (float64(d) / float64(max)))
	if filled < 1 {
		filled = 1
	}
	if filled > width {
		filled = width
	}
	g := output.Glyphs()
	bar := strings.Repeat(g.BarFull, filled) + strings.Repeat(" ", width-filled)
	return lipgloss.NewStyle().Foreground(clrDim).Render(bar)
}

// ─── Next steps ─────────────────────────────────────────────

// nextSteps suggests what to run now, from what was actually deployed. The
// old footer printed the same three commands whatever the deployment
// contained — including a port-forward for services that were not installed.
func nextSteps(entries []DeploySummaryEntry) []string {
	have := map[string]bool{}
	for _, e := range entries {
		if classifyStatus(e.Status) != "failed" {
			have[e.Release] = true
		}
	}

	steps := []string{"kates status"}
	if have["kates"] {
		steps = append(steps, "kates health")
	}
	if have[deployKafkaName] {
		steps = append(steps, "kates cluster topics")
	}
	if have["mm2"] {
		steps = append(steps, "kates migrate status")
	}
	if have["connect-cluster"] {
		steps = append(steps, "kates kafka connect test")
	}
	if have["kafka-ui"] || have["monitoring"] || have["kates"] {
		steps = append(steps, "kates ports        # forward every UI to localhost")
	}
	return steps
}

// ─── Failure hints ──────────────────────────────────────────

// failureHint turns a component's failure into the command that investigates
// it. A summary that says "failed" and stops has told the user the one thing
// they already knew.
func failureHint(e DeploySummaryEntry) string {
	switch e.Release {
	case "strimzi-operator":
		return "kubectl -n strimzi-operator logs deploy/strimzi-cluster-operator --tail=50"
	case deployKafkaName:
		return fmt.Sprintf("kubectl -n %s describe kafka %s", e.Namespace, deployKafkaName)
	case "connect-cluster", "mm2":
		return fmt.Sprintf("kates doctor --kafka-ns %s", e.Namespace)
	default:
		return fmt.Sprintf("kubectl -n %s get pods -l app.kubernetes.io/instance=%s", e.Namespace, e.Release)
	}
}

// ─── Summary statistics ─────────────────────────────────────

// deployStats is what the footer needs, counted once.
type deployStats struct {
	Deployed, Skipped, Failed int
	Total                     time.Duration
	Slowest                   DeploySummaryEntry
	MaxDuration               time.Duration
}

func summarise(entries []DeploySummaryEntry) deployStats {
	var s deployStats
	for _, e := range entries {
		switch classifyStatus(e.Status) {
		case "failed":
			s.Failed++
		case "skipped":
			s.Skipped++
		default:
			s.Deployed++
		}
		s.Total += e.Duration
		if e.Duration > s.MaxDuration {
			s.MaxDuration = e.Duration
			s.Slowest = e
		}
	}
	return s
}

// namespaceList returns the distinct namespaces a plan touches, in order, so
// the preview can say what it will create rather than only how many.
func namespaceList(entries []DeploySummaryEntry) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		if e.Namespace != "" && !seen[e.Namespace] {
			seen[e.Namespace] = true
			out = append(out, e.Namespace)
		}
	}
	sort.Strings(out)
	return out
}

// (plural lives in cluster_gate.go — the difference between "1 components"
// and "1 component" is wanted in both places and belongs in one.)
