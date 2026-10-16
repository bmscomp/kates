package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bmscomp/kates/cli/client"
)

// stubPoll scripts a sequence of GetTest responses and restores the seams on
// cleanup. The zero poll interval keeps these tests instant.
func stubPoll(t *testing.T, seq []func() (*client.TestRun, error)) {
	t.Helper()
	origGet, origRepair, origInterval := pollGetTestFn, repairPollingConnectionFn, pollInterval
	i := 0
	pollGetTestFn = func(context.Context, string) (*client.TestRun, error) {
		f := seq[i]
		if i < len(seq)-1 {
			i++ // hold on the last response so retries see a stable state
		}
		return f()
	}
	repairPollingConnectionFn = func(context.Context, error) (bool, error) { return false, nil }
	pollInterval = 0
	t.Cleanup(func() { pollGetTestFn, repairPollingConnectionFn, pollInterval = origGet, origRepair, origInterval })
}

func run(status string, sent float64) func() (*client.TestRun, error) {
	return func() (*client.TestRun, error) {
		return &client.TestRun{
			ID:     "test-1234",
			Status: status,
			Results: []client.PhaseResult{
				{RecordsSent: sent, ThroughputRecordsPerSec: 1000},
			},
		}, nil
	}
}

// The flagship contract: a FAILED test is reported as FAILED, never swallowed.
// The previous pollUntilDone returned nothing at all — the TUI printed "Test
// failed" and callers exited 0, keeping CI green on failed load tests.
func TestPollPlain_FailedStatusIsReturned(t *testing.T) {
	stubPoll(t, []func() (*client.TestRun, error){
		run("RUNNING", 10),
		run("FAILED", 10),
	})

	var out bytes.Buffer
	status, err := pollUntilDonePlain(context.Background(), "test-1234", &out)
	if err != nil {
		t.Fatalf("pollUntilDonePlain: %v", err)
	}
	if status != "FAILED" {
		t.Fatalf("status = %q, want FAILED", status)
	}
	if !isFailedStatus(status) {
		t.Error("isFailedStatus(FAILED) must be true — this is what flips the exit code")
	}
}

func TestPollPlain_CompletedStatus(t *testing.T) {
	stubPoll(t, []func() (*client.TestRun, error){
		run("RUNNING", 50),
		run("COMPLETED", 100),
	})

	var out bytes.Buffer
	status, err := pollUntilDonePlain(context.Background(), "test-1234", &out)
	if err != nil || status != "COMPLETED" {
		t.Fatalf("got (%q, %v), want (COMPLETED, nil)", status, err)
	}
	if isFailedStatus(status) {
		t.Error("COMPLETED must not read as failure")
	}
}

// One append-only line per status CHANGE — not per poll — and no escape codes.
// This output is a documented contract for CI logs.
func TestPollPlain_OneLinePerStatusChange(t *testing.T) {
	stubPoll(t, []func() (*client.TestRun, error){
		run("PENDING", 0),
		run("RUNNING", 10),
		run("RUNNING", 20),
		run("RUNNING", 30),
		run("COMPLETED", 100),
	})

	var out bytes.Buffer
	if _, err := pollUntilDonePlain(context.Background(), "test-1234", &out); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 { // PENDING, RUNNING, COMPLETED — the repeats collapse
		t.Errorf("want 3 lines (one per status change), got %d:\n%s", len(lines), out.String())
	}
	if strings.Contains(out.String(), "\x1b") {
		t.Error("plain poll output must not contain escape codes")
	}
	for _, want := range []string{"status=PENDING", "status=RUNNING", "status=COMPLETED"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
}

// Losing the connection means the outcome is UNKNOWN. Unknown is not success —
// reporting it as an error is what stops CI from going green blind.
func TestPollPlain_ConnectionLostIsAnError(t *testing.T) {
	stubPoll(t, []func() (*client.TestRun, error){
		func() (*client.TestRun, error) { return nil, errors.New("connection refused") },
	})

	var out bytes.Buffer
	start := time.Now()
	_, err := pollUntilDonePlain(context.Background(), "test-1234", &out)
	if err == nil {
		t.Fatal("a lost connection must surface as an error, not silence")
	}
	if !strings.Contains(err.Error(), "connection lost") {
		t.Errorf("error should say what happened, got: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("retries must respect the (zeroed) poll interval seam")
	}
}

// Transient errors within the retry budget do not kill the follow.
func TestPollPlain_TransientErrorRecovers(t *testing.T) {
	stubPoll(t, []func() (*client.TestRun, error){
		func() (*client.TestRun, error) { return nil, errors.New("blip") },
		run("COMPLETED", 100),
	})

	var out bytes.Buffer
	status, err := pollUntilDonePlain(context.Background(), "test-1234", &out)
	if err != nil || status != "COMPLETED" {
		t.Fatalf("got (%q, %v), want recovery to (COMPLETED, nil)", status, err)
	}
}

func TestPollPlain_RepairsLocalPortForward(t *testing.T) {
	origGet, origRepair, origInterval := pollGetTestFn, repairPollingConnectionFn, pollInterval
	t.Cleanup(func() { pollGetTestFn, repairPollingConnectionFn, pollInterval = origGet, origRepair, origInterval })

	calls := 0
	pollGetTestFn = func(context.Context, string) (*client.TestRun, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("connection refused")
		}
		return &client.TestRun{ID: "test-1234", Status: "COMPLETED"}, nil
	}
	repairs := 0
	repairPollingConnectionFn = func(context.Context, error) (bool, error) {
		repairs++
		return true, nil
	}
	pollInterval = 0

	var out bytes.Buffer
	status, err := pollUntilDonePlain(context.Background(), "test-1234", &out)
	if err != nil || status != "COMPLETED" {
		t.Fatalf("got (%q, %v), want repaired completion", status, err)
	}
	if calls != 2 {
		t.Errorf("poll calls = %d, want 2", calls)
	}
	if repairs != 1 {
		t.Errorf("repair attempts = %d, want 1", repairs)
	}
}

func TestPollPlain_ReattemptsRepairAfterInitialNoop(t *testing.T) {
	origGet, origRepair, origInterval := pollGetTestFn, repairPollingConnectionFn, pollInterval
	t.Cleanup(func() { pollGetTestFn, repairPollingConnectionFn, pollInterval = origGet, origRepair, origInterval })

	calls := 0
	pollGetTestFn = func(context.Context, string) (*client.TestRun, error) {
		calls++
		if calls <= 2 {
			return nil, errors.New("connection refused")
		}
		return &client.TestRun{ID: "test-1234", Status: "COMPLETED"}, nil
	}
	repairs := 0
	repairPollingConnectionFn = func(context.Context, error) (bool, error) {
		repairs++
		return repairs == 2, nil
	}
	pollInterval = 0

	var out bytes.Buffer
	status, err := pollUntilDonePlain(context.Background(), "test-1234", &out)
	if err != nil || status != "COMPLETED" {
		t.Fatalf("got (%q, %v), want repaired completion", status, err)
	}
	if calls != 3 {
		t.Errorf("poll calls = %d, want 3", calls)
	}
	if repairs != 2 {
		t.Errorf("repair attempts = %d, want 2", repairs)
	}
}

func TestPollPlain_ReportsRepairFailure(t *testing.T) {
	origGet, origRepair, origInterval := pollGetTestFn, repairPollingConnectionFn, pollInterval
	t.Cleanup(func() { pollGetTestFn, repairPollingConnectionFn, pollInterval = origGet, origRepair, origInterval })

	pollGetTestFn = func(context.Context, string) (*client.TestRun, error) {
		return nil, errors.New("connection refused")
	}
	repairPollingConnectionFn = func(context.Context, error) (bool, error) {
		return false, errors.New("kubectl unreachable")
	}
	pollInterval = 0

	var out bytes.Buffer
	_, err := pollUntilDonePlain(context.Background(), "test-1234", &out)
	if err == nil {
		t.Fatal("want connection loss after failed repair")
	}
	for _, want := range []string{"connection lost", "port-forward repair failed", "kubectl unreachable"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}
