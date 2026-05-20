package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ── ANSI color helpers ───────────────────────────────────────────────────────
const (
	cReset = "\033[0m"
	cBlue  = "\033[38;5;39m"  // bright blue — OK / success
	cRed   = "\033[38;5;196m" // bright red  — KO / error / pending
	cAmber = "\033[38;5;208m" // amber       — warning / hint
	cDim   = "\033[38;5;243m" // dim gray    — borders / labels
	cBold  = "\033[1m"
	cGreen = "\033[38;5;48m"  // green — final success banner
)

func blue(s string) string  { return cBlue + s + cReset }
func red(s string) string   { return cRed + s + cReset }
func amber(s string) string { return cAmber + s + cReset }
func dim(s string) string   { return cDim + s + cReset }
func green(s string) string { return cGreen + s + cReset }
func bold(s string) string  { return cBold + s + cReset }

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

// colorProgressBar returns a 10-char Unicode block bar with colors:
//   - filled blocks in blue, empty blocks in dim red.
func colorProgressBar(running, total int) string {
	const width = 10
	if total == 0 {
		return red(strings.Repeat("░", width))
	}
	filled := (running * width) / total
	return blue(strings.Repeat("█", filled)) + red(strings.Repeat("░", width-filled))
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

// podPhaseLabel returns a colored phase badge.
func podPhaseLabel(running, total int) string {
	if total == 0 {
		return red("⏳ waiting")
	}
	if running == total {
		return blue("✔ running")
	}
	pending := total - running
	return red(fmt.Sprintf("✖ %d pending", pending))
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

	fmt.Printf("\n    %s Kafka Cluster  %s %s\n",
		dim("╭─"), bold(fmt.Sprintf("(%s timeout)", fmtRemaining(timeout))), dim("─────────────────────────╮"))

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
		eoIcon := red("⏳")
		if eoReady {
			eoIcon = blue("✔")
		}

		// ── 4. Evaluate ────────────────────────────────────────────────────────
		allBrokersUp := pods.brokerTotal > 0 && pods.brokerRunning == pods.brokerTotal
		allCtrlUp := pods.ctrlTotal > 0 && pods.ctrlRunning == pods.ctrlTotal

		if crReady && allBrokersUp && allCtrlUp {
			// Final success block
			fmt.Printf("    %s  %s  [%s]  %s  %s\n",
				dim("│"), dim("Brokers     "),
				colorProgressBar(pods.brokerRunning, pods.brokerTotal),
				blue(fmt.Sprintf("%d/%d", pods.brokerRunning, pods.brokerTotal)),
				blue("✔ running"))
			fmt.Printf("    %s  %s  [%s]  %s  %s\n",
				dim("│"), dim("Controllers "),
				colorProgressBar(pods.ctrlRunning, pods.ctrlTotal),
				blue(fmt.Sprintf("%d/%d", pods.ctrlRunning, pods.ctrlTotal)),
				blue("✔ running"))
			fmt.Printf("    %s  %s  %s\n", dim("│"), dim("Entity Op   "), eoIcon)
			fmt.Printf("    %s  %s  %s\n", dim("│"), dim("CR status   "), blue("✔ Ready=True"))
			fmt.Printf("    %s\n", dim("╰──────────────────────────────────────────────────────────╯"))
			fmt.Printf("    %s Kafka ready  %s %s\n\n",
				green("✔"), dim("elapsed"), bold(fmtElapsed(elapsed)))
			return nil
		}

		// ── 5. Progress block ──────────────────────────────────────────────────
		remaining := time.Until(deadline)
		fmt.Printf("    %s  %s  [%s]  %s  %s\n",
			dim("│"), dim("Brokers     "),
			colorProgressBar(pods.brokerRunning, pods.brokerTotal),
			fmt.Sprintf("%d/%d", pods.brokerRunning, pods.brokerTotal),
			podPhaseLabel(pods.brokerRunning, pods.brokerTotal))
		fmt.Printf("    %s  %s  [%s]  %s  %s\n",
			dim("│"), dim("Controllers "),
			colorProgressBar(pods.ctrlRunning, pods.ctrlTotal),
			fmt.Sprintf("%d/%d", pods.ctrlRunning, pods.ctrlTotal),
			podPhaseLabel(pods.ctrlRunning, pods.ctrlTotal))
		fmt.Printf("    %s  %s  %s          %s %s  %s %s\n",
			dim("│"), dim("Entity Op   "), eoIcon,
			dim("elapsed"), fmtElapsed(elapsed),
			dim("remaining"), fmtRemaining(remaining))
		fmt.Printf("    %s\n", dim("│"))

		// ── 6. Pending pods hint (once, after 30 s) ────────────────────────────
		if !hintShown && elapsed >= 30 && len(pods.pendingPods) > 0 {
			hintShown = true
			fmt.Printf("    %s  %s\n", dim("│"), amber(fmt.Sprintf("⚠  %d pod(s) Pending — diagnose with:", len(pods.pendingPods))))
			fmt.Printf("    %s     %s\n", dim("│"), dim(fmt.Sprintf("kubectl describe pod %s -n %s", pods.pendingPods[0], namespace)))
			fmt.Printf("    %s\n", dim("│"))
		}

		// ── 7. Timeout ─────────────────────────────────────────────────────────
		if time.Now().After(deadline) {
			fmt.Printf("    %s\n", dim("╰──────────────────────────────────────────────────────────╯"))
			return fmt.Errorf("%s kafka not ready after %s (brokers:%d/%d controllers:%d/%d pending:%d)",
				red("✖"),
				timeout,
				pods.brokerRunning, pods.brokerTotal,
				pods.ctrlRunning, pods.ctrlTotal,
				len(pods.pendingPods))
		}

		// ── 8. Wait ────────────────────────────────────────────────────────────
		select {
		case <-ctx.Done():
			fmt.Printf("    %s\n", dim("╰──────────────────────────────────────────────────────────╯"))
			return ctx.Err()
		case <-time.After(poll):
			elapsed += int(poll.Seconds())
		}
	}
}
