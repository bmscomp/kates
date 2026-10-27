package cmd

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
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

// --timeout elapsing cancels the run too, and the gate fails. The flag takes
// a duration now, and still the plain seconds it used to.
func TestGate_TimeoutCancelsTheRun(t *testing.T) {
	b := newRunsBackend(t)
	b.stuck["LOAD"] = true

	_, stderr, err := commandOutput(t, func() error {
		return runCommandLine(t, "gate", "--records", "1000", "--timeout", "1ns")
	})
	if _, ok := err.(*silentErr); !ok {
		t.Fatalf("err = %v (%T), want a silentErr\n%s", err, err, stderr)
	}
	if !strings.Contains(stderr, "Timeout after 1ns") || !strings.Contains(stderr, "test still RUNNING") {
		t.Errorf("stderr does not say the gate timed out on a running test:\n%s", stderr)
	}
	if got := b.cancelledRuns(); !reflect.DeepEqual(got, []string{"run-1"}) {
		t.Errorf("cancelled %v, want run-1", got)
	}

	b.resetFlags()
	for _, tc := range []struct{ flag, want string }{
		{"300", "5m0s"}, {"2m", "2m0s"},
	} {
		if err := gateCmd.Flags().Set("timeout", tc.flag); err != nil {
			t.Fatal(err)
		}
		d, err := parseTimeoutFlag(gateTimeout)
		if err != nil || d.String() != tc.want {
			t.Errorf("--timeout %s reads as %v, %v; want %s", tc.flag, d, err, tc.want)
		}
	}
	created := len(b.createdTypes())
	for _, bad := range []string{"x", "ZZ", "A+"} {
		b.resetFlags()
		if _, _, err := commandOutput(t, func() error {
			return runCommandLine(t, "gate", "--min-grade", bad)
		}); err == nil || !strings.Contains(err.Error(), "unknown grade") {
			t.Errorf("--min-grade %s: err = %v, want the grade refused before any run starts", bad, err)
		}
		if now := b.createdTypes(); len(now) != created {
			t.Errorf("--min-grade %s started a run: %v", bad, now[created:])
		}
	}
}

// --help prints the ladder a run is graded on.
func TestGateAndBenchmarkHelpPrintTheScale(t *testing.T) {
	for _, tc := range []struct {
		cmd  *cobra.Command
		want string
	}{
		{gateCmd, "A  throughput >= 50,000 rec/s and p99 < 5 ms"},
		{benchmarkCmd, "A  score >= 90"},
	} {
		var help bytes.Buffer
		tc.cmd.SetOut(&help)
		err := tc.cmd.Help()
		tc.cmd.SetOut(nil)
		if err != nil || !strings.Contains(help.String(), tc.want) {
			t.Errorf("kates %s --help lacks %q (%v):\n%s", tc.cmd.Name(), tc.want, err, help.String())
		}
	}
}
