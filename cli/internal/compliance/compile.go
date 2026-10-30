package compliance

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Result is what Compile found: the parsed document, when it parses, and
// every diagnostic, in file order.
type Result struct {
	// Root is the document's top-level map; nil when the file does not parse.
	Root        *yaml.Node
	Diagnostics []Diagnostic
}

// Compile checks a reference the way a compiler does, phase by phase:
// parse, duplicates, schema, the API reader's own checks, then incoherence
// between constraints, which runs only on a reference with no error so far.
// today is the date waivers are read against.
func Compile(src []byte, today time.Time) Result {
	root, ds := parse(src)
	if root == nil {
		return Result{Diagnostics: ds}
	}
	ds = append(ds, schemaHeader(src)...)
	ds = append(ds, duplicates(root, "")...)
	ds = append(ds, schemaWalker().check(root, schemaWalker().root, "")...)
	ds = append(ds, semantics(root)...)
	if !HasErrors(ds) {
		ds = append(ds, incoherence(root, today)...)
	}
	sortDiagnostics(ds)
	return Result{Root: root, Diagnostics: ds}
}

var yamlLine = regexp.MustCompile(`line (\d+)`)

// schemaFile is the JSON Schema a reference is checked against.
const schemaFile = "kafkareference.v1alpha1.schema.json"

var schemaComment = regexp.MustCompile(`^#\s*yaml-language-server:\s*\$schema=(\S+)`)

// schemaHeader warns when the editor's schema comment links to another
// schema: the editor would then accept what the check refuses, or the reverse.
func schemaHeader(src []byte) []Diagnostic {
	for i, line := range strings.Split(string(src), "\n") {
		m := schemaComment.FindStringSubmatch(strings.TrimSpace(line))
		if m != nil && path.Base(m[1]) != schemaFile {
			return []Diagnostic{{Line: i + 1, Col: 1, Severity: Warning, Code: "schema-header",
				Message: fmt.Sprintf("the editor validates against %s, not %s", m[1], schemaFile)}}
		}
	}
	return nil
}

// parse reads the one YAML document a reference is.
func parse(src []byte) (*yaml.Node, []Diagnostic) {
	dec := yaml.NewDecoder(bytes.NewReader(src))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, []Diagnostic{{Severity: Error, Code: "syntax", Message: "the file is empty"}}
		}
		return nil, []Diagnostic{syntaxError(err)}
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err == nil {
		return nil, []Diagnostic{at(&extra, Error, "syntax", "", "a reference is one YAML document; this file has more")}
	} else if !errors.Is(err, io.EOF) {
		return nil, []Diagnostic{syntaxError(err)}
	}
	if len(doc.Content) == 0 {
		return nil, []Diagnostic{{Severity: Error, Code: "syntax", Message: "the file is empty"}}
	}
	return resolveAlias(doc.Content[0]), nil
}

func syntaxError(err error) Diagnostic {
	d := Diagnostic{Severity: Error, Code: "syntax", Message: strings.TrimPrefix(err.Error(), "yaml: ")}
	if m := yamlLine.FindStringSubmatch(err.Error()); m != nil {
		d.Line, _ = strconv.Atoi(m[1])
		d.Col = 1
		d.Message = strings.TrimSpace(yamlLine.ReplaceAllString(d.Message, ""))
		d.Message = strings.TrimPrefix(d.Message, ": ")
	}
	return d
}

// get is the value under key in a map, the first one when the key repeats.
func get(n *yaml.Node, key string) *yaml.Node {
	n = resolveAlias(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return resolveAlias(n.Content[i+1])
		}
	}
	return nil
}

func keyNode(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i]
		}
	}
	return nil
}

// equalNodes compares two nodes as values: tags, text and children, with a
// map's keys in any order; comments, style and position do not count.
func equalNodes(a, b *yaml.Node) bool {
	a, b = resolveAlias(a), resolveAlias(b)
	if a == nil || b == nil {
		return a == b
	}
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case yaml.ScalarNode:
		return kind(a) == kind(b) && a.Value == b.Value
	case yaml.SequenceNode:
		if len(a.Content) != len(b.Content) {
			return false
		}
		for i := range a.Content {
			if !equalNodes(a.Content[i], b.Content[i]) {
				return false
			}
		}
		return true
	case yaml.MappingNode:
		if len(a.Content) != len(b.Content) {
			return false
		}
		for i := 0; i+1 < len(a.Content); i += 2 {
			if !equalNodes(a.Content[i+1], get(b, a.Content[i].Value)) {
				return false
			}
		}
		return true
	}
	return false
}

// duplicates finds what a reference says twice. The same key with the same
// value, a value repeated in a list, and an item listed twice are fixable:
// kates compliance fmt keeps the first. A key set twice is an error either
// way, since the check and the API refuse it; with different values fmt
// cannot fix it, since only the author can say which one is meant.
func duplicates(n *yaml.Node, path string) []Diagnostic {
	n = resolveAlias(n)
	var ds []Diagnostic
	switch n.Kind {
	case yaml.MappingNode:
		first := map[string]*yaml.Node{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, val := n.Content[i], n.Content[i+1]
			p := childPath(path, key.Value)
			if prev, ok := first[key.Value]; ok {
				if equalNodes(prev, val) {
					d := at(key, Error, "duplicate-key", p, fmt.Sprintf("the key is already set, to the same value, at %d:%d", prev.Line, prev.Column))
					d.Fixable = true
					ds = append(ds, d)
				} else {
					ds = append(ds, at(key, Error, "conflicting-key", p, fmt.Sprintf("the key is already set, to another value, at %d:%d: keep one", prev.Line, prev.Column)))
				}
				continue
			}
			first[key.Value] = val
			ds = append(ds, duplicates(val, p)...)
		}
	case yaml.SequenceNode:
		for i, item := range n.Content {
			p := fmt.Sprintf("%s[%d]", path, i)
			for j := 0; j < i; j++ {
				if equalNodes(n.Content[j], item) {
					d := at(item, Warning, "duplicate-value", p, fmt.Sprintf("the same as [%d]: listed twice", j))
					if resolveAlias(item).Kind == yaml.MappingNode {
						// The API refuses a waiver or a listener listed twice.
						d.Severity, d.Code = Error, "duplicate-item"
					}
					d.Fixable = true
					ds = append(ds, d)
					break
				}
			}
			ds = append(ds, duplicates(item, p)...)
		}
	}
	return ds
}

// semantics makes the checks the API's reader makes beyond the schema.
func semantics(root *yaml.Node) []Diagnostic {
	spec := get(root, "spec")
	if spec == nil || spec.Kind != yaml.MappingNode {
		return nil
	}
	var ds []Diagnostic
	rules := declaredRules(spec)
	if len(rules) == 0 {
		ds = append(ds, at(keyNode(root, "spec"), Error, "no-rule", "spec", "declares no rule"))
	}
	ds = append(ds, listenerNames(spec)...)
	ds = append(ds, waiverChecks(spec, rules)...)
	for _, r := range rules {
		ds = append(ds, minAboveMax(r)...)
	}
	return ds
}

// rule is one declared constraint: its id, its node, and the kind its values have.
type rule struct {
	id   string
	node *yaml.Node
	kind string // integer, quantity, version, config, or text
}

var roleKinds = []struct{ path, kind string }{
	{"count", "integer"}, {"dedicated", "text"},
	{"requests.cpu", "quantity"}, {"requests.memory", "quantity"},
	{"limits.cpu", "quantity"}, {"limits.memory", "quantity"},
	{"storage.type", "text"}, {"storage.size", "quantity"}, {"storage.class", "text"},
	{"zones", "integer"}, {"skew", "integer"}, {"perNode", "integer"}, {"antiAffinity", "text"},
}

func lookup(n *yaml.Node, dotted string) *yaml.Node {
	for _, k := range strings.Split(dotted, ".") {
		n = get(n, k)
	}
	return n
}

func present(n *yaml.Node) bool {
	return n != nil && kind(n) != "null"
}

// declaredRules lists the rules a spec declares, ids as the API names them.
func declaredRules(spec *yaml.Node) []rule {
	var rules []rule
	add := func(id string, n *yaml.Node, k string) {
		if present(n) {
			rules = append(rules, rule{id, n, k})
		}
	}
	for _, v := range []string{"kubernetes", "strimzi", "kafka"} {
		add("versions."+v, lookup(spec, "versions."+v), "version")
	}
	for _, role := range []string{"controllers", "brokers"} {
		r := get(spec, role)
		for _, f := range roleKinds {
			if f.path == "dedicated" && role != "controllers" {
				continue
			}
			add(role+"."+f.path, lookup(r, f.path), f.kind)
		}
	}
	kafka := get(spec, "kafka")
	add("kafka.rackAwareness", get(kafka, "rackAwareness"), "text")
	add("kafka.authorization", get(kafka, "authorization"), "text")
	if req := lookup(kafka, "listeners.require"); req != nil && req.Kind == yaml.SequenceNode {
		for _, l := range req.Content {
			if name := get(l, "name"); name != nil && name.Kind == yaml.ScalarNode {
				for _, p := range []struct{ key, kind string }{{"type", "text"}, {"port", "integer"}, {"tls", "text"}, {"auth", "text"}} {
					if c := get(l, p.key); present(c) {
						rules = append(rules, rule{"kafka.listeners.require[" + name.Value + "]." + p.key, c, p.kind})
					}
				}
				rules = append(rules, rule{"kafka.listeners.require[" + name.Value + "]", l, "listener"})
			}
		}
	}
	for _, p := range []string{"type", "tls", "auth"} {
		add("kafka.listeners.every."+p, lookup(kafka, "listeners.every."+p), "text")
	}
	if config := get(kafka, "config"); config != nil && config.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(config.Content); i += 2 {
			add(childPath("kafka.config", config.Content[i].Value), config.Content[i+1], "config")
		}
	}
	topics := get(spec, "topics")
	for _, t := range []string{"replicationFactor", "minInsyncReplicas"} {
		add("topics."+t, get(topics, t), "integer")
	}
	monitoring := get(spec, "monitoring")
	for _, m := range []string{"metrics", "kafkaExporter", "podMonitor"} {
		add("monitoring."+m, get(monitoring, m), "text")
	}
	return rules
}

// isRuleID says whether a declared rule id names a rule a waiver can take:
// a required listener's properties are not rules of their own.
func isRuleID(r rule) bool {
	return !strings.Contains(r.id, "].")
}

func listenerNames(spec *yaml.Node) []Diagnostic {
	req := lookup(spec, "kafka.listeners.require")
	if req == nil || req.Kind != yaml.SequenceNode {
		return nil
	}
	var ds []Diagnostic
	first := map[string]*yaml.Node{}
	for i, l := range req.Content {
		name := get(l, "name")
		if name == nil || name.Kind != yaml.ScalarNode {
			continue
		}
		if prev, ok := first[name.Value]; ok {
			if !equalNodes(prev, l) {
				ds = append(ds, at(name, Error, "duplicate-listener", fmt.Sprintf("spec.kafka.listeners.require[%d].name", i), "names a listener listed before it, with other properties: keep one"))
			}
			continue
		}
		first[name.Value] = l
	}
	return ds
}

func waiverChecks(spec *yaml.Node, rules []rule) []Diagnostic {
	waivers := get(spec, "waivers")
	if waivers == nil || waivers.Kind != yaml.SequenceNode {
		return nil
	}
	declared := map[string]bool{}
	for _, r := range rules {
		if isRuleID(r) {
			declared[r.id] = true
		}
	}
	var ds []Diagnostic
	first := map[string]int{}
	for i, w := range waivers.Content {
		ruleNode := get(w, "rule")
		if ruleNode == nil || ruleNode.Kind != yaml.ScalarNode {
			continue
		}
		p := fmt.Sprintf("spec.waivers[%d].rule", i)
		if j, ok := first[ruleNode.Value]; ok {
			if !equalNodes(waivers.Content[j], w) {
				ds = append(ds, at(ruleNode, Error, "duplicate-waiver", p, fmt.Sprintf("waives the rule spec.waivers[%d] waives", j)))
			}
			continue
		}
		first[ruleNode.Value] = i
		if !declared[ruleNode.Value] {
			msg := "names no rule this reference declares"
			if guess := closestRule(ruleNode.Value, declared); guess != "" {
				msg += fmt.Sprintf(" (did you mean %q?)", guess)
			}
			ds = append(ds, at(ruleNode, Error, "unknown-rule", p, msg))
		}
	}
	return ds
}

func closestRule(id string, declared map[string]bool) string {
	ids := make([]string, 0, len(declared))
	for k := range declared {
		ids = append(ids, k)
	}
	return closest(id, ids)
}

// bounds are a constraint's min and max, when it is a map with both.
func bounds(n *yaml.Node) (lo, hi *yaml.Node) {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, nil
	}
	return get(n, "min"), get(n, "max")
}

func minAboveMax(r rule) []Diagnostic {
	lo, hi := bounds(r.node)
	if lo == nil || hi == nil {
		return nil
	}
	a, okA := ordinal(lo.Value, r.kind)
	b, okB := ordinal(hi.Value, r.kind)
	if okA && okB && compareOrdinal(a, b) > 0 {
		return []Diagnostic{at(lo, Error, "min-above-max", "spec."+r.id, "min is above max, so nothing can meet it")}
	}
	return nil
}

// ordinal is a value made comparable for its kind: a number, or a version's parts.
func ordinal(text, k string) ([]float64, bool) {
	switch k {
	case "integer", "config":
		f, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
		return []float64{f}, err == nil
	case "quantity":
		f, ok := quantity(text)
		return []float64{f}, ok
	case "version":
		v, ok := version(text)
		return v, ok
	}
	return nil, false
}

func compareOrdinal(a, b []float64) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y float64
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

var quantitySuffixes = map[string]float64{
	"": 1, "m": 1e-3, "k": 1e3, "M": 1e6, "G": 1e9, "T": 1e12, "P": 1e15, "E": 1e18,
	"Ki": 1 << 10, "Mi": 1 << 20, "Gi": 1 << 30, "Ti": 1 << 40, "Pi": 1 << 50, "Ei": 1 << 60,
}

var quantityRE = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)(m|k|M|G|T|P|E|Ki|Mi|Gi|Ti|Pi|Ei)?$`)

// quantity is a Kubernetes quantity in base units: 8Gi is 8·2³⁰, 500m is 0.5.
func quantity(text string) (float64, bool) {
	m := quantityRE.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return 0, false
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil || math.IsInf(f, 0) {
		return 0, false
	}
	return f * quantitySuffixes[m[2]], true
}

var versionRE = regexp.MustCompile(`^v?([0-9]+(?:\.[0-9]+){0,3})$`)

// version is a dotted version's parts; missing ones count as 0 when compared.
func version(text string) ([]float64, bool) {
	m := versionRE.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return nil, false
	}
	var parts []float64
	for _, p := range strings.Split(m[1], ".") {
		f, _ := strconv.ParseFloat(p, 64)
		parts = append(parts, f)
	}
	return parts, true
}
