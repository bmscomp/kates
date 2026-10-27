package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestTooSmall(t *testing.T) {
	for _, tc := range []struct {
		w, h int
		want bool
	}{
		{0, 0, false}, // no size yet: no flash of the card
		{0, 10, false},
		{10, 0, false},
		{79, 24, true},
		{80, 23, true},
		{80, 24, false},
		{200, 60, false},
	} {
		if got := TooSmall(tc.w, tc.h); got != tc.want {
			t.Errorf("TooSmall(%d, %d) = %v, want %v", tc.w, tc.h, got, tc.want)
		}
	}
}

func TestTooSmallCardIsCentred(t *testing.T) {
	card := TooSmallCard(70, 20)
	lines := strings.Split(card, "\n")
	if pad := (20 - 4) / 2; !allBlank(lines[:pad]) || strings.TrimSpace(lines[pad]) == "" {
		t.Errorf("want %d blank lines above the message:\n%q", pad, card)
	}
	msg := "Terminal too small: 70x20 (need at least 80x24)"
	for _, want := range []string{msg, "Enlarge the window, or press q to quit."} {
		line := lineWith(lines, want)
		if line == "" {
			t.Fatalf("no line holds %q:\n%s", want, card)
		}
		left := len(plain(line)) - len(strings.TrimLeft(plain(line), " "))
		if want := (70 - len(want)) / 2; left != want {
			t.Errorf("%q indented %d, want %d", plain(line), left, want)
		}
	}
	if got := TooSmallCard(10, 2); !strings.HasPrefix(got, "Terminal too small: 10x2") {
		t.Errorf("a card wider than the window starts at its edge: %q", got)
	}
}

// Centring counts display cells: escape codes take none and a wide rune two.
// It used to count bytes, so a styled or non-ASCII line sat left of centre.
func TestCenterLineMeasuresDisplayWidth(t *testing.T) {
	for _, s := range []string{
		"plain",
		"\x1b[1;35mstyled\x1b[0m",
		"café ✓",
		"日本語",
	} {
		got := centerLine(s, 40)
		pad := len(got) - len(strings.TrimLeft(got, " "))
		if want := (40 - lipgloss.Width(s)) / 2; pad != want {
			t.Errorf("centerLine(%q) pads %d, want %d", s, pad, want)
		}
	}
	if got := centerLine("too wide", 3); got != "too wide" {
		t.Errorf("a line wider than the width got %q", got)
	}
}

func allBlank(lines []string) bool {
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			return false
		}
	}
	return true
}

func lineWith(lines []string, s string) string {
	for _, l := range lines {
		if strings.Contains(plain(l), s) {
			return l
		}
	}
	return ""
}
