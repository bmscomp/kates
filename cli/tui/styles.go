package tui

import "github.com/charmbracelet/lipgloss"

var (
	purple = lipgloss.Color("#7C3AED")
	indigo = lipgloss.Color("#6366F1")
	cyan   = lipgloss.Color("#06B6D4")
	green  = lipgloss.Color("#22C55E")
	amber  = lipgloss.Color("#F59E0B")
	red    = lipgloss.Color("#EF4444")
	gray   = lipgloss.Color("#6B7280")
	light  = lipgloss.Color("#E5E7EB")
	dim    = lipgloss.Color("#4B5563")

	activeTabStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#000000")).
			Background(purple).
			Padding(0, 2)

	inactiveTabStyle = lipgloss.NewStyle().
				Foreground(light).
				Background(dim).
				Padding(0, 2)

	tabGapStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#1F2937")).
			Padding(0, 0)

	listItemStyle = lipgloss.NewStyle().
			Foreground(light).
			PaddingLeft(2)

	selectedItemStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#000000")).
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
				Foreground(light)

	healthyStyle = lipgloss.NewStyle().Foreground(green)
	warnStyle    = lipgloss.NewStyle().Foreground(amber)
	errorStyle   = lipgloss.NewStyle().Foreground(red)
	dimStyle     = lipgloss.NewStyle().Foreground(gray)

	statusBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#1F2937")).
			Foreground(gray).
			Padding(0, 1)

	filterActiveStyle = lipgloss.NewStyle().
				Foreground(amber).
				Bold(true)
)
