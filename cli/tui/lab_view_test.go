package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestLabViewAtEachLayout(t *testing.T) {
	for _, tc := range []struct {
		width, height int
		want          []string
	}{
		{90, 30, []string{"Kates Lab", "Interactive Performance Tuning", "Iteration History", "Throughput:"}},
		{160, 40, []string{"http://kates.example", "Err %", "0.1000%", "Replication"}},
	} {
		lab := labWithHistory(t)
		next, _ := lab.Update(tea.WindowSizeMsg{Width: tc.width, Height: tc.height})
		lab = next.(LabModel)
		view := lab.View()
		mustContain(t, plain(view), tc.want...)
		for _, l := range strings.Split(view, "\n") {
			if w := lipgloss.Width(l); w > tc.width {
				t.Errorf("%dx%d: a line is %d cells: %q", tc.width, tc.height, w, plain(l))
			}
		}
	}

	lab := labWithHistory(t)
	lab.width, lab.height = 70, 30
	if !strings.Contains(lab.View(), "Terminal too small") {
		t.Error("70 columns is below the floor, but the lab drew its layout")
	}
	lab.quitting = true
	if lab.View() != "" {
		t.Error("a quitting lab still draws")
	}
}

// Under 50 columns the parameter pane abbreviates its labels and windows a
// long value list around the current value.
func TestLabCompactParams(t *testing.T) {
	lab := NewLab(nil, "")
	lab.cursor = 1 // Producers: six values
	lab.params[1].Current = 5
	out := plain(lab.viewParams(30))
	mustContain(t, out, "Type", "Prod", "RecSize", "Comp", "Batch", "Linger", "Parts", "Repl", "32")
	if strings.Contains(out, "Record Size") {
		t.Errorf("compact pane kept the long label:\n%s", out)
	}
	for label, want := range map[string]string{"Acks": "Acks", "Something Long": "Somethin"} {
		if got := compactLabel(label); got != want {
			t.Errorf("compactLabel(%q) = %q, want %q", label, got, want)
		}
	}
	if out := plain(lab.viewParams(80)); !strings.Contains(out, "Record Size") {
		t.Errorf("the wide pane abbreviated:\n%s", out)
	}
}

func TestLabParamKeys(t *testing.T) {
	lab := NewLab(nil, "")
	press := func(k tea.KeyMsg) {
		t.Helper()
		lab, _ = updateLab(t, lab, k)
	}
	press(runes("k"))
	if lab.cursor != 0 {
		t.Errorf("up at the top: cursor %d", lab.cursor)
	}
	for range 20 {
		press(tea.KeyMsg{Type: tea.KeyDown})
	}
	if lab.cursor != len(lab.params)-1 {
		t.Errorf("down past the end: cursor %d", lab.cursor)
	}
	press(runes("j"))
	press(tea.KeyMsg{Type: tea.KeyUp})
	p := lab.cursor
	cur := lab.params[p].Current
	press(runes("l"))
	press(tea.KeyMsg{Type: tea.KeyRight})
	if lab.params[p].Current != min(cur+2, len(lab.params[p].Values)-1) {
		t.Errorf("right twice: value %d from %d", lab.params[p].Current, cur)
	}
	for range 10 {
		press(runes("h"))
	}
	if lab.params[p].Current != 0 {
		t.Errorf("left past the start: value %d", lab.params[p].Current)
	}
	for range 10 {
		press(tea.KeyMsg{Type: tea.KeyRight})
	}
	if lab.params[p].Current != len(lab.params[p].Values)-1 {
		t.Errorf("right past the end: value %d", lab.params[p].Current)
	}
}

func TestLabPinPicker(t *testing.T) {
	lab := labWithHistory(t)
	press := func(k tea.KeyMsg) {
		t.Helper()
		lab, _ = updateLab(t, lab, k)
	}
	press(runes("c"))
	if lab.view != labPinSelect || lab.pinA != 1 || lab.pinB != 2 {
		t.Fatalf("c: view %d, pins %d/%d", lab.view, lab.pinA, lab.pinB)
	}
	mustContain(t, plain(lab.View()), "Select Iterations to Compare", "A  #2", "B  #3")
	cursor := lab.cursor
	for range 5 {
		press(runes("k"))
		press(tea.KeyMsg{Type: tea.KeyLeft})
	}
	if lab.pinA != 0 || lab.pinB != 0 || lab.cursor != cursor {
		t.Errorf("up/left to the start: pins %d/%d, cursor %d", lab.pinA, lab.pinB, lab.cursor)
	}
	mustContain(t, plain(lab.View()), "AB #1")
	for range 5 {
		press(runes("j"))
		press(runes("l"))
	}
	if lab.pinA != 2 || lab.pinB != 2 {
		t.Errorf("down/right to the end: pins %d/%d", lab.pinA, lab.pinB)
	}
	press(runes("k"))
	press(tea.KeyMsg{Type: tea.KeyEnter})
	if lab.view != labHistory || lab.running {
		t.Fatalf("enter in the picker: view %d, running %v; want the diff, no run", lab.view, lab.running)
	}
	mustContain(t, plain(lab.View()), "Diff: #2 vs #3", "Throughput", "P99 Latency", "Parameter Changes")
	press(tea.KeyMsg{Type: tea.KeyEsc})
	if lab.view != labConfig {
		t.Errorf("esc: view %d", lab.view)
	}

	// d opens the diff on the last two when no pins are set.
	lab.pinA, lab.pinB = -1, -1
	press(runes("d"))
	if lab.view != labHistory || lab.pinA != 1 || lab.pinB != 2 {
		t.Errorf("d: view %d, pins %d/%d", lab.view, lab.pinA, lab.pinB)
	}
}

func TestLabDiffAndCompareNeedTwoIterations(t *testing.T) {
	lab := NewLab(nil, "")
	for _, k := range []string{"c", "d"} {
		if next, _ := updateLab(t, lab, runes(k)); next.view != labConfig {
			t.Errorf("%s with no iterations changed the view to %d", k, next.view)
		}
	}
	if got := plain(lab.viewDiff(80)); !strings.Contains(got, "Need at least 2 iterations") {
		t.Errorf("viewDiff with none: %q", got)
	}
	mustContain(t, plain(lab.viewResults(80)), "No iterations yet.")
}

func TestLabWarmupKeyCycles(t *testing.T) {
	lab := NewLab(nil, "")
	for i := 1; i <= 5; i++ {
		lab, _ = updateLab(t, lab, runes("W"))
		if lab.warmupCount != i {
			t.Fatalf("W #%d: warmup %d", i, lab.warmupCount)
		}
	}
	mustContain(t, plain(lab.status), "Warmup: 5 iteration(s)")
	lab, _ = updateLab(t, lab, runes("W"))
	if lab.warmupCount != 0 || !strings.Contains(plain(lab.status), "Warmup: off") {
		t.Errorf("W past 5: warmup %d, status %q", lab.warmupCount, plain(lab.status))
	}
}

func TestLabPresetKeyCycles(t *testing.T) {
	lab := NewLab(nil, "")
	for _, want := range []string{"Low Latency", "Max Throughput", "Durability", "Low Latency"} {
		lab, _ = updateLab(t, lab, runes("p"))
		if !strings.Contains(plain(lab.status), "Preset: "+want) {
			t.Errorf("status %q, want preset %s", plain(lab.status), want)
		}
	}
	if lab.paramVal("type") != "SPIKE" || lab.paramVal("acks") != "1" {
		t.Errorf("Low Latency set type %s acks %s", lab.paramVal("type"), lab.paramVal("acks"))
	}
}

func TestLabFileKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	lab := labWithHistory(t)

	lab, _ = updateLab(t, lab, runes("e"))
	mustContain(t, plain(lab.status), glyphs().Check+" Exported to "+home)
	lab, _ = updateLab(t, lab, runes("w"))
	mustContain(t, plain(lab.status), glyphs().Check+" Session saved to "+home)

	fresh := NewLab(nil, "")
	fresh, _ = updateLab(t, fresh, runes("e"))
	if strings.Contains(plain(fresh.status), "Exported") {
		t.Error("e with no iterations exported")
	}
	fresh, _ = updateLab(t, fresh, runes("L"))
	mustContain(t, plain(fresh.status), "Session loaded from "+home)
	if len(fresh.iterations) != 3 {
		t.Errorf("L loaded %d iterations, want 3", len(fresh.iterations))
	}

	t.Setenv("HOME", t.TempDir())
	fresh, _ = updateLab(t, NewLab(nil, ""), runes("L"))
	mustContain(t, plain(fresh.status), "No saved session found")
}

func TestLabQuitKey(t *testing.T) {
	lab, cmd := updateLab(t, NewLab(nil, ""), runes("q"))
	if !lab.quitting || cmd == nil {
		t.Fatal("q did not quit")
	}
	if lab.Init() != nil {
		t.Error("Init started something")
	}
}
