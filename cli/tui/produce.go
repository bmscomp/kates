package tui

import (
	"context"
	"fmt"
	"github.com/charmbracelet/bubbles/textinput"
	"strings"

	"github.com/bmscomp/kates/cli/client"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type produceView int

const (
	produceTopicSelect produceView = iota
	produceInput
)

type produceModel struct {
	client      *client.Client
	topics      []client.KafkaTopic
	cursor      int
	view        produceView
	keyInput    textinput.Model
	valueInput  textinput.Model
	activeField int // 0=key, 1=value
	history     []producedRecord
	width       int
	height      int
	loading     bool
	err         error
	statusMsg   string
}

type producedRecord struct {
	Topic     string
	Key       string
	Partition string
	Offset    string
}

type produceTopicsMsg struct {
	topics []client.KafkaTopic
	err    error
}

type produceSentMsg struct {
	topic     string
	key       string
	partition string
	offset    string
	err       error
}

func newProduceModel(c *client.Client) produceModel {
	// bubbles/textinput instead of hand-rolled byte buffers: the old input
	// only accepted single-byte keystrokes, which silently dropped paste and
	// every non-ASCII rune ("é" is two bytes, so it never appeared).
	key := textinput.New()
	key.Placeholder = "optional"
	key.Prompt = "  "
	key.CharLimit = 0
	value := textinput.New()
	value.Placeholder = "record value"
	value.Prompt = "  "
	value.CharLimit = 0
	key.Focus()
	return produceModel{client: c, loading: true, keyInput: key, valueInput: value}
}

func (m produceModel) loadTopics() tea.Cmd {
	return func() tea.Msg {
		topics, err := m.client.KafkaTopics(context.Background())
		return produceTopicsMsg{topics: topics, err: err}
	}
}

func (m produceModel) sendRecord(topic, key, value string) tea.Cmd {
	return func() tea.Msg {
		meta, err := m.client.KafkaProduce(context.Background(), topic, key, value)
		if err != nil {
			return produceSentMsg{err: err}
		}
		return produceSentMsg{
			topic:     meta.Topic,
			key:       key,
			partition: fmt.Sprintf("%v", meta.Partition),
			offset:    fmt.Sprintf("%v", meta.Offset),
		}
	}
}

func (m produceModel) Init() tea.Cmd {
	return m.loadTopics()
}

func (m produceModel) Update(msg tea.Msg) (produceModel, tea.Cmd) {
	switch msg := msg.(type) {
	case produceTopicsMsg:
		m.loading = false
		m.topics = msg.topics
		m.err = msg.err

	case produceSentMsg:
		m.loading = false
		if msg.err != nil {
			m.statusMsg = errorStyle.Render("✖ " + msg.err.Error())
		} else {
			m.statusMsg = healthyStyle.Render(
				fmt.Sprintf("✓ Produced to %s → partition %s, offset %s", msg.topic, msg.partition, msg.offset))
			m.history = append(m.history, producedRecord{
				Topic: msg.topic, Key: msg.key, Partition: msg.partition, Offset: msg.offset,
			})
			if len(m.history) > 50 {
				m.history = m.history[len(m.history)-50:]
			}
			m.valueInput.SetValue("")
		}

	case tea.KeyMsg:
		switch m.view {
		case produceTopicSelect:
			return m.updateTopicSelect(msg)
		case produceInput:
			return m.updateInput(msg)
		}
	}
	return m, nil
}

func (m produceModel) updateTopicSelect(msg tea.KeyMsg) (produceModel, tea.Cmd) {
	count := len(m.topics)
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < count-1 {
			m.cursor++
		}
	case "enter":
		if m.cursor < count {
			m.view = produceInput
			m.activeField = 1
			m.statusMsg = ""
		}
	case "r":
		m.loading = true
		return m, m.loadTopics()
	}
	return m, nil
}

func (m produceModel) updateInput(msg tea.KeyMsg) (produceModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.view = produceTopicSelect
		m.keyInput.SetValue("")
		m.valueInput.SetValue("")
		m.statusMsg = ""
		return m, nil
	case "tab":
		m.activeField = (m.activeField + 1) % 2
		if m.activeField == 0 {
			m.keyInput.Focus()
			m.valueInput.Blur()
		} else {
			m.valueInput.Focus()
			m.keyInput.Blur()
		}
		return m, nil
	case "enter":
		if m.valueInput.Value() == "" {
			m.statusMsg = warnStyle.Render("Value cannot be empty")
			return m, nil
		}
		topic := m.topics[m.cursor].Name
		m.loading = true
		m.statusMsg = dimStyle.Render("Sending…")
		return m, m.sendRecord(topic, m.keyInput.Value(), m.valueInput.Value())
	}

	// Everything else — runes, paste, cursor keys, backspace — belongs to the
	// focused textinput, which handles multi-rune input correctly.
	var cmd tea.Cmd
	if m.activeField == 0 {
		m.keyInput, cmd = m.keyInput.Update(msg)
	} else {
		m.valueInput, cmd = m.valueInput.Update(msg)
	}
	return m, cmd
}

func (m produceModel) View() string {
	if m.loading && len(m.topics) == 0 {
		return dimStyle.Render("  Loading topics…")
	}
	if m.err != nil {
		return errorStyle.Render("  Error: " + m.err.Error())
	}

	switch m.view {
	case produceInput:
		return m.viewInput()
	default:
		return m.viewTopicSelect()
	}
}

func (m produceModel) viewTopicSelect() string {
	if len(m.topics) == 0 {
		return dimStyle.Render("  No topics found. Press r to refresh.")
	}

	listWidth := 40
	var list strings.Builder
	list.WriteString(dimStyle.Render(fmt.Sprintf("  Select Topic (%d)", len(m.topics))) + "\n\n")

	maxVisible := m.height - 8
	if maxVisible < 5 {
		maxVisible = 20
	}
	start := 0
	if m.cursor >= maxVisible {
		start = m.cursor - maxVisible + 1
	}
	count := len(m.topics)

	for vi := start; vi < count && vi < start+maxVisible; vi++ {
		t := m.topics[vi]
		label := t.Name
		if t.Internal {
			label += dimStyle.Render(" (internal)")
		}
		if vi == m.cursor {
			list.WriteString(selectedItemStyle.Render("▸ "+label) + "\n")
		} else {
			list.WriteString(listItemStyle.Render("  "+label) + "\n")
		}
	}

	listPane := lipgloss.NewStyle().Width(listWidth).Render(list.String())

	var detailStr string
	if m.cursor < len(m.topics) {
		t := m.topics[m.cursor]
		detailStr = detailTitleStyle.Render(t.Name) + "\n\n" +
			detailKeyStyle.Render("Partitions") + detailValueStyle.Render(fmt.Sprintf("%d", t.Partitions)) + "\n" +
			detailKeyStyle.Render("Rep. Factor") + detailValueStyle.Render(fmt.Sprintf("%d", t.ReplicationFactor)) + "\n\n" +
			dimStyle.Render("Enter: select for producing")
	}

	if len(m.history) > 0 {
		detailStr += "\n\n" + dimStyle.Render(fmt.Sprintf("History (%d sent)", len(m.history))) + "\n"
		start := 0
		if len(m.history) > 5 {
			start = len(m.history) - 5
		}
		for _, h := range m.history[start:] {
			detailStr += fmt.Sprintf("  %s p%s @%s  %s\n",
				healthyStyle.Render("✓"), h.Partition, h.Offset,
				dimStyle.Render(h.Topic))
		}
	}

	detailWidth := m.width - listWidth - 6
	if detailWidth < 20 {
		detailWidth = 40
	}
	detailPane := detailBorderStyle.Width(detailWidth).Render(detailStr)

	return lipgloss.JoinHorizontal(lipgloss.Top, listPane, detailPane)
}

func (m produceModel) viewInput() string {
	topicName := ""
	if m.cursor < len(m.topics) {
		topicName = m.topics[m.cursor].Name
	}

	keyLabel := dimStyle.Render("Key")
	valueLabel := dimStyle.Render("Value")
	if m.activeField == 0 {
		keyLabel = filterActiveStyle.Render("Key")
	} else {
		valueLabel = filterActiveStyle.Render("Value")
	}

	var b strings.Builder
	b.WriteString(detailTitleStyle.Render(fmt.Sprintf("Produce → %s", topicName)) + "\n\n")
	b.WriteString(keyLabel + "\n")
	b.WriteString(m.keyInput.View() + "\n\n")
	b.WriteString(valueLabel + "\n")
	b.WriteString(m.valueInput.View() + "\n\n")

	if m.statusMsg != "" {
		b.WriteString(m.statusMsg + "\n\n")
	}

	b.WriteString(dimStyle.Render("Tab: switch field · Enter: send · Esc: back"))

	return detailBorderStyle.Width(m.width - 4).Render(b.String())
}

func (m produceModel) helpCtx() helpContext {
	switch m.view {
	case produceInput:
		return helpProduce
	default:
		return helpMain
	}
}
