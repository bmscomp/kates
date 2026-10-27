package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// loadedDashboard is the tab model with every tab's data loaded from the
// stub, at 120x40.
func loadedDashboard(t *testing.T) Model {
	t.Helper()
	m := New(newKafkaStub(t))
	m = update(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	for _, cmd := range []tea.Cmd{m.brokers.loadBrokers(), m.topics.loadTopics(), m.groups.loadGroups(), m.produce.loadTopics()} {
		m = update(t, m, cmd())
	}
	return m
}

func update(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	next, _ := m.Update(msg)
	return next.(Model)
}

func TestNewStartsOnTopics(t *testing.T) {
	m := New(newKafkaStub(t))
	if m.active != tabTopics {
		t.Errorf("active = %d, want the topics tab", m.active)
	}
	if m.Init() == nil {
		t.Error("Init returned no command; the tabs load nothing")
	}
}

func TestTabKeysSwitchTabs(t *testing.T) {
	m := loadedDashboard(t)
	for _, step := range []struct {
		key  tea.KeyMsg
		want tab
	}{
		{tea.KeyMsg{Type: tea.KeyTab}, tabGroups},
		{tea.KeyMsg{Type: tea.KeyTab}, tabProduce},
		{tea.KeyMsg{Type: tea.KeyTab}, tabBrokers}, // wraps
		{tea.KeyMsg{Type: tea.KeyShiftTab}, tabProduce},
		{runes("1"), tabBrokers},
		{runes("3"), tabGroups},
		{runes("4"), tabProduce},
		{runes("2"), tabTopics},
	} {
		m = update(t, m, step.key)
		if m.active != step.want {
			t.Fatalf("after %q active = %d, want %d", step.key.String(), m.active, step.want)
		}
	}
}

func TestQuitKeys(t *testing.T) {
	for _, k := range []tea.KeyMsg{runes("q"), {Type: tea.KeyCtrlC}} {
		next, cmd := loadedDashboard(t).Update(k)
		if !next.(Model).quitting || cmd == nil {
			t.Fatalf("%q: quitting %v, cmd %v; want a quit", k.String(), next.(Model).quitting, cmd)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("%q: cmd() = %T, want tea.QuitMsg", k.String(), cmd())
		}
		if next.View() != "" {
			t.Errorf("%q: a quitting model still draws", k.String())
		}
	}
}

// Keys that are not the tab bar's go to the active tab only.
func TestKeysRouteToTheActiveTab(t *testing.T) {
	m := loadedDashboard(t)
	m = update(t, m, runes("j"))
	if m.topics.cursor != 1 || m.groups.cursor != 0 || m.brokers.cursor != 0 {
		t.Errorf("j on topics moved cursors topics=%d groups=%d brokers=%d; want topics only",
			m.topics.cursor, m.groups.cursor, m.brokers.cursor)
	}
	m = update(t, m, runes("3"))
	m = update(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.groups.cursor != 1 || m.topics.cursor != 1 {
		t.Errorf("down on groups: groups=%d topics=%d; want groups 1, topics still 1", m.groups.cursor, m.topics.cursor)
	}
}

// A message that is not a key reaches every tab: each takes its own results.
func TestNonKeyMessagesReachEveryTab(t *testing.T) {
	m := New(newKafkaStub(t))
	m = update(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	for name, got := range map[string][2]int{
		"brokers": {m.brokers.width, m.brokers.height},
		"topics":  {m.topics.width, m.topics.height},
		"groups":  {m.groups.width, m.groups.height},
		"produce": {m.produce.width, m.produce.height},
	} {
		if got != [2]int{100, 30} {
			t.Errorf("%s size = %v, want [100 30]", name, got)
		}
	}
	m = update(t, m, m.groups.loadGroups()())
	if len(m.groups.groups) != 4 || m.groups.loading {
		t.Errorf("groups after their load: %d groups, loading %v", len(m.groups.groups), m.groups.loading)
	}
	if !m.topics.loading {
		t.Error("the groups result changed the topics tab")
	}
}

func TestAutoRefreshReloadsAndReschedules(t *testing.T) {
	_, cmd := loadedDashboard(t).Update(autoRefreshMsg{})
	if cmd == nil {
		t.Fatal("auto refresh produced no command")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) != 4 {
		t.Fatalf("cmd() = %T (len %d), want a batch of brokers, topics, groups and the next tick", cmd(), len(batch))
	}
}

func TestSlashOpensTheFilterOnlyOnTheTopicList(t *testing.T) {
	m := loadedDashboard(t)
	m = update(t, m, runes("1"))
	if m = update(t, m, runes("/")); m.filtering {
		t.Fatal("/ on the brokers tab opened the topic filter")
	}
	m = update(t, m, runes("2"))
	if m = update(t, m, runes("/")); !m.filtering {
		t.Fatal("/ on the topic list did not open the filter")
	}
	for _, k := range []tea.KeyMsg{runes("pay"), runes("q"), runes("1")} {
		m = update(t, m, k)
	}
	if m.filterBuf != "payq1" || m.quitting || m.active != tabTopics {
		t.Fatalf("while filtering, keys are text: buf %q quitting %v active %d", m.filterBuf, m.quitting, m.active)
	}
	view := plain(m.View())
	mustContain(t, view, "/ payq1", "type to filter", "esc clear filter")

	m = update(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.filtering || m.topics.filter != "pay" {
		t.Fatalf("after enter: filtering %v, filter %q; want the filter pay applied", m.filtering, m.topics.filter)
	}
	if got := m.topics.visibleCount(); got != 1 {
		t.Errorf("filter pay shows %d topics, want 1", got)
	}
	if sel := m.topics.selectedTopic(); sel == nil || sel.Name != "payments" {
		t.Errorf("selected topic = %v, want payments", sel)
	}
	mustContain(t, plain(m.View()), "filter: pay", "payments")

	// The topic detail has its own keys: / does nothing there.
	m.topics.view = topicsDetail
	if m = update(t, m, runes("/")); m.filtering {
		t.Error("/ on the topic detail opened the filter")
	}
	m.topics.view = topicsList

	m = update(t, m, runes("/"))
	if m.filterBuf != "pay" {
		t.Errorf("reopened filter holds %q, want the filter in force", m.filterBuf)
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.filtering || m.topics.filter != "" || m.topics.visibleCount() != 3 {
		t.Errorf("esc: filtering %v, filter %q, %d visible; want the filter cleared", m.filtering, m.topics.filter, m.topics.visibleCount())
	}
}

// In the produce input every key but ctrl+c is text: q, tab and the digits
// are typed, not acted on.
func TestProduceInputKeepsItsKeys(t *testing.T) {
	m := loadedDashboard(t)
	m = update(t, m, runes("4"))
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.produce.view != produceInput {
		t.Fatal("enter on the produce list did not open the input")
	}
	for _, k := range []tea.KeyMsg{runes("q1"), runes("2")} {
		m = update(t, m, k)
	}
	if m.quitting || m.active != tabProduce || m.produce.valueInput.Value() != "q12" {
		t.Fatalf("quitting %v, active %d, value %q; want q12 typed", m.quitting, m.active, m.produce.valueInput.Value())
	}
	m = update(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.active != tabProduce || m.produce.activeField != 0 {
		t.Errorf("tab in the input: active %d, field %d; want the key field", m.active, m.produce.activeField)
	}
	help := plain(renderHelp(m.helpBindings(), 200))
	if strings.Contains(help, "next tab") || !strings.Contains(help, "ctrl+c quit") {
		t.Errorf("the input's help offers the tab bar's keys, or not ctrl+c:\n%s", help)
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !next.(Model).quitting || cmd == nil {
		t.Error("ctrl+c in the produce input did not quit")
	}
}

func TestViewDrawsTabsBodyAndHelp(t *testing.T) {
	m := loadedDashboard(t)
	view := plain(m.View())
	mustContain(t, view, "Brokers", "Topics", "Groups", "Produce", "orders", "r refresh", "q quit")
	if lines := strings.Count(view, "\n") + 1; lines > 40 {
		t.Errorf("the view is %d lines in a 40-line terminal", lines)
	}

	for _, k := range []string{"1", "3", "4"} {
		m = update(t, m, runes(k))
		mustContain(t, plain(m.View()), "r refresh", "tab next tab")
	}

	m = update(t, m, tea.WindowSizeMsg{Width: 60, Height: 20})
	mustContain(t, plain(m.View()), "Terminal too small: 60x20")
}
