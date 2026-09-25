package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/client"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"
)

func mcpDraft(t *testing.T, h *mcpHarness, args map[string]any) (mcpEnvelope, mcpDraftScenarioOut) {
	t.Helper()
	env := h.callOK("draft_scenario", args)
	out := mcpData[mcpDraftScenarioOut](t, env)
	mcpScnReadsBackAsChecked(t, args, out)
	return env, out
}

// mcpScnReadsBackAsChecked holds every result to one property: the file it
// returns (or, when it leaves the echo out, the file it was given) reads back,
// as kates test apply reads it, as the scenarios and requests it checked.
func mcpScnReadsBackAsChecked(t *testing.T, args map[string]any, out mcpDraftScenarioOut) {
	t.Helper()
	text := out.YAML
	if out.YAMLOmitted {
		given, _ := args["yaml"].(string)
		text = strings.ReplaceAll(given, "\r\n", "\n")
	}
	again, err := mcpScnParseFile([]byte(text))
	if err != nil || len(again) != len(out.Scenarios) {
		t.Fatalf("the returned file reads back as %d scenarios (%v), the result checked %d", len(again), err, len(out.Scenarios))
	}
	for i, sc := range again {
		want, err := json.Marshal(mcpScnDisplayRequest(scenarioToRequest(sc)))
		if err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(out.Scenarios[i].Request)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("scenario %d: the returned file sends %s, the result checked %s", i, want, got)
		}
	}
}

// mcpFinding returns the findings of one scenario (or -1) on one field.
func mcpScnFindingsOn(out mcpDraftScenarioOut, scenario int, field string) []mcpScnFinding {
	var got []mcpScnFinding
	for _, f := range out.Findings {
		if f.Scenario == scenario && f.Field == field {
			got = append(got, f)
		}
	}
	return got
}

func mcpScnHasFinding(t *testing.T, out mcpDraftScenarioOut, scenario int, field, level, text string) {
	t.Helper()
	for _, f := range mcpScnFindingsOn(out, scenario, field) {
		if f.Level == level && strings.Contains(f.Message, text) {
			return
		}
	}
	t.Errorf("no %s finding on scenario %d %s containing %q; findings: %+v", level, scenario, field, text, out.Findings)
}

func mcpSecCaveatIDs(env mcpEnvelope) []string {
	ids := make([]string, 0, len(env.Caveats))
	for _, c := range env.Caveats {
		ids = append(ids, c.ID)
	}
	return ids
}

func mcpSecHasCaveats(t *testing.T, env mcpEnvelope, want ...mcpCaveatID) {
	t.Helper()
	got := mcpSecCaveatIDs(env)
	for _, w := range want {
		if !slices.Contains(got, string(w)) {
			t.Errorf("caveats %v lack %s", got, w)
		}
	}
}

// mcpScnOnlyPinCheck: draft_scenario is local; the only request its call makes
// is the guard's pin check.
func mcpScnOnlyPinCheck(t *testing.T, fb *mcpFakeBackend) {
	t.Helper()
	for _, p := range mcpPaths(fb.Requests()) {
		if p != "GET /api/cluster/info" {
			t.Errorf("draft_scenario sent %s; it sends nothing but the pin check", p)
		}
	}
	assertReadOnly(t, fb.Requests())
}

func TestMCPDraftScenarioTemplate(t *testing.T) {
	fb := newMCPFakeBackend(t, "cluster-a")
	h := newMCPHarness(t, fb)
	env, out := mcpDraft(t, h, map[string]any{"template": "quick-load"})

	data, err := scenarioFS.ReadFile("scenarios/quick-load.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if out.YAML != string(data) {
		t.Errorf("an unedited template comes back as it is; got\n%s", out.YAML)
	}
	if out.Verdict != mcpScnOutside || len(out.Scenarios) != 1 {
		t.Fatalf("verdict %s, %d scenarios", out.Verdict, len(out.Scenarios))
	}
	sc := out.Scenarios[0]
	want := scenarioToRequest(TestScenario{Type: "LOAD", Spec: map[string]any{"records": 50000, "parallelProducers": 2, "recordSizeBytes": 1024}})
	if sc.Request.TestType != "LOAD" || sc.Request.Spec == nil || *sc.Request.Spec != *want.Spec {
		t.Errorf("request = %+v, want what scenarioToRequest builds: %+v", sc.Request.Spec, want.Spec)
	}
	e := sc.Effective
	if e == nil || e.NumRecords != 50000 || e.DurationMs != 600_000 || e.Throughput != -1 || e.Topic != "load-test" ||
		e.ProducerTasks != 1 || e.RecordsPerSec != -1 {
		t.Fatalf("effective = %+v", e)
	}
	for _, f := range []string{"durationMs", "partitions", "throughput", "topic"} {
		if !slices.Contains(e.Defaulted, f) {
			t.Errorf("defaulted %v lacks %s", e.Defaulted, f)
		}
	}
	mcpScnHasFinding(t, out, 0, "spec.targetThroughput", mcpScnOutside, "the rate is unlimited: set targetThroughput")
	mcpScnHasFinding(t, out, 0, "spec.topic", mcpScnOutside, "load-test, the topic every LOAD run shares")
	mcpScnHasFinding(t, out, 0, "spec.parallelProducers", mcpScnWarning, "starts one producer")
	mcpSecHasCaveats(t, env, mcpCaveatAgentEnvelopeProposed, mcpCaveatScenarioShippedDefaults,
		mcpCaveatScenarioValidateGrading, mcpCaveatLoadSingleProducer)
	if out.Envelope.MaxRecordsPerSecond != mcpScnEnvMaxRecordsPerSec || out.Envelope.MaxBytes != 10<<30 || out.Envelope.TopicPrefix != "kates-mcp-" {
		t.Errorf("envelope = %+v", out.Envelope)
	}
	mcpScnOnlyPinCheck(t, fb)
}

// TestMCPDraftScenarioEveryTemplate: every built-in template is a valid
// scenario file, and none is inside the envelope as shipped, because none
// writes to a kates-mcp- topic, and most leave the rate unlimited.
func TestMCPDraftScenarioEveryTemplate(t *testing.T) {
	fb := newMCPFakeBackend(t, "cluster-a")
	h := newMCPHarness(t, fb)
	for _, meta := range builtinScenarios {
		_, out := mcpDraft(t, h, map[string]any{"template": meta.name})
		if out.Verdict != mcpScnOutside {
			t.Errorf("%s: verdict %s, want %s; findings %+v", meta.name, out.Verdict, mcpScnOutside, out.Findings)
		}
		if len(out.Scenarios) != 1 || out.Scenarios[0].Request.TestType != meta.testType {
			t.Errorf("%s: scenarios %+v", meta.name, out.Scenarios)
		}
	}
	_, out := mcpDraft(t, h, map[string]any{"template": "spike-test"})
	mcpScnHasFinding(t, out, 0, "type", mcpScnOutside, "SPIKE runs its producer at an unlimited rate")
	_, out = mcpDraft(t, h, map[string]any{"template": "endurance-soak"})
	mcpScnHasFinding(t, out, 0, "spec.durationSeconds", mcpScnOutside, "the run may last up to 3600 s")
	mcpScnHasFinding(t, out, 0, "spec", mcpScnOutside, "GiB")
	_, out = mcpDraft(t, h, map[string]any{"template": "integrity-tx"})
	mcpScnHasFinding(t, out, 0, "validate.maxDuplicatePercent", mcpScnWarning, "drops validate.maxDuplicatePercent")
	// The backend carries the integrity options now, and an INTEGRITY run with
	// acks=all can apply all three.
	for _, key := range []string{"spec.enableIdempotence", "spec.enableTransactions", "spec.enableCrc"} {
		if f := mcpScnFindingsOn(out, 0, key); len(f) != 0 {
			t.Errorf("integrity-tx: %s has findings %+v", key, f)
		}
	}
	_, out = mcpDraft(t, h, map[string]any{"template": "ci-gate"})
	mcpScnHasFinding(t, out, 0, "validate.maxErrorRate", mcpScnWarning, "never checks maxErrorRate")
	mcpScnHasFinding(t, out, 0, "validate.maxDataLossPercent", mcpScnWarning, "only INTEGRITY runs")
	mcpScnOnlyPinCheck(t, fb)
}

// TestMCPDraftScenarioInsideEnvelope: a template with overrides can come
// inside the envelope; the overrides reach the YAML and the request.
func TestMCPDraftScenarioInsideEnvelope(t *testing.T) {
	fb := newMCPFakeBackend(t, "cluster-a")
	h := newMCPHarness(t, fb)
	env, out := mcpDraft(t, h, map[string]any{
		"template":           "exactly-once",
		"name":               "RT smoke",
		"spec_overrides":     map[string]any{"topic": "kates-mcp-rt", "records": 20000},
		"validate_overrides": map[string]any{"maxP99LatencyMs": 150},
	})
	if out.Verdict != "inside_envelope" {
		t.Fatalf("verdict %s; findings %+v", out.Verdict, out.Findings)
	}
	sc := out.Scenarios[0]
	if sc.Name != "RT smoke" || sc.Request.Spec.Topic != "kates-mcp-rt" || sc.Request.Spec.Records != 20000 {
		t.Errorf("scenario = %+v", sc)
	}
	if e := sc.Effective; e.Throughput != 10000 || e.RecordsPerSec != 20000 || e.DurationMs != 600_000 {
		t.Errorf("effective = %+v", e)
	}
	for _, want := range []string{"name: RT smoke", "topic: kates-mcp-rt", "records: 20000", "maxP99LatencyMs: 150", "enableTransactions: true"} {
		if !strings.Contains(out.YAML, want) {
			t.Errorf("YAML lacks %q:\n%s", want, out.YAML)
		}
	}
	// The returned YAML reads back as the scenario that was checked.
	again, err := mcpScnParseFile([]byte(out.YAML))
	if err != nil || len(again) != 1 || scenarioToRequest(again[0]).Spec.Topic != "kates-mcp-rt" {
		t.Errorf("the YAML does not read back: %v %+v", err, again)
	}
	// Warnings remain: ROUND_TRIP reports no integrity data. Its producer
	// takes the idempotence and transactions the template asks for.
	mcpScnHasFinding(t, out, 0, "validate.maxOutOfOrder", mcpScnWarning, "never checked")
	mcpScnHasFinding(t, out, 0, "spec.numConsumers", mcpScnWarning, "no effect")
	if f := mcpScnFindingsOn(out, 0, "spec.enableIdempotence"); len(f) != 0 {
		t.Errorf("spec.enableIdempotence has findings %+v", f)
	}
	mcpSecHasCaveats(t, env, mcpCaveatAgentEnvelopeProposed)
	mcpScnOnlyPinCheck(t, fb)
}

// TestMCPDraftScenarioFieldsTheBackendRefuses: what the backend answers 400
// for, draft_scenario calls invalid, naming the key.
func TestMCPDraftScenarioFieldsTheBackendRefuses(t *testing.T) {
	fb := newMCPFakeBackend(t, "cluster-a")
	h := newMCPHarness(t, fb)
	yamlText := `scenarios:
  - name: stress with consumer settings
    type: STRESS
    spec: {topic: kates-mcp-a, consumerGroup: perf-cg, fetchMinBytes: 1024, fetchMaxWaitMs: 100, enableCrc: true}
  - name: spike with a rate and idempotence
    type: SPIKE
    spec: {topic: kates-mcp-b, targetThroughput: 500, enableIdempotence: true}
  - name: transactions on trogdor
    type: LOAD
    backend: trogdor
    spec: {topic: kates-mcp-c, enableTransactions: true}
  - name: transactions without idempotence
    type: LOAD
    spec: {topic: kates-mcp-d, enableTransactions: true, enableIdempotence: false, enableCrc: false}
`
	_, out := mcpDraft(t, h, map[string]any{"yaml": yamlText})
	if out.Verdict != mcpScnInvalid {
		t.Fatalf("verdict %s; findings %+v", out.Verdict, out.Findings)
	}
	for _, key := range []string{"spec.consumerGroup", "spec.fetchMinBytes", "spec.fetchMaxWaitMs"} {
		mcpScnHasFinding(t, out, 0, key, mcpScnInvalid, "STRESS starts no consumer")
	}
	mcpScnHasFinding(t, out, 0, "spec.enableCrc", mcpScnInvalid, "only an INTEGRITY run checks record CRCs")
	mcpScnHasFinding(t, out, 1, "spec.targetThroughput", mcpScnInvalid, "SPIKE runs its producers unthrottled")
	mcpScnHasFinding(t, out, 1, "spec.enableIdempotence", mcpScnInvalid, "acks is 1 (the type's default")
	mcpScnHasFinding(t, out, 2, "spec.enableTransactions", mcpScnInvalid, "the trogdor backend cannot")
	mcpScnHasFinding(t, out, 3, "spec.enableTransactions", mcpScnInvalid, "always idempotent")
	if f := mcpScnFindingsOn(out, 3, "spec.enableCrc"); len(f) != 0 {
		t.Errorf("enableCrc: false asks for no CRC check, which a LOAD run honours; findings %+v", f)
	}
	mcpScnOnlyPinCheck(t, fb)
}

// TestMCPDraftScenarioRefusesWhatApplyAndTheBackendRefuse: a flag that is
// not true or false stops kates test apply, and a blank consumer group fails
// the backend's validation, so both are invalid rather than warnings.
func TestMCPDraftScenarioRefusesWhatApplyAndTheBackendRefuse(t *testing.T) {
	fb := newMCPFakeBackend(t, "cluster-a")
	h := newMCPHarness(t, fb)
	yamlText := `scenarios:
  - name: integrity with a yes
    type: INTEGRITY
    spec: {topic: kates-mcp-a, enableCrc: yes, consumerGroup: "  "}
`
	_, out := mcpDraft(t, h, map[string]any{"yaml": yamlText})
	mcpScnHasFinding(t, out, 0, "spec.enableCrc", mcpScnInvalid, "write true or false")
	mcpScnHasFinding(t, out, 0, "spec.consumerGroup", mcpScnInvalid, "which the backend refuses for consumerGroup")
	if req := out.Scenarios[0].Request; req.Spec.EnableCrc != nil {
		t.Errorf("enableCrc: yes is sent as %v", *req.Spec.EnableCrc)
	}
	mcpScnOnlyPinCheck(t, fb)
}

// TestMCPDraftScenarioRulesMatchTheBackend runs the cases the backend's
// TestOrchestratorTest runs through TestOrchestrator.inapplicableFields
// through draft_scenario's copy of those rules: the two must refuse the same
// fields, or an agent is told a scenario runs that the backend refuses.
func TestMCPDraftScenarioRulesMatchTheBackend(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "kates", "src", "test", "resources", "spec-applicability.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name    string          `json:"name"`
			Type    string          `json:"type"`
			Backend string          `json:"backend"`
			Spec    json.RawMessage `json:"spec"`
			Refused []string        `json:"refused"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Cases) < 10 {
		t.Fatalf("only %d cases", len(fixture.Cases))
	}
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			spec := &client.TestSpec{}
			if err := json.Unmarshal(c.Spec, spec); err != nil {
				t.Fatal(err)
			}
			var fs mcpScnFindings
			mcpScnCheckApplies(0, &client.CreateTestRequest{TestType: c.Type, Backend: c.Backend, Spec: spec}, &fs)
			var got []string
			for _, f := range fs.list {
				got = append(got, strings.TrimPrefix(f.Field, "spec."))
			}
			want := append([]string(nil), c.Refused...)
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("refused %v, the backend refuses %v", got, want)
			}
		})
	}
}

// TestMCPDraftScenarioTargetThroughputSetsTheRate: targetThroughput is the
// scenario key for the rate, and the effective spec uses it.
func TestMCPDraftScenarioTargetThroughputSetsTheRate(t *testing.T) {
	fb := newMCPFakeBackend(t, "cluster-a")
	h := newMCPHarness(t, fb)
	_, out := mcpDraft(t, h, map[string]any{
		"template":       "quick-load",
		"spec_overrides": map[string]any{"topic": "kates-mcp-rate", "targetThroughput": 2000},
	})
	e := out.Scenarios[0].Effective
	if e.Throughput != 2000 || slices.Contains(e.Defaulted, "throughput") {
		t.Errorf("effective = %+v", e)
	}
	if f := mcpScnFindingsOn(out, 0, "spec.targetThroughput"); len(f) != 0 {
		t.Errorf("a set rate is inside the envelope; findings %+v", f)
	}
	mcpScnOnlyPinCheck(t, fb)
}

func TestMCPDraftScenarioYAML(t *testing.T) {
	fb := newMCPFakeBackend(t, "cluster-a")
	h := newMCPHarness(t, fb)
	yamlText := `# drafted by an agent
name: bare
type: stress
spec:
  records: "10000"
  parallelProducers: 8
  throughput: 500
  numRecords: 5
  lingerMs: 0
  durationSeconds: 1800
  partitions: 60
  topic: orders
  acks: "2"
phases:
  - name: warmup
validate:
  maxP99LatencyMs: 0
  maxRpoMs: 100
`
	env, out := mcpDraft(t, h, map[string]any{"yaml": yamlText})
	if !strings.HasPrefix(out.YAML, "scenarios:\n") || !strings.Contains(out.YAML, "# drafted by an agent") {
		t.Errorf("a lone scenario is put under scenarios:, comments kept:\n%s", out.YAML)
	}
	mcpScnHasFinding(t, out, -1, "scenarios", mcpScnWarning, "was not under scenarios:")
	if out.Verdict != mcpScnInvalid {
		t.Errorf("verdict %s, want invalid (acks \"2\")", out.Verdict)
	}
	sc := out.Scenarios[0]
	if sc.Request.TestType != "STRESS" || sc.Request.Spec.Records != 0 || sc.Request.Spec.LingerMs != 0 {
		t.Errorf("request = %+v", sc.Request.Spec)
	}
	mcpScnHasFinding(t, out, 0, "spec.records", mcpScnWarning, "reads as 0")
	mcpScnHasFinding(t, out, 0, "spec.throughput", mcpScnWarning, "the scenario key for the producer rate is targetThroughput")
	mcpScnHasFinding(t, out, 0, "spec.numRecords", mcpScnWarning, "the scenario key is records")
	mcpScnHasFinding(t, out, 0, "spec.lingerMs", mcpScnWarning, "0 is left out")
	mcpScnHasFinding(t, out, 0, "spec.acks", mcpScnInvalid, "the backend refuses for acks")
	mcpScnHasFinding(t, out, 0, "phases", mcpScnWarning, "no scenario phases")
	mcpScnHasFinding(t, out, 0, "spec.parallelProducers", mcpScnOutside, "STRESS starts 8 producers")
	mcpScnHasFinding(t, out, 0, "spec.durationSeconds", mcpScnOutside, "the run may last up to 1800 s")
	mcpScnHasFinding(t, out, 0, "spec.partitions", mcpScnOutside, "60 partitions")
	mcpScnHasFinding(t, out, 0, "spec.topic", mcpScnOutside, "\"orders\"")
	mcpScnHasFinding(t, out, 0, "validate.maxP99LatencyMs", mcpScnWarning, "0 turns this gate off")
	mcpScnHasFinding(t, out, 0, "validate.maxRpoMs", mcpScnWarning, "not evaluable")
	if e := sc.Effective; e.ProducerTasks != 8 || e.NumRecords != 5_000_000 || e.DurationMs != 1_800_000 {
		t.Errorf("effective = %+v", e)
	}
	mcpSecHasCaveats(t, env, mcpCaveatReaper30Minutes)
	mcpScnOnlyPinCheck(t, fb)
}

func TestMCPDraftScenarioInvalidValues(t *testing.T) {
	fb := newMCPFakeBackend(t, "cluster-a")
	h := newMCPHarness(t, fb)
	yamlText := `scenarios:
  - name: a
    type: BOGUS
    spec: {records: 10}
  - name: b
    type: INTEGRITY
    backend: trogdor
    spec: {records: -5, topic: "bad topic!", compressionType: brotli, durationSeconds: 0.5}
    validate: {maxDataLossPercent: 0}
  - name: c
    type: LOAD
    spec: {topic: "kates-mcp-x y", recordSizeBytes: 200000000}
`
	_, out := mcpDraft(t, h, map[string]any{"yaml": yamlText})
	if out.Verdict != mcpScnInvalid || len(out.Scenarios) != 3 {
		t.Fatalf("verdict %s, %d scenarios", out.Verdict, len(out.Scenarios))
	}
	mcpScnHasFinding(t, out, 0, "type", mcpScnInvalid, "no test type \"BOGUS\"")
	if out.Scenarios[0].Effective != nil {
		t.Errorf("an unknown type has no effective spec: %+v", out.Scenarios[0].Effective)
	}
	mcpScnHasFinding(t, out, -1, "scenarios", mcpScnOutside, "one scenario per file")
	mcpScnHasFinding(t, out, 1, "backend", mcpScnInvalid, "need the native backend")
	mcpScnHasFinding(t, out, 1, "spec.records", mcpScnInvalid, "numRecords -5")
	mcpScnHasFinding(t, out, 1, "spec.topic", mcpScnInvalid, "refuses for topic")
	mcpScnHasFinding(t, out, 1, "spec.compressionType", mcpScnInvalid, "brotli")
	mcpScnHasFinding(t, out, 1, "spec.durationSeconds", mcpScnWarning, "cuts to 0")
	mcpScnHasFinding(t, out, 1, "validate.maxDataLossPercent", mcpScnWarning, "only INTEGRITY runs on the native backend")
	mcpScnHasFinding(t, out, 2, "spec.topic", mcpScnInvalid, "refuses for topic")
	mcpScnHasFinding(t, out, 2, "spec.recordSizeBytes", mcpScnInvalid, "recordSize 200000000")
	if out.Findings[0].Level != mcpScnInvalid || out.Findings[len(out.Findings)-1].Level != mcpScnWarning {
		t.Errorf("findings are not worst first: %+v", out.Findings)
	}
	mcpScnOnlyPinCheck(t, fb)
}

func TestMCPDraftScenarioIntegrityGates(t *testing.T) {
	fb := newMCPFakeBackend(t, "cluster-a")
	h := newMCPHarness(t, fb)
	_, out := mcpDraft(t, h, map[string]any{"yaml": `scenarios:
  - name: i
    type: INTEGRITY
    spec: {topic: kates-mcp-i}
    validate: {maxCrcFailures: -1, maxOutOfOrder: 0.5}
`})
	mcpScnHasFinding(t, out, 0, "validate.maxOutOfOrder", mcpScnWarning, "reads as 0")
	mcpScnHasFinding(t, out, 0, "validate.maxCrcFailures", mcpScnWarning, "negative value turns this gate off")
	mcpScnHasFinding(t, out, 0, "validate", mcpScnWarning, "maxDataLossPercent absent")
	_, out = mcpDraft(t, h, map[string]any{"yaml": "scenarios:\n  - {name: n, type: VOLUME, spec: {topic: kates-mcp-v}}\n"})
	mcpScnHasFinding(t, out, 0, "validate", mcpScnWarning, "grades nothing")
	mcpScnOnlyPinCheck(t, fb)
}

func TestMCPDraftScenarioArguments(t *testing.T) {
	fb := newMCPFakeBackend(t, "cluster-a")
	h := newMCPHarness(t, fb)
	six := "scenarios:\n" + strings.Repeat("  - {name: x, type: LOAD}\n", 6)
	tests := []struct {
		name string
		args map[string]any
	}{
		{"neither", map[string]any{}},
		{"both", map[string]any{"yaml": "scenarios: []", "template": "quick-load"}},
		{"unknown template", map[string]any{"template": "../../etc/passwd"}},
		{"too long", map[string]any{"yaml": strings.Repeat("#", mcpDraftMaxYAMLBytes+1)}},
		{"control characters", map[string]any{"yaml": "scenarios:\n  - {name: \x1b[31mx, type: LOAD}\n"}},
		{"fence characters", map[string]any{"yaml": "scenarios:\n  - {name: «x», type: LOAD}\n"}},
		{"escaped control character", map[string]any{"yaml": "scenarios:\n  - {name: \"c\\x01\", type: LOAD}\n"}},
		{"escaped format character in a key", map[string]any{"yaml": "scenarios:\n  - {type: LOAD, spec: {\"re\\u200bcords\": 5}}\n"}},
		{"escaped fence character", map[string]any{"yaml": "scenarios:\n  - {type: LOAD, spec: {topic: \"\\u00abx\"}}\n"}},
		{"not YAML", map[string]any{"yaml": "scenarios: [\n"}},
		{"not a mapping", map[string]any{"yaml": "- a\n- b\n"}},
		{"no scenarios", map[string]any{"yaml": "name: x\n"}},
		{"empty list", map[string]any{"yaml": "scenarios: []\n"}},
		{"wrong threshold type", map[string]any{"yaml": "scenarios:\n  - {type: LOAD, validate: {maxOutOfOrder: none}}\n"}},
		{"too many", map[string]any{"yaml": six}},
		{"object override", map[string]any{"template": "quick-load", "spec_overrides": map[string]any{"records": map[string]any{"a": 1}}}},
		{"null override", map[string]any{"template": "quick-load", "spec_overrides": map[string]any{"records": nil}}},
		{"bad override key", map[string]any{"template": "quick-load", "spec_overrides": map[string]any{"a b": 1}}},
		{"multi-line name", map[string]any{"template": "quick-load", "name": "a\nb"}},
		{"unknown argument", map[string]any{"template": "quick-load", "wait": true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if e := h.callErr("draft_scenario", tt.args); e.Error.Code != mcpErrInvalidArgument {
				t.Errorf("code %s, want %s: %+v", e.Error.Code, mcpErrInvalidArgument, e.Error)
			}
		})
	}
	mcpScnOnlyPinCheck(t, fb)
}

// TestMCPDraftScenarioCapsFindings: a file with many keys nothing reads
// still fits one result.
func TestMCPDraftScenarioCapsFindings(t *testing.T) {
	fb := newMCPFakeBackend(t, "cluster-a")
	h := newMCPHarness(t, fb)
	var b strings.Builder
	b.WriteString("scenarios:\n  - name: x\n    type: LOAD\n    spec:\n")
	for i := 0; i < 80; i++ {
		fmt.Fprintf(&b, "      unknownKey%02d: %d\n", i, i)
	}
	env, out := mcpDraft(t, h, map[string]any{"yaml": b.String()})
	if len(out.Findings) != mcpDraftMaxFindings || !env.Truncated {
		t.Errorf("%d findings, truncated %v; want %d and truncated", len(out.Findings), env.Truncated, mcpDraftMaxFindings)
	}
	mcpScnOnlyPinCheck(t, fb)
}

func TestMCPScenarioResources(t *testing.T) {
	fb := newMCPFakeBackend(t, "cluster-a")
	h := newMCPHarness(t, fb)
	ctx := context.Background()
	listed := map[string]string{}
	for r, err := range h.session.Resources(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		listed[r.URI] = r.MIMEType
	}
	for _, meta := range builtinScenarios {
		uri := mcpScenarioURIPrefix + meta.name
		if listed[uri] != "application/yaml" {
			t.Errorf("%s is not listed as application/yaml (%v)", uri, listed)
			continue
		}
		res, err := h.session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		if err != nil {
			t.Fatal(err)
		}
		data, err := scenarioFS.ReadFile("scenarios/" + meta.filename)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Contents) != 1 || res.Contents[0].Text != string(data) {
			t.Errorf("%s does not serve the embedded template", uri)
		}
		if !mcpScnCleanText(string(data)) {
			t.Errorf("template %s holds characters draft_scenario would refuse", meta.name)
		}
	}
	_, err := h.session.ReadResource(ctx, &mcp.ReadResourceParams{URI: mcpScenarioURIPrefix + "nope"})
	var we *jsonrpc.Error
	if !errors.As(err, &we) || we.Code != jsonrpc.CodeInvalidParams {
		t.Errorf("an unknown scenario: err = %v", err)
	}
	if len(fb.Requests()) != 0 {
		t.Errorf("reading a scenario template reached the backend: %v", mcpPaths(fb.Requests()))
	}
}

// TestMCPScenarioShippedDefaultsMatchTheBackend holds mcpScnShippedDefaults
// to what Kates ships: the @ConfigProperty fallbacks of TestTypeDefaults.java,
// overridden by application.properties, and the kates chart's values.
func TestMCPScenarioShippedDefaultsMatchTheBackend(t *testing.T) {
	root := filepath.Join("..", "..")
	java, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(mcpJava+"config/TestTypeDefaults.java")))
	if err != nil {
		t.Fatal(err)
	}
	props, err := os.ReadFile(filepath.Join(root, "kates", "src", "main", "resources", "application.properties"))
	if err != nil {
		t.Fatal(err)
	}
	values, err := os.ReadFile(filepath.Join(root, "charts", "kates", "values.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	backend := map[string]string{}
	for _, m := range regexp.MustCompile(`name = "kates\.tests\.([a-z]+)\.([a-z-]+)", defaultValue = "([^"]*)"`).FindAllStringSubmatch(string(java), -1) {
		backend[m[1]+"."+m[2]] = m[3]
	}
	for _, m := range regexp.MustCompile(`(?m)^kates\.tests\.([a-z]+)\.([a-z-]+)=(.*)$`).FindAllStringSubmatch(string(props), -1) {
		backend[m[1]+"."+m[2]] = strings.TrimSpace(m[3])
	}
	var chart struct {
		Defaults map[string]string            `yaml:"defaults"`
		Tests    map[string]map[string]string `yaml:"tests"`
	}
	if err := yaml.Unmarshal(values, &chart); err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{
		"partitions": "partitions", "record-size": "recordSize", "num-producers": "numProducers",
		"throughput": "throughput", "num-records": "numRecords", "duration-ms": "durationMs",
	}
	types := map[string]string{
		"LOAD": "load", "STRESS": "stress", "SPIKE": "spike", "ENDURANCE": "endurance",
		"VOLUME": "volume", "CAPACITY": "capacity", "ROUND_TRIP": "roundtrip",
	}
	for typ, prefix := range types {
		d := mcpScnShippedDefaults[typ]
		ours := map[string]int64{
			"partitions": int64(d.partitions), "record-size": int64(d.recordSize), "num-producers": int64(d.numProducers),
			"throughput": int64(d.throughput), "num-records": d.numRecords, "duration-ms": d.durationMs,
		}
		for key, camel := range keys {
			b, ok := backend[prefix+"."+key]
			if !ok {
				t.Errorf("TestTypeDefaults has no kates.tests.%s.%s", prefix, key)
				continue
			}
			c := chart.Tests[prefix][camel]
			if c == "" {
				c = chart.Defaults[camel]
			}
			want := strconv.FormatInt(ours[key], 10)
			if b != want || c != want {
				t.Errorf("%s %s: draft_scenario assumes %s, the backend ships %s, the chart %s", typ, key, want, b, c)
			}
		}
		// acks is text, and decides whether the producer options apply.
		b, c := backend[prefix+".acks"], chart.Tests[prefix]["acks"]
		if c == "" {
			c = chart.Defaults["acks"]
		}
		if b != d.acks || c != d.acks {
			t.Errorf("%s acks: draft_scenario assumes %s, the backend ships %s, the chart %s", typ, d.acks, b, c)
		}
	}
	if len(mcpScnShippedDefaults) != len(types) {
		t.Errorf("mcpScnShippedDefaults has %d types, the test checks %d", len(mcpScnShippedDefaults), len(types))
	}
}

// TestMCPDraftScenarioSpecKeysMatchScenarioToRequest: every key the table
// describes is one scenarioToRequest reads, and it reads no other.
func TestMCPDraftScenarioSpecKeysMatchScenarioToRequest(t *testing.T) {
	src, err := os.ReadFile("apply.go")
	if err != nil {
		t.Fatal(err)
	}
	var read []string
	for _, m := range regexp.MustCompile(`s\.Spec\["([A-Za-z]+)"\]`).FindAllStringSubmatch(string(src), -1) {
		read = append(read, m[1])
	}
	slices.Sort(read)
	var ours []string
	for k := range mcpScnSpecKeys {
		ours = append(ours, k)
	}
	slices.Sort(ours)
	if !slices.Equal(read, ours) {
		t.Errorf("scenarioToRequest reads %v; draft_scenario describes %v", read, ours)
	}
	// Each key's wire name is the JSON name of the field it sets.
	for key, k := range mcpScnSpecKeys {
		sc := TestScenario{Type: "LOAD", Spec: map[string]any{key: 7}}
		switch k.kind {
		case 's':
			sc.Spec[key] = "x"
		case 'b':
			sc.Spec[key] = true
		}
		b, err := json.Marshal(scenarioToRequest(sc).Spec)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `"`+k.wire+`":`) {
			t.Errorf("spec.%s sets no field named %s: %s", key, k.wire, b)
		}
	}
}

// TestMCPDraftScenarioEscapesNeverChangeTheFile: a YAML escape can put a
// format character in a key of a plain ASCII file. Re-encoding writes it raw,
// and cleaning the result for display would then turn "re\u200bcords", a key
// the checks ignore, into records. Such a file is refused, edited or not.
func TestMCPDraftScenarioEscapesNeverChangeTheFile(t *testing.T) {
	fb := newMCPFakeBackend(t, "cluster-a")
	h := newMCPHarness(t, fb)
	lone := "name: x\ntype: ROUND_TRIP\n" +
		`spec: {topic: kates-mcp-a, "re\u200bcords": 1000000000, "duration\u200bSeconds": 86400, "parti\u200btions": 10000}` +
		"\nvalidate: {maxP99LatencyMs: 10}\n"
	for _, args := range []map[string]any{
		{"yaml": lone},
		{"yaml": "scenarios:\n  - " + strings.ReplaceAll(strings.TrimSuffix(lone, "\n"), "\n", "\n    ") + "\n"},
		{"yaml": "scenarios:\n  - " + strings.ReplaceAll(strings.TrimSuffix(lone, "\n"), "\n", "\n    ") + "\n", "name": "y"},
		{"template": "quick-load", "spec_overrides": map[string]any{"topic": "kates-mcp-\u200bx"}},
	} {
		if e := h.callErr("draft_scenario", args); e.Error.Code != mcpErrInvalidArgument {
			t.Errorf("%v: code %s, want %s", args, e.Error.Code, mcpErrInvalidArgument)
		}
	}
	mcpScnOnlyPinCheck(t, fb)
}

// TestMCPDraftScenarioJSON: kates test apply reads a .json file with
// encoding/json, which matches keys to fields in any case and lets a later
// key win. A JSON draft that reads differently that way is invalid.
func TestMCPDraftScenarioJSON(t *testing.T) {
	fb := newMCPFakeBackend(t, "cluster-a")
	h := newMCPHarness(t, fb)

	shadowed := `{"scenarios":[{"name":"x","type":"ROUND_TRIP","Type":"SPIKE",` +
		`"spec":{"topic":"kates-mcp-a","records":1000,"durationSeconds":600},` +
		`"SPEC":{"topic":"orders","durationSeconds":86400,"records":1000000000},"validate":{"maxP99LatencyMs":10}}]}`
	_, out := mcpDraft(t, h, map[string]any{"yaml": shadowed})
	if out.Verdict != mcpScnInvalid {
		t.Errorf("verdict %s, want invalid; findings %+v", out.Verdict, out.Findings)
	}
	mcpScnHasFinding(t, out, -1, "scenarios", mcpScnInvalid, "when it is named .json")
	mcpScnHasFinding(t, out, 0, "Type", mcpScnInvalid, "differs from type only in case")
	mcpScnHasFinding(t, out, 0, "SPEC", mcpScnInvalid, "in a .json file it replaces spec")
	// What apply would run from x.json is what the finding warns of.
	asJSON, err := mcpScnParseJSONFile([]byte(out.YAML))
	if err != nil || scenarioToRequest(asJSON[0]).TestType != "SPIKE" {
		t.Errorf("encoding/json reads %+v, %v", asJSON, err)
	}

	_, out = mcpDraft(t, h, map[string]any{"yaml": `{"scenarios":[{"name":"x","type":"ROUND_TRIP",` +
		`"spec":{"topic":"kates-mcp-a","records":1000},"validate":{"maxP99LatencyMs":10,"MaxP99LatencyMs":0}}]}`})
	mcpScnHasFinding(t, out, 0, "validate.MaxP99LatencyMs", mcpScnInvalid, "differs from validate.maxP99LatencyMs only in case")
	mcpScnHasFinding(t, out, -1, "scenarios", mcpScnInvalid, "when it is named .json")

	// JSON that reads the same both ways draws no finding about it.
	_, out = mcpDraft(t, h, map[string]any{"yaml": `{"scenarios":[{"name":"x","type":"ROUND_TRIP",` +
		`"spec":{"topic":"kates-mcp-a","records":1000},"validate":{"maxP99LatencyMs":10}}]}`})
	if out.Verdict != "inside_envelope" || len(mcpScnFindingsOn(out, -1, "scenarios")) != 0 {
		t.Errorf("verdict %s, findings %+v", out.Verdict, out.Findings)
	}
	// JSON encoding/json refuses: apply refuses it as x.json.
	_, out = mcpDraft(t, h, map[string]any{"yaml": `{"scenarios":[{"name":"x","type":"ROUND_TRIP",` +
		`"spec":{"topic":"kates-mcp-a","records":1000},"validate":{"maxP99LatencyMs":10,"maxOutOfOrder":0.5}}]}`})
	mcpScnHasFinding(t, out, -1, "scenarios", mcpScnWarning, "save it as .yaml")

	// A case variant in a YAML file is invalid too: saved as .json, the same
	// scenario written as JSON would run it.
	_, out = mcpDraft(t, h, map[string]any{"yaml": "scenarios:\n  - {name: x, type: ROUND_TRIP, Backend: trogdor, spec: {topic: kates-mcp-a}}\n"})
	mcpScnHasFinding(t, out, 0, "Backend", mcpScnInvalid, "only in case")
	mcpScnOnlyPinCheck(t, fb)
}

// TestMCPDraftScenarioOneScenarioPerFile: kates test apply without --wait
// starts every scenario at once, so a file of several is outside the envelope
// however small each is.
func TestMCPDraftScenarioOneScenarioPerFile(t *testing.T) {
	fb := newMCPFakeBackend(t, "cluster-a")
	h := newMCPHarness(t, fb)
	one := "  - {name: x, type: ROUND_TRIP, spec: {topic: kates-mcp-a, records: 1000}, validate: {maxP99LatencyMs: 10}}\n"
	_, out := mcpDraft(t, h, map[string]any{"yaml": "scenarios:\n" + one})
	if out.Verdict != "inside_envelope" {
		t.Fatalf("one scenario: verdict %s; findings %+v", out.Verdict, out.Findings)
	}
	_, out = mcpDraft(t, h, map[string]any{"yaml": "scenarios:\n" + strings.Repeat(one, 5)})
	if out.Verdict != mcpScnOutside {
		t.Errorf("five scenarios: verdict %s, want %s", out.Verdict, mcpScnOutside)
	}
	mcpScnHasFinding(t, out, -1, "scenarios", mcpScnOutside, "--wait")
	mcpScnOnlyPinCheck(t, fb)
}

// TestMCPDraftScenarioFindingsWorstFirst: the cap keeps the findings the
// verdict rests on, however many warnings come before them in the file.
func TestMCPDraftScenarioFindingsWorstFirst(t *testing.T) {
	fb := newMCPFakeBackend(t, "cluster-a")
	h := newMCPHarness(t, fb)
	var b strings.Builder
	b.WriteString("scenarios:\n  - name: x\n    type: SPIKE\n    spec:\n")
	for i := 0; i < 70; i++ {
		fmt.Fprintf(&b, "      unknownKey%02d: %d\n", i, i)
	}
	env, out := mcpDraft(t, h, map[string]any{"yaml": b.String()})
	if out.Verdict != mcpScnOutside || !env.Truncated || len(out.Findings) != mcpDraftMaxFindings {
		t.Fatalf("verdict %s, truncated %v, %d findings", out.Verdict, env.Truncated, len(out.Findings))
	}
	mcpScnHasFinding(t, out, 0, "type", mcpScnOutside, "SPIKE runs its producer at an unlimited rate")
	mcpScnHasFinding(t, out, 0, "spec.topic", mcpScnOutside, "spike-test")
	for i := 1; i < len(out.Findings); i++ {
		if mcpScnLevelRank(out.Findings[i].Level) < mcpScnLevelRank(out.Findings[i-1].Level) {
			t.Fatalf("finding %d (%s) comes after a lesser one", i, out.Findings[i].Level)
		}
	}
	mcpScnOnlyPinCheck(t, fb)
}

// TestMCPDraftScenarioFitsAtTheLimits: a file at the input limits returns a
// verdict, not KATES_RESULT_TOO_LARGE: the echo of an unchanged file, then
// warnings, give way first.
func TestMCPDraftScenarioFitsAtTheLimits(t *testing.T) {
	fb := newMCPFakeBackend(t, "cluster-a")
	h := newMCPHarness(t, fb)

	// JSON escapes & as six bytes, and the text goes out twice.
	var b strings.Builder
	b.WriteString("scenarios:\n")
	for i := 0; i < mcpDraftMaxScenarios; i++ {
		fmt.Fprintf(&b, "  - name: s%d\n    type: LOAD\n    spec:\n", i)
		for k := 0; k < 12; k++ {
			fmt.Fprintf(&b, "      unknownKey%02d: x\n", k)
		}
	}
	b.WriteString("# ")
	b.WriteString(strings.Repeat("&", mcpDraftMaxYAMLBytes-b.Len()-1))
	b.WriteString("\n")
	if b.Len() != mcpDraftMaxYAMLBytes {
		t.Fatalf("the file is %d bytes, want %d", b.Len(), mcpDraftMaxYAMLBytes)
	}
	env, out := mcpDraft(t, h, map[string]any{"yaml": b.String()})
	if !out.YAMLOmitted || out.YAML != "" || !env.Truncated || out.Verdict != mcpScnOutside {
		t.Errorf("yamlOmitted %v, truncated %v, verdict %s", out.YAMLOmitted, env.Truncated, out.Verdict)
	}
	outside := 0
	for _, f := range out.Findings {
		if f.Level == mcpScnOutside {
			outside++
		}
	}
	// Per scenario the rate and the shared load-test topic, and the file's
	// several scenarios.
	if want := 2*mcpDraftMaxScenarios + 1; outside != want {
		t.Errorf("%d outside_envelope findings kept, want %d", outside, want)
	}
	res := h.call("draft_scenario", map[string]any{"yaml": b.String()})
	if size := 2 * len(mcpResultText(t, res)); size > mcpDefaultLimits.MaxResultBytes {
		t.Errorf("result is %d bytes on the wire, over %d", size, mcpDefaultLimits.MaxResultBytes)
	}

	// An edited file cannot be left out, and one that still does not fit is
	// refused as too large an input, not failed as too large a page.
	lone := "name: x\ntype: LOAD\n# "
	lone += strings.Repeat("&", mcpDraftMaxYAMLBytes-len(lone)-1) + "\n"
	if e := h.callErr("draft_scenario", map[string]any{"yaml": lone}); e.Error.Code != mcpErrInvalidArgument {
		t.Errorf("code %s, want %s", e.Error.Code, mcpErrInvalidArgument)
	}
	mcpScnOnlyPinCheck(t, fb)
}
