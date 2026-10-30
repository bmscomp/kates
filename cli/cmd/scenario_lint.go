package cmd

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

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
	if t, ok := fields["type"]; ok {
		l.checkTestType(t, childPath(path, "type"))
	} else {
		l.errorAt(n, "missing-type", path, "the scenario has no type; the Kates API refuses a test without one")
	}
	if b, ok := fields["backend"]; ok && b.Value != "native" && b.Value != "trogdor" && b.Value != "" {
		l.errorAt(b, "unknown-backend", childPath(path, "backend"), "there is no benchmark backend "+nodeKind(b)+"; it is native or trogdor")
	}
	if s, ok := fields["spec"]; ok && s.Tag != "!!null" {
		l.scenarioSpec(s, childPath(path, "spec"))
	}
	if v, ok := fields["validate"]; ok && v.Tag != "!!null" {
		l.scenarioValidate(v, childPath(path, "validate"))
	}
}

func (l *fileLinter) scenarioSpec(n *yaml.Node, path string) {
	for key, v := range l.mapping(n, path, scenarioSpecKeys(), scenarioSpecHints) {
		p := childPath(path, key)
		if slices.Contains(scenarioStringKeys, key) {
			if v.Kind != yaml.ScalarNode {
				l.errorAt(v, "wrong-type", p, p+" is "+nodeKind(v)+"; write a value")
			}
			continue
		}
		var value any
		if err := v.Decode(&value); err != nil {
			l.errorAt(v, "wrong-type", p, err.Error())
			continue
		}
		for _, problem := range scenarioSpecProblems(TestScenario{Spec: map[string]any{key: value}}) {
			l.errorAt(v, "wrong-value", p, problem)
		}
	}
}

// validateOffAtZero are the gates validateSLAs checks only above 0.
var validateOffAtZero = []string{"maxP99LatencyMs", "maxAvgLatencyMs", "minThroughputRecPerSec", "maxRtoMs", "maxRpoMs"}

func (l *fileLinter) scenarioValidate(n *yaml.Node, path string) {
	known, types := yamlFields(reflect.TypeOf(ValidationSpec{}))
	for key, v := range l.mapping(n, path, known, nil) {
		p := childPath(path, key)
		before := len(l.ds)
		l.decodeAs(v, types[key], p)
		if len(l.ds) > before {
			continue
		}
		switch {
		case key == "maxErrorRate":
			l.warnAt(v, "gate-not-checked", p, "kates test apply never checks maxErrorRate: a run reports no error count")
		case slices.Contains(validateOffAtZero, key) && (v.Value == "0" || v.Value == "0.0"):
			l.warnAt(v, "gate-off", p, "0 turns this gate off: it is checked only above 0")
		}
	}
}
