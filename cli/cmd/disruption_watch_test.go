package cmd

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/bmscomp/kates/cli/client"
)

// TestDisruptionWatch_SendsAPIKey runs disruption watch against a backend that
// refuses a request without the key, as the Kates API does by default. The
// command used to open the stream with http.DefaultClient, which sent no key,
// so it failed with "unexpected status: 401" wherever keys are on.
func TestDisruptionWatch_SendsAPIKey(t *testing.T) {
	const key = "watch-key-0123456789abcdef"
	var (
		mu    sync.Mutex
		auths []string
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
		mu.Unlock()
		if r.URL.Path != "/api/disruptions/d-42/stream" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusTeapot)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+key {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"status":401,"error":"Unauthorized","message":"Missing API key"}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: STARTED\ndata: {\"type\":\"STARTED\",\"message\":\"plan started\"}\n\n"+
			"event: STEP_STARTED\ndata: {\"type\":\"STEP_STARTED\",\"stepName\":\"kill-leader\",\"message\":\"step started\"}\n\n"+
			"event: COMPLETED\ndata: {\"type\":\"COMPLETED\",\"message\":\"plan completed\"}\n\n")
	}))
	t.Cleanup(ts.Close)
	origClient := apiClient
	t.Cleanup(func() { apiClient = origClient })

	t.Run("with the key", func(t *testing.T) {
		apiClient = client.NewWithAPIKey(ts.URL, key)
		stdout, stderr, err := commandOutput(t, func() error { return runCommandLine(t, "disruption", "watch", "d-42") })
		if err != nil {
			t.Fatalf("watch: %v\n%s", err, stderr)
		}
		for _, want := range []string{"Watching disruption: d-42", "plan started", "[kill-leader] step started", "Disruption completed"} {
			if !strings.Contains(string(stdout), want) {
				t.Errorf("stdout lacks %q:\n%s", want, stdout)
			}
		}
	})

	t.Run("a refused key reports the status", func(t *testing.T) {
		apiClient = client.NewWithAPIKey(ts.URL, "wrong-key")
		_, _, err := commandOutput(t, func() error { return runCommandLine(t, "disruption", "watch", "d-42") })
		var httpErr *client.HTTPError
		if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusUnauthorized {
			t.Fatalf("err = %v, want the 401 as a client.HTTPError", err)
		}
		if !strings.Contains(err.Error(), "Missing API key") {
			t.Errorf("err = %v, want the backend's message", err)
		}
	})

	mu.Lock()
	defer mu.Unlock()
	if len(auths) == 0 || auths[0] != "Bearer "+key {
		t.Errorf("Authorization headers = %q, want the key on the first request", auths)
	}
}
