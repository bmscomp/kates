package tui

import (
	"strings"

	"github.com/bmscomp/kates/cli/pkg/theme"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"
)

// helpEntries is the help text of each binding, "key description", in the
// order given. A binding without help text is left out.
func helpEntries(bindings []key.Binding) []string {
	entries := make([]string, 0, len(bindings))
	for _, b := range bindings {
		h := b.Help()
		if h.Key == "" {
			continue
		}
		entries = append(entries, h.Key+" "+h.Desc)
	}
	return entries
}

// wrapHelp joins entries with sep into lines no wider than width display
// cells. An entry wider than width gets a line of its own; a width under one
// puts everything on one line.
func wrapHelp(entries []string, sep string, width int) []string {
	var lines []string
	line := ""
	for _, e := range entries {
		switch {
		case line == "":
			line = e
		case width > 0 && lipgloss.Width(line+sep+e) > width:
			lines = append(lines, line)
			line = e
		default:
			line += sep + e
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// lookHelp is the help for bindings drawn in the current look, wrapped to
// width; nil in Classic, whose callers draw their own.
func lookHelp(bindings []key.Binding, width int) []string {
	if theme.CurrentLook().Classic() {
		return nil
	}
	items := make([]string, 0, len(bindings))
	for _, b := range bindings {
		// "key: desc", because a key's help can hold spaces: "W (shift+w)".
		if h := b.Help(); h.Key != "" {
			items = append(items, h.Key+": "+h.Desc)
		}
	}
	return theme.KeyHints(items, width)
}

// renderHelp is the dashboard's help bar for bindings, wrapped to width.
func renderHelp(bindings []key.Binding, width int) string {
	if lines := lookHelp(bindings, width-2); lines != nil {
		return " " + strings.Join(lines, "\n ")
	}
	sep := " " + glyphs().Dot + " "
	// The bar's padding takes a cell either side.
	lines := wrapHelp(helpEntries(bindings), sep, width-2)
	for i, l := range lines {
		lines[i] = statusBarStyle.Render(l)
	}
	return strings.Join(lines, "\n")
}

// hint is the help of bindings as one inline line, for a panel's footer.
func hint(bindings ...key.Binding) string {
	return strings.Join(helpEntries(bindings), "  "+glyphs().Dot+"  ")
}

// listRow is one row of a list pane, marked when it holds the cursor.
func listRow(label string, selected bool) string {
	if selected {
		return theme.Pick(selectedItemStyle, glyphs().Arrow+" "+label)
	}
	return listItemStyle.Render("  " + label)
}
