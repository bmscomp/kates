package cmd

import (
	"strings"
	"testing"
)

func TestBuiltinScenarios_AllReadable(t *testing.T) {
	for _, s := range builtinScenarios {
		data, err := scenarioFS.ReadFile("scenarios/" + s.filename)
		if err != nil {
			t.Errorf("failed to read embedded scenario %s: %v", s.filename, err)
			continue
		}
		if len(data) == 0 {
			t.Errorf("embedded scenario %s is empty", s.filename)
		}
	}
}

func TestBuiltinScenarios_AllContainScenarioKey(t *testing.T) {
	for _, s := range builtinScenarios {
		data, _ := scenarioFS.ReadFile("scenarios/" + s.filename)
		if !strings.Contains(string(data), "scenarios:") {
			t.Errorf("scenario %s missing 'scenarios:' key", s.filename)
		}
	}
}

func TestBuiltinScenarios_AllContainTypeField(t *testing.T) {
	for _, s := range builtinScenarios {
		data, _ := scenarioFS.ReadFile("scenarios/" + s.filename)
		if !strings.Contains(string(data), "type:") {
			t.Errorf("scenario %s missing 'type:' field", s.filename)
		}
	}
}

func TestBuiltinScenarios_AllContainNameField(t *testing.T) {
	for _, s := range builtinScenarios {
		data, _ := scenarioFS.ReadFile("scenarios/" + s.filename)
		if !strings.Contains(string(data), "name:") {
			t.Errorf("scenario %s missing 'name:' field", s.filename)
		}
	}
}

func TestBuiltinScenarios_AllContainValidate(t *testing.T) {
	for _, s := range builtinScenarios {
		data, _ := scenarioFS.ReadFile("scenarios/" + s.filename)
		if !strings.Contains(string(data), "validate:") {
			t.Errorf("scenario %s missing 'validate:' section", s.filename)
		}
	}
}

func TestBuiltinScenarios_EnduranceHasLongDuration(t *testing.T) {
	data, err := scenarioFS.ReadFile("scenarios/endurance-soak.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "3600") {
		t.Error("endurance-soak should have 1-hour duration (3600)")
	}
}

func TestBuiltinScenarios_ExactlyOnceHasIntegrityFlags(t *testing.T) {
	data, err := scenarioFS.ReadFile("scenarios/exactly-once.yaml")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, field := range []string{"enableIdempotence", "enableTransactions", "enableCrc", "maxDataLossPercent"} {
		if !strings.Contains(content, field) {
			t.Errorf("exactly-once scenario missing field: %s", field)
		}
	}
}

func TestFindScenario_Found(t *testing.T) {
	meta := findScenario("quick-load")
	if meta == nil {
		t.Fatal("expected to find quick-load scenario")
	}
	if meta.testType != "LOAD" {
		t.Errorf("expected LOAD type, got %s", meta.testType)
	}
}

func TestFindScenario_NotFound(t *testing.T) {
	meta := findScenario("nonexistent")
	if meta != nil {
		t.Error("expected nil for unknown scenario")
	}
}

func TestFindScenario_WithExtension(t *testing.T) {
	meta := findScenario("ci-gate.yaml")
	if meta == nil {
		t.Fatal("expected to find ci-gate.yaml scenario")
	}
}

func TestBuiltinScenarios_MetadataComplete(t *testing.T) {
	for _, s := range builtinScenarios {
		if s.name == "" {
			t.Error("scenario has empty name")
		}
		if s.filename == "" {
			t.Error("scenario has empty filename")
		}
		if s.testType == "" {
			t.Errorf("scenario %s has empty testType", s.name)
		}
		if s.description == "" {
			t.Errorf("scenario %s has empty description", s.name)
		}
	}
}
