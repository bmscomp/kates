package cmd

import (
	"encoding/json"
	"testing"
)

func TestDryRunSerialization(t *testing.T) {
	payload := map[string]interface{}{
		"type":    "LOAD",
		"records": 1000,
		"nested":  map[string]string{"key": "value"},
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatal("dry-run serialization failed:", err)
	}

	str := string(data)
	if str == "" {
		t.Error("serialized payload should not be empty")
	}
	if len(str) < 10 {
		t.Error("serialized payload too short")
	}
}

func TestDryRunSerialization_Nil(t *testing.T) {
	data, err := json.MarshalIndent(nil, "", "  ")
	if err != nil {
		t.Fatal("nil serialization failed:", err)
	}
	if string(data) != "null" {
		t.Errorf("nil should serialize to 'null', got %s", string(data))
	}
}
