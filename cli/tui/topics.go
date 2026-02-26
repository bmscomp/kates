package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/klster/kates-cli/client"
)

type topicsView int

const (
	topicsList topicsView = iota
	topicsDetail
	topicsConsume
)

type topicsModel struct {
	client   *client.Client
	topics   []client.KafkaTopic
	detail   map[string]interface{}
	records  []client.KafkaRecord
	cursor   int
	view     topicsView
	filter   string
	filtered []int
	width    int
	height   int
	loading  bool
	err      error
}

type topicsLoadedMsg struct {
	topics []client.KafkaTopic
	err    error
}

type topicDetailMsg struct {
	detail map[string]interface{}
	err    error
}

type consumeTickMsg struct{}

type consumeResultMsg struct {
	records []client.KafkaRecord
	err     error
}

func newTopicsModel(c *client.Client) topicsModel {
	return topicsModel{client: c, loading: true}
}

func (m topicsModel) loadTopics() tea.Cmd {
	return func() tea.Msg {
		topics, err := m.client.KafkaTopics(context.Background())
		return topicsLoadedMsg{topics: topics, err: err}
	}
}

func (m topicsModel) loadDetail(name string) tea.Cmd {
	return func() tea.Msg {
		detail, err := m.client.KafkaTopicDetail(context.Background(), name)
		return topicDetailMsg{detail: detail, err: err}
	}
}

func (m topicsModel) consumeTick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg {
		return consumeTickMsg{}
	})
}

func (m topicsModel) fetchRecords(topic string) tea.Cmd {
	return func() tea.Msg {
		recs, err := m.client.KafkaConsume(context.Background(), topic, "latest", 20)
		return consumeResultMsg{records: recs, err: err}
	}
}

func (m topicsModel) Init() tea.Cmd {
	return m.loadTopics()
}

func (m *topicsModel) applyFilter() {
	m.filtered = nil
	if m.filter == "" {
		return
	}
	f := strings.ToLower(m.filter)
	for i, t := range m.topics {
		if strings.Contains(strings.ToLower(t.Name), f) {
			m.filtered = append(m.filtered, i)
		}
	}
}

func (m topicsModel) visibleCount() int {
	if m.filtered != nil {
		return len(m.filtered)
	}
	return len(m.topics)
}

func (m topicsModel) visibleIndex(i int) int {
	if m.filtered != nil && i < len(m.filtered) {
		return m.filtered[i]
	}
	return i
}

func (m topicsModel) selectedTopic() *client.KafkaTopic {
	if m.visibleCount() == 0 {
		return nil
	}
	idx := m.visibleIndex(m.cursor)
	if idx < len(m.topics) {
		return &m.topics[idx]
	}
	return nil
}

func (m topicsModel) Update(msg tea.Msg) (topicsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case topicsLoadedMsg:
		m.loading = false
		m.topics = msg.topics
		m.err = msg.err
		m.applyFilter()

	case topicDetailMsg:
		m.loading = false
		m.detail = msg.detail
		m.err = msg.err
		if msg.err == nil {
			m.view = topicsDetail
		}

	case consumeResultMsg:
		if msg.err == nil && len(msg.records) > 0 {
			m.records = append(m.records, msg.records...)
			if len(m.records) > 100 {
				m.records = m.records[len(m.records)-100:]
			}
		}

	case consumeTickMsg:
		if m.view == topicsConsume {
			t := m.selectedTopic()
			if t != nil {
				return m, tea.Batch(m.fetchRecords(t.Name), m.consumeTick())
			}
		}

	case tea.KeyMsg:
		switch m.view {
		case topicsList:
			return m.updateList(msg)
		case topicsDetail:
			return m.updateDetail(msg)
		case topicsConsume:
			return m.updateConsume(msg)
		}
	}
	return m, nil
}

func (m topicsModel) updateList(msg tea.KeyMsg) (topicsModel, tea.Cmd) {
	count := m.visibleCount()
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
		t := m.selectedTopic()
		if t != nil {
			m.loading = true
			return m, m.loadDetail(t.Name)
		}
	case "r":
		m.loading = true
		return m, m.loadTopics()
	}
	return m, nil
}

func (m topicsModel) updateDetail(msg tea.KeyMsg) (topicsModel, tea.Cmd) {
	switch msg.String() {
	case "esc", "backspace":
		m.view = topicsList
		m.detail = nil
	case "c":
		m.view = topicsConsume
		m.records = nil
		t := m.selectedTopic()
		if t != nil {
			return m, tea.Batch(m.fetchRecords(t.Name), m.consumeTick())
		}
	}
	return m, nil
}

func (m topicsModel) updateConsume(msg tea.KeyMsg) (topicsModel, tea.Cmd) {
	switch msg.String() {
	case "esc", "backspace":
		m.view = topicsDetail
		m.records = nil
	}
	return m, nil
}

func (m topicsModel) View() string {
	if m.loading {
		return dimStyle.Render("  Loading topics…")
	}
	if m.err != nil {
		return errorStyle.Render("  Error: " + m.err.Error())
	}

	switch m.view {
	case topicsDetail:
		return m.viewDetail()
	case topicsConsume:
		return m.viewConsume()
	default:
		return m.viewList()
	}
}

func (m topicsModel) viewList() string {
	if len(m.topics) == 0 {
		return dimStyle.Render("  No topics found.")
	}

	listWidth := 40
	var list strings.Builder

	header := fmt.Sprintf("  Topics (%d)", m.visibleCount())
	if m.filter != "" {
		header += filterActiveStyle.Render(fmt.Sprintf("  filter: %s", m.filter))
	}
	list.WriteString(dimStyle.Render(header) + "\n\n")

	count := m.visibleCount()
	start := 0
	maxVisible := m.height - 8
	if maxVisible < 5 {
		maxVisible = 20
	}
	if m.cursor >= maxVisible {
		start = m.cursor - maxVisible + 1
	}

	for vi := start; vi < count && vi < start+maxVisible; vi++ {
		idx := m.visibleIndex(vi)
		t := m.topics[idx]
		health := healthyStyle.Render("✓")
		ur := fmt.Sprintf("%v", t.UnderReplicated)
		if ur != "0" && ur != "<nil>" {
			health = warnStyle.Render("⚠")
		}
		label := fmt.Sprintf("%s %s", health, t.Name)
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

	t := m.selectedTopic()
	var detailStr string
	if t != nil {
		detailStr = detailTitleStyle.Render(t.Name) + "\n\n" +
			detailKeyStyle.Render("Partitions") + detailValueStyle.Render(fmt.Sprintf("%d", t.Partitions)) + "\n" +
			detailKeyStyle.Render("Rep. Factor") + detailValueStyle.Render(fmt.Sprintf("%d", t.ReplicationFactor)) + "\n" +
			detailKeyStyle.Render("Internal") + detailValueStyle.Render(fmt.Sprintf("%v", t.Internal)) + "\n\n" +
			dimStyle.Render("Enter: full detail  ·  c: consume")
	} else {
		detailStr = dimStyle.Render("No topic selected")
	}

	detailWidth := m.width - listWidth - 6
	if detailWidth < 20 {
		detailWidth = 40
	}
	detailPane := detailBorderStyle.Width(detailWidth).Render(detailStr)

	return lipgloss.JoinHorizontal(lipgloss.Top, listPane, detailPane)
}

func (m topicsModel) viewDetail() string {
	if m.detail == nil {
		return dimStyle.Render("  No detail available.")
	}

	name, _ := m.detail["name"].(string)
	partitions := fmt.Sprintf("%v", m.detail["partitions"])
	rf := fmt.Sprintf("%v", m.detail["replicationFactor"])

	var b strings.Builder
	b.WriteString(detailTitleStyle.Render("Topic: "+name) + "\n\n")
	b.WriteString(detailKeyStyle.Render("Partitions") + detailValueStyle.Render(partitions) + "\n")
	b.WriteString(detailKeyStyle.Render("Rep. Factor") + detailValueStyle.Render(rf) + "\n")

	if configs, ok := m.detail["configs"].(map[string]interface{}); ok && len(configs) > 0 {
		b.WriteString("\n" + dimStyle.Render("Configuration") + "\n")
		for k, v := range configs {
			b.WriteString(detailKeyStyle.Render("  "+k) + detailValueStyle.Render(fmt.Sprintf("%v", v)) + "\n")
		}
	}

	if piRaw, ok := m.detail["partitionInfo"].([]interface{}); ok && len(piRaw) > 0 {
		b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("Partitions (%d)", len(piRaw))) + "\n")
		b.WriteString(dimStyle.Render("  Part  Leader  Replicas       ISR            Health") + "\n")
		for _, p := range piRaw {
			pm, ok := p.(map[string]interface{})
			if !ok {
				continue
			}
			health := healthyStyle.Render("✓")
			if ur, _ := pm["underReplicated"].(bool); ur {
				health = warnStyle.Render("⚠")
			}
			b.WriteString(fmt.Sprintf("  %-6v%-8v%-15v%-15v%s\n",
				pm["partition"], pm["leader"], pm["replicas"], pm["isr"], health))
		}
	}

	b.WriteString("\n" + dimStyle.Render("c: consume  ·  Esc: back"))

	return detailBorderStyle.Width(m.width - 4).Render(b.String())
}

func (m topicsModel) viewConsume() string {
	t := m.selectedTopic()
	topicName := ""
	if t != nil {
		topicName = t.Name
	}

	var b strings.Builder
	b.WriteString(detailTitleStyle.Render(fmt.Sprintf("Consuming: %s", topicName)) + "\n")
	b.WriteString(dimStyle.Render(fmt.Sprintf("  %d records  ·  polling every 2s  ·  Esc to stop", len(m.records))) + "\n\n")

	if len(m.records) == 0 {
		b.WriteString(dimStyle.Render("  Waiting for records…"))
	} else {
		b.WriteString(dimStyle.Render("  Part  Offset  Key                Value") + "\n")
		start := 0
		maxShow := m.height - 10
		if maxShow < 5 {
			maxShow = 15
		}
		if len(m.records) > maxShow {
			start = len(m.records) - maxShow
		}
		for _, r := range m.records[start:] {
			key := fmt.Sprintf("%v", r.Key)
			if key == "<nil>" {
				key = dimStyle.Render("(null)")
			}
			val := fmt.Sprintf("%v", r.Value)
			if len(val) > 50 {
				val = val[:47] + "..."
			}
			b.WriteString(fmt.Sprintf("  %-6v%-8v%-19s%s\n", r.Partition, r.Offset, key, val))
		}
	}

	return detailBorderStyle.Width(m.width - 4).Render(b.String())
}

func (m topicsModel) helpContext() helpContext {
	switch m.view {
	case topicsDetail:
		return helpDetail
	case topicsConsume:
		return helpConsumer
	default:
		return helpMain
	}
}
