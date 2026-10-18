package tui

import (
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/client"
)

// The cursor on the last broker, and a refresh with one broker fewer: View
// indexed past the end and the dashboard died with the broker.
func TestBrokersRefreshWithFewerBrokers(t *testing.T) {
	brokers := func(n int) *client.ClusterInfo {
		info := &client.ClusterInfo{ClusterID: "c1", BrokerCount: n}
		for i := range n {
			info.Brokers = append(info.Brokers, client.BrokerNode{ID: i, Host: "kafka", Port: 9092})
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
