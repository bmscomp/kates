package conformity

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// catalogueRule is one entry of testdata/rules.json: a reference fragment
// (under spec:) that must trigger the rule, and a near miss that must not.
type catalogueRule struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Triggers string `json:"triggers"`
	NearMiss string `json:"nearMiss"`
}

func catalogue(t *testing.T) []catalogueRule {
	t.Helper()
	data, err := os.ReadFile("testdata/rules.json")
	if err != nil {
		t.Fatal(err)
	}
	var rules []catalogueRule
	if err := json.Unmarshal(data, &rules); err != nil {
		t.Fatal(err)
	}
	return rules
}

// underSpec turns a fragment into a reference, its lines under spec:.
func underSpec(fragment string) []byte {
	var b strings.Builder
	b.WriteString(header + "spec:\n")
	for _, line := range strings.Split(strings.TrimRight(fragment, "\n"), "\n") {
		b.WriteString("  " + line + "\n")
	}
	return []byte(b.String())
}

func TestCatalogue(t *testing.T) {
	for _, r := range catalogue(t) {
		t.Run(r.Code, func(t *testing.T) {
			ds := Compile(underSpec(r.Triggers), lintDay).Diagnostics
			hit := find(ds, r.Code)
			if hit == nil {
				t.Errorf("the trigger does not fire it; got %v", codes(ds))
			} else if hit.Severity.String() != r.Severity {
				t.Errorf("severity %s, want %s: %s", hit.Severity, r.Severity, hit.Message)
			}
			for _, d := range ds {
				if d.Severity == Error && d.Code != r.Code && hit == nil {
					t.Errorf("the trigger does not compile: %s", d.Format("trigger"))
				}
			}
			near := Compile(underSpec(r.NearMiss), lintDay).Diagnostics
			if d := find(near, r.Code); d != nil {
				t.Errorf("the near miss fires it: %s", d.Format("nearMiss"))
			}
		})
	}
}

// The catalogue and the registered checks are the same set, so a check
// ships with its trigger and near miss.
func TestEveryCatalogueRuleIsRegistered(t *testing.T) {
	registered := map[string]bool{}
	for _, c := range incoherenceChecks {
		registered[c.code] = true
	}
	for _, r := range catalogue(t) {
		if !registered[r.Code] {
			t.Errorf("no check registered for %s", r.Code)
		}
		delete(registered, r.Code)
	}
	for code := range registered {
		t.Errorf("no catalogue entry for %s", code)
	}
}

func codes(ds []Diagnostic) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.Code
	}
	return out
}
