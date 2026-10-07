package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmscomp/kates/cli/client"
	"github.com/spf13/pflag"
)

// pruneBackend is DELETE /api/tests over a number of matching runs: a dry run
// counts them, a delete takes up to limit of them, the oldest first, as the
// Kates API does.
type pruneBackend struct {
	t *testing.T

	mu       sync.Mutex
	matching int64
	calls    []url.Values // the query of each call, in order
	// stuck deletes none of them, as when the API cannot delete the runs it
	// picks.
	stuck bool
	// refuse answers every call with this status, as an older API or one
	// that refuses the key does.
	refuse int
}

func newPruneBackend(t *testing.T, matching int64) *pruneBackend {
	t.Helper()
	b := &pruneBackend{t: t, matching: matching}
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
		resetPruneFlags()
	})
	return b
}

// resetPruneFlags puts kates test prune's flags back to their defaults, as a
// new process starts them. A string slice flag, once set, appends what a later
// command line sets to it, and keeps that in its value, so --status gets a new
// value rather than its default back.
func resetPruneFlags() {
	pruneOlderThan, pruneYes, pruneDryRun = "", false, false
	fresh := pflag.NewFlagSet("prune", pflag.ContinueOnError)
	fresh.StringSliceVar(&pruneStatuses, "status", defaultPruneStatuses, "")
	status := testPruneCmd.Flags().Lookup("status")
	status.Value = fresh.Lookup("status").Value
	testPruneCmd.Flags().VisitAll(func(f *pflag.Flag) { f.Changed = false })
}

func (b *pruneBackend) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodDelete || r.URL.Path != "/api/tests" {
		b.t.Errorf("unexpected request %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusTeapot)
		return
	}
	q := r.URL.Query()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, q)
	if b.refuse != 0 {
		w.WriteHeader(b.refuse)
		return
	}
	limit, err := strconv.ParseInt(q.Get("limit"), 10, 64)
	if err != nil {
		b.t.Errorf("limit = %q", q.Get("limit"))
	}
	statuses := q["status"]
	if len(statuses) == 0 {
		statuses = []string{"DONE", "FAILED"}
	}
	dryRun := q.Get("dryRun") == "true"
	matched := b.matching
	var deleted int64
	if !dryRun && !b.stuck {
		deleted = min(limit, b.matching)
		b.matching -= deleted
	}
	_ = json.NewEncoder(w).Encode(client.PruneResult{
		CreatedBefore: q.Get("createdBefore"), Statuses: statuses, DryRun: dryRun,
		Matched: matched, Deleted: deleted, Remaining: b.matching,
	})
}

// requests returns the query of each call, and the runs still matching.
func (b *pruneBackend) requests() ([]url.Values, int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]url.Values(nil), b.calls...), b.matching
}

// dryRuns says, call by call, which were dry runs.
func dryRuns(calls []url.Values) []bool {
	var out []bool
	for _, q := range calls {
		out = append(out, q.Get("dryRun") == "true")
	}
	return out
}

// The count comes first, with the cutoff --older-than says, and --dry-run
// stops there.
func TestTestPrune_DryRun(t *testing.T) {
	b := newPruneBackend(t, 1500)

	before := time.Now().UTC()
	stdout := commandStdout(t, func() error {
		return runCommandLine(t, "test", "prune", "--older-than", "30d", "--dry-run")
	})
	calls, left := b.requests()
	if !reflect.DeepEqual(dryRuns(calls), []bool{true}) || left != 1500 {
		t.Fatalf("calls %v, %d left; want one dry run, 1500 left", calls, left)
	}
	q := calls[0]
	if !reflect.DeepEqual(q["status"], []string{"DONE", "FAILED"}) || q.Get("limit") != "1000" {
		t.Errorf("query = %v, want status DONE and FAILED, limit 1000", q)
	}
	cutoff, err := time.Parse(time.RFC3339, q.Get("createdBefore"))
	if err != nil {
		t.Fatalf("createdBefore %q: %v", q.Get("createdBefore"), err)
	}
	if want := before.Add(-30 * 24 * time.Hour); cutoff.Before(want.Add(-2*time.Second)) || cutoff.After(want.Add(2*time.Second)) {
		t.Errorf("createdBefore = %s, want about %s", cutoff, want)
	}
	for _, want := range []string{
		"Found 1500 DONE or FAILED runs created before " + q.Get("createdBefore"),
		"Dry run: nothing deleted",
	} {
		if !strings.Contains(string(stdout), want) {
			t.Errorf("output lacks %q:\n%s", want, stdout)
		}
	}
}

// --yes deletes 1000 a call until none is left, with the cutoff the count
// used.
func TestTestPrune_YesDeletesInCalls(t *testing.T) {
	b := newPruneBackend(t, 1500)

	stdout := commandStdout(t, func() error {
		return runCommandLine(t, "test", "prune", "--older-than", "720h", "--yes")
	})
	calls, left := b.requests()
	if !reflect.DeepEqual(dryRuns(calls), []bool{true, false, false}) || left != 0 {
		t.Fatalf("dry runs %v, %d left; want a count, then two deletes, and none left", dryRuns(calls), left)
	}
	for i, q := range calls {
		if q.Get("createdBefore") != calls[0].Get("createdBefore") || q.Get("limit") != "1000" {
			t.Errorf("call %d: %v, want the count's cutoff and limit 1000", i, q)
		}
	}
	for _, want := range []string{
		"Found 1500 DONE or FAILED runs",
		"Deleted 1000 runs, 500 left",
		"Deleted 1500 DONE or FAILED runs created before " + calls[0].Get("createdBefore"),
	} {
		if !strings.Contains(string(stdout), want) {
			t.Errorf("output lacks %q:\n%s", want, stdout)
		}
	}
}

// --status narrows every call to the statuses it names.
func TestTestPrune_Status(t *testing.T) {
	b := newPruneBackend(t, 3)

	commandStdout(t, func() error {
		return runCommandLine(t, "test", "prune", "--older-than", "7d", "--status", "failed", "--yes")
	})
	calls, _ := b.requests()
	if len(calls) != 2 {
		t.Fatalf("%d calls, want a count and a delete", len(calls))
	}
	for i, q := range calls {
		if !reflect.DeepEqual(q["status"], []string{"FAILED"}) {
			t.Errorf("call %d: status %v, want [FAILED]", i, q["status"])
		}
	}
}

func TestTestPrune_NothingToDelete(t *testing.T) {
	b := newPruneBackend(t, 0)

	stdout := commandStdout(t, func() error {
		return runCommandLine(t, "test", "prune", "--older-than", "30d")
	})
	if calls, _ := b.requests(); !reflect.DeepEqual(dryRuns(calls), []bool{true}) {
		t.Errorf("dry runs %v, want the count alone", dryRuns(calls))
	}
	if !strings.Contains(string(stdout), "No DONE or FAILED run was created before") {
		t.Errorf("output does not say none matched:\n%s", stdout)
	}
}

// A call that deletes none stops the loop: the next would find the same runs.
// The runs left make the command fail.
func TestTestPrune_CallThatDeletesNone(t *testing.T) {
	b := newPruneBackend(t, 5)
	b.stuck = true

	_, stderr, err := commandOutput(t, func() error {
		return runCommandLine(t, "test", "prune", "--older-than", "30d", "--yes")
	})
	if _, ok := err.(*silentErr); !ok {
		t.Fatalf("err = %v (%T), want a silentErr, which exits 1", err, err)
	}
	if calls, _ := b.requests(); !reflect.DeepEqual(dryRuns(calls), []bool{true, false}) {
		t.Errorf("dry runs %v, want a count and one delete", dryRuns(calls))
	}
	if !strings.Contains(stderr, "5 more still match") || !strings.Contains(stderr, "deleted none") {
		t.Errorf("the error does not say the runs left and that the call deleted none:\n%s", stderr)
	}
}

// The calls stop at 100, 100,000 runs, and say how many are left.
func TestTestPrune_StopsAfterMaxCalls(t *testing.T) {
	b := newPruneBackend(t, pruneMaxCalls*prunePerCall+700)

	_, stderr, err := commandOutput(t, func() error {
		return runCommandLine(t, "test", "prune", "--older-than", "30d", "--yes")
	})
	if _, ok := err.(*silentErr); !ok {
		t.Fatalf("err = %v (%T), want a silentErr, which exits 1", err, err)
	}
	calls, left := b.requests()
	if len(calls) != 1+pruneMaxCalls || left != 700 {
		t.Errorf("%d calls, %d left; want %d calls and 700 left", len(calls), left, 1+pruneMaxCalls)
	}
	if !strings.Contains(stderr, "stopped with 700 left") {
		t.Errorf("the error does not say how many are left:\n%s", stderr)
	}
}

// Without a terminal, prune counts and refuses unless --yes says to go
// ahead, as kates test cleanup does.
func TestTestPrune_NoTerminalNeedsYes(t *testing.T) {
	b := newPruneBackend(t, 3)

	stdout, stderr, err := commandOutput(t, func() error {
		return runCommandLine(t, "test", "prune", "--older-than", "30d")
	})
	if _, ok := err.(*silentErr); !ok {
		t.Fatalf("err = %v (%T), want a silentErr, which exits 1", err, err)
	}
	if calls, left := b.requests(); !reflect.DeepEqual(dryRuns(calls), []bool{true}) || left != 3 {
		t.Errorf("dry runs %v, %d left; want the count alone and nothing deleted", dryRuns(calls), left)
	}
	if !strings.Contains(stderr, "--yes") {
		t.Errorf("the refusal does not mention --yes:\n%s", stderr)
	}
	if !strings.Contains(string(stdout), "Found 3 DONE or FAILED runs") {
		t.Errorf("the count is not shown before the refusal:\n%s", stdout)
	}
}

func TestTestPrune_AsksInATerminal(t *testing.T) {
	for _, tt := range []struct {
		matching int64
		answer   bool
		question string
	}{
		{3, false, "Delete 3 DONE or FAILED runs and their results?"},
		{3, true, "Delete 3 DONE or FAILED runs and their results?"},
		{1, true, "Delete 1 DONE or FAILED run and its results?"},
	} {
		t.Run(fmt.Sprintf("%d runs, answer %v", tt.matching, tt.answer), func(t *testing.T) {
			b := newPruneBackend(t, tt.matching)
			interactiveAllowedFn = func() bool { return true }
			var asked string
			confirmFn = func(q string) (bool, error) { asked = q; return tt.answer, nil }

			_, _, err := commandOutput(t, func() error {
				return runCommandLine(t, "test", "prune", "--older-than", "30d")
			})
			if asked != tt.question {
				t.Errorf("question = %q, want %q", asked, tt.question)
			}
			_, left := b.requests()
			if tt.answer && (err != nil || left != 0) {
				t.Errorf("after yes: err = %v, %d left; want nil and none", err, left)
			}
			if !tt.answer && (err == nil || left != tt.matching) {
				t.Errorf("after no: err = %v, %d left; want an error and nothing deleted", err, left)
			}
		})
	}
}

// An API from before DELETE /api/tests answers 405; kates says to upgrade it.
func TestTestPrune_OlderAPI(t *testing.T) {
	b := newPruneBackend(t, 3)
	b.refuse = http.StatusMethodNotAllowed

	_, stderr, err := commandOutput(t, func() error {
		return runCommandLine(t, "test", "prune", "--older-than", "30d", "--yes")
	})
	if _, ok := err.(*silentErr); !ok {
		t.Fatalf("err = %v (%T), want a silentErr, which exits 1", err, err)
	}
	if calls, _ := b.requests(); len(calls) != 1 {
		t.Errorf("%d calls, want the count alone", len(calls))
	}
	if !strings.Contains(stderr, "has no DELETE /api/tests") || !strings.Contains(stderr, "upgrade") {
		t.Errorf("the error does not say to upgrade the Kates API:\n%s", stderr)
	}
}

// Bad flags are refused before anything is sent.
func TestTestPrune_FlagValidation(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want string
	}{
		{nil, "--older-than is required"},
		{[]string{"--older-than", "0"}, "must be more than 0"},
		{[]string{"--older-than", "0d"}, "must be more than 0"},
		{[]string{"--older-than", "-1h"}, "must be more than 0"},
		{[]string{"--older-than", "-30d"}, "must be more than 0"},
		{[]string{"--older-than", "30x"}, "is not a duration"},
		{[]string{"--older-than", "1.5d"}, "is not a duration"},
		{[]string{"--older-than", "999999999d"}, "is not a duration"},
		{[]string{"--older-than", "30d", "--status", "RUNNING"}, "--status must be DONE or FAILED"},
		{[]string{"--older-than", "30d", "--status", "DONE,PENDING"}, "--status must be DONE or FAILED"},
		{[]string{"--older-than", "30d", "--status", ""}, "--status must name DONE, FAILED or both"},
	} {
		t.Run(strings.Join(append([]string{"prune"}, tt.args...), " "), func(t *testing.T) {
			b := newPruneBackend(t, 3)

			_, stderr, err := commandOutput(t, func() error {
				return runCommandLine(t, append([]string{"test", "prune", "--yes"}, tt.args...)...)
			})
			if err == nil || !strings.Contains(stderr, tt.want) {
				t.Errorf("err = %v, stderr %q; want an error saying %q", err, stderr, tt.want)
			}
			if calls, _ := b.requests(); len(calls) != 0 {
				t.Errorf("sent %v", calls)
			}
		})
	}
}

func TestParseOlderThan(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"30d":     30 * 24 * time.Hour,
		" 7d ":    7 * 24 * time.Hour,
		"+2d":     48 * time.Hour,
		"720h":    720 * time.Hour,
		"90m":     90 * time.Minute,
		"1h30m":   90 * time.Minute,
		"1.5h":    90 * time.Minute,
		"106751d": time.Duration(maxOlderThanDays) * 24 * time.Hour,
	} {
		got, err := parseOlderThan(in)
		if err != nil || got != want {
			t.Errorf("parseOlderThan(%q) = %s, %v; want %s", in, got, err, want)
		}
	}
}

func TestParsePruneStatuses(t *testing.T) {
	for _, tt := range []struct {
		in   []string
		want []string
	}{
		{[]string{"DONE", "FAILED"}, []string{"DONE", "FAILED"}},
		{[]string{"failed", "done"}, []string{"DONE", "FAILED"}},
		{[]string{" Failed ", "FAILED"}, []string{"FAILED"}},
		{[]string{"done"}, []string{"DONE"}},
	} {
		got, err := parsePruneStatuses(tt.in)
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("parsePruneStatuses(%q) = %v, %v; want %v", tt.in, got, err, tt.want)
		}
	}
}

// -o json prints the answer for the whole command and nothing else: with
// --dry-run the count, otherwise matched as the first delete found it,
// deleted over every call, and remaining after the last.
func TestTestPrune_JSON(t *testing.T) {
	t.Run("dry run", func(t *testing.T) {
		b := newPruneBackend(t, 1500)

		stdout := commandStdout(t, func() error {
			return runCommandLine(t, "test", "prune", "--older-than", "30d", "--dry-run", "-o", "json")
		})
		calls, _ := b.requests()
		want := fmt.Sprintf(`{"createdBefore":%q,"statuses":["DONE","FAILED"],"dryRun":true,"matched":1500,"deleted":0,"remaining":1500}`,
			calls[0].Get("createdBefore"))
		if got := compactJSON(t, stdout); got != want {
			t.Errorf("stdout = %s, want %s", got, want)
		}
	})
	t.Run("delete", func(t *testing.T) {
		b := newPruneBackend(t, 1500)

		stdout := commandStdout(t, func() error {
			return runCommandLine(t, "test", "prune", "--older-than", "30d", "--yes", "-o", "json")
		})
		calls, _ := b.requests()
		want := fmt.Sprintf(`{"createdBefore":%q,"statuses":["DONE","FAILED"],"dryRun":false,"matched":1500,"deleted":1500,"remaining":0}`,
			calls[0].Get("createdBefore"))
		if got := compactJSON(t, stdout); got != want {
			t.Errorf("stdout = %s, want %s", got, want)
		}
	})
}
