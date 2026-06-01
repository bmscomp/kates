package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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
			readyCond = fields[1]
			phase = fields[2]
		} else {
			phase = fields[1]
		}

		total++
		if readyCond == "True" {
			running++
		} else if phase == "Pending" || phase == "ContainerCreating" {
			pending = append(pending, name)
		} else {
			pending = append(pending, name)
		}
	}
	return
}

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

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

func boxTop(title string, insideWidth int) string {
	visibleLen := runewidth.StringWidth(stripANSI(title))
	dashes := insideWidth - visibleLen - 1
	if dashes < 0 {
		dashes = 0
	}
	return dim("╭─") + title + dim(strings.Repeat("─", dashes) + "╮")
}

func boxBottom(insideWidth int) string {
	return dim("╰" + strings.Repeat("─", insideWidth) + "╯")
}

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
	if running > 0 && filled == 0 {
		filled = 1
	}
	if failed {
		return red(strings.Repeat(charFilled, filled) + strings.Repeat(charUnfilled, width-filled))
	}
	return blue(strings.Repeat(charFilled, filled)) + dim(strings.Repeat(charUnfilled, width-filled))
}

func colorProgressBar(running, total int) string {
	return renderProgressBar(running, total, 15, false)
}

func fmtElapsed(secs int) string {
	return fmt.Sprintf("%d:%02d", secs/60, secs%60)
}

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

// ── Bubble Tea Implementation ──────────────────────────────────────────────

type statusMsg struct {
	crReady  bool
	eoReady  bool
	pods     kafkaPodStatus
	pvcError bool
	err      error
}

type tickMsg time.Time

func pollKafkaStatus(ctx context.Context, namespace string, elapsed int) tea.Cmd {
	return func() tea.Msg {
		var s statusMsg
		// 1. CR condition
		condOut, _ := exec.CommandContext(ctx,
			"kubectl", "get", "kafka/krafter", "-n", namespace,
			"-o", `jsonpath={range .status.conditions[*]}{.type}={.status} {end}`,
		).Output()
		s.crReady = strings.Contains(string(condOut), "Ready=True")

		// 2. Pod counts
		s.pods = pollKafkaPods(ctx, namespace)

		// 3. Entity Operator
		eoOut, _ := exec.CommandContext(ctx,
			"kubectl", "get", "pods", "-n", namespace,
			"-l", "app.kubernetes.io/name=entity-operator",
			"--no-headers",
			"-o", "custom-columns=PHASE:.status.phase",
		).Output()
		s.eoReady = strings.Contains(string(eoOut), "Running")

		// 4. PVC Error
		if elapsed > 30 && len(s.pods.pendingPods) > 0 {
			evOut, _ := exec.CommandContext(ctx,
				"kubectl", "get", "events", "-n", namespace,
				"--field-selector=reason=FailedScheduling",
				"--no-headers", "-o", "custom-columns=MSG:.message",
			).Output()
			if strings.Contains(string(evOut), "unbound immediate PersistentVolumeClaims") {
				s.pvcError = true
			}
		}

		return s
	}
}

type kafkaWaitModel struct {
	ctx       context.Context
	namespace string
	timeout   time.Duration
	start     time.Time
	
	pods      kafkaPodStatus
	crReady   bool
	eoReady   bool
	pvcError  bool
	err       error
	done      bool
}

func (m kafkaWaitModel) Init() tea.Cmd {
	m.start = time.Now()
	return tea.Batch(
		pollKafkaStatus(m.ctx, m.namespace, 0),
		tea.Tick(6*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }),
	)
}

func (m kafkaWaitModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.err = fmt.Errorf("user aborted")
			return m, tea.Quit
		}
	case statusMsg:
		m.pods = msg.pods
		m.crReady = msg.crReady
		m.eoReady = msg.eoReady
		
		elapsed := int(time.Since(m.start).Seconds())
		
		if msg.pvcError {
			m.pvcError = true
			m.err = fmt.Errorf(
				"pods stuck Pending: PVCs unbound (StorageClass likely using Immediate mode — check kind_storage.go)\n" +
				"Fix: kubectl delete pvc -n %s --all && kubectl delete kafka/krafter -n %s --ignore-not-found",
				m.namespace, m.namespace,
			)
			return m, tea.Quit
		}

		allBrokersUp := m.pods.brokerTotal > 0 && m.pods.brokerRunning == m.pods.brokerTotal
		allCtrlUp := m.pods.ctrlTotal > 0 && m.pods.ctrlRunning == m.pods.ctrlTotal

		if m.crReady && allBrokersUp && allCtrlUp {
			m.done = true
			return m, tea.Quit
		}
		
		if elapsed >= int(m.timeout.Seconds()) {
			m.err = fmt.Errorf("%s kafka not ready after %s (brokers:%d/%d controllers:%d/%d pending:%d)",
				red("✖"),
				m.timeout,
				m.pods.brokerRunning, m.pods.brokerTotal,
				m.pods.ctrlRunning, m.pods.ctrlTotal,
				len(m.pods.pendingPods))
			return m, tea.Quit
		}

	case tickMsg:
		elapsed := int(time.Since(m.start).Seconds())
		if elapsed >= int(m.timeout.Seconds()) {
			m.err = fmt.Errorf("%s kafka not ready after %s (brokers:%d/%d controllers:%d/%d pending:%d)",
				red("✖"),
				m.timeout,
				m.pods.brokerRunning, m.pods.brokerTotal,
				m.pods.ctrlRunning, m.pods.ctrlTotal,
				len(m.pods.pendingPods))
			return m, tea.Quit
		}
		return m, tea.Batch(
			pollKafkaStatus(m.ctx, m.namespace, elapsed),
			tea.Tick(6*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }),
		)
	}
	return m, nil
}

func (m kafkaWaitModel) View() string {
	var b strings.Builder

	elapsed := int(time.Since(m.start).Seconds())
	totalSecs := int(m.timeout.Seconds())
	if elapsed > totalSecs {
		elapsed = totalSecs
	}

	failed := m.err != nil

	b.WriteString(fmt.Sprintf("\n    %s\n", boxTop(fmt.Sprintf(" Kafka Cluster  %s ", bold(fmt.Sprintf("(%s timeout)", fmtRemaining(m.timeout)))), 58)))

	if m.done {
		b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  [%s]  %s  %s",
			dim("Brokers     "),
			renderProgressBar(m.pods.brokerRunning, m.pods.brokerTotal, 15, false),
			blue(fmt.Sprintf("%-5s", fmt.Sprintf("%d/%d", m.pods.brokerRunning, m.pods.brokerTotal))),
			blue("✔ running")), 58)))
		b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  [%s]  %s  %s",
			dim("Controllers "),
			renderProgressBar(m.pods.ctrlRunning, m.pods.ctrlTotal, 15, false),
			blue(fmt.Sprintf("%-5s", fmt.Sprintf("%d/%d", m.pods.ctrlRunning, m.pods.ctrlTotal))),
			blue("✔ running")), 58)))
		b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  [%s]  %s / %s",
			dim("Timeout     "),
			renderProgressBar(elapsed, totalSecs, 15, false),
			blue(fmtElapsed(elapsed)), blue(fmtElapsed(totalSecs))), 58)))
		
		eoIcon := blue("✔")
		b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  %s", dim("Entity Op   "), eoIcon), 58)))
		b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  %s", dim("CR status   "), blue("✔ Ready=True")), 58)))
		b.WriteString(fmt.Sprintf("    %s\n", boxBottom(58)))
		b.WriteString(fmt.Sprintf("    %s Kafka ready  %s %s\n",
			green("✔"), dim("elapsed"), bold(fmtElapsed(elapsed))))
		return b.String()
	}

	eoIcon := red("⏳")
	if m.eoReady {
		eoIcon = blue("✔")
	}

	b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  [%s]  %s  %s",
		dim("Brokers     "),
		renderProgressBar(m.pods.brokerRunning, m.pods.brokerTotal, 15, failed),
		fmt.Sprintf("%-5s", fmt.Sprintf("%d/%d", m.pods.brokerRunning, m.pods.brokerTotal)),
		podPhaseLabel(m.pods.brokerRunning, m.pods.brokerTotal)), 58)))

	b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  [%s]  %s  %s",
		dim("Controllers "),
		renderProgressBar(m.pods.ctrlRunning, m.pods.ctrlTotal, 15, failed),
		fmt.Sprintf("%-5s", fmt.Sprintf("%d/%d", m.pods.ctrlRunning, m.pods.ctrlTotal)),
		podPhaseLabel(m.pods.ctrlRunning, m.pods.ctrlTotal)), 58)))

	b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  [%s]  %s / %s",
		dim("Timeout     "),
		renderProgressBar(elapsed, totalSecs, 15, failed),
		fmtElapsed(elapsed), fmtElapsed(totalSecs)), 58)))

	b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  %s",
		dim("Entity Op   "), eoIcon), 58)))

	if elapsed >= 30 && len(m.pods.pendingPods) > 0 {
		b.WriteString(fmt.Sprintf("    %s\n", boxRow("", 58)))
		b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s", amber(fmt.Sprintf("⚠  %d pod(s) Pending — diagnose with:", len(m.pods.pendingPods)))), 58)))
		b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf("    %s", dim(fmt.Sprintf("kubectl describe pod %s -n %s", m.pods.pendingPods[0], m.namespace))), 58)))
	}

	b.WriteString(fmt.Sprintf("    %s\n", boxBottom(58)))
	return b.String()
}

func waitKafkaReady(ctx context.Context, namespace string, timeout time.Duration) error {
	m := kafkaWaitModel{
		ctx:       ctx,
		namespace: namespace,
		timeout:   timeout,
		start:     time.Now(),
	}
	
	p := tea.NewProgram(m)
	finalModel, err := p.Run()
	if err != nil {
		return err
	}
	
	fm := finalModel.(kafkaWaitModel)
	if fm.err != nil {
		return fm.err
	}
	
	return nil
}

// ── Bubble Tea Implementation for Kafka Users ─────────────────────────────

type userStat struct {
	name  string
	ready bool
}

type usersStatusMsg struct {
	users []userStat
	total int
	ready int
	err   error
}

func pollKafkaUsers(ctx context.Context, namespace string) tea.Cmd {
	return func() tea.Msg {
		var msg usersStatusMsg
		out, _ := exec.CommandContext(ctx,
			"kubectl", "get", "kafkauser",
			"-n", namespace,
			"--no-headers",
			"-o", "custom-columns=NAME:.metadata.name,READY:.status.conditions[0].status",
		).Output()

		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line == "" {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 1 {
				continue
			}
			msg.total++
			isReady := len(fields) >= 2 && fields[1] == "True"
			if isReady {
				msg.ready++
			}
			msg.users = append(msg.users, userStat{name: fields[0], ready: isReady})
		}
		
		sort.Slice(msg.users, func(i, j int) bool {
			return msg.users[i].name < msg.users[j].name
		})

		return msg
	}
}

type kafkaUsersModel struct {
	ctx       context.Context
	namespace string
	timeout   time.Duration
	start     time.Time

	users   []userStat
	total   int
	ready   int
	err     error
	done      bool
	isTimeout bool
}

func (m kafkaUsersModel) Init() tea.Cmd {
	m.start = time.Now()
	return tea.Batch(
		pollKafkaUsers(m.ctx, m.namespace),
		tea.Tick(6*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }),
	)
}

func (m kafkaUsersModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.err = fmt.Errorf("user aborted")
			return m, tea.Quit
		}
	case usersStatusMsg:
		m.users = msg.users
		m.total = msg.total
		m.ready = msg.ready
		
		elapsed := int(time.Since(m.start).Seconds())
		
		if m.total > 0 && m.ready == m.total {
			m.done = true
			return m, tea.Quit
		}
		
		if elapsed >= int(m.timeout.Seconds()) {
			m.isTimeout = true
			m.done = true
			return m, tea.Quit
		}

	case tickMsg:
		elapsed := int(time.Since(m.start).Seconds())
		if elapsed >= int(m.timeout.Seconds()) {
			m.isTimeout = true
			m.done = true
			return m, tea.Quit
		}
		return m, tea.Batch(
			pollKafkaUsers(m.ctx, m.namespace),
			tea.Tick(6*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }),
		)
	}
	return m, nil
}

func (m kafkaUsersModel) View() string {
	var b strings.Builder

	elapsed := int(time.Since(m.start).Seconds())
	totalSecs := int(m.timeout.Seconds())
	if elapsed > totalSecs {
		elapsed = totalSecs
	}

	failed := m.err != nil || m.isTimeout

	b.WriteString(fmt.Sprintf("\n    %s\n", boxTop(fmt.Sprintf(" Kafka Users  %s ", bold(fmt.Sprintf("(%s timeout)", fmtRemaining(m.timeout)))), 58)))

	if m.done {
		if m.isTimeout {
			b.WriteString(m.renderUsers(true))
			b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  [%s]  %s / %s",
				dim(fmt.Sprintf("%-12s", "Timeout")),
				renderProgressBar(totalSecs, totalSecs, 15, true),
				red(fmtElapsed(totalSecs)), red(fmtElapsed(totalSecs))), 58)))
			b.WriteString(fmt.Sprintf("    %s\n", boxBottom(58)))
			return b.String()
		}
		
		b.WriteString(m.renderUsers(false))
		b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  [%s]  %s / %s",
			dim(fmt.Sprintf("%-12s", "Timeout")),
			renderProgressBar(elapsed, totalSecs, 15, false),
			blue(fmtElapsed(elapsed)), blue(fmtElapsed(totalSecs))), 58)))
		b.WriteString(fmt.Sprintf("    %s\n", boxBottom(58)))
		b.WriteString(fmt.Sprintf("    %s All %d KafkaUser credentials ready  %s %s\n\n",
			green("✔"), m.total, dim("elapsed"), bold(fmtElapsed(elapsed))))
		return b.String()
	}

	b.WriteString(m.renderUsers(failed))
	b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  [%s]  %s / %s",
		dim(fmt.Sprintf("%-12s", "Timeout")),
		renderProgressBar(elapsed, totalSecs, 15, failed),
		fmtElapsed(elapsed), fmtElapsed(totalSecs)), 58)))
	b.WriteString(fmt.Sprintf("    %s\n", boxBottom(58)))

	return b.String()
}

func (m kafkaUsersModel) renderUsers(finalFailed bool) string {
	var b strings.Builder
	if len(m.users) == 0 {
		b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s", dim("Waiting for KafkaUsers to be applied...")), 58)))
		return b.String()
	}
	for _, u := range m.users {
		namePadded := fmt.Sprintf("%-12s", u.name)
		if len(u.name) > 12 {
			namePadded = u.name[:9] + "..."
		}
		
		statusStr := red("pending")
		progress := 0
		if u.ready {
			statusStr = blue("ready")
			progress = 1
		} else if finalFailed {
			statusStr = red("failed")
			if m.isTimeout {
				statusStr = red("timed out")
			}
		}
		
		b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  [%s]  %s  %s",
			dim(namePadded),
			renderProgressBar(progress, 1, 15, finalFailed && !u.ready),
			fmt.Sprintf("%-5s", fmt.Sprintf("%d/%d", progress, 1)),
			statusStr), 58)))
	}
	return b.String()
}

func waitKafkaUsersReady(ctx context.Context, namespace string, timeout time.Duration) (bool, error) {
	m := kafkaUsersModel{
		ctx:       ctx,
		namespace: namespace,
		timeout:   timeout,
		start:     time.Now(),
	}
	
	p := tea.NewProgram(m)
	finalModel, err := p.Run()
	if err != nil {
		return false, err
	}
	
	fm := finalModel.(kafkaUsersModel)
	if fm.err != nil {
		return false, fm.err
	}
	
	// Return true if all users are ready, false if timed out
	return fm.total > 0 && fm.ready == fm.total, nil
}
