package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"gopkg.in/yaml.v3"
)

// conformityInstance reads a reference as JSON, as the CLI must send it:
// from the node tree, so that a scalar keeps its text unless YAML tags it a
// number, a boolean or null. Decoding into any would turn until: 2026-12-31
// into "2026-12-31T00:00:00Z", which the Kates API refuses as a date.
func conformityInstance(t *testing.T, data []byte) any {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(conformityValue(t, &doc))
	if err != nil {
		t.Fatal(err)
	}
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		t.Fatal(err)
	}
	return instance
}

func conformityValue(t *testing.T, n *yaml.Node) any {
	t.Helper()
	switch n.Kind {
	case yaml.DocumentNode:
		return conformityValue(t, n.Content[0])
	case yaml.AliasNode:
		return conformityValue(t, n.Alias)
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			m[n.Content[i].Value] = conformityValue(t, n.Content[i+1])
		}
		return m
	case yaml.SequenceNode:
		s := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			s = append(s, conformityValue(t, c))
		}
		return s
	}
	switch n.ShortTag() {
	case "!!int", "!!float", "!!bool", "!!null":
		var v any
		if err := n.Decode(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	return n.Value
}

// Every reference cli/examples/conformity ships is one the JSON Schema beside
// it accepts, so an editor shows no error on an example. The Kates API's
// KafkaReferenceExamplesTest reads the same files with its strict reader, so
// the schema, the API's model and the examples stay in step.
func TestConformityExamplesMatchTheirSchema(t *testing.T) {
	dir := filepath.Join("..", "examples", "conformity")
	raw, err := os.ReadFile(filepath.Join(dir, "kafkareference.v1alpha1.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}

	paths, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) < 2 {
		t.Fatalf("found only %d conformity examples: %v", len(paths), paths)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := resolved.Validate(conformityInstance(t, data)); err != nil {
				t.Error(err)
			}
		})
	}

	// A waiver's date arrives as the date it was written, not a timestamp.
	until := conformityInstance(t, []byte("until: 2026-12-31\n")).(map[string]any)["until"]
	if until != "2026-12-31" {
		t.Errorf("until arrives as %v, want 2026-12-31", until)
	}

	// The schema refuses too: a typo is not an example.
	typo := []byte("apiVersion: conformity.kates.io/v1alpha1\nkind: KafkaReference\n" +
		"metadata: {name: typo}\nspec: {brokers: {cuont: 3}}\n")
	if err := resolved.Validate(conformityInstance(t, typo)); err == nil {
		t.Error("the schema accepts brokers.cuont")
	}
}
