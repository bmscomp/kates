package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"gopkg.in/yaml.v3"
)

// conformityInstance reads a reference as JSON, as the CLI sends it: numbers
// as float64 and maps keyed by string.
func conformityInstance(t *testing.T, data []byte) any {
	t.Helper()
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		t.Fatal(err)
	}
	return instance
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

	// The schema refuses too: a typo is not an example.
	typo := []byte("apiVersion: conformity.kates.io/v1alpha1\nkind: KafkaReference\n" +
		"metadata: {name: typo}\nspec: {brokers: {cuont: 3}}\n")
	if err := resolved.Validate(conformityInstance(t, typo)); err == nil {
		t.Error("the schema accepts brokers.cuont")
	}
}
