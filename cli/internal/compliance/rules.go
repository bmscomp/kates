package compliance

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// The incoherence stage: checks that read several constraints together and
// find ones that cannot all hold (errors) or that make each other moot
// (warnings). Each check is registered in incoherenceChecks by the
// rules_*.go files. testdata/rules.json holds, for every check, a reference
// that must trigger it and a near miss that must not.

// check is one incoherence rule.
type check struct {
	code     string
	severity Severity
	// twoPass marks an error rule that the reference can tolerate: it is
	// run once with tolerated constraints treated as absent, and when it
	// fires only as written, rule-never-passes warns instead of an error.
	twoPass bool
	run     func(c *ctx) []finding
}

// finding is what a check reports, before it becomes a Diagnostic.
type finding struct {
	node    *yaml.Node
	path    string
	message string
	rules   []string // the rule ids it read, which rule-never-passes names when tolerated
}

var incoherenceChecks []check

func register(cs ...check) {
	incoherenceChecks = append(incoherenceChecks, cs...)
}

// ctx is what a check sees: the spec, the lint date, the declared rules
// and the waivers.
type ctx struct {
	spec    *yaml.Node
	today   time.Time
	rules   map[string]*yaml.Node // declared rules by id, as declaredRules names them
	waivers map[string]waiverInfo // the first waiver for each rule id
	source  string                // kafka.source: brokers, or cr when empty
	// ignoreTolerated is a two-pass check's first pass: a tolerated constraint reads as absent.
	ignoreTolerated bool
}

type waiverInfo struct {
	index int
	until time.Time
	node  *yaml.Node // the until value
}

// active says whether a waiver still applies on the lint date; until is inclusive.
func (w waiverInfo) active(today time.Time) bool {
	return !w.until.Before(today)
}

func newCtx(spec *yaml.Node, today time.Time) *ctx {
	c := &ctx{spec: spec, today: today, rules: map[string]*yaml.Node{}, waivers: map[string]waiverInfo{}}
	for _, r := range declaredRules(spec) {
		c.rules[r.id] = r.node
	}
	if s := lookup(spec, "kafka.source"); s != nil && s.Kind == yaml.ScalarNode {
		c.source = s.Value
	}
	if ws := get(spec, "waivers"); ws != nil && ws.Kind == yaml.SequenceNode {
		for i, w := range ws.Content {
			rule, until := get(w, "rule"), get(w, "until")
			if rule == nil || until == nil {
				continue
			}
			d, err := time.Parse("2006-01-02", until.Value)
			if err != nil {
				continue
			}
			if _, seen := c.waivers[rule.Value]; !seen {
				c.waivers[rule.Value] = waiverInfo{index: i, until: d, node: until}
			}
		}
	}
	return c
}

// tolerated says whether a declared rule cannot fail the check: its
// severity is warn, or an active waiver names it.
func (c *ctx) tolerated(id string) bool {
	if w, ok := c.waivers[id]; ok && w.active(c.today) {
		return true
	}
	sev := get(c.rules[id], "severity")
	return sev != nil && sev.Value == "warn"
}

// node is the constraint a rule id declares, or nil when it is absent, or
// tolerated during a two-pass check's first pass.
func (c *ctx) node(id string) *yaml.Node {
	if c.ignoreTolerated && c.tolerated(id) {
		return nil
	}
	return c.rules[id]
}

// incoherence runs every registered check. It runs only on a reference
// with no error so far.
func incoherence(root *yaml.Node, today time.Time) []Diagnostic {
	spec := get(root, "spec")
	if spec == nil || spec.Kind != yaml.MappingNode {
		return nil
	}
	c := newCtx(spec, today)
	var ds []Diagnostic
	for _, chk := range incoherenceChecks {
		if !chk.twoPass {
			for _, f := range chk.run(c) {
				ds = append(ds, at(f.node, chk.severity, chk.code, f.path, f.message))
			}
			continue
		}
		c.ignoreTolerated = true
		hard := map[string]bool{}
		for _, f := range chk.run(c) {
			hard[f.path] = true
			ds = append(ds, at(f.node, chk.severity, chk.code, f.path, f.message))
		}
		c.ignoreTolerated = false
		for _, f := range chk.run(c) {
			if !hard[f.path] {
				ds = append(ds, at(f.node, Warning, "rule-never-passes", f.path, c.neverPasses(chk.code, f)))
			}
		}
	}
	return ds
}

// neverPasses explains a tolerated rule that cannot pass on any cluster.
func (c *ctx) neverPasses(code string, f finding) string {
	var how []string
	for _, id := range f.rules {
		if w, ok := c.waivers[id]; ok && w.active(c.today) {
			how = append(how, fmt.Sprintf("spec.%s is waived until %s, a waiver no change to the cluster can retire", id, w.until.Format("2006-01-02")))
		} else if c.tolerated(id) {
			how = append(how, "spec."+id+" has severity warn, so the reference passes only while it warns")
		}
	}
	return fmt.Sprintf("%s [%s]; it is tolerated, so the check does not fail, but it can never pass: %s",
		strings.TrimSuffix(f.message, "."), code, strings.Join(how, "; "))
}

// jsonText is a scalar as the CLI sends it: an int, float or bool as
// encoding/json writes the decoded value (2.0 is 2, 0x10 is 16, True is
// true), anything else as its text.
func jsonText(n *yaml.Node) string {
	n = resolveAlias(n)
	switch kind(n) {
	case "integer", "number", "boolean":
		var v any
		if err := n.Decode(&v); err == nil {
			if b, err := json.Marshal(v); err == nil {
				return string(b)
			}
		}
	}
	return n.Value
}

// maxCount is the most an integer field allows: count, zones, skew and
// perNode range over 0..maxCount when absent.
const maxCount = 10000

// ints is the values an integer constraint allows: a sorted list for a bare
// value, equals or oneOf, else the range lo..hi; absent means 0..maxCount.
type ints struct {
	list   []int
	lo, hi int
}

func intsOf(n *yaml.Node) ints {
	n = resolveAlias(n)
	whole := ints{lo: 0, hi: maxCount}
	if !present(n) {
		return whole
	}
	val := func(v *yaml.Node) (int, bool) {
		if v == nil || v.Kind != yaml.ScalarNode {
			return 0, false
		}
		i, err := strconv.Atoi(jsonText(v))
		return i, err == nil
	}
	single := n
	if n.Kind == yaml.MappingNode {
		single = get(n, "equals")
	}
	if i, ok := val(single); ok {
		return ints{list: []int{i}, lo: i, hi: i}
	}
	if list := get(n, "oneOf"); list != nil && list.Kind == yaml.SequenceNode {
		var out ints
		for _, item := range list.Content {
			if i, ok := val(item); ok {
				out.list = append(out.list, i)
			}
		}
		sort.Ints(out.list)
		if len(out.list) > 0 {
			out.lo, out.hi = out.list[0], out.list[len(out.list)-1]
			return out
		}
	}
	if i, ok := val(get(n, "min")); ok {
		whole.lo = i
	}
	if i, ok := val(get(n, "max")); ok {
		whole.hi = i
	}
	return whole
}

// has says whether the constraint allows v.
func (s ints) has(v int) bool {
	if s.list != nil {
		i := sort.SearchInts(s.list, v)
		return i < len(s.list) && s.list[i] == v
	}
	return v >= s.lo && v <= s.hi
}

// any says whether some allowed value, tried in order, satisfies f.
func (s ints) any(f func(int) bool) bool {
	if s.list != nil {
		for _, v := range s.list {
			if f(v) {
				return true
			}
		}
		return false
	}
	for v := s.lo; v <= s.hi; v++ {
		if f(v) {
			return true
		}
	}
	return false
}

func (s ints) String() string {
	if s.list != nil {
		parts := make([]string, len(s.list))
		for i, v := range s.list {
			parts[i] = strconv.Itoa(v)
		}
		if len(parts) == 1 {
			return parts[0]
		}
		return "one of " + strings.Join(parts, ", ")
	}
	switch {
	case s.lo == 0 && s.hi == maxCount:
		return "any number"
	case s.hi == maxCount:
		return fmt.Sprintf("at least %d", s.lo)
	case s.lo == 0:
		return fmt.Sprintf("at most %d", s.hi)
	}
	return fmt.Sprintf("%d to %d", s.lo, s.hi)
}

// textsOf is the values a bare, equals or oneOf constraint allows, as the
// CLI sends them; nil for min and max, or when it is absent.
func textsOf(n *yaml.Node) []string {
	n = resolveAlias(n)
	if !present(n) {
		return nil
	}
	if n.Kind == yaml.ScalarNode {
		return []string{jsonText(n)}
	}
	if eq := get(n, "equals"); eq != nil && eq.Kind == yaml.ScalarNode {
		return []string{jsonText(eq)}
	}
	if list := get(n, "oneOf"); list != nil && list.Kind == yaml.SequenceNode {
		var out []string
		for _, item := range list.Content {
			if item = resolveAlias(item); item.Kind == yaml.ScalarNode {
				out = append(out, jsonText(item))
			}
		}
		return out
	}
	return nil
}

// only says whether a constraint is declared and allows nothing but v, in any case.
func only(n *yaml.Node, v string) bool {
	vals := textsOf(n)
	for _, x := range vals {
		if !strings.EqualFold(x, v) {
			return false
		}
	}
	return len(vals) > 0
}
