package cmd

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Poll and heartbeat intervals of the silent waits; variables so a test can
// shorten them.
var (
	silentPollInterval = 3 * time.Second
	silentHeartbeat    = 30 * time.Second
)

// waitComponentReadySilent polls pods matching a label selector until all are
// Running/Succeeded or the timeout expires. It does NOT call
// dl.StartComponent/FinishComponent — the caller (deploy.go) is responsible
// for managing the dashboard component lifecycle.
func waitComponentReadySilent(ctx context.Context, namespace, selector string, timeout time.Duration) error {
	start := time.Now()
	deadline := start.Add(timeout)
	lastBeat := start
	for time.Now().Before(deadline) {
		out, err := runExecOutputFn(ctx, "kubectl", "get", "pods", "-n", namespace, "-l", selector, "-o", "jsonpath={range .items[*]}{.status.phase}{','}{end}")
		var phases []string
		if err == nil {
			phases = podPhases(string(out))
			if len(phases) > 0 && readyPods(phases) == len(phases) {
				return nil
			}
		}

		// With no dashboard (CI, a pipe) nothing else says the deploy is
		// alive: a line every 30 s shows the pod counts moving, where the
		// log used to go quiet for up to fifteen minutes.
		if !dl.live() && time.Since(lastBeat) >= silentHeartbeat {
			lastBeat = time.Now()
			dl.Printf("    %s\n", podHeartbeat(namespace, selector, phases, time.Since(start)))
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(silentPollInterval):
		}
	}
	return fmt.Errorf("timeout waiting for pods (selector=%s) to become ready", selector)
}

// podPhases splits kubectl's "Running,Pending," list, dropping the empty tail.
func podPhases(out string) []string {
	var phases []string
	for _, p := range strings.Split(strings.TrimSpace(out), ",") {
		if p != "" {
			phases = append(phases, p)
		}
	}
	return phases
}

// readyPods counts the phases that are done waiting.
func readyPods(phases []string) int {
	n := 0
	for _, p := range phases {
		if p == "Running" || p == "Succeeded" {
			n++
		}
	}
	return n
}

// podHeartbeat is one progress line for a log: "still waiting for pods in
// kafka (strimzi.io/cluster=krafter): 2/3 ready, 1 Pending (1m30s)".
func podHeartbeat(namespace, selector string, phases []string, elapsed time.Duration) string {
	where := fmt.Sprintf("pods in %s (%s)", namespace, selector)
	if len(phases) == 0 {
		return fmt.Sprintf("still waiting for %s: none yet (%s)", where, elapsed.Round(time.Second))
	}
	counts := map[string]int{}
	for _, p := range phases {
		if p != "Running" && p != "Succeeded" {
			counts[p]++
		}
	}
	others := make([]string, 0, len(counts))
	for p, n := range counts {
		others = append(others, fmt.Sprintf("%d %s", n, p))
	}
	sort.Strings(others)
	line := fmt.Sprintf("still waiting for %s: %d/%d ready", where, readyPods(phases), len(phases))
	if len(others) > 0 {
		line += ", " + strings.Join(others, ", ")
	}
	return fmt.Sprintf("%s (%s)", line, elapsed.Round(time.Second))
}

// waitCustomResourceReadySilent polls custom resources matching a label selector
// until all have Ready=True condition or the timeout expires.
func waitCustomResourceReadySilent(ctx context.Context, kind, namespace, selector string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := runExecOutputFn(ctx, "kubectl", "get", kind, "-n", namespace, "-l", selector, "-o", "jsonpath={range .items[*]}{.status.conditions[?(@.type==\"Ready\")].status}{','}{end}")
		if err == nil {
			statuses := strings.Split(strings.TrimSpace(string(out)), ",")
			allReady := true
			count := 0
			for _, s := range statuses {
				if s != "" {
					count++
					if s != "True" {
						allReady = false
					}
				}
			}
			if count > 0 && allReady {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(silentPollInterval):
		}
	}
	return fmt.Errorf("timeout waiting for %s to become ready", kind)
}

func waitNamedCustomResourceReadySilent(ctx context.Context, kind, namespace, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := runExecOutputFn(ctx, "kubectl", "get", kind, name, "-n", namespace, "-o", `jsonpath={.status.conditions[?(@.type=="Ready")].status}`)
		if err == nil && strings.Contains(string(out), "True") {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(silentPollInterval):
		}
	}
	return fmt.Errorf("timeout waiting for %s/%s to become ready", kind, name)
}

func waitKafkaUsersReadySilent(ctx context.Context, namespace string, timeout time.Duration) error {
	return waitCustomResourceReadySilent(ctx, "kafkauser", namespace, "strimzi.io/cluster="+deployKafkaName, timeout)
}

func waitKafkaConnectReadySilent(ctx context.Context, namespace, name string, timeout time.Duration) error {
	return waitNamedCustomResourceReadySilent(ctx, "kafkaconnect", namespace, name, timeout)
}

func waitConnectorReadySilent(ctx context.Context, namespace string, timeout time.Duration) error {
	return waitCustomResourceReadySilent(ctx, "kafkaconnector", namespace, "strimzi.io/cluster=connect-cluster", timeout)
}
