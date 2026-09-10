package migrate

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A small JSON Schema checker covering the subset the two charts' schemas
// use — type, enum, properties, additionalProperties, required, items,
// minItems, minimum, maximum, minLength, pattern and $ref into definitions —
// so the values this package writes are held to the charts' contract
// without a schema library in go.mod.

const (
	mirrorSchemaPath = "../../../charts/mirror-maker2/values.schema.json"
	sourceSchemaPath = "../../../charts/legacy-kafka/values.schema.json"
)

type schemaChecker struct {
	root map[string]any
}

func loadSchema(t *testing.T, path string) *schemaChecker {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("chart schema not available: %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return &schemaChecker{root: root}
}

// validateYAML checks a values document against the schema and returns
// every violation found.
func (c *schemaChecker) validateYAML(t *testing.T, doc string) []string {
	t.Helper()
	var v any
	if err := yaml.Unmarshal([]byte(doc), &v); err != nil {
		t.Fatalf("values are not valid YAML: %v\n%s", err, doc)
	}
	var errs []string
	c.check(c.root, normalize(v), "$", &errs)
	return errs
}

// normalize turns yaml.v3's generic decoding into the shapes the checker
// expects (maps keyed by string, numbers as float64).
func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range x {
			out[k] = normalize(val)
		}
		return out
	case map[any]any:
		out := map[string]any{}
		for k, val := range x {
			out[fmt.Sprint(k)] = normalize(val)
		}
		return out
	case []any:
		for i := range x {
			x[i] = normalize(x[i])
		}
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	default:
		return v
	}
}

func (c *schemaChecker) resolve(s map[string]any) map[string]any {
	ref, ok := s["$ref"].(string)
	if !ok {
		return s
	}
	const prefix = "#/definitions/"
	if !strings.HasPrefix(ref, prefix) {
		panic("unsupported $ref " + ref)
	}
	defs, _ := c.root["definitions"].(map[string]any)
	target, ok := defs[strings.TrimPrefix(ref, prefix)].(map[string]any)
	if !ok {
		panic("unknown $ref " + ref)
	}
	return target
}

func (c *schemaChecker) check(schema map[string]any, v any, path string, errs *[]string) {
	schema = c.resolve(schema)
	if !typeMatches(schema["type"], v) {
		*errs = append(*errs, fmt.Sprintf("%s: type %v, want %v", path, typeName(v), schema["type"]))
		return
	}
	if enum, ok := schema["enum"].([]any); ok {
		found := false
		for _, e := range enum {
			if e == v {
				found = true
			}
		}
		if !found {
			*errs = append(*errs, fmt.Sprintf("%s: %v is not one of %v", path, v, enum))
		}
	}
	switch x := v.(type) {
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		if req, ok := schema["required"].([]any); ok {
			for _, r := range req {
				if _, present := x[r.(string)]; !present {
					*errs = append(*errs, fmt.Sprintf("%s: required key %v missing", path, r))
				}
			}
		}
		for k, val := range x {
			sub, known := props[k].(map[string]any)
			if !known {
				if ap, isBool := schema["additionalProperties"].(bool); isBool && !ap {
					*errs = append(*errs, fmt.Sprintf("%s: key %q is not in the schema", path, k))
				}
				continue
			}
			c.check(sub, val, path+"."+k, errs)
		}
	case []any:
		if min, ok := schema["minItems"].(float64); ok && float64(len(x)) < min {
			*errs = append(*errs, fmt.Sprintf("%s: %d items, want at least %v", path, len(x), min))
		}
		if items, ok := schema["items"].(map[string]any); ok {
			for i, val := range x {
				c.check(items, val, fmt.Sprintf("%s[%d]", path, i), errs)
			}
		}
	case float64:
		if min, ok := schema["minimum"].(float64); ok && x < min {
			*errs = append(*errs, fmt.Sprintf("%s: %v is below the minimum %v", path, x, min))
		}
		if max, ok := schema["maximum"].(float64); ok && x > max {
			*errs = append(*errs, fmt.Sprintf("%s: %v is above the maximum %v", path, x, max))
		}
	case string:
		if min, ok := schema["minLength"].(float64); ok && float64(len(x)) < min {
			*errs = append(*errs, fmt.Sprintf("%s: %q is shorter than %v", path, x, min))
		}
		if pat, ok := schema["pattern"].(string); ok {
			if !regexp.MustCompile(pat).MatchString(x) {
				*errs = append(*errs, fmt.Sprintf("%s: %q does not match %s", path, x, pat))
			}
		}
	}
}

func typeMatches(want any, v any) bool {
	switch w := want.(type) {
	case nil:
		return true
	case string:
		return typeIs(w, v)
	case []any:
		for _, t := range w {
			if typeIs(t.(string), v) {
				return true
			}
		}
		return false
	}
	return true
}

func typeIs(t string, v any) bool {
	switch t {
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "integer":
		f, ok := v.(float64)
		return ok && f == math.Trunc(f)
	case "number":
		_, ok := v.(float64)
		return ok
	case "null":
		return v == nil
	}
	return false
}

func typeName(v any) string {
	switch v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case nil:
		return "null"
	}
	return fmt.Sprintf("%T", v)
}

// TestSchemaCheckerCatchesViolations makes sure the checker is not a rubber
// stamp before the values tests rely on it.
func TestSchemaCheckerCatchesViolations(t *testing.T) {
	c := loadSchema(t, sourceSchemaPath)
	bad := "mode: kraft\nkafka:\n  version: 3.9.1\n  image: apache/kafka:3.9.1\n  replicas: 0\n  replicationFactor: 1\nlisteners: {}\nnoSuchKey: 1\n"
	errs := c.validateYAML(t, bad)
	joined := strings.Join(errs, "\n")
	for _, want := range []string{"noSuchKey", "replicas: 0 is below the minimum 1"} {
		if !strings.Contains(joined, want) {
			t.Errorf("checker missed %q; got:\n%s", want, joined)
		}
	}
	m := loadSchema(t, mirrorSchemaPath)
	bad = "target: {}\nmirrors:\n  - source: {}\n    sourceConnector:\n      bogus: 1\nreplicationPolicy:\n  mode: sideways\n"
	joined = strings.Join(m.validateYAML(t, bad), "\n")
	for _, want := range []string{`key "bogus"`, "sideways is not one of"} {
		if !strings.Contains(joined, want) {
			t.Errorf("checker missed %q; got:\n%s", want, joined)
		}
	}
}
