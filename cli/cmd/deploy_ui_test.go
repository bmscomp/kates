package cmd

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/theme"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// captureStdout runs f and returns everything it printed.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				b.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		done <- b.String()
	}()
	f()
	w.Close()
	os.Stdout = orig
	return <-done
}

// displayWidth is the width of a line as a terminal draws it: ANSI removed,
// then measured. It is plain runewidth on purpose — the whole point of the
// icon rule in deploy_ui.go is that runewidth and the terminal agree about
// every glyph we print, so a test that measured with a private width table
// would only be checking that table against itself. That is exactly the
// mistake the previous version of this file made, and it passed while the
// user's terminal showed ☸ glued to "Strimzi Operator".
func displayWidth(s string) int {
	return runewidth.StringWidth(stripStyle(s))
}

func uiTestEntries() []DeploySummaryEntry {
	deployTopology = "isolated"
	deployKafkaNS, deployAppNS, deployConnectNS, deployDbNS = "kafka", "kates", "connect", "database"
	deployMonitoringNS, deployKafkaUINS, deployChaosNS, deployMM2NS = "monitoring", "kafka", "litmus", "kafka"
	deployKafkaName = "krafter"
	deployWithStrimzi, deployWithKafkaUI, deployWithMirrorMaker2 = true, true, true
	deployWithMonitoring, deployWithKafkaConnect, deployWithChaos = true, true, true
	deployWithCertManager, deployWithKyverno = true, true
	deployWithSchemaRegistry = "apicurio"
	return buildSharedEntries(resolveNamespaces())
}

// TestComponentIconsAreUnambiguous is the guard that actually holds the
// columns straight, and it is a rule about which glyphs we may use rather
// than a measurement of the ones we did.
//
// History: ☸ (U+2638), 🛡 (U+1F6E1) and 🖥 (U+1F5A5) default to TEXT
// presentation. runewidth calls them one cell; terminals draw them as one or
// two depending on the font; adding U+FE0F is supposed to settle it and does
// not, consistently. The CLI compensated with a width table, the table was
// wrong for the user's terminal, and the icon ended up touching the component
// name on exactly those three rows.
//
// So: component icons must be Emoji_Presentation=Yes code points, which every
// terminal draws two cells wide and runewidth counts as two. No variation
// selectors, no compensation, no table.
func TestComponentIconsAreUnambiguous(t *testing.T) {
	icons := map[string]string{}
	for _, e := range uiTestEntries() {
		icons[e.Icon] = "deploy plan (" + e.Name + ")"
	}
	for icon := range asciiIcons {
		if _, seen := icons[icon]; !seen {
			icons[icon] = "asciiIcons"
		}
	}

	for icon, where := range icons {
		if w := runewidth.StringWidth(icon); w != 2 {
			t.Errorf("icon %q from %s measures %d cells, want 2 — pick an "+
				"emoji-presentation code point instead", icon, where, w)
		}
		for _, r := range icon {
			if r == '️' {
				t.Errorf("icon %q from %s carries U+FE0F; the selector is a "+
					"symptom of a text-presentation glyph, not a fix", icon, where)
			}
			if name, banned := textPresentationRunes[r]; banned {
				t.Errorf("icon %q from %s uses %s, which terminals may draw "+
					"one cell wide", icon, where, name)
			}
		}
	}
}

// Given icons that measure honestly, every row's columns must begin at the
// same offset — in both glyph modes.
func TestDeployRowsShareColumnOffsets(t *testing.T) {
	entries := uiTestEntries()

	for _, ascii := range []bool{false, true} {
		name := "utf8"
		if ascii {
			name = "ascii"
		}
		t.Run(name, func(t *testing.T) {
			if ascii {
				t.Setenv("KATES_ASCII", "1")
			}
			output.SetPlain(ascii)
			defer output.SetPlain(false)

			// The icon cell is the whole fix: every icon occupies the same width.
			for _, e := range entries {
				if got := displayWidth(iconCell(e.Icon)); got != iconCellWidth() {
					t.Errorf("iconCell(%q) is %d cells, want %d", e.Icon, got, iconCellWidth())
				}
			}

			// And the composed name cell, which is what actually places the
			// next column.
			widths := map[int][]string{}
			for _, e := range entries {
				cell := iconCell(e.Icon) + padCell(e.Name, summaryNameWidth-iconCellWidth())
				widths[displayWidth(cell)] = append(widths[displayWidth(cell)], e.Name)
			}
			if len(widths) != 1 {
				t.Errorf("name cells have %d different widths, want 1: %v", len(widths), widths)
			}
		})
	}
}

// The preview and the summary are deliberately different shapes now — a grid
// to scan before, a table with timings after. What they must still share is
// the vocabulary: the same waves, in the same order, naming the same
// components, so the row you approved is the row you can find afterwards.
func TestPreviewAndSummarySpeakTheSameLanguage(t *testing.T) {
	entries := uiTestEntries()
	preview := stripStyle(captureStdout(t, func() {
		renderDeployPreview(entries, map[string]bool{})
	}))
	for i := range entries {
		entries[i].Status = "deployed"
		entries[i].Duration = 30 * time.Second
	}
	summary := stripStyle(captureStdout(t, func() {
		RenderDeployDashboard(t.Context(), entries, time.Minute)
	}))

	for _, group := range []string{"A", "B", "C"} {
		header := waveMark(group) + " " + componentGroupNames[group]
		if !strings.Contains(preview, header) {
			t.Errorf("the plan does not use the wave header %q", header)
		}
		if !strings.Contains(summary, header) {
			t.Errorf("the summary does not use the wave header %q", header)
		}
	}
	for _, e := range entries {
		if !strings.Contains(preview, e.Name) {
			t.Errorf("the plan does not name %q", e.Name)
		}
		if !strings.Contains(summary, e.Name) {
			t.Errorf("the summary does not name %q", e.Name)
		}
	}
}

func TestHumanDuration(t *testing.T) {
	cases := map[time.Duration]string{
		0:                              "-",
		250 * time.Millisecond:         "250ms",
		42 * time.Second:               "42s",
		90 * time.Second:               "1m30s",
		4*time.Minute + 5*time.Second:  "4m05s",
		12*time.Minute + 0*time.Second: "12m00s",
	}
	for d, want := range cases {
		if got := humanDuration(d); got != want {
			t.Errorf("humanDuration(%s) = %q, want %q", d, got, want)
		}
	}
}

func TestDurationBarScalesToTheSlowest(t *testing.T) {
	const w = 8
	if got := displayWidth(durationBar(time.Minute, time.Minute, w)); got != w {
		t.Errorf("the slowest bar is %d cells, want %d", got, w)
	}
	// Every bar occupies the full column so the rows stay aligned; only the
	// filled part varies.
	if got := displayWidth(durationBar(time.Second, time.Minute, w)); got != w {
		t.Errorf("a short bar is %d cells, want %d (padded)", got, w)
	}
	if strings.Count(stripStyle(durationBar(time.Second, time.Minute, w)), output.Glyphs().BarFull) != 1 {
		t.Error("a component far below the maximum should still show one block, not none")
	}
	if got := stripStyle(durationBar(0, time.Minute, w)); strings.TrimSpace(got) != "" {
		t.Errorf("a component with no recorded time should draw nothing, got %q", got)
	}
}

// The old footer printed the same three commands whatever was installed,
// including a port-forward for services that were not.
func TestNextStepsFollowWhatWasDeployed(t *testing.T) {
	deployKafkaName = "krafter"
	steps := strings.Join(nextSteps([]DeploySummaryEntry{
		{Release: "krafter", Status: "deployed"},
		{Release: "mm2", Status: "deployed"},
	}), "\n")
	for _, want := range []string{"kates status", "kates cluster topics", "kates migrate status"} {
		if !strings.Contains(steps, want) {
			t.Errorf("missing %q in:\n%s", want, steps)
		}
	}
	if strings.Contains(steps, "connect test") {
		t.Errorf("suggested a Connect command with no Connect deployed:\n%s", steps)
	}

	// A failed component is not something to point the user at.
	steps = strings.Join(nextSteps([]DeploySummaryEntry{{Release: "mm2", Status: "failed"}}), "\n")
	if strings.Contains(steps, "migrate status") {
		t.Errorf("suggested a command for a component that failed:\n%s", steps)
	}
}

func TestFailureHintNamesTheInvestigation(t *testing.T) {
	deployKafkaName = "krafter"
	cases := map[string]string{
		"strimzi-operator": "logs deploy/strimzi-cluster-operator",
		"krafter":          "describe kafka krafter",
		"mm2":              "kates doctor",
		"apicurio":         "get pods -l app.kubernetes.io/instance=apicurio",
	}
	for release, want := range cases {
		got := failureHint(DeploySummaryEntry{Release: release, Namespace: "kafka"})
		if !strings.Contains(got, want) {
			t.Errorf("failureHint(%s) = %q, want it to contain %q", release, got, want)
		}
	}
}

func TestSummariseCountsAndFindsTheSlowest(t *testing.T) {
	s := summarise([]DeploySummaryEntry{
		{Name: "a", Status: "deployed", Duration: 10 * time.Second},
		{Name: "b", Status: "skipped", Duration: time.Second},
		{Name: "c", Status: "failed", Duration: 40 * time.Second},
		{Name: "d", Status: "deployed", Duration: 5 * time.Second},
	})
	if s.Deployed != 2 || s.Skipped != 1 || s.Failed != 1 {
		t.Errorf("counts: %+v", s)
	}
	if s.Slowest.Name != "c" || s.MaxDuration != 40*time.Second {
		t.Errorf("slowest: %s (%s)", s.Slowest.Name, s.MaxDuration)
	}
	if s.Total != 56*time.Second {
		t.Errorf("total: %s", s.Total)
	}
}

// The preview's job is to be checkable: a typo in --kafka-ns should be
// visible as a namespace nobody meant to create.
func TestPreviewNamesTheNamespaces(t *testing.T) {
	entries := uiTestEntries()
	out := stripStyle(captureStdout(t, func() {
		renderDeployPreview(entries, map[string]bool{})
	}))
	for _, ns := range []string{"kafka", "kates", "connect", "database", "litmus"} {
		if !strings.Contains(out, ns) {
			t.Errorf("preview does not name the %q namespace:\n%s", ns, out)
		}
	}
	if !strings.Contains(out, "components to deploy") {
		t.Errorf("preview has no total:\n%s", out)
	}
	// Grammar: the old footer said "1 components".
	single := stripStyle(captureStdout(t, func() {
		renderDeployPreview(entries[:1], map[string]bool{})
	}))
	if strings.Contains(single, "1 components") {
		t.Errorf("plural bug is back:\n%s", single)
	}
}

// The grid replaced a comma-joined sentence, and a grid only reads as one if
// its columns line up. Rows are chips concatenated with a fixed gutter, so
// the whole property reduces to this: every chip is exactly one width, in
// both glyph modes, whatever the icon.
func TestComponentChipsAreOneWidth(t *testing.T) {
	entries := uiTestEntries()

	for _, ascii := range []bool{false, true} {
		name := "utf8"
		if ascii {
			name = "ascii"
		}
		t.Run(name, func(t *testing.T) {
			if ascii {
				t.Setenv("KATES_ASCII", "1")
			}
			output.SetPlain(ascii)
			defer output.SetPlain(false)

			nameW, nsW := chipDimensions(entries)
			want := chipWidth(nameW, nsW)
			for _, e := range entries {
				chip := componentChip(e, nameW, nsW, e.Group, gridOptions{})
				if got := displayWidth(stripStyle(chip)); got != want {
					t.Errorf("chip for %q (icon %q) is %d cells, want %d: %q",
						e.Name, e.Icon, got, want, stripStyle(chip))
				}
			}
		})
	}
}

// A grid that lays out one column has not done its job: the sentence it
// replaced was already one column.
func TestComponentGridUsesTheWidth(t *testing.T) {
	entries := uiTestEntries()
	nameW, nsW := chipDimensions(entries)
	grid := componentGrid(entries, gridOptions{Width: 84})

	widest := 0
	for _, line := range strings.Split(grid, "\n") {
		plain := stripStyle(line)
		if !strings.HasPrefix(plain, "  ") {
			continue
		}
		if w := displayWidth(plain); w > widest {
			widest = w
		}
	}
	if widest <= chipWidth(nameW, nsW)+2 {
		t.Errorf("the widest row is %d cells and a chip is %d: the grid is single-column at width 84",
			widest, chipWidth(nameW, nsW))
	}
	if widest > 84 {
		t.Errorf("the grid is %d cells wide, past the %d it was given", widest, 84)
	}
}

// Colour is the grouping cue, so the waves must actually differ — and never
// borrow the outcome colours, which mean something else in this CLI.
func TestWaveColoursAreDistinctAndNotOutcomes(t *testing.T) {
	a, b, c := waveColor("A"), waveColor("B"), waveColor("C")
	if a == b || b == c || a == c {
		t.Errorf("waves share a colour: %v %v %v", a, b, c)
	}
	for _, w := range []lipgloss.TerminalColor{a, b, c} {
		if w == theme.Error {
			t.Error("a wave is using the error colour; red means failure here")
		}
		if w == theme.Highlight {
			t.Error("a wave is using the selection colour")
		}
	}
}

// The preview's grid must distinguish what is new from what is already there
// — that is the question it exists to answer.
func TestPreviewGridMarksWhatIsNew(t *testing.T) {
	entries := uiTestEntries()
	existing := map[string]bool{"monitoring/monitoring": true}
	grid := stripStyle(componentGrid(entries, gridOptions{
		Width: 84, Planned: true, Existing: existing,
	}))
	g := output.Glyphs()
	for _, line := range strings.Split(grid, "\n") {
		if strings.Contains(line, "Monitoring ") && !strings.Contains(line, g.Ring) {
			t.Errorf("an already-present component is not marked as such: %q", line)
		}
		if strings.Contains(line, "Kates Backend") && !strings.Contains(line, g.Check) {
			t.Errorf("a new component is not marked as such: %q", line)
		}
	}
	if !strings.Contains(grid, "already present") {
		t.Error("the planned grid has no legend")
	}
}
