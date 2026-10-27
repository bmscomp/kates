package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidProfileName(t *testing.T) {
	for _, ok := range []string{"production-v3.2", "pre_upgrade", "A1"} {
		if err := validProfileName(ok); err != nil {
			t.Errorf("%q: %v, want valid", ok, err)
		}
	}
	for _, bad := range []string{"", "../etc", "a/b", "a b", ".", "..", "name\x00"} {
		if err := validProfileName(bad); err == nil {
			t.Errorf("%q: valid, want refused", bad)
		}
	}
}

// A profile is a baseline, so only a finished run with results makes one: a
// RUNNING run saved as a profile asserted every later run against zeros.
func TestProfileSave_RefusesUnfinishedRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ts, buf := setupTest(t, "GET", "/api/tests/run-1", 200,
		`{"id":"run-1","testType":"LOAD","status":"RUNNING","results":[{"phaseName":"produce","throughputRecordsPerSec":100}]}`)
	defer ts.Close()

	err := profileSaveCmd.RunE(profileSaveCmd, []string{"base", "run-1"})
	if _, ok := err.(*silentErr); !ok {
		t.Fatalf("err = %v (%T), want a silentErr", err, err)
	}
	if out := stripAnsi(buf.String()); !strings.Contains(out, "RUNNING") || !strings.Contains(out, "not DONE") {
		t.Errorf("message = %q, want it to say the run is RUNNING, not DONE", out)
	}
	if _, err := os.Stat(filepath.Join(profileDir(), "base.json")); !os.IsNotExist(err) {
		t.Errorf("profile written for an unfinished run (stat: %v)", err)
	}

	ts2, buf2 := setupTest(t, "GET", "/api/tests/run-2", 200, `{"id":"run-2","testType":"LOAD","status":"DONE"}`)
	defer ts2.Close()
	err = profileSaveCmd.RunE(profileSaveCmd, []string{"base", "run-2"})
	if _, ok := err.(*silentErr); !ok {
		t.Fatalf("a DONE run without results: err = %v (%T), want a silentErr", err, err)
	}
	if out := stripAnsi(buf2.String()); !strings.Contains(out, "no results") {
		t.Errorf("message = %q, want it to say the run has no results", out)
	}

	ts3, _ := setupTest(t, "GET", "/api/tests/run-3", 200, `{"id":"run-3","testType":"LOAD","status":"DONE"}`)
	defer ts3.Close()
	if err := profileSaveCmd.RunE(profileSaveCmd, []string{"../escape", "run-3"}); err == nil {
		t.Errorf("a name with a path in it was accepted")
	}
}

// A zero baseline is an error, not a PASS: the deltas against it were ±Inf
// or NaN, which compared as no regression.
func TestProfileAssert_ZeroBaselineIsAnError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ts, buf := setupTest(t, "GET", "/api/tests/run-1", 200,
		`{"id":"run-1","testType":"LOAD","status":"DONE","results":[{"phaseName":"produce","recordsSent":100,"throughputRecordsPerSec":1000,"p99LatencyMs":5}]}`)
	defer ts.Close()

	for _, tc := range []struct {
		name    string
		profile PerformanceProfile
		want    string
	}{
		{"no throughput", PerformanceProfile{Name: "zero-tp", P99Ms: 5}, "no throughput"},
		{"no p99", PerformanceProfile{Name: "zero-p99", Throughput: 1000}, "no P99"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := saveProfile(tc.profile); err != nil {
				t.Fatal(err)
			}
			buf.Reset()
			err := profileAssertCmd.RunE(profileAssertCmd, []string{tc.profile.Name, "run-1"})
			if _, ok := err.(*silentErr); !ok {
				t.Fatalf("err = %v (%T), want a silentErr", err, err)
			}
			out := stripAnsi(buf.String())
			if !strings.Contains(out, tc.want) {
				t.Errorf("message = %q, want %q", out, tc.want)
			}
			if strings.Contains(out, "PASS") {
				t.Errorf("a zero baseline passed:\n%s", out)
			}
		})
	}

	// The same run against a real baseline passes.
	if err := saveProfile(PerformanceProfile{Name: "ok", Throughput: 1000, P99Ms: 5}); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := profileAssertCmd.RunE(profileAssertCmd, []string{"ok", "run-1"}); err != nil {
		t.Errorf("assert against a real baseline: %v\n%s", err, buf.String())
	}
}
