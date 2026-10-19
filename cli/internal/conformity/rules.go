package conformity

import (
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
	run      func(c *ctx) []finding
}

// finding is what a check reports, before it becomes a Diagnostic.
type finding struct {
	node    *yaml.Node
	path    string
	message string
}

var incoherenceChecks []check

func register(cs ...check) {
	incoherenceChecks = append(incoherenceChecks, cs...)
}

// ctx is what a check sees: the lint date and the waivers.
type ctx struct {
	today   time.Time
	waivers map[string]waiverInfo // the first waiver for each rule id
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
	c := &ctx{today: today, waivers: map[string]waiverInfo{}}
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
		for _, f := range chk.run(c) {
			ds = append(ds, at(f.node, chk.severity, chk.code, f.path, f.message))
		}
	}
	return ds
}
