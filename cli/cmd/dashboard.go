package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/theme"
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
		theme.FullScreen()
		_, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx)).Run()
		output.KeepHint()
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

// renderDashFrame is one dashboard frame, width columns wide, drawn as the
// lab draws its screen: a title bar, then the Cluster and Tests panes over
// the Active and Recent run panes. With height above 0 the frame takes that
// many rows and the run panes get what the others leave; at 0 every pane
// takes the rows it needs.
func renderDashFrame(s dashSnapshot, h dashHistory, width, height int) string {
	width = max(width, 60)
	leftW := (width - 3) / 2
	rightW := width - 3 - leftW

	header := tui.ScreenHeader("Kates Dashboard",
		"  "+glyphOr("·", "|")+"  updated "+s.at.Format("15:04:05"), width-2)

	system := dashHealthContent(s.health, s.healthErr) + "\n" + dashClusterContent(s.check, s.checkErr)
	tests := dashSummaryContent(s.paged, s.listErr, rightW-6) + "\n" +
		dashTrendLine("Throughput "+glyphOr("↗", "(up)"), h.throughput, true, " rec/s", rightW-34) + "\n" +
		dashTrendLine("P99        "+glyphOr("↘", "(down)"), h.latency, false, " ms", rightW-34)
	topH := max(tui.PaneHeight(system), tui.PaneHeight(tests))

	active, recent, _, _ := dashRunsContent(s.paged, s.listErr)
	runsH := max(tui.PaneHeight(active), tui.PaneHeight(recent))
	if height > 0 {
		// header, the blank under it, and the status and help lines
		runsH = max(height-lipgloss.Height(header)-1-topH-dashFooterRows, 5)
	}

	return header + "\n\n" +
		tui.JoinPanes(tui.Pane("Cluster", system, leftW, topH, true), tui.Pane("Tests", tests, rightW, topH, false)) + "\n" +
		tui.JoinPanes(tui.Pane("Active Tests", active, leftW, runsH, false), tui.Pane("Recent Completed", recent, rightW, runsH, false)) + "\n"
}

// dashFooterRows is the rows under a full-screen frame: the status line and
// one line of help.
const dashFooterRows = 2

// dashHealthContent is the API's own health: its status and engine, Kafka as
// it sees it, and the test configs it loaded.
func dashHealthContent(health *client.HealthResponse, err error) string {
	if err != nil || health == nil {
		return output.ErrorStyle.Render("  " + output.Glyphs().Cross + " API unreachable")
	}
	// Pad the plain label, then style it: padding a styled string pads nothing.
	row := func(label, value string) string {
		return "  " + output.DimStyle.Render(fmt.Sprintf("%-10s", label)) + " " + value
	}
	api := output.StatusBadge(health.Status)
	if eng := health.Engine; eng != nil {
		api += "  " + output.AccentStyle.Render(eng.ActiveBackend+" engine")
	}
	rows := []string{row("API", api)}
	if kafka := health.Kafka; kafka != nil {
		rows = append(rows, row("Kafka", output.StatusBadge(kafka.Status)+"  "+output.LightStyle.Render(kafka.BootstrapServers)))
	}
	rows = append(rows, row("Configs", output.LightStyle.Render(fmt.Sprintf("%d test configs loaded", len(health.Tests)))))
	return strings.Join(rows, "\n")
}

// dashTrendLine is one sparkline of the Tests pane: its label, the last
// points that fit in room columns, and the latest value.
func dashTrendLine(label string, values []float64, higherIsBetter bool, unit string, room int) string {
	label = "  " + output.DimStyle.Render(label) + "  "
	if len(values) <= 1 {
		return label + output.DimStyle.Render("collecting"+output.Glyphs().Arrow)
	}
	if room = max(room, 8); len(values) > room {
		values = values[len(values)-room:]
	}
	latest := values[len(values)-1]
	value := output.SuccessStyle.Render(fmtNum(latest))
	if !higherIsBetter {
		value = output.WarningStyle.Render(fmtFloat(latest, 1))
	}
	return label + output.SparklineColored(values, higherIsBetter) + "  " + value + unit
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
		fmt.Fprint(output.Out, renderDashFrame(s, h, termWidth(), 0)+dashFooter(tick, interval, "Ctrl+C to stop"))
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

var (
	dashQuitKey   = key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit"))
	dashLookKey   = key.NewBinding(key.WithKeys("T"), key.WithHelp("T (shift+t)", "next look"))
	dashColorsKey = key.NewBinding(key.WithKeys("C"), key.WithHelp("C (shift+c)", "next colors"))
)

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
		switch {
		case key.Matches(msg, dashQuitKey):
			m.quitting = true
			return m, tea.Quit
		case key.Matches(msg, dashLookKey):
			theme.NextLook()
		case key.Matches(msg, dashColorsKey):
			theme.NextPalette()
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
	status := fmt.Sprintf("  %s Refreshing every %ds", spinnerFrame(m.frames-1), int(m.interval/time.Second))
	view := renderDashFrame(*m.snap, m.hist, width, m.height) + status +
		tui.HelpLines([]key.Binding{dashLookKey, dashColorsKey, dashQuitKey}, width)
	return theme.Screen(view, m.width, m.height)
}

// dashSummaryContent is the Test Summary panel. A run list the API did not
// return is unknown, not empty, so every count reads — rather than 0.
func dashSummaryContent(paged *client.PagedTests, err error, ruleWidth int) string {
	unknown := output.DimStyle.Render(fmt.Sprintf("%3s", glyphOr("—", "-")))
	running, pending, done, failed, total := unknown, unknown, unknown, unknown, unknown
	if err == nil && paged != nil {
		counts := CountStatuses(paged.Content)
		running = output.AccentStyle.Bold(true).Render(fmt.Sprintf("%3d", counts.Running))
		pending = output.WarningStyle.Render(fmt.Sprintf("%3d", counts.Pending))
		done = output.SuccessStyle.Render(fmt.Sprintf("%3d", counts.Done))
		failed = output.DimStyle.Render(fmt.Sprintf("%3d", counts.Failed))
		if counts.Failed > 0 {
			failed = output.ErrorStyle.Render(fmt.Sprintf("%3d", counts.Failed))
		}
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

		// Pad the plain type, then style it; the columns run from most to
		// least telling, so a narrow pane cuts the least.
		line := fmt.Sprintf("  %s %s %s",
			output.AccentStyle.Render(g.DotOn),
			output.LightStyle.Bold(true).Render(fmt.Sprintf("%-9s", t.TestType)),
			output.DimStyle.Render(shortID(t.ID)),
		)
		if status != "RUNNING" {
			line += "  " + output.DimStyle.Render(strings.ToLower(status))
		}
		if len(t.Results) > 0 {
			r := t.Results[len(t.Results)-1]
			latestThroughput += r.ThroughputRecordsPerSec
			latestLatency = r.P99LatencyMs
			line += fmt.Sprintf("  %s  %s  %s",
				output.SuccessStyle.Render(fmt.Sprintf("%12s", fmtNum(r.ThroughputRecordsPerSec)+" rec/s")),
				output.WarningStyle.Render(fmt.Sprintf("%8s", fmtFloat(r.P99LatencyMs, 1)+" ms")),
				output.DimStyle.Render(fmtNum(r.RecordsSent)+" rec"),
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
		fmt.Fprintf(recentContent, "  %s %s %s  %s  %s\n",
			icon,
			output.LightStyle.Render(fmt.Sprintf("%-9s", t.TestType)),
			output.DimStyle.Render(shortID(t.ID)),
			output.DimStyle.Render(dashWhen(t.CreatedAt)),
			output.StatusBadge(status),
		)
	}
	if recentCount == 0 {
		recentContent.WriteString(output.DimStyle.Render("  No completed tests"))
	}
	return activeContent.String(), recentContent.String(), latestThroughput, latestLatency
}

// shortID is a run id's first 8 characters, enough to tell runs apart and
// to find one with kates test get.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// dashWhen is when a run was created: the time today, the date before.
func dashWhen(createdAt string) string {
	at, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return formatTime(createdAt)
	}
	if at = at.Local(); at.Format(time.DateOnly) == time.Now().Format(time.DateOnly) {
		return at.Format("15:04")
	}
	return at.Format("Jan 02")
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
