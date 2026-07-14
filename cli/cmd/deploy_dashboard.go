package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/theme"
)

// ── Controller Interface for deploy.go ─────────────────────────────────────

var dl *DashboardController

type DashboardController struct {
	p *tea.Program
}

func (c *DashboardController) Printf(format string, a ...any) {
	if c.p != nil {
		c.p.Send(logMsg{text: fmt.Sprintf(format, a...)})
	} else {
		fmt.Printf(format, a...)
	}
}

func (c *DashboardController) Println(a ...any) {
	if c.p != nil {
		c.p.Send(logMsg{text: fmt.Sprint(a...)})
	} else {
		fmt.Println(a...)
	}
}

func (c *DashboardController) StartComponent(id string, timeout time.Duration) {
	if c.p != nil {
		c.p.Send(compStatusMsg{id: id, active: true, timeout: timeout})
	}
}

func (c *DashboardController) FinishComponent(id string, success bool) {
	if c.p != nil {
		c.p.Send(compStatusMsg{id: id, active: false, done: true, success: success})
	}
}

func (c *DashboardController) UpdateProgress(current, total int) {
	if c.p != nil {
		c.p.Send(progressMsg{current: current, total: total})
	}
}

// ── Messages ─────────────────────────────────────────────────────────────

type logMsg struct {
	text string
}

type compStatusMsg struct {
	id      string
	active  bool
	done    bool
	success bool
	timeout time.Duration
}

type progressMsg struct {
	current int
	total   int
}

type allWorkloadsMsg struct {
	workloads map[string][]workloadStat
}

// ── Bubble Tea Model ─────────────────────────────────────────────────────

type Target struct {
	Namespace string
	Selector  string
}

type componentStat struct {
	ID        string
	Name      string
	Targets   []Target
	Group     string
	Active    bool
	Done      bool
	Success   bool
	Timeout   time.Duration
	StartTime time.Time
	Workloads []workloadStat
}

type deployDashboardModel struct {
	ctx        context.Context
	cancel     context.CancelFunc
	components []*componentStat
	compMap    map[string]*componentStat

	logs               []string
	logsViewport       viewport.Model
	componentsViewport viewport.Model
	spinner            spinner.Model
	progressBar        progress.Model

	currentStep int
	totalSteps  int
	err         error

	width     int
	height    int
	compWidth int

	focusedPane int // 0: components, 1: logs
	helmVersion string
}

func NewDeployDashboard(ctx context.Context, totalSteps int) deployDashboardModel {
	ctx, cancel := context.WithCancel(ctx)

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(theme.Accent)

	prog := progress.New(progress.WithDefaultGradient())

	logsVp := viewport.New(100, 10)
	compVp := viewport.New(100, 15)

	m := deployDashboardModel{
		ctx:                ctx,
		cancel:             cancel,
		compMap:            make(map[string]*componentStat),
		spinner:            s,
		progressBar:        prog,
		logsViewport:       logsVp,
		componentsViewport: compVp,
		totalSteps:         totalSteps,
		compWidth:          60,
	}

	out, err := exec.CommandContext(ctx, "helm", "version", "--short").Output()
	if err == nil {
		m.helmVersion = strings.TrimSpace(string(out))
	} else {
		m.helmVersion = "unknown"
	}

	return m
}

// ── Dashboard Methods ────────────────────────────────────────────────────

func (m *deployDashboardModel) RegisterComponent(id, name, group string, targets ...Target) {
	c := &componentStat{
		ID:      id,
		Name:    name,
		Targets: targets,
		Group:   group,
	}
	m.components = append(m.components, c)
	m.compMap[id] = c
}

func pollAllWorkloads(ctx context.Context, components []*componentStat) tea.Cmd {
	return func() tea.Msg {
		results := make(map[string][]workloadStat)

		for _, c := range components {
			if len(c.Targets) == 0 {
				continue
			}

			var workloads []workloadStat

			for _, target := range c.Targets {
				if target.Selector == "" {
					continue
				}

				out, err := exec.CommandContext(ctx, "kubectl", "get", "deploy,sts,ds,strimzipodsets",
					"-n", target.Namespace,
					"-l", target.Selector,
					"-o", "json").Output()

				if err != nil || len(out) == 0 {
					continue
				}

				var list struct {
					Items []struct {
						Kind     string `json:"kind"`
						Metadata struct {
							Name string `json:"name"`
						} `json:"metadata"`
						Spec struct {
							Replicas *int `json:"replicas"`
						} `json:"spec"`
						Status struct {
							ReadyReplicas *int `json:"readyReplicas"`
							NumberReady   *int `json:"numberReady"`
							DesiredNumber *int `json:"desiredNumberScheduled"`
							Pods          *int `json:"pods"`
							ReadyPods     *int `json:"readyPods"`
						} `json:"status"`
					} `json:"items"`
				}

				if err := json.Unmarshal(out, &list); err != nil {
					continue
				}

				for _, item := range list.Items {
					stat := workloadStat{
						kind: item.Kind,
						name: item.Metadata.Name,
					}
					if item.Kind == "DaemonSet" {
						if item.Status.DesiredNumber != nil {
							stat.expected = *item.Status.DesiredNumber
						}
						if item.Status.NumberReady != nil {
							stat.ready = *item.Status.NumberReady
						}
					} else if item.Kind == "StrimziPodSet" {
						if item.Status.Pods != nil {
							stat.expected = *item.Status.Pods
						} else {
							stat.expected = 1
						}
						if item.Status.ReadyPods != nil {
							stat.ready = *item.Status.ReadyPods
						}
					} else {
						if item.Spec.Replicas != nil {
							stat.expected = *item.Spec.Replicas
						} else {
							stat.expected = 1
						}
						if item.Status.ReadyReplicas != nil {
							stat.ready = *item.Status.ReadyReplicas
						}
					}
					// Poll pods for this workload to get phase
					podOut, _ := exec.CommandContext(ctx, "kubectl", "get", "pods", "-n", target.Namespace, "-l", target.Selector, "-o", "jsonpath={range .items[*]}{.status.phase}{','}{end}").Output()
					phases := strings.Split(strings.TrimSpace(string(podOut)), ",")
					if len(phases) > 0 && phases[0] != "" {
						stat.phase = phases[0] // Simplify by just taking the first pod's phase for the workload
					}

					workloads = append(workloads, stat)
				}
			}

			sort.Slice(workloads, func(i, j int) bool { return workloads[i].name < workloads[j].name })
			results[c.ID] = workloads
		}

		return allWorkloadsMsg{workloads: results}
	}
}

func (m deployDashboardModel) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }),
	)
}

func (m deployDashboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		// 6 for header/footer (progress bar + padding)
		viewHeight := msg.Height - 6
		if viewHeight < 5 {
			viewHeight = 5
		}

		// Components pane is responsive
		compWidth := msg.Width * 55 / 100
		if compWidth > 70 {
			compWidth = 70
		}
		if compWidth < 50 {
			compWidth = 50
		}
		m.compWidth = compWidth - 2
		logsWidth := msg.Width - compWidth - 4 // 4 for spacing/margins
		if logsWidth < 20 {
			logsWidth = 20
		}

		m.componentsViewport.Width = compWidth - 2
		m.componentsViewport.Height = viewHeight - 2

		m.logsViewport.Width = logsWidth - 2
		m.logsViewport.Height = viewHeight - 2

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.cancel() // cancel context for background workers
			return m, tea.Quit
		}
		if msg.String() == "tab" {
			m.focusedPane = (m.focusedPane + 1) % 2
			return m, nil
		}

		var cmd tea.Cmd
		if m.focusedPane == 0 {
			m.componentsViewport, cmd = m.componentsViewport.Update(msg)
		} else {
			m.logsViewport, cmd = m.logsViewport.Update(msg)
		}
		cmds = append(cmds, cmd)

	case logMsg:
		lines := strings.Split(strings.TrimSpace(msg.text), "\n")
		for _, l := range lines {
			if l != "" {
				m.logs = append(m.logs, l)
			}
		}
		if len(m.logs) > 100 {
			m.logs = m.logs[len(m.logs)-100:]
		}
		m.logsViewport.SetContent(strings.Join(m.logs, "\n"))
		if m.logsViewport.AtBottom() || len(m.logs) < m.logsViewport.Height {
			m.logsViewport.GotoBottom()
		}

	case compStatusMsg:
		if c, ok := m.compMap[msg.id]; ok {
			c.Active = msg.active
			if msg.active && !msg.done {
				c.Timeout = msg.timeout
				c.StartTime = time.Now()
			}
			if msg.done {
				c.Done = true
				c.Success = msg.success
			}
		}

	case progressMsg:
		m.currentStep = msg.current
		if msg.total > 0 {
			m.totalSteps = msg.total
		}
		if m.currentStep > m.totalSteps {
			m.currentStep = m.totalSteps // Clamp
		}
		pct := 0.0
		if m.totalSteps > 0 {
			pct = float64(m.currentStep) / float64(m.totalSteps)
		}
		cmds = append(cmds, m.progressBar.SetPercent(pct))

	case progress.FrameMsg:
		progressModel, cmd := m.progressBar.Update(msg)
		m.progressBar = progressModel.(progress.Model)
		cmds = append(cmds, cmd)

	case allWorkloadsMsg:
		for id, w := range msg.workloads {
			if c, ok := m.compMap[id]; ok {
				c.Workloads = w
			}
		}

	case tickMsg:
		cmds = append(cmds,
			pollAllWorkloads(m.ctx, m.components),
			tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }),
		)
	}

	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m deployDashboardModel) View() string {
	var b strings.Builder

	pct := float64(m.currentStep) / float64(m.totalSteps)
	if m.totalSteps == 0 {
		pct = 0
	}
	if pct > 1.0 {
		pct = 1.0
	}

	bar := m.progressBar.ViewAs(pct)

	prevGroup := ""
	for i, c := range m.components {
		isFirstInGroup := (c.Group != prevGroup)
		isLastInGroup := (i == len(m.components)-1 || m.components[i+1].Group != c.Group)

		if isFirstInGroup {
			label := componentGroupNames[c.Group]
			if label == "" {
				label = c.Group
			}
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(fmt.Sprintf("%s\n", lipgloss.NewStyle().Bold(true).Foreground(theme.Accent).Render("── "+label)))
		}

		if !c.Active && !c.Done {
			b.WriteString(fmt.Sprintf("  %s\n", output.DimStyle.Render("◯ Pending...")))
		} else {
			icon := m.spinner.View()
			if c.Done {
				if c.Success {
					icon = output.SuccessStyle.Render("✔")
				} else {
					icon = output.ErrorStyle.Render("✖")
				}
			}

			statusText := output.DimStyle.Render("Deploying...")
			if c.Done {
				if c.Success {
					statusText = output.AccentStyle.Render("Ready")
				} else {
					statusText = output.ErrorStyle.Render("Failed")
				}
			}

			nsStr := ""
			if len(c.Targets) > 0 {
				nsStr = " [" + c.Targets[0].Namespace + "]"
			}
			boldStyle := lipgloss.NewStyle().Bold(true)
			leftPart := fmt.Sprintf("%s %s%s", icon, boldStyle.Render(c.Name), output.DimStyle.Render(nsStr))
			rightPart := statusText
			// We just use m.compWidth for total width
			spaces := m.compWidth - lipgloss.Width(leftPart) - lipgloss.Width(rightPart)
			if spaces < 1 {
				spaces = 1
			}
			b.WriteString(fmt.Sprintf("%s%s%s\n", leftPart, strings.Repeat(" ", spaces), rightPart))

			// Render timeout bar if active
			if c.Active && !c.Done && c.Timeout > 0 {
				elapsed := time.Since(c.StartTime)
				if elapsed > c.Timeout {
					elapsed = c.Timeout
				}

				tLeft := fmt.Sprintf("  %s", output.DimStyle.Render("Timeout"))

				// Format mm:ss
				eM := int(elapsed.Minutes())
				eS := int(elapsed.Seconds()) % 60
				tM := int(c.Timeout.Minutes())
				tS := int(c.Timeout.Seconds()) % 60

				tRight := fmt.Sprintf("[%s] %02d:%02d / %02d:%02d",
					renderProgressBar(int(elapsed.Seconds()), int(c.Timeout.Seconds()), 15, false, true),
					eM, eS, tM, tS)

				tSpaces := m.compWidth - lipgloss.Width(tLeft) - lipgloss.Width(tRight)
				if tSpaces < 1 {
					tSpaces = 1
				}
				b.WriteString(fmt.Sprintf("%s%s%s\n", tLeft, strings.Repeat(" ", tSpaces), output.DimStyle.Render(tRight)))
			}

			for _, w := range c.Workloads {
				name := w.name
				maxNameLen := (m.compWidth - 4) * 45 / 100
				if maxNameLen < 18 {
					maxNameLen = 18
				}
				if len(name) > maxNameLen {
					name = name[:maxNameLen-3] + "..."
				}
				barReady := w.ready
				barTotal := w.expected
				if barTotal == 0 {
					barTotal = 1
					barReady = 1
				}

				status := w.phase
				if status == "" {
					if w.ready >= w.expected {
						status = "Ready"
					} else {
						status = "Pending"
					}
				}
				if status == "Ready" {
					status = output.AccentStyle.Render("Ready")
				} else {
					status = output.WarningStyle.Render(status)
				}

				wLeft := fmt.Sprintf("  %s", output.DimStyle.Render(name)) // removed one space
				wRight := fmt.Sprintf("[%s] %2d/%-2d %s", renderProgressBar(barReady, barTotal, 10, false, false), w.ready, w.expected, status)
				wSpaces := m.compWidth - lipgloss.Width(wLeft) - lipgloss.Width(wRight)
				if wSpaces < 1 {
					wSpaces = 1
				}
				b.WriteString(fmt.Sprintf("%s%s%s\n", wLeft, strings.Repeat(" ", wSpaces), wRight))
			}
		}

		if isLastInGroup {
			b.WriteString("\n")
		}

		prevGroup = c.Group
	}

	// Calculate which line the active component starts at to auto-scroll
	contentStr := b.String()
	m.componentsViewport.SetContent(contentStr)

	// Simple auto-scroll heuristic: if there's an active component, scroll roughly to it
	activeIdx := -1
	for i, c := range m.components {
		if c.Active {
			activeIdx = i
			break
		}
	}
	if activeIdx >= 0 && m.focusedPane != 0 {
		pct := float64(activeIdx) / float64(len(m.components))
		targetLine := int(pct * float64(m.componentsViewport.TotalLineCount()))
		m.componentsViewport.SetYOffset(targetLine)
	}

	var out strings.Builder

	headerStyle := lipgloss.NewStyle().
		Padding(0, 1).
		Border(lipgloss.RoundedBorder(), true).
		BorderForeground(theme.Accent).
		Width(m.width - 2)

	headerContent := fmt.Sprintf("📦 Kates Cluster Deployment: %s %.0f%% (%d/%d Steps) │ Helm: %s", bar, pct*100, m.currentStep, m.totalSteps, m.helmVersion)
	out.WriteString(headerStyle.Render(headerContent) + "\n")

	compBorderStyle := lipgloss.NewStyle().Border(lipgloss.RoundedBorder())
	if m.focusedPane == 0 {
		compBorderStyle = compBorderStyle.BorderForeground(theme.Accent)
	} else {
		compBorderStyle = compBorderStyle.BorderForeground(theme.Subtle)
	}

	logBorderStyle := lipgloss.NewStyle().Border(lipgloss.RoundedBorder())
	if m.focusedPane == 1 {
		logBorderStyle = logBorderStyle.BorderForeground(theme.Accent)
	} else {
		logBorderStyle = logBorderStyle.BorderForeground(theme.Subtle)
	}

	split := lipgloss.JoinHorizontal(lipgloss.Top,
		compBorderStyle.Render(m.componentsViewport.View()),
		logBorderStyle.Render(m.logsViewport.View()),
	)
	out.WriteString(split)
	out.WriteString("\n")

	return out.String()
}

// extend workloadStat to support phase
type workloadStat struct {
	kind     string
	name     string
	ready    int
	expected int
	phase    string
}

type tickMsg time.Time

func renderProgressBar(current, total, width int, failed bool, isTimeout bool) string {
	if total == 0 {
		total = 1
	}
	if current > total {
		current = total
	}
	pct := float64(current) / float64(total)
	filled := int(pct * float64(width))
	empty := width - filled

	fillChar := "▰"
	emptyChar := "▱"

	if !isTimeout {
		fillChar = "█"
		emptyChar = "░"
	}

	fillStr := strings.Repeat(fillChar, filled)
	if failed {
		fillStr = output.ErrorStyle.Render(fillStr)
	} else if current == total {
		if isTimeout {
			fillStr = output.ErrorStyle.Render(fillStr) // Timeout reaching max is bad
		} else {
			fillStr = output.AccentStyle.Render(fillStr)
		}
	} else if isTimeout {
		fillStr = output.WarningStyle.Render(fillStr) // Timeout progress in amber
	}

	emptyStr := strings.Repeat(emptyChar, empty)
	if !isTimeout {
		emptyStr = output.DimStyle.Render(emptyStr)
	}

	return fmt.Sprintf("%s%s", fillStr, emptyStr)
}
