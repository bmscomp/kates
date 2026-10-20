package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
)

const conformityReference = `apiVersion: conformity.kates.io/v1alpha1
kind: KafkaReference
metadata: {name: prod}
spec:
  versions: {kafka: {min: "4.10"}}
  brokers: {count: {min: 3}}
  waivers:
    - {rule: brokers.count, reason: OPS-1, by: me, until: 2026-12-31}
`

// conformityStub answers POST /api/conformity/check with status and body, and
// keeps what it was sent.
func conformityStub(t *testing.T, status int, body string) *map[string]any {
	t.Helper()
	sent := map[string]any{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/conformity/check" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		data, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(data, &sent); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(ts.Close)
	apiClient = client.New(ts.URL)
	output.ResetForTesting()
	return &sent
}

func conformityRun(t *testing.T) (string, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "prod.yaml")
	if err := os.WriteFile(path, []byte(conformityReference), 0o644); err != nil {
		t.Fatal(err)
	}
	conformityFile, conformityJUnit = path, filepath.Join(t.TempDir(), "conformity.xml")
	t.Cleanup(func() { conformityFile, conformityJUnit = "", "" })
	buf := output.ResetForTesting()
	var err error
	captureStdout(t, func() { err = runConformityCheck(nil, nil) })
	return buf.String(), err
}

func exitCode(err error) int {
	var exit *exitError
	if errors.As(err, &exit) {
		return exit.code
	}
	if err != nil {
		return 1
	}
	return 0
}

const conformityFailing = `{"reference":"prod","target":{"namespace":"kafka","name":"krafter"},
"cluster":{"ready":true,"generation":2,"observedGeneration":1},"verdict":"FAIL",
"summary":{"pass":1,"fail":2,"warn":0,"waived":1,"unknown":0},"results":[
{"rule":"versions.kafka","status":"FAIL","severity":"fail","expected":{"min":"4.10"},"observed":"4.3.1"},
{"rule":"kafka.listeners.require[plain]","status":"FAIL","severity":"fail",
 "expected":{"port":{"equals":9092},"tls":{"equals":true},"auth":{"oneOf":["tls","scram-sha-512"]}},
 "observed":{"type":"internal","port":9092,"tls":false,"auth":"scram-sha-512"},"detail":"tls is false"},
{"rule":"brokers.count","status":"WAIVED","severity":"fail","expected":{"min":3},"observed":2,
 "waiver":{"status":"FAIL","reason":"OPS-1","by":"me","until":"2026-12-31"}},
{"rule":"controllers.count","status":"PASS","severity":"fail","expected":{"equals":3},"observed":3}]}`

func TestConformityCheckSendsDatesAndVersionsAsWritten(t *testing.T) {
	sent := conformityStub(t, 200, conformityFailing)

	if _, err := conformityRun(t); exitCode(err) != 1 {
		t.Fatalf("exit %d, want 1 for a FAIL verdict", exitCode(err))
	}
	spec := (*sent)["spec"].(map[string]any)
	if got := spec["waivers"].([]any)[0].(map[string]any)["until"]; got != "2026-12-31" {
		t.Errorf("until sent as %v", got)
	}
	if got := spec["versions"].(map[string]any)["kafka"].(map[string]any)["min"]; got != "4.10" {
		t.Errorf("a quoted version sent as %v", got)
	}
}

// Each file the API's strict reader would refuse, but a node walk would send
// with a rule missing, is refused before it is sent.
func TestConformityJSONRefusesWhatItWouldDrop(t *testing.T) {
	for _, tc := range []struct{ name, yaml, want string }{
		{"repeated key", "spec:\n  brokers: {count: {min: 3}}\n  brokers: {zones: {min: 3}}\n", `line 3: mapping key "brokers" already defined at line 2`},
		{"second document", "kind: KafkaReference\n---\nkind: KafkaReference\n", "line 2: a second document; one KafkaReference per file"},
		{"merge key", "role: &role {count: {min: 3}}\nspec: {brokers: {<<: *role}}\n", "line 2: merge keys (<<) are not read"},
		{"anchor in itself", "x: &a [1, *a]\n", "anchor 'a' value contains itself"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := conformityJSON([]byte(tc.yaml)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
	if v, err := conformityJSON([]byte("kind: KafkaReference\n---\n")); err != nil || v.(map[string]any)["kind"] != "KafkaReference" {
		t.Errorf("a trailing --- gives %v, %v", v, err)
	}
}

func TestConformityCheckShowsTheGaps(t *testing.T) {
	conformityStub(t, 200, conformityFailing)

	out, _ := conformityRun(t)
	for _, want := range []string{
		"versions.kafka", "min 4.10", "4.3.1", "waived until 2026-12-31 by me: OPS-1", "reconcile pending", "1 passing rules not shown",
		"port 9092, tls true, auth one of tls, scram-sha-512", "type internal, port 9092, tls false, auth scram-sha-512",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "controllers.count") {
		t.Error("a passing rule is listed")
	}
}

func TestConformityCheckJUnit(t *testing.T) {
	conformityStub(t, 200, conformityFailing)
	path := filepath.Join(t.TempDir(), "prod.yaml")
	if err := os.WriteFile(path, []byte(conformityReference), 0o644); err != nil {
		t.Fatal(err)
	}
	junit := filepath.Join(t.TempDir(), "conformity.xml")
	conformityFile, conformityJUnit = path, junit
	defer func() { conformityFile, conformityJUnit = "", "" }()
	captureStdout(t, func() { _ = runConformityCheck(nil, nil) })

	data, err := os.ReadFile(junit)
	if err != nil {
		t.Fatal(err)
	}
	xml := string(data)
	for _, want := range []string{
		`<testsuite name="conformity.brokers" tests="1" failures="0" errors="0" skipped="1"`,
		`<skipped message="FAIL, waived until 2026-12-31 by me: OPS-1">`,
		`<failure message="expected min 4.10, observed 4.3.1" type="fail">`,
		`<failure message="expected port 9092, tls true, auth one of tls, scram-sha-512, observed type internal, port 9092, tls false, auth scram-sha-512" type="fail">`,
	} {
		if !strings.Contains(xml, want) {
			t.Errorf("JUnit lacks %s:\n%s", want, xml)
		}
	}
}

// A warn rule cannot fail the check, so a CI report must not count it as an
// error when it could not be read.
func TestConformityJUnitPassesAnUnknownWarnRule(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conformity.xml")
	report := &client.ConformityReport{Verdict: "UNKNOWN", Results: []client.ConformityResult{
		{Rule: "monitoring.podMonitor", Status: "UNKNOWN", Severity: "warn", Detail: "podmonitors is forbidden"},
		{Rule: "kafka.authorization", Status: "UNKNOWN", Severity: "fail", Detail: "Kubernetes API not available"},
	}}
	if err := writeConformityJUnit(report, path, 0); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<testsuite name="conformity.monitoring" tests="1" failures="0" errors="0"`,
		`<system-out>UNKNOWN (warn): podmonitors is forbidden</system-out>`,
		`<testsuite name="conformity.kafka" tests="1" failures="0" errors="1"`,
		`<error message="Kubernetes API not available" type="unknown"></error>`,
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("JUnit lacks %s:\n%s", want, data)
		}
	}
}

func TestConformityCheckExitCodes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   int
	}{
		{"pass", 200, `{"verdict":"PASS","results":[]}`, 0},
		{"unknown", 200, `{"verdict":"UNKNOWN","results":[]}`, 2},
		{"invalid reference", 400, `{"status":400,"error":"Validation Failed","message":"spec.brokers.cuont: unknown field"}`, 2},
		{"another cluster", 409, `{"status":409,"error":"Conflict","message":"The reference is for Kafka x/y"}`, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conformityStub(t, tc.status, tc.body)
			_, err := conformityRun(t)
			if got := exitCode(err); got != tc.want {
				t.Errorf("exit %d, want %d (%v)", got, tc.want, err)
			}
			if tc.status == 400 && !strings.Contains(err.Error(), "spec.brokers.cuont: unknown field") {
				t.Errorf("the refusal does not name the path: %v", err)
			}
		})
	}
}

func TestConformityCheckRefusesWithoutAFile(t *testing.T) {
	if got := exitCode(runConformityCheck(nil, nil)); got != 2 {
		t.Errorf("exit %d, want 2", got)
	}
}
