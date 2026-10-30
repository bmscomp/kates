package compliance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	examples "github.com/bmscomp/kates/cli/examples/conformity"
)

// walker checks a YAML node tree against the KafkaReference JSON Schema. It
// covers the keywords that schema uses (schemaKeywords); a test fails when the
// schema starts using another one. Each finding points at the node.
type walker struct {
	root map[string]any
	defs map[string]any
}

var (
	schemaOnce sync.Once
	theWalker  *walker
)

func schemaWalker() *walker {
	schemaOnce.Do(func() {
		decoded, err := decodeOrdered(json.NewDecoder(bytes.NewReader(examples.Schema)))
		root, ok := decoded.(map[string]any)
		if err != nil || !ok {
			panic(fmt.Sprintf("the embedded KafkaReference schema is not a JSON object: %v", err))
		}
		defs, _ := root["$defs"].(map[string]any)
		theWalker = &walker{root: root, defs: defs}
	})
	return theWalker
}

// orderKey holds, in each decoded JSON object, its keys in the order the
// schema writes them; the formatter orders a map's keys by it.
const orderKey = "\x00order"

// decodeOrdered decodes JSON as encoding/json does, but records each
// object's key order under orderKey.
func decodeOrdered(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := map[string]any{}
			var order []string
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, _ := keyTok.(string)
				val, err := decodeOrdered(dec)
				if err != nil {
					return nil, err
				}
				obj[key] = val
				order = append(order, key)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			obj[orderKey] = order
			return obj, nil
		case '[':
			var arr []any
			for dec.More() {
				val, err := decodeOrdered(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, val)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return arr, nil
		}
	}
	return tok, nil
}

// schemaKeywords are the keywords check understands; annotations are listed too.
var schemaKeywords = map[string]bool{
	orderKey: true, "$schema": true, "$id": true, "$defs": true, "$ref": true, "$comment": true,
	"title": true, "description": true, "default": true,
	"type": true, "enum": true, "const": true, "pattern": true, "format": true,
	"minLength": true, "maxLength": true, "minimum": true, "maximum": true,
	"properties": true, "additionalProperties": true, "unevaluatedProperties": true,
	"required": true, "minProperties": true, "maxProperties": true, "propertyNames": true,
	"items": true, "minItems": true, "maxItems": true,
	"allOf": true, "anyOf": true, "oneOf": true, "not": true, "dependentSchemas": true,
}

// patternMessages say in words what a pattern in the schema asks for, as the
// API's reader does; a test fails on a pattern without one.
var patternMessages = map[string]string{
	`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`: "must be a DNS label: up to 63 lower-case letters, digits and -",

	`^[A-Za-z0-9_-]{22}$`: "must be a Kafka cluster id: 22 letters, digits, - or _",

	`^([a-z0-9]([-a-z0-9.]*[a-z0-9])?/)?[A-Za-z0-9]([-A-Za-z0-9_.]{0,61}[A-Za-z0-9])?$`: "must be a Kubernetes label key, such as topology.kubernetes.io/zone",

	`^[0-9]+(\.[0-9]+)?(m|k|M|G|T|P|E|Ki|Mi|Gi|Ti|Pi|Ei)?$`: "expected a quantity such as 8Gi, 500m or 2",

	`^v?[0-9]+(\.[0-9]+){0,3}$`: `expected a version such as "4.2.0"`,

	`^[a-z0-9]{1,11}$`: "must be a Strimzi listener name: up to 11 lower-case letters and digits",

	`^[a-z][A-Za-z]*(\.[a-z][A-Za-z]*|\[[^\]]+\])*$`: "must be a rule id, such as brokers.count or kafka.config[min.insync.replicas]",

	`^[a-z0-9]+([._-][a-z0-9]+)*$`: "must be a broker config name, such as min.insync.replicas",

	`^[a-zA-Z0-9._-]{1,249}$`: "must be the start of a topic name: up to 249 letters, digits, ., _ and -",
}

var (
	regexMu    sync.Mutex
	regexCache = map[string]*regexp.Regexp{}
)

func compiled(pattern string) *regexp.Regexp {
	regexMu.Lock()
	defer regexMu.Unlock()
	re, ok := regexCache[pattern]
	if !ok {
		re = regexp.MustCompile(pattern)
		regexCache[pattern] = re
	}
	return re
}

func resolveAlias(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// kind is the JSON type a node has: object, array, string, integer, number, boolean or null.
func kind(n *yaml.Node) string {
	switch n.Kind {
	case yaml.MappingNode:
		return "object"
	case yaml.SequenceNode:
		return "array"
	}
	switch n.ShortTag() {
	case "!!int":
		return "integer"
	case "!!float":
		return "number"
	case "!!bool":
		return "boolean"
	case "!!null":
		return "null"
	}
	return "string"
}

func childPath(base, key string) string {
	if strings.Contains(key, ".") {
		return base + "[" + key + "]"
	}
	if base == "" {
		return key
	}
	return base + "." + key
}

func types(t any) []string {
	switch v := t.(type) {
	case string:
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func typeMatches(k string, want []string) bool {
	for _, t := range want {
		if t == k || (t == "number" && k == "integer") {
			return true
		}
	}
	return false
}

var typeWords = map[string]string{
	"object": "a map", "array": "a list", "string": "text", "integer": "a whole number",
	"number": "a number", "boolean": "true or false", "null": "nothing",
}

func typeError(n *yaml.Node, want []string, path string) Diagnostic {
	k := kind(n)
	if k == "null" {
		return at(n, Error, "type", path, "a value is required")
	}
	if typeMatches("string", want) && len(want) == 1 && (k == "integer" || k == "number") {
		return at(n, Error, "type", path, fmt.Sprintf(`expected text, not the number %s: unquoted, YAML reads it as a number, so quote it ("%s")`, n.Value, n.Value))
	}
	words := make([]string, len(want))
	for i, t := range want {
		words[i] = typeWords[t]
	}
	return at(n, Error, "type", path, "expected "+orList(words))
}

func orList(words []string) string {
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	}
	return strings.Join(words[:len(words)-1], ", ") + " or " + words[len(words)-1]
}

func (w *walker) def(ref string) map[string]any {
	s, _ := w.defs[strings.TrimPrefix(ref, "#/$defs/")].(map[string]any)
	return s
}

// kinds lists the JSON types a schema could accept, looking through $ref,
// allOf, oneOf and anyOf; an enum or const means text.
func (w *walker) kinds(s map[string]any) map[string]bool {
	out := map[string]bool{}
	if ref, ok := s["$ref"].(string); ok {
		for k := range w.kinds(w.def(ref)) {
			out[k] = true
		}
	}
	for _, t := range types(s["type"]) {
		out[t] = true
		if t == "number" {
			out["integer"] = true
		}
	}
	if _, ok := s["enum"]; ok {
		out["string"] = true
	}
	if _, ok := s["const"]; ok {
		out["string"] = true
	}
	for _, key := range []string{"allOf", "oneOf", "anyOf"} {
		if list, ok := s[key].([]any); ok {
			for _, b := range list {
				if bs, ok := b.(map[string]any); ok {
					for k := range w.kinds(bs) {
						out[k] = true
					}
				}
			}
		}
	}
	return out
}

func errorsIn(ds []Diagnostic) int {
	n := 0
	for _, d := range ds {
		if d.Severity == Error {
			n++
		}
	}
	return n
}

// check validates n against schema s; path is n's place in the reference.
func (w *walker) check(n *yaml.Node, s map[string]any, path string) []Diagnostic {
	n = resolveAlias(n)
	var ds []Diagnostic
	if ref, ok := s["$ref"].(string); ok {
		switch {
		case strings.HasSuffix(ref, "/operatorShape"):
			ds = append(ds, operatorShape(n, path)...)
		case strings.HasSuffix(ref, "/noWarn"):
			if n.Kind == yaml.MappingNode && get(n, "severity") != nil && get(n, "severity").Value == "warn" {
				ds = append(ds, at(keyNode(n, "severity"), Error, "constraint", path, "set severity on the listener, not on its properties"))
			}
		default:
			ds = append(ds, w.check(n, w.def(ref), path)...)
		}
	}
	if list, ok := s["allOf"].([]any); ok {
		for _, b := range list {
			if bs, ok := b.(map[string]any); ok {
				ds = append(ds, w.check(n, bs, path)...)
			}
		}
	}
	if list, ok := s["oneOf"].([]any); ok {
		ds = append(ds, w.oneOf(n, list, path)...)
	}
	if list, ok := s["anyOf"].([]any); ok {
		ds = append(ds, w.anyOf(n, list, path)...)
	}
	if not, ok := s["not"].(map[string]any); ok && errorsIn(w.check(n, not, path)) == 0 {
		ds = append(ds, at(n, Error, "schema", path, "not allowed here"))
	}
	if t, ok := s["type"]; ok && !typeMatches(kind(n), types(t)) {
		return append(ds, typeError(n, types(t), path))
	}
	if enum, ok := s["enum"].([]any); ok {
		ds = append(ds, enumCheck(n, enum, path)...)
	}
	if c, ok := s["const"]; ok && (n.Kind != yaml.ScalarNode || n.Value != fmt.Sprint(c)) {
		ds = append(ds, at(n, Error, "schema", path, fmt.Sprintf("must be %v", c)))
	}
	if n.Kind == yaml.ScalarNode {
		ds = append(ds, w.scalar(n, s, path)...)
	}
	if n.Kind == yaml.MappingNode {
		ds = append(ds, w.object(n, s, path)...)
	}
	if n.Kind == yaml.SequenceNode {
		ds = append(ds, w.array(n, s, path)...)
	}
	return ds
}

// oneOf passes when a branch does. Otherwise it reports the branch that
// could take a node of this kind, so a map is held to the map form of a
// constraint and a scalar to the bare form.
func (w *walker) oneOf(n *yaml.Node, branches []any, path string) []Diagnostic {
	results := make([][]Diagnostic, len(branches))
	best := -1
	for i, b := range branches {
		bs, _ := b.(map[string]any)
		results[i] = w.check(n, bs, path)
		if errorsIn(results[i]) == 0 {
			return nil
		}
		if best < 0 && w.kinds(bs)[kind(n)] {
			best = i
		}
	}
	if best < 0 {
		best = 0
		for i := range results {
			if len(results[i]) < len(results[best]) {
				best = i
			}
		}
	}
	return results[best]
}

func (w *walker) anyOf(n *yaml.Node, branches []any, path string) []Diagnostic {
	var keys []string
	allRequired := true
	var closest []Diagnostic
	for _, b := range branches {
		bs, _ := b.(map[string]any)
		r := w.check(n, bs, path)
		if errorsIn(r) == 0 {
			return nil
		}
		if closest == nil || len(r) < len(closest) {
			closest = r
		}
		req, ok := bs["required"].([]any)
		if !ok || len(bs) != 2 || len(req) != 1 { // required, and the order key
			allRequired = false
			continue
		}
		keys = append(keys, fmt.Sprint(req[0]))
	}
	if allRequired && n.Kind == yaml.MappingNode {
		return []Diagnostic{at(n, Error, "schema", path, "lists no property to check: "+orList(keys))}
	}
	return closest
}

func enumCheck(n *yaml.Node, enum []any, path string) []Diagnostic {
	vals := make([]string, len(enum))
	for i, e := range enum {
		vals[i] = fmt.Sprint(e)
		if n.Kind == yaml.ScalarNode && n.Value == vals[i] {
			return nil
		}
	}
	return []Diagnostic{at(n, Error, "enum", path, "must be one of "+strings.Join(vals, ", "))}
}

func number(s map[string]any, key string) (float64, bool) {
	v, ok := s[key].(float64)
	return v, ok
}

func trimFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func (w *walker) scalar(n *yaml.Node, s map[string]any, path string) []Diagnostic {
	var ds []Diagnostic
	k := kind(n)
	if p, ok := s["pattern"].(string); ok && k == "string" && !compiled(p).MatchString(n.Value) {
		msg, known := patternMessages[p]
		if !known {
			msg = "does not match " + p
		}
		ds = append(ds, at(n, Error, "pattern", path, msg))
	}
	if f, ok := s["format"].(string); ok && f == "date" && k == "string" {
		if _, err := time.Parse("2006-01-02", n.Value); err != nil {
			ds = append(ds, at(n, Error, "format", path, "expected a date such as 2026-12-31"))
		}
	}
	if k == "string" {
		length := len([]rune(n.Value))
		lo, hasLo := number(s, "minLength")
		hi, hasHi := number(s, "maxLength")
		switch {
		case hasLo && float64(length) < lo && lo == 1:
			ds = append(ds, at(n, Error, "length", path, "must not be empty"))
		case hasLo && float64(length) < lo:
			ds = append(ds, at(n, Error, "length", path, fmt.Sprintf("must be at least %s characters", trimFloat(lo))))
		case hasHi && float64(length) > hi:
			ds = append(ds, at(n, Error, "length", path, fmt.Sprintf("must be at most %s characters", trimFloat(hi))))
		}
	}
	if k == "integer" || k == "number" {
		v, err := strconv.ParseFloat(n.Value, 64)
		if err != nil || math.IsInf(v, 0) {
			return append(ds, at(n, Error, "range", path, "number out of range"))
		}
		lo, hasLo := number(s, "minimum")
		hi, hasHi := number(s, "maximum")
		if (hasLo && v < lo) || (hasHi && v > hi) {
			switch {
			case hasLo && hasHi:
				ds = append(ds, at(n, Error, "range", path, fmt.Sprintf("must be from %s to %s", trimFloat(lo), trimFloat(hi))))
			case hasLo:
				ds = append(ds, at(n, Error, "range", path, "must be at least "+trimFloat(lo)))
			default:
				ds = append(ds, at(n, Error, "range", path, "must be at most "+trimFloat(hi)))
			}
		}
	}
	return ds
}

// allowedKeys are the properties a map may have: its own, plus those of the
// schemas it composes when unevaluatedProperties is false.
func (w *walker) allowedKeys(s map[string]any, into map[string]bool) {
	if props, ok := s["properties"].(map[string]any); ok {
		for k := range props {
			if k != orderKey {
				into[k] = true
			}
		}
	}
	if ref, ok := s["$ref"].(string); ok {
		w.allowedKeys(w.def(ref), into)
	}
	if list, ok := s["allOf"].([]any); ok {
		for _, b := range list {
			if bs, ok := b.(map[string]any); ok {
				w.allowedKeys(bs, into)
			}
		}
	}
}

func (w *walker) object(n *yaml.Node, s map[string]any, path string) []Diagnostic {
	var ds []Diagnostic
	props, _ := s["properties"].(map[string]any)
	seen := map[string]bool{}
	var allowed map[string]bool
	closed := s["additionalProperties"] == false
	if s["unevaluatedProperties"] == false {
		closed = true
		allowed = map[string]bool{}
		w.allowedKeys(s, allowed)
	}
	extra, _ := s["additionalProperties"].(map[string]any)
	names, _ := s["propertyNames"].(map[string]any)
	pairs := 0
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, val := n.Content[i], n.Content[i+1]
		if seen[key.Value] {
			continue // the duplicates pass reports it
		}
		seen[key.Value] = true
		pairs++
		p := childPath(path, key.Value)
		if names != nil {
			for _, d := range w.check(key, names, p) {
				if d.Code == "pattern" || d.Code == "length" {
					d.Message = strings.Replace(d.Message, "must be", "the key must be", 1)
				}
				ds = append(ds, d)
			}
		}
		if sub, ok := props[key.Value].(map[string]any); ok {
			ds = append(ds, w.check(val, sub, p)...)
			continue
		}
		if extra != nil {
			ds = append(ds, w.check(val, extra, p)...)
			continue
		}
		if closed && (allowed == nil || !allowed[key.Value]) {
			ds = append(ds, unknownField(key, p, w.knownKeys(s, props, allowed)))
		}
	}
	if req, ok := s["required"].([]any); ok {
		for _, r := range req {
			if !seen[fmt.Sprint(r)] {
				ds = append(ds, at(n, Error, "required", childPath(path, fmt.Sprint(r)), "required"))
			}
		}
	}
	if lo, ok := number(s, "minProperties"); ok && float64(pairs) < lo {
		ds = append(ds, at(n, Error, "empty", path, "declares nothing"))
	}
	if hi, ok := number(s, "maxProperties"); ok && float64(pairs) > hi {
		ds = append(ds, at(n, Error, "size", path, fmt.Sprintf("has more than %s keys", trimFloat(hi))))
	}
	return ds
}

func (w *walker) knownKeys(s, props map[string]any, allowed map[string]bool) []string {
	var keys []string
	if allowed != nil {
		for k := range allowed {
			keys = append(keys, k)
		}
	} else {
		for k := range props {
			if k != orderKey {
				keys = append(keys, k)
			}
		}
	}
	sort.Strings(keys)
	return keys
}

func unknownField(key *yaml.Node, path string, known []string) Diagnostic {
	msg := "unknown field"
	if guess := closest(key.Value, known); guess != "" {
		msg += fmt.Sprintf(" (did you mean %q?)", guess)
	}
	return at(key, Error, "unknown-field", path, msg)
}

// Closest is closest for another linter: kates test lint and flow lint
// suggest a key the same way.
func Closest(key string, known []string) string {
	return closest(key, known)
}

// closest is the known key within two edits of key, if one is.
func closest(key string, known []string) string {
	best, bestDist := "", 3
	for _, k := range known {
		if d := editDistance(strings.ToLower(key), strings.ToLower(k)); d < bestDist {
			best, bestDist = k, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

func (w *walker) array(n *yaml.Node, s map[string]any, path string) []Diagnostic {
	var ds []Diagnostic
	if items, ok := s["items"].(map[string]any); ok {
		for i, item := range n.Content {
			ds = append(ds, w.check(item, items, fmt.Sprintf("%s[%d]", path, i))...)
		}
	}
	count := float64(len(n.Content))
	if lo, ok := number(s, "minItems"); ok && count < lo {
		ds = append(ds, at(n, Error, "empty", path, "lists no value"))
	}
	if hi, ok := number(s, "maxItems"); ok && count > hi {
		ds = append(ds, at(n, Error, "size", path, fmt.Sprintf("lists more than %s values", trimFloat(hi))))
	}
	return ds
}

// operatorShape holds a constraint map to its operators, with the words the
// API's reader uses: equals and oneOf stand alone, min and max combine.
func operatorShape(n *yaml.Node, path string) []Diagnostic {
	if n.Kind != yaml.MappingNode {
		return nil
	}
	has := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		has[n.Content[i].Value] = true
	}
	switch {
	case !has["equals"] && !has["min"] && !has["max"] && !has["oneOf"]:
		return []Diagnostic{at(n, Error, "constraint", path, "needs equals, min, max or oneOf")}
	case has["equals"] && (has["min"] || has["max"] || has["oneOf"]):
		return []Diagnostic{at(n, Error, "constraint", path, "equals stands alone: drop min, max and oneOf")}
	case has["oneOf"] && (has["min"] || has["max"]):
		return []Diagnostic{at(n, Error, "constraint", path, "oneOf stands alone: drop min and max")}
	}
	return nil
}
