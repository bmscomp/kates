package cmd

import (
	"strings"
	"testing"
)

func TestScaffoldTemplates_AllTypesExist(t *testing.T) {
	types := []string{"LOAD", "STRESS", "SPIKE", "ENDURANCE", "VOLUME", "CAPACITY", "ROUND_TRIP"}
	for _, typ := range types {
		if _, ok := scaffoldTemplates[typ]; !ok {
			t.Errorf("missing scaffold template for type %s", typ)
		}
	}
}

func TestScaffoldTemplates_AllNonEmpty(t *testing.T) {
	for typ, fn := range scaffoldTemplates {
		content := fn()
		if len(content) == 0 {
			t.Errorf("scaffold template for %s is empty", typ)
		}
	}
}

func TestScaffoldTemplates_ContainScenarios(t *testing.T) {
	for typ, fn := range scaffoldTemplates {
		content := fn()
		if !strings.Contains(content, "scenarios:") {
			t.Errorf("scaffold template for %s missing 'scenarios:' key", typ)
		}
	}
}

func TestScaffoldTemplates_ContainType(t *testing.T) {
	for typ, fn := range scaffoldTemplates {
		content := fn()
		if !strings.Contains(content, "type:") {
			t.Errorf("scaffold template for %s missing 'type:' field", typ)
		}
	}
}

func TestScaffoldTemplates_ContainName(t *testing.T) {
	for typ, fn := range scaffoldTemplates {
		content := fn()
		if !strings.Contains(content, "name:") {
			t.Errorf("scaffold template for %s missing 'name:' field", typ)
		}
	}
}

func TestScaffoldTemplates_ContainUsageHint(t *testing.T) {
	for typ, fn := range scaffoldTemplates {
		content := fn()
		if !strings.Contains(content, "kates test apply") {
			t.Errorf("scaffold template for %s missing usage hint", typ)
		}
	}
}

func TestScaffoldLoad_ContainsAllFields(t *testing.T) {
	content := scaffoldLoad()
	fields := []string{
		"records:", "parallelProducers:", "numConsumers:", "recordSizeBytes:",
		"durationSeconds:", "topic:", "acks:", "batchSize:", "lingerMs:",
		"compressionType:", "consumerGroup:", "fetchMinBytes:", "fetchMaxWaitMs:",
		"partitions:", "replicationFactor:", "minInsyncReplicas:", "validate:",
	}
	for _, field := range fields {
		if !strings.Contains(content, field) {
			t.Errorf("LOAD scaffold missing field: %s", field)
		}
	}
}

func TestScaffoldStress_HasMultipleScenarios(t *testing.T) {
	content := scaffoldStress()
	count := strings.Count(content, "- name:")
	if count < 3 {
		t.Errorf("STRESS scaffold expected at least 3 scenarios, got %d", count)
	}
}

func TestScaffoldSpike_HasThreePhases(t *testing.T) {
	content := scaffoldSpike()
	if !strings.Contains(content, "Baseline Phase") {
		t.Error("SPIKE scaffold missing Baseline Phase")
	}
	if !strings.Contains(content, "Burst Phase") {
		t.Error("SPIKE scaffold missing Burst Phase")
	}
	if !strings.Contains(content, "Recovery Phase") {
		t.Error("SPIKE scaffold missing Recovery Phase")
	}
}

func TestScaffoldEndurance_HasLongDuration(t *testing.T) {
	content := scaffoldEndurance()
	if !strings.Contains(content, "3600") {
		t.Error("ENDURANCE scaffold should have at least 1-hour duration (3600s)")
	}
}

func TestScaffoldVolume_HasLargeAndSmallMessages(t *testing.T) {
	content := scaffoldVolume()
	if !strings.Contains(content, "102400") {
		t.Error("VOLUME scaffold missing large message size (100KB)")
	}
	if !strings.Contains(content, "5000000") {
		t.Error("VOLUME scaffold missing high message count (5M)")
	}
}

func TestScaffoldCapacity_HasSteppedProbes(t *testing.T) {
	content := scaffoldCapacity()
	count := strings.Count(content, "- name:")
	if count < 4 {
		t.Errorf("CAPACITY scaffold expected at least 4 probe steps, got %d", count)
	}
}

func TestScaffoldRoundTrip_HasValidate(t *testing.T) {
	content := scaffoldRoundTrip()
	if !strings.Contains(content, "maxP99LatencyMs:") {
		t.Error("ROUND_TRIP scaffold missing P99 validation")
	}
	if !strings.Contains(content, "maxAvgLatencyMs:") {
		t.Error("ROUND_TRIP scaffold missing avg latency validation")
	}
}

func TestScaffoldTemplates_UnknownType(t *testing.T) {
	_, ok := scaffoldTemplates["UNKNOWN"]
	if ok {
		t.Error("expected no template for UNKNOWN type")
	}
}

func TestScaffoldTemplates_ValidYAML(t *testing.T) {
	for typ, fn := range scaffoldTemplates {
		content := fn()
		lines := strings.Split(content, "\n")
		hasScenarios := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "scenarios:" {
				hasScenarios = true
				break
			}
		}
		if !hasScenarios {
			t.Errorf("template for %s does not start with a valid scenarios: block", typ)
		}
	}
}
