package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"gopkg.in/yaml.v3"
)

// A report whose status is ERROR says why, and the command prints it: the
// reason used to reach only the server log.
func TestResilienceRun_PrintsWhyTheReportIsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.yaml")
	data := "testRequest:\n  type: LOAD\nchaosSpec:\n  experimentName: x\nsteadyStateSec: 1\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	ts, buf := setupTest(t, "POST", "/api/resilience", 200,
		`{"status":"ERROR","error":"The benchmark did not start: Concurrency limit reached"}`)
	defer ts.Close()
	resilienceFile, resilienceDryRun = path, false
	defer func() { resilienceFile = "" }()

	if err := resilienceRunCmd.RunE(resilienceRunCmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out := stripAnsi(buf.String()); !strings.Contains(out, "The benchmark did not start: Concurrency limit reached") {
		t.Errorf("the reason is not printed:\n%s", out)
	}
}

// writeResilienceFile writes data to a file of that name in a fresh
// directory and returns its path.
func writeResilienceFile(t *testing.T, name, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// resilienceRunPosting runs kates resilience run on the file at path against
// a backend that answers COMPLETED, and returns the body it posted.
func resilienceRunPosting(t *testing.T, path string) []byte {
	t.Helper()
	var posted []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/resilience" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		posted = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"COMPLETED"}`))
	}))
	defer ts.Close()
	apiClient = client.New(ts.URL)
	outputMode = "table"
	output.ResetForTesting()
	resilienceFile, resilienceDryRun = path, false
	defer func() { resilienceFile = "" }()

	if err := resilienceRunCmd.RunE(resilienceRunCmd, nil); err != nil {
		t.Fatalf("kates resilience run -f %s: %v", path, err)
	}
	if posted == nil {
		t.Fatalf("kates resilience run -f %s posted nothing", path)
	}
	return posted
}

// asJSON is v as encoding/json reads it back, the form a posted body takes
// once decoded: numbers become float64.
func asJSON(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// checkSentAsWritten fails unless body, the JSON a run posted, holds what the
// YAML file says, each field once, and nothing else.
func checkSentAsWritten(t *testing.T, path string, body []byte) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file any
	if err := yaml.Unmarshal(data, &file); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	var sent any
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("posted %q: %v", body, err)
	}
	if want := asJSON(t, file); !reflect.DeepEqual(sent, want) {
		t.Errorf("%s:\nposted %v\nfile   %v", path, sent, want)
	}
}

// The file is the request. Decoded into a struct, a probe lost its mode,
// expectedOutput, comparator, intervalSec and timeoutSec, so the API ran each
// as an Edge probe that passed whatever its command printed; a scenario was
// dropped; gracePeriodSec: 0 and targetBrokerId: 0 were left out, which the
// API reads as 30 seconds and a random broker; and a file without
// steadyStateSec sent 0 for it, where the API waits 30 seconds.
func TestResilienceRun_SendsTheFileAsWritten(t *testing.T) {
	path := writeResilienceFile(t, "r.yaml", `testRequest:
  type: LOAD
  scenario:
    name: two-topics
    phases:
      - name: steady
        phaseType: STEADY
        durationMs: 60000
chaosSpec:
  experimentName: x
  disruptionType: POD_DELETE
  gracePeriodSec: 0
  targetBrokerId: 0
probes:
  - name: isr
    type: cmdProbe
    mode: Continuous
    command: "echo 0"
    expectedOutput: "0"
    comparator: "<="
    intervalSec: 5
    timeoutSec: 20
`)
	body := resilienceRunPosting(t, path)
	checkSentAsWritten(t, path, body)

	var sent map[string]any
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatal(err)
	}
	if v, ok := sent["steadyStateSec"]; ok {
		t.Errorf("steadyStateSec is sent as %v; the file has none, so the API's default applies", v)
	}
}

// Every resilience example cli/examples ships reaches the Kates API whole.
// ResilienceExamplesTest (kates/src/test) checks that the API reads each
// field of it as the example means it.
func TestResilienceRun_SendsEveryExampleAsWritten(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "examples", "resilience-*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) < 12 {
		t.Fatalf("found only %d resilience examples: %v", len(paths), paths)
	}
	for _, path := range paths {
		checkSentAsWritten(t, path, resilienceRunPosting(t, path))
	}
}

// A .json file's numbers reach the API as written, not through a float64,
// which holds integers exactly only up to 2^53.
func TestResilienceRun_SendsAJSONFilesNumbersAsWritten(t *testing.T) {
	path := writeResilienceFile(t, "r.json",
		`{"testRequest":{"type":"LOAD","spec":{"numRecords":9007199254740993}},"chaosSpec":{"experimentName":"x"}}`)

	if body := resilienceRunPosting(t, path); !strings.Contains(string(body), `"numRecords":9007199254740993`) {
		t.Errorf("posted %s", body)
	}
}

// A scenario may carry the test's type itself: the Kates API takes
// testRequest.scenario.type in place of testRequest.type.
func TestResilienceRun_TakesTheTypeFromTheScenario(t *testing.T) {
	path := writeResilienceFile(t, "r.yaml", `testRequest:
  scenario:
    type: LOAD
    phases:
      - name: steady
        phaseType: STEADY
chaosSpec:
  experimentName: x
`)
	checkSentAsWritten(t, path, resilienceRunPosting(t, path))
}

// --dry-run prints the body a run would send, which is the file, with its
// comparators as written rather than escaped for HTML.
func TestResilienceRun_DryRunPrintsTheFile(t *testing.T) {
	path := writeResilienceFile(t, "r.yaml", `testRequest:
  type: LOAD
chaosSpec:
  experimentName: x
probes:
  - name: isr
    mode: Continuous
    comparator: "<="
    timeoutSec: 20
`)
	output.ResetForTesting()
	resilienceFile, resilienceDryRun = path, true
	defer func() { resilienceFile, resilienceDryRun = "", false }()

	out := captureStdout(t, func() {
		if err := resilienceRunCmd.RunE(resilienceRunCmd, nil); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
	for _, want := range []string{`"mode": "Continuous"`, `"comparator": "<="`, `"timeoutSec": 20`} {
		if !strings.Contains(out, want) {
			t.Errorf("the dry run does not print %s:\n%s", want, out)
		}
	}
}

// A file the command can't send as the request it says is refused before
// anything is sent.
func TestResilienceRun_RefusesAFileItCannotSend(t *testing.T) {
	for _, tc := range []struct{ name, file, data, want string }{
		{"no type", "r.yaml", "testRequest:\n  spec: {}\nchaosSpec:\n  experimentName: x\n", "testRequest.type is required"},
		{"no experiment name", "r.yaml", "testRequest:\n  type: LOAD\nchaosSpec: {}\n", "chaosSpec.experimentName is required"},
		{"a value JSON can't hold", "r.yaml", "testRequest:\n  type: LOAD\nchaosSpec:\n  experimentName: x\n  networkLatencyMs: .inf\n", "doesn't convert to JSON"},
		{"two JSON values", "r.json", `{"testRequest":{"type":"LOAD"},"chaosSpec":{"experimentName":"x"}} {}`, "after the top-level JSON value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				_, _ = w.Write([]byte(`{"status":"COMPLETED"}`))
			}))
			defer ts.Close()
			apiClient = client.New(ts.URL)
			output.ResetForTesting()
			resilienceFile, resilienceDryRun = writeResilienceFile(t, tc.file, tc.data), false
			defer func() { resilienceFile = "" }()

			err := resilienceRunCmd.RunE(resilienceRunCmd, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want one that says %q", err, tc.want)
			}
		})
	}
}
