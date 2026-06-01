package cmd

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// ── Bubble Tea: Strimzi Operator ───────────────────────────────────────────

type strimziStatusMsg struct {
	podRunning int
	podTotal   int
	pending    []string
	crdReady   bool
	err        error
}

func pollStrimziStatus(ctx context.Context, namespace string) tea.Cmd {
	return func() tea.Msg {
		var s strimziStatusMsg

		// 1. Operator pod
		s.podRunning, s.podTotal, s.pending = countPodsByLabel(ctx, namespace,
			"name=strimzi-cluster-operator")

		// 2. CRD established
		crdOut, _ := exec.CommandContext(ctx,
			"kubectl", "get", "crd", "kafkas.kafka.strimzi.io",
			"-o", `jsonpath={.status.conditions[?(@.type=="Established")].status}`,
		).Output()
		s.crdReady = strings.TrimSpace(string(crdOut)) == "True"

		return s
	}
}

type strimziWaitModel struct {
	ctx       context.Context
	namespace string
	timeout   time.Duration
	start     time.Time

	podRunning int
	podTotal   int
	pending    []string
	crdReady   bool
	err        error
	done       bool
}

func (m strimziWaitModel) Init() tea.Cmd {
	m.start = time.Now()
	return tea.Batch(
		pollStrimziStatus(m.ctx, m.namespace),
		tea.Tick(6*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }),
	)
}

func (m strimziWaitModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.err = fmt.Errorf("user aborted")
			return m, tea.Quit
		}
	case strimziStatusMsg:
		m.podRunning = msg.podRunning
		m.podTotal = msg.podTotal
		m.pending = msg.pending
		m.crdReady = msg.crdReady

		allPodsUp := m.podTotal > 0 && m.podRunning == m.podTotal
		if allPodsUp && m.crdReady {
			m.done = true
			return m, tea.Quit
		}

		elapsed := int(time.Since(m.start).Seconds())
		if elapsed >= int(m.timeout.Seconds()) {
			m.err = fmt.Errorf("%s strimzi operator not ready after %s (pods:%d/%d crd:%v)",
				red("✖"), m.timeout, m.podRunning, m.podTotal, m.crdReady)
			return m, tea.Quit
		}

	case tickMsg:
		elapsed := int(time.Since(m.start).Seconds())
		if elapsed >= int(m.timeout.Seconds()) {
			m.err = fmt.Errorf("%s strimzi operator not ready after %s (pods:%d/%d crd:%v)",
				red("✖"), m.timeout, m.podRunning, m.podTotal, m.crdReady)
			return m, tea.Quit
		}
		return m, tea.Batch(
			pollStrimziStatus(m.ctx, m.namespace),
			tea.Tick(6*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }),
		)
	}
	return m, nil
}

func (m strimziWaitModel) View() string {
	var b strings.Builder

	elapsed := int(time.Since(m.start).Seconds())
	totalSecs := int(m.timeout.Seconds())
	if elapsed > totalSecs {
		elapsed = totalSecs
	}

	failed := m.err != nil

	b.WriteString(fmt.Sprintf("\n    %s\n", boxTop(fmt.Sprintf(" Strimzi Operator  %s ", bold(fmt.Sprintf("(%s timeout)", fmtRemaining(m.timeout)))), 58)))

	if m.done {
		b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  [%s]  %s  %s",
			dim("Operator    "),
			renderProgressBar(m.podRunning, m.podTotal, 15, false),
			blue(fmt.Sprintf("%-5s", fmt.Sprintf("%d/%d", m.podRunning, m.podTotal))),
			blue("✔ running")), 58)))
		b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  [%s]  %s / %s",
			dim("Timeout     "),
			renderProgressBar(elapsed, totalSecs, 15, false),
			blue(fmtElapsed(elapsed)), blue(fmtElapsed(totalSecs))), 58)))
		b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  %s", dim("CRDs        "), blue("✔ Established")), 58)))
		b.WriteString(fmt.Sprintf("    %s\n", boxBottom(58)))
		b.WriteString(fmt.Sprintf("    %s Strimzi Operator ready  %s %s\n",
			green("✔"), dim("elapsed"), bold(fmtElapsed(elapsed))))
		return b.String()
	}

	crdIcon := red("⏳ waiting")
	if m.crdReady {
		crdIcon = blue("✔ Established")
	}

	b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  [%s]  %s  %s",
		dim("Operator    "),
		renderProgressBar(m.podRunning, m.podTotal, 15, failed),
		fmt.Sprintf("%-5s", fmt.Sprintf("%d/%d", m.podRunning, m.podTotal)),
		podPhaseLabel(m.podRunning, m.podTotal)), 58)))

	b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  [%s]  %s / %s",
		dim("Timeout     "),
		renderProgressBar(elapsed, totalSecs, 15, failed),
		fmtElapsed(elapsed), fmtElapsed(totalSecs)), 58)))

	b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s  %s",
		dim("CRDs        "), crdIcon), 58)))

	if elapsed >= 30 && len(m.pending) > 0 {
		b.WriteString(fmt.Sprintf("    %s\n", boxRow("", 58)))
		b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf(" %s", amber(fmt.Sprintf("⚠  %d pod(s) Pending — diagnose with:", len(m.pending)))), 58)))
		b.WriteString(fmt.Sprintf("    %s\n", boxRow(fmt.Sprintf("    %s", dim(fmt.Sprintf("kubectl describe pod %s -n %s", m.pending[0], m.namespace))), 58)))
	}

	b.WriteString(fmt.Sprintf("    %s\n", boxBottom(58)))
	return b.String()
}

func waitStrimziReady(ctx context.Context, namespace string, timeout time.Duration) error {
	m := strimziWaitModel{
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

	fm := finalModel.(strimziWaitModel)
	if fm.err != nil {
		return fm.err
	}

	return nil
}
