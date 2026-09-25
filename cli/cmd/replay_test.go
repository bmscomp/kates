package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
)

// replayAgainst runs kates replay against a backend that serves run as the
// original and answers the create; it returns the body the replay posted.
func replayAgainst(t *testing.T, run string) map[string]any {
	t.Helper()
	var posted []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/tests/0a1b2c3d":
			_, _ = w.Write([]byte(run))
		case r.Method == http.MethodPost && r.URL.Path == "/api/tests":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			posted = body
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"9f8e7d6c","testType":"LOAD","status":"PENDING"}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	apiClient = client.New(ts.URL)
	outputMode = "table"
	output.ResetForTesting()
	replayWait = false

	if err := replayCmd.RunE(replayCmd, []string{"0a1b2c3d"}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(posted, &body); err != nil {
		t.Fatalf("posted %q: %v", posted, err)
	}
	return body
}

// A replay sends the request the run was started with, so the backend merges
// it with the type's defaults as it did the first time. The effective spec
// is not a request: sent back it asked for what the defaults had filled in,
// including fields the type cannot use, which the backend refuses.
func TestReplay_SendsTheRequestedSpec(t *testing.T) {
	body := replayAgainst(t, `{
		"id": "0a1b2c3d", "testType": "ENDURANCE", "backend": "native", "status": "DONE",
		"spec": {"numRecords": 1000, "throughput": 5000, "lingerMs": 0, "acks": "all"},
		"requestedSpec": {"numRecords": 1000, "lingerMs": 0}
	}`)

	want := map[string]any{"numRecords": 1000.0, "lingerMs": 0.0}
	got, _ := body["spec"].(map[string]any)
	if len(got) != len(want) || got["numRecords"] != want["numRecords"] || got["lingerMs"] != want["lingerMs"] {
		t.Errorf("spec = %v, want %v", body["spec"], want)
	}
	if body["type"] != "ENDURANCE" || body["backend"] != "native" {
		t.Errorf("body = %v", body)
	}
}

// A run stored before the backend kept the request has only the merged
// spec, which holds the seven fields that backend never used, at their Java
// defaults. The replay leaves them out and sends the rest as the run used
// it, the rate included, so the new run does what the old one did.
func TestReplay_ARunWithoutARequestedSpecSendsWhatItUsed(t *testing.T) {
	body := replayAgainst(t, `{
		"id": "0a1b2c3d", "testType": "INTEGRITY", "status": "DONE",
		"spec": {"numRecords": 1000, "throughput": 5000, "acks": "all", "lingerMs": 0,
			"targetThroughput": -1, "fetchMinBytes": 1, "fetchMaxWaitMs": 500,
			"enableIdempotence": false, "enableTransactions": false, "enableCrc": true}
	}`)

	got, _ := body["spec"].(map[string]any)
	want := map[string]any{"numRecords": 1000.0, "throughput": 5000.0, "acks": "all", "lingerMs": 0.0}
	if len(got) != len(want) {
		t.Fatalf("spec = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("spec.%s = %v, want %v", k, got[k], v)
		}
	}
}
