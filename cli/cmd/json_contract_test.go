package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The -o json contract (output/render.go, exitcodes.go): a command run with
// -o json puts one JSON document on stdout and nothing else, whatever it has
// to say beside it, and a command with no JSON form says so and exits 2.
//
// Each case runs kates as a subprocess, through Execute and the root's
// pre-run, against a fake API that answers the GETs the command makes, with
// no terminal and a kubectl stub that answers nothing. The commands that
// start work on the API (gate, benchmark, test apply, tune run) are covered
// in json_output_test.go with the in-process runs backend; the ones that need
// a cluster or a terminal are listed in jsonContractSkipped with the reason.

// jsonContractRoutes is the fake API: a GET path and its body.
var jsonContractRoutes = map[string]string{
	"/api/health":                     `{"status":"UP","version":"1.0.0","uptimeSeconds":60,"kafka":{"status":"UP","bootstrapServers":"kafka:9092"},"engine":{"activeBackend":"native"}}`,
	"/api/tests":                      `{"items":[{"id":"run-1","testType":"LOAD","status":"DONE","backend":"native","createdAt":"2026-01-01T00:00:00Z"}],"page":0,"size":20,"total":1,"totalPages":1,"count":1}`,
	"/api/tests/run-1":                `{"id":"run-1","testType":"LOAD","status":"DONE","spec":{"batchSize":16384},"results":[{"phaseName":"produce","status":"DONE","recordsSent":1000,"throughputRecordsPerSec":25000,"p99LatencyMs":12.5}]}`,
	"/api/tests/run-1/report":         `{"testId":"run-1","summary":{"totalRecords":1000,"avgThroughputRecPerSec":25000,"p99LatencyMs":12.5}}`,
	"/api/tests/run-1/report/summary": `{"totalRecords":1000,"avgThroughputRecPerSec":25000,"p99LatencyMs":12.5}`,
	"/api/tests/types":                `["LOAD","STRESS"]`,
	"/api/tests/backends":             `["native"]`,
	"/api/tests/tuning/types":         `[{"type":"TUNE_BATCHING","parameter":"batch.size","steps":4,"description":"Sweep batch.size"}]`,
	"/api/audit":                      `{"items":[]}`,
	"/api/schedules":                  `[]`,
	"/api/disruptions":                `{"items":[]}`,
	"/api/disruptions/types":          `[]`,
	// Three brokers and healthy partitions: kates doctor passes every
	// required check, so its exit code is 0 when the JSON is right.
	"/api/cluster/info":   `{"clusterId":"abc","brokerCount":3,"brokers":[{"id":0,"host":"kafka-0","port":9092},{"id":1,"host":"kafka-1","port":9092},{"id":2,"host":"kafka-2","port":9092}]}`,
	"/api/cluster/topics": `{"items":["orders"]}`,
	"/api/cluster/check":  `{"clusterId":"abc","brokers":3,"partitionHealth":{"underReplicated":0,"offline":0}}`,
	"/api/kafka/brokers":  `{"clusterId":"abc","brokerCount":1,"brokers":[{"id":0,"host":"kafka","port":9092}]}`,
	"/api/kafka/topics":   `[]`,
}

// jsonContractCases are the leaf commands with a JSON form. noJSON marks a
// command that has none and must refuse -o json.
var jsonContractCases = []struct {
	args   []string
	noJSON bool
}{
	{args: []string{"health"}},
	{args: []string{"status"}},
	{args: []string{"version"}},
	{args: []string{"doctor"}},
	{args: []string{"ctx", "show"}},
	{args: []string{"ctx", "current"}},
	{args: []string{"ctx", "profiles"}},
	{args: []string{"test", "list"}},
	{args: []string{"test", "get", "run-1"}},
	{args: []string{"test", "types"}},
	{args: []string{"test", "backends"}},
	{args: []string{"report", "show", "run-1"}},
	{args: []string{"report", "summary", "run-1"}},
	{args: []string{"explain", "run-1"}},
	{args: []string{"advisor", "run-1"}},
	{args: []string{"tune", "types"}},
	{args: []string{"audit"}},
	{args: []string{"schedule", "list"}},
	{args: []string{"disruption", "list"}},
	{args: []string{"disruption", "types"}},
	{args: []string{"chaos", "list"}},
	{args: []string{"cluster", "info"}},
	{args: []string{"cluster", "topics"}},
	{args: []string{"kafka", "brokers"}},
	{args: []string{"kafka", "topics"}},
	// No JSON form: exit 2, nothing on stdout.
	{args: []string{"webhook", "list"}, noJSON: true},
	{args: []string{"theme", "list"}, noJSON: true},
	{args: []string{"plugin", "list"}, noJSON: true},
	{args: []string{"profile", "list"}, noJSON: true},
	{args: []string{"snapshot", "list"}, noJSON: true},
	{args: []string{"changelog"}, noJSON: true},
	{args: []string{"test", "summary"}, noJSON: true},
}

// jsonContractSkipped names the leaf commands this test does not run, and why.
var jsonContractSkipped = map[string]string{
	"gate, benchmark, test apply, tune run": "start runs and poll them: json_output_test.go covers them with the runs backend",
	"conformity check, detect, versions, operators, deploy, clean, kyverno, auto, migrate, connect, doctor dns, doctor network": "need a cluster through kubectl or helm",
	"security *":                            "need the API's security endpoints, which the fake does not model",
	"lab, dashboard, top, watch, kafka tui": "full-screen views with no JSON form; they refuse without a terminal",
	"test create, test delete, test cancel, prune, webhook add/remove, schedule create/delete": "mutate the API; their JSON forms are tested beside them",
}

func TestJSONContract(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the kubectl stub is a shell script")
	}
	_ = jsonContractSkipped
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body, ok := jsonContractRoutes[r.URL.Path]
		if r.Method != http.MethodGet || !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":404,"error":"Not Found","message":"no route for ` + r.Method + ` ` + r.URL.Path + `"}`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(api.Close)

	home := t.TempDir()
	config := "current-context: lab\ncontexts:\n  lab:\n    url: " + api.URL + "\n    api-key: lab-key-0123456789abcdef\n"
	if err := os.WriteFile(filepath.Join(home, ".kates.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	// kubectl that finds nothing, so kates doctor's Kyverno checks fail as
	// warnings rather than reaching whatever cluster the developer has.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tt := range jsonContractCases {
		name := strings.Join(tt.args, " ")
		t.Run(name, func(t *testing.T) {
			stdout, stderr, code := runKatesJSON(t, home, bin, tt.args...)
			if tt.noJSON {
				if code != couldNotTellExitCode {
					t.Errorf("exit %d, want %d for a command with no JSON form\nstdout:\n%s\nstderr:\n%s", code, couldNotTellExitCode, stdout, stderr)
				}
				if stdout != "" {
					t.Errorf("stdout is not empty for a command with no JSON form:\n%s", stdout)
				}
				if !strings.Contains(stderr, "this command has no JSON output") {
					t.Errorf("stderr does not say the command has no JSON output:\n%s", stderr)
				}
				return
			}
			if code != 0 {
				t.Errorf("exit %d\nstderr:\n%s", code, stderr)
			}
			var doc any
			dec := json.NewDecoder(strings.NewReader(stdout))
			if err := dec.Decode(&doc); err != nil {
				t.Fatalf("stdout is not JSON: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
			}
			if dec.More() {
				t.Errorf("stdout carries more than one JSON document:\n%s", stdout)
			}
			if strings.Contains(stdout, "\x1b[") {
				t.Errorf("stdout carries escape sequences:\n%q", stdout)
			}
		})
	}
}

// runKatesJSON runs kates -o json with args as a subprocess, HOME at home and
// bin first on PATH, and returns its stdout, stderr and exit code.
func runKatesJSON(t *testing.T, home, bin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperRunKates$")
	cmd.Env = append(os.Environ(),
		"KATES_TEST_RUN_AS_CLI=1",
		"KATES_TEST_ARGS="+strings.Join(append(append([]string{}, args...), "-o", "json"), " "),
		"KATES_TEST_HOME="+home,
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"KATES_URL=", "KATES_API_KEY=", "KATES_CONTEXT=", "KATES_OUTPUT=", "KATES_PROFILES=", "KATES_PLAIN=",
	)
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		code = exit.ExitCode()
	case err != nil:
		t.Fatal(err)
	}
	return out.String(), errBuf.String(), code
}
