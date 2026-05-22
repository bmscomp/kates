package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
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
// how many are fully Ready (all containers passing readiness probes) vs total.
//
// We check the pod-level Ready condition (True only when ALL containers are
// ready) rather than just the pod phase, because a broker in phase "Running"
// with 0/1 containers ready is NOT serving traffic.
func countPodsByLabel(ctx context.Context, namespace, selector string) (running, total int, pending []string) {
	out, _ := exec.CommandContext(ctx,
		"kubectl", "get", "pods",
		"-n", namespace,
		"-l", selector,
		"--no-headers",
		"-o", "custom-columns=NAME:.metadata.name,READY:.status.conditions[?(@.type==\"Ready\")].status,PHASE:.status.phase",
	).Output()

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		readyCond := ""
		phase := ""
		if len(fields) >= 3 {
			readyCond = fields[1] // "True" or "False" or "<none>"
			phase = fields[2]
		} else {
			// Only 2 fields — Ready condition might be missing
			phase = fields[1]
		}

		total++
		if readyCond == "True" {
			running++
		} else if phase == "Pending" || phase == "ContainerCreating" {
			pending = append(pending, name)
		} else {
			// Running but not ready (e.g., 0/1) — still counts as pending
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

// ── Progress Bar & Display Helpers ──────────────────────────────────────────

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

// boxTop returns the top border of a box.
func boxTop(title string, insideWidth int) string {
	visibleLen := runewidth.StringWidth(stripANSI(title))
	dashes := insideWidth - visibleLen - 1
	if dashes < 0 {
		dashes = 0
	}
	return dim("╭─") + title + dim(strings.Repeat("─", dashes) + "╮")
}

// boxBottom returns the bottom border of a box.
func boxBottom(insideWidth int) string {
	return dim("╰" + strings.Repeat("─", insideWidth) + "╯")
}

// boxRow pads the content to insideWidth and wraps it in left and right borders.
func boxRow(content string, insideWidth int) string {
	visibleLen := runewidth.StringWidth(stripANSI(content))
	if visibleLen < insideWidth {
		return dim("│") + content + strings.Repeat(" ", insideWidth-visibleLen) + dim("│")
	}
	return dim("│") + content + dim("│")
}

const (
	charFilled   = "▰"
	charUnfilled = "▱"
)

// renderProgressBar returns a customizable width Unicode block bar with adaptive colors:
//   - When not failed, filled blocks are in blue, empty blocks in dim gray.
//   - When failed, the entire bar is shown in red.
func renderProgressBar(running, total int, width int, failed bool) string {
	if total <= 0 {
		if failed {
			return red(strings.Repeat(charFilled, width))
		}
		return dim(strings.Repeat(charUnfilled, width))
	}
	filled := (running * width) / total
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	if failed {
		return red(strings.Repeat(charFilled, filled) + strings.Repeat(charUnfilled, width-filled))
	}
	return blue(strings.Repeat(charFilled, filled)) + dim(strings.Repeat(charUnfilled, width-filled))
}

// colorProgressBar is a backward-compatible wrapper for renderProgressBar.
func colorProgressBar(running, total int) string {
	return renderProgressBar(running, total, 15, false)
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
	totalSecs := int(timeout.Seconds())

	fmt.Printf("\n    %s\n", boxTop(fmt.Sprintf(" Kafka Cluster  %s ", bold(fmt.Sprintf("(%s timeout)", fmtRemaining(timeout)))), 58))

	lastPrintedLines := 0

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

		isTimedOut := time.Now().After(deadline)
		isCancelled := ctx.Err() != nil
		failed := isTimedOut || isCancelled

		if lastPrintedLines > 0 {
			fmt.Printf("\033[%dA", lastPrintedLines)
		}

		if crReady && allBrokersUp && allCtrlUp {
			// Final success block
			fmt.Printf("    %s\033[K\n", boxRow(fmt.Sprintf(" %s  [%s]  %s  %s",
				dim("Brokers     "),
				renderProgressBar(pods.brokerRunning, pods.brokerTotal, 15, false),
				blue(fmt.Sprintf("%-5s", fmt.Sprintf("%d/%d", pods.brokerRunning, pods.brokerTotal))),
				blue("✔ running")), 58))
			fmt.Printf("    %s\033[K\n", boxRow(fmt.Sprintf(" %s  [%s]  %s  %s",
				dim("Controllers "),
				renderProgressBar(pods.ctrlRunning, pods.ctrlTotal, 15, false),
				blue(fmt.Sprintf("%-5s", fmt.Sprintf("%d/%d", pods.ctrlRunning, pods.ctrlTotal))),
				blue("✔ running")), 58))
			fmt.Printf("    %s\033[K\n", boxRow(fmt.Sprintf(" %s  [%s]  %s / %s",
				dim("Timeout     "),
				renderProgressBar(totalSecs, totalSecs, 15, false),
				blue(fmtElapsed(elapsed)), blue(fmtElapsed(totalSecs))), 58))
			fmt.Printf("    %s\033[K\n", boxRow(fmt.Sprintf(" %s  %s", dim("Entity Op   "), eoIcon), 58))
			fmt.Printf("    %s\033[K\n", boxRow(fmt.Sprintf(" %s  %s", dim("CR status   "), blue("✔ Ready=True")), 58))
			fmt.Printf("    %s\033[K\n", boxBottom(58))
			fmt.Printf("    %s Kafka ready  %s %s\033[K\n\n",
				green("✔"), dim("elapsed"), bold(fmtElapsed(elapsed)))
			return nil
		}

		// ── 5. Progress block ──────────────────────────────────────────────────
		remaining := time.Until(deadline)
		if remaining < 0 {
			remaining = 0
		}
		
		lines := 0
		fmt.Printf("    %s\033[K\n", boxRow(fmt.Sprintf(" %s  [%s]  %s  %s",
			dim("Brokers     "),
			renderProgressBar(pods.brokerRunning, pods.brokerTotal, 15, failed),
			fmt.Sprintf("%-5s", fmt.Sprintf("%d/%d", pods.brokerRunning, pods.brokerTotal)),
			podPhaseLabel(pods.brokerRunning, pods.brokerTotal)), 58))
		lines++
		fmt.Printf("    %s\033[K\n", boxRow(fmt.Sprintf(" %s  [%s]  %s  %s",
			dim("Controllers "),
			renderProgressBar(pods.ctrlRunning, pods.ctrlTotal, 15, failed),
			fmt.Sprintf("%-5s", fmt.Sprintf("%d/%d", pods.ctrlRunning, pods.ctrlTotal)),
			podPhaseLabel(pods.ctrlRunning, pods.ctrlTotal)), 58))
		lines++
		fmt.Printf("    %s\033[K\n", boxRow(fmt.Sprintf(" %s  [%s]  %s / %s",
			dim("Timeout     "),
			renderProgressBar(elapsed, totalSecs, 15, failed),
			fmtElapsed(elapsed), fmtElapsed(totalSecs)), 58))
		lines++
		fmt.Printf("    %s\033[K\n", boxRow(fmt.Sprintf(" %s  %s",
			dim("Entity Op   "), eoIcon), 58))
		lines++

		// ── 6. Pending pods hint (after 30 s) ────────────────────────────
		if elapsed >= 30 && len(pods.pendingPods) > 0 {
			fmt.Printf("    %s\033[K\n", boxRow("", 58))
			lines++
			fmt.Printf("    %s\033[K\n", boxRow(fmt.Sprintf(" %s", amber(fmt.Sprintf("⚠  %d pod(s) Pending — diagnose with:", len(pods.pendingPods)))), 58))
			lines++
			fmt.Printf("    %s\033[K\n", boxRow(fmt.Sprintf("    %s", dim(fmt.Sprintf("kubectl describe pod %s -n %s", pods.pendingPods[0], namespace))), 58))
			lines++
		}

		// Always print the bottom border of the box
		fmt.Printf("    %s\033[K\n", boxBottom(58))
		lines++
		
		// Clear any leftover lines from a previous taller frame
		if lastPrintedLines > lines {
			for i := lines; i < lastPrintedLines; i++ {
				fmt.Printf("\033[K\n")
			}
			fmt.Printf("\033[%dA", lastPrintedLines-lines)
		}

		// ── 6b. Early exit on unrecoverable storage errors ────────────────────
		if elapsed > 30 && len(pods.pendingPods) > 0 {
			evOut, _ := exec.CommandContext(ctx,
				"kubectl", "get", "events", "-n", namespace,
				"--field-selector=reason=FailedScheduling",
				"--no-headers", "-o", "custom-columns=MSG:.message",
			).Output()
			if strings.Contains(string(evOut), "unbound immediate PersistentVolumeClaims") {
				fmt.Printf("\033[%dA", lines) // Re-render in place
				fmt.Printf("    %s\033[K\n", boxRow(fmt.Sprintf(" %s  [%s]  %s  %s",
					dim("Brokers     "),
					renderProgressBar(pods.brokerRunning, pods.brokerTotal, 15, true),
					red(fmt.Sprintf("%-5s", fmt.Sprintf("%d/%d", pods.brokerRunning, pods.brokerTotal))),
					red("✖ failed")), 58))
				fmt.Printf("    %s\033[K\n", boxRow(fmt.Sprintf(" %s  [%s]  %s  %s",
					dim("Controllers "),
					renderProgressBar(pods.ctrlRunning, pods.ctrlTotal, 15, true),
					red(fmt.Sprintf("%-5s", fmt.Sprintf("%d/%d", pods.ctrlRunning, pods.ctrlTotal))),
					red("✖ failed")), 58))
				fmt.Printf("    %s\033[K\n", boxRow(fmt.Sprintf(" %s  [%s]  %s / %s",
					dim("Timeout     "),
					renderProgressBar(elapsed, totalSecs, 15, true),
					red(fmtElapsed(elapsed)), red(fmtElapsed(totalSecs))), 58))
				fmt.Printf("    %s\033[K\n", boxBottom(58))
				return fmt.Errorf(
					"pods stuck Pending: PVCs unbound (StorageClass likely using Immediate mode — check kind_storage.go)\n" +
					"Fix: kubectl delete pvc -n %s --all && kubectl delete kafka/krafter -n %s --ignore-not-found",
					namespace, namespace,
				)
			}
		}

		// ── 7. Timeout ─────────────────────────────────────────────────────────
		if time.Now().After(deadline) {
			fmt.Printf("\033[%dA", lines) // Re-render in place
			fmt.Printf("    %s\033[K\n", boxRow(fmt.Sprintf(" %s  [%s]  %s  %s",
				dim("Brokers     "),
				renderProgressBar(pods.brokerRunning, pods.brokerTotal, 15, true),
				red(fmt.Sprintf("%-5s", fmt.Sprintf("%d/%d", pods.brokerRunning, pods.brokerTotal))),
				red("✖ timed out")), 58))
			fmt.Printf("    %s\033[K\n", boxRow(fmt.Sprintf(" %s  [%s]  %s  %s",
				dim("Controllers "),
				renderProgressBar(pods.ctrlRunning, pods.ctrlTotal, 15, true),
				red(fmt.Sprintf("%-5s", fmt.Sprintf("%d/%d", pods.ctrlRunning, pods.ctrlTotal))),
				red("✖ timed out")), 58))
			fmt.Printf("    %s\033[K\n", boxRow(fmt.Sprintf(" %s  [%s]  %s / %s",
				dim("Timeout     "),
				renderProgressBar(totalSecs, totalSecs, 15, true),
				red(fmtElapsed(totalSecs)), red(fmtElapsed(totalSecs))), 58))
			fmt.Printf("    %s\033[K\n", boxBottom(58))
			return fmt.Errorf("%s kafka not ready after %s (brokers:%d/%d controllers:%d/%d pending:%d)",
				red("✖"),
				timeout,
				pods.brokerRunning, pods.brokerTotal,
				pods.ctrlRunning, pods.ctrlTotal,
				len(pods.pendingPods))
		}

		lastPrintedLines = lines

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
