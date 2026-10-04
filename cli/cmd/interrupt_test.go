package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/bmscomp/kates/cli/client"
	tea "github.com/charmbracelet/bubbletea"
)

// runWithContext runs a command line as runCommandLine does, with ctx as the
// command's context, the way Execute hands it the one Ctrl-C cancels.
func runWithContext(t *testing.T, ctx context.Context, args ...string) error {
	t.Helper()
	cmd, _, err := rootCmd.Find(args)
	if err != nil {
		t.Fatalf("no command for %q: %v", args, err)
	}
	prev := cmd.Context()
	cmd.SetContext(ctx)
	defer cmd.SetContext(prev)
	return runCommandLine(t, args...)
}

// cancelAfterFirstPoll is a context cancelled, as Ctrl-C cancels it, once the
// backend has answered run id's first poll: the command is then waiting.
func cancelAfterFirstPoll(t *testing.T, b *runsBackend, id string) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		for b.pollCount(id) == 0 {
			if ctx.Err() != nil {
				return
			}
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()
	return ctx
}

const twoLoadScenarios = "scenarios:\n  - name: first\n    type: LOAD\n  - name: second\n    type: LOAD\n"

// Ctrl-C used to end one scenario's wait: the scenario became an ERROR row,
// its run went on, and apply started the next scenario.
func TestApply_CtrlCStopsTheApplyAndCancelsTheRun(t *testing.T) {
	b := applyBackend(t)
	b.stuck["LOAD"] = true
	file := writeScenarioFile(t, twoLoadScenarios)
	ctx := cancelAfterFirstPoll(t, b, "run-1")

	stdout, stderr, err := commandOutput(t, func() error {
		return runWithContext(t, ctx, "test", "apply", "-f", file, "--wait", "-o", "json")
	})
	if !errors.Is(err, errInterrupted) {
		t.Fatalf("err = %v, want errInterrupted, which exits 130", err)
	}
	var got applyResult
	decodeStdout(t, stdout, &got)
	want := applyResult{File: file, Waited: true, Interrupted: true, Scenarios: []applyScenarioResult{
		{Name: "first", Type: "LOAD", RunID: "run-1", Status: "CANCELLED", Error: "interrupted; the run was cancelled"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("result = %+v\nwant %+v", got, want)
	}
	if created := b.createdTypes(); len(created) != 1 {
		t.Errorf("created %d runs, want 1: the second scenario must not start", len(created))
	}
	if c := b.cancelledRuns(); !reflect.DeepEqual(c, []string{"run-1"}) {
		t.Errorf("cancelled %v, want [run-1]", c)
	}
	if !strings.Contains(stderr, "the 1 scenario(s) after it were not started") {
		t.Errorf("stderr does not say what was not started:\n%s", stderr)
	}
}

// In a terminal, Ctrl-C reaches the spinner as a key, and ends the apply the
// same way.
func TestApply_CtrlCInTheSpinner(t *testing.T) {
	b := applyBackend(t)
	file := writeScenarioFile(t, twoLoadScenarios)
	interactiveAllowedFn = func() bool { return true }
	origTUI := waitForTestTUI
	t.Cleanup(func() { waitForTestTUI = origTUI })
	waitForTestTUI = func(context.Context, string, string) (*client.TestRun, error) {
		return nil, errStoppedWaiting
	}

	stdout, stderr, err := commandOutput(t, func() error {
		return runCommandLine(t, "test", "apply", "-f", file, "--wait")
	})
	if !errors.Is(err, errInterrupted) {
		t.Fatalf("err = %v, want errInterrupted", err)
	}
	if created := b.createdTypes(); len(created) != 1 {
		t.Errorf("created %d runs, want 1", len(created))
	}
	if c := b.cancelledRuns(); !reflect.DeepEqual(c, []string{"run-1"}) {
		t.Errorf("cancelled %v, want [run-1]", c)
	}
	if !tableHasRow(stripAnsi(string(stdout)), []string{"first", "CANCELLED"}) {
		t.Errorf("summary lacks the cancelled scenario:\n%s", stdout)
	}
	if !strings.Contains(stderr, "not started") {
		t.Errorf("stderr lacks the interruption:\n%s", stderr)
	}
}

// A cancel that fails leaves the run's fate to the user, with the command
// that ends it.
func TestApply_CancelRefused(t *testing.T) {
	b := applyBackend(t)
	b.stuck["LOAD"] = true
	b.refuseCancel = true
	file := writeScenarioFile(t, twoLoadScenarios)
	ctx := cancelAfterFirstPoll(t, b, "run-1")

	stdout, _, err := commandOutput(t, func() error {
		return runWithContext(t, ctx, "test", "apply", "-f", file, "--wait", "-o", "json")
	})
	if !errors.Is(err, errInterrupted) {
		t.Fatalf("err = %v, want errInterrupted", err)
	}
	var got applyResult
	decodeStdout(t, stdout, &got)
	if len(got.Scenarios) != 1 || got.Scenarios[0].Status != "INTERRUPTED" ||
		!strings.Contains(got.Scenarios[0].Error, "kates test cancel run-1") {
		t.Errorf("scenarios = %+v, want one INTERRUPTED naming kates test cancel run-1", got.Scenarios)
	}
}

func TestWaitModel_CtrlCAndQStopTheWait(t *testing.T) {
	for _, key := range []tea.KeyMsg{{Type: tea.KeyCtrlC}, {Type: tea.KeyRunes, Runes: []rune("q")}} {
		m, _ := waitModel{ctx: context.Background()}.Update(key)
		if err := m.(waitModel).err; !errors.Is(err, errStoppedWaiting) {
			t.Errorf("after %q the wait's error is %v, want errStoppedWaiting", key.String(), err)
		}
	}
}

// create --wait started the run, so stopping it cancels the run. On Ctrl-C it
// used to exit 0 with the test still running.
func TestCreateWait_CtrlCCancelsTheRun(t *testing.T) {
	b := newRunsBackend(t)
	b.stuck["LOAD"] = true
	t.Cleanup(func() { createWait, createType = false, "LOAD" })
	ctx := cancelAfterFirstPoll(t, b, "run-1")

	_, stderr, err := commandOutput(t, func() error {
		return runWithContext(t, ctx, "test", "create", "--type", "LOAD", "--wait")
	})
	if !errors.Is(err, errInterrupted) {
		t.Fatalf("err = %v, want errInterrupted", err)
	}
	if c := b.cancelledRuns(); !reflect.DeepEqual(c, []string{"run-1"}) {
		t.Errorf("cancelled %v, want [run-1]", c)
	}
	if !strings.Contains(stderr, "Stopped waiting for test run-1: the run was cancelled") {
		t.Errorf("stderr does not say the run was cancelled:\n%s", stderr)
	}
}

func TestPollUntilDonePlain_StopsWithItsContext(t *testing.T) {
	origGet, origInterval := pollGetTestFn, pollInterval
	t.Cleanup(func() { pollGetTestFn, pollInterval = origGet, origInterval })
	ctx, cancel := context.WithCancel(context.Background())
	pollGetTestFn = func(context.Context, string) (*client.TestRun, error) {
		cancel()
		return &client.TestRun{ID: "test-1234", Status: "RUNNING"}, nil
	}
	pollInterval = time.Hour

	status, err := pollUntilDonePlain(ctx, "test-1234", io.Discard)
	if !errors.Is(err, errStoppedWaiting) || status != "RUNNING" {
		t.Errorf("= (%q, %v), want (RUNNING, errStoppedWaiting)", status, err)
	}
}

func TestTestCancel(t *testing.T) {
	b := newRunsBackend(t)

	stdout := commandStdout(t, func() error { return runCommandLine(t, "test", "cancel", "run-7", "run-8") })
	if c := b.cancelledRuns(); !reflect.DeepEqual(c, []string{"run-7", "run-8"}) {
		t.Errorf("cancelled %v, want [run-7 run-8]", c)
	}
	if !strings.Contains(string(stdout), "Cancelled: run-7") {
		t.Errorf("output lacks the cancelled run:\n%s", stdout)
	}

	b.refuseCancel = true
	stdout, _, err := commandOutput(t, func() error { return runCommandLine(t, "test", "cancel", "run-9", "-o", "json") })
	if _, ok := err.(*silentErr); !ok {
		t.Errorf("err = %v (%T), want a silentErr, which exits 1", err, err)
	}
	var got []testCancelResult
	decodeStdout(t, stdout, &got)
	if len(got) != 1 || got[0].ID != "run-9" || got[0].Cancelled || !strings.Contains(got[0].Error, "not running") {
		t.Errorf("result = %+v, want run-9 not cancelled because it is not running", got)
	}
}

func TestInterruptContext_OnlyForInterruptibleCommands(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want bool
	}{
		{[]string{"test", "apply", "-f", "x.yaml", "--wait"}, true},
		{[]string{"--url", "http://127.0.0.1:1", "test", "create", "--wait"}, true},
		{[]string{"replay", "abc", "--wait"}, true},
		{[]string{"disruption", "run", "--config", "p.json"}, true},
		{[]string{"disruption", "playbook", "run", "broker-kill"}, true},
		{[]string{"test", "list"}, false},
		{[]string{"test", "watch", "abc"}, false},
		{[]string{"no-such-command"}, false},
	} {
		ctx, stop := interruptContext(tt.args)
		if got := ctx.Done() != nil; got != tt.want {
			t.Errorf("kates %s: Ctrl-C handled by the command = %v, want %v", strings.Join(tt.args, " "), got, tt.want)
		}
		stop()
	}
}

// disruptionBackend accepts one plan, as d-7, and reports it RUNNING to every
// poll.
func disruptionBackend(t *testing.T) (polled func() int) {
	t.Helper()
	var mu sync.Mutex
	polls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/disruptions":
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"id":"d-7","status":"RUNNING"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/disruptions/d-7":
			mu.Lock()
			polls++
			mu.Unlock()
			_, _ = io.WriteString(w, `{"id":"d-7","planName":"broker-kill","status":"RUNNING"}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	t.Cleanup(ts.Close)
	prevClient, prevOutput := apiClient, outputMode
	apiClient, outputMode = client.New(ts.URL), "table"
	t.Cleanup(func() {
		apiClient, outputMode = prevClient, prevOutput
		disruptionFile, dryRunMode = "", false
	})
	return func() int {
		mu.Lock()
		defer mu.Unlock()
		return polls
	}
}

// The API cannot cancel a running plan, so the command names it before it
// waits: the id used to come only with the report, and a run interrupted
// while waiting left chaos going under an id nobody had seen.
func TestDisruptionRun_CtrlCNamesThePlan(t *testing.T) {
	polled := disruptionBackend(t)
	plan := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(plan, []byte(`{"planName":"broker-kill","steps":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		for polled() == 0 && ctx.Err() == nil {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()

	stdout, stderr, err := commandOutput(t, func() error {
		return runWithContext(t, ctx, "disruption", "run", "--config", plan)
	})
	if !errors.Is(err, errInterrupted) {
		t.Fatalf("err = %v, want errInterrupted", err)
	}
	if !strings.Contains(stripAnsi(string(stdout)), "Running disruption plan d-7") {
		t.Errorf("the plan was not named when it started:\n%s", stdout)
	}
	if !strings.Contains(stderr, "kates disruption status d-7") {
		t.Errorf("stderr does not say how to follow the plan:\n%s", stderr)
	}
}

// Real signals: these run the test binary again as kates, since a SIGINT
// sent to this process would end the test run.

// TestHelperInterruptProcess is kates itself when startKates runs this binary.
func TestHelperInterruptProcess(t *testing.T) {
	if os.Getenv("KATES_TEST_INTERRUPT_PROCESS") != "1" {
		t.Skip("runs only as startKates' subprocess")
	}
	// TestMain made this process a home it cannot remove, since Execute
	// exits without returning; use the test's.
	_ = os.RemoveAll(os.Getenv("HOME"))
	os.Setenv("HOME", os.Getenv("KATES_TEST_HOME"))
	os.Args = append([]string{"kates"}, strings.Split(os.Getenv("KATES_TEST_ARGS"), "\x1f")...)
	Execute()
	os.Exit(0)
}

// startKates starts kates with args and returns it, running, with its output
// collected in out.
func startKates(t *testing.T, out *bytes.Buffer, args ...string) *exec.Cmd {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("sends POSIX signals")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperInterruptProcess$")
	cmd.Env = append(os.Environ(),
		"KATES_TEST_INTERRUPT_PROCESS=1",
		"KATES_TEST_HOME="+t.TempDir(),
		"KATES_TEST_ARGS="+strings.Join(args, "\x1f"),
	)
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return cmd
}

// waitExit waits for cmd to end and returns how it ended.
func waitExit(t *testing.T, cmd *exec.Cmd, out *bytes.Buffer) syscall.WaitStatus {
	t.Helper()
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatalf("kates was still running 20s after the signal\n%s", out)
	}
	return cmd.ProcessState.Sys().(syscall.WaitStatus)
}

// describeExit says how a process ended, for a failure message.
func describeExit(ws syscall.WaitStatus) string {
	if ws.Signaled() {
		return "killed by " + ws.Signal().String()
	}
	return fmt.Sprintf("exit status %d", ws.ExitStatus())
}

// signalBackend is a Kates API whose runs never finish. It counts what kates
// asked of it; a cancel waits for release when blockCancel is set.
type signalBackend struct {
	url         string
	blockCancel bool
	release     chan struct{}

	mu        sync.Mutex
	created   int
	polls     int
	cancelled []string
}

func newSignalBackend(t *testing.T) *signalBackend {
	t.Helper()
	b := &signalBackend{release: make(chan struct{})}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		b.mu.Lock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/tests":
			b.created++
			fmt.Fprintf(w, `{"id":"run-%d","testType":"LOAD","status":"PENDING"}`, b.created)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cancel"):
			b.cancelled = append(b.cancelled, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/tests/"), "/cancel"))
			if b.blockCancel {
				b.mu.Unlock()
				<-b.release
				return
			}
			_, _ = io.WriteString(w, `{"status":"FAILED","reason":"cancelled"}`)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/tests/"):
			b.polls++
			id := strings.TrimPrefix(r.URL.Path, "/api/tests/")
			fmt.Fprintf(w, `{"id":%q,"testType":"LOAD","status":"RUNNING"}`, id)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"status":404,"error":"Not Found","message":"no such endpoint"}`)
		}
		b.mu.Unlock()
	}))
	t.Cleanup(func() {
		close(b.release)
		ts.Close()
	})
	b.url = ts.URL
	return b
}

func (b *signalBackend) waitFor(t *testing.T, what string, cond func(*signalBackend) bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		b.mu.Lock()
		ok := cond(b)
		b.mu.Unlock()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("kates never %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSIGINT_StopsTheApplyAndCancelsItsRun(t *testing.T) {
	b := newSignalBackend(t)
	file := writeScenarioFile(t, twoLoadScenarios)
	var out bytes.Buffer
	kates := startKates(t, &out, "--url", b.url, "test", "apply", "-f", file, "--wait")
	b.waitFor(t, "polled its first run", func(b *signalBackend) bool { return b.polls > 0 })

	if err := kates.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	ws := waitExit(t, kates, &out)

	if ws.Signaled() || ws.ExitStatus() != interruptedExitCode {
		t.Errorf("kates ended with %s, want exit status 130\n%s", describeExit(ws), &out)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !reflect.DeepEqual(b.cancelled, []string{"run-1"}) || b.created != 1 {
		t.Errorf("created %d run(s) and cancelled %v, want 1 and [run-1]\n%s", b.created, b.cancelled, &out)
	}
}

// The first signal gives back Go's default, so a second Ctrl-C ends kates
// even while it is still cancelling what it started.
func TestSIGINT_SecondOneEndsKatesAtOnce(t *testing.T) {
	b := newSignalBackend(t)
	b.blockCancel = true
	file := writeScenarioFile(t, twoLoadScenarios)
	var out bytes.Buffer
	kates := startKates(t, &out, "--url", b.url, "test", "apply", "-f", file, "--wait")
	b.waitFor(t, "polled its first run", func(b *signalBackend) bool { return b.polls > 0 })

	if err := kates.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	b.waitFor(t, "asked to cancel its run", func(b *signalBackend) bool { return len(b.cancelled) > 0 })
	time.Sleep(100 * time.Millisecond)
	if err := kates.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if ws := waitExit(t, kates, &out); !ws.Signaled() || ws.Signal() != syscall.SIGINT {
		t.Errorf("kates ended with %s, want killed by the second SIGINT\n%s", describeExit(ws), &out)
	}
}

// A command that is not marked interruptible keeps Go's default: the first
// Ctrl-C ends it. A handler for every command would have left the ones that
// never look at their context deaf to it.
func TestSIGINT_EndsOtherCommandsAtOnce(t *testing.T) {
	b := newSignalBackend(t)
	var out bytes.Buffer
	kates := startKates(t, &out, "--url", b.url, "test", "watch", "run-1")
	b.waitFor(t, "polled the run", func(b *signalBackend) bool { return b.polls > 0 })

	if err := kates.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if ws := waitExit(t, kates, &out); !ws.Signaled() || ws.Signal() != syscall.SIGINT {
		t.Errorf("kates ended with %s, want killed by SIGINT\n%s", describeExit(ws), &out)
	}
}
