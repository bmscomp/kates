package cmd

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Without a dashboard the wait prints pod counts while it polls, so a CI log
// shows progress instead of going quiet for the whole timeout.
func TestWaitComponentReadySilent_HeartbeatWithoutDashboard(t *testing.T) {
	origOut, prevDL, prevPoll, prevBeat := runExecOutputFn, dl, silentPollInterval, silentHeartbeat
	t.Cleanup(func() { runExecOutputFn, dl, silentPollInterval, silentHeartbeat = origOut, prevDL, prevPoll, prevBeat })
	dl = nil
	silentPollInterval, silentHeartbeat = time.Millisecond, 0

	polls := 0
	runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		polls++
		if polls < 3 {
			return []byte("Running,Pending,Pending,"), nil
		}
		return []byte("Running,Running,Running,"), nil
	}
	var err error
	out := captureStdout(t, func() {
		err = waitComponentReadySilent(context.Background(), "kafka", "strimzi.io/cluster=krafter", 5*time.Second)
	})
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if !strings.Contains(out, "still waiting for pods in kafka (strimzi.io/cluster=krafter): 1/3 ready, 2 Pending") {
		t.Errorf("no heartbeat in:\n%s", out)
	}
	if strings.Contains(out, "3/3 ready") {
		t.Errorf("a heartbeat after the pods were ready:\n%s", out)
	}
}

func TestPodHeartbeat(t *testing.T) {
	for _, tc := range []struct {
		phases []string
		want   string
	}{
		{nil, "still waiting for pods in ns (app=x): none yet (1m30s)"},
		{[]string{"Running", "Succeeded"}, "still waiting for pods in ns (app=x): 2/2 ready (1m30s)"},
		{[]string{"Pending", "Running", "Pending", "Failed"}, "still waiting for pods in ns (app=x): 1/4 ready, 1 Failed, 2 Pending (1m30s)"},
	} {
		if got := podHeartbeat("ns", "app=x", tc.phases, 90*time.Second+300*time.Millisecond); got != tc.want {
			t.Errorf("podHeartbeat(%v) = %q, want %q", tc.phases, got, tc.want)
		}
	}
	if got := podPhases(" Running,Pending, "); len(got) != 2 || got[0] != "Running" || got[1] != "Pending" {
		t.Errorf("podPhases: %q", got)
	}
}
