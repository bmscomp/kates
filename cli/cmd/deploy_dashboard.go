package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/theme"
	"github.com/bmscomp/kates/cli/tui"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Controller Interface for deploy.go ─────────────────────────────────────

var dl *DashboardController

type DashboardController struct {
	p *tea.Program
	// closed is set once the window is gone (Ctrl-C closes it mid-deploy):
	// what the deploy says from then on goes to the terminal.
	closed atomic.Bool
}

// Every method tolerates a nil receiver. dl is a package-level pointer set
// when the dashboard starts, so any path that runs without one — a unit test,
// a step that moved earlier than the dashboard's construction — would
// otherwise panic mid-deploy on a log line. Logging is never worth a crash.

func (c *DashboardController) live() bool {
	return c != nil && c.p != nil && !c.closed.Load()
}

func (c *DashboardController) Printf(format string, a ...any) {
	if c.live() {
		c.p.Send(logMsg{text: fmt.Sprintf(format, a...)})
	} else {
		fmt.Printf(format, a...)
	}
}

func (c *DashboardController) Println(a ...any) {
	if c.live() {
		c.p.Send(logMsg{text: fmt.Sprint(a...)})
	} else {
		fmt.Println(a...)
	}
}

func (c *DashboardController) StartComponent(id string, timeout time.Duration) {
	if c.live() {
		c.p.Send(compStatusMsg{id: id, active: true, timeout: timeout})
	}
}

func (c *DashboardController) FinishComponent(id string, success bool) {
	if c.live() {
		c.p.Send(compStatusMsg{id: id, active: false, done: true, success: success})
	}
}

func (c *DashboardController) UpdateProgress(current, total int) {
	if c.live() {
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

	width     int
	height    int
	compWidth int

	focusedPane int // 0: components, 1: logs
	helmVersion string
	// scrolled is set once a key scrolls the components pane; until then the
	// pane follows the component being deployed.
	scrolled bool
	// interrupted is set when Ctrl-C closed the window before the deploy ended.
	interrupted bool

	// read runs the poller's kubectl queries. It is the seam as it stood when
	// the dashboard was built: the poller runs on Bubble Tea's goroutines,
	// some of which outlive the deploy, and runDeploy restores the seams when
	// it returns (pinKubeContext).
	read func(ctx context.Context, name string, args ...string) ([]byte, error)
}

func NewDeployDashboard(ctx context.Context, totalSteps int) deployDashboardModel {
	ctx, cancel := context.WithCancel(ctx)

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(theme.Accent)

	// The header prints the percentage itself.
	prog := progress.New(progress.WithDefaultGradient(), progress.WithoutPercentage())

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
		read:               runExecOutputFn,
	}

	out, err := m.read(ctx, "helm", "version", "--short")
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

// pollTarget is what the poller needs of a component — its id and targets —
// copied out of the model when the poll is scheduled. The poll runs on a
// Bubble Tea goroutine while Update keeps writing the *componentStat it
// came from; sharing the pointer was a data race.
type pollTarget struct {
	id      string
	targets []Target
}

func pollAllWorkloads(ctx context.Context, components []pollTarget, read func(ctx context.Context, name string, args ...string) ([]byte, error)) tea.Cmd {
	return func() tea.Msg {
		results := make(map[string][]workloadStat)

		for _, c := range components {
			if len(c.targets) == 0 {
				continue
			}

			var workloads []workloadStat

			for _, target := range c.targets {
				if target.Selector == "" {
					continue
				}

				out, err := read(ctx, "kubectl", "get", "deploy,sts,ds,strimzipodsets",
					"-n", target.Namespace,
					"-l", target.Selector,
					"-o", "json")

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
					switch item.Kind {
					case "DaemonSet":
						if item.Status.DesiredNumber != nil {
							stat.expected = *item.Status.DesiredNumber
						}
						if item.Status.NumberReady != nil {
							stat.ready = *item.Status.NumberReady
						}
					case "StrimziPodSet":
						if item.Status.Pods != nil {
							stat.expected = *item.Status.Pods
						} else {
							stat.expected = 1
						}
						if item.Status.ReadyPods != nil {
							stat.ready = *item.Status.ReadyPods
						}
					default:
						if item.Spec.Replicas != nil {
							stat.expected = *item.Spec.Replicas
						} else {
							stat.expected = 1
						}
						if item.Status.ReadyReplicas != nil {
							stat.ready = *item.Status.ReadyReplicas
						}
					}
					workloads = append(workloads, stat)
				}
			}

			sort.Slice(workloads, func(i, j int) bool { return workloads[i].name < workloads[j].name })
			results[c.id] = workloads
		}

		return allWorkloadsMsg{workloads: results}
	}
}

// sanitizeLogLine strips variation selector-16 (U+FE0F). The padding stack
// (viewport/lipgloss via go-runewidth) counts emoji+VS16 sequences as ONE
// cell while terminals render TWO — so every "⏭️ …" line pushed the pane's
// right border out by a column, producing the ragged edge. Without VS16 the
// base rune renders text-style at the width runewidth already computes.
func sanitizeLogLine(s string) string {
	return strings.ReplaceAll(s, "\ufe0f", "")
}

// buildLogContent joins the log ring with every line truncated to the pane
// width. Lines were previously passed through untouched, so one over-long
// helm message stretched the box past the terminal edge and the border
// wrapped into fragments.
func (m deployDashboardModel) buildLogContent() string {
	w := m.logsViewport.Width
	if w <= 0 {
		return strings.Join(m.logs, "\n")
	}
	out := make([]string, 0, len(m.logs))
	for _, l := range m.logs {
		out = append(out, ansi.Truncate(l, w, "…"))
	}
	return strings.Join(out, "\n")
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
		m.resize(msg)

	case tea.KeyMsg:
		cmd, done := m.handleKey(msg)
		if done {
			return m, cmd
		}
		cmds = append(cmds, cmd)

	case logMsg:
		m.appendLogs(msg.text)

	case compStatusMsg:
		m.applyCompStatus(msg)

	case progressMsg:
		cmds = append(cmds, m.applyProgress(msg))

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
		// The next poll waits for this one: ticking regardless stacked
		// kubectl calls up whenever a poll took longer than the tick.
		cmds = append(cmds, tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }))

	case tickMsg:
		cmds = append(cmds, pollAllWorkloads(m.ctx, m.startedTargets(), m.read))
	}

	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	cmds = append(cmds, cmd)

	// The pane's content lives in the model, not only in View's copy, so
	// the keys can scroll it.
	content, activeLine := m.componentsContent()
	m.componentsViewport.SetContent(content)
	if !m.scrolled && activeLine >= 0 {
		m.componentsViewport.SetYOffset(activeLine - 1) // with the line above it
	}

	return m, tea.Batch(cmds...)
}

// dashboardPaneWidths is the components pane's width and the logs pane's
// width, each including its border, for a terminal width columns wide.
func dashboardPaneWidths(width int) (compWidth, logsWidth int) {
	// Components pane is responsive
	compWidth = min(max(width*55/100, 50), 70)

	// One row width for the whole dashboard: header and panes must end at
	// the same column. The header renders at width-2 total (content
	// width + its border), while the old pane math produced width-4 —
	// so the panes row sat 2-4 columns short of the header's right edge.
	rowWidth := width - 2
	logsWidth = max(rowWidth-compWidth, 20)
	return compWidth, logsWidth
}

// resize lays the panes out for a new window size.
func (m *deployDashboardModel) resize(msg tea.WindowSizeMsg) {
	m.width = msg.Width
	m.height = msg.Height

	// 6 for header/footer (progress bar + padding)
	viewHeight := max(msg.Height-6, 5)

	compWidth, logsWidth := dashboardPaneWidths(msg.Width)
	m.compWidth = compWidth - 2

	m.componentsViewport.Width = compWidth - 2
	m.componentsViewport.Height = viewHeight - 2

	m.logsViewport.Width = logsWidth - 2
	m.logsViewport.Height = viewHeight - 2
	// Stored lines were truncated for the old width; rebuild for the new.
	m.logsViewport.SetContent(m.buildLogContent())
}

// handleKey handles a key. done says Update returns cmd at once, without
// the spinner and components refresh every other message gets.
func (m *deployDashboardModel) handleKey(msg tea.KeyMsg) (cmd tea.Cmd, done bool) {
	// The too-small card offers q; otherwise q is a viewport key.
	if msg.String() == "ctrl+c" || (msg.String() == "q" && tui.TooSmall(m.width, m.height)) {
		// Closes the window only. Helm may be mid-install, so the deploy
		// goes on, printing to the terminal (runDeploy); the terminal is
		// no longer raw, so a second Ctrl-C ends kates.
		m.cancel() // stops the poller
		m.interrupted = true
		return tea.Quit, true
	}
	switch msg.String() {
	case "tab":
		m.focusedPane = (m.focusedPane + 1) % 2
		return nil, true
	case "T":
		theme.NextLook()
		return nil, true
	case "C":
		theme.NextPalette()
		return nil, true
	}

	if m.focusedPane == 0 {
		m.componentsViewport, cmd = m.componentsViewport.Update(msg)
		m.scrolled = true
	} else {
		m.logsViewport, cmd = m.logsViewport.Update(msg)
	}
	return cmd, false
}

// appendLogs adds a log message's non-empty lines to the ring of the last
// 100, following the bottom unless the pane was scrolled up.
func (m *deployDashboardModel) appendLogs(text string) {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for _, l := range lines {
		if l != "" {
			m.logs = append(m.logs, sanitizeLogLine(l))
		}
	}
	if len(m.logs) > 100 {
		m.logs = m.logs[len(m.logs)-100:]
	}
	m.logsViewport.SetContent(m.buildLogContent())
	if m.logsViewport.AtBottom() || len(m.logs) < m.logsViewport.Height {
		m.logsViewport.GotoBottom()
	}
}

func (m *deployDashboardModel) applyCompStatus(msg compStatusMsg) {
	c, ok := m.compMap[msg.id]
	if !ok {
		return
	}
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

func (m *deployDashboardModel) applyProgress(msg progressMsg) tea.Cmd {
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
	return m.progressBar.SetPercent(pct)
}

// startedTargets is what the poller reads: only the components that have
// started have workloads to show. The poll gets copies: it runs while
// Update goes on writing these.
func (m deployDashboardModel) startedTargets() []pollTarget {
	var started []pollTarget
	for _, c := range m.components {
		if c.Active || c.Done {
			started = append(started, pollTarget{id: c.ID, targets: slices.Clone(c.Targets)})
		}
	}
	return started
}

// componentsContent is the components pane, and the line the first component
// being deployed starts on (-1 when none is).
func (m deployDashboardModel) componentsContent() (string, int) {
	var b strings.Builder
	activeLine := -1

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
			fmt.Fprintf(&b, "%s\n", lipgloss.NewStyle().Bold(true).Foreground(theme.Accent).Render("── "+label))
		}

		if c.Active && activeLine < 0 {
			activeLine = strings.Count(b.String(), "\n")
		}

		b.WriteString(m.componentLine(c))
		if c.Active && !c.Done && c.Timeout > 0 {
			b.WriteString(m.timeoutLine(c))
		}
		for _, w := range c.Workloads {
			b.WriteString(m.workloadLine(w))
		}

		if isLastInGroup {
			b.WriteString("\n")
		}

		prevGroup = c.Group
	}

	return b.String(), activeLine
}

// paneLine is left and right on one line of the components pane, the right
// part flush with its edge.
func (m deployDashboardModel) paneLine(left, right string) string {
	// We just use m.compWidth for total width
	spaces := max(m.compWidth-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return left + strings.Repeat(" ", spaces) + right + "\n"
}

// componentLine is a component's status line. A pending component is named
// too: the pane is the plan as well as the progress.
func (m deployDashboardModel) componentLine(c *componentStat) string {
	icon, statusText, name := output.DimStyle.Render("◯"), output.DimStyle.Render("Pending"), output.DimStyle.Render(c.Name)
	switch {
	case c.Done && c.Success:
		icon, statusText = output.SuccessStyle.Render("✔"), output.AccentStyle.Render("Ready")
	case c.Done:
		icon, statusText = output.ErrorStyle.Render("✖"), output.ErrorStyle.Render("Failed")
	case c.Active:
		icon, statusText = m.spinner.View(), output.DimStyle.Render("Deploying...")
	}
	if c.Active || c.Done {
		name = lipgloss.NewStyle().Bold(true).Render(c.Name)
	}

	nsStr := ""
	if len(c.Targets) > 0 {
		nsStr = " [" + c.Targets[0].Namespace + "]"
	}
	return m.paneLine(fmt.Sprintf("%s %s%s", icon, name, output.DimStyle.Render(nsStr)), statusText)
}

// timeoutLine is the timeout bar of a component being deployed.
func (m deployDashboardModel) timeoutLine(c *componentStat) string {
	elapsed := min(time.Since(c.StartTime), c.Timeout)

	tLeft := fmt.Sprintf("  %s", output.DimStyle.Render("Timeout"))

	// Format mm:ss
	eM := int(elapsed.Minutes())
	eS := int(elapsed.Seconds()) % 60
	tM := int(c.Timeout.Minutes())
	tS := int(c.Timeout.Seconds()) % 60

	tRight := fmt.Sprintf("[%s] %02d:%02d / %02d:%02d",
		renderProgressBar(int(elapsed.Seconds()), int(c.Timeout.Seconds()), 15, false, true),
		eM, eS, tM, tS)
	return m.paneLine(tLeft, output.DimStyle.Render(tRight))
}

// workloadLine is one workload of a component, with its ready count.
func (m deployDashboardModel) workloadLine(w workloadStat) string {
	name := w.name
	maxNameLen := max((m.compWidth-4)*45/100, 18)
	if len(name) > maxNameLen {
		name = name[:maxNameLen-3] + "..."
	}
	barReady := w.ready
	barTotal := w.expected
	if barTotal == 0 {
		barTotal = 1
		barReady = 1
	}

	// From the ready count: the first pod's phase, read before, put
	// a healthy workload in amber as "Running".
	status := output.AccentStyle.Render("Ready")
	if w.ready < w.expected {
		status = output.WarningStyle.Render("Waiting")
	}

	wLeft := fmt.Sprintf("  %s", output.DimStyle.Render(name)) // removed one space
	wRight := fmt.Sprintf("[%s] %2d/%-2d %s", renderProgressBar(barReady, barTotal, 10, false, false), w.ready, w.expected, status)
	return m.paneLine(wLeft, wRight)
}

func (m deployDashboardModel) View() string {
	if tui.TooSmall(m.width, m.height) {
		return tui.TooSmallCard(m.width, m.height)
	}
	pct := float64(m.currentStep) / float64(m.totalSteps)
	if m.totalSteps == 0 {
		pct = 0
	}
	if pct > 1.0 {
		pct = 1.0
	}

	bar := m.progressBar.ViewAs(pct)

	var out strings.Builder

	// The lab's title bar, m.width-2 wide: the rowWidth the panes fill, so
	// the right edges align.
	dot := "  " + glyphOr("·", "|") + "  "
	rest := fmt.Sprintf("%s%s %.0f%% (%d/%d steps)%sHelm %s", dot, bar, pct*100, m.currentStep, m.totalSteps, dot, m.helmVersion)
	out.WriteString(tui.ScreenHeader("Kates Cluster Deployment", rest, m.width-2) + "\n\n")

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
		theme.Box(compBorderStyle, m.focusedPane == 0, m.componentsViewport.View()),
		theme.Box(logBorderStyle, m.focusedPane == 1, m.logsViewport.View()),
	)
	out.WriteString(split)
	out.WriteString(tui.HelpLines(deployDashboardHelp, m.width))

	return theme.Screen(out.String(), m.width, m.height)
}

// deployDashboardHelp is the dashboard's key help, as the lab shows its own.
var deployDashboardHelp = []key.Binding{
	key.NewBinding(key.WithHelp("tab", "switch pane")),
	key.NewBinding(key.WithHelp(glyphOr("↑/↓", "up/down"), "scroll")),
	key.NewBinding(key.WithHelp("T (shift+t)", "next look")),
	key.NewBinding(key.WithHelp("C (shift+c)", "next colors")),
	key.NewBinding(key.WithHelp("ctrl+c", "close the window")),
}

// extend workloadStat to support phase
type workloadStat struct {
	kind     string
	name     string
	ready    int
	expected int
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
