// Package theme provides the centralized design token system for the Kates CLI.
// All color values are expressed as lipgloss.AdaptiveColor pairs so the terminal
// renderer automatically selects the correct shade based on the background.
//
// The tokens come from one named Palette, chosen at start-up (see Selected):
// KATES_THEME, else `theme:` in ~/.kates.yaml, else "default". Each token
// points into the active palette rather than copying it, so UsePalette
// recolors every style already built from the tokens.
//
// Usage:
//
//	import "github.com/bmscomp/kates/cli/pkg/theme"
//
//	style := lipgloss.NewStyle().Foreground(theme.Accent)
package theme

import "github.com/charmbracelet/lipgloss"

// adaptive is a convenience constructor for AdaptiveColor.
func adaptive(light, dark string) lipgloss.AdaptiveColor {
	return lipgloss.AdaptiveColor{Light: light, Dark: dark}
}

// Color is a token: it draws with its role's color in the active palette,
// whichever palette is active when it renders.
type Color struct{ *lipgloss.AdaptiveColor }

// Active is the palette this process renders with now; activeName names it.
var (
	Active     = Palettes[Selected()]
	activeName = Selected()
)

// ─── Core semantic roles ─────────────────────────────────────────────────────

var (
	// Accent is the primary interactive color — used for banners, focused
	// borders, and active selectors.
	Accent = Color{&Active.Accent}

	// Text is primary body text — must have high contrast on both backgrounds.
	Text = Color{&Active.Text}

	// Muted is secondary / de-emphasized text (descriptions, hints, dim labels).
	Muted = Color{&Active.Muted}

	// Subtle is used for borders and separator lines.
	Subtle = Color{&Active.Subtle}

	// Surface is the background color for panels and status bars.
	Surface = Color{&Active.Surface}
)

// ─── Status / semantic ───────────────────────────────────────────────────────

var (
	// Success indicates a passing / healthy / completed state.
	Success = Color{&Active.Success}

	// Warning indicates a degraded or cautionary state.
	Warning = Color{&Active.Warning}

	// Error indicates a failure or critical state.
	Error = Color{&Active.Error}

	// Info is used for informational highlights (step headers, teal accents).
	Info = Color{&Active.Info}
)

// ─── Hierarchy ───────────────────────────────────────────────────────────────

var (
	// Primary is used for top-level headings and phase labels.
	Primary = Color{&Active.Primary}

	// Secondary is used for sub-headings, key labels, and section titles.
	Secondary = Color{&Active.Secondary}

	// Highlight is the selected-item foreground color in lists and multi-selects.
	Highlight = Color{&Active.Highlight}
)

// ─── Fixed / context-independent ─────────────────────────────────────────────

var (
	// OnColor is used as foreground on colored backgrounds (e.g. badges).
	OnColor = Color{&Active.OnColor}

	// OnDark is the foreground for dark/filled button backgrounds.
	OnDark = Color{&Active.OnDark}
)
