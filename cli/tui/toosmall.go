package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// MinTUIWidth/MinTUIHeight are the floor every kates full-screen TUI supports.
// 80x24 is the classic terminal default and the smallest size the layouts are
// designed against.
const (
	MinTUIWidth  = 80
	MinTUIHeight = 24
)

// TooSmallCard is the one shared "terminal too small" screen. Every full-screen
// TUI renders this instead of its normal View when the window is below minimum,
// rather than each view clipping, wrapping, or panicking in its own way.
func TooSmallCard(width, height int) string {
	msg := fmt.Sprintf("Terminal too small: %dx%d (need at least %dx%d)",
		width, height, MinTUIWidth, MinTUIHeight)
	hint := "Enlarge the window, or press q to quit."

	var b strings.Builder
	// Vertical centering within whatever height we do have.
	pad := (height - 4) / 2
	if pad < 0 {
		pad = 0
	}
	b.WriteString(strings.Repeat("\n", pad))
	b.WriteString(centerLine(msg, width) + "\n\n")
	b.WriteString(centerLine(dimStyle.Render(hint), width) + "\n")
	return b.String()
}

// TooSmall reports whether a window is below the supported floor.
func TooSmall(width, height int) bool {
	// Zero means "no WindowSizeMsg yet" — do not flash the card during the
	// first frame before the real size arrives.
	if width == 0 || height == 0 {
		return false
	}
	return width < MinTUIWidth || height < MinTUIHeight
}

// centerLine pads s to the middle of a line width cells wide. It measures
// display cells, not bytes: escape codes take none, and a wide rune two.
func centerLine(s string, width int) string {
	pad := (width - lipgloss.Width(s)) / 2
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat(" ", pad) + s
}
