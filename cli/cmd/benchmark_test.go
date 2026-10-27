package cmd

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A poll that failed used to be skipped without a word, so an API that had
// gone away was polled in vain until the hidden six-minute cap. Three
// failures in a row now end the phase with the last error.
func TestWaitForCompletion_ConsecutiveErrors(t *testing.T) {
	b := newRunsBackend(t) // a run it did not create answers 404

	run, err := waitForCompletion(context.Background(), "missing", time.Minute)
	if run != nil || err == nil {
		t.Fatalf("run = %v, err = %v; want an error", run, err)
	}
	if !strings.Contains(err.Error(), "3 times in a row") || !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v, want the count and the last error", err)
	}
	if polls := b.pollCount("missing"); polls != benchmarkPollFailures {
		t.Errorf("polled %d times, want %d", polls, benchmarkPollFailures)
	}
}

// A cancelled context ends the wait at once, with the context's error.
func TestWaitForCompletion_Cancelled(t *testing.T) {
	b := newRunsBackend(t)
	b.stuck["LOAD"] = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := waitForCompletion(ctx, "missing", time.Minute); err != context.Canceled {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if polls := b.pollCount("missing"); polls != 0 {
		t.Errorf("polled %d times after the cancel", polls)
	}
}

func TestParseBenchTimeout(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want time.Duration
	}{
		{"30m", 30 * time.Minute}, {"90", 90 * time.Second}, {"1h30m", 90 * time.Minute},
	} {
		if got, err := parseBenchTimeout(tc.in); err != nil || got != tc.want {
			t.Errorf("parseBenchTimeout(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []string{"", "0", "-5m", "soon"} {
		if _, err := parseBenchTimeout(bad); err == nil {
			t.Errorf("parseBenchTimeout(%q): no error", bad)
		}
	}
}
