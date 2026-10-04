package output

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// Printable returns s without anything that acts on a terminal instead of
// printing: ANSI escape sequences, removed whole; any other C0 or C1 control
// character; and Unicode format characters, which include the bidi overrides
// that reorder a line and the zero-width characters that hide text. Line
// breaks and tabs become spaces, so the string stays on its row of a table.
//
// It is for text the CLI prints but did not write, such as the names and
// descriptions a backend returns. lipgloss styles text; it does not escape it,
// and with styling off (a pipe, NO_COLOR) it passes the text through as is, so
// an ESC in such a string reaches the terminal and can recolour, clear or
// retitle it.
func Printable(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n', r == '\r', r == '\t', unicode.In(r, unicode.Zl, unicode.Zp):
			return ' '
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, ansi.Strip(s))
}
