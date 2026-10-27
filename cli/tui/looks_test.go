package tui

import (
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/pkg/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func keepLook(t *testing.T) {
	prev, profile := theme.CurrentLook().Name, lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() {
		_ = theme.UseLook(prev)
		lipgloss.SetColorProfile(profile)
	})
}

func labAt(t *testing.T, w, h int) LabModel {
	t.Helper()
	var m tea.Model = NewLab(nil, "http://localhost:8080")
	m, _ = m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	lm := m.(LabModel)
	lm.iterations = []labIteration{
		{Number: 1, Throughput: 45200, P99Ms: 12, Delta: "—"},
		{Number: 2, Throughput: 52100, P99Ms: 8, Delta: "▲15%"},
		{Number: 3, Throughput: 48700, P99Ms: 11, Delta: "▼7%"},
	}
	return lm
}

func tuiAt(t *testing.T, w, h int) tea.Model {
	t.Helper()
	var m tea.Model = New(nil)
	m, _ = m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	info := &client.ClusterInfo{ClusterID: "c1", BrokerCount: client.IntOf(2), Brokers: []client.BrokerNode{
		{ID: client.IntOf(0), Host: "kafka", Port: client.IntOf(9092)},
		{ID: client.IntOf(1), Host: "kafka", Port: client.IntOf(9092)},
	}}
	m, _ = m.Update(brokersLoadedMsg{info: info})
	m, _ = m.Update(topicsLoadedMsg{topics: []client.KafkaTopic{{Name: "orders"}, {Name: "a-topic-name-longer-than-the-list-is-wide"}}})
	return m
}

// assertFits fails when a line of view is wider than the screen: the
// terminal would wrap it and push the layout down.
func assertFits(t *testing.T, what, view string, width int) {
	t.Helper()
	for i, line := range strings.Split(view, "\n") {
		if w := lipgloss.Width(line); w > width {
			t.Errorf("%s: line %d is %d wide, the screen %d:\n%s", what, i, w, width, line)
		}
	}
}

func TestViewsFitTheScreenInEveryLook(t *testing.T) {
	keepLook(t)
	for _, name := range theme.LookNames() {
		if err := theme.UseLook(name); err != nil {
			t.Fatal(err)
		}
		for _, w := range []int{80, 110, 130} {
			assertFits(t, name+" lab", labAt(t, w, 30).View(), w)
		}
		m := tuiAt(t, 100, 30)
		for _, key := range []string{"2", "1", "3", "4"} {
			m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
			assertFits(t, name+" tui tab "+key, m.View(), 100)
		}
	}
}

func TestTSwitchesTheLookOnTheSpot(t *testing.T) {
	keepLook(t)
	_ = theme.UseLook(theme.Classic)

	var lab tea.Model = labAt(t, 110, 30)
	lab, _ = lab.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("T")})
	if got := theme.CurrentLook().Name; got != theme.Panels {
		t.Fatalf("T in the lab: look %s, want panels", got)
	}
	if view := lab.View(); !strings.Contains(view, "Look: panels") || !strings.Contains(view, "╭") {
		t.Errorf("the lab does not show the panels look:\n%s", view)
	}

	m := tuiAt(t, 100, 30)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("T")})
	if got := theme.CurrentLook().Name; got != theme.Minimal {
		t.Errorf("T in kates kafka tui: look %s, want minimal", got)
	}
	// Typing a filter is text, not a key binding.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("T")})
	if got := theme.CurrentLook().Name; got != theme.Minimal {
		t.Errorf("T while filtering switched the look to %s", got)
	}
	if !strings.Contains(m.View(), "/ T") {
		t.Errorf("T while filtering did not reach the filter:\n%s", m.View())
	}
}
