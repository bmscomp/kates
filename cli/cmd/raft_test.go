package cmd

import (
	"strings"
	"testing"
)

func TestRaftPs(t *testing.T) {
	body := `{"enabled": true, "answeredBy": "kates-1", "members": [
		{"id": "kates-0", "role": "LEADER", "term": 7, "leaderId": "kates-0", "roleElapsedMs": 11520000,
		 "commitIndex": 1042, "appliedIndex": 1042, "storageHealthy": true},
		{"id": "kates-1", "role": "FOLLOWER", "term": 7, "leaderId": "kates-0", "roleElapsedMs": 5000,
		 "commitIndex": 1039, "appliedIndex": 1039, "storageHealthy": false, "lastContactMs": 40},
		{"id": "kates-2", "error": "TimeoutIOException: no reply"}]}`
	ts, buf := setupTest(t, "GET", "/api/quorum", 200, body)
	defer ts.Close()

	if err := raftPsCmd.RunE(raftPsCmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stripAnsi(buf.String())
	for _, want := range []string{
		"LEADER", "1042", "40ms", "storage unhealthy",
		"UNREACHABLE", "TimeoutIOException: no reply",
		"leader kates-0 for 3h12m0s", "quorum OK (2/3)", "answered by kates-1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	// kates-1 trails the leader's commit index by 3.
	if !strings.Contains(out, " 3 ") {
		t.Errorf("missing the lag of 3:\n%s", out)
	}
}

func TestRaftPsNoQuorum(t *testing.T) {
	ts, buf := setupTest(t, "GET", "/api/quorum", 200, `{"enabled": true, "members": [
		{"id": "kates-0", "role": "CANDIDATE", "commitIndex": 5},
		{"id": "kates-1", "error": "x"}, {"id": "kates-2", "error": "y"}]}`)
	defer ts.Close()

	if err := raftPsCmd.RunE(raftPsCmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stripAnsi(buf.String())
	if !strings.Contains(out, "no leader") || !strings.Contains(out, "NO QUORUM (1/3 answered, 2 needed)") {
		t.Errorf("want no leader and no quorum:\n%s", out)
	}
}

func TestRaftPsAlone(t *testing.T) {
	ts, buf := setupTest(t, "GET", "/api/quorum", 200, `{"enabled": false, "members": []}`)
	defer ts.Close()

	if err := raftPsCmd.RunE(raftPsCmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out := stripAnsi(buf.String()); !strings.Contains(out, "runs alone") {
		t.Errorf("want the alone hint:\n%s", out)
	}
}
