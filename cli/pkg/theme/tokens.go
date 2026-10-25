// Package theme provides the centralized design token system for the Kates CLI.
// All color values are expressed as lipgloss.AdaptiveColor pairs so the terminal
// renderer automatically selects the correct shade based on the background.
//
// The tokens come from one named Palette, chosen once at start-up (see
// Selected): KATES_THEME, else `theme:` in ~/.kates.yaml, else "default".
// Styles across the CLI are built from these variables when their packages
// initialise, so the choice cannot change mid-process.
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

// Active is the palette this process renders with.
var Active = Palettes[Selected()]

// ─── Core semantic roles ─────────────────────────────────────────────────────

var (
	// Accent is the primary interactive color — used for banners, focused
	// borders, and active selectors.
	Accent = Active.Accent

	// Text is primary body text — must have high contrast on both backgrounds.
	Text = Active.Text

	// Muted is secondary / de-emphasized text (descriptions, hints, dim labels).
	Muted = Active.Muted

	// Subtle is used for borders and separator lines.
	Subtle = Active.Subtle

	// Surface is the background color for panels and status bars.
	Surface = Active.Surface
)

// ─── Status / semantic ───────────────────────────────────────────────────────

var (
	// Success indicates a passing / healthy / completed state.
	Success = Active.Success

	// Warning indicates a degraded or cautionary state.
	Warning = Active.Warning

	// Error indicates a failure or critical state.
	Error = Active.Error

	// Info is used for informational highlights (step headers, teal accents).
	Info = Active.Info
)

// ─── Hierarchy ───────────────────────────────────────────────────────────────

var (
	// Primary is used for top-level headings and phase labels.
	Primary = Active.Primary

	// Secondary is used for sub-headings, key labels, and section titles.
	Secondary = Active.Secondary

	// Highlight is the selected-item foreground color in lists and multi-selects.
	Highlight = Active.Highlight
)

// ─── Fixed / context-independent ─────────────────────────────────────────────

var (
	// OnColor is used as foreground on colored backgrounds (e.g. badges).
	OnColor = Active.OnColor

	// OnDark is the foreground for dark/filled button backgrounds.
	OnDark = Active.OnDark
)
