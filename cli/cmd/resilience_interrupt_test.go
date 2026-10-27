package cmd

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
)

// kates resilience run waits up to 20 minutes for POST /api/resilience to
// answer. It used to wait on context.Background(), deaf to Ctrl-C; its
// context now ends the call, and the command says that the run may go on,
// since the Kates API has no way to cancel a resilience run, and how to
// follow it.
func TestResilienceRun_CtrlCStopsWaiting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.yaml")
	data := "testRequest:\n  type: LOAD\nchaosSpec:\n  experimentName: x\nsteadyStateSec: 1\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	// The API answers only when the run ends; here, never. The call is
	// cancelled once the request has reached the server, so the command is
	// waiting when the signal comes.
	started := make(chan struct{}, 1)
	over := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/resilience" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		// The server notices a closed connection only once the body is read.
		_, _ = io.ReadAll(r.Body)
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-over:
		}
	}))
	defer ts.Close()
	defer close(over)
	apiClient = client.New(ts.URL)
	apiClient.MaxRetries = 1
	output.ResetForTesting()
	outputMode = "table"
	resilienceFile, resilienceDryRun = "", false
	defer func() { resilienceFile = "" }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Error("the request never reached the API")
		}
		cancel()
	}()

	begin := time.Now()
	_, stderr, err := commandOutput(t, func() error {
		return runWithContext(t, ctx, "resilience", "run", "-f", path)
	})
	if !errors.Is(err, errInterrupted) {
		t.Fatalf("err = %v, want errInterrupted, which exits 130\n%s", err, stderr)
	}
	if took := time.Since(begin); took > 5*time.Second {
		t.Errorf("the command took %s to stop after the cancel", took)
	}
	for _, want := range []string{"Stopped waiting", "cannot cancel", "kates disruption list"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
}

// Only the commands that wait for work on the Kates API are interruptible;
// resilience run is one of them, so Execute runs it with the context Ctrl-C
// cancels.
func TestResilienceRun_IsInterruptible(t *testing.T) {
	if !isInterruptible(resilienceRunCmd) {
		t.Error("kates resilience run is not marked interruptible")
	}
}
