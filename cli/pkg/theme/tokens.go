// Package theme provides the centralized design token system for the Kates CLI.
// All color values are expressed as lipgloss.AdaptiveColor pairs so the terminal
// renderer automatically selects the correct shade based on the background.
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

// ─── Core semantic roles ─────────────────────────────────────────────────────

var (
	// Accent is the primary interactive blue — used for banners, focused borders,
	// and active selectors.
	Accent = adaptive("#2563EB", "#60A5FA")

	// Text is primary body text — must have high contrast on both backgrounds.
	Text = adaptive("#1E293B", "#E2E8F0")

	// Muted is secondary / de-emphasized text (descriptions, hints, dim labels).
	Muted = adaptive("#6B7280", "#9CA3AF")

	// Subtle is used for borders and separator lines.
	Subtle = adaptive("#D1D5DB", "#4B5563")

	// Surface is the background color for panels and status bars.
	Surface = adaptive("#F3F4F6", "#1F2937")
)

// ─── Status / semantic ───────────────────────────────────────────────────────

var (
	// Success indicates a passing / healthy / completed state.
	// Blue (not green) — aligns with the kates blue=OK / red=KO palette.
	Success = adaptive("#2563EB", "#60A5FA")

	// Warning indicates a degraded or cautionary state.
	Warning = adaptive("#C2410C", "#FB923C")

	// Error indicates a failure or critical state.
	Error = adaptive("#DC2626", "#F87171")

	// Info is used for informational highlights (step headers, teal accents).
	Info = adaptive("#0891B2", "#22D3EE")
)

// ─── Hierarchy ───────────────────────────────────────────────────────────────

var (
	// Primary is used for top-level headings and phase labels.
	Primary = adaptive("#1D4ED8", "#93C5FD")

	// Secondary is used for sub-headings, key labels, and section titles.
	Secondary = adaptive("#6D28D9", "#C4B5FD")

	// Highlight is the selected-item foreground color in lists and multi-selects.
	Highlight = adaptive("#059669", "#34D399")
)

// ─── Fixed / context-independent ─────────────────────────────────────────────

var (
	// OnColor is used as foreground on colored backgrounds (e.g. badges).
	// Always black — colored backgrounds are bright enough for contrast.
	OnColor = lipgloss.Color("#000000")

	// OnDark is white foreground for dark/filled button backgrounds.
	OnDark = adaptive("#FFFFFF", "#F9FAFB")
)
