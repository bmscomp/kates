package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/client"
)

// procRunner is the one seam through which the version, operator and
// migration commands run helm and kubectl. It satisfies both
// strimzi.Runner (Run) and podrun.Runner (Run + RunInput), so a single value
// is handed to every package that needs a process, and a single fake replaces
// it in tests.
//
// Why not the deploy_* injection variables: those return only an error or raw
// bytes and are shaped around the deploy dashboard. The packages behind this
// seam parse stdout and want stderr in the error, which is what Run gives
// them.
type procRunner struct{}

// Run executes name with args and returns trimmed stdout. On failure the
// error carries the command's stderr, so callers can show why kubectl or
// helm refused without a second call.
func (procRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	out, err := runProc(ctx, "", name, args...)
	return strings.TrimSpace(out), err
}

// RunInput is Run with data on stdin — `kubectl apply -f -`, `kubectl exec
// -i … -- tee`, the console producer — except that it returns stdout as the
// process wrote it, which is what podrun.Runner promises. podrun.WriteFile
// compares tee's echo with what it wrote, byte for byte: trimmed, the echo of
// any file ending in a newline, every client.properties among them, came back
// one byte short, and `kates migrate verify` failed before its first record.
func (procRunner) RunInput(ctx context.Context, stdin string, name string, args ...string) (string, error) {
	return runProc(ctx, stdin, name, args...)
}

// runProcFn is the injectable implementation behind procRunner. Tests replace
// it to script helm/kubectl output without a cluster.
var runProcFn = runProcDefault

func runProc(ctx context.Context, stdin, name string, args ...string) (string, error) {
	return runProcFn(ctx, stdin, name, args...)
}

func runProcDefault(ctx context.Context, stdin, name string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		c.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		shown := args
		if len(shown) > 4 {
			shown = shown[:4]
		}
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(shown, " "), err, msg)
	}
	return stdout.String(), nil
}

// defaultRunner is the process runner every non-deploy command uses.
var defaultRunner procRunner

// Following a test run to its end.
//
// Five commands used to poll GET /api/tests/{id} with a loop of their own,
// and the loops drifted: two knew only DONE and FAILED as final statuses, one
// skipped every error without a word, two slept with time.Sleep that Ctrl-C
// could not end, and each had its own cap. followRun is the one loop; what
// differs between the commands is in followOpts.

// followMaxErrors is how many polls in a row may fail before followRun gives
// up, when followOpts does not say.
const followMaxErrors = 3

// followBackoffCap bounds the wait between polls while they fail.
const followBackoffCap = 30 * time.Second

// errFollowDeadline ends a follow whose deadline passed with the run still
// going. The run returned with it is the last one read, which may be nil.
var errFollowDeadline = errors.New("no final status before the deadline")

// followOpts is what a command asks of followRun beyond the run id.
type followOpts struct {
	// interval is the time between two polls that answered; zero means
	// pollInterval, the test family's. While polls fail it grows: see
	// nextPollDelay.
	interval time.Duration
	// deadline, when set, ends the follow with errFollowDeadline once it has
	// passed and the run is still not final.
	deadline time.Time
	// maxErrors is how many polls in a row may fail before the follow ends
	// with the last error; zero means followMaxErrors. A poll that answers
	// resets the count.
	maxErrors int
	// onPoll sees every run a poll read, final or not, before followRun
	// decides anything: a spinner, a progress line or a TUI updates from it.
	onPoll func(*client.TestRun)
	// onError sees every poll error that is not the context's. It may mend
	// the connection (watch repairs its port-forward) and say so, in which
	// case the follow polls again at once without counting the error; and it
	// may return the error to count and report in place of the one it saw.
	onError func(ctx context.Context, err error) (repaired bool, report error)
	// cancelOnStop runs when ctx ends before the run is final, with the run
	// id, and its result is the follow's error: a command that started the
	// run passes stopStartedRun, which cancels the run, says so and returns
	// errInterrupted. Without it the follow returns errStoppedWaiting, as a
	// command that watches someone else's run wants.
	cancelOnStop func(id string) error
}

// followRun polls run id until its status is terminal and returns it. It
// returns the last run read (nil when none was) and:
//   - errStoppedWaiting, or what opts.cancelOnStop returned, when ctx ended
//     first;
//   - errFollowDeadline when opts.deadline passed first;
//   - an error naming the count and the last poll error when opts.maxErrors
//     polls in a row failed.
//
// The first poll is at once; the wait between polls is opts.interval, grown
// with jitter after each failed poll and back to the interval after one that
// answered, so a command that follows a run through a flapping API neither
// hammers it nor sits on a stale wait.
func followRun(ctx context.Context, id string, opts followOpts) (*client.TestRun, error) {
	interval := opts.interval
	if interval == 0 {
		interval = pollInterval
	}
	maxErrors := opts.maxErrors
	if maxErrors <= 0 {
		maxErrors = followMaxErrors
	}
	stopped := func(last *client.TestRun) (*client.TestRun, error) {
		if opts.cancelOnStop != nil {
			return last, opts.cancelOnStop(id)
		}
		return last, errStoppedWaiting
	}

	var last *client.TestRun
	var lastErr error
	failures := 0
	for {
		if ctx.Err() != nil {
			return stopped(last)
		}
		run, err := pollGetTestFn(ctx, id)
		switch {
		case err != nil && ctx.Err() != nil:
			// A poll cut off by the stop is not a lost connection.
			return stopped(last)
		case err != nil:
			if opts.onError != nil {
				repaired, report := opts.onError(ctx, err)
				if repaired {
					failures = 0
					continue
				}
				if report != nil {
					err = report
				}
			}
			failures++
			lastErr = err
			if failures >= maxErrors {
				return last, fmt.Errorf("connection lost: could not read the run's status %d times in a row: %w", failures, lastErr)
			}
		default:
			failures, lastErr = 0, nil
			last = run
			if opts.onPoll != nil {
				opts.onPoll(run)
			}
			if terminalRunStatus(run.Status) {
				return run, nil
			}
		}
		if !opts.deadline.IsZero() && time.Now().After(opts.deadline) {
			return last, errFollowDeadline
		}
		if !sleepCtx(ctx, nextPollDelay(interval, failures, lastErr)) {
			return stopped(last)
		}
	}
}

// nextPollDelay is how long a poller waits before its next poll: base after a
// poll that answered (err nil, attempt 0), and after attempt failed polls in a
// row, base doubled for each failure up to followBackoffCap, with a quarter of
// jitter either way so that pollers restarted together do not stay in step.
// A zero base, which tests use, stays zero.
func nextPollDelay(base time.Duration, attempt int, err error) time.Duration {
	if err == nil || attempt <= 0 || base <= 0 {
		return base
	}
	d := base
	for i := 1; i < attempt && d < followBackoffCap; i++ {
		d *= 2
	}
	d = min(d, max(followBackoffCap, base))
	jitter := time.Duration(rand.Int64N(int64(d)/2+1)) - d/4
	return max(d+jitter, base)
}
