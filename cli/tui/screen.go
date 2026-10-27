package tui

import (
	"strings"

	"github.com/bmscomp/kates/cli/pkg/theme"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The pieces the lab draws a screen with, for the other full-screen views to
// look like it.

// ScreenHeader is the title bar of a full-screen view, width columns wide:
// title stands out and rest follows it.
func ScreenHeader(title, rest string, width int) string {
	if !theme.CurrentLook().Classic() {
		return theme.TitleBar(title, rest, width)
	}
	// Cut, not wrapped: the padding takes a column either side.
	return labHeaderStyle.Width(width).Render(ansi.Truncate("  "+title+rest, max(width-2, 0), glyphs().Ellipsis))
}

// Pane is a framed pane as the lab draws its results, width by height cells:
// the title, then as much of body as fits, each line cut to the pane. Looks
// with titles in the border put the title there.
func Pane(title, body string, width, height int, focused bool) string {
	inner := width - 2
	rows := height - 2
	if !theme.CurrentLook().TitleInBorder {
		rows-- // the title's line
	}
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) > rows {
		lines = lines[:max(rows, 0)]
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, inner, glyphs().Ellipsis)
	}
	// No padding: the body indents itself, as the lab's results do.
	s := detailBox().Padding(0).Width(inner).Height(height - 2)
	return theme.TitledBox(s, focused, detailTitleStyle.MarginBottom(0).Render(" "+title), strings.Join(lines, "\n"))
}

// PaneHeight is the height a Pane needs to show all of body.
func PaneHeight(body string) int {
	h := strings.Count(strings.TrimRight(body, "\n"), "\n") + 1 + 2
	if !theme.CurrentLook().TitleInBorder {
		h++
	}
	return h
}

// HelpLines is the lab's help for bindings, wrapped to width, one indented
// line each.
func HelpLines(bindings []key.Binding, width int) string {
	lines := lookHelp(bindings, width-4)
	if lines == nil {
		sep := "  " + glyphs().Dot + "  "
		for _, l := range wrapHelp(helpEntries(bindings), sep, width-4) {
			lines = append(lines, dimStyle.Render(l))
		}
	}
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString("\n  " + l)
	}
	return sb.String()
}

// JoinPanes sets panes side by side, one column apart.
func JoinPanes(left, right string) string {
	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
}
