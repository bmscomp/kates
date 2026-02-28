package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/klster/kates-cli/client"
	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var watchInterval int

var testWatchCmd = &cobra.Command{
	Use:   "watch <id>",
	Short: "Live-watch a running test until completion",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		tick := 0
		var throughputHistory []float64

		for {
			result, err := apiClient.GetTest(context.Background(), id)
			if err != nil {
				return cmdErr("Failed to fetch test: " + err.Error())
			}

			status := strings.ToUpper(result.Status)

			var currentThroughput float64
			for _, r := range result.Results {
				if r.ThroughputRecordsPerSec > currentThroughput {
					currentThroughput = r.ThroughputRecordsPerSec
				}
			}
			throughputHistory = append(throughputHistory, currentThroughput)

			fmt.Print("\033[2J\033[H")

			output.Banner("Test Watch", fmt.Sprintf("%s · %s", result.TestType, truncID(id)))

			output.SubHeader("Status")
			output.KeyValue("Test ID", id)
			output.KeyValue("Type", result.TestType)
			output.KeyValue("Backend", result.Backend)
			output.KeyValue("Status", output.StatusBadge(status))
			output.KeyValue("Created", formatTime(result.CreatedAt))

			if len(result.Results) > 0 {
				output.SubHeader(fmt.Sprintf("Results (%d phases)", len(result.Results)))
				rows := make([][]string, 0, len(result.Results))
				for _, r := range result.Results {
					phase := r.PhaseName
					if phase == "" {
						phase = "main"
					}
					rows = append(rows, []string{
						phase,
						r.Status,
						fmtNum(r.RecordsSent),
						fmtFloat(r.ThroughputRecordsPerSec, 1),
						fmtFloat(r.AvgLatencyMs, 2),
						fmtFloat(r.P99LatencyMs, 2),
					})
				}
				output.Table(
					[]string{"Phase", "Status", "Records", "Throughput", "Avg Lat.", "P99 Lat."},
					rows,
				)
			}

			if len(throughputHistory) > 1 {
				fmt.Println()
				sparkline := output.Sparkline(throughputHistory)
				output.KeyValue("Throughput Trend", sparkline+" "+fmtNum(currentThroughput)+" rec/s")
			}

			switch status {
			case "DONE", "COMPLETED":
				output.Success("Test completed successfully")
				output.Hint(fmt.Sprintf("View report: kates report show %s", id))
				return nil
			case "FAILED", "ERROR":
				return cmdErr("Test failed")
			default:
				fmt.Println()
				fmt.Printf("  %s Refreshing every %ds... (Ctrl+C to stop)\n",
					spinnerFrame(tick),
					watchInterval,
				)
			}

			tick++
			time.Sleep(time.Duration(watchInterval) * time.Second)
		}
	},
}

var testListWatchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Auto-refreshing test list",
	RunE: func(cmd *cobra.Command, args []string) error {
		tick := 0

		for {
			paged, err := apiClient.ListTests(context.Background(), testTypeFlag, testStatusFlag, testPageFlag, testSizeFlag)
			if err != nil {
				output.Error("Failed to list tests: " + err.Error())
				time.Sleep(time.Duration(watchInterval) * time.Second)
				tick++
				continue
			}

			fmt.Print("\033[2J\033[H")
			output.Header("Test Runs (live)")

			if len(paged.Content) == 0 {
				output.Hint("No test runs found.")
			} else {
				rows := make([][]string, 0, len(paged.Content))
				for _, run := range paged.Content {
					rows = append(rows, []string{
						truncID(run.ID),
						run.TestType,
						run.Status,
						run.Backend,
						formatTime(run.CreatedAt),
					})
				}
				output.Table([]string{"ID", "Type", "Status", "Backend", "Created"}, rows)
				output.Hint(fmt.Sprintf("%d items total", paged.TotalItems))
			}

			fmt.Printf("\n  %s Refreshing every %ds... (Ctrl+C to stop)\n",
				spinnerFrame(tick),
				watchInterval,
			)

			tick++
			time.Sleep(time.Duration(watchInterval) * time.Second)
		}
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
			m.err = msg.err
			return m, nil
		}
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
			m.done = true
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
			}
			b.WriteString(fmt.Sprintf("  %-12s %s  %s rec/s  p99=%sms\n",
				phase,
				phaseStatus,
				fmtFloat(r.ThroughputRecordsPerSec, 1),
				fmtFloat(r.P99LatencyMs, 2),
			))
		}
	}

	if m.done && m.summary != nil {
		s := m.summary
		b.WriteString(fmt.Sprintf("\n  %s\n", output.SuccessStyle.Render("Test completed successfully")))
		b.WriteString(fmt.Sprintf("  Throughput: %s rec/s  │  P99: %s ms  │  Errors: %.4f%%\n",
			fmtNum(s.AvgThroughputRecPerSec),
			fmtFloat(s.P99LatencyMs, 2),
			s.ErrorRate*100,
		))
		b.WriteString(fmt.Sprintf("  %s\n",
			output.DimStyle.Render(fmt.Sprintf("Full report: kates report show %s", m.id)),
		))
	}

	if m.failed {
		b.WriteString(fmt.Sprintf("\n  %s\n", output.ErrorStyle.Render("Test failed")))
	}

	if !m.done && !m.failed {
		b.WriteString(fmt.Sprintf("\n  %s\n",
			output.DimStyle.Render("Polling every 2s · q to detach"),
		))
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

func pollUntilDone(id string) {
	m := newPollModel(id)
	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		output.Error("Watch error: " + err.Error())
	}
}

func init() {
	testWatchCmd.Flags().IntVar(&watchInterval, "interval", 3, "Refresh interval in seconds")
	testCmd.AddCommand(testWatchCmd)
}
