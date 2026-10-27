package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// trueColor renders in 24-bit color for the rest of the test.
func trueColor(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

// useLook draws with name for the rest of the test.
func useLook(t *testing.T, name string) {
	t.Helper()
	prev := CurrentLook().Name
	if err := UseLook(name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = UseLook(prev) })
}

func TestNextLookStepsThroughEveryLookBackToClassic(t *testing.T) {
	useLook(t, Classic)
	names := LookNames()
	if names[0] != Classic || len(names) != len(Looks) {
		t.Fatalf("LookNames() = %v, want classic first and every look", names)
	}
	for _, want := range append(names[1:], Classic) {
		if got := NextLook(); got != want || CurrentLook().Name != want {
			t.Fatalf("NextLook() = %s (current %s), want %s", got, CurrentLook().Name, want)
		}
	}
	if err := UseLook("Nope"); err == nil || CurrentLook().Name != Classic {
		t.Errorf("UseLook(Nope) = %v, now %s; want an error and no change", err, CurrentLook().Name)
	}
}

func TestRequestedLookPrefersTheEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KATES_LOOK", "")
	if err := os.WriteFile(filepath.Join(home, ".kates.yaml"), []byte("theme: aurora\nlook: Grid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := RequestedLook(); got != Grid {
		t.Errorf("from the config: %q, want grid", got)
	}
	if got := Requested(); got != "aurora" {
		t.Errorf("the theme next to it: %q, want aurora", got)
	}
	t.Setenv("KATES_LOOK", " chips ")
	if got := RequestedLook(); got != Chips {
		t.Errorf("KATES_LOOK=chips: %q", got)
	}
	t.Setenv("KATES_LOOK", "neon")
	if got := SelectedLook(); got != Classic {
		t.Errorf("an unknown look starts classic, got %q", got)
	}
}

// Screens lay panes out by their sizes, so a look may redraw a frame but
// never resize it.
func TestBoxKeepsTheFrameSizeInEveryLook(t *testing.T) {
	trueColor(t)
	s := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Width(24).Padding(0, 1)
	content := lipgloss.NewStyle().Foreground(Accent).Render("styled") + " plain\nsecond line"
	useLook(t, Classic)
	classic := Box(s, true, content)
	if classic != s.Render(content) {
		t.Fatal("Classic must draw the frame exactly as its caller's style")
	}
	for _, name := range LookNames() {
		useLook(t, name)
		for _, out := range []string{Box(s, false, content), TitledBox(s, true, "Title", content)} {
			if lipgloss.Width(out) != lipgloss.Width(classic) || lipgloss.Height(out) < lipgloss.Height(classic) {
				t.Errorf("%s: %dx%d, classic %dx%d:\n%s", name, lipgloss.Width(out), lipgloss.Height(out),
					lipgloss.Width(classic), lipgloss.Height(classic), out)
			}
		}
	}
}

func TestFillKeepsTheBackgroundAfterStyledSpans(t *testing.T) {
	trueColor(t)
	bg := lipgloss.Color("#102A6B")
	seq, _, _ := strings.Cut(lipgloss.NewStyle().Background(bg).Render(" "), " ")
	out := Fill(lipgloss.NewStyle().Bold(true).Render("a")+"b\nc\x1b[1md\x1b[me", bg)
	if strings.Count(out, seq) != 4 || !strings.Contains(out, "\x1b[0m"+seq+"b") || !strings.Contains(out, "\x1b[m"+seq+"e") {
		t.Errorf("Fill lost the background:\n%q", out)
	}

	lipgloss.SetColorProfile(termenv.Ascii)
	if got := Fill("a\nb", bg); got != "a\nb" {
		t.Errorf("without colors Fill must change nothing, got %q", got)
	}
}

func TestKeyHintsWrapToTheWidth(t *testing.T) {
	items := []string{"↑↓ navigate", "←→ change", "Enter run", "p preset", "Tab/1-4: switch tab", "q quit"}
	for _, name := range LookNames()[1:] {
		useLook(t, name)
		lines := KeyHints(items, 30)
		if len(lines) < 2 {
			t.Errorf("%s: %d line(s), want the hints wrapped", name, len(lines))
		}
		all := ""
		for _, l := range lines {
			if lipgloss.Width(l) > 30 {
				t.Errorf("%s: %q is %d wide, over 30", name, l, lipgloss.Width(l))
			}
			all += l
		}
		if !strings.Contains(all, "switch tab") || !strings.Contains(all, "Tab/1-4") {
			t.Errorf("%s: a hint went missing: %q", name, all)
		}
	}
}

func TestScreenPaintsEdgeToEdge(t *testing.T) {
	useLook(t, Commander)
	out := Screen("ab\nc", 5, 4)
	lines := strings.Split(out, "\n")
	if len(lines) != 4 {
		t.Fatalf("%d lines, want 4", len(lines))
	}
	for _, l := range lines {
		if lipgloss.Width(l) != 5 {
			t.Errorf("%q is %d wide, want 5", l, lipgloss.Width(l))
		}
	}
	useLook(t, Panels)
	if Screen("ab", 5, 4) != "ab" {
		t.Error("a look without a screen color must leave the view alone")
	}
}

// A selected row is one style in the other looks: a colored icon inside it
// must not end the fill.
func TestPickDrawsTheRowInOneStyle(t *testing.T) {
	trueColor(t)
	s := lipgloss.NewStyle().Background(Primary).Padding(0, 1)
	text := "▸ " + lipgloss.NewStyle().Foreground(Success).Render("✓") + " orders"
	useLook(t, Classic)
	if Pick(s, text) != s.Render(text) {
		t.Error("Classic must draw the row as its caller's style")
	}
	for _, name := range LookNames()[1:] {
		useLook(t, name)
		out := Pick(s, text)
		if lipgloss.Width(out) != lipgloss.Width(s.Render(text)) || !strings.Contains(out, "▸ ✓ orders") {
			t.Errorf("%s: %q, want the row unbroken and as wide as Classic's", name, out)
		}
	}
}

// Commander paints a dark screen in full-screen views, so it draws the dark
// shades there even on a light terminal, and gives the terminal's own
// background back when the look changes.
func TestCommanderDrawsDarkShadesOnItsScreen(t *testing.T) {
	prevFull, prevDark := fullScreen, termDark
	t.Cleanup(func() {
		fullScreen, termDark = prevFull, prevDark
		lipgloss.SetHasDarkBackground(true)
	})
	useLook(t, Classic)
	fullScreen, termDark = false, nil
	lipgloss.SetHasDarkBackground(false)

	useLook(t, Commander)
	if lipgloss.HasDarkBackground() {
		t.Error("plain output has no screen: Commander must keep the terminal's light shades")
	}
	FullScreen()
	if !lipgloss.HasDarkBackground() {
		t.Error("on its screen Commander must draw the dark shades")
	}
	useLook(t, Panels)
	if lipgloss.HasDarkBackground() {
		t.Error("leaving Commander must give the light terminal its shades back")
	}
}

// Downsampled, Surface is navy on 256 colors and blue on 16: Commander's
// screen, not a pane.
func TestPaneFillStaysGrayOnFewerColors(t *testing.T) {
	r := lipgloss.NewRenderer(nil)
	r.SetHasDarkBackground(true)
	r.SetColorProfile(termenv.ANSI256)
	if got := r.NewStyle().Background(PaneFill).Render("x"); !strings.Contains(got, "48;5;235m") {
		t.Errorf("256 colors: %q, want gray 235", got)
	}
	r.SetColorProfile(termenv.ANSI)
	if got := r.NewStyle().Background(PaneFill).Render("x"); strings.Contains(got, "44m") {
		t.Errorf("16 colors: %q is blue", got)
	}
}

// Tokens point into the active palette, so a style built before a switch
// draws with the new palette after it.
func TestUsePaletteRecolorsStylesAlreadyBuilt(t *testing.T) {
	trueColor(t)
	lipgloss.SetHasDarkBackground(true)
	prev := ActivePalette()
	t.Cleanup(func() { _ = UsePalette(prev) })
	_ = UsePalette(DefaultName)
	s := lipgloss.NewStyle().Foreground(Accent).Background(PaneFill)
	before := s.Render("x")
	if err := UsePalette("Dracula"); err != nil || ActivePalette() != "dracula" {
		t.Fatalf("UsePalette(Dracula) = %v, active %s", err, ActivePalette())
	}
	if after := s.Render("x"); after == before {
		t.Errorf("the style kept the default colors: %q", after)
	}
	if err := UsePalette("neon"); err == nil || ActivePalette() != "dracula" {
		t.Errorf("an unknown theme must be refused and change nothing")
	}
	_ = UsePalette(DefaultName)
	for _, want := range append(Names()[1:], DefaultName) {
		if got := NextPalette(); got != want {
			t.Fatalf("NextPalette() = %s, want %s", got, want)
		}
	}
}

func TestSwitchedOnScreenNamesOnlyWhatChanged(t *testing.T) {
	prevFull, prevLook, prevPalette := fullScreen, screenLook, screenPalette
	t.Cleanup(func() {
		fullScreen, screenLook, screenPalette = prevFull, prevLook, prevPalette
		_ = UsePalette(DefaultName)
	})
	useLook(t, Classic)
	_ = UsePalette(DefaultName)
	fullScreen = false
	if l, p := SwitchedOnScreen(); l != "" || p != "" {
		t.Errorf("before a full-screen view: %q %q", l, p)
	}
	FullScreen()
	if l, p := SwitchedOnScreen(); l != "" || p != "" {
		t.Errorf("nothing switched yet: %q %q", l, p)
	}
	NextLook()
	if l, p := SwitchedOnScreen(); l != Panels || p != "" {
		t.Errorf("after T: %q %q, want panels and no palette", l, p)
	}
	NextPalette()
	if _, p := SwitchedOnScreen(); p != Names()[1] {
		t.Errorf("after C: palette %q, want %s", p, Names()[1])
	}
}
