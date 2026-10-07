package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmscomp/kates/cli/client"
)

func TestFindOrphanedRuns(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339Nano) }
	tenMinutes := &client.TestSpec{DurationMs: 600_000}
	fortyMinutes := &client.TestSpec{DurationMs: 2_400_000}

	runs := []client.TestRun{
		// The old rule, 5 minutes from the start, deleted this one while it
		// still had 10 minutes to go.
		{ID: "midway", CreatedAt: ago(5*time.Minute + time.Second), Spec: tenMinutes},
		{ID: "just-inside", CreatedAt: ago(39 * time.Minute), Spec: tenMinutes},
		{ID: "overdue", CreatedAt: ago(41 * time.Minute), Spec: tenMinutes},
		// An INTEGRITY run reads its records back for up to its duration
		// again: 75 minutes in, this one may still be reading, and the Kates
		// API allows it 85.
		{ID: "integrity-reading", TestType: "INTEGRITY", CreatedAt: ago(75 * time.Minute), Spec: fortyMinutes},
		{ID: "integrity-overdue", TestType: "INTEGRITY", CreatedAt: ago(111 * time.Minute), Spec: fortyMinutes},
		// A scenario's phases run one after another, and its spec shows
		// none of them: 90 minutes in, its base spec's ten are long over,
		// but its phases may add up to the two hours the Kates API allows.
		{ID: "scenario-running", ScenarioName: "ramp", CreatedAt: ago(90 * time.Minute), Spec: tenMinutes},
		{ID: "scenario-overdue", ScenarioName: "ramp", CreatedAt: ago(151 * time.Minute), Spec: tenMinutes},
		{ID: "no-duration", CreatedAt: ago(31 * time.Minute)},
		{ID: "no-duration-young", CreatedAt: ago(29 * time.Minute), Spec: &client.TestSpec{}},
		{ID: "no-zone", CreatedAt: now.Add(-3 * time.Hour).Format("2006-01-02T15:04:05")},
		{ID: "garbled", CreatedAt: "yesterday"},
		{ID: "empty", CreatedAt: ""},
	}

	orphans, unreadable := findOrphanedRuns(runs, now, 30*time.Minute)

	var got []string
	for _, o := range orphans {
		got = append(got, o.Run.ID)
	}
	if want := []string{"overdue", "integrity-overdue", "scenario-overdue", "no-duration", "no-zone"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("orphans = %v, want %v", got, want)
	}
	if o := orphans[0]; o.Planned != 10*time.Minute || o.Overdue != 31*time.Minute {
		t.Errorf("overdue run: planned %s, overdue %s; want 10m0s and 31m0s", o.Planned, o.Overdue)
	}
	if o := orphans[1]; o.Planned != 80*time.Minute || o.Overdue != 31*time.Minute {
		t.Errorf("overdue INTEGRITY run: planned %s, overdue %s; want 1h20m0s and 31m0s", o.Planned, o.Overdue)
	}
	if o := orphans[2]; o.Planned != 2*time.Hour || o.Overdue != 31*time.Minute {
		t.Errorf("overdue scenario: planned %s, overdue %s; want 2h0m0s and 31m0s", o.Planned, o.Overdue)
	}
	if o := orphans[4]; o.Overdue != 3*time.Hour {
		t.Errorf("a createdAt without a zone is UTC: overdue %s, want 3h0m0s", o.Overdue)
	}
	var skipped []string
	for _, r := range unreadable {
		skipped = append(skipped, r.ID)
	}
	if want := []string{"garbled", "empty"}; !reflect.DeepEqual(skipped, want) {
		t.Errorf("unreadable = %v, want %v", skipped, want)
	}
}

func TestShortDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                               "-",
		45 * time.Second:                "45s",
		40*time.Minute + 20*time.Second: "40m",
		2*time.Hour + 5*time.Minute:     "2h05m",
		30 * time.Minute:                "30m",
	} {
		if got := shortDuration(d); got != want {
			t.Errorf("shortDuration(%s) = %q, want %q", d, got, want)
		}
	}
}

// cleanupBackend serves one RUNNING run past its planned end, "stuck", and
// one still inside it, "healthy", and records the runs deleted.
type cleanupBackend struct {
	t          *testing.T
	mu         sync.Mutex
	deleted    []string
	failDelete bool
}

func newCleanupBackend(t *testing.T) *cleanupBackend {
	t.Helper()
	b := &cleanupBackend{t: t}
	ts := httptest.NewServer(http.HandlerFunc(b.serve))
	t.Cleanup(ts.Close)

	prevClient, prevOutput, prevInteractive, prevConfirm := apiClient, outputMode, interactiveAllowedFn, confirmFn
	apiClient, outputMode = client.New(ts.URL), "table"
	apiClient.MaxRetries = 1
	interactiveAllowedFn = func() bool { return false }
	confirmFn = func(string) (bool, error) {
		t.Error("asked to confirm")
		return false, nil
	}
	t.Cleanup(func() {
		apiClient, outputMode, interactiveAllowedFn, confirmFn = prevClient, prevOutput, prevInteractive, prevConfirm
		cleanupDryRun, cleanupYes, cleanupOlderThan = false, false, defaultCleanupOlderThan
	})
	return b
}

func (b *cleanupBackend) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/tests":
		if s := r.URL.Query().Get("status"); s != "RUNNING" {
			b.t.Errorf("listed status %q, want RUNNING", s)
		}
		now := time.Now().UTC()
		runs := []client.TestRun{
			{ID: "stuck", TestType: "LOAD", Status: "RUNNING", CreatedAt: now.Add(-2 * time.Hour).Format(time.RFC3339Nano),
				Spec: &client.TestSpec{DurationMs: 600_000}},
			{ID: "healthy", TestType: "ENDURANCE", Status: "RUNNING", CreatedAt: now.Add(-20 * time.Minute).Format(time.RFC3339Nano),
				Spec: &client.TestSpec{DurationMs: 3_600_000}},
		}
		_ = json.NewEncoder(w).Encode(client.PagedTests{Content: runs, Count: len(runs), TotalItems: len(runs)})
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/tests/"):
		if b.failDelete {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"status":500,"error":"Internal Server Error","message":"boom"}`))
			return
		}
		b.mu.Lock()
		b.deleted = append(b.deleted, strings.TrimPrefix(r.URL.Path, "/api/tests/"))
		b.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		b.t.Errorf("unexpected request %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusTeapot)
	}
}

func (b *cleanupBackend) deletedRuns() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.deleted...)
}

// Without a terminal, cleanup used to delete at once. It now refuses unless
// --yes says to go ahead.
func TestTestCleanup_NoTerminalNeedsYes(t *testing.T) {
	b := newCleanupBackend(t)

	stdout, stderr, err := commandOutput(t, func() error { return runCommandLine(t, "test", "cleanup") })
	if _, ok := err.(*silentErr); !ok {
		t.Fatalf("err = %v (%T), want a silentErr, which exits 1", err, err)
	}
	if d := b.deletedRuns(); len(d) != 0 {
		t.Errorf("deleted %v without --yes", d)
	}
	if !strings.Contains(stderr, "--yes") {
		t.Errorf("the refusal does not mention --yes:\n%s", stderr)
	}
	if !strings.Contains(string(stdout), "stuck") || strings.Contains(string(stdout), "healthy") {
		t.Errorf("the list should name the stuck run and only it:\n%s", stdout)
	}
}

func TestTestCleanup_YesDeletesOnlyOrphans(t *testing.T) {
	b := newCleanupBackend(t)

	commandStdout(t, func() error { return runCommandLine(t, "test", "cleanup", "--yes") })
	if d := b.deletedRuns(); !reflect.DeepEqual(d, []string{"stuck"}) {
		t.Errorf("deleted %v, want [stuck]", d)
	}
}

func TestTestCleanup_DryRunDeletesNothing(t *testing.T) {
	b := newCleanupBackend(t)

	stdout := commandStdout(t, func() error { return runCommandLine(t, "test", "cleanup", "--dry-run") })
	if d := b.deletedRuns(); len(d) != 0 {
		t.Errorf("--dry-run deleted %v", d)
	}
	if !strings.Contains(string(stdout), "stuck") {
		t.Errorf("--dry-run should list the stuck run:\n%s", stdout)
	}
}

func TestTestCleanup_AsksInATerminal(t *testing.T) {
	for _, answer := range []bool{false, true} {
		b := newCleanupBackend(t)
		interactiveAllowedFn = func() bool { return true }
		var asked string
		confirmFn = func(q string) (bool, error) { asked = q; return answer, nil }

		_, _, err := commandOutput(t, func() error { return runCommandLine(t, "test", "cleanup") })
		if asked != "Stop and delete this run and its results?" {
			t.Errorf("question = %q", asked)
		}
		deleted := b.deletedRuns()
		if answer && (err != nil || !reflect.DeepEqual(deleted, []string{"stuck"})) {
			t.Errorf("after yes: err = %v, deleted %v; want nil and [stuck]", err, deleted)
		}
		if !answer && (err == nil || len(deleted) != 0) {
			t.Errorf("after no: err = %v, deleted %v; want an error and nothing deleted", err, deleted)
		}
	}
}

// --older-than widens or narrows the window: at 5m the healthy hour-long run
// is still inside its planned hour, so only the stuck one goes.
func TestTestCleanup_OlderThan(t *testing.T) {
	b := newCleanupBackend(t)

	commandStdout(t, func() error { return runCommandLine(t, "test", "cleanup", "--older-than", "5m", "--yes") })
	if d := b.deletedRuns(); !reflect.DeepEqual(d, []string{"stuck"}) {
		t.Errorf("deleted %v, want [stuck]", d)
	}

	stdout := commandStdout(t, func() error { return runCommandLine(t, "test", "cleanup", "--older-than", "3h", "--yes") })
	if d := b.deletedRuns(); len(d) != 1 {
		t.Errorf("--older-than 3h deleted more: %v", d)
	}
	if !strings.Contains(string(stdout), "none more than 3h00m past") {
		t.Errorf("output does not say none is old enough:\n%s", stdout)
	}

	if _, _, err := commandOutput(t, func() error { return runCommandLine(t, "test", "cleanup", "--older-than", "0s") }); err == nil {
		t.Error("--older-than 0s was accepted")
	}
}

// A delete that fails used to leave the exit code at 0.
func TestTestCleanup_FailedDeleteExitsNonZero(t *testing.T) {
	b := newCleanupBackend(t)
	b.failDelete = true

	_, _, err := commandOutput(t, func() error { return runCommandLine(t, "test", "cleanup", "--yes") })
	if _, ok := err.(*silentErr); !ok {
		t.Errorf("err = %v (%T), want a silentErr, which exits 1", err, err)
	}
}
