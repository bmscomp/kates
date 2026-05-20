package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/klster/kates-cli/pkg/theme"
)

// Adaptive palette for the TUI — all values flip between light/dark terminal modes.
var (
	purple = theme.Secondary // violet headings
	indigo = theme.Primary   // deep blue accents
	cyan   = theme.Info      // teal keys / accents
	green  = theme.Success   // healthy status
	amber  = theme.Warning   // warn status
	red    = theme.Error     // error status
	gray   = theme.Muted     // dim / secondary text

	// Structural TUI colors — background panels always use a dark surface on
	// dark terminals and a light surface on light terminals.
	tabBar = lipgloss.AdaptiveColor{Light: "#E5E7EB", Dark: "#1F2937"}
	tabFg  = lipgloss.AdaptiveColor{Light: "#1E293B", Dark: "#E2E8F0"} // readable body text

	activeTabStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(theme.OnColor).
			Background(purple).
			Padding(0, 2)

	inactiveTabStyle = lipgloss.NewStyle().
				Foreground(tabFg).
				Background(tabBar).
				Padding(0, 2)

	tabGapStyle = lipgloss.NewStyle().
			Background(tabBar).
			Padding(0, 0)

	listItemStyle = lipgloss.NewStyle().
			Foreground(tabFg). // visible on both backgrounds
			PaddingLeft(2)

	selectedItemStyle = lipgloss.NewStyle().
				Foreground(theme.OnColor).
				Background(indigo).
				Bold(true).
				PaddingLeft(1).
				PaddingRight(1)

	detailBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(indigo).
				Padding(1, 2)

	detailTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(purple).
				MarginBottom(1)

	detailKeyStyle = lipgloss.NewStyle().
			Foreground(cyan).
			Width(20)

	detailValueStyle = lipgloss.NewStyle().
				Foreground(tabFg)

	healthyStyle = lipgloss.NewStyle().Foreground(green)
	warnStyle    = lipgloss.NewStyle().Foreground(amber)
	errorStyle   = lipgloss.NewStyle().Foreground(red)
	dimStyle     = lipgloss.NewStyle().Foreground(gray)

	statusBarStyle = lipgloss.NewStyle().
			Background(tabBar).
			Foreground(gray).
			Padding(0, 1)

	filterActiveStyle = lipgloss.NewStyle().
				Foreground(amber).
				Bold(true)
)
