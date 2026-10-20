package conformity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	examples "github.com/bmscomp/kates/cli/examples/conformity"
)

const header = "apiVersion: conformity.kates.io/v1alpha1\nkind: KafkaReference\nmetadata: {name: t}\n"

func diagnose(t *testing.T, spec string) []Diagnostic {
	t.Helper()
	return Compile([]byte(header+"spec:\n"+spec), lintDay).Diagnostics
}

func find(ds []Diagnostic, code string) *Diagnostic {
	for i := range ds {
		if ds[i].Code == code {
			return &ds[i]
		}
	}
	return nil
}

func TestExamplesCompileClean(t *testing.T) {
	files, _ := filepath.Glob("../../examples/conformity/*.yaml")
	if len(files) < 3 {
		t.Fatalf("found %d examples", len(files))
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range Compile(src, lintDay).Diagnostics {
			t.Errorf("%s", d.Format(f))
		}
	}
}

// Each finding points at the node, with the API's path and words.
func TestDiagnosticsPointAtTheNode(t *testing.T) {
	ds := diagnose(t, "  brokers:\n    cuont: 3\n  versions:\n    kafka: {min: 4.10}\n")

	unknown := find(ds, "unknown-field")
	if unknown == nil || unknown.Line != 6 || unknown.Col != 5 || unknown.Path != "spec.brokers.cuont" ||
		!strings.Contains(unknown.Message, `did you mean "count"?`) {
		t.Errorf("unknown field: %+v", unknown)
	}
	quote := find(ds, "type")
	if quote == nil || quote.Path != "spec.versions.kafka.min" || !strings.Contains(quote.Message, `quote it ("4.10")`) {
		t.Errorf("unquoted version: %+v", quote)
	}
	if got := unknown.Format("prod.yaml"); got != `prod.yaml:6:5: error: spec.brokers.cuont: unknown field (did you mean "count"?) [unknown-field]` {
		t.Errorf("format: %s", got)
	}
}

func TestSyntaxAndShapeErrors(t *testing.T) {
	for _, tc := range []struct {
		name, src, code string
	}{
		{"empty", "", "syntax"},
		{"two documents", header + "spec: {brokers: {count: 3}}\n---\nkind: x\n", "syntax"},
		{"bad yaml", header + "spec:\n  brokers: [\n", "syntax"},
		{"not a map", "- 1\n", "type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds := Compile([]byte(tc.src), lintDay).Diagnostics
			if find(ds, tc.code) == nil || !HasErrors(ds) {
				t.Errorf("want an error %s, got %+v", tc.code, ds)
			}
		})
	}
}

func TestDuplicates(t *testing.T) {
	ds := diagnose(t, "  brokers:\n    count: 3\n    count: 3\n    zones: 2\n    zones: 3\n"+
		"  kafka:\n    authorization: {oneOf: [simple, simple]}\n"+
		"  waivers:\n    - {rule: brokers.count, reason: r, by: me, until: 2026-12-31}\n"+
		"    - {rule: brokers.count, reason: r, by: me, until: 2026-12-31}\n")

	if d := find(ds, "duplicate-key"); d == nil || d.Severity != Warning || !d.Fixable || d.Path != "spec.brokers.count" {
		t.Errorf("same key, same value: %+v", d)
	}
	if d := find(ds, "conflicting-key"); d == nil || d.Severity != Error || d.Fixable {
		t.Errorf("same key, another value: %+v", d)
	}
	if d := find(ds, "duplicate-value"); d == nil || d.Path != "spec.kafka.authorization.oneOf[1]" || !d.Fixable {
		t.Errorf("value listed twice: %+v", d)
	}
	if d := find(ds, "duplicate-item"); d == nil || d.Severity != Error || !d.Fixable {
		t.Errorf("waiver listed twice: %+v", d)
	}
	if find(ds, "duplicate-waiver") != nil {
		t.Error("an identical waiver is reported twice")
	}
}

func TestWaiverAndListenerChecks(t *testing.T) {
	ds := diagnose(t, "  brokers: {count: 3, zones: 3}\n"+
		"  kafka:\n    listeners:\n      require:\n        - {name: tls, tls: true}\n        - {name: tls, port: 9093}\n"+
		"  waivers:\n    - {rule: brokers.zone, reason: r, by: me, until: 2026-12-31}\n"+
		"    - {rule: brokers.count, reason: r, by: me, until: 2026-12-31}\n"+
		"    - {rule: brokers.count, reason: s, by: me, until: 2026-12-31}\n")

	if d := find(ds, "unknown-rule"); d == nil || !strings.Contains(d.Message, `did you mean "brokers.zones"?`) {
		t.Errorf("unknown rule: %+v", d)
	}
	if d := find(ds, "duplicate-waiver"); d == nil || d.Path != "spec.waivers[2].rule" {
		t.Errorf("second waiver for a rule: %+v", d)
	}
	if d := find(ds, "duplicate-listener"); d == nil || d.Path != "spec.kafka.listeners.require[1].name" {
		t.Errorf("listener named twice: %+v", d)
	}
}

func TestMinAboveMaxPerKind(t *testing.T) {
	for _, spec := range []string{
		"  brokers: {count: {min: 5, max: 3}}\n",
		"  brokers: {requests: {memory: {min: 8Gi, max: 4000Mi}}}\n",
		`  versions: {kafka: {min: "4.10", max: "4.9"}}` + "\n",
		"  kafka: {config: {num.partitions: {min: 12, max: 6}}}\n",
	} {
		if d := find(diagnose(t, spec), "min-above-max"); d == nil {
			t.Errorf("no min-above-max for %s", spec)
		}
	}
	if d := find(diagnose(t, "  brokers: {requests: {memory: {min: 1Gi, max: 1024Mi}}}\n"), "min-above-max"); d != nil {
		t.Errorf("1Gi is 1024Mi: %+v", d)
	}
}

// The walker covers every keyword the schema uses, and says each pattern in words.
func TestTheWalkerCoversTheSchema(t *testing.T) {
	var schema any
	if err := json.Unmarshal(examples.Schema, &schema); err != nil {
		t.Fatal(err)
	}
	var walk func(n any, inProperties bool)
	walk = func(n any, inProperties bool) {
		switch v := n.(type) {
		case map[string]any:
			for k, child := range v {
				if !inProperties && !schemaKeywords[k] {
					t.Errorf("the walker does not understand the schema keyword %q", k)
				}
				if k == "pattern" {
					if _, ok := patternMessages[child.(string)]; !ok {
						t.Errorf("no message for the pattern %s", child)
					}
				}
				if k == "enum" || k == "const" || k == "required" || k == "default" {
					continue
				}
				walk(child, !inProperties && (k == "properties" || k == "$defs" || k == "dependentSchemas"))
			}
		case []any:
			for _, child := range v {
				walk(child, false)
			}
		}
	}
	walk(schema, false)
}

// The editor's schema comment must link to the schema the check uses.
func TestSchemaHeader(t *testing.T) {
	body := header + "spec:\n  brokers:\n    count: 3\n"
	wrong := Compile([]byte("# yaml-language-server: $schema=https://example.com/kafkareference.v1beta1.schema.json\n"+body), lintDay).Diagnostics
	if d := find(wrong, "schema-header"); d == nil || d.Line != 1 || d.Severity != Warning {
		t.Errorf("a link to another schema: %+v", wrong)
	}
	right := Compile([]byte("# yaml-language-server: $schema=../schemas/"+schemaFile+"\n"+body), lintDay).Diagnostics
	if d := find(right, "schema-header"); d != nil {
		t.Errorf("a link to this schema warns: %s", d.Format("f"))
	}
}

// The report puts the source line under the finding, with the caret under
// its column, keeping a tab so the caret lines up.
func TestReportShowsTheSourceLine(t *testing.T) {
	d := Diagnostic{Line: 2, Col: 4, Severity: Error, Code: "c", Message: "m"}
	got := d.Report("f.yaml", []string{"a: 1", "\tb: x\r"})
	if want := "f.yaml:2:4: error: m [c]\n  2 | \tb: x\n    | \t  ^"; got != want {
		t.Errorf("report\n%s\nwant\n%s", got, want)
	}
	if got := (Diagnostic{Severity: Error, Code: "c", Message: "m"}).Report("f.yaml", nil); got != "f.yaml: error: m [c]" {
		t.Errorf("a finding without a line: %s", got)
	}
}

// An expired waiver warns at its date; one that ends today does not.
func TestWaiverExpired(t *testing.T) {
	ds := diagnose(t, "  brokers:\n    count: 3\n  waivers:\n    - {rule: brokers.count, reason: r, by: me, until: 2026-01-31}\n")
	d := find(ds, "waiver-expired")
	if d == nil || d.Path != "spec.waivers[0].until" || d.Line != 8 ||
		d.Message != "the waiver expired on 2026-01-31, so brokers.count counts again: renew the date or remove the waiver" {
		t.Errorf("expired waiver: %+v", ds)
	}
}

// topics declares rules of its own: a reference with no other compiles,
// and a waiver can name them.
func TestTopicsRules(t *testing.T) {
	ds := diagnose(t, "  topics:\n    replicationFactor: {min: 3}\n    exclude: [__]\n"+
		"  waivers:\n    - {rule: topics.replicationFactor, reason: r, by: me, until: 2026-12-31}\n")
	for _, d := range ds {
		t.Errorf("%s", d.Format("topics.yaml"))
	}
}
