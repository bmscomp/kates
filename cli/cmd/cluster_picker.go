package cmd

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/bmscomp/kates/cli/pkg/cluster"
)

// pickerModel is a minimal bubbletea list for choosing a context.
//
// It is deliberately small: the choice is "which of these few clusters", and a
// heavier component would add scrolling, filtering, and pagination for a list
// that is almost always 2-4 items long.
type pickerModel struct {
	contexts []cluster.Context
	cursor   int
	chosen   string
	aborted  bool
}

func (m pickerModel) Init() tea.Cmd { return nil }

func (m pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch key.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.contexts)-1 {
			m.cursor++
		}
	case "enter":
		m.chosen = m.contexts[m.cursor].Name
		return m, tea.Quit
	// Aborting must be explicit and always available: this is the last gate
	// before we start writing to a cluster.
	case "q", "esc", "ctrl+c":
		m.aborted = true
		return m, tea.Quit
	}
	return m, nil
}

func (m pickerModel) View() string {
	if m.chosen != "" || m.aborted {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n  " + gateTitle.Render("Select a cluster to deploy to") + "\n\n")

	for i, c := range m.contexts {
		cursor := "  "
		name := c.Name
		if i == m.cursor {
			cursor = gateAccent.Render("▸ ")
			name = gateAccent.Bold(true).Render(name)
		} else {
			name = lipgloss.NewStyle().Render(name)
		}

		line := "  " + cursor + name
		// The current kubeconfig context is worth marking: it is what every
		// other kubectl command in this shell is already pointing at.
		if c.Current {
			line += gateDim.Render(" (current)")
		}
		line += gateDim.Render(describeContext(c))
		b.WriteString(line + "\n")
	}

	b.WriteString("\n  " + gateDim.Render("↑/↓ move · enter select · q abort") + "\n")
	return b.String()
}

// pickContext runs the picker. Callers must ensure a TTY first — see
// isInteractive.
func pickContext(contexts []cluster.Context) (string, error) {
	// Start on the current context: it is the likeliest intent, and it means
	// enter-without-reading picks what kubectl would have picked.
	start := 0
	for i, c := range contexts {
		if c.Current {
			start = i
			break
		}
	}

	m, err := tea.NewProgram(pickerModel{contexts: contexts, cursor: start}).Run()
	if err != nil {
		return "", fmt.Errorf("cluster picker: %w", err)
	}

	final := m.(pickerModel)
	if final.aborted || final.chosen == "" {
		return "", fmt.Errorf("no cluster selected")
	}

	fmt.Println("  " + gateOK.Render("✓") + " Cluster: " + gateAccent.Render(final.chosen))
	return final.chosen, nil
}

// confirmModel is a yes/no prompt.
type confirmModel struct {
	question string
	yes      bool
	answered bool
}

func (m confirmModel) Init() tea.Cmd { return nil }

func (m confirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "y", "Y":
		m.yes, m.answered = true, true
		return m, tea.Quit
	// Default to no on anything else that ends the prompt: creating a cluster
	// should never happen because someone hit the wrong key.
	case "n", "N", "enter", "q", "esc", "ctrl+c":
		m.yes, m.answered = false, true
		return m, tea.Quit
	}
	return m, nil
}

func (m confirmModel) View() string {
	if m.answered {
		return ""
	}
	return "  " + m.question + gateDim.Render(" [y/N] ")
}

// confirmPrompt asks a yes/no question. Callers must ensure a TTY first.
func confirmPrompt(question string) (bool, error) {
	m, err := tea.NewProgram(confirmModel{question: question}).Run()
	if err != nil {
		return false, fmt.Errorf("confirm prompt: %w", err)
	}
	return m.(confirmModel).yes, nil
}
