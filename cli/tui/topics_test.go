package tui

import (
	"strings"
	"testing"
)

// TestTopicDetailShowsConfigSources: the backend reports each config's value
// in force, a broker-level one included, so the detail says where each comes
// from, as the CLI's topic tables do; an older backend reports no sources.
func TestTopicDetailShowsConfigSources(t *testing.T) {
	detail := map[string]interface{}{
		"name":              "orders",
		"partitions":        3,
		"replicationFactor": 3,
		"configs": map[string]interface{}{
			"min.insync.replicas": "2",
			"retention.ms":        "3600000",
		},
		"configSources": map[string]interface{}{
			"min.insync.replicas": "STATIC_BROKER_CONFIG",
			"retention.ms":        "DYNAMIC_TOPIC_CONFIG",
		},
	}
	m := topicsModel{detail: detail, width: 160, height: 40}
	view := m.viewDetail()
	for key, source := range map[string]string{
		"min.insync.replicas": "STATIC_BROKER_CONFIG",
		"retention.ms":        "DYNAMIC_TOPIC_CONFIG",
	} {
		if line := topicDetailLine(view, key); !strings.Contains(line, source) {
			t.Errorf("%s line %q lacks its source %s", key, line, source)
		}
	}

	delete(detail, "configSources")
	view = topicsModel{detail: detail, width: 160, height: 40}.viewDetail()
	if line := topicDetailLine(view, "min.insync.replicas"); line == "" || strings.Contains(line, "CONFIG") {
		t.Errorf("without sources the line is %q, want the value alone", line)
	}
}

func topicDetailLine(view, key string) string {
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, key) {
			return line
		}
	}
	return ""
}
