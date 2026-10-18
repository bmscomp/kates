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

// Broker 0 answered DescribeCluster; broker 1 leads the KRaft quorum.
func TestBrokersMarkTheKRaftLeader(t *testing.T) {
	info := &client.ClusterInfo{ClusterID: "c1", BrokerCount: 2, Controller: &client.BrokerNode{ID: 0},
		Brokers: []client.BrokerNode{{ID: 0, Host: "kafka-0", Port: 9092}, {ID: 1, Host: "kafka-1", Port: 9092}}}
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
