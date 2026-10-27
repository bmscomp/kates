package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func loadedGroups(t *testing.T) groupsModel {
	t.Helper()
	m := newGroupsModel(newKafkaStub(t))
	m.width, m.height = 120, 40
	if !m.loading || !strings.Contains(plain(m.View()), "Loading consumer groups") {
		t.Fatalf("a new groups tab is not loading:\n%s", m.View())
	}
	m, _ = m.Update(m.Init()())
	if m.loading || len(m.groups) != 4 {
		t.Fatalf("after the load: loading %v, %d groups", m.loading, len(m.groups))
	}
	return m
}

func TestGroupsCursorKeys(t *testing.T) {
	m := loadedGroups(t)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.cursor != 0 {
		t.Errorf("up at the top: cursor %d", m.cursor)
	}
	for range 10 {
		m, _ = m.Update(runes("j"))
	}
	if m.cursor != 3 {
		t.Errorf("down past the end: cursor %d, want 3", m.cursor)
	}
	m, _ = m.Update(runes("k"))
	if m.cursor != 2 {
		t.Errorf("k: cursor %d, want 2", m.cursor)
	}
}

func TestGroupsDetailAndBack(t *testing.T) {
	m := loadedGroups(t)
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !m.loading {
		t.Fatalf("enter: cmd %v, loading %v; want the detail loading", cmd, m.loading)
	}
	m, _ = m.Update(cmd())
	if m.view != groupsDetail || m.err != nil {
		t.Fatalf("after the detail load: view %d, err %v", m.view, m.err)
	}
	view := plain(m.View())
	mustContain(t, view, "Group: billing", "STABLE", "Total Lag", "Partition Offsets (2)", "esc/backspace back to the list")

	// Keys of the list do nothing in the detail.
	if next, cmd := m.Update(runes("r")); cmd != nil || next.view != groupsDetail {
		t.Error("r in the detail acted")
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.view != groupsList || m.detail != nil {
		t.Errorf("esc: view %d, detail %v; want the list", m.view, m.detail)
	}
	m.view = groupsDetail
	m.detail = map[string]interface{}{}
	if m, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace}); m.view != groupsList {
		t.Error("backspace did not go back to the list")
	}
}

func TestGroupsDetailFailureStaysOnTheList(t *testing.T) {
	m := loadedGroups(t)
	m, _ = m.Update(groupDetailMsg{err: errors.New("group not found")})
	if m.view != groupsList {
		t.Errorf("a failed detail load opened the detail")
	}
	mustContain(t, plain(m.View()), "Error: group not found")
}

func TestGroupsRefresh(t *testing.T) {
	m := loadedGroups(t)
	m, cmd := m.Update(runes("r"))
	if cmd == nil || !m.loading {
		t.Fatal("r did not reload the groups")
	}
	if msg, ok := cmd().(groupsLoadedMsg); !ok || len(msg.groups) != 4 {
		t.Errorf("the reload returned %#v", msg)
	}
}

func TestGroupsListMarksEachState(t *testing.T) {
	m := loadedGroups(t)
	view := plain(m.viewList())
	g := glyphs()
	for _, row := range []string{g.Bullet + " billing", g.Ring + " audit", g.Cross + " legacy", g.Warn + " rebalancing"} {
		if !strings.Contains(view, row) {
			t.Errorf("want %q in:\n%s", row, view)
		}
	}
	mustContain(t, view, g.Bullet+" STABLE", "enter offsets & lag")

	m.cursor = 2
	mustContain(t, plain(m.viewList()), g.Cross+" DEAD")
	m.cursor = 3
	mustContain(t, plain(m.viewList()), "State               PreparingRebalance")
}

func TestGroupsEmptyAndFailedLoads(t *testing.T) {
	m := groupsModel{width: 120, height: 40}
	m, _ = m.Update(groupsLoadedMsg{})
	mustContain(t, plain(m.View()), "No consumer groups found.")
	m, _ = m.Update(groupsLoadedMsg{err: errors.New("connection refused")})
	mustContain(t, plain(m.View()), "Error: connection refused")
	if m.viewDetail() == "" || !strings.Contains(m.viewDetail(), "No detail available.") {
		t.Error("the detail without data does not say so")
	}
}

func TestLagLabel(t *testing.T) {
	if got := plain(lagLabel("0")); got != "0" {
		t.Errorf("lagLabel(0) = %q", got)
	}
	if got := plain(lagLabel("12")); got != "12" {
		t.Errorf("lagLabel(12) = %q", got)
	}
}
