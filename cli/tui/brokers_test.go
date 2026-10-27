package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/client"
	tea "github.com/charmbracelet/bubbletea"
)

// The cursor on the last broker, and a refresh with one broker fewer: View
// indexed past the end and the dashboard died with the broker.
func TestBrokersRefreshWithFewerBrokers(t *testing.T) {
	brokers := func(n int) *client.ClusterInfo {
		info := &client.ClusterInfo{ClusterID: "c1", BrokerCount: client.IntOf(int64(n))}
		for i := range n {
			info.Brokers = append(info.Brokers, client.BrokerNode{ID: client.IntOf(int64(i)), Host: "kafka", Port: client.IntOf(9092)})
		}
		return info
	}
	m := brokersModel{width: 120, height: 40}
	m, _ = m.Update(brokersLoadedMsg{info: brokers(3)})
	m.cursor = 2

	m, _ = m.Update(brokersLoadedMsg{info: brokers(2)})
	if m.cursor != 1 {
		t.Errorf("cursor = %d after the refresh, want 1, the last broker", m.cursor)
	}
	if view := m.View(); !strings.Contains(view, "Broker 1") {
		t.Errorf("the view does not show broker 1:\n%s", view)
	}

	m, _ = m.Update(brokersLoadedMsg{info: brokers(0)})
	if m.cursor != 0 || !strings.Contains(m.View(), "No brokers found") {
		t.Errorf("cursor = %d with no brokers, want 0 and the empty view", m.cursor)
	}
}

// A failed load leaves info nil, and every key was dropped while it was: r
// could not retry. The cursor keys still need data.
func TestBrokersRefreshKeyWorksWithoutData(t *testing.T) {
	m := brokersModel{client: &client.Client{}, width: 120, height: 40}
	m, _ = m.Update(brokersLoadedMsg{err: errors.New("connection refused")})
	if m.info != nil || m.loading {
		t.Fatalf("after a failed load: info %v, loading %v", m.info, m.loading)
	}

	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("r with no data produced no load command")
	}
	if !m.loading {
		t.Error("r with no data did not put the view back to loading")
	}

	m.loading = false
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown}); cmd != nil || m.cursor != 0 {
		t.Errorf("down with no data: cmd %v, cursor %d; want nothing", cmd, m.cursor)
	}
}

// Broker 0 answered DescribeCluster; broker 1 leads the KRaft quorum.
func TestBrokersMarkTheKRaftLeader(t *testing.T) {
	info := &client.ClusterInfo{ClusterID: "c1", BrokerCount: client.IntOf(2), Controller: &client.BrokerNode{ID: client.IntOf(0)},
		Brokers: []client.BrokerNode{{ID: client.IntOf(0), Host: "kafka-0", Port: client.IntOf(9092)}, {ID: client.IntOf(1), Host: "kafka-1", Port: client.IntOf(9092)}}}
	check := &client.ClusterHealthReport{ControllerID: 0,
		KraftQuorum: &client.KraftQuorumHealth{LeaderID: 1, Voters: 3, HasLeader: true}}
	m := brokersModel{width: 120, height: 40}

	m, _ = m.Update(brokersLoadedMsg{info: info, check: check})
	view := m.View()
	if !strings.Contains(view, "Broker 1 · ? ★") || strings.Contains(view, "Broker 0 · ? ★") {
		t.Errorf("want the star on broker 1, the quorum leader, not broker 0:\n%s", view)
	}
	if !strings.Contains(view, "KRaft leader 1") || strings.Contains(view, "CONTROLLER") {
		t.Errorf("want broker 0's detail to name broker 1's quorum as the controller:\n%s", view)
	}

	m, _ = m.Update(brokersLoadedMsg{info: info})
	if view := m.View(); strings.Contains(view, "★") || !strings.Contains(view, "unknown (cluster check failed)") {
		t.Errorf("without a check the controller is unknown, and no broker is starred:\n%s", view)
	}
}

func TestControllerTextTellsAnUnknownLeaderFromNone(t *testing.T) {
	for issue, want := range map[string]string{
		"NO_LEADER":                     "✖ no KRaft leader",
		"UNAVAILABLE: TimeoutException": "⚠ KRaft leader unknown",
	} {
		check := &client.ClusterHealthReport{KraftQuorum: &client.KraftQuorumHealth{LeaderID: -1, Issue: issue}}
		if got := controllerText(check); !strings.Contains(got, want) {
			t.Errorf("issue %q: got %q, want %q", issue, got, want)
		}
	}
}
