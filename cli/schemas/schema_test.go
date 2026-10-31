package schemas

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"gopkg.in/yaml.v3"
)

var allSchemaNames = []string{
	"scenario.v1.schema.json",
	"test-request.v1.schema.json",
	"flow.v1.schema.json",
	"resilience.v1.schema.json",
	"disruption.v1.schema.json",
	"kafkareference.v1alpha1.schema.json",
}

func resolveAllSchemas(t *testing.T) map[string]*jsonschema.Resolved {
	t.Helper()
	resolved := map[string]*jsonschema.Resolved{}
	for _, name := range allSchemaNames {
		raw, err := Files.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		var s jsonschema.Schema
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatalf("unmarshal %s: %v", name, err)
		}
		r, err := s.Resolve(nil)
		if err != nil {
			t.Fatalf("resolve %s: %v", name, err)
		}
		resolved[name] = r
	}
	return resolved
}

func yamlBytesToJSONMap(t *testing.T, label string, raw []byte) any {
	t.Helper()
	var v any
	if err := yaml.Unmarshal(raw, &v); err != nil {
		t.Fatalf("yaml unmarshal %s: %v", label, err)
	}
	jb, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json marshal %s: %v", label, err)
	}
	var out any
	if err := json.Unmarshal(jb, &out); err != nil {
		t.Fatalf("json unmarshal %s: %v", label, err)
	}
	return out
}

func yamlFileToJSONMap(t *testing.T, path string) any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return yamlBytesToJSONMap(t, path, raw)
}

func TestAllShippedExamplesValidateAgainstSchemas(t *testing.T) {
	resolved := resolveAllSchemas(t)

	// 1. Scenario files
	scenarioFiles, _ := filepath.Glob("../cmd/scenarios/*.yaml")
	perfFiles, _ := filepath.Glob("../examples/perf-*.yaml")
	scenarioFiles = append(scenarioFiles, perfFiles...)
	scenarioFiles = append(scenarioFiles, "../examples/load-test.yaml")
	if len(scenarioFiles) < 15 {
		t.Fatalf("expected >= 15 scenario files, got %d", len(scenarioFiles))
	}
	for _, f := range scenarioFiles {
		v := yamlFileToJSONMap(t, f)
		if err := resolved["scenario.v1.schema.json"].Validate(v); err != nil {
			t.Errorf("scenario schema rejected %s: %v", f, err)
		}
	}

	// 2. Resilience files
	resilienceFiles, _ := filepath.Glob("../examples/resilience-*.yaml")
	if len(resilienceFiles) < 10 {
		t.Fatalf("expected >= 10 resilience files, got %d", len(resilienceFiles))
	}
	for _, f := range resilienceFiles {
		v := yamlFileToJSONMap(t, f)
		if err := resolved["resilience.v1.schema.json"].Validate(v); err != nil {
			t.Errorf("resilience schema rejected %s: %v", f, err)
		}
	}

	// 3. Disruption playbooks
	playbookFiles, _ := filepath.Glob("../../kates/src/main/resources/playbooks/*.yaml")
	if len(playbookFiles) < 6 {
		t.Fatalf("expected >= 6 playbook files, got %d", len(playbookFiles))
	}
	for _, f := range playbookFiles {
		v := yamlFileToJSONMap(t, f)
		if err := resolved["disruption.v1.schema.json"].Validate(v); err != nil {
			t.Errorf("disruption schema rejected %s: %v", f, err)
		}
	}

	// 4. Compliance reference files
	complianceFiles, _ := filepath.Glob("../examples/conformity/*.yaml")
	if len(complianceFiles) < 4 {
		t.Fatalf("expected >= 4 compliance files, got %d", len(complianceFiles))
	}
	for _, f := range complianceFiles {
		v := yamlFileToJSONMap(t, f)
		if err := resolved["kafkareference.v1alpha1.schema.json"].Validate(v); err != nil {
			t.Errorf("compliance schema rejected %s: %v", f, err)
		}
	}
}

func TestSchemasValidateAll6DialectPayloads(t *testing.T) {
	resolved := resolveAllSchemas(t)

	validDocs := []struct {
		name   string
		schema string
		yaml   string
	}{
		{
			name:   "dialect1-multi-scenario",
			schema: "scenario.v1.schema.json",
			yaml: `scenarios:
  - name: "Throughput & Latency"
    type: LOAD
    backend: native
    spec:
      topic: kates-load
      partitions: 12
      replicationFactor: 3
      minInsyncReplicas: 2
      records: 100000
      recordSizeBytes: 1024
      parallelProducers: 4
      acks: "all"
      compressionType: lz4
      enableIdempotence: true
    validate:
      maxP99LatencyMs: 80
      minThroughputRecPerSec: 10000
      maxErrorRate: 0.01
`,
		},
		{
			name:   "dialect2-multi-phase-test-request",
			schema: "test-request.v1.schema.json",
			yaml: `backend: native
scenario:
  name: "Multi-Phase Ramp"
  baseSpec:
    topic: kates-ramp
    partitions: 6
    replicationFactor: 3
    minInsyncReplicas: 2
    numRecords: 50000
    recordSize: 512
    acks: "all"
  phases:
    - name: warmup
      type: WARMUP
      durationMs: 10000
      specOverrides:
        throughput: 1000
    - name: ramp
      type: RAMP
      durationMs: 30000
      rampSteps: 4
      specOverrides:
        throughput: 10000
    - name: steady
      type: STEADY
      durationMs: 60000
    - name: cooldown
      type: COOLDOWN
      durationMs: 10000
  sla:
    p95LatencyMs: 40
    p99LatencyMs: 120
    minThroughputRecPerSec: 8000
    maxErrorRatePercent: 0.5
`,
		},
		{
			name:   "dialect2-single-test-request",
			schema: "test-request.v1.schema.json",
			yaml: `type: STRESS
backend: trogdor
spec:
  topic: kates-stress
  numRecords: 200000
  numProducers: 4
  durationMs: 60000
`,
		},
		{
			name:   "dialect3-flow-pipeline",
			schema: "flow.v1.schema.json",
			yaml: `name: "End-to-End Qualification Flow"
steps:
  - name: baseline-load
    action: test
    type: LOAD
    records: 50000
    spec:
      topic: kates-flow
      numRecords: 50000
      acks: "all"
    gate:
      minGrade: B
      maxP99: 100
      minThroughput: 5000
      maxErrorRate: 0.01
    onFail: stop
  - name: pause-before-chaos
    action: wait
    records: 5
  - name: stress-follow-up
    action: test
    type: STRESS
    records: 25000
    onFail: continue
`,
		},
		{
			name:   "dialect4-resilience-request",
			schema: "resilience.v1.schema.json",
			yaml: `testRequest:
  type: LOAD
  spec:
    topic: kates-chaos
    partitions: 6
    replicationFactor: 3
    minInsyncReplicas: 2
    numRecords: 100000
    acks: "all"
steadyStateSec: 15
maxRecoveryWaitSec: 120
chaosSpec:
  experimentName: kill-leader
  disruptionType: LEADER_ELECTION
  targetTopic: kates-chaos
  targetPartition: 0
  delayBeforeSec: 5
  chaosDurationSec: 30
probes:
  - name: urp-check
    type: kafkaProbe
    mode: Continuous
    command: under-replicated-partitions
    comparator: "=="
    expectedOutput: "0"
    intervalSec: 5
    timeoutSec: 5
`,
		},
		{
			name:   "dialect5-disruption-plan",
			schema: "disruption.v1.schema.json",
			yaml: `name: "Broker Failover Playbook"
description: "Validates leader failover and ISR recovery"
category: kafka
baselineDurationSec: 15
autoRollback: true
isrTrackingTopic: kates-events
lagTrackingGroupId: kates-cg
steps:
  - name: "Kill Partition Leader"
    steadyStateSec: 15
    observationWindowSec: 30
    faultSpec:
      experimentName: kill-leader
      disruptionType: LEADER_ELECTION
      targetTopic: kates-events
      chaosDurationSec: 30
sla:
  maxP99LatencyMs: 250
  maxErrorRate: 0.01
  maxDataLossPercent: 0
  maxRtoMs: 60000
`,
		},
		{
			name:   "dialect6-kafka-reference",
			schema: "kafkareference.v1alpha1.schema.json",
			yaml: `apiVersion: conformity.kates.io/v1alpha1
kind: KafkaReference
metadata:
  name: prod-3az
  description: "Production 3-AZ Strimzi baseline"
spec:
  versions:
    kafka:
      min: "3.9.0"
  brokers:
    count:
      min: 3
    storage:
      type: persistent-claim
      size:
        min: 500Gi
  kafka:
    rackAwareness: true
  waivers:
    - rule: kafka.rackAwareness
      reason: "Single-AZ staging lab"
      until: "2027-12-31"
      by: "platform-team@example.com"
`,
		},
	}

	for _, tc := range validDocs {
		t.Run(tc.name, func(t *testing.T) {
			v := yamlBytesToJSONMap(t, tc.name, []byte(tc.yaml))
			if err := resolved[tc.schema].Validate(v); err != nil {
				t.Errorf("%s rejected valid document: %v", tc.schema, err)
			}
		})
	}
}

func TestSchemasRejectUnknownAttributes(t *testing.T) {
	resolved := resolveAllSchemas(t)

	cases := []struct {
		schema string
		doc    string
	}{
		{
			"scenario.v1.schema.json",
			`{"type": "LOAD", "spec": {"records": 100, "nonExistentAttr": 1}}`,
		},
		{
			"test-request.v1.schema.json",
			`{"scenario": {"name": "mp", "phases": [{"phaseType": "RAMP", "unknownPhaseField": 5}]}}`,
		},
		{
			"flow.v1.schema.json",
			`{"name": "p", "steps": [{"action": "test", "bogusKey": true}]}`,
		},
		{
			"resilience.v1.schema.json",
			`{"testRequest": {"type": "LOAD"}, "chaosSpec": {"experimentName": "x", "unknownFaultField": 1}}`,
		},
		{
			"disruption.v1.schema.json",
			`{"name": "d", "steps": [{"name": "s1", "faultSpec": {"experimentName": "x"}, "unknownStepAttr": 1}]}`,
		},
		{
			"kafkareference.v1alpha1.schema.json",
			`{"apiVersion": "conformity.kates.io/v1alpha1", "kind": "KafkaReference", "metadata": {"name": "x"}, "spec": {"brokers": {"count": 3, "nonExistingAttr": true}}}`,
		},
	}

	for _, tc := range cases {
		var v any
		if err := json.Unmarshal([]byte(tc.doc), &v); err != nil {
			t.Fatal(err)
		}
		if err := resolved[tc.schema].Validate(v); err == nil {
			t.Errorf("expected %s to reject unknown attribute in %s, but it passed", tc.schema, tc.doc)
		}
	}
}

func TestSchemasRejectInvalidEnumsTypesAndBounds(t *testing.T) {
	resolved := resolveAllSchemas(t)

	cases := []struct {
		name   string
		schema string
		doc    string
	}{
		{
			name:   "scenario invalid TestType enum",
			schema: "scenario.v1.schema.json",
			doc:    `{"type": "NON_EXISTENT_TYPE"}`,
		},
		{
			name:   "scenario invalid acks enum",
			schema: "scenario.v1.schema.json",
			doc:    `{"type": "LOAD", "spec": {"acks": "quorum"}}`,
		},
		{
			name:   "scenario negative records",
			schema: "scenario.v1.schema.json",
			doc:    `{"type": "LOAD", "spec": {"records": -10}}`,
		},
		{
			name:   "test-request invalid phase type",
			schema: "test-request.v1.schema.json",
			doc:    `{"scenario": {"name": "s", "phases": [{"name": "p1", "type": "EXPLODE", "durationMs": 1000}]}}`,
		},
		{
			name:   "test-request rampSteps below minimum",
			schema: "test-request.v1.schema.json",
			doc:    `{"scenario": {"name": "s", "phases": [{"name": "p1", "type": "RAMP", "durationMs": 1000, "rampSteps": 1}]}}`,
		},
		{
			name:   "flow invalid action enum",
			schema: "flow.v1.schema.json",
			doc:    `{"name": "f", "steps": [{"name": "s1", "action": "deploy"}]}`,
		},
		{
			name:   "flow invalid minGrade enum",
			schema: "flow.v1.schema.json",
			doc:    `{"name": "f", "steps": [{"name": "s1", "action": "test", "gate": {"minGrade": "Z"}}]}`,
		},
		{
			name:   "resilience invalid disruptionType",
			schema: "resilience.v1.schema.json",
			doc:    `{"testRequest": {"type": "LOAD"}, "chaosSpec": {"experimentName": "x", "disruptionType": "SOLAR_FLARE"}}`,
		},
		{
			name:   "resilience networkLatencyMs above maximum",
			schema: "resilience.v1.schema.json",
			doc:    `{"testRequest": {"type": "LOAD"}, "chaosSpec": {"experimentName": "x", "networkLatencyMs": 99999}}`,
		},
		{
			name:   "disruption invalid category enum",
			schema: "disruption.v1.schema.json",
			doc:    `{"name": "d", "category": "invalid-cat", "steps": [{"name": "s1", "faultSpec": {"experimentName": "x"}}]}`,
		},
		{
			name:   "disruption fillPercentage above 100",
			schema: "disruption.v1.schema.json",
			doc:    `{"name": "d", "steps": [{"name": "s1", "faultSpec": {"experimentName": "x", "fillPercentage": 120}}]}`,
		},
		{
			name:   "compliance invalid metadata.name pattern",
			schema: "kafkareference.v1alpha1.schema.json",
			doc:    `{"apiVersion": "conformity.kates.io/v1alpha1", "kind": "KafkaReference", "metadata": {"name": "UPPER_BAD"}, "spec": {"brokers": {"count": 3}}}`,
		},
		{
			name:   "compliance invalid storage size pattern",
			schema: "kafkareference.v1alpha1.schema.json",
			doc:    `{"apiVersion": "conformity.kates.io/v1alpha1", "kind": "KafkaReference", "metadata": {"name": "prod"}, "spec": {"brokers": {"storage": {"size": "500Gigabytes"}}}}`,
		},
		{
			name:   "compliance waiver missing required reason/until/by",
			schema: "kafkareference.v1alpha1.schema.json",
			doc:    `{"apiVersion": "conformity.kates.io/v1alpha1", "kind": "KafkaReference", "metadata": {"name": "prod"}, "spec": {"brokers": {"count": 3}, "waivers": [{"rule": "brokers.count"}]}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var v any
			if err := json.Unmarshal([]byte(tc.doc), &v); err != nil {
				t.Fatal(err)
			}
			if err := resolved[tc.schema].Validate(v); err == nil {
				t.Errorf("expected %s to reject invalid document %s, but it passed", tc.schema, tc.doc)
			}
		})
	}
}

func TestSchemaParityAcrossDirectories(t *testing.T) {
	mirrorDirs := []string{
		"../../schemas",
		"../../editors/vscode-kates/schemas",
		"../../editors/visualstudio-kates/Schemas",
	}

	for _, name := range allSchemaNames {
		canonical, err := Files.ReadFile(name)
		if err != nil {
			t.Fatalf("read embedded %s: %v", name, err)
		}
		for _, dir := range mirrorDirs {
			targetPath := filepath.Join(dir, name)
			other, err := os.ReadFile(targetPath)
			if err != nil {
				t.Fatalf("read mirror schema %s: %v", targetPath, err)
			}
			if !bytes.Equal(canonical, other) {
				t.Errorf("schema drift detected: %s differs from cli/schemas/%s", targetPath, name)
			}
		}
	}

	// Verify VS Code & Visual Studio JSON manifests parse cleanly
	jsonAssets := []string{
		"../../editors/vscode-kates/package.json",
		"../../editors/vscode-kates/syntaxes/kates-yaml.tmLanguage.json",
		"../../editors/vscode-kates/snippets/kates-yaml.json",
		"../../editors/visualstudio-kates/catalog.json",
		"../../editors/visualstudio-kates/Grammars/kates-yaml.tmLanguage.json",
	}
	for _, asset := range jsonAssets {
		raw, err := os.ReadFile(asset)
		if err != nil {
			t.Fatalf("read editor asset %s: %v", asset, err)
		}
		var parsed map[string]any
		if err := json.Unmarshal(raw, &parsed); err != nil {
			t.Errorf("invalid JSON in editor asset %s: %v", asset, err)
		}
	}
}

func TestAllSchemaPropertiesHaveDescriptions(t *testing.T) {
	for _, name := range allSchemaNames {
		raw, err := Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		var root map[string]any
		if err := json.Unmarshal(raw, &root); err != nil {
			t.Fatal(err)
		}
		checkPropertiesHaveDescriptions(t, name, "$", root)
	}
}

func checkPropertiesHaveDescriptions(t *testing.T, schemaFile, path string, node map[string]any) {
	t.Helper()
	if props, ok := node["properties"].(map[string]any); ok {
		for propName, rawProp := range props {
			propMap, ok := rawProp.(map[string]any)
			if !ok {
				continue
			}
			propPath := path + "." + propName
			desc, _ := propMap["description"].(string)
			ref, _ := propMap["$ref"].(string)
			if desc == "" && ref == "" {
				t.Errorf("%s: property %s has neither description nor $ref", schemaFile, propPath)
			}
			checkPropertiesHaveDescriptions(t, schemaFile, propPath, propMap)
		}
	}
	if defs, ok := node["$defs"].(map[string]any); ok {
		for defName, rawDef := range defs {
			if defMap, ok := rawDef.(map[string]any); ok {
				checkPropertiesHaveDescriptions(t, schemaFile, path+".$defs."+defName, defMap)
			}
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		checkPropertiesHaveDescriptions(t, schemaFile, path+"[]", items)
	}
}

func TestEditorExtensionManifestAndSnippetIntegration(t *testing.T) {
	resolved := resolveAllSchemas(t)

	// 1. Verify VS Code package.json commands are all registered in src/extension.ts
	pkgBytes, err := os.ReadFile("../../editors/vscode-kates/package.json")
	if err != nil {
		t.Fatalf("read package.json: %v", err)
	}
	extBytes, err := os.ReadFile("../../editors/vscode-kates/src/extension.ts")
	if err != nil {
		t.Fatalf("read extension.ts: %v", err)
	}
	extSource := string(extBytes)

	var pkg struct {
		Contributes struct {
			Commands []struct {
				Command string `json:"command"`
				Title   string `json:"title"`
			} `json:"commands"`
			YamlValidation []struct {
				FileMatch []string `json:"fileMatch"`
				URL       string   `json:"url"`
			} `json:"yamlValidation"`
		} `json:"contributes"`
	}
	if err := json.Unmarshal(pkgBytes, &pkg); err != nil {
		t.Fatalf("unmarshal package.json: %v", err)
	}
	if len(pkg.Contributes.Commands) < 10 {
		t.Fatalf("expected >= 10 commands in package.json, got %d", len(pkg.Contributes.Commands))
	}
	for _, cmd := range pkg.Contributes.Commands {
		if !strings.Contains(extSource, `"`+cmd.Command+`"`) && !strings.Contains(extSource, `'`+cmd.Command+`'`) {
			t.Errorf("command %q declared in package.json is not registered in src/extension.ts", cmd.Command)
		}
	}
	for _, yv := range pkg.Contributes.YamlValidation {
		schemaPath := filepath.Join("../../editors/vscode-kates", yv.URL)
		if _, err := os.Stat(schemaPath); err != nil {
			t.Errorf("yamlValidation schema URL %q does not exist at %s: %v", yv.URL, schemaPath, err)
		}
	}

	// 2. Verify TextMate grammar parity between VS Code and Visual Studio
	vscodeGrammar, err := os.ReadFile("../../editors/vscode-kates/syntaxes/kates-yaml.tmLanguage.json")
	if err != nil {
		t.Fatalf("read vscode grammar: %v", err)
	}
	vsGrammar, err := os.ReadFile("../../editors/visualstudio-kates/Grammars/kates-yaml.tmLanguage.json")
	if err != nil {
		t.Fatalf("read visualstudio grammar: %v", err)
	}
	if !bytes.Equal(vscodeGrammar, vsGrammar) {
		t.Errorf("TextMate grammar drift between editors/vscode-kates and editors/visualstudio-kates")
	}

	// 3. Expand all snippets in snippets/kates-yaml.json and validate against their JSON schemas
	snippetBytes, err := os.ReadFile("../../editors/vscode-kates/snippets/kates-yaml.json")
	if err != nil {
		t.Fatalf("read snippets: %v", err)
	}
	var snippets map[string]struct {
		Prefix      string   `json:"prefix"`
		Description string   `json:"description"`
		Body        []string `json:"body"`
	}
	if err := json.Unmarshal(snippetBytes, &snippets); err != nil {
		t.Fatalf("unmarshal snippets: %v", err)
	}
	if len(snippets) < 6 {
		t.Fatalf("expected >= 6 snippets, got %d", len(snippets))
	}

	prefixToSchema := map[string]string{
		"kates-scenario":           "scenario.v1.schema.json",
		"kates-scenario-integrity": "scenario.v1.schema.json",
		"kates-multiphase":         "test-request.v1.schema.json",
		"kates-flow":               "flow.v1.schema.json",
		"kates-resilience":         "resilience.v1.schema.json",
		"kates-disruption":         "disruption.v1.schema.json",
		"kates-compliance":         "kafkareference.v1alpha1.schema.json",
	}

	choiceRe := regexp.MustCompile(`\$\{\d+\|([^,|]+)[^}]*\}`)
	defaultRe := regexp.MustCompile(`\$\{\d+:([^}]+)\}`)

	for name, snip := range snippets {
		t.Run("snippet_"+snip.Prefix, func(t *testing.T) {
			if snip.Prefix == "" || snip.Description == "" || len(snip.Body) == 0 {
				t.Fatalf("snippet %q has missing prefix, description, or body", name)
			}
			expanded := strings.Join(snip.Body, "\n")
			expanded = choiceRe.ReplaceAllString(expanded, "$1")
			expanded = defaultRe.ReplaceAllString(expanded, "$1")
			expanded = strings.ReplaceAll(expanded, `\$`, `$`)

			v := yamlBytesToJSONMap(t, name, []byte(expanded))
			schemaName, ok := prefixToSchema[snip.Prefix]
			if !ok {
				t.Fatalf("unmapped snippet prefix %q", snip.Prefix)
			}
			if err := resolved[schemaName].Validate(v); err != nil {
				t.Errorf("expanded snippet %q (%s) failed schema %s validation: %v\nExpanded:\n%s", name, snip.Prefix, schemaName, err, expanded)
			}
		})
	}

	// 4. Verify JetBrains, Neovim, and Helix editor configurations reference all 6 schemas and kates lsp
	jbXMLBytes, err := os.ReadFile("../../editors/jetbrains-kates/jsonSchemas.xml")
	if err != nil {
		t.Fatalf("read jetbrains jsonSchemas.xml: %v", err)
	}
	helixTOMLBytes, err := os.ReadFile("../../editors/helix-kates/languages.toml")
	if err != nil {
		t.Fatalf("read helix languages.toml: %v", err)
	}
	nvimLuaBytes, err := os.ReadFile("../../editors/neovim-kates/kates_lsp.lua")
	if err != nil {
		t.Fatalf("read neovim kates_lsp.lua: %v", err)
	}
	for _, s := range allSchemaNames {
		if !strings.Contains(string(jbXMLBytes), s) {
			t.Errorf("editors/jetbrains-kates/jsonSchemas.xml missing schema %s", s)
		}
		if !strings.Contains(string(helixTOMLBytes), s) {
			t.Errorf("editors/helix-kates/languages.toml missing schema %s", s)
		}
	}
	if !strings.Contains(string(nvimLuaBytes), `"lsp"`) || !strings.Contains(string(nvimLuaBytes), `"--stdio"`) {
		t.Errorf("editors/neovim-kates/kates_lsp.lua does not invoke 'kates lsp --stdio'")
	}
}
