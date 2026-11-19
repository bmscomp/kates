package cmd

import (
	"slices"
	"testing"
)

func TestStopPortForwardsOnPortStopsOnlyThatPort(t *testing.T) {
	prevList, prevStop, prevCtx := listProcessesFn, stopProcessFn, portsKubeContext
	t.Cleanup(func() { listProcessesFn, stopProcessFn, portsKubeContext = prevList, prevStop, prevCtx })

	portsKubeContext = "kind-panda"
	listProcessesFn = func() (map[int]string, error) {
		return map[int]string{
			1: "kubectl port-forward --address 127.0.0.1 service/kates 8080:8080 -n kates",
			2: "kubectl --context kind-panda port-forward service/kates 8080:8080 -n kates",
			3: "kubectl port-forward service/krafter-kafka-bootstrap 9092:9092 -n kafka",
			4: "kubectl port-forward service/other 18080:8080 -n kates",
			5: "kubectl --context eks-prod port-forward service/kates 8080:8080 -n kates",
			6: "kates test create --records 8080:1",
		}, nil
	}
	var stopped []int
	stopProcessFn = func(pid int) error { stopped = append(stopped, pid); return nil }

	if n := stopPortForwardsOnPort(8080); n != 2 {
		t.Errorf("stopped %d forwards, want 2", n)
	}
	slices.Sort(stopped)
	if !slices.Equal(stopped, []int{1, 2}) {
		t.Errorf("stopped %v, want [1 2]", stopped)
	}
}

func TestFollowLeaderPortForwardOnlyForThePortsContext(t *testing.T) {
	prevCtx := activeContext
	t.Cleanup(func() { activeContext = prevCtx })
	activeContext = "prod"
	if err := followLeaderPortForward(t.Context()); err == nil {
		t.Error("followLeaderPortForward touched a forward outside the ports context")
	}
}
