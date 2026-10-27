package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The topic filter took a key only when its String() was one byte long, so
// every non-ASCII rune was dropped; it reads typed runes, as produce does.
func TestFilterAcceptsNonASCIIRunes(t *testing.T) {
	m := Model{filtering: true}
	press := func(r ...rune) {
		t.Helper()
		next, _ := m.handleFilter(tea.KeyMsg{Type: tea.KeyRunes, Runes: r})
		m = next.(Model)
	}
	press('é')
	press('v', 'é') // a paste arrives as several runes in one key
	if m.filterBuf != "évé" {
		t.Fatalf("filterBuf = %q, want évé", m.filterBuf)
	}

	next, _ := m.handleFilter(tea.KeyMsg{Type: tea.KeyBackspace})
	m = next.(Model)
	if m.filterBuf != "év" {
		t.Errorf("after backspace filterBuf = %q, want év (a whole rune removed)", m.filterBuf)
	}

	next, _ = m.handleFilter(tea.KeyMsg{Type: tea.KeyUp})
	if m = next.(Model); m.filterBuf != "év" {
		t.Errorf("an arrow key changed the filter to %q", m.filterBuf)
	}
}
