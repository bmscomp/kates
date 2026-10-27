package cmd

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmscomp/kates/cli/client"
)

// scriptPolls makes pollGetTestFn answer each poll from seq in turn and hold
// on the last answer, with no wait between polls.
func scriptPolls(t *testing.T, seq ...func() (*client.TestRun, error)) (polled func() int) {
	t.Helper()
	origGet, origInterval := pollGetTestFn, pollInterval
	t.Cleanup(func() { pollGetTestFn, pollInterval = origGet, origInterval })
	var mu sync.Mutex
	n := 0
	pollGetTestFn = func(context.Context, string) (*client.TestRun, error) {
		mu.Lock()
		i := min(n, len(seq)-1)
		n++
		mu.Unlock()
		return seq[i]()
	}
	pollInterval = 0
	return func() int {
		mu.Lock()
		defer mu.Unlock()
		return n
	}
}

func answer(status string) func() (*client.TestRun, error) {
	return func() (*client.TestRun, error) { return &client.TestRun{ID: "run-1", Status: status}, nil }
}

func refuse(msg string) func() (*client.TestRun, error) {
	return func() (*client.TestRun, error) { return nil, errors.New(msg) }
}

// The run is returned once its status is terminal, whichever of the four
// terminal statuses it is, and every poll before that reaches onPoll.
func TestFollowRun_SuccessAfterPolls(t *testing.T) {
	for _, final := range []string{"DONE", "COMPLETED", "FAILED", "error"} {
		polled := scriptPolls(t, answer("PENDING"), answer("RUNNING"), answer("RUNNING"), answer(final))
		var seen []string
		run, err := followRun(context.Background(), "run-1", followOpts{
			onPoll: func(r *client.TestRun) { seen = append(seen, r.Status) },
		})
		if err != nil || run == nil || run.Status != final {
			t.Fatalf("%s: = %+v, %v; want the %s run", final, run, err, final)
		}
		if polled() != 4 || strings.Join(seen, ",") != "PENDING,RUNNING,RUNNING,"+final {
			t.Errorf("%s: polled %d times and saw %v, want 4 polls all seen", final, polled(), seen)
		}
	}
}

// A poll that fails is retried; maxErrors failures in a row end the follow
// with the count and the last error, and a poll that answers resets the
// count.
func TestFollowRun_ConsecutiveErrors(t *testing.T) {
	polled := scriptPolls(t, refuse("first"), refuse("second"), refuse("third"), answer("DONE"))
	run, err := followRun(context.Background(), "run-1", followOpts{})
	if run != nil || err == nil {
		t.Fatalf("= %+v, %v; want an error", run, err)
	}
	for _, want := range []string{"connection lost", "3 times in a row", "third"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err %q lacks %q", err, want)
		}
	}
	if polled() != followMaxErrors {
		t.Errorf("polled %d times, want %d", polled(), followMaxErrors)
	}

	// Two failures, an answer, two failures: never three in a row.
	polled = scriptPolls(t, refuse("a"), refuse("b"), answer("RUNNING"), refuse("c"), refuse("d"), answer("DONE"))
	run, err = followRun(context.Background(), "run-1", followOpts{})
	if err != nil || run.Status != "DONE" || polled() != 6 {
		t.Errorf("= %+v, %v after %d polls; want DONE after 6", run, err, polled())
	}

	// onError may mend the connection, in which case the failure is not
	// counted, or replace the error that is.
	repairs := 0
	polled = scriptPolls(t, refuse("x"), refuse("x"), refuse("x"), refuse("x"), answer("DONE"))
	run, err = followRun(context.Background(), "run-1", followOpts{
		maxErrors: 2,
		onError: func(_ context.Context, err error) (bool, error) {
			repairs++
			if repairs <= 2 {
				return true, nil
			}
			return false, errors.New("wrapped " + err.Error())
		},
	})
	if run != nil || err == nil || !strings.Contains(err.Error(), "2 times in a row: wrapped x") || polled() != 4 {
		t.Errorf("= %+v, %v after %d polls; want the wrapped error after 2 uncounted repairs and 2 failures", run, err, polled())
	}
}

// The deadline ends a follow whose run is still going, with the last run
// read, and does not end one whose run finished on the same poll.
func TestFollowRun_Deadline(t *testing.T) {
	scriptPolls(t, answer("RUNNING"))
	run, err := followRun(context.Background(), "run-1", followOpts{deadline: time.Now().Add(time.Nanosecond)})
	if !errors.Is(err, errFollowDeadline) || run == nil || run.Status != "RUNNING" {
		t.Errorf("= %+v, %v; want errFollowDeadline with the RUNNING run", run, err)
	}

	scriptPolls(t, answer("DONE"))
	run, err = followRun(context.Background(), "run-1", followOpts{deadline: time.Now().Add(time.Nanosecond)})
	if err != nil || run.Status != "DONE" {
		t.Errorf("= %+v, %v; want the DONE run: it ended before the deadline was checked", run, err)
	}

	// A deadline reached while polls fail reports the failures only once
	// they reach maxErrors; before that it is the deadline.
	scriptPolls(t, refuse("down"))
	run, err = followRun(context.Background(), "run-1", followOpts{deadline: time.Now().Add(time.Nanosecond)})
	if !errors.Is(err, errFollowDeadline) || run != nil {
		t.Errorf("= %+v, %v; want errFollowDeadline with no run", run, err)
	}
}

// A cancelled context ends the follow, with the cancelOnStop hook's result
// when there is one and errStoppedWaiting otherwise; a context cancelled
// before the first poll polls nothing.
func TestFollowRun_CancellationRunsTheHook(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	polled := scriptPolls(t, func() (*client.TestRun, error) {
		cancel()
		return &client.TestRun{ID: "run-1", Status: "RUNNING"}, nil
	})
	hooked := ""
	run, err := followRun(ctx, "run-1", followOpts{
		interval:     time.Hour,
		cancelOnStop: func(id string) error { hooked = id; return errInterrupted },
	})
	if !errors.Is(err, errInterrupted) || hooked != "run-1" || run == nil || run.Status != "RUNNING" {
		t.Errorf("= %+v, %v with hook(%q); want errInterrupted from the hook and the last run", run, err, hooked)
	}
	if polled() != 1 {
		t.Errorf("polled %d times after the cancel, want 1", polled())
	}

	polled = scriptPolls(t, answer("RUNNING"))
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	run, err = followRun(ctx, "run-1", followOpts{})
	if !errors.Is(err, errStoppedWaiting) || run != nil || polled() != 0 {
		t.Errorf("= %+v, %v after %d polls; want errStoppedWaiting with no poll", run, err, polled())
	}

	// A poll cut off by the cancel is the stop, not a lost connection.
	ctx, cancel = context.WithCancel(context.Background())
	scriptPolls(t, func() (*client.TestRun, error) { cancel(); return nil, context.Canceled })
	if _, err := followRun(ctx, "run-1", followOpts{}); !errors.Is(err, errStoppedWaiting) {
		t.Errorf("err = %v, want errStoppedWaiting", err)
	}
}

// The gate, benchmark and flow keep their own poll interval seam, and the
// cancel hook the commands pass is the one that cancels the run.
func TestFollowRun_StopStartedRunCancelsTheRun(t *testing.T) {
	b := newRunsBackend(t)
	b.stuck["LOAD"] = true
	run, err := apiClient.CreateTest(context.Background(), &client.CreateTestRequest{TestType: "LOAD"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := cancelAfterFirstPoll(t, b, run.ID)
	_, _, ferr := commandOutput(t, func() error {
		_, err := followRun(ctx, run.ID, followOpts{interval: testPollInterval, cancelOnStop: stopStartedRun})
		return err
	})
	if !errors.Is(ferr, errInterrupted) {
		t.Errorf("err = %v, want errInterrupted", ferr)
	}
	if c := b.cancelledRuns(); len(c) != 1 || c[0] != run.ID {
		t.Errorf("cancelled %v, want [%s]", c, run.ID)
	}
}

func TestNextPollDelay(t *testing.T) {
	base := 2 * time.Second
	if d := nextPollDelay(base, 0, nil); d != base {
		t.Errorf("after a poll that answered: %v, want %v", d, base)
	}
	if d := nextPollDelay(0, 3, errors.New("x")); d != 0 {
		t.Errorf("a zero base grows: %v", d)
	}
	err := errors.New("down")
	// Each failure doubles the wait, within a quarter of jitter, never below
	// the base and never above the cap.
	for attempt, want := range map[int]time.Duration{1: base, 2: 2 * base, 3: 4 * base, 4: 8 * base} {
		for range 50 {
			d := nextPollDelay(base, attempt, err)
			lo, hi := want-want/4, want+want/4
			if d < max(lo, base) || d > hi {
				t.Errorf("attempt %d: %v outside [%v, %v]", attempt, d, lo, hi)
			}
		}
	}
	for range 50 {
		if d := nextPollDelay(base, 20, err); d > followBackoffCap+followBackoffCap/4 || d < followBackoffCap-followBackoffCap/4 {
			t.Errorf("attempt 20: %v, want about the cap %v", d, followBackoffCap)
		}
	}
	// Two pollers do not stay in step.
	same := 0
	for range 20 {
		a, b := nextPollDelay(base, 3, err), nextPollDelay(base, 3, err)
		if a == b {
			same++
		}
	}
	if same == 20 {
		t.Error("no jitter: 20 pairs of delays were equal")
	}
}
