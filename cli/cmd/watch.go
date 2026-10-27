package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

var watchInterval int

var testWatchCmd = &cobra.Command{
	Use:   "watch <id>",
	Short: "Live-watch a running test until completion",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]

		// One follower for the whole test family. `watch` used to be its own
		// clear-screen loop — a second implementation with its own rendering,
		// its own polling, and (until wave 1) DIFFERENT exit semantics from
		// `create --wait`. pollUntilDone gives the rich TUI on a terminal and
		// the append-only line format everywhere else.
		if watchInterval > 0 {
			origInterval := pollInterval
			pollInterval = time.Duration(watchInterval) * time.Second
			defer func() { pollInterval = origInterval }()
		}

		status, err := pollUntilDone(commandContext(cmd), id)
		if errors.Is(err, errStoppedWaiting) {
			// Watching someone's run and stopping is not a failure: the
			// test carries on, and this says so.
			state := "running"
			if status != "" {
				state = strings.ToLower(status)
			}
			output.Hint(fmt.Sprintf("Stopped watching; test %s is still %s. Resume: kates test watch %s",
				truncID(id), state, id))
			return nil
		}
		if err != nil {
			return cmdErr("Lost track of test " + truncID(id) + ": " + err.Error())
		}
		if isFailedStatus(status) {
			return cmdErr("Test failed — details: kates test get " + id)
		}
		output.Hint(fmt.Sprintf("View report: kates report show %s", id))
		return nil
	},
}

var createWait bool

type pollTickMsg time.Time

type pollResultMsg struct {
	test *client.TestRun
	err  error
}

type pollDoneMsg struct {
	summary *client.ReportSummary
}

const maxStaleRetries = 5
const maxConnRetries = 10

// sparklinePoints is how many polls the throughput sparkline shows.
const sparklinePoints = 40

type pollModel struct {
	ctx            context.Context
	id             string
	progress       progress.Model
	elapsed        time.Duration
	startTime      time.Time
	throughputHist []float64
	lastStatus     string
	totalRecords   float64
	recordsSent    float64
	phases         []client.PhaseResult
	done           bool
	failed         bool
	noData         bool
	staleRetries   int
	connRetries    int
	// retryAt is when the next poll may go after a failed one: the ticks
	// before it only redraw, so a failing API is polled on the same backoff
	// followRun uses (nextPollDelay) rather than on every tick.
	retryAt   time.Time
	lastErr   error
	repairErr error
	summary   *client.ReportSummary
	err       error
}

func newPollModel(ctx context.Context, id string) pollModel {
	p := progress.New(
		progress.WithDefaultGradient(),
		progress.WithWidth(40),
	)
	return pollModel{
		ctx:       ctx,
		id:        id,
		progress:  p,
		startTime: time.Now(),
	}
}

func (m pollModel) Init() tea.Cmd {
	return tea.Batch(
		m.fetchTest(),
		m.tickCmd(),
	)
}

func (m pollModel) tickCmd() tea.Cmd {
	// pollInterval, not a fixed 2 s: --interval set it and the TUI ignored it.
	return tea.Tick(pollInterval, func(t time.Time) tea.Msg {
		return pollTickMsg(t)
	})
}

func (m pollModel) fetchTest() tea.Cmd {
	return func() tea.Msg {
		result, err := apiClient.GetTest(m.ctx, m.id)
		return pollResultMsg{test: result, err: err}
	}
}

func (m pollModel) fetchSummary() tea.Cmd {
	return func() tea.Msg {
		summary, _ := apiClient.ReportSummary(m.ctx, m.id)
		return pollDoneMsg{summary: summary}
	}
}

func (m pollModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}

	case pollTickMsg:
		m.elapsed = time.Since(m.startTime)
		if m.done || m.failed {
			return m, nil
		}
		if time.Time(msg).Before(m.retryAt) {
			return m, m.tickCmd()
		}
		return m, tea.Batch(m.fetchTest(), m.tickCmd())

	case pollResultMsg:
		if msg.err != nil {
			cmd := m.onPollError(msg.err)
			return m, cmd
		}
		cmd := m.onTest(msg.test)
		return m, cmd

	case pollDoneMsg:
		m.summary = msg.summary
		return m, tea.Quit

	case progress.FrameMsg:
		progressModel, cmd := m.progress.Update(msg)
		m.progress = progressModel.(progress.Model)
		return m, cmd
	}
	return m, nil
}

// onPollError handles a failed poll: a repaired port-forward polls again at
// once, maxConnRetries failures in a row end the watch, and anything less
// waits out the backoff before the next poll.
func (m *pollModel) onPollError(pollErr error) tea.Cmd {
	repaired, repairErr := tryRepairPollingConnection(m.ctx, pollErr, m.connRetries)
	if repaired {
		m.lastErr = nil
		m.repairErr = nil
		return m.fetchTest()
	}
	if repairErr != nil {
		m.repairErr = repairErr
	}
	m.connRetries++
	m.lastErr = pollErrorWithRepair(pollErr, m.repairErr)
	if m.connRetries >= maxConnRetries {
		m.err = fmt.Errorf("connection lost: could not read the run's status %d times in a row: %w", m.connRetries, m.lastErr)
		return tea.Quit
	}
	m.retryAt = time.Now().Add(nextPollDelay(pollInterval, m.connRetries, m.lastErr))
	return nil
}

// onTest records a polled run: its phases, records sent and throughput, and
// whether it has finished. A run that succeeded with stale results is polled
// up to maxStaleRetries more times before it counts as one with no data.
func (m *pollModel) onTest(test *client.TestRun) tea.Cmd {
	m.connRetries = 0
	m.retryAt = time.Time{}
	m.lastErr = nil
	m.repairErr = nil
	m.lastStatus = strings.ToUpper(test.Status)
	m.phases = test.Results

	var totalSent float64
	var maxThroughput float64
	for _, r := range test.Results {
		totalSent += r.RecordsSent
		if r.ThroughputRecordsPerSec > maxThroughput {
			maxThroughput = r.ThroughputRecordsPerSec
		}
	}
	m.recordsSent = totalSent
	m.throughputHist = append(m.throughputHist, maxThroughput)
	// The whole run's history grew without bound: an hour in, the
	// sparkline was 1,823 columns and the live number was cut off.
	if len(m.throughputHist) > sparklinePoints {
		m.throughputHist = m.throughputHist[len(m.throughputHist)-sparklinePoints:]
	}

	if test.Spec != nil && test.Spec.Records > 0 {
		m.totalRecords = float64(test.Spec.Records)
	}

	switch {
	case succeededRunStatus(m.lastStatus):
		if isStaleResult(test.Results) && m.staleRetries < maxStaleRetries {
			m.staleRetries++
			return nil
		}
		m.done = true
		m.noData = isStaleResult(test.Results)
		return m.fetchSummary()
	case isFailedStatus(m.lastStatus):
		m.failed = true
		return tea.Quit
	}
	return nil
}

func (m pollModel) View() string {
	if m.err != nil {
		return output.ErrorStyle.Render("  ✖ Polling failed: "+m.err.Error()) + "\n"
	}

	var b strings.Builder
	b.WriteString("\n")
	m.viewProgress(&b)
	m.viewPhases(&b)
	switch {
	case m.done && m.noData:
		viewNoData(&b)
	case m.done:
		m.viewCompleted(&b)
	}
	if m.failed {
		fmt.Fprintf(&b, "\n  %s\n", output.ErrorStyle.Render("Test failed"))
	}
	if !m.done && !m.failed {
		m.viewPolling(&b)
	}
	return b.String()
}

// viewProgress writes the header line, the progress bar, the elapsed time and
// records sent, and the throughput sparkline once there are two points.
func (m pollModel) viewProgress(b *strings.Builder) {
	statusBadge := output.AccentStyle.Render(m.lastStatus)
	if m.done {
		statusBadge = output.SuccessStyle.Render("✓ COMPLETED")
	} else if m.failed {
		statusBadge = output.ErrorStyle.Render("✖ FAILED")
	}

	fmt.Fprintf(b, "  %s  %s  %s\n\n",
		output.AccentStyle.Render("Test"),
		output.DimStyle.Render(truncID(m.id)),
		statusBadge,
	)

	pct := m.progressPercent()
	bar := m.progress.ViewAs(pct)
	fmt.Fprintf(b, "  %s  %s\n",
		bar,
		output.DimStyle.Render(fmt.Sprintf("%.0f%%", pct*100)),
	)

	elapsedStr := m.elapsed.Truncate(time.Second).String()
	recordsStr := fmtNum(m.recordsSent)
	fmt.Fprintf(b, "  %s %s  %s %s",
		output.DimStyle.Render("Elapsed:"),
		elapsedStr,
		output.DimStyle.Render("Records:"),
		recordsStr,
	)
	if m.totalRecords > 0 {
		fmt.Fprintf(b, " / %s", fmtNum(m.totalRecords))
	}
	b.WriteString("\n")

	if len(m.throughputHist) > 1 {
		spark := output.Sparkline(m.throughputHist)
		latest := m.throughputHist[len(m.throughputHist)-1]
		fmt.Fprintf(b, "  %s %s %s\n",
			output.DimStyle.Render("Throughput:"),
			spark,
			fmtNum(latest)+" rec/s",
		)
	}
}

// viewPhases writes one line per phase, and the phase's error under it.
func (m pollModel) viewPhases(b *strings.Builder) {
	if len(m.phases) == 0 {
		return
	}
	b.WriteString("\n")
	for _, r := range m.phases {
		phase := r.PhaseName
		if phase == "" {
			phase = "main"
		}
		fmt.Fprintf(b, "  %-12s %s  %s rec/s  p99=%sms\n",
			phase,
			phaseStatusBadge(r.Status),
			fmtFloat(r.ThroughputRecordsPerSec, 1),
			fmtFloat(r.P99LatencyMs, 2),
		)
		// The API sends the reason with the status. Printing only "FAILED"
		// left you watching a red word for the rest of the run and reaching
		// for kubectl to find out what it meant.
		if r.Error != "" {
			fmt.Fprintf(b, "  %s\n", output.DimStyle.Render("   └ "+truncate(r.Error, 100)))
		}
	}
}

// phaseStatusBadge styles a phase status: a tick when it finished, a dot
// while it runs, a cross when it failed, and dim text otherwise.
func phaseStatusBadge(status string) string {
	switch {
	case strings.EqualFold(status, "DONE") || strings.EqualFold(status, "COMPLETED"):
		return output.SuccessStyle.Render("✓ " + status)
	case strings.EqualFold(status, "RUNNING"):
		return output.AccentStyle.Render("● " + status)
	case strings.EqualFold(status, "FAILED"):
		return output.ErrorStyle.Render("✖ " + status)
	default:
		return output.DimStyle.Render(status)
	}
}

func viewNoData(b *strings.Builder) {
	fmt.Fprintf(b, "\n  %s\n", output.WarningStyle.Render("⚠ Test completed but produced no data (0 records sent)"))
	fmt.Fprintf(b, "  %s\n", output.DimStyle.Render("Possible causes:"))
	fmt.Fprintf(b, "  %s\n", output.DimStyle.Render("  • Backend pod restarted (in-memory state lost)"))
	fmt.Fprintf(b, "  %s\n", output.DimStyle.Render("  • Kafka producer/consumer failed to connect"))
	fmt.Fprintf(b, "  %s\n", output.DimStyle.Render("  • Check logs: kubectl logs -n kates -l app=kates --tail=50"))
}

func (m pollModel) viewCompleted(b *strings.Builder) {
	throughput, p99, errorRate := m.completedTotals()
	fmt.Fprintf(b, "\n  %s\n", output.SuccessStyle.Render("Test completed successfully"))
	fmt.Fprintf(b, "  Throughput: %s rec/s  │  P99: %s ms  │  Errors: %.4f%%\n",
		fmtNum(throughput),
		fmtFloat(p99, 2),
		errorRate,
	)
	fmt.Fprintf(b, "  %s\n",
		output.DimStyle.Render(fmt.Sprintf("Full report: kates report show %s", m.id)),
	)
}

// completedTotals gives a finished run's throughput, p99 and error rate (in
// percent): the report summary's when it has a throughput, otherwise the
// best throughput and worst p99 of the phases, with no error rate.
func (m pollModel) completedTotals() (throughput, p99, errorRate float64) {
	if m.summary != nil && m.summary.AvgThroughputRecPerSec > 0 {
		return m.summary.AvgThroughputRecPerSec, m.summary.P99LatencyMs, m.summary.ErrorRate * 100
	}
	for _, r := range m.phases {
		if r.ThroughputRecordsPerSec > throughput {
			throughput = r.ThroughputRecordsPerSec
		}
		if r.P99LatencyMs > p99 {
			p99 = r.P99LatencyMs
		}
	}
	return throughput, p99, 0
}

func (m pollModel) viewPolling(b *strings.Builder) {
	if m.connRetries > 0 {
		fmt.Fprintf(b, "\n  %s\n",
			output.WarningStyle.Render(fmt.Sprintf("⚠ Connection error (retry %d/%d)...", m.connRetries, maxConnRetries)),
		)
		return
	}
	fmt.Fprintf(b, "\n  %s\n",
		output.DimStyle.Render(fmt.Sprintf("Polling every %s · q to detach", pollInterval.Truncate(time.Millisecond))),
	)
}

func (m pollModel) progressPercent() float64 {
	if m.done {
		return 1.0
	}
	if m.failed {
		return 0.0
	}
	if m.totalRecords > 0 && m.recordsSent > 0 {
		pct := m.recordsSent / m.totalRecords
		if pct > 0.99 {
			pct = 0.99
		}
		return pct
	}
	elapsed := m.elapsed.Seconds()
	if elapsed < 5 {
		return 0.05
	}
	pct := elapsed / (elapsed + 30)
	if pct > 0.95 {
		pct = 0.95
	}
	return pct
}

func isStaleResult(results []client.PhaseResult) bool {
	if len(results) == 0 {
		return true
	}
	for _, r := range results {
		if r.RecordsSent > 0 {
			return false
		}
	}
	return true
}

// Seams for tests: the plain poll loop must be drivable without a server or a
// real clock.
var (
	pollGetTestFn = func(ctx context.Context, id string) (*client.TestRun, error) {
		return apiClient.GetTest(ctx, id)
	}
	repairPollingConnectionFn = repairLocalAPIPortForward
	pollInterval              = 2 * time.Second
)

// pollUntilDone follows a test until it reaches a terminal state and returns
// the final status ("COMPLETED", "FAILED", …). A non-nil error means we lost
// the ability to follow (connection lost, UI failure) — the test's real
// outcome is unknown, which callers must NOT report as success.
//
// The previous version returned nothing: the TUI rendered "Test failed" and
// the command exited 0 anyway, so CI stayed green on failed load tests.
//
// Without a terminal this skips bubbletea entirely — tea cannot open /dev/tty
// there, so the old path turned "no TTY" into a crash before any status was
// ever polled — and runs a plain append-only loop instead.
//
// A follow stopped before the test finished, by q or Ctrl-C in the terminal
// UI or by ctx, returns the last status seen and errStoppedWaiting. That used
// to be a nil error, so `create --wait` exited 0 on Ctrl-C with its test
// still running; each caller now decides what stopping means for it.
func pollUntilDone(ctx context.Context, id string) (string, error) {
	if !IsInteractive() {
		return pollUntilDonePlain(ctx, id, os.Stdout)
	}
	m := newPollModel(ctx, id)
	final, err := tea.NewProgram(m, tea.WithContext(ctx)).Run()
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, tea.ErrInterrupted) || errors.Is(err, tea.ErrProgramKilled) {
			return lastPolledStatus(final), errStoppedWaiting
		}
		return "", fmt.Errorf("watch UI error: %w", err)
	}
	fm := final.(pollModel)
	if fm.err != nil {
		return "", fm.err
	}
	// The UI quits by itself once the test ends. Quitting before that was
	// the user (q, Ctrl-C), or Bubble Tea on SIGTERM.
	if !fm.done && !fm.failed {
		return fm.lastStatus, errStoppedWaiting
	}
	return fm.lastStatus, nil
}

func lastPolledStatus(m tea.Model) string {
	if pm, ok := m.(pollModel); ok {
		return pm.lastStatus
	}
	return ""
}

// pollUntilDonePlain is the non-interactive follower: one line per status
// change, append-only, no escape codes — the format CI logs can rely on.
func pollUntilDonePlain(ctx context.Context, id string, w io.Writer) (string, error) {
	lastStatus := ""
	var repairErr error
	run, err := followRun(ctx, id, followOpts{
		interval:  pollInterval,
		maxErrors: maxConnRetries,
		onPoll: func(test *client.TestRun) {
			status := strings.ToUpper(test.Status)
			if status == lastStatus {
				return
			}
			var sent float64
			var throughput float64
			for _, r := range test.Results {
				sent += float64(r.RecordsSent)
				if r.ThroughputRecordsPerSec > throughput {
					throughput = r.ThroughputRecordsPerSec
				}
			}
			fmt.Fprintf(w, "%s  test=%s  status=%s  records=%.0f  throughput=%.0f rec/s\n",
				time.Now().Format(time.RFC3339), truncID(id), status, sent, throughput)
			lastStatus = status
		},
		onError: func(ctx context.Context, err error) (bool, error) {
			repaired, nextRepairErr := tryRepairPollingConnection(ctx, err, 0)
			if repaired {
				repairErr = nil
				return true, nil
			}
			if nextRepairErr != nil {
				repairErr = nextRepairErr
			}
			return false, pollErrorWithRepair(err, repairErr)
		},
	})
	if errors.Is(err, errStoppedWaiting) {
		// A poll that answered before the stop is still reported: a run that
		// finished as the stop came has nothing left to stop.
		return lastStatus, err
	}
	if err != nil {
		return "", err
	}
	return strings.ToUpper(run.Status), nil
}

func tryRepairPollingConnection(ctx context.Context, pollErr error, _ int) (bool, error) {
	repaired, repairErr := repairPollingConnectionFn(ctx, pollErr)
	if repairErr != nil {
		return false, repairErr
	}
	if repaired {
		return true, nil
	}
	return false, nil
}

func pollErrorWithRepair(pollErr, repairErr error) error {
	if repairErr == nil {
		return pollErr
	}
	return fmt.Errorf("%w; port-forward repair failed: %v", pollErr, repairErr)
}

// sleepCtx sleeps for d, or until ctx is done; it reports whether it slept
// the whole time.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func init() {
	testWatchCmd.Flags().IntVar(&watchInterval, "interval", 3, "Refresh interval in seconds")
	testCmd.AddCommand(testWatchCmd)
}
