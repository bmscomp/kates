package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var cdcPhaseOrder = []string{
	"DB_SETUP",
	"TOPIC_CREATE",
	"SOURCE_DEPLOY",
	"SINK_DEPLOY",
	"CONNECTOR_READY",
	"KAFKA_VERIFY",
	"SINK_VERIFY",
	"CLEANUP",
}

var cdcPhaseLabels = map[string]string{
	"DB_SETUP":        "Database Setup",
	"TOPIC_CREATE":    "KafkaTopic Created",
	"SOURCE_DEPLOY":   "Source Connector",
	"SINK_DEPLOY":     "Sink Connector",
	"CONNECTOR_READY": "Connectors Ready",
	"KAFKA_VERIFY":    "Kafka Verification",
	"SINK_VERIFY":     "Sink Verification",
	"CLEANUP":         "Cleanup",
}

var connectTestCmd = &cobra.Command{
	Use:   "test",
	Short: "Run a backend integration test against Kafka Connect and PostgreSQL CDC",
	RunE: func(cmd *cobra.Command, args []string) error {
		output.Header("Kafka Connect CDC Integration Test")

		req := &client.CreateTestRequest{
			TestType: "INTEGRATION_CDC",
		}

		result, err := apiClient.CreateTest(context.Background(), req)
		if err != nil {
			return cmdErr("Failed to start backend integration test: " + err.Error())
		}

		output.Success("Backend integration test started successfully")
		output.KeyValue("ID", result.ID)
		output.KeyValue("Type", result.TestType)

		// Use CDC-specific progress if TTY is available, otherwise fall back
		if term.IsTerminal(int(os.Stdin.Fd())) {
			pollCdcUntilDone(result.ID)
		} else {
			pollCdcPlain(result.ID)
		}

		return nil
	},
}

// ---------- Bubble Tea CDC model ----------

type cdcPollMsg time.Time
type cdcResultMsg struct {
	test *client.TestRun
	err  error
}

type cdcModel struct {
	id           string
	startTime    time.Time
	elapsed      time.Duration
	currentPhase string
	phases       map[string]int64 // phase -> duration ms
	done         bool
	failed       bool
	errMsg       string
	connRetries  int
}

func newCdcModel(id string) cdcModel {
	return cdcModel{
		id:        id,
		startTime: time.Now(),
		phases:    make(map[string]int64),
	}
}

func (m cdcModel) Init() tea.Cmd {
	return tea.Batch(m.fetchTest(), m.tickCmd())
}

func (m cdcModel) tickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return cdcPollMsg(t)
	})
}

func (m cdcModel) fetchTest() tea.Cmd {
	return func() tea.Msg {
		result, err := apiClient.GetTest(context.Background(), m.id)
		return cdcResultMsg{test: result, err: err}
	}
}

func (m cdcModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "q" || msg.String() == "ctrl+c" {
			return m, tea.Quit
		}

	case cdcPollMsg:
		m.elapsed = time.Since(m.startTime)
		if m.done || m.failed {
			return m, nil
		}
		return m, tea.Batch(m.fetchTest(), m.tickCmd())

	case cdcResultMsg:
		if msg.err != nil {
			m.connRetries++
			if m.connRetries > 10 {
				m.failed = true
				m.errMsg = "Connection lost: " + msg.err.Error()
				return m, tea.Quit
			}
			return m, nil
		}
		m.connRetries = 0
		test := msg.test

		// Update phases from the test run
		if test.CdcPhases != nil {
			for k, v := range test.CdcPhases {
				m.phases[k] = v
			}
		}
		if test.CdcPhase != "" {
			m.currentPhase = test.CdcPhase
		}

		status := strings.ToUpper(test.Status)
		switch status {
		case "DONE", "COMPLETED":
			m.done = true
			return m, tea.Quit
		case "FAILED", "ERROR":
			m.failed = true
			// Extract error from results
			for _, r := range test.Results {
				if r.Error != "" {
					m.errMsg = r.Error
					break
				}
			}
			if m.errMsg == "" {
				m.errMsg = "Test failed (check backend logs)"
			}
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m cdcModel) View() string {
	var b strings.Builder

	b.WriteString("\n")

	for _, phase := range cdcPhaseOrder {
		label := cdcPhaseLabels[phase]
		duration, completed := m.phases[phase]

		if completed {
			// Phase completed
			b.WriteString(fmt.Sprintf("  %s %-24s %s\n",
				output.SuccessStyle.Render("✓"),
				label,
				output.DimStyle.Render(fmt.Sprintf("%dms", duration)),
			))
		} else if phase == m.currentPhase {
			// Currently running
			b.WriteString(fmt.Sprintf("  %s %-24s %s\n",
				output.AccentStyle.Render("●"),
				output.AccentStyle.Render(label),
				output.DimStyle.Render("..."),
			))
		} else {
			// Pending
			b.WriteString(fmt.Sprintf("  %s %s\n",
				output.DimStyle.Render("○"),
				output.DimStyle.Render(label),
			))
		}
	}

	b.WriteString("\n")

	elapsedStr := m.elapsed.Truncate(time.Second).String()
	b.WriteString(fmt.Sprintf("  %s %s\n",
		output.DimStyle.Render("Elapsed:"),
		elapsedStr,
	))

	if m.done {
		totalMs := int64(0)
		for _, d := range m.phases {
			totalMs += d
		}
		b.WriteString(fmt.Sprintf("\n  %s\n",
			output.SuccessStyle.Render(fmt.Sprintf("✓ CDC integration test passed (%dms total)", totalMs)),
		))
	}

	if m.failed {
		b.WriteString(fmt.Sprintf("\n  %s\n",
			output.ErrorStyle.Render("✖ CDC integration test failed"),
		))
		if m.errMsg != "" {
			b.WriteString(fmt.Sprintf("  %s %s\n",
				output.DimStyle.Render("Error:"),
				m.errMsg,
			))
		}
	}

	if !m.done && !m.failed {
		if m.connRetries > 0 {
			b.WriteString(fmt.Sprintf("  %s\n",
				output.WarningStyle.Render(fmt.Sprintf("⚠ Connection error (retry %d/10)...", m.connRetries)),
			))
		} else {
			b.WriteString(fmt.Sprintf("  %s\n",
				output.DimStyle.Render("Polling every 2s · q to detach"),
			))
		}
	}

	return b.String()
}

func pollCdcUntilDone(id string) {
	m := newCdcModel(id)
	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		output.Error("Watch error: " + err.Error())
	}
}

// pollCdcPlain is the non-TTY fallback for CI environments
func pollCdcPlain(id string) {
	for {
		result, err := apiClient.GetTest(context.Background(), id)
		if err != nil {
			output.Error("Failed to fetch test: " + err.Error())
			time.Sleep(3 * time.Second)
			continue
		}

		status := strings.ToUpper(result.Status)

		switch status {
		case "DONE", "COMPLETED":
			output.Success("CDC integration test passed")
			if result.CdcPhases != nil {
				for _, phase := range cdcPhaseOrder {
					if d, ok := result.CdcPhases[phase]; ok {
						fmt.Printf("  ✓ %-24s %dms\n", cdcPhaseLabels[phase], d)
					}
				}
			}
			return
		case "FAILED", "ERROR":
			output.Error("CDC integration test failed")
			for _, r := range result.Results {
				if r.Error != "" {
					fmt.Printf("  Error: %s\n", r.Error)
				}
			}
			return
		default:
			phase := result.CdcPhase
			if phase == "" {
				phase = "starting"
			}
			fmt.Printf("  ● %s...\n", phase)
		}

		time.Sleep(3 * time.Second)
	}
}

func init() {
	kafkaConnectCmd.AddCommand(connectTestCmd)
}
