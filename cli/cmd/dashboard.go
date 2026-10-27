package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/tui"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var dashInterval int

// dashCallTimeout bounds each API call of a refresh: a hung API shows as
// unreachable on the next frame instead of freezing the dashboard.
const dashCallTimeout = 5 * time.Second

// dashHistoryLen is how many refreshes the sparklines remember.
const dashHistoryLen = 30

var dashboardCmd = &cobra.Command{
	Use:     "dashboard",
	Aliases: []string{"dash"},
	Short:   "Full-screen monitoring dashboard with live metrics",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := checkInterval(dashInterval); err != nil {
			return err
		}
		ctx, cancel := context.WithCancel(commandContext(cmd))
		defer cancel()
		interval := time.Duration(dashInterval) * time.Second
		if !IsInteractive() {
			return runDashboardFrames(ctx, apiClient, interval)
		}
		m := newDashModel(ctx, apiClient, interval)
		_, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx)).Run()
		return err
	},
}

// dashAPI is what the dashboard reads. *client.Client is one; tests use a
// fake.
type dashAPI interface {
	Health(ctx context.Context) (*client.HealthResponse, error)
	ListTests(ctx context.Context, testType, status string, page, size int) (*client.PagedTests, error)
	ClusterCheck(ctx context.Context) (*client.ClusterHealthReport, error)
}

// dashSnapshot is one refresh's API answers.
type dashSnapshot struct {
	health    *client.HealthResponse
	healthErr error
	paged     *client.PagedTests
	listErr   error
	check     *client.ClusterHealthReport
	checkErr  error
	at        time.Time
}

// fetchDashSnapshot makes a refresh's API calls, each under its own timeout
// within ctx.
func fetchDashSnapshot(ctx context.Context, api dashAPI, timeout time.Duration) dashSnapshot {
	var s dashSnapshot
	call := func(f func(context.Context)) {
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		f(callCtx)
	}
	call(func(c context.Context) { s.health, s.healthErr = api.Health(c) })
	call(func(c context.Context) { s.paged, s.listErr = api.ListTests(c, "", "", 0, 50) })
	call(func(c context.Context) { s.check, s.checkErr = api.ClusterCheck(c) })
	s.at = time.Now()
	return s
}

// dashHistory is the sparklines' memory: the active runs' summed throughput
// and last p99 at each refresh that had any.
type dashHistory struct {
	throughput []float64
	latency    []float64
}

// observe adds a snapshot's active-run figures to the history.
func (h *dashHistory) observe(s dashSnapshot) {
	_, _, throughput, latency := dashRunsContent(s.paged, s.listErr)
	if throughput > 0 {
		h.throughput = append(h.throughput, throughput)
	}
	if latency > 0 {
		h.latency = append(h.latency, latency)
	}
	if len(h.throughput) > dashHistoryLen {
		h.throughput = h.throughput[len(h.throughput)-dashHistoryLen:]
	}
	if len(h.latency) > dashHistoryLen {
		h.latency = h.latency[len(h.latency)-dashHistoryLen:]
	}
}

// glyphOr is a mark the glyph set lacks: utf in a UTF-8 terminal, ascii
// under --plain, KATES_ASCII or a C locale.
func glyphOr(utf, ascii string) string {
	if output.ASCII() {
		return ascii
	}
	return utf
}

// renderDashFrame is one dashboard frame for a terminal width columns wide,
// every panel but the refresh line.
func renderDashFrame(s dashSnapshot, h dashHistory, width int) string {
	panelW := (width / 2) - 2
	if panelW < 38 {
		panelW = 38
	}
	var b strings.Builder
	row := func(left, right string) {
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", right) + "\n\n")
	}

	titleBar := lipgloss.NewStyle().
		Bold(true).
		Foreground(output.HeaderColor).
		Background(output.Surface).
		Width(width).
		Padding(0, 1).
		Render(fmt.Sprintf(
			"  Kates Dashboard  %s  %s",
			output.DimStyle.Render(glyphOr("│", "|")),
			s.at.Format("15:04:05"),
		))
	b.WriteString(titleBar + "\n\n")

	row(output.Panel("System Health", dashHealthContent(s.health, s.healthErr), panelW),
		output.Panel("Test Summary", dashSummaryContent(s.paged, s.listErr, panelW-6), panelW))

	activeContent, recentContent, _, _ := dashRunsContent(s.paged, s.listErr)
	row(output.Panel("Active Tests", activeContent, panelW),
		output.Panel("Recent Completed", recentContent, panelW))

	if len(h.throughput) > 1 || len(h.latency) > 1 {
		row(output.Panel("Throughput "+glyphOr("↗", "(up)"), dashSparkContent(h.throughput, true, " rec/s"), panelW),
			output.Panel("P99 Latency "+glyphOr("↘", "(down)"), dashSparkContent(h.latency, false, " ms"), panelW))
	}

	row(output.Panel("Cluster Detail", dashClusterContent(s.check, s.checkErr), panelW),
		output.Panel("Quick Commands", dashQuickContent(), panelW))
	return b.String()
}

// dashHealthContent is the System Health panel.
func dashHealthContent(health *client.HealthResponse, err error) string {
	var b strings.Builder
	if err != nil || health == nil {
		b.WriteString(output.ErrorStyle.Render("  " + output.Glyphs().Cross + " API unreachable"))
		return b.String()
	}
	line := func(label, value string) {
		fmt.Fprintf(&b, "  %-10s %s\n", output.DimStyle.Render(label), value)
	}
	line("API", output.StatusBadge(health.Status))
	if kafka := health.Kafka; kafka != nil {
		line("Kafka", output.StatusBadge(kafka.Status))
		line("Brokers", output.LightStyle.Render(kafka.BootstrapServers))
	}
	if eng := health.Engine; eng != nil {
		line("Engine", output.AccentStyle.Render(eng.ActiveBackend))
	}
	fmt.Fprintf(&b, "  %-10s %s",
		output.DimStyle.Render("Configs"),
		output.LightStyle.Render(fmt.Sprintf("%d test configs loaded", len(health.Tests))))
	return b.String()
}

// dashSparkContent is a sparkline panel's body: the line and its latest
// value, or a note until there are two points.
func dashSparkContent(values []float64, higherIsBetter bool, unit string) string {
	if len(values) <= 1 {
		return output.DimStyle.Render("  Collecting data...")
	}
	latest := values[len(values)-1]
	value := output.SuccessStyle.Render(fmtNum(latest))
	if !higherIsBetter {
		value = output.WarningStyle.Render(fmtFloat(latest, 1))
	}
	return fmt.Sprintf("  %s\n  Current: %s%s", output.SparklineColored(values, higherIsBetter), value, unit)
}

// dashQuickContent is the Quick Commands panel.
func dashQuickContent() string {
	arrow := output.DimStyle.Render(output.Glyphs().Arrow)
	lines := []string{
		"kates test create --type LOAD",
		"kates benchmark",
		"kates gate --min-grade B",
		"kates test cleanup",
	}
	for i, l := range lines {
		lines[i] = fmt.Sprintf("  %s  %s", arrow, l)
	}
	return strings.Join(lines, "\n")
}

// dashFooter is the refresh line under a frame.
func dashFooter(tick int, interval time.Duration, stop string) string {
	return fmt.Sprintf("  %s Refreshing every %ds... (%s)\n",
		spinnerFrame(tick), int(interval/time.Second), stop)
}

// runDashboardFrames is the dashboard without a terminal to take over: it
// prints a frame per refresh, as it always has, until ctx ends.
func runDashboardFrames(ctx context.Context, api dashAPI, interval time.Duration) error {
	var h dashHistory
	for tick := 0; ; tick++ {
		output.ClearFrame(false)
		s := fetchDashSnapshot(ctx, api, dashCallTimeout)
		h.observe(s)
		fmt.Fprint(output.Out, renderDashFrame(s, h, termWidth())+dashFooter(tick, interval, "Ctrl+C to stop"))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
		}
	}
}

// dashModel is the full-screen dashboard: it refreshes every interval, one
// refresh at a time, and quits on q or Ctrl+C.
type dashModel struct {
	ctx      context.Context
	api      dashAPI
	interval time.Duration
	timeout  time.Duration
	// tick schedules the next refresh; tea.Tick, but a test can see the
	// interval asked for.
	tick func(time.Duration, func(time.Time) tea.Msg) tea.Cmd

	snap     *dashSnapshot
	hist     dashHistory
	frames   int
	width    int
	height   int
	quitting bool
}

// dashSnapshotMsg carries a finished refresh.
type dashSnapshotMsg dashSnapshot

// dashTickMsg asks for the next refresh.
type dashTickMsg struct{}

var dashQuitKey = key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit"))

func newDashModel(ctx context.Context, api dashAPI, interval time.Duration) dashModel {
	return dashModel{ctx: ctx, api: api, interval: interval, timeout: dashCallTimeout, tick: tea.Tick}
}

func (m dashModel) Init() tea.Cmd {
	return m.refresh()
}

// refresh fetches a snapshot off the UI goroutine.
func (m dashModel) refresh() tea.Cmd {
	ctx, api, timeout := m.ctx, m.api, m.timeout
	return func() tea.Msg {
		return dashSnapshotMsg(fetchDashSnapshot(ctx, api, timeout))
	}
}

func (m dashModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		if key.Matches(msg, dashQuitKey) {
			m.quitting = true
			return m, tea.Quit
		}
	case dashSnapshotMsg:
		s := dashSnapshot(msg)
		m.snap = &s
		m.hist.observe(s)
		m.frames++
		// The next refresh is scheduled when this one lands, so a slow API
		// never has two refreshes in flight.
		return m, m.tick(m.interval, func(time.Time) tea.Msg { return dashTickMsg{} })
	case dashTickMsg:
		return m, m.refresh()
	}
	return m, nil
}

func (m dashModel) View() string {
	if m.quitting {
		return ""
	}
	if tui.TooSmall(m.width, m.height) {
		return tui.TooSmallCard(m.width, m.height)
	}
	if m.snap == nil {
		return "\n  " + output.DimStyle.Render("Loading dashboard...")
	}
	width := m.width
	if width == 0 {
		width = termWidth()
	}
	return renderDashFrame(*m.snap, m.hist, width) + dashFooter(m.frames-1, m.interval, "q or Ctrl+C to quit")
}

// dashSummaryContent is the Test Summary panel. A run list the API did not
// return is unknown, not empty, so every count reads — rather than 0.
func dashSummaryContent(paged *client.PagedTests, err error, ruleWidth int) string {
	unknown := output.DimStyle.Render(fmt.Sprintf("%3s", glyphOr("—", "-")))
	running, pending, done, total := unknown, unknown, unknown, unknown
	failed := output.DimStyle.Render(glyphOr("—", "-"))
	if err == nil && paged != nil {
		counts := CountStatuses(paged.Content)
		running = output.AccentStyle.Bold(true).Render(fmt.Sprintf("%3d", counts.Running))
		pending = output.WarningStyle.Render(fmt.Sprintf("%3d", counts.Pending))
		done = output.SuccessStyle.Render(fmt.Sprintf("%3d", counts.Done))
		failed = coloredCount(counts.Failed)
		total = output.LightStyle.Bold(true).Render(fmt.Sprintf("%3d", paged.TotalItems))
	}

	summaryContent := &strings.Builder{}
	fmt.Fprintf(summaryContent, "  %s %-11s %s %s\n",
		running,
		output.LightStyle.Render("Running"),
		pending,
		output.LightStyle.Render("Pending"),
	)
	fmt.Fprintf(summaryContent, "  %s %-11s %s %s\n",
		done,
		output.LightStyle.Render("Done"),
		failed,
		output.LightStyle.Render("Failed"),
	)
	fmt.Fprintf(summaryContent, "  %s\n",
		output.DimStyle.Render(strings.Repeat(glyphOr("·", "."), ruleWidth)),
	)
	fmt.Fprintf(summaryContent, "  %s %s",
		total,
		output.DimStyle.Render("Total tests"),
	)
	return summaryContent.String()
}

// dashActive reports whether a run with this upper-case status has yet to
// end. A STOPPING run has not: it is still active, never completed.
func dashActive(status string) bool {
	return status == "RUNNING" || status == "PENDING" || status == "STOPPING"
}

// dashRunsContent is the Active Tests and Recent Completed panels, plus the
// summed throughput and the last p99 of the active runs for the sparklines.
// A run list the API did not return is unknown, not empty.
func dashRunsContent(paged *client.PagedTests, err error) (active, recent string, throughput, p99 float64) {
	if err != nil || paged == nil {
		unavailable := output.DimStyle.Render("  Run list unavailable")
		return unavailable, unavailable, 0, 0
	}
	g := output.Glyphs()
	activeContent := &strings.Builder{}
	activeCount := 0
	var latestThroughput, latestLatency float64
	for _, t := range paged.Content {
		status := strings.ToUpper(t.Status)
		if !dashActive(status) {
			continue
		}
		activeCount++

		line := fmt.Sprintf("  %s %-12s %s",
			output.AccentStyle.Render(g.DotOn),
			output.LightStyle.Bold(true).Render(t.TestType),
			output.DimStyle.Render(truncID(t.ID)),
		)
		if status != "RUNNING" {
			line += "  " + output.DimStyle.Render(strings.ToLower(status))
		}
		if len(t.Results) > 0 {
			r := t.Results[len(t.Results)-1]
			latestThroughput += r.ThroughputRecordsPerSec
			latestLatency = r.P99LatencyMs
			line += fmt.Sprintf("  "+glyphOr("│", "|")+" %s rec  %s  %s p99",
				output.LightStyle.Render(fmtNum(r.RecordsSent)),
				output.SuccessStyle.Render(fmtNum(r.ThroughputRecordsPerSec)+" rec/s"),
				output.WarningStyle.Render(fmtFloat(r.P99LatencyMs, 1)+" ms"),
			)
		}
		activeContent.WriteString(line + "\n")
	}
	if activeCount == 0 {
		activeContent.WriteString(output.DimStyle.Render("  No active tests"))
	}
	recentContent := &strings.Builder{}
	recentCount := 0
	for _, t := range paged.Content {
		status := strings.ToUpper(t.Status)
		if dashActive(status) {
			continue
		}
		if recentCount >= 5 {
			break
		}
		recentCount++
		icon := output.DimStyle.Render(g.Ring)
		switch status {
		case "DONE", "COMPLETED":
			icon = output.SuccessStyle.Render(g.Check)
		case "FAILED", "ERROR":
			icon = output.ErrorStyle.Render(g.Cross)
		}
		fmt.Fprintf(recentContent, "  %s %-12s %s  %s  %s\n",
			icon,
			output.LightStyle.Render(t.TestType),
			output.DimStyle.Render(truncID(t.ID)),
			output.StatusBadge(status),
			output.DimStyle.Render(formatTime(t.CreatedAt)),
		)
	}
	if recentCount == 0 {
		recentContent.WriteString(output.DimStyle.Render("  No completed tests"))
	}
	return activeContent.String(), recentContent.String(), latestThroughput, latestLatency
}

// dashClusterContent is the Cluster Detail panel. Its verdict is the check's
// own status: the API makes a cluster CRITICAL for offline partitions or a
// KRaft quorum with no leader, and WARNING for under-replicated partitions.
func dashClusterContent(check *client.ClusterHealthReport, err error) string {
	if err != nil || check == nil {
		return output.DimStyle.Render("  Cluster data unavailable")
	}
	g := output.Glyphs()
	// Pad the plain label, then style it: padding a styled string counts its
	// escape bytes and pads nothing.
	row := func(label, value string) string {
		return "  " + output.DimStyle.Render(fmt.Sprintf("%-10s", label)) + " " + value
	}

	ph := check.PartitionHealth
	offline := output.ErrorStyle.Render(fmt.Sprintf("%s %d offline", g.Cross, ph.Offline))
	underReplicated := output.WarningStyle.Render(fmt.Sprintf("%s %d under-replicated", g.Warn, ph.UnderReplicated))
	rows := []string{
		row("Health", clusterVerdict(check.Status)),
		row("Brokers", fmt.Sprintf("%d", check.Brokers)),
		row("Topics", fmt.Sprintf("%d", check.Topics)),
	}
	switch {
	case ph.Offline > 0 && ph.UnderReplicated > 0:
		// Two rows: on one line the pair overflows an 80-column panel.
		rows = append(rows, row("Partitions", offline), row("", underReplicated))
	case ph.Offline > 0:
		rows = append(rows, row("Partitions", offline))
	case ph.UnderReplicated > 0:
		rows = append(rows, row("Partitions", underReplicated))
	default:
		rows = append(rows, row("Partitions", output.SuccessStyle.Render(g.Check+" all in sync")))
	}

	return strings.Join(append(rows, row("Controller", controllerLabel(check))), "\n")
}

// clusterVerdict is a /api/cluster/check status in the words kates cluster
// watch uses for it.
func clusterVerdict(status string) string {
	g := output.Glyphs()
	switch strings.ToUpper(status) {
	case "HEALTHY":
		return output.SuccessStyle.Render(g.Check + " HEALTHY")
	case "WARNING":
		return output.WarningStyle.Render(g.Warn + " WARNING")
	case "CRITICAL":
		return output.ErrorStyle.Render(g.Cross + " CRITICAL")
	}
	return output.DimStyle.Render(g.Ring + " " + status)
}

func init() {
	dashboardCmd.Flags().IntVar(&dashInterval, "interval", 3, "Refresh interval in seconds")
	rootCmd.AddCommand(dashboardCmd)
}
