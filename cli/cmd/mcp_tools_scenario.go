package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/bmscomp/kates/cli/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"
)

// draft_scenario and the kates://scenarios/{name} resources (plan §4.1,
// §4.2). The tool is local compute: it parses a scenario file the way
// kates test apply does (apply.go:74-91), a JSON one both as a .json and as a
// YAML file, converts each scenario with
// scenarioToRequest (apply.go:194), and checks the result against the agent
// envelope of plan §5.4-§5.5. It saves nothing and sends the scenario
// nowhere; like every tool it runs behind the guard, whose pin check is the
// only request it causes.

func registerMCPScenarioTools(s *mcp.Server, deps *mcpDeps) {
	addReadTool(s, deps, &mcp.Tool{
		Name:        "draft_scenario",
		Title:       "Draft a test scenario",
		Description: mcpDraftScenarioDescription,
		InputSchema: mcpDraftScenarioInputSchema(),
	}, mcpDraftScenario,
		mcpCaveatAgentEnvelopeProposed, mcpCaveatScenarioShippedDefaults,
		mcpCaveatScenarioThroughputUnsettable, mcpCaveatScenarioValidateGrading)

	for _, meta := range builtinScenarios {
		data, err := scenarioFS.ReadFile("scenarios/" + meta.filename)
		if err != nil {
			panic(fmt.Sprintf("kates mcp: scenario template %s: %v", meta.name, err))
		}
		addStaticResource(s, deps, &mcp.Resource{
			URI:   mcpScenarioURIPrefix + meta.name,
			Name:  "scenario-" + meta.name,
			Title: "Scenario template " + meta.name,
			Description: "The built-in " + meta.testType + " scenario template " + meta.name + ", as kates test scaffold " +
				"export writes it. Check it, or a copy with overrides, with draft_scenario before anyone runs it.",
			MIMEType: "application/yaml",
		}, string(data))
	}
}

const mcpScenarioURIPrefix = "kates://scenarios/"

const mcpDraftScenarioDescription = "Checks a load-test scenario before anyone runs it. Takes a scenario file as " +
	"kates test apply -f reads it (YAML, or JSON, which must read the same as a .json and as a YAML file), or a " +
	"built-in template (kates://scenarios/{name}) with optional overrides. It parses the file as kates test apply " +
	"does, converts each scenario with the CLI's scenarioToRequest, fills what a scenario leaves out with the " +
	"per-type defaults Kates ships, and checks the result against the agent envelope the Kates MCP plan proposes: " +
	"one scenario per file (one test at a time), an allowed test type, a set producer rate under a cap, a byte " +
	"budget, at most 20 minutes, at most 50 partitions, a topic named kates-mcp-.... Returns the YAML, the request " +
	"each scenario would send, its effective spec, a verdict, and findings, worst first: invalid (the CLI or backend " +
	"would refuse it, it would run other than checked, or the run would fail at once), outside_envelope, and " +
	"warning (it runs, but not as written: fields the backend drops, keys nothing reads, thresholds that are never " +
	"graded). Warnings, then the echo of an unchanged yaml argument, are left out when the result would not fit. " +
	"A scenario file has no key for the producer rate, so most types run unlimited. Saves nothing, starts nothing " +
	"and sends the scenario nowhere; nothing enforces the envelope today, so the YAML is a draft for a human to " +
	"review before running it."

// The agent envelope: plan §5.4-§5.5 starting values. The plan names no rate
// cap, so mcpScnEnvMaxRecordsPerSec is this server's choice, stated in every
// result.
const (
	mcpScnEnvMaxStressProducers = 4
	mcpScnEnvMaxRecordsPerSec   = 20_000
	mcpScnEnvMaxBytes           = 10 << 30 // 10 GiB
	mcpScnEnvMaxDurationMs      = 20 * 60 * 1000
	mcpScnEnvMaxPartitions      = 50
	mcpScnEnvTopicPrefix        = "kates-mcp-"
	// mcpScnReaperMs is kates.engine.max-duration-ms (application.properties:278).
	mcpScnReaperMs = 30 * 60 * 1000
)

var mcpScnEnvTypes = []string{"LOAD", "ROUND_TRIP", "ENDURANCE", "INTEGRITY", "VOLUME", "STRESS"}

// mcpScnEnvRefused says why the envelope refuses the other test types.
var mcpScnEnvRefused = map[string]string{
	"SPIKE":            "SPIKE runs its producer at an unlimited rate whatever the spec says",
	"CAPACITY":         "CAPACITY runs every producer at an unlimited rate whatever the spec says",
	"TUNE_REPLICATION": "TUNE_* runs one produce task and ranks nothing until P-14 lands",
	"TUNE_ACKS":        "TUNE_* runs one produce task and ranks nothing until P-14 lands",
	"TUNE_BATCHING":    "TUNE_* runs one produce task and ranks nothing until P-14 lands",
	"TUNE_COMPRESSION": "TUNE_* runs one produce task and ranks nothing until P-14 lands",
	"TUNE_PARTITIONS":  "TUNE_* runs one produce task and ranks nothing until P-14 lands",
	"INTEGRATION_CDC":  "INTEGRATION_CDC connects to the first PostgreSQL Service it finds, in any namespace",
}

// mcpScnTestTypes is the backend's TestType enum (domain/TestType.java).
var mcpScnTestTypes = []string{
	"LOAD", "STRESS", "SPIKE", "ENDURANCE", "VOLUME", "CAPACITY", "ROUND_TRIP", "INTEGRITY",
	"TUNE_REPLICATION", "TUNE_ACKS", "TUNE_BATCHING", "TUNE_COMPRESSION", "TUNE_PARTITIONS", "INTEGRATION_CDC",
}

// mcpScnTypeDefaults are the defaults applyTypeDefaults fills in for the fields
// the envelope reads. They are the ones Kates ships: the @ConfigProperty
// fallbacks of TestTypeDefaults.java, the kates.tests.* lines of
// application.properties and the tests section of the kates chart, which
// agree (TestMCPScenarioShippedDefaultsMatchTheBackend checks all three).
type mcpScnTypeDefaults struct {
	partitions, recordSize, numProducers, throughput int
	numRecords, durationMs                           int64
}

var mcpScnShippedDefaults = map[string]mcpScnTypeDefaults{
	"LOAD":       {partitions: 3, recordSize: 1024, numProducers: 1, throughput: -1, numRecords: 1_000_000, durationMs: 600_000},
	"STRESS":     {partitions: 6, recordSize: 1024, numProducers: 3, throughput: -1, numRecords: 5_000_000, durationMs: 900_000},
	"SPIKE":      {partitions: 3, recordSize: 1024, numProducers: 1, throughput: -1, numRecords: 2_000_000, durationMs: 300_000},
	"ENDURANCE":  {partitions: 3, recordSize: 1024, numProducers: 1, throughput: 5000, numRecords: 10_000_000, durationMs: 3_600_000},
	"VOLUME":     {partitions: 6, recordSize: 10240, numProducers: 1, throughput: -1, numRecords: 2_000_000, durationMs: 600_000},
	"CAPACITY":   {partitions: 12, recordSize: 1024, numProducers: 5, throughput: -1, numRecords: 10_000_000, durationMs: 1_200_000},
	"ROUND_TRIP": {partitions: 3, recordSize: 1024, numProducers: 1, throughput: 10000, numRecords: 500_000, durationMs: 600_000},
}

// mcpScnTypeDefaultsFor returns a type's shipped defaults. INTEGRITY, TUNE_* and
// INTEGRATION_CDC use LOAD's (TestTypeDefaults.forType).
func mcpScnTypeDefaultsFor(t string) mcpScnTypeDefaults {
	if d, ok := mcpScnShippedDefaults[t]; ok {
		return d
	}
	return mcpScnShippedDefaults["LOAD"]
}

// Input limits: a scenario file an agent drafts is small, and the result
// carries it back with a request and findings per scenario. What still does
// not fit is left out by mcpScnFit.
const (
	mcpDraftMaxYAMLBytes = 8_000
	mcpDraftMaxScenarios = 5
	mcpDraftMaxFindings  = 60
)

type mcpDraftScenarioIn struct {
	YAML              string             `json:"yaml,omitempty" jsonschema:"a scenario file as kates test apply -f reads it: a scenarios list of name, type, spec and validate. JSON is checked both as kates test apply reads a .json file and as YAML, and must read the same both ways. Give this or template"`
	Template          string             `json:"template,omitempty" jsonschema:"a built-in scenario template to start from, as listed under kates://scenarios/. Give this or yaml"`
	Name              string             `json:"name,omitempty" jsonschema:"a new name for every scenario in the file"`
	SpecOverrides     map[string]any     `json:"spec_overrides,omitempty" jsonschema:"spec keys to set in every scenario, named as scenario files name them (records, parallelProducers, recordSizeBytes, durationSeconds, topic, partitions, ...); each a number, text or true/false"`
	ValidateOverrides map[string]float64 `json:"validate_overrides,omitempty" jsonschema:"validate thresholds to set in every scenario (maxP99LatencyMs, maxAvgLatencyMs, minThroughputRecPerSec, maxDataLossPercent, ...)"`
}

func mcpDraftScenarioInputSchema() any {
	s, err := mcpSchemaFor[mcpDraftScenarioIn]()
	if err != nil {
		panic(fmt.Sprintf("kates mcp: draft_scenario input schema: %v", err))
	}
	names := make([]any, 0, len(builtinScenarios))
	for _, m := range builtinScenarios {
		names = append(names, m.name)
	}
	s.Properties["template"].Enum = names
	maxYAML, maxName, maxSpec, maxValidate := mcpDraftMaxYAMLBytes, 200, 30, 20
	s.Properties["yaml"].MaxLength = &maxYAML
	s.Properties["name"].MaxLength = &maxName
	s.Properties["spec_overrides"].MaxProperties = &maxSpec
	s.Properties["validate_overrides"].MaxProperties = &maxValidate
	return s
}

// Finding levels.
const (
	mcpScnInvalid = "invalid"
	mcpScnOutside = "outside_envelope"
	mcpScnWarning = "warning"
)

type mcpDraftScenarioOut struct {
	YAML        string               `json:"yaml,omitempty" jsonschema:"the scenario file that was checked, ready for kates test apply -f; it was neither saved nor run"`
	YAMLOmitted bool                 `json:"yamlOmitted,omitempty" jsonschema:"true when yaml is left out to fit the result: the file checked is the yaml argument, unchanged"`
	Verdict     string               `json:"verdict" jsonschema:"invalid when a finding is invalid; outside_envelope when one is outside the envelope; inside_envelope otherwise, warnings included; it counts findings left out to fit"`
	Scenarios   []mcpDraftedScenario `json:"scenarios"`
	Findings    []mcpScnFinding      `json:"findings" jsonschema:"invalid first, then outside_envelope, then warning, each in file order; file-level findings have scenario -1; warnings are the first left out when the result would not fit"`
	Envelope    mcpScnEnvelope       `json:"envelope"`
}

type mcpDraftedScenario struct {
	Index     int                      `json:"index" jsonschema:"position in the file, from 0"`
	Name      string                   `json:"name"`
	Request   client.CreateTestRequest `json:"request" jsonschema:"the body kates test apply would POST to /api/tests, as scenarioToRequest builds it; zero values are left out"`
	Effective *mcpDraftEffective       `json:"effective,omitempty" jsonschema:"the request merged with the shipped defaults of its type, as the backend would merge it; absent when the type is unknown"`
}

type mcpDraftEffective struct {
	Type          string   `json:"type"`
	Topic         string   `json:"topic" jsonschema:"the topic the run writes to: the one given, or <type>-test, which every run of the type shares"`
	NumRecords    int64    `json:"numRecords"`
	RecordSize    int      `json:"recordSize"`
	NumProducers  int      `json:"numProducers"`
	ProducerTasks int      `json:"producerTasks" jsonschema:"producers the backend starts: numProducers for STRESS and CAPACITY, one for every other type"`
	Throughput    int      `json:"throughput" jsonschema:"records per second per producer; -1 is unlimited"`
	DurationMs    int64    `json:"durationMs" jsonschema:"the most the run lasts: its producer stops at numRecords or at this deadline, whichever comes first"`
	Partitions    int      `json:"partitions"`
	RecordsPerSec float64  `json:"recordsPerSecond" jsonschema:"numProducers × throughput, the envelope's rate; -1 when unlimited"`
	Bytes         float64  `json:"bytes" jsonschema:"numProducers × numRecords × recordSize, the envelope's volume"`
	Defaulted     []string `json:"defaulted" jsonschema:"fields the request leaves out, taken from the shipped defaults; a deployment can set other defaults"`
}

type mcpScnFinding struct {
	Scenario int    `json:"scenario" jsonschema:"index of the scenario, or -1 for the file"`
	Level    string `json:"level" jsonschema:"invalid, outside_envelope or warning"`
	Field    string `json:"field,omitempty" jsonschema:"the key, as spec.records or validate.maxErrorRate"`
	Message  string `json:"message"`
}

type mcpScnEnvelope struct {
	Types               []string `json:"types" jsonschema:"test types allowed"`
	MaxStressProducers  int      `json:"maxStressProducers"`
	MaxRecordsPerSecond int      `json:"maxRecordsPerSecond" jsonschema:"cap on numProducers × throughput; the plan names none, so this is this server's"`
	MaxBytes            int64    `json:"maxBytes"`
	MaxDurationMs       int64    `json:"maxDurationMs"`
	MaxPartitions       int      `json:"maxPartitions"`
	TopicPrefix         string   `json:"topicPrefix"`
	Source              string   `json:"source"`
}

func mcpScnEnvelopeLimits() mcpScnEnvelope {
	return mcpScnEnvelope{
		Types:               append([]string(nil), mcpScnEnvTypes...),
		MaxStressProducers:  mcpScnEnvMaxStressProducers,
		MaxRecordsPerSecond: mcpScnEnvMaxRecordsPerSec,
		MaxBytes:            mcpScnEnvMaxBytes,
		MaxDurationMs:       mcpScnEnvMaxDurationMs,
		MaxPartitions:       mcpScnEnvMaxPartitions,
		TopicPrefix:         mcpScnEnvTopicPrefix,
		Source: "The starting values plan §5.4-§5.5 proposes for runs an agent starts, one agent test at a time; " +
			"nothing enforces them today.",
	}
}

func mcpDraftScenario(_ context.Context, call *mcpCall, in mcpDraftScenarioIn) (mcpDraftScenarioOut, error) {
	source, err := mcpScnSource(in)
	if err != nil {
		return mcpDraftScenarioOut{}, err
	}
	var fs mcpScnFindings
	text, err := mcpScnEdit(source, in, &fs)
	if err != nil {
		return mcpDraftScenarioOut{}, err
	}
	// The checks read text, and the result returns it as it is: one text, so
	// that what was checked is what kates test apply would run. The source is
	// clean and so is every scalar in it (mcpScnCleanScalars), so re-encoding
	// has nothing to write that sanitizing would change; this holds the line
	// if that ever stops being so, rather than return a text other than the
	// one checked.
	if mcpSanitize(text, 0) != text {
		return mcpDraftScenarioOut{}, &mcpToolError{
			Code:    mcpErrInternal,
			Message: "draft_scenario produced a scenario file it cannot return as it is, so it checked nothing. This is a bug in kates mcp.",
		}
	}
	scenarios, raw, err := mcpScnParseDraft(text)
	if err != nil {
		return mcpDraftScenarioOut{}, err
	}
	out := mcpDraftScenarioOut{
		YAML:      text,
		Scenarios: make([]mcpDraftedScenario, 0, len(scenarios)),
		Envelope:  mcpScnEnvelopeLimits(),
	}
	fs.file(raw)
	mcpScnCheckJSON(text, scenarios, &fs)
	if len(scenarios) > 1 {
		fs.add(-1, mcpScnOutside, "scenarios", fmt.Sprintf("the file holds %d scenarios, and the envelope allows one agent "+
			"test at a time: kates test apply without --wait starts them all at once, and nothing in the file can require "+
			"--wait, so draft one scenario per file", len(scenarios)))
	}
	for i, sc := range scenarios {
		out.Scenarios = append(out.Scenarios, mcpScnCheckScenario(call, i, sc, mcpScnRawScenario(raw, i), &fs))
	}
	call.Caveat(fs.caveats...)
	out.Verdict = fs.verdict()
	fs.sortWorstFirst()
	out.Findings = mcpCap(call, fs.list, mcpDraftMaxFindings)
	if err := mcpScnFit(call, &out, in.YAML != "" && text == source); err != nil {
		return mcpDraftScenarioOut{}, err
	}
	return out, nil
}

// mcpScnFit makes the result fit one response, marking it truncated: first it
// leaves out the echo of a file the caller sent and nothing changed, which
// the caller already holds, then warnings from the end. Invalid and
// outside_envelope findings, which the verdict rests on, are always kept.
func mcpScnFit(call *mcpCall, out *mcpDraftScenarioOut, echoIsInput bool) error {
	for !call.Fits(out) {
		last := len(out.Findings) - 1
		switch {
		case echoIsInput && !out.YAMLOmitted:
			out.YAML, out.YAMLOmitted = "", true
		case last >= 0 && out.Findings[last].Level == mcpScnWarning:
			out.Findings = out.Findings[:last]
		default:
			return mcpInvalidArgument("The findings for this scenario file would not fit one result, even without its "+
				"warnings; draft a shorter file, one scenario at a time.", "")
		}
		call.MarkTruncated()
	}
	return nil
}

// mcpScnSource returns the scenario text to start from.
func mcpScnSource(in mcpDraftScenarioIn) (string, error) {
	switch {
	case in.YAML != "" && in.Template != "":
		return "", mcpInvalidArgument("Give yaml or template, not both.", "")
	case in.Template != "":
		meta := findScenario(in.Template)
		if meta == nil || meta.name != in.Template {
			return "", mcpInvalidArgument("template must name a built-in scenario template (kates://scenarios/).", in.Template)
		}
		data, err := scenarioFS.ReadFile("scenarios/" + meta.filename)
		if err != nil {
			return "", err
		}
		return string(data), nil
	case strings.TrimSpace(in.YAML) == "":
		return "", mcpInvalidArgument("Give yaml (a scenario file) or template (a built-in template name).", "")
	case len(in.YAML) > mcpDraftMaxYAMLBytes:
		return "", mcpInvalidArgument(fmt.Sprintf("yaml is longer than %d bytes.", mcpDraftMaxYAMLBytes), "")
	}
	if !mcpScnCleanText(in.YAML) {
		return "", mcpInvalidArgument("yaml holds control, format or fence characters; a scenario file has no use for them.", "")
	}
	return strings.ReplaceAll(in.YAML, "\r\n", "\n"), nil
}

// mcpScnCleanText reports whether s is text mcpSanitize leaves as it is, CR LF
// line ends apart: no control, format or fence characters.
func mcpScnCleanText(s string) bool {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return mcpSanitize(s, 0) == s
}

var mcpScnOverrideKeyRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)

// mcpScnEdit applies the overrides and the new name to every scenario, and
// puts a lone scenario under scenarios:, where kates test apply looks for it.
// Unchanged text comes back as it was, comments and all; an edited file is
// re-encoded from its YAML tree, which keeps comments and key order.
func mcpScnEdit(text string, in mcpDraftScenarioIn, fs *mcpScnFindings) (string, error) {
	if err := mcpScnCheckOverrides(in); err != nil {
		return "", err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return "", mcpScnNotAScenarioFile(err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return "", mcpInvalidArgument("The YAML is not a scenario file: it needs a scenarios list of name, type, spec and validate.", "")
	}
	if !mcpScnCleanScalars(&doc) {
		return "", mcpInvalidArgument("yaml holds an escape that decodes to a control, format or fence character, such as "+
			"\"\\u200b\"; scenario keys and values have no use for one.", "")
	}
	edited := false
	root := doc.Content[0]
	if mcpScnMapValue(root, "scenarios") == nil && mcpScnMapValue(root, "type") != nil {
		root = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "scenarios"},
			{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{doc.Content[0]}},
		}}
		doc.Content[0] = root
		edited = true
		fs.add(-1, mcpScnWarning, "scenarios", "the scenario was not under scenarios:, so kates test apply would have "+
			"found none in it and stopped; the returned YAML puts it there")
	}
	if in.Name != "" || len(in.SpecOverrides) > 0 || len(in.ValidateOverrides) > 0 {
		if err := mcpScnApplyOverrides(root, in); err != nil {
			return "", err
		}
		edited = true
	}
	if !edited {
		return text, nil
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// mcpScnCleanScalars reports whether every key and value in the tree decodes
// to text mcpSanitize leaves as it is. The file is plain text by then, but a
// YAML escape such as "\u200b" decodes to a format character: the checks
// would read a key that is not records, while the result, once re-encoded
// (which writes the character raw) and cleaned for display, would show one
// that is.
func mcpScnCleanScalars(n *yaml.Node) bool {
	if n.Kind == yaml.ScalarNode && mcpSanitize(n.Value, 0) != n.Value {
		return false
	}
	for _, c := range n.Content {
		if !mcpScnCleanScalars(c) {
			return false
		}
	}
	return true
}

func mcpScnCheckOverrides(in mcpDraftScenarioIn) error {
	if in.Name != "" && (!mcpScnCleanText(in.Name) || strings.ContainsAny(in.Name, "\n\r\t") || len(in.Name) > 200) {
		return mcpInvalidArgument("name must be one line of plain text, at most 200 bytes.", "")
	}
	for k, v := range in.SpecOverrides {
		if !mcpScnOverrideKeyRE.MatchString(k) {
			return mcpInvalidArgument("spec_overrides keys are spec key names, such as records or durationSeconds.", mcpSanitizeLine(k, 64))
		}
		switch x := v.(type) {
		case float64, bool:
		case string:
			if !mcpScnCleanText(x) || len(x) > 300 {
				return mcpInvalidArgument("spec_overrides."+k+" must be plain text of at most 300 bytes.", "")
			}
		default:
			return mcpInvalidArgument("spec_overrides."+k+" must be a number, text or true/false.", "")
		}
	}
	for k := range in.ValidateOverrides {
		if !mcpScnOverrideKeyRE.MatchString(k) {
			return mcpInvalidArgument("validate_overrides keys are threshold names, such as maxP99LatencyMs.", mcpSanitizeLine(k, 64))
		}
	}
	return nil
}

// mcpScnApplyOverrides sets the name, spec and validate overrides on every
// scenario of root's scenarios list, in key order.
func mcpScnApplyOverrides(root *yaml.Node, in mcpDraftScenarioIn) error {
	seq := mcpScnMapValue(root, "scenarios")
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return mcpInvalidArgument("Overrides need a scenario file whose scenarios is a list.", "")
	}
	for _, sc := range seq.Content {
		if sc.Kind != yaml.MappingNode {
			return mcpInvalidArgument("Overrides need every entry of scenarios to be a mapping.", "")
		}
		if in.Name != "" {
			mcpScnSetMapValue(sc, "name", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: in.Name})
		}
		if err := mcpScnOverrideSection(sc, "spec", mcpScnNumbersAsInts(in.SpecOverrides)); err != nil {
			return err
		}
		validate := make(map[string]any, len(in.ValidateOverrides))
		for k, v := range in.ValidateOverrides {
			validate[k] = v
		}
		if err := mcpScnOverrideSection(sc, "validate", mcpScnNumbersAsInts(validate)); err != nil {
			return err
		}
	}
	return nil
}

// mcpScnNumbersAsInts writes whole numbers from JSON as integers, as a person
// would write them in a scenario file.
func mcpScnNumbersAsInts(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if f, ok := v.(float64); ok && f == math.Trunc(f) && math.Abs(f) < 1e15 {
			out[k] = int64(f)
			continue
		}
		out[k] = v
	}
	return out
}

func mcpScnOverrideSection(sc *yaml.Node, section string, values map[string]any) error {
	if len(values) == 0 {
		return nil
	}
	m := mcpScnMapValue(sc, section)
	if m == nil || m.Kind != yaml.MappingNode {
		m = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		mcpScnSetMapValue(sc, section, m)
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		var n yaml.Node
		if err := n.Encode(values[k]); err != nil {
			return err
		}
		mcpScnSetMapValue(m, k, &n)
	}
	return nil
}

func mcpScnMapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func mcpScnSetMapValue(m *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = v
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, v)
}

func mcpScnNotAScenarioFile(err error) error {
	return &mcpToolError{
		Code:    mcpErrInvalidArgument,
		Message: "kates test apply could not read this as a scenario file; the detail says where.",
		Detail:  err.Error(),
	}
}

// mcpScnParseFile reads a scenario file as kates test apply reads a YAML
// one (apply.go:74-91): a scenarios list, or failing that one scenario with a
// type. yaml.v3 ignores keys the structs do not name.
func mcpScnParseFile(data []byte) ([]TestScenario, error) {
	var sf ScenarioFile
	if err := yaml.Unmarshal(data, &sf); err != nil {
		var single TestScenario
		if yaml.Unmarshal(data, &single) == nil && single.Type != "" {
			return []TestScenario{single}, nil
		}
		return nil, err
	}
	return sf.Scenarios, nil
}

// mcpScnLooksJSON reports whether text is JSON, a file someone would save as
// .json: its first byte other than white space opens an object or a list.
func mcpScnLooksJSON(text string) bool {
	t := strings.TrimLeft(text, " \t\r\n")
	return strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[")
}

// mcpScnParseJSONFile reads a scenario file as kates test apply reads a .json
// one (apply.go:74-91): with encoding/json, which matches a key to a field in
// any case and lets a later key replace an earlier one, and failing that as
// one scenario in YAML.
func mcpScnParseJSONFile(data []byte) ([]TestScenario, error) {
	var sf ScenarioFile
	if err := json.Unmarshal(data, &sf); err != nil {
		var single TestScenario
		if yaml.Unmarshal(data, &single) == nil && single.Type != "" {
			return []TestScenario{single}, nil
		}
		return nil, err
	}
	return sf.Scenarios, nil
}

// mcpScnCheckJSON flags JSON that kates test apply would read, from a file
// named .json, as other scenarios than the ones checked here, which read it
// as YAML.
func mcpScnCheckJSON(text string, checked []TestScenario, fs *mcpScnFindings) {
	if !mcpScnLooksJSON(text) {
		return
	}
	asJSON, err := mcpScnParseJSONFile([]byte(text))
	switch {
	case err != nil:
		fs.add(-1, mcpScnWarning, "scenarios", "kates test apply refuses this file when it is named .json (encoding/json "+
			"cannot read it), so save it as .yaml")
	case !mcpScnSameScenarios(checked, asJSON):
		fs.add(-1, mcpScnInvalid, "scenarios", "kates test apply reads this file as other scenarios when it is named "+
			".json: encoding/json matches keys to fields in any case and lets a later key replace an earlier one, so "+
			"what it would run is not what was checked here")
	}
}

// mcpScnSameScenarios reports whether two readings of a file would run the
// same tests and grade them the same way.
func mcpScnSameScenarios(a, b []TestScenario) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || !reflect.DeepEqual(a[i].Validate, b[i].Validate) ||
			!reflect.DeepEqual(scenarioToRequest(a[i]), scenarioToRequest(b[i])) {
			return false
		}
	}
	return true
}

// mcpScnParseDraft parses the text twice: as kates test apply does, which is
// what runs, and as plain maps, which shows the keys apply drops.
func mcpScnParseDraft(text string) ([]TestScenario, map[string]any, error) {
	scenarios, err := mcpScnParseFile([]byte(text))
	if err != nil {
		return nil, nil, mcpScnNotAScenarioFile(err)
	}
	switch {
	case len(scenarios) == 0:
		return nil, nil, mcpInvalidArgument("kates test apply finds no scenarios in this file: it needs a scenarios list.", "")
	case len(scenarios) > mcpDraftMaxScenarios:
		return nil, nil, mcpInvalidArgument(fmt.Sprintf("The file holds %d scenarios; draft at most %d at a time.", len(scenarios), mcpDraftMaxScenarios), "")
	}
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(text), &raw); err != nil {
		return nil, nil, mcpScnNotAScenarioFile(err)
	}
	return scenarios, raw, nil
}

func mcpScnRawScenario(raw map[string]any, i int) map[string]any {
	list, _ := raw["scenarios"].([]any)
	if i < len(list) {
		if m, ok := list[i].(map[string]any); ok {
			return m
		}
	}
	return map[string]any{}
}

// mcpScnFindings collects findings, and the caveats they call for.
type mcpScnFindings struct {
	list    []mcpScnFinding
	caveats []mcpCaveatID
}

func (f *mcpScnFindings) add(scenario int, level, field, message string) {
	f.list = append(f.list, mcpScnFinding{
		Scenario: scenario,
		Level:    level,
		Field:    mcpSanitizeLine(field, 80),
		Message:  mcpSanitizeLine(message, 500),
	})
}

// mcpScnLevelRank orders finding levels worst first.
func mcpScnLevelRank(level string) int {
	switch level {
	case mcpScnInvalid:
		return 0
	case mcpScnOutside:
		return 1
	}
	return 2
}

// sortWorstFirst puts invalid findings first, then outside_envelope, then
// warnings, keeping file order within a level, so that a cap or a cut to fit
// removes warnings and never what the verdict rests on.
func (f *mcpScnFindings) sortWorstFirst() {
	sort.SliceStable(f.list, func(i, j int) bool { return mcpScnLevelRank(f.list[i].Level) < mcpScnLevelRank(f.list[j].Level) })
}

func (f *mcpScnFindings) verdict() string {
	v := "inside_envelope"
	for _, x := range f.list {
		switch x.Level {
		case mcpScnInvalid:
			return mcpScnInvalid
		case mcpScnOutside:
			v = mcpScnOutside
		}
	}
	return v
}

// file flags top-level keys kates test apply does not read.
func (f *mcpScnFindings) file(raw map[string]any) {
	for _, k := range mcpScnSortedKeys(raw) {
		switch {
		case k == "scenarios":
		case f.caseVariant(-1, "", k, mcpScnFileFields):
		default:
			f.add(-1, mcpScnWarning, k, "kates test apply ignores the top-level key "+strconv.Quote(k))
		}
	}
}

// The field names kates test apply reads, as its structs name them
// (apply.go:19-41).
var (
	mcpScnFileFields     = []string{"scenarios"}
	mcpScnScenarioFields = []string{"name", "type", "backend", "spec", "validate"}
)

// caseVariant flags a key that names one of fields in another case, and
// reports whether it did. kates test apply ignores such a key in a YAML file,
// but encoding/json, which reads a .json one, matches keys to fields in any
// case, so there the key replaces the field it names.
func (f *mcpScnFindings) caseVariant(scenario int, prefix, key string, fields []string) bool {
	for _, field := range fields {
		if key != field && strings.EqualFold(key, field) {
			f.add(scenario, mcpScnInvalid, prefix+key, strconv.Quote(key)+" differs from "+prefix+field+" only in case: "+
				"kates test apply ignores it in a YAML file, but in a .json file it replaces "+prefix+field+"; name it "+field)
			return true
		}
	}
	return false
}

func mcpScnSortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// mcpScnShow renders a value from a scenario file for a message.
func mcpScnShow(v any) string {
	switch x := v.(type) {
	case nil:
		return "empty"
	case string:
		if len(x) > 60 {
			x = x[:60] + "…"
		}
		return strconv.Quote(x)
	case map[string]any:
		return "a mapping"
	case []any:
		return "a list"
	}
	return fmt.Sprintf("%v", v)
}

func mcpScnCheckScenario(call *mcpCall, i int, sc TestScenario, raw map[string]any, fs *mcpScnFindings) mcpDraftedScenario {
	req := scenarioToRequest(sc)
	out := mcpDraftedScenario{Index: i, Name: mcpSanitizeLine(sc.Name, 200), Request: mcpScnDisplayRequest(req)}
	for _, k := range mcpScnSortedKeys(raw) {
		switch k {
		case "name", "type", "backend", "spec", "validate":
		case "phases", "scenario", "steps":
			fs.add(i, mcpScnWarning, k, "kates test apply ignores "+strconv.Quote(k)+": scenarioToRequest builds no scenario "+
				"phases, so the run is one plain "+req.TestType+" run")
		default:
			if !fs.caseVariant(i, "", k, mcpScnScenarioFields) {
				fs.add(i, mcpScnWarning, k, "kates test apply ignores the scenario key "+strconv.Quote(k))
			}
		}
	}
	spec, _ := raw["spec"].(map[string]any)
	for _, k := range mcpScnSortedKeys(spec) {
		mcpScnCheckSpecKey(i, k, spec[k], req.TestType, fs)
	}
	if !mcpScnCheckTypeAndBackend(i, req, fs) {
		mcpScnCheckValidate(i, req, raw, fs)
		return out
	}
	out.Effective = mcpScnEffectiveOf(req)
	mcpScnCheckEnvelope(call, i, out.Effective, fs)
	mcpScnCheckValidate(i, req, raw, fs)
	return out
}

// mcpScnDisplayRequest is the request with its text cleaned for the result. A
// YAML escape can put control characters in a value; the checks flag such a
// value, and the result shows it cleaned.
func mcpScnDisplayRequest(req *client.CreateTestRequest) client.CreateTestRequest {
	r := *req
	r.TestType = mcpSanitizeLine(r.TestType, 64)
	r.Backend = mcpSanitizeLine(r.Backend, 64)
	if req.Spec != nil {
		s := *req.Spec
		s.Topic = mcpSanitizeLine(s.Topic, 300)
		s.Acks = mcpSanitizeLine(s.Acks, 64)
		s.CompressionType = mcpSanitizeLine(s.CompressionType, 64)
		s.ConsumerGroup = mcpSanitizeLine(s.ConsumerGroup, 300)
		r.Spec = &s
	}
	return r
}

// mcpScnCheckTypeAndBackend flags a type or backend the backend refuses, and
// reports whether the type is one it knows.
func mcpScnCheckTypeAndBackend(i int, req *client.CreateTestRequest, fs *mcpScnFindings) bool {
	known := false
	for _, t := range mcpScnTestTypes {
		known = known || t == req.TestType
	}
	switch {
	case req.TestType == "":
		fs.add(i, mcpScnInvalid, "type", "type is missing; the backend refuses a test without one")
	case !known:
		fs.add(i, mcpScnInvalid, "type", "the backend has no test type "+strconv.Quote(req.TestType)+" (it has "+strings.Join(mcpScnTestTypes, ", ")+")")
	}
	switch req.Backend {
	case "", "native", "trogdor":
	default:
		fs.add(i, mcpScnInvalid, "backend", "the backend has no benchmark backend "+strconv.Quote(req.Backend)+"; it has native and trogdor")
	}
	if req.Backend == "trogdor" && (req.TestType == "INTEGRITY" || req.TestType == "INTEGRATION_CDC") {
		fs.add(i, mcpScnInvalid, "backend", req.TestType+" runs need the native backend; on trogdor the run fails as it starts")
	}
	return known
}

// mcpScnSpecKey describes one spec key scenarioToRequest reads (apply.go:199-263)
// and the range the backend accepts for the field it becomes
// (domain/TestSpec.java:16-85).
type mcpScnSpecKey struct {
	wire     string
	kind     byte // 'i' number, 's' text, 'b' true/false
	scale    int64
	min, max int64
	pattern  *regexp.Regexp
	dropped  string // why applyTypeDefaults drops it, when it does
}

var (
	mcpScnTopicRE       = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,249}$`)
	mcpScnAcksRE        = regexp.MustCompile(`^(all|-1|0|1)$`)
	mcpScnCompressionRE = regexp.MustCompile(`^(none|gzip|snappy|lz4|zstd)$`)
	mcpScnGroupRE       = regexp.MustCompile(`^(?s).{0,255}$`)
)

const (
	mcpScnDroppedFlags = "the backend drops it when it merges the spec, so every run uses enableIdempotence false, " +
		"enableTransactions false and enableCrc true whatever the scenario says"
	mcpScnDroppedFetch = "the backend drops it when it merges the spec, so the consumers use their defaults"
)

var mcpScnSpecKeys = map[string]mcpScnSpecKey{
	"records":            {wire: "numRecords", kind: 'i', min: 1, max: 1_000_000_000},
	"parallelProducers":  {wire: "numProducers", kind: 'i', min: 1, max: 100},
	"recordSizeBytes":    {wire: "recordSize", kind: 'i', min: 1, max: 104_857_600},
	"durationSeconds":    {wire: "durationMs", kind: 'i', scale: 1000, min: 1_000, max: 86_400_000},
	"topic":              {wire: "topic", kind: 's', pattern: mcpScnTopicRE},
	"acks":               {wire: "acks", kind: 's', pattern: mcpScnAcksRE},
	"batchSize":          {wire: "batchSize", kind: 'i', min: 0, max: 134_217_728},
	"lingerMs":           {wire: "lingerMs", kind: 'i', min: 0, max: 300_000},
	"compressionType":    {wire: "compressionType", kind: 's', pattern: mcpScnCompressionRE},
	"numConsumers":       {wire: "numConsumers", kind: 'i', min: 0, max: 100},
	"replicationFactor":  {wire: "replicationFactor", kind: 'i', min: 1, max: 10},
	"partitions":         {wire: "partitions", kind: 'i', min: 1, max: 10_000},
	"minInsyncReplicas":  {wire: "minInsyncReplicas", kind: 'i', min: 1, max: 10},
	"consumerGroup":      {wire: "consumerGroup", kind: 's', pattern: mcpScnGroupRE, dropped: "the backend drops it when it merges the spec: an INTEGRITY run uses the group integrity-cg, and other runs name their own groups"},
	"targetThroughput":   {wire: "targetThroughput", kind: 'i', min: -1, max: math.MaxInt32, dropped: "the backend drops it when it merges the spec, so it never sets the rate"},
	"fetchMinBytes":      {wire: "fetchMinBytes", kind: 'i', min: 1, max: math.MaxInt32, dropped: mcpScnDroppedFetch},
	"fetchMaxWaitMs":     {wire: "fetchMaxWaitMs", kind: 'i', min: 0, max: 300_000, dropped: mcpScnDroppedFetch},
	"enableIdempotence":  {wire: "enableIdempotence", kind: 'b', dropped: mcpScnDroppedFlags},
	"enableTransactions": {wire: "enableTransactions", kind: 'b', dropped: mcpScnDroppedFlags},
	"enableCrc":          {wire: "enableCrc", kind: 'b', dropped: mcpScnDroppedFlags},
}

// mcpScnSpecKeyHints names the scenario key for the backend names an agent may
// reach for, which scenarioToRequest does not read.
var mcpScnSpecKeyHints = map[string]string{
	"throughput":   "no scenario key sets the producer rate",
	"numRecords":   "the scenario key is records",
	"numProducers": "the scenario key is parallelProducers",
	"recordSize":   "the scenario key is recordSizeBytes",
	"durationMs":   "the scenario key is durationSeconds, in seconds",
	"duration":     "the scenario key is durationSeconds, in seconds",
}

func mcpScnCheckSpecKey(i int, key string, v any, testType string, fs *mcpScnFindings) {
	field := "spec." + key
	k, ok := mcpScnSpecKeys[key]
	if !ok {
		msg := "scenarioToRequest does not read " + field + ", so it is not sent"
		if hint := mcpScnSpecKeyHints[key]; hint != "" {
			msg += "; " + hint
		}
		fs.add(i, mcpScnWarning, field, msg)
		return
	}
	if k.dropped != "" {
		fs.add(i, mcpScnWarning, field, k.dropped)
		fs.caveats = append(fs.caveats, mcpCaveatMergedSpecOnly)
	}
	switch k.kind {
	case 'i':
		mcpScnCheckSpecNumber(i, field, k, v, fs)
		mcpScnCheckSpecEffect(i, key, v, testType, fs)
	case 's':
		sent := fmt.Sprintf("%v", v)
		switch {
		case sent == "":
			fs.add(i, mcpScnWarning, field, "it is empty, so it is left out of the request and the type default applies")
		case !k.pattern.MatchString(sent):
			fs.add(i, mcpScnInvalid, field, "it is sent as "+mcpScnShow(sent)+", which the backend refuses for "+k.wire)
		}
	case 'b':
		switch x := v.(type) {
		case bool:
		case string:
			if x != "true" && x != "false" {
				fs.add(i, mcpScnWarning, field, "it is "+mcpScnShow(v)+", which scenarioToRequest reads as false")
			}
		default:
			fs.add(i, mcpScnWarning, field, "it is "+mcpScnShow(v)+", which scenarioToRequest reads as false")
		}
	}
}

// mcpScnCheckSpecNumber flags a number scenarioToRequest reads as 0 (and so
// leaves out), cuts, or sends outside the range the backend accepts.
func mcpScnCheckSpecNumber(i int, field string, k mcpScnSpecKey, v any, fs *mcpScnFindings) {
	var n int64
	switch x := v.(type) {
	case int:
		n = int64(x)
	case float64:
		if math.Abs(x) > 1e15 {
			fs.add(i, mcpScnInvalid, field, "it is "+mcpScnShow(v)+", too large for the backend")
			return
		}
		n = int64(x)
		if float64(n) != x {
			fs.add(i, mcpScnWarning, field, fmt.Sprintf("it is %v, which scenarioToRequest cuts to %d", x, n))
		}
	default:
		fs.add(i, mcpScnWarning, field, "it is "+mcpScnShow(v)+", which scenarioToRequest reads as 0, so it is left out "+
			"of the request and the type default applies")
		return
	}
	scale := max(k.scale, 1)
	sent := n * scale
	switch {
	case sent == 0:
		fs.add(i, mcpScnWarning, field, "0 is left out of the request (the field is omitted when zero), so the type default applies")
	case sent < k.min || sent > k.max:
		fs.add(i, mcpScnInvalid, field, fmt.Sprintf("it is sent as %s %d, which the backend refuses (it accepts %d to %d)", k.wire, sent, k.min, k.max))
	}
}

// mcpScnCheckSpecEffect flags counts the backend does not use for the type.
func mcpScnCheckSpecEffect(i int, key string, v any, testType string, fs *mcpScnFindings) {
	n, _ := v.(int)
	switch {
	case key == "parallelProducers" && n > 1 && testType != "STRESS" && testType != "CAPACITY":
		fs.add(i, mcpScnWarning, "spec."+key, "a "+testType+" run starts one producer whatever parallelProducers says; "+
			"only STRESS and CAPACITY start one per producer")
		if testType == "LOAD" {
			fs.caveats = append(fs.caveats, mcpCaveatLoadSingleProducer)
		}
	case key == "numConsumers" && n > 1:
		fs.add(i, mcpScnWarning, "spec."+key, "numConsumers has no effect: LOAD and ENDURANCE runs start one consumer, "+
			"and no other type starts a separate one")
	}
}

// mcpScnEffectiveOf merges the request with the shipped defaults of its type,
// for the fields the envelope reads, as applyTypeDefaults would.
func mcpScnEffectiveOf(req *client.CreateTestRequest) *mcpDraftEffective {
	d := mcpScnTypeDefaultsFor(req.TestType)
	spec := client.TestSpec{}
	if req.Spec != nil {
		spec = *req.Spec
	}
	e := &mcpDraftEffective{Type: req.TestType, Defaulted: []string{}}
	pick := func(name string, v, def int64) int64 {
		if v != 0 {
			return v
		}
		e.Defaulted = append(e.Defaulted, name)
		return def
	}
	e.NumRecords = pick("numRecords", int64(spec.Records), d.numRecords)
	e.RecordSize = int(pick("recordSize", int64(spec.RecordSizeBytes), int64(d.recordSize)))
	e.NumProducers = int(pick("numProducers", int64(spec.ParallelProducers), int64(d.numProducers)))
	e.DurationMs = pick("durationMs", int64(spec.DurationMs), d.durationMs)
	e.Partitions = int(pick("partitions", int64(spec.Partitions), int64(d.partitions)))
	e.Throughput = d.throughput
	e.Defaulted = append(e.Defaulted, "throughput")
	e.Topic = spec.Topic
	if e.Topic == "" {
		e.Topic = strings.ToLower(req.TestType) + "-test"
		e.Defaulted = append(e.Defaulted, "topic")
	}
	e.Topic = mcpSanitizeLine(e.Topic, 300)
	e.ProducerTasks = 1
	if req.TestType == "STRESS" || req.TestType == "CAPACITY" {
		e.ProducerTasks = e.NumProducers
	}
	e.RecordsPerSec = -1
	if e.Throughput > 0 {
		e.RecordsPerSec = float64(e.NumProducers) * float64(e.Throughput)
	}
	e.Bytes = float64(e.NumProducers) * float64(e.NumRecords) * float64(e.RecordSize)
	return e
}

// mcpScnCheckEnvelope checks the effective spec against the agent envelope.
func mcpScnCheckEnvelope(call *mcpCall, i int, e *mcpDraftEffective, fs *mcpScnFindings) {
	t := e.Type
	allowed := false
	for _, a := range mcpScnEnvTypes {
		allowed = allowed || a == t
	}
	if !allowed {
		fs.add(i, mcpScnOutside, "type", t+" is outside the envelope: "+mcpScnEnvRefused[t])
	}
	if t == "STRESS" && e.NumProducers > mcpScnEnvMaxStressProducers {
		fs.add(i, mcpScnOutside, "spec.parallelProducers", fmt.Sprintf("STRESS starts %d producers; the envelope allows %d", e.NumProducers, mcpScnEnvMaxStressProducers))
	}
	switch {
	case e.Throughput <= 0 && allowed:
		fs.add(i, mcpScnOutside, "spec", "the rate is unlimited: a "+t+" run uses its type's default throughput, -1 "+
			"(unlimited) as Kates ships, and a scenario file has no key that sets throughput")
	case e.RecordsPerSec > mcpScnEnvMaxRecordsPerSec:
		fs.add(i, mcpScnOutside, "spec", fmt.Sprintf("numProducers × throughput is %.0f records/s; the envelope allows %d",
			e.RecordsPerSec, mcpScnEnvMaxRecordsPerSec))
	}
	if e.Bytes > mcpScnEnvMaxBytes {
		fs.add(i, mcpScnOutside, "spec", fmt.Sprintf("numProducers × records × recordSize is %.1f GiB; the envelope allows %d GiB",
			e.Bytes/(1<<30), mcpScnEnvMaxBytes>>30))
	}
	if e.DurationMs > mcpScnEnvMaxDurationMs {
		fs.add(i, mcpScnOutside, "spec.durationSeconds", fmt.Sprintf("the run may last up to %d s; the envelope allows %d s",
			e.DurationMs/1000, mcpScnEnvMaxDurationMs/1000))
	}
	if e.DurationMs >= mcpScnReaperMs {
		call.Caveat(mcpCaveatReaper30Minutes)
	}
	if e.Partitions > mcpScnEnvMaxPartitions {
		fs.add(i, mcpScnOutside, "spec.partitions", fmt.Sprintf("%d partitions; the envelope allows %d", e.Partitions, mcpScnEnvMaxPartitions))
	}
	mcpScnCheckTopic(i, e, fs)
}

func mcpScnCheckTopic(i int, e *mcpDraftEffective, fs *mcpScnFindings) {
	switch {
	case strings.HasPrefix(e.Topic, mcpScnEnvTopicPrefix):
	case e.Topic == strings.ToLower(e.Type)+"-test":
		fs.add(i, mcpScnOutside, "spec.topic", "the run writes to "+e.Topic+", the topic every "+e.Type+" run shares; "+
			"agent load belongs on a topic named "+mcpScnEnvTopicPrefix+"..., created by the run")
	default:
		fs.add(i, mcpScnOutside, "spec.topic", "the run writes to "+strconv.Quote(e.Topic)+"; agent load belongs on a "+
			"topic named "+mcpScnEnvTopicPrefix+"..., created by the run, and this tool cannot tell whether that topic exists")
	}
}

// Validate keys ValidationSpec reads (apply.go:27-37).
var (
	mcpScnValidateFields = []string{
		"maxP99LatencyMs", "maxAvgLatencyMs", "minThroughputRecPerSec", "maxErrorRate",
		"maxDataLossPercent", "maxRtoMs", "maxRpoMs", "maxOutOfOrder", "maxCrcFailures",
	}
	mcpScnValidateKeys = func() map[string]bool {
		m := make(map[string]bool, len(mcpScnValidateFields))
		for _, k := range mcpScnValidateFields {
			m[k] = true
		}
		return m
	}()
	// validateSLAs checks these only when above 0.
	mcpScnValidateOffAtZero = map[string]bool{
		"maxP99LatencyMs": true, "maxAvgLatencyMs": true, "minThroughputRecPerSec": true, "maxRtoMs": true, "maxRpoMs": true,
	}
	// validateSLAs checks these, on integrity results, whenever they are 0
	// or more, absent included.
	mcpScnValidateOnAtZero = []string{"maxDataLossPercent", "maxOutOfOrder", "maxCrcFailures"}
)

func mcpScnCheckValidate(i int, req *client.CreateTestRequest, raw map[string]any, fs *mcpScnFindings) {
	v, present := raw["validate"].(map[string]any)
	if !present || len(v) == 0 {
		fs.add(i, mcpScnWarning, "validate", "there is no validate block, so kates test apply --wait grades nothing")
		return
	}
	integrity := req.TestType == "INTEGRITY" && req.Backend != "trogdor"
	for _, k := range mcpScnSortedKeys(v) {
		field := "validate." + k
		f, _ := v[k].(float64)
		if n, ok := v[k].(int); ok {
			f = float64(n)
		}
		if (k == "maxOutOfOrder" || k == "maxCrcFailures") && f != math.Trunc(f) {
			fs.add(i, mcpScnWarning, field, fmt.Sprintf("it is %v, which kates test apply reads as %d", f, int64(f)))
		}
		switch {
		case !mcpScnValidateKeys[k] && fs.caseVariant(i, "validate.", k, mcpScnValidateFields):
		case !mcpScnValidateKeys[k]:
			fs.add(i, mcpScnWarning, field, "kates test apply drops "+field+" when it reads the file")
		case k == "maxErrorRate":
			fs.add(i, mcpScnWarning, field, "validateSLAs never checks maxErrorRate")
		case !integrity && (k == "maxRtoMs" || k == "maxRpoMs"):
			fs.add(i, mcpScnWarning, field, "only INTEGRITY runs on the native backend report RTO and RPO, so kates "+
				"test apply reports "+k+" as not evaluable")
		case !integrity && !mcpScnValidateOffAtZero[k]:
			fs.add(i, mcpScnWarning, field, "only INTEGRITY runs on the native backend report integrity data, so "+
				k+" is never checked and no violation is ever reported")
		case integrity && k == "maxRpoMs":
			fs.add(i, mcpScnWarning, field, "a run from a scenario file marks no fault, so RPO is never measured and "+
				"kates test apply reports maxRpoMs as not evaluable")
		case mcpScnValidateOffAtZero[k] && f == 0:
			fs.add(i, mcpScnWarning, field, "0 turns this gate off: validateSLAs checks it only when above 0")
		case !mcpScnValidateOffAtZero[k] && f < 0:
			fs.add(i, mcpScnWarning, field, "a negative value turns this gate off")
		}
	}
	if integrity {
		var implicit []string
		for _, k := range mcpScnValidateOnAtZero {
			if _, ok := v[k]; !ok {
				implicit = append(implicit, k)
			}
		}
		if len(implicit) > 0 {
			fs.add(i, mcpScnWarning, "validate", strings.Join(implicit, ", ")+" absent: validateSLAs holds an INTEGRITY run "+
				"to 0 for each, so any loss, reordering or CRC failure fails the gate")
		}
	}
}

// Caveats only draft_scenario uses, part of mcpCaveatsSecurity.
const (
	mcpCaveatAgentEnvelopeProposed        mcpCaveatID = "agent-envelope-proposed"
	mcpCaveatScenarioShippedDefaults      mcpCaveatID = "scenario-shipped-defaults"
	mcpCaveatScenarioThroughputUnsettable mcpCaveatID = "scenario-throughput-unsettable"
	mcpCaveatScenarioValidateGrading      mcpCaveatID = "scenario-validate-grading"
)

var mcpCaveatsScenario = []mcpCaveat{
	{
		ID: mcpCaveatAgentEnvelopeProposed,
		Text: "The agent envelope draft_scenario checks is the one the Kates MCP plan proposes for runs an agent " +
			"starts (§5.4-§5.5), with a rate cap of 20,000 records/s chosen by this server because the plan names " +
			"none. Nothing enforces it today: POST /api/tests checks only the type and the ranges and formats of " +
			"fields, and kates test apply sends a scenario with whatever key it holds.",
		Refs: []string{
			"plans/mcp-server.md:387-405",
			mcpJava + "api/TestResource.java:69-95",
			mcpJava + "domain/TestSpec.java:16-85",
			"cli/cmd/apply.go:98-130",
		},
	},
	{
		ID: mcpCaveatScenarioShippedDefaults,
		Text: "draft_scenario reads nothing from the cluster. It fills what a scenario leaves out with the per-type " +
			"defaults Kates ships (a LOAD run: up to 1,000,000 records of 1024 bytes within 10 minutes, at an unlimited " +
			"rate); " +
			"a deployment can set others (kates.tests.<type>.*, or tests in the kates chart's values), so a value " +
			"listed as defaulted may differ on the cluster that runs the scenario.",
		Refs: []string{
			mcpJava + "config/TestTypeDefaults.java:22-64,323-464",
			mcpJava + "engine/TestOrchestrator.java:845-880",
			"kates/src/main/resources/application.properties:37-78",
			"charts/kates/values.yaml:551-618",
			"charts/kates/templates/configmap.yaml:75-170",
		},
	},
	{
		ID: mcpCaveatScenarioThroughputUnsettable,
		Text: "A scenario file cannot set the producer rate: scenarioToRequest has no key for throughput and sends " +
			"targetThroughput, which the backend drops when it merges the spec. A run from a scenario file therefore " +
			"uses its type's default throughput, which Kates ships as unlimited (-1) for every type but ENDURANCE " +
			"(5,000 records/s) and ROUND_TRIP (10,000 records/s).",
		Refs: []string{
			"cli/cmd/apply.go:194-267",
			mcpJava + "engine/TestOrchestrator.java:845-880,1118-1130",
			mcpJava + "config/TestTypeDefaults.java:50-51,179-180,308-309",
			"kates/src/main/resources/application.properties:55,78",
		},
	},
	{
		ID: mcpCaveatScenarioValidateGrading,
		Text: "A validate block is graded only by kates test apply --wait, after the run. validateSLAs never checks " +
			"maxErrorRate. maxDataLossPercent, maxOutOfOrder and maxCrcFailures (held at 0 when absent), maxRtoMs and " +
			"maxRpoMs apply only to results with integrity data, which only INTEGRITY runs on the native backend " +
			"produce, and RPO is measured only when a resilience run marks a fault, so a scenario run never has one. " +
			"Keys ValidationSpec does not name, such as maxDuplicatePercent, are dropped when the file is read.",
		Refs: []string{
			"cli/cmd/apply.go:27-37,120-126,142-156,294-358",
			mcpJava + "engine/NativeKafkaBackend.java:142-147,287-306",
			mcpJava + "engine/TrogdorBackend.java:100-101",
			mcpJava + "resilience/ResilienceOrchestrator.java:109",
		},
	},
}
