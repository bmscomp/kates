package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/client"
	tea "github.com/charmbracelet/bubbletea"
)

func loadedProduce(t *testing.T) produceModel {
	t.Helper()
	m := newProduceModel(newKafkaStub(t))
	m.width, m.height = 120, 40
	mustContain(t, plain(m.View()), "Loading topics")
	m, _ = m.Update(m.Init()())
	if len(m.topics) != 3 || m.err != nil {
		t.Fatalf("after the load: %d topics, err %v", len(m.topics), m.err)
	}
	return m
}

func TestProduceTopicSelect(t *testing.T) {
	m := loadedProduce(t)
	view := plain(m.View())
	mustContain(t, view, "Select Topic (3)", glyphs().Arrow+" orders", "(internal)", "enter produce to topic")

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.cursor != 0 {
		t.Errorf("up at the top: cursor %d", m.cursor)
	}
	for range 5 {
		m, _ = m.Update(runes("j"))
	}
	if m.cursor != 2 {
		t.Errorf("down past the end: cursor %d", m.cursor)
	}
	m, _ = m.Update(runes("k"))

	m, cmd := m.Update(runes("r"))
	if cmd == nil || !m.loading {
		t.Fatal("r did not reload the topics")
	}
	m, _ = m.Update(cmd())

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != produceInput || m.activeField != 1 {
		t.Fatalf("enter: view %d, field %d; want the value input", m.view, m.activeField)
	}
	mustContain(t, plain(m.View()), "Produce "+glyphs().To+" payments", "tab switch field", "enter send", "esc back to the topic list")
}

func TestProduceInputSendsARecord(t *testing.T) {
	m := loadedProduce(t)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	// An empty value is refused.
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || !strings.Contains(plain(m.statusMsg), "Value cannot be empty") {
		t.Fatalf("enter with no value: cmd %v, status %q", cmd, m.statusMsg)
	}

	// Typing goes to the value field at once; tab moves to the key.
	m, _ = m.Update(runes("héllo"))
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m, _ = m.Update(runes("k1"))
	if m.valueInput.Value() != "héllo" || m.keyInput.Value() != "k1" {
		t.Fatalf("key %q value %q, want k1 and héllo", m.keyInput.Value(), m.valueInput.Value())
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.activeField != 1 || !m.valueInput.Focused() || m.keyInput.Focused() {
		t.Error("a second tab did not go back to the value")
	}

	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !m.loading {
		t.Fatal("enter with a value sent nothing")
	}
	mustContain(t, plain(m.statusMsg), "Sending")
	sent, ok := cmd().(produceSentMsg)
	if !ok || sent.err != nil {
		t.Fatalf("send returned %#v", sent)
	}
	m, _ = m.Update(sent)
	mustContain(t, plain(m.statusMsg), glyphs().Check+" Produced to orders", "partition 2, offset 42")
	if len(m.history) != 1 || m.history[0].Key != "k1" || m.valueInput.Value() != "" {
		t.Errorf("history %v, value %q; want one record and the value cleared", m.history, m.valueInput.Value())
	}

	m, _ = m.Update(produceSentMsg{err: errors.New("topic is read-only")})
	mustContain(t, plain(m.statusMsg), glyphs().Cross+" topic is read-only")
	if len(m.history) != 1 {
		t.Error("a failed send was added to the history")
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.view != produceTopicSelect || m.keyInput.Value() != "" || m.statusMsg != "" {
		t.Errorf("esc: view %d, key %q, status %q; want the list, cleared", m.view, m.keyInput.Value(), m.statusMsg)
	}
	mustContain(t, plain(m.View()), "History (1 sent)", "p2 @42")
}

func TestProduceHistoryKeepsTheLast50(t *testing.T) {
	m := produceModel{topics: []client.KafkaTopic{{Name: "orders"}}, width: 120, height: 40}
	for i := range 60 {
		m, _ = m.Update(produceSentMsg{topic: "orders", partition: "0", offset: fmt.Sprint(i)})
	}
	if len(m.history) != 50 || m.history[0].Offset != "10" {
		t.Errorf("history holds %d, first offset %s; want the last 50", len(m.history), m.history[0].Offset)
	}
	view := plain(m.viewTopicSelect())
	if !strings.Contains(view, "@59") || strings.Contains(view, "@54") {
		t.Errorf("the pane shows the last five sends:\n%s", view)
	}
}

func TestProduceWithoutTopics(t *testing.T) {
	m := newProduceModel(nil)
	m.width, m.height, m.loading = 120, 40, false
	mustContain(t, plain(m.View()), "No topics found. Press r to refresh.")
	if m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil || m.view != produceTopicSelect {
		t.Error("enter with no topics opened the input")
	}
	// An input left open on a list that has since emptied refuses to send
	// rather than index past the end.
	m.view = produceInput
	m.activeField = 1
	m.valueInput.Focus()
	m.valueInput.SetValue("v")
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || !strings.Contains(m.statusMsg, "No topic selected") {
		t.Errorf("enter with no topic: cmd %v, status %q", cmd, m.statusMsg)
	}
	m, _ = m.Update(produceTopicsMsg{err: errors.New("connection refused")})
	mustContain(t, plain(m.View()), "Error: connection refused")
}
