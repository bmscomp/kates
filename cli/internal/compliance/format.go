package compliance

import (
	"bytes"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Format writes a reference canonically: keys in the schema's order (a
// broker config's in alphabetical order), two-space indentation, a
// constraint map on one line, and each constraint in its shortest form:
// {equals: x} and {oneOf: [x]} become x, {min: x, max: x} becomes x, and
// severity: fail, the default, goes. Exact duplicates go too: a key set
// twice to the same value, a value repeated in a list, an item listed twice.
// Comments stay with what they annotate.
//
// It refuses, with diagnostics, a file that does not parse or sets one key to
// two values: only the author can say which one is meant.
func Format(src []byte) ([]byte, []Diagnostic) {
	var doc yaml.Node
	if _, ds := parse(src); ds != nil {
		return nil, ds
	}
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, []Diagnostic{syntaxError(err)}
	}
	root := resolveAlias(doc.Content[0])
	var conflicts []Diagnostic
	for _, d := range duplicates(root, "") {
		if d.Code == "conflicting-key" {
			conflicts = append(conflicts, d)
		}
	}
	if len(conflicts) > 0 {
		return nil, conflicts
	}
	w := schemaWalker()
	head := ""
	if len(root.Content) > 0 {
		// A comment above the first key is the file's; it stays on top.
		head, root.Content[0].HeadComment = root.Content[0].HeadComment, ""
	}
	doc.Content[0] = w.format(root, []map[string]any{w.root})
	if head != "" && len(doc.Content[0].Content) > 0 {
		first := doc.Content[0].Content[0]
		first.HeadComment = strings.TrimSpace(head + "\n" + first.HeadComment)
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, []Diagnostic{{Severity: Error, Code: "format", Message: err.Error()}}
	}
	if err := enc.Close(); err != nil {
		return nil, []Diagnostic{{Severity: Error, Code: "format", Message: err.Error()}}
	}
	return spaceSections(out.Bytes()), nil
}

// spaceSections puts one blank line before each section of spec but the
// first, above the comment that heads it; YAML keeps no blank lines.
func spaceSections(out []byte) []byte {
	lines := strings.Split(string(out), "\n")
	var result []string
	inSpec, first := false, true
	for _, line := range lines {
		if !strings.HasPrefix(line, " ") && line != "" && !strings.HasPrefix(line, "#") {
			inSpec, first = line == "spec:", true
		}
		section := inSpec && strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && !strings.HasPrefix(line, "  #") && !strings.HasPrefix(line, "  - ")
		if section {
			if !first {
				at := len(result)
				for at > 0 && strings.HasPrefix(result[at-1], "  #") {
					at--
				}
				if at > 0 && result[at-1] != "" {
					result = append(result[:at], append([]string{""}, result[at:]...)...)
				}
			}
			first = false
		}
		result = append(result, line)
	}
	return []byte(strings.Join(result, "\n"))
}

// effective is every schema that applies to a node of this kind: s itself
// and what it composes, taking from a oneOf the branches that accept the kind.
func (w *walker) effective(n *yaml.Node, schemas []map[string]any) []map[string]any {
	var out []map[string]any
	var visit func(s map[string]any)
	// What a schema composes comes before the schema itself, so a role's own
	// fields come before controllers' dedicated.
	visit = func(s map[string]any) {
		if s == nil {
			return
		}
		defer func() { out = append(out, s) }()
		if ref, ok := s["$ref"].(string); ok {
			visit(w.def(ref))
		}
		for _, key := range []string{"allOf", "oneOf", "anyOf"} {
			list, _ := s[key].([]any)
			for _, b := range list {
				bs, _ := b.(map[string]any)
				if key == "allOf" || w.kinds(bs)[kind(n)] {
					visit(bs)
				}
			}
		}
	}
	for _, s := range schemas {
		visit(s)
	}
	return out
}

// child is what applies to a key's value, or to a list's items when key is "".
func child(schemas []map[string]any, key string) []map[string]any {
	var out []map[string]any
	for _, s := range schemas {
		if key == "" {
			if items, ok := s["items"].(map[string]any); ok {
				out = append(out, items)
			}
			continue
		}
		if props, ok := s["properties"].(map[string]any); ok {
			if sub, ok := props[key].(map[string]any); ok {
				out = append(out, sub)
				continue
			}
		}
		if extra, ok := s["additionalProperties"].(map[string]any); ok {
			out = append(out, extra)
		}
	}
	return out
}

func order(schemas []map[string]any) (keys []string, free bool) {
	seen := map[string]bool{}
	for _, s := range schemas {
		if props, ok := s["properties"].(map[string]any); ok {
			names, _ := props[orderKey].([]string)
			for _, k := range names {
				if !seen[k] {
					seen[k] = true
					keys = append(keys, k)
				}
			}
		}
		if _, ok := s["additionalProperties"].(map[string]any); ok {
			free = true
		}
	}
	return keys, free
}

// isConstraint says whether a map is a constraint's map form.
func isConstraint(schemas []map[string]any) bool {
	keys, _ := order(schemas)
	has := map[string]bool{}
	for _, k := range keys {
		has[k] = true
	}
	return has["equals"] && has["severity"]
}

func (w *walker) format(n *yaml.Node, schemas []map[string]any) *yaml.Node {
	n = resolveAlias(n)
	eff := w.effective(n, schemas)
	switch n.Kind {
	case yaml.SequenceNode:
		var items []*yaml.Node
		for _, item := range n.Content {
			dup := false
			for _, kept := range items {
				if equalNodes(kept, item) {
					dup = true
					break
				}
			}
			if !dup {
				items = append(items, w.format(item, child(eff, "")))
			}
		}
		n.Content = items
		n.Style &^= yaml.FlowStyle
	case yaml.MappingNode:
		pairs := map[string][2]*yaml.Node{}
		var original []string
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, val := n.Content[i], n.Content[i+1]
			if _, dup := pairs[key.Value]; dup {
				continue // an exact duplicate: Format refused the others
			}
			formatted := w.format(val, child(eff, key.Value))
			keepOnKeyLine(key, formatted)
			pairs[key.Value] = [2]*yaml.Node{key, formatted}
			original = append(original, key.Value)
		}
		keys, free := order(eff)
		var sorted []string
		if free && len(keys) == 0 {
			sorted = append(sorted, original...)
			sort.Strings(sorted)
		} else {
			for _, k := range keys {
				if _, ok := pairs[k]; ok {
					sorted = append(sorted, k)
				}
			}
			for _, k := range original {
				if !contains(sorted, k) {
					sorted = append(sorted, k)
				}
			}
		}
		n.Content = n.Content[:0]
		for _, k := range sorted {
			n.Content = append(n.Content, pairs[k][0], pairs[k][1])
		}
		n.Style &^= yaml.FlowStyle
		if isConstraint(eff) {
			allowed, _ := order(eff)
			return shortest(n, allowed)
		}
	}
	return n
}

// keepOnKeyLine puts the comment written on a key's line where yaml.v3
// prints it on that line: on a value written on one line, or on the key of
// a value that spans lines. Anywhere else it prints after the next key.
func keepOnKeyLine(key, val *yaml.Node) {
	comment := strings.TrimSpace(key.LineComment + " " + val.LineComment)
	if val.Kind == yaml.ScalarNode || val.Style&yaml.FlowStyle != 0 {
		key.LineComment, val.LineComment = "", comment
	} else {
		key.LineComment, val.LineComment = comment, ""
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// shortest writes a constraint in its shortest form, on one line when no
// comment sits inside it. It shortens only operators the constraint's schema
// allows, so it never makes a refused reference pass.
func shortest(m *yaml.Node, allowed []string) *yaml.Node {
	if sev := get(m, "severity"); sev != nil && sev.Value == "fail" && !hasComments(sev) && !hasComments(keyNode(m, "severity")) {
		drop(m, "severity")
	}
	keys := make([]string, 0, len(m.Content)/2)
	for i := 0; i+1 < len(m.Content); i += 2 {
		keys = append(keys, m.Content[i].Value)
	}
	var kept *yaml.Node
	switch strings.Join(keys, ",") {
	case "equals":
		kept = get(m, "equals")
	case "oneOf":
		if list := get(m, "oneOf"); contains(allowed, "oneOf") && list.Kind == yaml.SequenceNode && len(list.Content) == 1 {
			kept = list.Content[0]
		}
	case "min,max":
		lo, hi := get(m, "min"), get(m, "max")
		if contains(allowed, "min") && equalNodes(lo, hi) {
			kept = lo
		}
	}
	if kept != nil && kept.Kind == yaml.ScalarNode {
		inner := make([]*yaml.Node, 0, len(m.Content))
		inner = append(inner, m.Content...)
		moveComments(kept, append([]*yaml.Node{m}, inner...))
		return kept
	}
	if !hasComments(m.Content...) {
		m.Style |= yaml.FlowStyle
		if list := get(m, "oneOf"); list != nil {
			list.Style |= yaml.FlowStyle
		}
	}
	return m
}

func drop(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

func hasComments(nodes ...*yaml.Node) bool {
	for _, n := range nodes {
		if n == nil {
			continue
		}
		if n.HeadComment != "" || n.LineComment != "" || n.FootComment != "" || hasComments(n.Content...) {
			return true
		}
	}
	return false
}

// moveComments gives kept the comments of the nodes it replaces.
func moveComments(kept *yaml.Node, from []*yaml.Node) {
	join := func(parts ...string) string {
		var out []string
		for _, p := range parts {
			if p != "" {
				out = append(out, p)
			}
		}
		return strings.Join(out, "\n")
	}
	for _, f := range from {
		if f == kept {
			continue
		}
		kept.HeadComment = join(f.HeadComment, kept.HeadComment)
		kept.LineComment = join(kept.LineComment, f.LineComment)
		kept.FootComment = join(kept.FootComment, f.FootComment)
	}
}
