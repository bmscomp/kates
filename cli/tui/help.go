package tui

import "github.com/charmbracelet/lipgloss"

type helpContext int

const (
	helpMain helpContext = iota
	helpDetail
	helpConsumer
	helpFilter
)

func renderHelp(ctx helpContext) string {
	style := lipgloss.NewStyle().Foreground(gray)
	sep := dimStyle.Render(" · ")

	var keys string
	switch ctx {
	case helpFilter:
		keys = style.Render("Type to filter") + sep +
			style.Render("Enter: apply") + sep +
			style.Render("Esc: cancel")
	case helpDetail:
		keys = style.Render("Esc/Backspace: back") + sep +
			style.Render("c: consume (topics)") + sep +
			style.Render("q: quit")
	case helpConsumer:
		keys = style.Render("Esc: stop tailing") + sep +
			style.Render("q: quit")
	default:
		keys = style.Render("Tab/1-3: switch tab") + sep +
			style.Render("↑/↓ j/k: navigate") + sep +
			style.Render("Enter: detail") + sep +
			style.Render("/: filter") + sep +
			style.Render("q: quit")
	}
	return statusBarStyle.Render(keys)
}
