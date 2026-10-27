package tui

import (
	"strings"

	"github.com/bmscomp/kates/cli/pkg/theme"
	"github.com/charmbracelet/lipgloss"
)

type helpContext int

const (
	helpMain helpContext = iota
	helpDetail
	helpConsumer
	helpFilter
	helpProduce
)

func renderHelp(ctx helpContext) string {
	var items []string
	switch ctx {
	case helpFilter:
		items = []string{"Type to filter", "Enter: apply", "Esc: cancel"}
	case helpDetail:
		items = []string{"Esc/Backspace: back", "c: consume (topics)", "q: quit"}
	case helpConsumer:
		items = []string{"Esc: stop tailing", "q: quit"}
	case helpProduce:
		items = []string{"Tab: switch field", "Enter: send", "Esc: back to topic list"}
	default:
		items = []string{"Tab/1-4: switch tab", "↑/↓ j/k: navigate", "Enter: detail", "/: filter", "T: look", "q: quit"}
	}
	if !theme.CurrentLook().Classic() {
		return " " + strings.Join(theme.KeyHints(items, 0), "")
	}
	style := lipgloss.NewStyle().Foreground(gray)
	for i, it := range items {
		items[i] = style.Render(it)
	}
	return statusBarStyle.Render(strings.Join(items, dimStyle.Render(" · ")))
}
