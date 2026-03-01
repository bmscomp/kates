package cmd

import (
	"strings"
	"testing"
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

func TestClusterInfoCmd_Table(t *testing.T) {
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
	// Broker 1 is controller, should have star
	if !strings.Contains(out, "★") {
		t.Errorf("missing controller star indicator: %s", out)
	}
}

func TestClusterTopicsCmd_Empty(t *testing.T) {
	ts, buf := setupTest(t, "GET", "/api/cluster/topics", 200, `[]`)
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
	ts, buf := setupTest(t, "GET", "/api/cluster/topics", 200, `["topic-a", "topic-b"]`)
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
