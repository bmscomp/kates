package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/client"
	tea "github.com/charmbracelet/bubbletea"
)

func loadedTopics(t *testing.T) topicsModel {
	t.Helper()
	m := newTopicsModel(newKafkaStub(t))
	m.width, m.height = 120, 40
	mustContain(t, plain(m.View()), "Loading topics")
	m, _ = m.Update(m.Init()())
	if len(m.topics) != 3 || m.loading {
		t.Fatalf("after the load: %d topics, loading %v", len(m.topics), m.loading)
	}
	return m
}

func TestTopicsListMarksHealthAndTheCursor(t *testing.T) {
	m := loadedTopics(t)
	g := glyphs()
	view := plain(m.View())
	mustContain(t, view, "Topics (3)", g.Arrow+" "+g.Check+" orders", g.Warn+" payments", "(internal)", "enter topic detail")

	for range 5 {
		m, _ = m.Update(runes("j"))
	}
	if m.cursor != 2 {
		t.Errorf("down past the end: cursor %d", m.cursor)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.cursor != 1 {
		t.Errorf("up: cursor %d", m.cursor)
	}
	m, cmd := m.Update(runes("r"))
	if cmd == nil || !m.loading {
		t.Error("r did not reload")
	}
}

func TestTopicsListScrollsWithTheCursor(t *testing.T) {
	m := topicsModel{width: 120, height: 14} // 6 rows
	for i := range 30 {
		m.topics = append(m.topics, client.KafkaTopic{Name: fmt.Sprintf("topic-%02d", i)})
	}
	m.cursor = 20
	view := plain(m.viewList())
	if !strings.Contains(view, "topic-20") || strings.Contains(view, "topic-14") || !strings.Contains(view, "topic-15") {
		t.Errorf("cursor on topic-20 in 6 rows: want topic-15..20:\n%s", view)
	}
}

func TestTopicsDetailConsumeAndBack(t *testing.T) {
	m := loadedTopics(t)
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter loaded no detail")
	}
	m, _ = m.Update(cmd())
	if m.view != topicsDetail {
		t.Fatalf("view %d after the detail load", m.view)
	}
	mustContain(t, plain(m.View()), "Topic: orders", "retention.ms", "Partitions (2)", "c consume", "esc/backspace back to the list")

	m, cmd = m.Update(runes("c"))
	if m.view != topicsConsume || cmd == nil {
		t.Fatalf("c: view %d, cmd %v", m.view, cmd)
	}
	mustContain(t, plain(m.View()), "Consuming: orders", "Waiting for records", "esc/backspace stop tailing")

	m, _ = m.Update(m.fetchRecords("orders")())
	view := plain(m.View())
	mustContain(t, view, "2 records", "k1", "(null)", "v2")

	// The tick polls again while tailing, and stops once it is not.
	if _, cmd := m.Update(consumeTickMsg{}); cmd == nil {
		t.Error("a tick while tailing did not poll")
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.view != topicsDetail || m.records != nil {
		t.Errorf("esc in consume: view %d, records %v; want the detail", m.view, m.records)
	}
	if _, cmd := m.Update(consumeTickMsg{}); cmd != nil {
		t.Error("a tick after tailing stopped still polled")
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if m.view != topicsList || m.detail != nil {
		t.Errorf("backspace in the detail: view %d", m.view)
	}
}

func TestTopicsConsumeKeepsTheLast100Records(t *testing.T) {
	m := topicsModel{view: topicsConsume, width: 120, height: 40}
	for i := range 12 {
		recs := make([]client.KafkaRecord, 10)
		for j := range recs {
			recs[j] = client.KafkaRecord{Offset: client.IntOf(int64(i*10 + j)), Value: strings.Repeat("x", 60)}
		}
		m, _ = m.Update(consumeResultMsg{records: recs})
	}
	if len(m.records) != 100 || m.records[0].Offset.Int() != 20 {
		t.Errorf("%d records, first offset %v; want the last 100", len(m.records), m.records[0].Offset)
	}
	if view := plain(m.viewConsume()); !strings.Contains(view, "...") {
		t.Errorf("a long value is not cut:\n%s", view)
	}
	before := len(m.records)
	m, _ = m.Update(consumeResultMsg{err: errors.New("timeout")})
	if len(m.records) != before {
		t.Error("a failed poll changed the records")
	}
}

func TestTopicsFailuresAndEmptyStates(t *testing.T) {
	m := topicsModel{width: 120, height: 40}
	m, _ = m.Update(topicsLoadedMsg{})
	mustContain(t, plain(m.View()), "No topics found.")
	if m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil || m.loading {
		t.Error("enter with no topics loaded a detail")
	}
	m, _ = m.Update(topicDetailMsg{err: errors.New("not found")})
	if m.view != topicsList {
		t.Error("a failed detail load opened the detail")
	}
	mustContain(t, plain(m.View()), "Error: not found")
	if got := plain(topicsModel{}.viewDetail()); !strings.Contains(got, "No detail available.") {
		t.Errorf("detail without data: %q", got)
	}
}
