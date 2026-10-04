package cmd

import (
	"strings"
	"testing"
)

// TestKafkaTopicCmdShowsConfigSources: topic detail reports the values in
// force, broker-level ones included, so the table says where each comes from.
func TestKafkaTopicCmdShowsConfigSources(t *testing.T) {
	mockResponse := `{
		"name": "orders",
		"internal": false,
		"partitions": 1,
		"replicationFactor": 3,
		"configs": {"min.insync.replicas": "2", "retention.ms": "600000"},
		"configSources": {"min.insync.replicas": "STATIC_BROKER_CONFIG", "retention.ms": "DYNAMIC_TOPIC_CONFIG"},
		"partitionInfo": [{"partition": 0, "leader": 0, "replicas": [0, 1, 2], "isr": [0, 1, 2], "underReplicated": false}]
	}`
	ts, buf := setupTest(t, "GET", "/api/kafka/topics/orders", 200, mockResponse)
	defer ts.Close()

	if err := kafkaTopicCmd.RunE(kafkaTopicCmd, []string{"orders"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stripAnsi(buf.String())
	for _, want := range []string{"Source", "min.insync.replicas", "STATIC_BROKER_CONFIG", "DYNAMIC_TOPIC_CONFIG"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q: %s", want, out)
		}
	}
}

// TestKafkaTopicCmdWithoutConfigSources: an older backend reports no sources,
// and the table keeps its two columns.
func TestKafkaTopicCmdWithoutConfigSources(t *testing.T) {
	mockResponse := `{
		"name": "orders",
		"partitions": 1,
		"replicationFactor": 1,
		"configs": {"retention.ms": "600000"},
		"partitionInfo": []
	}`
	ts, buf := setupTest(t, "GET", "/api/kafka/topics/orders", 200, mockResponse)
	defer ts.Close()

	if err := kafkaTopicCmd.RunE(kafkaTopicCmd, []string{"orders"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stripAnsi(buf.String())
	if !strings.Contains(out, "retention.ms") || strings.Contains(out, "Source") {
		t.Errorf("want the config without a Source column: %s", out)
	}
}
