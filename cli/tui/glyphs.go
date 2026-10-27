package tui

import (
	"strings"

	"github.com/bmscomp/kates/cli/output"
	"github.com/charmbracelet/lipgloss"
)

// tuiGlyphSet is output.Glyphs() plus the marks only the TUIs draw: the
// controller star, delta arrows, the help's key arrows and the status icons.
// Every mark a TUI renders comes from here, so --plain, KATES_ASCII and
// LC_ALL=C give ASCII screens, as they give ASCII command output.
type tuiGlyphSet struct {
	output.GlyphSet

	Star     string // the KRaft leader in the broker list
	Up       string // a delta that rose
	Down     string // a delta that fell
	Approx   string // a delta within noise
	Dash     string // no value
	To       string // a parameter's old → new value
	Dot      string // the separator between inline items
	Ellipsis string // a line still loading
	Caret    string // the filter's text cursor

	// The arrow keys, as the help names them.
	KeyUp, KeyDown, KeyLeft, KeyRight string

	// Status-line icons; empty in ASCII, where withIcon drops them.
	Warmup, Running, Median, Stopped, Preset, Sweep string
}

var utf8TUIGlyphs = tuiGlyphSet{
	Star: "★", Up: "▲", Down: "▼", Approx: "≈", Dash: "—", To: "→",
	Dot: "·", Ellipsis: "…", Caret: "▌",
	KeyUp: "↑", KeyDown: "↓", KeyLeft: "←", KeyRight: "→",
	Warmup: "🔥", Running: "⏳", Median: "📊", Stopped: "⏹", Preset: "⚡", Sweep: "⟳",
}

var asciiTUIGlyphs = tuiGlyphSet{
	Star: "*", Up: "+", Down: "-", Approx: "~", Dash: "-", To: "->",
	Dot: "|", Ellipsis: "...", Caret: "_",
	KeyUp: "up", KeyDown: "down", KeyLeft: "left", KeyRight: "right",
}

// glyphs returns the active TUI glyph set. It is read on every render, not
// cached: --plain is applied after package init.
func glyphs() tuiGlyphSet {
	g := utf8TUIGlyphs
	if output.ASCII() {
		g = asciiTUIGlyphs
	}
	g.GlyphSet = output.Glyphs()
	return g
}

// withIcon prefixes text with icon and a space, or returns text alone when
// the glyph set has no icon for it.
func withIcon(icon, text string) string {
	if icon == "" {
		return text
	}
	return icon + " " + text
}

// rule is a horizontal separator n cells wide.
func rule(n int) string {
	return strings.Repeat(glyphs().Rule, n)
}

// boxBorder is the panel border: rounded, or plain ASCII when the glyph set
// is.
func boxBorder() lipgloss.Border {
	if output.ASCII() {
		return lipgloss.ASCIIBorder()
	}
	return lipgloss.RoundedBorder()
}

// detailBox is the bordered detail panel, its border chosen per render.
func detailBox() lipgloss.Style {
	return detailBorderStyle.Border(boxBorder())
}
