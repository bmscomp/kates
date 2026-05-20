package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// kafkaPodStatus holds Running/Total counts for broker and controller pods.
type kafkaPodStatus struct {
	brokerRunning int
	brokerTotal   int
	ctrlRunning   int
	ctrlTotal     int
	pendingPods   []string // names of Pending pods, for diagnostics
}

// countPodsByLabel queries pods matching the given label selector and counts
// how many are Running vs total. Uses separate NAME + PHASE columns to avoid
// JSONPath escaping issues with label keys that contain dots or slashes.
func countPodsByLabel(ctx context.Context, namespace, selector string) (running, total int, pending []string) {
	out, _ := exec.CommandContext(ctx,
		"kubectl", "get", "pods",
		"-n", namespace,
		"-l", selector,
		"--no-headers",
		"-o", "custom-columns=NAME:.metadata.name,PHASE:.status.phase",
	).Output()

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name, phase := fields[0], fields[1]
		total++
		switch phase {
		case "Running":
			running++
		case "Pending", "ContainerCreating":
			pending = append(pending, name)
		}
	}
	return
}

// pollKafkaPods returns broker and controller pod counts using Strimzi's own
// role labels (strimzi.io/broker-role and strimzi.io/controller-role).
func pollKafkaPods(ctx context.Context, namespace string) kafkaPodStatus {
	var s kafkaPodStatus

	s.brokerRunning, s.brokerTotal, s.pendingPods = countPodsByLabel(ctx, namespace,
		"strimzi.io/cluster=krafter,strimzi.io/broker-role=true")

	ctrlRunning, ctrlTotal, ctrlPending := countPodsByLabel(ctx, namespace,
		"strimzi.io/cluster=krafter,strimzi.io/controller-role=true")
	s.ctrlRunning = ctrlRunning
	s.ctrlTotal = ctrlTotal
	s.pendingPods = append(s.pendingPods, ctrlPending...)

	return s
}

// progressBar returns a 10-char Unicode block bar, e.g. "████████░░".
func progressBar(running, total int) string {
	const width = 10
	const full = "█"
	const empty = "░"
	if total == 0 {
		return strings.Repeat(empty, width)
	}
	filled := (running * width) / total
	return strings.Repeat(full, filled) + strings.Repeat(empty, width-filled)
}

// fmtElapsed formats seconds as "0:00".
func fmtElapsed(secs int) string {
	return fmt.Sprintf("%d:%02d", secs/60, secs%60)
}

// fmtRemaining formats a duration as "7m54s" trimming sub-seconds.
func fmtRemaining(d time.Duration) string {
	d = d.Round(time.Second)
	if d <= 0 {
		return "0s"
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if m == 0 {
		return fmt.Sprintf("%ds", s)
	}
	return fmt.Sprintf("%dm%02ds", m, s)
}

// podPhaseLabel returns a short phase badge for display.
func podPhaseLabel(running, total int) string {
	if total == 0 {
		return "⏳ waiting"
	}
	if running == total {
		return "✅ running"
	}
	pending := total - running
	return fmt.Sprintf("⚠  %d pending", pending)
}

// waitKafkaReady polls the Kafka CR condition AND actual pod phases every 6 s.
//
// It declares success only when ALL three conditions hold simultaneously:
//   - kafka/krafter has condition Ready=True         (Strimzi agrees)
//   - every broker pod is Running (and at least one exists)
//   - every controller pod is Running (and at least one exists)
func waitKafkaReady(ctx context.Context, namespace string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	poll := 6 * time.Second
	elapsed := 0
	hintShown := false

	fmt.Printf("\n    ╭─ Kafka Cluster  (%s timeout) ─────────────────────────╮\n", fmtRemaining(timeout))

	for {
		// ── 1. Kafka CR condition ──────────────────────────────────────────────
		condOut, _ := exec.CommandContext(ctx,
			"kubectl", "get", "kafka/krafter", "-n", namespace,
			"-o", `jsonpath={range .status.conditions[*]}{.type}={.status} {end}`,
		).Output()
		crReady := strings.Contains(string(condOut), "Ready=True")

		// ── 2. Pod counts ──────────────────────────────────────────────────────
		pods := pollKafkaPods(ctx, namespace)

		// ── 3. Entity Operator ─────────────────────────────────────────────────
		eoOut, _ := exec.CommandContext(ctx,
			"kubectl", "get", "pods", "-n", namespace,
			"-l", "app.kubernetes.io/name=entity-operator",
			"--no-headers",
			"-o", "custom-columns=PHASE:.status.phase",
		).Output()
		eoReady := strings.Contains(string(eoOut), "Running")
		eoIcon := "⏳"
		if eoReady {
			eoIcon = "✅"
		}

		// ── 4. Evaluate ────────────────────────────────────────────────────────
		allBrokersUp := pods.brokerTotal > 0 && pods.brokerRunning == pods.brokerTotal
		allCtrlUp := pods.ctrlTotal > 0 && pods.ctrlRunning == pods.ctrlTotal

		if crReady && allBrokersUp && allCtrlUp {
			// Final success block
			fmt.Printf("    │  Brokers      [%s]  %d/%d  ✅ running\n",
				progressBar(pods.brokerRunning, pods.brokerTotal),
				pods.brokerRunning, pods.brokerTotal)
			fmt.Printf("    │  Controllers  [%s]  %d/%d  ✅ running\n",
				progressBar(pods.ctrlRunning, pods.ctrlTotal),
				pods.ctrlRunning, pods.ctrlTotal)
			fmt.Printf("    │  Entity Op    %s\n", eoIcon)
			fmt.Printf("    │  CR status    ✅ Ready=True\n")
			fmt.Printf("    ╰──────────────────────────────────────────────────────────╯\n")
			fmt.Printf("    ✅ Kafka ready  elapsed %s\n\n", fmtElapsed(elapsed))
			return nil
		}

		// ── 5. Progress block (overwrites previous with blank prefix trick) ────
		remaining := time.Until(deadline)
		fmt.Printf("    │  Brokers      [%s]  %d/%d  %s\n",
			progressBar(pods.brokerRunning, pods.brokerTotal),
			pods.brokerRunning, pods.brokerTotal,
			podPhaseLabel(pods.brokerRunning, pods.brokerTotal))
		fmt.Printf("    │  Controllers  [%s]  %d/%d  %s\n",
			progressBar(pods.ctrlRunning, pods.ctrlTotal),
			pods.ctrlRunning, pods.ctrlTotal,
			podPhaseLabel(pods.ctrlRunning, pods.ctrlTotal))
		fmt.Printf("    │  Entity Op    %s          %s elapsed  %s left\n",
			eoIcon, fmtElapsed(elapsed), fmtRemaining(remaining))
		fmt.Printf("    │\n")

		// ── 6. Pending pods hint (once, after 30 s) ────────────────────────────
		if !hintShown && elapsed >= 30 && len(pods.pendingPods) > 0 {
			hintShown = true
			fmt.Printf("    │  ⚠  %d pod(s) Pending — diagnose with:\n", len(pods.pendingPods))
			fmt.Printf("    │     kubectl describe pod %s -n %s\n", pods.pendingPods[0], namespace)
			fmt.Printf("    │\n")
		}

		// ── 7. Timeout ─────────────────────────────────────────────────────────
		if time.Now().After(deadline) {
			fmt.Printf("    ╰──────────────────────────────────────────────────────────╯\n")
			return fmt.Errorf("kafka not ready after %s (brokers:%d/%d controllers:%d/%d pending:%d)",
				timeout,
				pods.brokerRunning, pods.brokerTotal,
				pods.ctrlRunning, pods.ctrlTotal,
				len(pods.pendingPods))
		}

		// ── 8. Wait ────────────────────────────────────────────────────────────
		select {
		case <-ctx.Done():
			fmt.Printf("    ╰──────────────────────────────────────────────────────────╯\n")
			return ctx.Err()
		case <-time.After(poll):
			elapsed += int(poll.Seconds())
		}
	}
}
