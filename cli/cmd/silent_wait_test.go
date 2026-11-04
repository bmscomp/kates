package cmd

import (
	"context"
	"errors"
	"fmt"
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

// podEventsStub answers the wait's three reads: the phases, the Pending pods'
// UIDs and the namespace's events. A nil events read is refused.
func podEventsStub(t *testing.T, events func() []byte) {
	t.Helper()
	origOut, prevDL, prevClock := runExecOutputFn, dl, deployClock
	t.Cleanup(func() { runExecOutputFn, dl, deployClock = origOut, prevDL, prevClock })
	dl, deployClock = nil, newFakeClock()
	runExecOutputFn = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		all := strings.Join(args, " ")
		switch {
		case strings.Contains(all, "get events"):
			if out := events(); out != nil {
				return out, nil
			}
			return nil, errors.New("events is forbidden")
		case strings.Contains(all, "status.phase=Pending"):
			return []byte("u1 u2"), nil
		}
		return []byte("Running,Pending,Pending,"), nil
	}
}

const podEventsFormat = `{"items":[
 {"involvedObject":{"uid":"gone"},"reason":"FailedScheduling","message":"from the pod that had this name","eventTime":"2026-01-01T00:00:09.000000Z"},
 {"involvedObject":{"uid":"u1"},"reason":"FailedScheduling","message":"%[1]s","eventTime":"2026-01-01T00:00:01.000000Z","lastTimestamp":null,"series":null},
 {"involvedObject":{"uid":"u2"},"reason":"FailedScheduling","message":"%[1]s","eventTime":"2026-01-01T00:00:01.000000Z","series":{"lastObservedTime":"2026-01-01T00:00:05.000000Z"}}
 %[2]s]}`

const allShortOfCPU = "0/3 nodes are available: 3 Insufficient cpu. preemption: 0/3 nodes are available: 3 No preemption victims found for incoming pod."

// The wait says why within a minute, once, and gives up when every node stays
// short of CPU, well before the timeout.
func TestWaitComponentReadySilent_NamesWhyAndStopsOnInsufficient(t *testing.T) {
	podEventsStub(t, func() []byte { return fmt.Appendf(nil, podEventsFormat, allShortOfCPU, "") })

	var err error
	start := time.Time{}
	out := captureStdout(t, func() {
		start = deployClock.Now()
		err = waitComponentReadySilent(context.Background(), "kafka", "strimzi.io/cluster=krafter", 15*time.Minute)
	})
	want := "2 pod(s) in kafka cannot be scheduled: 0/3 nodes are available: 3 Insufficient cpu"
	if strings.Count(out, want) != 1 {
		t.Errorf("want the reason once:\n%s", out)
	}
	if strings.Contains(out, "from the pod that had this name") {
		t.Errorf("an old pod's event was read as this one's:\n%s", out)
	}
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want the reason", err)
	}
	if took := deployClock.Now().Sub(start); took < podBlockHold || took > 4*time.Minute {
		t.Errorf("stopped after %s, want about %s", took, podBlockHold)
	}
}

func TestEveryNodeShort(t *testing.T) {
	for detail, want := range map[string]bool{
		"0/3 nodes are available: 3 Insufficient cpu":                                                                            true,
		"0/1 nodes are available: 1 Insufficient cpu, 1 Insufficient memory":                                                     true,
		"0/3 nodes are available: 2 Insufficient cpu, 1 Insufficient memory":                                                     true,
		"0/4 nodes are available: 1 node(s) had untolerated taint {node-role.kubernetes.io/control-plane: }, 3 Insufficient cpu": true,
		"0/4 nodes are available: 1 node(s) had untolerated taint {a: b}, 3 Insufficient cpu":                                    false,
		"0/1 nodes are available: 1 node(s) had untolerated taint {node-role.kubernetes.io/control-plane: }":                     false,
		"0/3 nodes are available: 1 Insufficient cpu, 2 node(s) didn't match Pod's node affinity/selector":                       false,
		"1/3 nodes are available: 2 Insufficient cpu":                                                                            false,
	} {
		if got := everyNodeShort(detail); got != want {
			t.Errorf("everyNodeShort(%q) = %v, want %v", detail, got, want)
		}
	}
}

// The Kafka Connect wait reads its pods the way the others do: the workers can
// stay Pending where Kafka fit.
func TestWaitKafkaConnectReadySilent_StopsOnInsufficient(t *testing.T) {
	podEventsStub(t, func() []byte { return fmt.Appendf(nil, podEventsFormat, allShortOfCPU, "") })

	var err error
	captureStdout(t, func() {
		err = waitKafkaConnectReadySilent(context.Background(), "kafka", "connect-cluster", 15*time.Minute)
	})
	if err == nil || !strings.Contains(err.Error(), "stopped waiting") || !strings.Contains(err.Error(), "3 Insufficient cpu") {
		t.Errorf("err = %v, want the early stop naming the reason", err)
	}
}

// A wait that may yet clear runs to its timeout, and the timeout names the
// reason.
func TestWaitComponentReadySilent_NoEarlyStop(t *testing.T) {
	scaleUp := `,{"involvedObject":{"uid":"u1"},"reason":"TriggeredScaleUp","message":"pod triggered scale-up","lastTimestamp":"2026-01-01T00:00:00Z"}`
	imagePull := `,{"involvedObject":{"uid":"u1"},"reason":"Failed","message":"Failed to pull image \"kates:1\": not found","lastTimestamp":"2026-01-01T00:00:06Z"},
	 {"involvedObject":{"uid":"u1"},"reason":"BackOff","message":"Back-off pulling image \"kates:1\"","lastTimestamp":"2026-01-01T00:00:07Z"},
	 {"involvedObject":{"uid":"u2"},"reason":"BackOff","message":"Back-off pulling image \"kates:1\"","lastTimestamp":"2026-01-01T00:00:07Z"}`
	for name, tc := range map[string]struct {
		message, extra, want string
	}{
		"an autoscaler is adding a node": {allShortOfCPU, scaleUp, "3 Insufficient cpu"},
		"some nodes refuse for a taint":  {"0/4 nodes are available: 1 node(s) had untolerated taint {a: b}, 3 Insufficient cpu.", "", "1 node(s) had untolerated taint"},
		"some nodes refuse for a volume": {"0/3 nodes are available: 1 Insufficient cpu, 2 node(s) had volume node affinity conflict.", "", "2 node(s) had volume"},
		"only the control plane's taint": {"0/1 nodes are available: 1 node(s) had untolerated taint {node-role.kubernetes.io/control-plane: }.", "", "untolerated taint"},
		"an image will not pull":         {allShortOfCPU, imagePull, `1 pod(s) in kafka cannot pull an image: Failed to pull image "kates:1": not found`},
	} {
		t.Run(name, func(t *testing.T) {
			podEventsStub(t, func() []byte { return fmt.Appendf(nil, podEventsFormat, tc.message, tc.extra) })
			var err error
			start := time.Time{}
			captureStdout(t, func() {
				start = deployClock.Now()
				err = waitComponentReadySilent(context.Background(), "kafka", "x=y", 15*time.Minute)
			})
			if err == nil || !strings.Contains(err.Error(), "timeout waiting for pods") || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want a timeout naming %q", err, tc.want)
			}
			if took := deployClock.Now().Sub(start); took < 15*time.Minute {
				t.Errorf("stopped after %s, want the whole timeout", took)
			}
		})
	}
}

// Pods that moved on leave no reason behind, and a pod scheduled since does
// not count as blocked.
func TestWaitComponentReadySilent_ReasonClears(t *testing.T) {
	reads := 0
	podEventsStub(t, func() []byte {
		reads++
		scheduled := ""
		if reads > 1 {
			for _, uid := range []string{"u1", "u2"} {
				scheduled += fmt.Sprintf(`,{"involvedObject":{"uid":%q},"reason":"Scheduled","message":"assigned","eventTime":"2026-01-01T00:00:08.000000Z"}`, uid)
			}
		}
		return fmt.Appendf(nil, podEventsFormat, allShortOfCPU, scheduled)
	})
	var err error
	out := captureStdout(t, func() {
		err = waitComponentReadySilent(context.Background(), "kafka", "x=y", time.Minute)
	})
	if !strings.Contains(out, "cannot be scheduled") {
		t.Errorf("first read not reported:\n%s", out)
	}
	if err == nil || strings.Contains(err.Error(), "scheduled") {
		t.Errorf("err = %v, want a plain timeout", err)
	}
}

// A refused events read is silent and leaves the wait as it was.
func TestWaitComponentReadySilent_EventsRefused(t *testing.T) {
	for name, read := range map[string]func() []byte{
		"forbidden": func() []byte { return nil },
		"garbage":   func() []byte { return []byte("not json") },
	} {
		t.Run(name, func(t *testing.T) {
			podEventsStub(t, read)
			var err error
			out := captureStdout(t, func() {
				err = waitComponentReadySilent(context.Background(), "kafka", "x=y", 15*time.Minute)
			})
			if want := "timeout waiting for pods (selector=x=y) to become ready"; err == nil || err.Error() != want {
				t.Errorf("err = %v, want %q", err, want)
			}
			if strings.Contains(out, "⚠") || strings.Contains(out, "forbidden") {
				t.Errorf("a refused read spoke:\n%s", out)
			}
		})
	}
}

// Kafka's pods can go Pending after waitPods passed; the CR wait says so too.
func TestWaitKafkaReady_NamesWhyPodsWait(t *testing.T) {
	podEventsStub(t, func() []byte { return fmt.Appendf(nil, podEventsFormat, allShortOfCPU, "") })
	inner := runExecOutputFn
	runExecOutputFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "kafka krafter") {
			return []byte("3|2|False||NotReady: brokers not up"), nil
		}
		return inner(ctx, name, args...)
	}
	var err error
	captureStdout(t, func() {
		err = waitKafkaReady(context.Background(), "kafka", "krafter", deployClock.Now().Add(15*time.Minute))
	})
	if err == nil || !strings.Contains(err.Error(), "3 Insufficient cpu") {
		t.Errorf("err = %v, want the pods' reason", err)
	}
}
