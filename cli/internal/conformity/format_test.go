package conformity

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func format(t *testing.T, src string) string {
	t.Helper()
	out, ds := Format([]byte(src))
	if out == nil {
		t.Fatalf("refused: %+v", ds)
	}
	return string(out)
}

// The examples are formatted: kates conformity fmt --check passes on them.
func TestExamplesAreFormatted(t *testing.T) {
	files, _ := filepath.Glob("../../examples/conformity/*.yaml")
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if out, _ := Format(src); !bytes.Equal(out, src) {
			t.Errorf("%s is not formatted; run kates conformity fmt -w %s", f, f)
		}
	}
}

// Formatting twice changes nothing, and never changes what a file means.
func TestFormatIsIdempotentAndKeepsTheMeaning(t *testing.T) {
	files, _ := filepath.Glob("testdata/*/*.yaml")
	examplesFiles, _ := filepath.Glob("../../examples/conformity/*.yaml")
	for _, f := range append(files, examplesFiles...) {
		src, _ := os.ReadFile(f)
		once, _ := Format(src)
		if once == nil {
			continue // refused: a conflicting key or broken YAML
		}
		twice, _ := Format(once)
		if !bytes.Equal(once, twice) {
			t.Errorf("%s: a second fmt changes it:\n%s\n---\n%s", f, once, twice)
		}
		if unfixable(Compile(src, lintDay).Diagnostics) != unfixable(Compile(once, lintDay).Diagnostics) {
			t.Errorf("%s: fmt changed whether it compiles, beyond removing duplicates", f)
		}
	}
}

func TestFormatWritesTheShortestCanonicalForm(t *testing.T) {
	got := format(t, `# a reference
kind: KafkaReference
apiVersion: conformity.kates.io/v1alpha1
spec:
  brokers: {zones: {min: 3, max: 3}, count: {equals: 3, severity: fail}, perNode: {max: 1, severity: warn}}
  kafka:
    config:
      unclean.leader.election.enable: false
      default.replication.factor: {min: 3}
    authorization: {oneOf: [simple, simple]} # kept
  versions:
    kafka: {min: "4.2.0"}
  waivers:
    - {rule: brokers.zones, reason: r, by: me, until: 2026-12-31}
    - {rule: brokers.zones, reason: r, by: me, until: 2026-12-31}
metadata: {name: t}
`)
	want := `# a reference
apiVersion: conformity.kates.io/v1alpha1
kind: KafkaReference
metadata:
  name: t
spec:
  versions:
    kafka: {min: "4.2.0"}

  brokers:
    count: 3
    zones: 3
    perNode: {max: 1, severity: warn}

  kafka:
    authorization: simple # kept
    config:
      default.replication.factor: {min: 3}
      unclean.leader.election.enable: false

  waivers:
    - rule: brokers.zones
      reason: r
      by: me
      until: 2026-12-31
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormatKeepsCommentsInsideAConstraint(t *testing.T) {
	got := format(t, header+"spec:\n  brokers:\n    count:\n      min: 3 # three zones\n      max: 9\n")
	if !strings.Contains(got, "min: 3 # three zones") {
		t.Errorf("the comment is lost or the map squashed:\n%s", got)
	}
}

func TestFormatRefusesWhatOnlyTheAuthorCanSettle(t *testing.T) {
	for _, src := range []string{
		header + "spec:\n  brokers:\n    count: 3\n    count: 4\n",
		header + "spec:\n  brokers: [\n",
	} {
		if out, ds := Format([]byte(src)); out != nil || !HasErrors(ds) {
			t.Errorf("formatted %q; want a refusal", src)
		}
	}
}

// unfixable counts the errors fmt cannot remove.
func unfixable(ds []Diagnostic) int {
	n := 0
	for _, d := range ds {
		if d.Severity == Error && !d.Fixable {
			n++
		}
	}
	return n
}

// A comment on a key's line stays on that line, whether fmt writes the
// value on one line or over several.
func TestFormatKeepsTheCommentOnTheKeyLine(t *testing.T) {
	src := header + "spec:\n  brokers:\n    count: # quorum\n      min: 3\n    zones: 3\n" +
		"  topics:\n    replicationFactor: 3\n    exclude: [kates-] # Kates' own\n  monitoring:\n    podMonitor: true\n"
	out, ds := Format([]byte(src))
	if ds != nil {
		t.Fatalf("refused: %v", ds)
	}
	for _, want := range []string{"    count: {min: 3} # quorum\n    zones: 3\n", "    exclude: # Kates' own\n      - kates-\n", "  monitoring:\n"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
}
