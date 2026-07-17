package cmd

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func dashUpdate(t *testing.T, m deployDashboardModel, msg tea.Msg) deployDashboardModel {
	t.Helper()
	next, _ := m.Update(msg)
	dm, ok := next.(deployDashboardModel)
	if !ok {
		t.Fatalf("Update returned %T, want deployDashboardModel", next)
	}
	return dm
}

// Root cause 1 of the ragged border: emoji + VS16 (U+FE0F) sequences. The
// padding stack (viewport/lipgloss via go-runewidth) counts them as ONE cell
// while terminals render TWO, so every such line pushed the pane's right
// border out by a column. No VS16 may survive into the log ring.
func TestDashboardLog_StripsVariationSelectors(t *testing.T) {
	m := NewDeployDashboard(context.Background(), 13)
	m = dashUpdate(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = dashUpdate(t, m, logMsg{text: "⏭️  Kyverno already deployed. Skipping."})
	m = dashUpdate(t, m, logMsg{text: "⚠️ warning with selector"})

	for i, l := range m.logs {
		if strings.ContainsRune(l, '️') {
			t.Errorf("logs[%d] still contains VS16: %q", i, l)
		}
	}
}

// Root cause 2: over-long lines. Nothing truncated them, so one long helm
// message stretched the whole bordered box past the terminal edge and the
// border wrapped into fragments. Every line in the pane content must fit.
func TestDashboardLog_TruncatesToPaneWidth(t *testing.T) {
	m := NewDeployDashboard(context.Background(), 13)
	m = dashUpdate(t, m, tea.WindowSizeMsg{Width: 100, Height: 40})

	long := "📦 Deploying Strimzi Operator (Namespace: strimzi-operator) with a very long trailing explanation that exceeds any reasonable pane width by a comfortable margin"
	m = dashUpdate(t, m, logMsg{text: long})
	m = dashUpdate(t, m, logMsg{text: "\x1b[31mstyled and also much much much much much much much much much much too long to fit in the pane\x1b[0m"})

	w := m.logsViewport.Width
	if w <= 0 {
		t.Fatal("viewport width not set by WindowSizeMsg")
	}
	for i, l := range strings.Split(m.buildLogContent(), "\n") {
		if got := ansi.StringWidth(l); got > w {
			t.Errorf("content line %d width %d exceeds pane width %d: %q", i, got, w, l)
		}
	}
}

// Resizing must re-truncate: lines stored under a wide pane may not fit a
// narrower one.
func TestDashboardLog_RetruncatesOnResize(t *testing.T) {
	m := NewDeployDashboard(context.Background(), 13)
	m = dashUpdate(t, m, tea.WindowSizeMsg{Width: 160, Height: 40})
	m = dashUpdate(t, m, logMsg{text: strings.Repeat("x", 150)})

	m = dashUpdate(t, m, tea.WindowSizeMsg{Width: 100, Height: 40})
	w := m.logsViewport.Width
	for i, l := range strings.Split(m.buildLogContent(), "\n") {
		if got := ansi.StringWidth(l); got > w {
			t.Errorf("after shrink, line %d width %d exceeds %d", i, got, w)
		}
	}
}

// The header box and the panes row must end at the same column. The header
// rendered at m.width while the panes row filled m.width-4, so the dashboard's
// right edge was a staircase: header sticking out past the panes below it.
func TestDashboardRowsShareOneWidth(t *testing.T) {
	for _, w := range []int{80, 100, 120, 160} {
		m := NewDeployDashboard(context.Background(), 13)
		m = dashUpdate(t, m, tea.WindowSizeMsg{Width: w, Height: 40})
		m = dashUpdate(t, m, logMsg{text: "a log line"})

		view := m.View()
		var topBorders []int
		for _, line := range strings.Split(view, "\n") {
			if strings.Contains(line, "╭") {
				topBorders = append(topBorders, ansi.StringWidth(line))
			}
		}
		// Two top-border lines: the header box, and the joined panes row.
		if len(topBorders) < 2 {
			t.Fatalf("width %d: found %d top-border lines, want 2", w, len(topBorders))
		}
		if topBorders[0] != topBorders[1] {
			t.Errorf("width %d: header renders %d cols, panes row %d — right edges misaligned",
				w, topBorders[0], topBorders[1])
		}
	}
}
