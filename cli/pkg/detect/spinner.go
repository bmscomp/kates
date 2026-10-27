package detect

import (
	"fmt"

	"github.com/bmscomp/kates/cli/output"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type ProgressMsg string

type SpinnerModel struct {
	spinner  spinner.Model
	quitting bool
	message  string
}

func NewSpinnerModel() SpinnerModel {
	return SpinnerModel{
		spinner: spinner.New(spinner.WithSpinner(spinner.Dot)),
		message: "Introspecting cluster state...",
	}
}

func (m SpinnerModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m SpinnerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Ctrl-C, q or Esc stops the collection; any other key is ignored
		// rather than taken as a reason to stop.
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case ProgressMsg:
		m.message = string(msg)
		return m, nil
	default:
		return m, nil
	}
}

func (m SpinnerModel) View() string {
	if m.quitting {
		return ""
	}
	return fmt.Sprintf("\n  %s %s\n", lipgloss.NewStyle().Foreground(output.Cyan).Render(m.spinner.View()), m.message)
}
