package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ── Bubble Tea: Kafka Connect ──────────────────────────────────────────────

type connectStatusMsg struct {
	crReady    bool
	podRunning int
	podTotal   int
	pending    []string
	err        error
}

func pollConnectStatus(ctx context.Context, namespace string) tea.Cmd {
	return func() tea.Msg {
		var s connectStatusMsg

		// 1. CR condition
		condOut, _ := exec.CommandContext(ctx,
			"kubectl", "get", "kafkaconnect", "-n", namespace,
			"--no-headers",
			"-o", `custom-columns=READY:.status.conditions[?(@.type=="Ready")].status`,
		).Output()
		s.crReady = strings.Contains(string(condOut), "True")

		// 2. Pod counts
		s.podRunning, s.podTotal, s.pending = countPodsByLabel(ctx, namespace,
			"strimzi.io/name=krafter-connect-connect")

		return s
	}
}

type connectWaitModel struct {
	ctx       context.Context
	namespace string
	timeout   time.Duration
	start     time.Time

	podRunning int
	podTotal   int
	pending    []string
	crReady    bool
	err        error
	done       bool
}

func (m connectWaitModel) Init() tea.Cmd {
	m.start = time.Now()
	return tea.Batch(
		pollConnectStatus(m.ctx, m.namespace),
		tea.Tick(6*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }),
	)
}

func (m connectWaitModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.err = fmt.Errorf("user aborted")
			return m, tea.Quit
		}
	case connectStatusMsg:
		m.podRunning = msg.podRunning
		m.podTotal = msg.podTotal
		m.pending = msg.pending
		m.crReady = msg.crReady

		allPodsUp := m.podTotal > 0 && m.podRunning == m.podTotal
		if m.crReady && allPodsUp {
			m.done = true
			return m, tea.Quit
		}

		elapsed := int(time.Since(m.start).Seconds())
		if elapsed >= int(m.timeout.Seconds()) {
			m.err = fmt.Errorf("%s kafka connect not ready after %s (pods:%d/%d)",
				red("✖"), m.timeout, m.podRunning, m.podTotal)
			return m, tea.Quit
		}

	case tickMsg:
		elapsed := int(time.Since(m.start).Seconds())
		if elapsed >= int(m.timeout.Seconds()) {
			m.err = fmt.Errorf("%s kafka connect not ready after %s (pods:%d/%d)",
				red("✖"), m.timeout, m.podRunning, m.podTotal)
			return m, tea.Quit
		}
		return m, tea.Batch(
			pollConnectStatus(m.ctx, m.namespace),
			tea.Tick(6*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }),
		)
	}
	return m, nil
}

func (m connectWaitModel) View() string {
	var b strings.Builder

	elapsed := int(time.Since(m.start).Seconds())
	totalSecs := int(m.timeout.Seconds())
	if elapsed > totalSecs {
		elapsed = totalSecs
	}

	failed := m.err != nil

	b.WriteString(fmt.Sprintf("\n    %s\n", boxTop(fmt.Sprintf(" Kafka Connect  %s ", bold(fmt.Sprintf("(%s timeout)", fmtRemaining(m.timeout)))), 58)))

	if m.done {
		b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  [%s]  %s  %s",
			dim("Workers     "),
			renderProgressBar(m.podRunning, m.podTotal, 15, false),
			blue(fmt.Sprintf("%-5s", fmt.Sprintf("%d/%d", m.podRunning, m.podTotal))),
			blue("✔ running")), 58)))
		b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  [%s]  %s / %s",
			dim("Timeout     "),
			renderProgressBar(elapsed, totalSecs, 15, false),
			blue(fmtElapsed(elapsed)), blue(fmtElapsed(totalSecs))), 58)))
		b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  %s", dim("CR status   "), blue("✔ Ready=True")), 58)))
		b.WriteString(fmt.Sprintf("    %s\n", boxBottom(58)))
		b.WriteString(fmt.Sprintf("    %s Kafka Connect ready  %s %s\n",
			green("✔"), dim("elapsed"), bold(fmtElapsed(elapsed))))
		return b.String()
	}

	crIcon := red("⏳ waiting")
	if m.crReady {
		crIcon = blue("✔ Ready=True")
	}

	b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  [%s]  %s  %s",
		dim("Workers     "),
		renderProgressBar(m.podRunning, m.podTotal, 15, failed),
		fmt.Sprintf("%-5s", fmt.Sprintf("%d/%d", m.podRunning, m.podTotal)),
		podPhaseLabel(m.podRunning, m.podTotal)), 58)))

	b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  [%s]  %s / %s",
		dim("Timeout     "),
		renderProgressBar(elapsed, totalSecs, 15, failed),
		fmtElapsed(elapsed), fmtElapsed(totalSecs)), 58)))

	b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  %s",
		dim("CR status   "), crIcon), 58)))

	b.WriteString(fmt.Sprintf("    %s\n", boxBottom(58)))
	return b.String()
}

func waitConnectReady(ctx context.Context, namespace string, timeout time.Duration) error {
	m := connectWaitModel{
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

	fm := finalModel.(connectWaitModel)
	if fm.err != nil {
		return fm.err
	}

	return nil
}

// ── Bubble Tea: Kafka Connectors ───────────────────────────────────────────

type connectorStat struct {
	name  string
	ready bool
}

type connectorsStatusMsg struct {
	connectors []connectorStat
	total      int
	ready      int
	err        error
}

func pollConnectors(ctx context.Context, namespace string) tea.Cmd {
	return func() tea.Msg {
		var msg connectorsStatusMsg
		out, _ := exec.CommandContext(ctx,
			"kubectl", "get", "kafkaconnector",
			"-n", namespace,
			"--no-headers",
			"-o", `custom-columns=NAME:.metadata.name,READY:.status.conditions[?(@.type=="Ready")].status`,
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
			msg.connectors = append(msg.connectors, connectorStat{name: fields[0], ready: isReady})
		}

		sort.Slice(msg.connectors, func(i, j int) bool {
			return msg.connectors[i].name < msg.connectors[j].name
		})

		return msg
	}
}

type connectorWaitModel struct {
	ctx       context.Context
	namespace string
	timeout   time.Duration
	start     time.Time

	connectors []connectorStat
	total      int
	ready      int
	err        error
	done       bool
	isTimeout  bool
}

func (m connectorWaitModel) Init() tea.Cmd {
	m.start = time.Now()
	return tea.Batch(
		pollConnectors(m.ctx, m.namespace),
		tea.Tick(6*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }),
	)
}

func (m connectorWaitModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.err = fmt.Errorf("user aborted")
			return m, tea.Quit
		}
	case connectorsStatusMsg:
		m.connectors = msg.connectors
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
			pollConnectors(m.ctx, m.namespace),
			tea.Tick(6*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }),
		)
	}
	return m, nil
}

func (m connectorWaitModel) View() string {
	var b strings.Builder

	elapsed := int(time.Since(m.start).Seconds())
	totalSecs := int(m.timeout.Seconds())
	if elapsed > totalSecs {
		elapsed = totalSecs
	}

	failed := m.err != nil || m.isTimeout

	b.WriteString(fmt.Sprintf("\n    %s\n", boxTop(fmt.Sprintf(" Kafka Connectors  %s ", bold(fmt.Sprintf("(%s timeout)", fmtRemaining(m.timeout)))), 58)))

	if m.done {
		if m.isTimeout {
			b.WriteString(m.renderConnectors(true))
			b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  [%s]  %s / %s",
				dim(fmt.Sprintf("%-12s", "Timeout")),
				renderProgressBar(totalSecs, totalSecs, 15, true),
				red(fmtElapsed(totalSecs)), red(fmtElapsed(totalSecs))), 58)))
			b.WriteString(fmt.Sprintf("    %s\n", boxBottom(58)))
			return b.String()
		}

		b.WriteString(m.renderConnectors(false))
		b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  [%s]  %s / %s",
			dim(fmt.Sprintf("%-12s", "Timeout")),
			renderProgressBar(elapsed, totalSecs, 15, false),
			blue(fmtElapsed(elapsed)), blue(fmtElapsed(totalSecs))), 58)))
		b.WriteString(fmt.Sprintf("    %s\n", boxBottom(58)))
		b.WriteString(fmt.Sprintf("    %s All %d connectors ready  %s %s\n\n",
			green("✔"), m.total, dim("elapsed"), bold(fmtElapsed(elapsed))))
		return b.String()
	}

	b.WriteString(m.renderConnectors(failed))
	b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  [%s]  %s / %s",
		dim(fmt.Sprintf("%-12s", "Timeout")),
		renderProgressBar(elapsed, totalSecs, 15, failed),
		fmtElapsed(elapsed), fmtElapsed(totalSecs)), 58)))
	b.WriteString(fmt.Sprintf("    %s\n", boxBottom(58)))

	return b.String()
}

func (m connectorWaitModel) renderConnectors(finalFailed bool) string {
	var b strings.Builder
	if len(m.connectors) == 0 {
		b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s", dim("Waiting for KafkaConnectors to appear...")), 58)))
		return b.String()
	}
	for _, c := range m.connectors {
		namePadded := fmt.Sprintf("%-12s", c.name)
		if len(c.name) > 12 {
			namePadded = c.name[:9] + "..."
		}

		statusStr := red("pending")
		progress := 0
		if c.ready {
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
			renderProgressBar(progress, 1, 15, finalFailed && !c.ready),
			fmt.Sprintf("%-5s", fmt.Sprintf("%d/%d", progress, 1)),
			statusStr), 58)))
	}
	return b.String()
}

func waitConnectorReady(ctx context.Context, namespace string, timeout time.Duration) error {
	m := connectorWaitModel{
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

	fm := finalModel.(connectorWaitModel)
	if fm.err != nil {
		return fm.err
	}

	if fm.isTimeout {
		return fmt.Errorf("connectors not all ready after %s (%d/%d ready)", timeout, fm.ready, fm.total)
	}

	return nil
}

// ── Bubble Tea: PostgreSQL ─────────────────────────────────────────────────

type pgStatusMsg struct {
	podRunning int
	podTotal   int
	pending    []string
	err        error
}

func pollPostgresStatus(ctx context.Context, namespace string) tea.Cmd {
	return func() tea.Msg {
		var s pgStatusMsg
		s.podRunning, s.podTotal, s.pending = countPodsByLabel(ctx, namespace,
			"app.kubernetes.io/name=postgresql")
		return s
	}
}

type pgWaitModel struct {
	ctx       context.Context
	namespace string
	timeout   time.Duration
	start     time.Time

	podRunning int
	podTotal   int
	pending    []string
	err        error
	done       bool
}

func (m pgWaitModel) Init() tea.Cmd {
	m.start = time.Now()
	return tea.Batch(
		pollPostgresStatus(m.ctx, m.namespace),
		tea.Tick(6*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }),
	)
}

func (m pgWaitModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.err = fmt.Errorf("user aborted")
			return m, tea.Quit
		}
	case pgStatusMsg:
		m.podRunning = msg.podRunning
		m.podTotal = msg.podTotal
		m.pending = msg.pending

		if m.podTotal > 0 && m.podRunning == m.podTotal {
			m.done = true
			return m, tea.Quit
		}

		elapsed := int(time.Since(m.start).Seconds())
		if elapsed >= int(m.timeout.Seconds()) {
			m.err = fmt.Errorf("%s postgresql not ready after %s (pods:%d/%d)",
				red("✖"), m.timeout, m.podRunning, m.podTotal)
			return m, tea.Quit
		}

	case tickMsg:
		elapsed := int(time.Since(m.start).Seconds())
		if elapsed >= int(m.timeout.Seconds()) {
			m.err = fmt.Errorf("%s postgresql not ready after %s (pods:%d/%d)",
				red("✖"), m.timeout, m.podRunning, m.podTotal)
			return m, tea.Quit
		}
		return m, tea.Batch(
			pollPostgresStatus(m.ctx, m.namespace),
			tea.Tick(6*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }),
		)
	}
	return m, nil
}

func (m pgWaitModel) View() string {
	var b strings.Builder

	elapsed := int(time.Since(m.start).Seconds())
	totalSecs := int(m.timeout.Seconds())
	if elapsed > totalSecs {
		elapsed = totalSecs
	}

	failed := m.err != nil

	b.WriteString(fmt.Sprintf("\n    %s\n", boxTop(fmt.Sprintf(" PostgreSQL  %s ", bold(fmt.Sprintf("(%s timeout)", fmtRemaining(m.timeout)))), 58)))

	if m.done {
		b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  [%s]  %s  %s",
			dim("Pods        "),
			renderProgressBar(m.podRunning, m.podTotal, 15, false),
			blue(fmt.Sprintf("%-5s", fmt.Sprintf("%d/%d", m.podRunning, m.podTotal))),
			blue("✔ running")), 58)))
		b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  [%s]  %s / %s",
			dim("Timeout     "),
			renderProgressBar(elapsed, totalSecs, 15, false),
			blue(fmtElapsed(elapsed)), blue(fmtElapsed(totalSecs))), 58)))
		b.WriteString(fmt.Sprintf("    %s\n", boxBottom(58)))
		b.WriteString(fmt.Sprintf("    %s PostgreSQL ready  %s %s\n",
			green("✔"), dim("elapsed"), bold(fmtElapsed(elapsed))))
		return b.String()
	}

	b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  [%s]  %s  %s",
		dim("Pods        "),
		renderProgressBar(m.podRunning, m.podTotal, 15, failed),
		fmt.Sprintf("%-5s", fmt.Sprintf("%d/%d", m.podRunning, m.podTotal)),
		podPhaseLabel(m.podRunning, m.podTotal)), 58)))

	b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  [%s]  %s / %s",
		dim("Timeout     "),
		renderProgressBar(elapsed, totalSecs, 15, failed),
		fmtElapsed(elapsed), fmtElapsed(totalSecs)), 58)))

	b.WriteString(fmt.Sprintf("    %s\n", boxBottom(58)))
	return b.String()
}

func waitPostgresReady(ctx context.Context, namespace string, timeout time.Duration) error {
	m := pgWaitModel{
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

	fm := finalModel.(pgWaitModel)
	if fm.err != nil {
		return fm.err
	}

	return nil
}
