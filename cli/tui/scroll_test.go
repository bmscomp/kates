package tui

import "testing"

func TestScrollWindow(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		cursor, count, height int
		start, end            int
	}{
		{"empty list", 0, 0, 10, 0, 0},
		{"empty list, cursor past it", 3, 0, 10, 0, 0},
		{"negative count", 0, -1, 10, 0, 0},
		{"list shorter than the window", 2, 5, 10, 0, 5},
		{"list as long as the window", 9, 10, 10, 0, 10},
		{"cursor at the top", 0, 50, 10, 0, 10},
		{"cursor on the last row of the first window", 9, 50, 10, 0, 10},
		{"cursor one past the first window", 10, 50, 10, 1, 11},
		{"cursor at the bottom", 49, 50, 10, 40, 50},
		{"cursor past the bottom is clamped", 70, 50, 10, 40, 50},
		{"negative cursor is clamped", -4, 50, 10, 0, 10},
		{"height zero still shows the cursor", 7, 50, 0, 7, 8},
		{"negative height still shows the cursor", 0, 50, -3, 0, 1},
		{"one-row window follows the cursor", 30, 50, 1, 30, 31},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start, end := scrollWindow(tc.cursor, tc.count, tc.height)
			if start != tc.start || end != tc.end {
				t.Errorf("scrollWindow(%d, %d, %d) = [%d, %d), want [%d, %d)",
					tc.cursor, tc.count, tc.height, start, end, tc.start, tc.end)
			}
			if tc.count > 0 {
				cursor := max(0, min(tc.cursor, tc.count-1))
				if cursor < start || cursor >= end {
					t.Errorf("cursor %d is outside the window [%d, %d)", cursor, start, end)
				}
			}
		})
	}
}

func TestListRows(t *testing.T) {
	for height, want := range map[int]int{0: 20, 12: 20, 13: 5, 24: 16, 60: 52} {
		if got := listRows(height); got != want {
			t.Errorf("listRows(%d) = %d, want %d", height, got, want)
		}
	}
}
