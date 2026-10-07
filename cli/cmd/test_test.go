package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/pflag"
)

func TestTestGet_ShowsIntegrityRtoRpoAndVerdict(t *testing.T) {
	mockResponse := `{
		"id": "run-1",
		"testType": "INTEGRITY",
		"status": "DONE",
		"results": [{
			"phaseName": "integrity",
			"status": "DONE",
			"integrity": {
				"totalSent": 1000, "totalAcked": 1000, "totalConsumed": 998,
				"lostRecords": 2, "dataLossPercent": 0.2,
				"producerRto": 1.5, "maxRto": 1.5, "rpo": null,
				"producerRtoMs": 1500.0, "consumerRtoMs": 0.0, "maxRtoMs": 1500.0, "rpoMs": -1.0,
				"verdict": "DATA_LOSS"
			}
		}]
	}`
	ts, buf := setupTest(t, "GET", "/api/tests/run-1", 200, mockResponse)
	defer ts.Close()

	if err := testGetCmd.RunE(testGetCmd, []string{"run-1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stripAnsi(buf.String())
	for _, want := range []string{"Producer RTO", "1500 ms", "Max RTO", "DATA_LOSS"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// rpoMs -1 is "not measured", never a zero or a hidden line.
	if !strings.Contains(out, "not measured") {
		t.Errorf("unmeasured RPO should print as not measured:\n%s", out)
	}
	if strings.Contains(out, "-1 ms") {
		t.Errorf("the -1 sentinel must not be printed as a value:\n%s", out)
	}
}

// The throughput bar is drawn against the rate the run used, spec.throughput:
// throughput wins over targetThroughput when a request sets both, and a
// request that sets only throughput has no targetThroughput at all.
func TestTestGet_ThroughputBarUsesTheRateTheRunUsed(t *testing.T) {
	ts, buf := setupTest(t, "GET", "/api/tests/run-2", 200, `{
		"id": "run-2", "testType": "LOAD", "status": "DONE",
		"spec": {"throughput": 300, "targetThroughput": 2000},
		"results": [{"phaseName": "produce", "status": "DONE", "throughputRecordsPerSec": 290}]
	}`)
	defer ts.Close()

	if err := testGetCmd.RunE(testGetCmd, []string{"run-2"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 290 of 300 fills 19 of the bar's 20 cells; against 2000 it filled 2.
	var bar string
	for _, line := range strings.Split(stripAnsi(buf.String()), "\n") {
		if strings.Contains(line, "Throughput") && strings.Contains(line, output.Glyphs().BarEmpty) {
			bar = line
		}
	}
	if got := strings.Count(bar, output.Glyphs().BarFull); got != 19 {
		t.Errorf("bar %q fills %d cells, want 19: it is not drawn against the rate the run used", bar, got)
	}
}

// A run's linger.ms of 0 shows as 0, and a spec that reports no linger.ms or
// batch.size shows a dash for it, not a 0 the run never reported.
func TestTestGet_ShowsTheProducerSettingsTheRunReports(t *testing.T) {
	for _, tt := range []struct{ spec, batchSize, lingerMs string }{
		{`{"numRecords": 500000, "batchSize": 16384, "lingerMs": 0}`, "16.4K", "0"},
		{`{"numRecords": 500000}`, "—", "—"},
	} {
		ts, buf := setupTest(t, "GET", "/api/tests/run-3", 200,
			`{"id": "run-3", "testType": "ROUND_TRIP", "status": "RUNNING", "spec": `+tt.spec+`}`)
		if err := testGetCmd.RunE(testGetCmd, []string{"run-3"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		ts.Close()
		shown := map[string]string{}
		for _, line := range strings.Split(stripAnsi(buf.String()), "\n") {
			if fields := strings.Fields(line); len(fields) == 3 {
				shown[fields[0]+" "+fields[1]] = fields[2]
			}
		}
		if shown["Batch Size"] != tt.batchSize || shown["Linger ms"] != tt.lingerMs {
			t.Errorf("spec %s shows batch size %q and linger %q, want %q and %q",
				tt.spec, shown["Batch Size"], shown["Linger ms"], tt.batchSize, tt.lingerMs)
		}
	}
}

// A flag of 0 for batch.size, linger.ms or fetch.max.wait.ms is a setting, so
// it is sent, and one not given is left out, so the type's default applies.
// --linger-ms 0 alone used to send no spec at all, and so did --throughput -1,
// which left an ENDURANCE run at its type's 5,000 records/s. The other flags
// still send nothing for a 0.
func TestTestCreate_SendsTheFlagsGiven(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want map[string]any // the spec posted, nil when there is none
	}{
		{[]string{"--type", "LOAD", "--linger-ms", "0"}, map[string]any{"lingerMs": 0.0}},
		{[]string{"--type", "STRESS", "--records", "1000", "--batch-size", "0", "--linger-ms", "0"},
			map[string]any{"numRecords": 1000.0, "batchSize": 0.0, "lingerMs": 0.0}},
		{[]string{"--type", "LOAD", "--fetch-max-wait-ms", "0"}, map[string]any{"fetchMaxWaitMs": 0.0}},
		{[]string{"--type", "LOAD", "--records", "1000"}, map[string]any{"numRecords": 1000.0}},
		{[]string{"--type", "ENDURANCE", "--throughput", "-1"}, map[string]any{"targetThroughput": -1.0}},
		{[]string{"--type", "LOAD", "--consumers", "0", "--throughput", "0"}, nil},
		{[]string{"--type", "LOAD"}, nil},
	} {
		if got := createPosts(t, tt.args...); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("kates test create %s posted spec %v, want %v", strings.Join(tt.args, " "), got, tt.want)
		}
	}
}

// createPosts runs kates test create with args against a backend that answers
// the create, and returns the spec it posted, nil when it posted none. It puts
// the command's flags back to their defaults afterwards: values and Changed
// both stay set between runCommandLine calls otherwise.
func createPosts(t *testing.T, args ...string) map[string]any {
	t.Helper()
	defer testCreateCmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
	var body struct {
		Spec map[string]any `json:"spec"`
	}
	posts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/tests" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		posts++
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"run-1","testType":"LOAD","status":"PENDING"}`)
	}))
	defer ts.Close()
	apiClient = client.New(ts.URL)
	outputMode = "table"
	output.ResetForTesting()

	if err := runCommandLine(t, append([]string{"test", "create"}, args...)...); err != nil {
		t.Fatalf("kates test create %s: %v", strings.Join(args, " "), err)
	}
	if posts != 1 {
		t.Fatalf("kates test create %s posted %d requests", strings.Join(args, " "), posts)
	}
	return body.Spec
}
