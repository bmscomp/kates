package detect

import (
	"fmt"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/klster/kates-cli/output"
)

type SpinnerModel struct {
	spinner  spinner.Model
	quitting bool
}

func NewSpinnerModel() SpinnerModel {
	return SpinnerModel{
		spinner: spinner.New(spinner.WithSpinner(spinner.Dot)),
	}
}

func (m SpinnerModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m SpinnerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m, tea.Quit
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	default:
		return m, nil
	}
}

func (m SpinnerModel) View() string {
	if m.quitting {
		return ""
	}
	return fmt.Sprintf("\n  %s Introspecting cluster state...\n", lipgloss.NewStyle().Foreground(output.Cyan).Render(m.spinner.View()))
}
