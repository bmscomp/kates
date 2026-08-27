package cmd

import (
	"context"
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

		status, err := pollUntilDone(id)
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

type pollModel struct {
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
	lastErr        error
	summary        *client.ReportSummary
	err            error
}

func newPollModel(id string) pollModel {
	p := progress.New(
		progress.WithDefaultGradient(),
		progress.WithWidth(40),
	)
	return pollModel{
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
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return pollTickMsg(t)
	})
}

func (m pollModel) fetchTest() tea.Cmd {
	return func() tea.Msg {
		result, err := apiClient.GetTest(context.Background(), m.id)
		return pollResultMsg{test: result, err: err}
	}
}

func (m pollModel) fetchSummary() tea.Cmd {
	return func() tea.Msg {
		summary, _ := apiClient.ReportSummary(context.Background(), m.id)
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
		return m, tea.Batch(m.fetchTest(), m.tickCmd())

	case pollResultMsg:
		if msg.err != nil {
			m.connRetries++
			m.lastErr = msg.err
			if m.connRetries > maxConnRetries {
				m.err = fmt.Errorf("connection lost after %d retries: %w", maxConnRetries, msg.err)
				return m, tea.Quit
			}
			return m, nil
		}
		m.connRetries = 0
		m.lastErr = nil
		test := msg.test
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

		if test.Spec != nil && test.Spec.Records > 0 {
			m.totalRecords = float64(test.Spec.Records)
		}

		switch m.lastStatus {
		case "DONE", "COMPLETED":
			if isStaleResult(test.Results) && m.staleRetries < maxStaleRetries {
				m.staleRetries++
				return m, nil
			}
			m.done = true
			m.noData = isStaleResult(test.Results)
			return m, m.fetchSummary()
		case "FAILED", "ERROR":
			m.failed = true
			return m, tea.Quit
		}

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

func (m pollModel) View() string {
	if m.err != nil {
		return output.ErrorStyle.Render("  ✖ Polling failed: "+m.err.Error()) + "\n"
	}

	var b strings.Builder

	b.WriteString("\n")

	statusBadge := output.AccentStyle.Render(m.lastStatus)
	if m.done {
		statusBadge = output.SuccessStyle.Render("✓ COMPLETED")
	} else if m.failed {
		statusBadge = output.ErrorStyle.Render("✖ FAILED")
	}

	b.WriteString(fmt.Sprintf("  %s  %s  %s\n\n",
		output.AccentStyle.Render("Test"),
		output.DimStyle.Render(truncID(m.id)),
		statusBadge,
	))

	pct := m.progressPercent()
	bar := m.progress.ViewAs(pct)
	b.WriteString(fmt.Sprintf("  %s  %s\n",
		bar,
		output.DimStyle.Render(fmt.Sprintf("%.0f%%", pct*100)),
	))

	elapsedStr := m.elapsed.Truncate(time.Second).String()
	recordsStr := fmtNum(m.recordsSent)
	b.WriteString(fmt.Sprintf("  %s %s  %s %s",
		output.DimStyle.Render("Elapsed:"),
		elapsedStr,
		output.DimStyle.Render("Records:"),
		recordsStr,
	))
	if m.totalRecords > 0 {
		b.WriteString(fmt.Sprintf(" / %s", fmtNum(m.totalRecords)))
	}
	b.WriteString("\n")

	if len(m.throughputHist) > 1 {
		spark := output.Sparkline(m.throughputHist)
		latest := m.throughputHist[len(m.throughputHist)-1]
		b.WriteString(fmt.Sprintf("  %s %s %s\n",
			output.DimStyle.Render("Throughput:"),
			spark,
			fmtNum(latest)+" rec/s",
		))
	}

	if len(m.phases) > 0 {
		b.WriteString("\n")
		for _, r := range m.phases {
			phase := r.PhaseName
			if phase == "" {
				phase = "main"
			}
			phaseStatus := output.DimStyle.Render(r.Status)
			if strings.EqualFold(r.Status, "DONE") || strings.EqualFold(r.Status, "COMPLETED") {
				phaseStatus = output.SuccessStyle.Render("✓ " + r.Status)
			} else if strings.EqualFold(r.Status, "RUNNING") {
				phaseStatus = output.AccentStyle.Render("● " + r.Status)
			} else if strings.EqualFold(r.Status, "FAILED") {
				phaseStatus = output.ErrorStyle.Render("✖ " + r.Status)
			}
			b.WriteString(fmt.Sprintf("  %-12s %s  %s rec/s  p99=%sms\n",
				phase,
				phaseStatus,
				fmtFloat(r.ThroughputRecordsPerSec, 1),
				fmtFloat(r.P99LatencyMs, 2),
			))
			// The API sends the reason with the status. Printing only "FAILED"
			// left you watching a red word for the rest of the run and reaching
			// for kubectl to find out what it meant.
			if r.Error != "" {
				b.WriteString(fmt.Sprintf("  %s\n", output.DimStyle.Render("   └ "+truncate(r.Error, 100))))
			}
		}
	}

	if m.done && m.noData {
		b.WriteString(fmt.Sprintf("\n  %s\n", output.WarningStyle.Render("⚠ Test completed but produced no data (0 records sent)")))
		b.WriteString(fmt.Sprintf("  %s\n", output.DimStyle.Render("Possible causes:")))
		b.WriteString(fmt.Sprintf("  %s\n", output.DimStyle.Render("  • Backend pod restarted (in-memory state lost)")))
		b.WriteString(fmt.Sprintf("  %s\n", output.DimStyle.Render("  • Kafka producer/consumer failed to connect")))
		b.WriteString(fmt.Sprintf("  %s\n", output.DimStyle.Render("  • Check logs: kubectl logs -n kates -l app=kates --tail=50")))
	} else if m.done {
		throughput := 0.0
		p99 := 0.0
		errorRate := 0.0
		if m.summary != nil && m.summary.AvgThroughputRecPerSec > 0 {
			throughput = m.summary.AvgThroughputRecPerSec
			p99 = m.summary.P99LatencyMs
			errorRate = m.summary.ErrorRate * 100
		} else {
			for _, r := range m.phases {
				if r.ThroughputRecordsPerSec > throughput {
					throughput = r.ThroughputRecordsPerSec
				}
				if r.P99LatencyMs > p99 {
					p99 = r.P99LatencyMs
				}
			}
		}
		b.WriteString(fmt.Sprintf("\n  %s\n", output.SuccessStyle.Render("Test completed successfully")))
		b.WriteString(fmt.Sprintf("  Throughput: %s rec/s  │  P99: %s ms  │  Errors: %.4f%%\n",
			fmtNum(throughput),
			fmtFloat(p99, 2),
			errorRate,
		))
		b.WriteString(fmt.Sprintf("  %s\n",
			output.DimStyle.Render(fmt.Sprintf("Full report: kates report show %s", m.id)),
		))
	}

	if m.failed {
		b.WriteString(fmt.Sprintf("\n  %s\n", output.ErrorStyle.Render("Test failed")))
	}

	if !m.done && !m.failed {
		if m.connRetries > 0 {
			b.WriteString(fmt.Sprintf("\n  %s\n",
				output.WarningStyle.Render(fmt.Sprintf("⚠ Connection error (retry %d/%d)...", m.connRetries, maxConnRetries)),
			))
		} else {
			b.WriteString(fmt.Sprintf("\n  %s\n",
				output.DimStyle.Render("Polling every 2s · q to detach"),
			))
		}
	}

	return b.String()
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
	pollGetTestFn = func(id string) (*client.TestRun, error) {
		return apiClient.GetTest(context.Background(), id)
	}
	pollInterval = 2 * time.Second
)

// isFailedStatus reports whether a terminal test status means failure.
func isFailedStatus(status string) bool {
	return status == "FAILED" || status == "ERROR"
}

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
func pollUntilDone(id string) (string, error) {
	if !IsInteractive() {
		return pollUntilDonePlain(id, os.Stdout)
	}
	m := newPollModel(id)
	final, err := tea.NewProgram(m).Run()
	if err != nil {
		return "", fmt.Errorf("watch UI error: %w", err)
	}
	fm := final.(pollModel)
	if fm.err != nil {
		return "", fm.err
	}
	// A detach (q) surfaces the last status seen; only terminal failure
	// statuses are errors for callers.
	return fm.lastStatus, nil
}

// pollUntilDonePlain is the non-interactive follower: one line per status
// change, append-only, no escape codes — the format CI logs can rely on.
func pollUntilDonePlain(id string, w io.Writer) (string, error) {
	lastStatus := ""
	retries := 0
	for {
		test, err := pollGetTestFn(id)
		if err != nil {
			retries++
			if retries > maxConnRetries {
				return "", fmt.Errorf("connection lost after %d retries: %w", maxConnRetries, err)
			}
			time.Sleep(pollInterval)
			continue
		}
		retries = 0

		status := strings.ToUpper(test.Status)
		if status != lastStatus {
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
		}

		switch status {
		case "DONE", "COMPLETED", "FAILED", "ERROR":
			return status, nil
		}
		time.Sleep(pollInterval)
	}
}

func init() {
	testWatchCmd.Flags().IntVar(&watchInterval, "interval", 3, "Refresh interval in seconds")
	testCmd.AddCommand(testWatchCmd)
}
