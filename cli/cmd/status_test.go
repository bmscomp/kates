package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
)

// statusAPI is a Kates API whose answer to the status's two requests is
// state(n), n counting the health requests from 1.
func statusAPI(t *testing.T, state func(n int64) (health string, running int)) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var polls atomic.Int64
	var n atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/health":
			polls.Add(1)
			health, _ := state(n.Add(1))
			fmt.Fprintf(w, `{"status":%q,"kafka":{"status":"UP"}}`, health)
		case "/api/tests":
			_, running := state(max(n.Load(), 1))
			items := make([]string, running)
			for i := range items {
				items[i] = fmt.Sprintf(`{"id":"r%d","testType":"LOAD","status":"RUNNING"}`, i)
			}
			fmt.Fprintf(w, `{"items":[%s],"page":0,"size":100,"total":%d}`, strings.Join(items, ","), running)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts, &polls
}

// useStatusAPI points the command globals at url for the test.
func useStatusAPI(t *testing.T, url, mode string) *bytes.Buffer {
	t.Helper()
	prevClient, prevURL, prevCtx, prevMode := apiClient, apiURL, activeContext, outputMode
	t.Cleanup(func() {
		apiClient, apiURL, activeContext, outputMode = prevClient, prevURL, prevCtx, prevMode
		output.Out, output.Err = os.Stdout, os.Stderr
	})
	apiClient, apiURL, activeContext, outputMode = client.New(url), url, "lab", mode
	return output.ResetForTesting()
}

// Away from a terminal, a watch prints a line when the status changes and
// nothing while it stays the same.
func TestStatusWatchPrintsChangesOnly(t *testing.T) {
	ts, polls := statusAPI(t, func(n int64) (string, int) {
		switch {
		case n <= 3:
			return "UP", 0
		case n <= 6:
			return "UP", 2
		default:
			return "DEGRADED", 2
		}
	})
	buf := useStatusAPI(t, ts.URL, "table")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- watchStatus(ctx, 5*time.Millisecond, false) }()
	for polls.Load() < 9 {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("a stopped watch: err = %v, want nil (exit 0)", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("%d lines for 3 states over %d polls, want 3:\n%s", len(lines), polls.Load(), buf)
	}
	for i, want := range []string{"0 running", "2 running", "DEGRADED"} {
		if !strings.Contains(lines[i], want) {
			t.Errorf("line %d lacks %q: %s", i+1, want, lines[i])
		}
	}
	if strings.Contains(buf.String(), "\033[2J") {
		t.Errorf("non-terminal watch cleared the screen:\n%q", buf)
	}
}

// -o json --watch is NDJSON: one compact object per change.
func TestStatusWatchJSONIsNDJSON(t *testing.T) {
	ts, polls := statusAPI(t, func(n int64) (string, int) {
		if n <= 2 {
			return "UP", 1
		}
		return "UP", 0
	})
	buf := useStatusAPI(t, ts.URL, "json")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- watchStatus(ctx, 5*time.Millisecond, true) }()
	for polls.Load() < 5 {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("%d lines, want one object per change (2):\n%s", len(lines), buf)
	}
	for i, wantRunning := range []int{1, 0} {
		var res statusResult
		if err := json.Unmarshal([]byte(lines[i]), &res); err != nil {
			t.Fatalf("line %d is not a JSON object: %v\n%s", i+1, err, lines[i])
		}
		if res.Context != "lab" || res.Status != "UP" || res.RunningTests != wantRunning {
			t.Errorf("line %d = %+v, want lab, UP, %d running", i+1, res, wantRunning)
		}
	}
}

// In a terminal the line is redrawn on a cleared screen every poll.
func TestStatusWatchRedrawsInATerminal(t *testing.T) {
	ts, polls := statusAPI(t, func(int64) (string, int) { return "UP", 0 })
	buf := useStatusAPI(t, ts.URL, "table")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- watchStatus(ctx, 5*time.Millisecond, true) }()
	for polls.Load() < 3 {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(buf.String(), "\033[2J"); got < 2 {
		t.Errorf("cleared the screen %d times over %d polls, want once a poll:\n%q", got, polls.Load(), buf)
	}
}

// An API that goes away is a status change, not the end of the watch.
func TestStatusWatchSurvivesAnUnreachableAPI(t *testing.T) {
	gone := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(gone.Close)
	buf := useStatusAPI(t, gone.URL, "table")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := watchStatus(ctx, 10*time.Millisecond, false); err != nil {
		t.Fatalf("err = %v, want nil once the watch is stopped", err)
	}
	if got := strings.Count(buf.String(), "unreachable"); got != 1 {
		t.Errorf("%d unreachable lines, want 1 (no change, no new line):\n%s", got, buf)
	}
}

func TestStatusWatchRefusesABadInterval(t *testing.T) {
	prevWatch, prevInterval := statusWatch, statusInterval
	t.Cleanup(func() { statusWatch, statusInterval = prevWatch, prevInterval })
	statusWatch, statusInterval = true, 0
	err := statusCmd.RunE(statusCmd, nil)
	var exit *exitError
	if !errors.As(err, &exit) || exit.code != couldNotTellExitCode {
		t.Errorf("--interval 0: err = %v, want exit 2", err)
	}
}

// Ctrl-C stops a watch with exit 0: the user ended it, nothing failed.
func TestSIGINT_EndsStatusWatchWithExit0(t *testing.T) {
	ts, polls := statusAPI(t, func(int64) (string, int) { return "UP", 0 })
	var out bytes.Buffer
	kates := startKates(t, &out, "--url", ts.URL, "status", "--watch", "--interval", "1")
	deadline := time.Now().Add(15 * time.Second)
	for polls.Load() < 1 {
		if time.Now().After(deadline) {
			t.Fatal("kates never polled the status")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := kates.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if ws := waitExit(t, kates, &out); ws.Signaled() || ws.ExitStatus() != 0 {
		t.Errorf("kates ended with %s, want exit status 0\n%s", describeExit(ws), &out)
	}
}
