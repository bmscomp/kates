package cmd

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/internal/compliance"
	"gopkg.in/yaml.v3"
)

// fileLinter collects the diagnostics of one scenario or flow file, each at
// the line and column of the node it is about, as compliance lint reports
// its own.
type fileLinter struct {
	ds []compliance.Diagnostic
}

func (l *fileLinter) add(n *yaml.Node, sev compliance.Severity, code, path, msg string) {
	d := compliance.Diagnostic{Severity: sev, Code: code, Path: path, Message: msg}
	if n != nil {
		d.Line, d.Col = n.Line, n.Column
	}
	l.ds = append(l.ds, d)
}

func (l *fileLinter) errorAt(n *yaml.Node, code, path, msg string) {
	l.add(n, compliance.Error, code, path, msg)
}

func (l *fileLinter) warnAt(n *yaml.Node, code, path, msg string) {
	l.add(n, compliance.Warning, code, path, msg)
}

func (l *fileLinter) hasErrors() bool {
	return compliance.HasErrors(l.ds)
}

// sorted returns the diagnostics in file order.
func (l *fileLinter) sorted() []compliance.Diagnostic {
	slices.SortStableFunc(l.ds, func(a, b compliance.Diagnostic) int {
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		return a.Col - b.Col
	})
	return l.ds
}

var yamlErrLineRE = regexp.MustCompile(`line (\d+)`)

// decodeError is an error yaml.v3 or encoding/json gave, at the line it names.
func (l *fileLinter) decodeError(code, path string, err error) {
	msg := strings.TrimPrefix(err.Error(), "yaml: ")
	msg = strings.TrimPrefix(msg, "unmarshal errors:\n")
	d := compliance.Diagnostic{Severity: compliance.Error, Code: code, Path: path, Message: strings.TrimSpace(msg)}
	if m := yamlErrLineRE.FindStringSubmatch(msg); m != nil {
		d.Line, _ = strconv.Atoi(m[1])
		d.Col = 1
	}
	l.ds = append(l.ds, d)
}

// parseNode reads src as YAML (JSON is YAML too) and returns its top-level
// node, or nil after a syntax error or for an empty file.
func (l *fileLinter) parseNode(src []byte) *yaml.Node {
	var root yaml.Node
	if err := yaml.Unmarshal(src, &root); err != nil {
		l.decodeError("syntax", "", err)
		return nil
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return nil
	}
	return root.Content[0]
}

// childPath names a key under path, as the API names a field: spec.records.
func childPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// mapping checks that n is a mapping whose keys are among known, and returns
// the value of each known key. A key set twice is an error, as yaml.v3 makes
// it when it reads the file; an unknown key is an error with the known key
// it is closest to, since the command that reads the file drops it without
// a word. hints names the right key for a key that is a common mistake.
func (l *fileLinter) mapping(n *yaml.Node, path string, known []string, hints map[string]string) map[string]*yaml.Node {
	if n.Kind != yaml.MappingNode {
		l.errorAt(n, "wrong-type", path, path+" is "+nodeKind(n)+"; write a mapping")
		return nil
	}
	values := map[string]*yaml.Node{}
	seen := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		p := childPath(path, k.Value)
		switch {
		case seen[k.Value]:
			l.errorAt(k, "duplicate-key", p, k.Value+" is set twice")
		case slices.Contains(known, k.Value):
			values[k.Value] = v
		default:
			msg := "unknown field"
			if hint := hints[k.Value]; hint != "" {
				msg += " (" + hint + ")"
			} else if guess := compliance.Closest(k.Value, known); guess != "" {
				msg += fmt.Sprintf(" (did you mean %q?)", guess)
			}
			l.errorAt(k, "unknown-field", p, msg)
		}
		seen[k.Value] = true
	}
	return values
}

func nodeKind(n *yaml.Node) string {
	switch n.Kind {
	case yaml.MappingNode:
		return "a mapping"
	case yaml.SequenceNode:
		return "a list"
	}
	if n.Tag == "!!null" {
		return "empty"
	}
	return strconv.Quote(n.Value)
}

// yamlFields lists the YAML keys a struct reads, in its order, with the type
// of each.
func yamlFields(t reflect.Type) ([]string, map[string]reflect.Type) {
	var names []string
	types := map[string]reflect.Type{}
	for _, f := range reflect.VisibleFields(t) {
		name := strings.Split(f.Tag.Get("yaml"), ",")[0]
		if name == "" || name == "-" || !f.IsExported() {
			continue
		}
		names = append(names, name)
		types[name] = f.Type
	}
	return names, types
}

// decodeAs checks that n reads as a value of type t, as yaml.v3 reads it into
// the struct field: a quoted number for a number, or 1.5 for a whole number,
// is an error at n.
func (l *fileLinter) decodeAs(n *yaml.Node, t reflect.Type, path string) {
	whole := t
	if whole.Kind() == reflect.Pointer {
		whole = whole.Elem()
	}
	var f float64
	if whole.Kind() >= reflect.Int && whole.Kind() <= reflect.Int64 && n.Kind == yaml.ScalarNode &&
		n.ShortTag() == "!!float" && n.Decode(&f) == nil && f != float64(int64(f)) {
		// yaml.v3 cuts the fraction off without a word: a maxOutOfOrder of
		// 0.5 was read as 0, a wait of 1.5 seconds as 1.
		l.errorAt(n, "wrong-value", path, fmt.Sprintf("it is %s, which is read as %d; write a whole number", n.Value, int64(f)))
		return
	}
	if err := n.Decode(reflect.New(t).Interface()); err != nil {
		msg := strings.TrimPrefix(err.Error(), "yaml: unmarshal errors:\n")
		msg = strings.TrimSpace(yamlErrLineRE.ReplaceAllString(msg, ""))
		msg = strings.TrimPrefix(msg, ": ")
		l.errorAt(n, "wrong-type", path, msg)
	}
}

// lintTestTypes are the types a scenario or a flow test step can name:
// validTestTypes, and INTEGRATION_CDC, which the Kates API runs too.
func lintTestTypes() []string {
	return append(slices.Clone(validTestTypes), "INTEGRATION_CDC")
}

// checkTestType reports a type the Kates API has no test for.
func (l *fileLinter) checkTestType(n *yaml.Node, path string) {
	if n.Kind != yaml.ScalarNode || !slices.Contains(lintTestTypes(), strings.ToUpper(n.Value)) {
		l.errorAt(n, "unknown-type", path, fmt.Sprintf("there is no test type %s; it is one of %s",
			nodeKind(n), strings.Join(lintTestTypes(), ", ")))
	}
}

// The scenario keys kates test apply reads, besides the struct fields.
var (
	scenarioStringKeys = []string{"topic", "acks", "compressionType", "consumerGroup"}
	// scenarioSpecHints names the scenario key for a name the request uses,
	// which scenarioToRequest does not read.
	scenarioSpecHints = map[string]string{
		"numRecords":   "the scenario key is records",
		"numProducers": "the scenario key is parallelProducers",
		"recordSize":   "the scenario key is recordSizeBytes",
		"durationMs":   "the scenario key is durationSeconds, in seconds",
		"duration":     "the scenario key is durationSeconds, in seconds",
		"throughput":   "the scenario key for the producer rate is targetThroughput",
	}
)

func scenarioSpecKeys() []string {
	return slices.Concat(scenarioIntKeys, scenarioBoolKeys, scenarioStringKeys)
}

// lintScenarioFile checks a scenario file as kates test apply reads it: its
// YAML, a scenarios list or one scenario at its top level, every key against
// the ones apply reads, each type, each spec value scenarioToRequest could
// not send as written, and each validate gate. An error means apply would
// refuse the file, or run something other than what it says; a warning, a
// gate that checks nothing.
func lintScenarioFile(name string, src []byte) []compliance.Diagnostic {
	l := &fileLinter{}
	doc := l.parseNode(src)
	if doc == nil {
		if !l.hasErrors() {
			l.errorAt(nil, "no-scenarios", "", "the file is empty: it needs a scenarios list, or one scenario with a type at its top level")
		}
		return l.sorted()
	}
	if doc.Kind != yaml.MappingNode {
		l.errorAt(doc, "wrong-type", "", "a scenario file is a mapping, with a scenarios list or one scenario's fields; this is "+nodeKind(doc))
		return l.sorted()
	}
	l.scenarioFile(doc)
	if !l.hasErrors() {
		// What the checks above do not cover, such as a key both decoders
		// read in another case, still stops apply's own reader.
		if _, err := parseScenarioFile(name, src); err != nil {
			l.decodeError("decode", "", err)
		}
	}
	return l.sorted()
}

func (l *fileLinter) scenarioFile(doc *yaml.Node) {
	scenarioKeys, _ := yamlFields(reflect.TypeOf(TestScenario{}))
	var list *yaml.Node
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i].Value == "scenarios" {
			list = doc.Content[i+1]
		}
	}
	if list != nil && list.Kind == yaml.SequenceNode && len(list.Content) > 0 {
		// Beside a list, apply reads nothing else at the top level.
		hints := map[string]string{}
		for _, k := range scenarioKeys {
			hints[k] = "kates test apply ignores a scenario's " + k + " at the top level beside a scenarios list"
		}
		l.mapping(doc, "", []string{"scenarios"}, hints)
		for i, sc := range list.Content {
			l.scenario(sc, fmt.Sprintf("scenarios[%d]", i), scenarioKeys)
		}
		return
	}
	if list != nil && list.Kind != yaml.SequenceNode && list.Tag != "!!null" {
		l.errorAt(list, "wrong-type", "scenarios", "scenarios is "+nodeKind(list)+"; write a list of scenarios")
		return
	}
	hasType := false
	for i := 0; i+1 < len(doc.Content); i += 2 {
		hasType = hasType || doc.Content[i].Value == "type"
	}
	if !hasType {
		// A key in another case, such as Scenarios, is named first.
		l.mapping(doc, "", append(slices.Clone(scenarioKeys), "scenarios"), nil)
		l.errorAt(doc, "no-scenarios", "", "no scenarios found: it needs a scenarios list, or one scenario with a type at its top level")
		return
	}
	l.scenario(doc, "", append(slices.Clone(scenarioKeys), "scenarios"))
}

func (l *fileLinter) scenario(n *yaml.Node, path string, known []string) {
	fields := l.mapping(n, path, known, nil)
	if fields == nil {
		return
	}
	testType := ""
	if t, ok := fields["type"]; ok {
		l.checkTestType(t, childPath(path, "type"))
		testType = strings.ToUpper(t.Value)
	} else {
		l.errorAt(n, "missing-type", path, "the scenario has no type; the Kates API refuses a test without one")
	}
	backend := "native"
	if b, ok := fields["backend"]; ok {
		if b.Value != "native" && b.Value != "trogdor" && b.Value != "" {
			l.errorAt(b, "unknown-backend", childPath(path, "backend"), "there is no benchmark backend "+nodeKind(b)+"; it is native or trogdor")
		} else if b.Value != "" {
			backend = b.Value
			if backend == "trogdor" && (testType == "INTEGRITY" || testType == "INTEGRATION_CDC") {
				l.errorAt(b, "incompatible-backend", childPath(path, "backend"),
					fmt.Sprintf("%s requires the native backend; trogdor does not support it", testType))
			}
		}
	}
	effectiveProducers := mcpScnTypeDefaultsFor(testType).numProducers
	if effectiveProducers <= 0 {
		effectiveProducers = 1
	}
	explicitThroughput := 0
	if s, ok := fields["spec"]; ok && s.Tag != "!!null" {
		effectiveProducers, explicitThroughput = l.scenarioSpecWithContext(s, childPath(path, "spec"), testType, backend)
	}
	if v, ok := fields["validate"]; ok && v.Tag != "!!null" {
		l.scenarioValidateWithContext(v, childPath(path, "validate"), testType, effectiveProducers, explicitThroughput)
	}
}

func (l *fileLinter) scenarioSpecWithContext(n *yaml.Node, path, testType, backend string) (effectiveProducers, explicitThroughput int) {
	effectiveProducers = mcpScnTypeDefaultsFor(testType).numProducers
	if effectiveProducers <= 0 {
		effectiveProducers = 1
	}
	validNodes := map[string]*yaml.Node{}
	validVals := map[string]any{}
	for key, v := range l.mapping(n, path, scenarioSpecKeys(), scenarioSpecHints) {
		p := childPath(path, key)
		if slices.Contains(scenarioStringKeys, key) {
			if v.Kind != yaml.ScalarNode {
				l.errorAt(v, "wrong-type", p, p+" is "+nodeKind(v)+"; write a value")
				continue
			}
			if l.checkSpecStringValue(v, p, key) {
				validNodes[key] = v
				validVals[key] = v.Value
			}
			continue
		}
		var value any
		if err := v.Decode(&value); err != nil {
			l.errorAt(v, "wrong-type", p, err.Error())
			continue
		}
		problems := scenarioSpecProblems(TestScenario{Spec: map[string]any{key: value}})
		for _, problem := range problems {
			l.errorAt(v, "wrong-value", p, problem)
		}
		if len(problems) > 0 {
			continue
		}
		if meta, ok := mcpScnSpecKeys[key]; ok && meta.kind == 'i' {
			minVal, maxVal := meta.min, meta.max
			if key == "durationSeconds" {
				// mcpScnSpecKeys stores duration bounds in milliseconds (1000..86400000).
				minVal, maxVal = 1, 86_400
			}
			if iv := toIntPtr(value); iv != nil && (int64(*iv) < minVal || int64(*iv) > maxVal) {
				l.errorAt(v, "wrong-value", p, fmt.Sprintf("spec.%s is %d; must be between %d and %d", key, *iv, minVal, maxVal))
				continue
			}
		}
		validNodes[key] = v
		validVals[key] = value
	}
	for _, k := range []string{"parallelProducers", "numProducers"} {
		if iv := toIntPtr(validVals[k]); iv != nil && *iv > 0 {
			effectiveProducers = *iv
		}
	}
	for _, k := range []string{"targetThroughput", "throughput"} {
		if iv := toIntPtr(validVals[k]); iv != nil && *iv > 0 {
			explicitThroughput = *iv
		}
	}
	l.checkSpecCrossFields(path, testType, backend, validNodes, validVals)
	return effectiveProducers, explicitThroughput
}

func (l *fileLinter) checkSpecStringValue(v *yaml.Node, path, key string) bool {
	if v.Value == "" {
		return true
	}
	switch key {
	case "topic":
		if v.Value == "." || v.Value == ".." || !mcpKafkaTopicRE.MatchString(v.Value) {
			l.errorAt(v, "wrong-value", path, fmt.Sprintf("%s %q is not a valid Kafka topic name (1-249 chars of [a-zA-Z0-9._-], not . or ..)", path, v.Value))
			return false
		}
	case "acks":
		if !mcpAcksRE.MatchString(v.Value) {
			l.errorAt(v, "wrong-value", path, fmt.Sprintf("%s %q is not valid; must be all, -1, 0, or 1", path, v.Value))
			return false
		}
	case "compressionType":
		if !mcpCompressionRE.MatchString(v.Value) {
			l.errorAt(v, "wrong-value", path, fmt.Sprintf("%s %q is not valid; must be none, gzip, snappy, lz4, or zstd", path, v.Value))
			return false
		}
	case "consumerGroup":
		if strings.TrimSpace(v.Value) == "" || !mcpScnGroupRE.MatchString(v.Value) {
			l.errorAt(v, "wrong-value", path, fmt.Sprintf("%s %q is not a valid consumer group ID", path, v.Value))
			return false
		}
	}
	return true
}

func (l *fileLinter) checkSpecCrossFields(path, testType, backend string, nodes map[string]*yaml.Node, vals map[string]any) {
	validType := testType != "" && slices.Contains(lintTestTypes(), testType)
	l.checkSpecSafetyInvariants(path, testType, backend, validType, nodes, vals)
	l.checkSpecApplicabilityAndDuration(path, testType, validType, nodes, vals)
}

func (l *fileLinter) checkSpecSafetyInvariants(path, testType, backend string, validType bool, nodes map[string]*yaml.Node, vals map[string]any) {
	defaults := mcpScnTypeDefaultsFor(testType)

	// 1. minInsyncReplicas <= replicationFactor
	if isrNode, ok := nodes["minInsyncReplicas"]; ok {
		if isr := toIntPtr(vals["minInsyncReplicas"]); isr != nil {
			rf := 3
			hasRF := validType
			if rfVal, rfOK := vals["replicationFactor"]; rfOK {
				if rfPtr := toIntPtr(rfVal); rfPtr != nil {
					rf = *rfPtr
					hasRF = true
				}
			}
			if hasRF && *isr > rf {
				l.errorAt(isrNode, "isr-exceeds-rf", childPath(path, "minInsyncReplicas"),
					fmt.Sprintf("minInsyncReplicas (%d) cannot exceed replicationFactor (%d)", *isr, rf))
			}
		}
	}

	// 2. enableIdempotence / enableTransactions requires acks=all or -1
	idem := toBoolPtr(vals["enableIdempotence"]) != nil && *toBoolPtr(vals["enableIdempotence"])
	tx := toBoolPtr(vals["enableTransactions"]) != nil && *toBoolPtr(vals["enableTransactions"])
	if idem || tx {
		acks := ""
		if a, ok := vals["acks"].(string); ok && a != "" {
			acks = a
		} else if validType {
			acks = defaults.acks
		}
		if acks != "" && acks != "all" && acks != "-1" {
			target, targetKey := nodes["acks"], "acks"
			if target == nil {
				if nodes["enableTransactions"] != nil {
					target, targetKey = nodes["enableTransactions"], "enableTransactions"
				} else {
					target, targetKey = nodes["enableIdempotence"], "enableIdempotence"
				}
			}
			l.errorAt(target, "idempotence-requires-acks-all", childPath(path, targetKey),
				fmt.Sprintf("idempotent/transactional producer requires acks=all (or -1), got %q", acks))
		}
	}

	// 3. enableTransactions requires enableIdempotence != false and backend == native
	if tx {
		txNode := nodes["enableTransactions"]
		if b := toBoolPtr(vals["enableIdempotence"]); b != nil && !*b {
			l.errorAt(txNode, "invalid-transactions-config", childPath(path, "enableTransactions"),
				"enableTransactions: true cannot be combined with enableIdempotence: false")
		}
		if backend == "trogdor" {
			l.errorAt(txNode, "invalid-transactions-config", childPath(path, "enableTransactions"),
				"enableTransactions: true is only supported on the native backend, not trogdor")
		}
	}
}

func (l *fileLinter) checkSpecApplicabilityAndDuration(path, testType string, validType bool, nodes map[string]*yaml.Node, vals map[string]any) {
	if validType {
		for _, k := range []string{"targetThroughput", "throughput"} {
			if tpNode, ok := nodes[k]; ok {
				if tp := toIntPtr(vals[k]); tp != nil && *tp > 0 &&
					(testType == "SPIKE" || testType == "CAPACITY" || testType == "INTEGRATION_CDC") {
					l.warnAt(tpNode, "inapplicable-field", childPath(path, k),
						fmt.Sprintf("%s ignores %s", testType, childPath(path, k)))
				}
			}
		}
		hasConsumerSettings := testType == "LOAD" || testType == "ENDURANCE" || testType == "INTEGRITY"
		for _, k := range []string{"consumerGroup", "fetchMinBytes", "fetchMaxWaitMs"} {
			if cNode, ok := nodes[k]; ok && !hasConsumerSettings {
				l.warnAt(cNode, "inapplicable-field", childPath(path, k),
					fmt.Sprintf("%s takes no consumer settings and ignores %s (only LOAD, ENDURANCE and INTEGRITY do)", testType, childPath(path, k)))
			}
		}
		if crcNode, ok := nodes["enableCrc"]; ok {
			if b := toBoolPtr(vals["enableCrc"]); b != nil && *b && testType != "INTEGRITY" {
				l.warnAt(crcNode, "inapplicable-field", childPath(path, "enableCrc"),
					fmt.Sprintf("enableCrc applies only to INTEGRITY tests, not %s", testType))
			}
		}
	}

	// Reaper deadline warning (kates.engine.max-duration-ms = 3600s)
	var durSec int
	var durNode *yaml.Node
	var durKey string
	if n, ok := nodes["durationSeconds"]; ok {
		if iv := toIntPtr(vals["durationSeconds"]); iv != nil && *iv > 0 {
			durSec, durNode, durKey = *iv, n, "durationSeconds"
		}
	} else if n, ok := nodes["durationMs"]; ok {
		if iv := toIntPtr(vals["durationMs"]); iv != nil && *iv > 0 {
			durSec, durNode, durKey = *iv/1000, n, "durationMs"
		}
	}
	if durNode != nil && durSec > 0 {
		plannedSec := durSec
		if testType == "INTEGRITY" {
			plannedSec = durSec * 2
		}
		if plannedSec > 3600 {
			l.warnAt(durNode, "reaper-deadline", childPath(path, durKey),
				fmt.Sprintf("planned run duration (%ds) exceeds kates.engine.max-duration-ms (3600s) and will be refused or terminated by the reaper", plannedSec))
		}
	}
}

// validateOffAtZero are the gates validateSLAs checks only above 0.
var validateOffAtZero = []string{"maxP99LatencyMs", "maxAvgLatencyMs", "minThroughputRecPerSec", "maxRtoMs", "maxRpoMs"}

func (l *fileLinter) scenarioValidateWithContext(n *yaml.Node, path, testType string, effectiveProducers, explicitThroughput int) {
	known, types := yamlFields(reflect.TypeOf(ValidationSpec{}))
	validFloats := map[string]float64{}
	validNodes := map[string]*yaml.Node{}
	for key, v := range l.mapping(n, path, known, nil) {
		p := childPath(path, key)
		before := len(l.ds)
		l.decodeAs(v, types[key], p)
		if len(l.ds) > before {
			continue
		}
		if key != "perTask" {
			var f float64
			if v.Decode(&f) == nil {
				if f < 0 {
					l.errorAt(v, "wrong-value", p, fmt.Sprintf("%s cannot be negative (%v)", p, f))
					continue
				}
				validFloats[key] = f
				validNodes[key] = v
			}
		}
		switch {
		case key == "maxErrorRate":
			l.warnAt(v, "gate-not-checked", p, "kates test apply never checks maxErrorRate: a run reports no error count")
		case slices.Contains(validateOffAtZero, key) && (v.Value == "0" || v.Value == "0.0"):
			l.warnAt(v, "gate-off", p, "0 turns this gate off: it is checked only above 0")
		}
	}

	if avg, hasAvg := validFloats["maxAvgLatencyMs"]; hasAvg && avg > 0 {
		if p99, hasP99 := validFloats["maxP99LatencyMs"]; hasP99 && p99 > 0 && avg > p99 {
			l.warnAt(validNodes["maxAvgLatencyMs"], "contradictory-sla", childPath(path, "maxAvgLatencyMs"),
				fmt.Sprintf("maxAvgLatencyMs (%v) exceeds maxP99LatencyMs (%v)", avg, p99))
		}
	}
	if minTp, hasMinTp := validFloats["minThroughputRecPerSec"]; hasMinTp && minTp > 0 && explicitThroughput > 0 && effectiveProducers > 0 {
		capRate := explicitThroughput * effectiveProducers
		if minTp > float64(capRate) {
			l.warnAt(validNodes["minThroughputRecPerSec"], "impossible-sla-gate", childPath(path, "minThroughputRecPerSec"),
				fmt.Sprintf("minThroughputRecPerSec (%v) exceeds the scenario's producer rate cap (%d producers × %d rec/s = %d)", minTp, effectiveProducers, explicitThroughput, capRate))
		}
	}
	if testType != "" && slices.Contains(lintTestTypes(), testType) && testType != "INTEGRITY" {
		for _, k := range []string{"maxRtoMs", "maxRpoMs"} {
			if val, ok := validFloats[k]; ok && val > 0 {
				l.warnAt(validNodes[k], "unevaluable-gate", childPath(path, k),
					fmt.Sprintf("%s runs do not measure RTO/RPO (only INTEGRITY does)", testType))
			}
		}
	}
}

// Wire-format CreateTestRequest / multi-phase TestScenario linting (Dialect 2).
var (
	testRequestKeys  = []string{"type", "spec", "backend", "scenario"}
	testRequestHints = map[string]string{
		"scenarios": "a CreateTestRequest takes a single type/spec or multi-phase scenario, not scenarios",
		"validate":  "use scenario.sla for SLA thresholds in a CreateTestRequest",
	}
	multiPhaseScenarioKeys = []string{"name", "baseSpec", "phases", "sla", "labels"}
	testPhaseKeys          = []string{"name", "type", "durationMs", "specOverrides", "rampSteps"}
	validPhaseTypes        = []string{"WARMUP", "RAMP", "STEADY", "SPIKE", "COOLDOWN"}
	slaDefinitionKeys      = []string{
		"p95LatencyMs", "p99LatencyMs", "avgLatencyMs", "minThroughputRecPerSec",
		"maxErrorRatePercent", "maxRecoveryTimeMs", "maxThroughputDegradationPercent",
		"maxIsrRecoveryTimeMs", "maxConsumerLag", "maxRtoMs", "maxRpoMs",
	}
	wireSpecHints = map[string]string{
		"records":           "wire TestSpec uses numRecords",
		"parallelProducers": "wire TestSpec uses numProducers",
		"recordSizeBytes":   "wire TestSpec uses recordSize",
		"durationSeconds":   "wire TestSpec uses durationMs, in milliseconds",
	}
	wireSpecIntBounds = map[string][2]int{
		"numRecords":        {1, 100_000_000},
		"numProducers":      {1, 64},
		"recordSize":        {1, 10_485_760},
		"durationMs":        {1, 86_400_000},
		"batchSize":         {0, 10_485_760},
		"lingerMs":          {0, 60_000},
		"numConsumers":      {1, 64},
		"replicationFactor": {1, 10},
		"partitions":        {1, 10_000},
		"minInsyncReplicas": {1, 10},
		"throughput":        {-1, 10_000_000},
		"targetThroughput":  {-1, 10_000_000},
		"fetchMinBytes":     {1, 104_857_600},
		"fetchMaxWaitMs":    {0, 60_000},
	}
)

// lintTestRequestFile checks a wire-format CreateTestRequest or multi-phase
// TestScenario document (Dialect 2).
func lintTestRequestFile(src []byte) []compliance.Diagnostic {
	l := &fileLinter{}
	doc := l.parseNode(src)
	if doc == nil {
		if !l.hasErrors() {
			l.errorAt(nil, "empty", "", "the file is empty: expected a CreateTestRequest with type or scenario")
		}
		return l.sorted()
	}
	l.testRequest(doc, "")
	return l.sorted()
}

func (l *fileLinter) testRequest(n *yaml.Node, path string) {
	fields := l.mapping(n, path, testRequestKeys, testRequestHints)
	if fields == nil {
		return
	}
	tNode, hasType := fields["type"]
	scNode, hasScenario := fields["scenario"]
	if !hasType && (!hasScenario || scNode.Tag == "!!null") {
		l.errorAt(n, "missing-type", path, "a test request requires type or scenario")
	}
	testType := ""
	if hasType {
		l.checkTestType(tNode, childPath(path, "type"))
		testType = strings.ToUpper(tNode.Value)
	}
	backend := "native"
	if b, ok := fields["backend"]; ok {
		if b.Value != "native" && b.Value != "trogdor" && b.Value != "" {
			l.errorAt(b, "unknown-backend", childPath(path, "backend"), "there is no benchmark backend "+nodeKind(b)+"; it is native or trogdor")
		} else if b.Value != "" {
			backend = b.Value
			if backend == "trogdor" && (testType == "INTEGRITY" || testType == "INTEGRATION_CDC") {
				l.errorAt(b, "incompatible-backend", childPath(path, "backend"),
					fmt.Sprintf("%s requires the native backend; trogdor does not support it", testType))
			}
		}
	}
	if s, ok := fields["spec"]; ok && s.Tag != "!!null" {
		l.wireTestSpec(s, childPath(path, "spec"), testType, backend)
	}
	if hasScenario && scNode.Tag != "!!null" {
		l.multiPhaseScenario(scNode, childPath(path, "scenario"), backend)
	}
}

func (l *fileLinter) wireTestSpec(n *yaml.Node, path, testType, backend string) {
	known, types := yamlFields(reflect.TypeOf(client.TestSpec{}))
	validNodes := map[string]*yaml.Node{}
	validVals := map[string]any{}
	for key, v := range l.mapping(n, path, known, wireSpecHints) {
		p := childPath(path, key)
		if slices.Contains(scenarioStringKeys, key) {
			if v.Kind != yaml.ScalarNode {
				l.errorAt(v, "wrong-type", p, p+" is "+nodeKind(v)+"; write a value")
				continue
			}
			if l.checkSpecStringValue(v, p, key) {
				validNodes[key] = v
				validVals[key] = v.Value
			}
			continue
		}
		before := len(l.ds)
		l.decodeAs(v, types[key], p)
		if len(l.ds) > before {
			continue
		}
		var value any
		_ = v.Decode(&value)
		if bounds, ok := wireSpecIntBounds[key]; ok {
			if iv := toIntPtr(value); iv != nil && (*iv < bounds[0] || *iv > bounds[1]) {
				l.errorAt(v, "wrong-value", p, fmt.Sprintf("%s is %d; must be between %d and %d", p, *iv, bounds[0], bounds[1]))
				continue
			}
		}
		validNodes[key] = v
		validVals[key] = value
	}
	l.checkSpecCrossFields(path, testType, backend, validNodes, validVals)
}

func (l *fileLinter) multiPhaseScenario(n *yaml.Node, path, backend string) {
	fields := l.mapping(n, path, multiPhaseScenarioKeys, nil)
	if fields == nil {
		return
	}
	if name, ok := fields["name"]; !ok || name.Kind != yaml.ScalarNode || strings.TrimSpace(name.Value) == "" {
		l.errorAt(n, "missing-field", childPath(path, "name"), "scenario.name is required")
	}
	if bs, ok := fields["baseSpec"]; ok && bs.Tag != "!!null" {
		l.wireTestSpec(bs, childPath(path, "baseSpec"), "", backend)
	}
	phases, hasPhases := fields["phases"]
	switch {
	case !hasPhases || phases.Tag == "!!null" || (phases.Kind == yaml.SequenceNode && len(phases.Content) == 0):
		l.errorAt(n, "empty-phases", childPath(path, "phases"), "scenario.phases requires at least one phase")
	case phases.Kind != yaml.SequenceNode:
		l.errorAt(phases, "wrong-type", childPath(path, "phases"), "phases is "+nodeKind(phases)+"; write a list of phases")
	default:
		for i, ph := range phases.Content {
			l.testPhase(ph, fmt.Sprintf("%s[%d]", childPath(path, "phases"), i), backend)
		}
	}
	if sla, ok := fields["sla"]; ok && sla.Tag != "!!null" {
		l.slaSpec(sla, childPath(path, "sla"))
	}
	if labels, ok := fields["labels"]; ok && labels.Tag != "!!null" {
		if labels.Kind != yaml.MappingNode {
			l.errorAt(labels, "wrong-type", childPath(path, "labels"), "labels is "+nodeKind(labels)+"; write a mapping of string keys and values")
		} else {
			for i := 0; i+1 < len(labels.Content); i += 2 {
				k, v := labels.Content[i], labels.Content[i+1]
				if v.Kind != yaml.ScalarNode {
					l.errorAt(v, "wrong-type", childPath(childPath(path, "labels"), k.Value), "label value must be a scalar string")
				}
			}
		}
	}
}

func (l *fileLinter) testPhase(n *yaml.Node, path, backend string) {
	fields := l.mapping(n, path, testPhaseKeys, nil)
	if fields == nil {
		return
	}
	if name, ok := fields["name"]; !ok || name.Kind != yaml.ScalarNode || strings.TrimSpace(name.Value) == "" {
		l.errorAt(n, "missing-field", childPath(path, "name"), "phase name is required")
	}
	phaseType := ""
	if pt, ok := fields["type"]; ok {
		upper := strings.ToUpper(pt.Value)
		if pt.Kind != yaml.ScalarNode || !slices.Contains(validPhaseTypes, upper) {
			msg := fmt.Sprintf("there is no phase type %s; must be one of %s", nodeKind(pt), strings.Join(validPhaseTypes, ", "))
			if guess := compliance.Closest(upper, validPhaseTypes); guess != "" {
				msg += fmt.Sprintf(" (did you mean %q?)", guess)
			}
			l.errorAt(pt, "unknown-phase-type", childPath(path, "type"), msg)
		} else {
			phaseType = upper
		}
	} else {
		l.errorAt(n, "missing-field", childPath(path, "type"), "phase type is required (WARMUP, RAMP, STEADY, SPIKE, or COOLDOWN)")
	}
	if dur, ok := fields["durationMs"]; ok {
		p := childPath(path, "durationMs")
		before := len(l.ds)
		l.decodeAs(dur, reflect.TypeOf(int64(0)), p)
		if len(l.ds) == before {
			var d int64
			if dur.Decode(&d) == nil && d < 1 {
				l.errorAt(dur, "wrong-value", p, "durationMs must be >= 1")
			}
		}
	} else {
		l.errorAt(n, "missing-field", childPath(path, "durationMs"), "phase durationMs is required")
	}
	l.checkPhaseRampAndOverrides(fields, path, phaseType, backend)
}

func (l *fileLinter) checkPhaseRampAndOverrides(fields map[string]*yaml.Node, path, phaseType, backend string) {
	if rs, ok := fields["rampSteps"]; ok {
		p := childPath(path, "rampSteps")
		before := len(l.ds)
		l.decodeAs(rs, reflect.TypeOf(int(0)), p)
		if len(l.ds) == before {
			var steps int
			if rs.Decode(&steps) == nil {
				if phaseType == "RAMP" && steps < 2 {
					l.errorAt(rs, "invalid-ramp-steps", p, "rampSteps must be >= 2 for a RAMP phase")
				} else if phaseType != "" && phaseType != "RAMP" && steps > 0 {
					l.warnAt(rs, "unused-field", p, fmt.Sprintf("rampSteps is only used by RAMP phases, not %s", phaseType))
				}
			}
		}
	}
	if ov, ok := fields["specOverrides"]; ok && ov.Tag != "!!null" {
		l.wireTestSpec(ov, childPath(path, "specOverrides"), "", backend)
		if phaseType == "RAMP" && ov.Kind == yaml.MappingNode {
			hasRampTarget := false
			for i := 0; i+1 < len(ov.Content); i += 2 {
				k := ov.Content[i].Value
				if k == "throughput" || k == "targetThroughput" || k == "numProducers" {
					hasRampTarget = true
					break
				}
			}
			if !hasRampTarget {
				l.warnAt(ov, "ramp-without-override", childPath(path, "specOverrides"),
					"RAMP phase has no specOverrides (throughput or numProducers) to ramp toward")
			}
		}
	} else if phaseType == "RAMP" {
		l.warnAt(fields["type"], "ramp-without-override", childPath(path, "specOverrides"),
			"RAMP phase has no specOverrides (throughput or numProducers) to ramp toward")
	}
}

func (l *fileLinter) slaSpec(n *yaml.Node, path string) {
	validFloats := map[string]float64{}
	validNodes := map[string]*yaml.Node{}
	for key, v := range l.mapping(n, path, slaDefinitionKeys, nil) {
		p := childPath(path, key)
		before := len(l.ds)
		if key == "maxConsumerLag" {
			l.decodeAs(v, reflect.TypeOf(int64(0)), p)
		} else {
			l.decodeAs(v, reflect.TypeOf(float64(0)), p)
		}
		if len(l.ds) > before {
			continue
		}
		var f float64
		if v.Decode(&f) == nil {
			if f < 0 {
				l.errorAt(v, "wrong-value", p, fmt.Sprintf("%s cannot be negative (%v)", p, f))
			} else if (key == "maxErrorRatePercent" || key == "maxThroughputDegradationPercent") && f > 100 {
				l.errorAt(v, "wrong-value", p, fmt.Sprintf("%s is %v; percentage must be <= 100", p, f))
			} else {
				validFloats[key] = f
				validNodes[key] = v
			}
		}
	}
	if p95, ok1 := validFloats["p95LatencyMs"]; ok1 && p95 > 0 {
		if p99, ok2 := validFloats["p99LatencyMs"]; ok2 && p99 > 0 && p95 > p99 {
			l.errorAt(validNodes["p95LatencyMs"], "contradictory-sla", childPath(path, "p95LatencyMs"),
				fmt.Sprintf("p95LatencyMs (%v) cannot exceed p99LatencyMs (%v)", p95, p99))
		}
	}
	if avg, ok1 := validFloats["avgLatencyMs"]; ok1 && avg > 0 {
		if p99, ok2 := validFloats["p99LatencyMs"]; ok2 && p99 > 0 && avg > p99 {
			l.errorAt(validNodes["avgLatencyMs"], "contradictory-sla", childPath(path, "avgLatencyMs"),
				fmt.Sprintf("avgLatencyMs (%v) cannot exceed p99LatencyMs (%v)", avg, p99))
		}
	}
}
