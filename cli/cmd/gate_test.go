package cmd

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// A gate killed while it waits (Ctrl-C, a CI timeout) used to leave its run
// going. Its context now ends the wait, and the run with it.
func TestGate_CancelledContextStopsTheRun(t *testing.T) {
	b := newRunsBackend(t)
	b.stuck["LOAD"] = true

	ctx, cancel := context.WithCancel(context.Background())
	gateCmd.SetContext(ctx)
	t.Cleanup(func() { gateCmd.SetContext(context.Background()) })
	time.AfterFunc(30*time.Millisecond, cancel)

	_, stderr, err := commandOutput(t, func() error { return runCommandLine(t, "gate", "--records", "1000") })
	if !errors.Is(err, errInterrupted) {
		t.Fatalf("err = %v, want errInterrupted\n%s", err, stderr)
	}
	if got := b.cancelledRuns(); !reflect.DeepEqual(got, []string{"run-1"}) {
		t.Errorf("cancelled %v, want run-1", got)
	}
	polls := b.pollCount("run-1")
	time.Sleep(20 * time.Millisecond)
	if again := b.pollCount("run-1"); again != polls {
		t.Errorf("still polling after the cancel: %d then %d polls", polls, again)
	}
	if stderr == "" {
		t.Errorf("nothing on stderr: the gate must say it stopped the run")
	}
}

// --timeout elapsing cancels the run too, and the gate fails.
func TestGate_TimeoutCancelsTheRun(t *testing.T) {
	b := newRunsBackend(t)
	b.stuck["LOAD"] = true

	_, stderr, err := commandOutput(t, func() error {
		return runCommandLine(t, "gate", "--records", "1000", "--timeout", "0")
	})
	if _, ok := err.(*silentErr); !ok {
		t.Fatalf("err = %v (%T), want a silentErr\n%s", err, err, stderr)
	}
	if got := b.cancelledRuns(); !reflect.DeepEqual(got, []string{"run-1"}) {
		t.Errorf("cancelled %v, want run-1", got)
	}
}
