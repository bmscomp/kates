package cmd

import (
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
)

func TestClusterInfoCmd_JSON(t *testing.T) {
	mockResponse := `{
		"clusterId": "test-cluster-123",
		"controller": {"id": 1, "host": "broker-1", "port": 9092, "rack": "us-east-1a"},
		"brokers": [
			{"id": 1, "host": "broker-1", "port": 9092, "rack": "us-east-1a"},
			{"id": 2, "host": "broker-2", "port": 9092, "rack": "us-east-1b"}
		],
		"brokerCount": 2
	}`
	ts, buf := setupTest(t, "GET", "/api/cluster/info", 200, mockResponse)
	defer ts.Close()

	outputMode = "json" // Override setupTest for this specific test
	err := clusterInfoCmd.RunE(clusterInfoCmd, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "test-cluster-123") {
		t.Errorf("Expected JSON output to contain clusterId, got:\n%s", out)
	}
}

// On KRaft the check's controllerId is whichever broker answered
// DescribeCluster; the active controller is the metadata quorum's leader.
func TestClusterCheckCmdNamesTheKRaftLeader(t *testing.T) {
	ts, buf := setupTest(t, "GET", "/api/cluster/check", 200, `{
		"clusterId": "c1", "brokers": 3, "controllerId": 4, "topics": 2, "partitions": 6,
		"consumerGroups": 1, "partitionHealth": {"underReplicated": 0, "offline": 0},
		"kraftQuorum": {"leaderId": 1, "voters": 3, "observers": 0, "hasLeader": true},
		"status": "HEALTHY"}`)
	defer ts.Close()

	if err := clusterCheckCmd.RunE(clusterCheckCmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stripAnsi(buf.String())
	if !tableHasRow(out, []string{"Controller", "KRaft leader 1"}) {
		t.Errorf("want the quorum leader as the controller:\n%s", out)
	}
	if strings.Contains(out, "Broker 4") {
		t.Errorf("broker 4 only answered DescribeCluster:\n%s", out)
	}
}

func TestControllerLabel(t *testing.T) {
	cases := []struct {
		name   string
		report client.ClusterHealthReport
		want   string
	}{
		{"ZooKeeper", client.ClusterHealthReport{ControllerID: 4}, "Broker 4"},
		{"KRaft", client.ClusterHealthReport{ControllerID: 4,
			KraftQuorum: &client.KraftQuorumHealth{LeaderID: 1, Voters: 3, HasLeader: true}}, "KRaft leader 1"},
		{"KRaft without a leader", client.ClusterHealthReport{ControllerID: 4,
			KraftQuorum: &client.KraftQuorumHealth{LeaderID: -1, Voters: 3, Issue: "NO_LEADER"}}, output.Glyphs().Cross + " no KRaft leader"},
		{"KRaft quorum not described", client.ClusterHealthReport{ControllerID: 4,
			KraftQuorum: &client.KraftQuorumHealth{LeaderID: -1, Issue: "UNAVAILABLE: TimeoutException"}}, output.Glyphs().Warn + " KRaft leader unknown"},
	}
	for _, tc := range cases {
		if got := stripAnsi(controllerLabel(&tc.report)); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Broker 1 answered DescribeCluster; broker 2 leads the KRaft quorum.
const (
	describedClusterInfo = `{
		"clusterId": "test-cluster-123",
		"controller": {"id": 1, "host": "broker-1", "port": 9092, "rack": "us-east-1a"},
		"brokers": [
			{"id": 1, "host": "broker-1", "port": 9092, "rack": "us-east-1a"},
			{"id": 2, "host": "broker-2", "port": 9092, "rack": "us-east-1b"}
		],
		"brokerCount": 2
	}`
	leaderTwoCheck = `{
		"clusterId": "test-cluster-123", "brokers": 2, "controllerId": 1,
		"partitionHealth": {"underReplicated": 0, "offline": 0},
		"kraftQuorum": {"leaderId": 2, "voters": 2, "observers": 0, "hasLeader": true},
		"status": "HEALTHY"}`
)

func TestClusterInfoCmd_Table(t *testing.T) {
	ts, buf := setupRoutes(t, map[string]string{
		"/api/cluster/info":  describedClusterInfo,
		"/api/cluster/check": leaderTwoCheck,
	})
	defer ts.Close()

	err := clusterInfoCmd.RunE(clusterInfoCmd, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stripAnsi(buf.String())
	if !strings.Contains(out, "Cluster ID: test-cluster-123") {
		t.Errorf("missing cluster ID: %s", out)
	}
	if !strings.Contains(out, "Broker Count") {
		t.Errorf("missing broker count: %s", out)
	}
	if !strings.Contains(out, "us-east-1a") {
		t.Errorf("missing rack info: %s", out)
	}
	if !tableHasRow(out, []string{"Controller", "KRaft leader 2"}) {
		t.Errorf("want the quorum leader as the controller:\n%s", out)
	}
	if !tableHasRow(out, []string{"broker-2", "us-east-1b", "★"}) || tableHasRow(out, []string{"broker-1", "★"}) {
		t.Errorf("want the star on broker 2, the quorum leader, not broker 1:\n%s", out)
	}
}

func TestClusterInfoCmdWithoutACheck(t *testing.T) {
	ts, buf := setupRoutes(t, map[string]string{"/api/cluster/info": describedClusterInfo})
	defer ts.Close()

	if err := clusterInfoCmd.RunE(clusterInfoCmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stripAnsi(buf.String())
	if !tableHasRow(out, []string{"Controller", "unknown (cluster check failed)"}) || strings.Contains(out, "★") {
		t.Errorf("without a check the controller is unknown, and no broker is starred:\n%s", out)
	}
}

func TestClusterTopicsCmd_Empty(t *testing.T) {
	ts, buf := setupTest(t, "GET", "/api/cluster/topics", 200, `{"items":[],"page":0,"size":50,"total":0,"count":0}`)
	defer ts.Close()

	err := clusterTopicsCmd.RunE(clusterTopicsCmd, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stripAnsi(buf.String())
	if !strings.Contains(out, "No topics found") {
		t.Errorf("expected empty topic message, got: %s", out)
	}
}

func TestClusterTopicsCmd_List(t *testing.T) {
	ts, buf := setupTest(t, "GET", "/api/cluster/topics", 200, `{"items":["topic-a","topic-b"],"page":0,"size":50,"total":2,"count":2}`)
	defer ts.Close()

	err := clusterTopicsCmd.RunE(clusterTopicsCmd, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stripAnsi(buf.String())
	if !strings.Contains(out, "topic-a") || !strings.Contains(out, "topic-b") {
		t.Errorf("missing topics from output: %s", out)
	}
}

func TestClusterTopicDescribeCmd(t *testing.T) {
	mockResponse := `{
		"name": "my-topic",
		"internal": false,
		"partitions": 3,
		"replicationFactor": 2,
		"configs": {
			"retention.ms": "86400000",
			"cleanup.policy": "delete"
		},
		"configSources": {
			"retention.ms": "DYNAMIC_TOPIC_CONFIG",
			"cleanup.policy": "STATIC_BROKER_CONFIG"
		},
		"partitionInfo": [
			{"partition": 0, "leader": 1, "replicas": [1, 2], "isrs": [1, 2], "underReplicated": false},
			{"partition": 1, "leader": 2, "replicas": [2, 3], "isrs": [2], "underReplicated": true}
		]
	}`
	ts, buf := setupTest(t, "GET", "/api/cluster/topics/my-topic", 200, mockResponse)
	defer ts.Close()

	err := clusterTopicDescribeCmd.RunE(clusterTopicDescribeCmd, []string{"my-topic"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stripAnsi(buf.String())
	if !strings.Contains(out, "Topic: my-topic") {
		t.Errorf("missing topic header: %s", out)
	}
	if !strings.Contains(out, "retention.ms") {
		t.Errorf("missing config keys: %s", out)
	}
	if !strings.Contains(out, "Source") || !strings.Contains(out, "STATIC_BROKER_CONFIG") {
		t.Errorf("missing where each config value comes from: %s", out)
	}
	if !strings.Contains(out, "YES") {
		t.Errorf("missing under-replicated warning: %s", out)
	}
}

func TestClusterBrokerConfigsCmd(t *testing.T) {
	mockResponse := `[
		{"name": "log.dirs", "value": "/var/lib/kafka/data", "readOnly": true, "source": "STATIC_BROKER_CONFIG"},
		{"name": "min.insync.replicas", "value": "2", "readOnly": false, "source": "DYNAMIC_BROKER_CONFIG"}
	]`
	ts, buf := setupTest(t, "GET", "/api/cluster/brokers/1/configs", 200, mockResponse)
	defer ts.Close()

	err := clusterBrokerConfigsCmd.RunE(clusterBrokerConfigsCmd, []string{"1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stripAnsi(buf.String())
	if !strings.Contains(out, "Broker 1 — Configuration") {
		t.Errorf("missing broker header: %s", out)
	}
	if !strings.Contains(out, "log.dirs") || !strings.Contains(out, "STATIC_BROKER_CONFIG") {
		t.Errorf("missing static config: %s", out)
	}
}
