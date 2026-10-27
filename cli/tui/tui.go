package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bmscomp/kates/cli/client"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type tab int

const (
	tabBrokers tab = iota
	tabTopics
	tabGroups
	tabProduce
)

type autoRefreshMsg struct{}

type Model struct {
	tabs      []string
	active    tab
	brokers   brokersModel
	topics    topicsModel
	groups    groupsModel
	produce   produceModel
	width     int
	height    int
	filtering bool
	filterBuf string
	quitting  bool
}

func New(c *client.Client) Model {
	return Model{
		tabs:    []string{"Brokers", "Topics", "Groups", "Produce"},
		active:  tabTopics,
		brokers: newBrokersModel(c),
		topics:  newTopicsModel(c),
		groups:  newGroupsModel(c),
		produce: newProduceModel(c),
	}
}

func Run(c *client.Client) error {
	m := New(c)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func scheduleAutoRefresh() tea.Cmd {
	return tea.Tick(10*time.Second, func(time.Time) tea.Msg {
		return autoRefreshMsg{}
	})
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.brokers.Init(),
		m.topics.Init(),
		m.groups.Init(),
		m.produce.Init(),
		scheduleAutoRefresh(),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.brokers.width, m.brokers.height = msg.Width, msg.Height
		m.topics.width, m.topics.height = msg.Width, msg.Height
		m.groups.width, m.groups.height = msg.Width, msg.Height
		m.produce.width, m.produce.height = msg.Width, msg.Height
		return m, nil

	case autoRefreshMsg:
		return m, tea.Batch(
			m.brokers.loadBrokers(),
			m.topics.loadTopics(),
			m.groups.loadGroups(),
			scheduleAutoRefresh(),
		)

	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m.broadcast(msg)
}

// updateKey handles a key press: the filter's keys while it is open, the
// produce input's while that has focus, the tab bar's own, and otherwise the
// active tab's.
func (m Model) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.filtering {
		return m.handleFilter(msg)
	}

	if m.active == tabProduce && m.produce.view == produceInput {
		// Every key but ctrl+c is the input's: q, tab and the digits are text.
		if key.Matches(msg, produceKeys().Quit) {
			m.quitting = true
			return m, tea.Quit
		}
		var cmd tea.Cmd
		m.produce, cmd = m.produce.Update(msg)
		return m, cmd
	}

	k := tabsKeys()
	switch {
	case key.Matches(msg, k.Quit):
		m.quitting = true
		return m, tea.Quit
	case key.Matches(msg, k.NextTab):
		m.active = tab((int(m.active) + 1) % len(m.tabs))
		return m, nil
	case key.Matches(msg, k.PrevTab):
		m.active = tab((int(m.active) - 1 + len(m.tabs)) % len(m.tabs))
		return m, nil
	case key.Matches(msg, k.JumpTab):
		m.active = tab(msg.String()[0] - '1')
		return m, nil
	case key.Matches(msg, topicsKeys().Filter) && m.active == tabTopics && m.topics.view == topicsList:
		m.filtering = true
		m.filterBuf = m.topics.filter
		return m, nil
	}

	var cmd tea.Cmd
	switch m.active {
	case tabBrokers:
		m.brokers, cmd = m.brokers.Update(msg)
	case tabTopics:
		m.topics, cmd = m.topics.Update(msg)
	case tabGroups:
		m.groups, cmd = m.groups.Update(msg)
	case tabProduce:
		m.produce, cmd = m.produce.Update(msg)
	}
	return m, cmd
}

// broadcast hands a message that is not a key to every tab: each picks out
// the results of its own commands.
func (m Model) broadcast(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmds := make([]tea.Cmd, 4)
	m.brokers, cmds[0] = m.brokers.Update(msg)
	m.topics, cmds[1] = m.topics.Update(msg)
	m.groups, cmds[2] = m.groups.Update(msg)
	m.produce, cmds[3] = m.produce.Update(msg)
	return m, tea.Batch(cmds...)
}

func (m Model) handleFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := tabsKeys()
	switch {
	case key.Matches(msg, k.FilterApply):
		m.filtering = false
		m.topics.filter = m.filterBuf
		m.topics.cursor = 0
		m.topics.applyFilter()
	case key.Matches(msg, k.FilterClear):
		m.filtering = false
		m.filterBuf = ""
		m.topics.filter = ""
		m.topics.cursor = 0
		m.topics.applyFilter()
	case key.Matches(msg, k.FilterErase):
		// The buffer holds runes, so backspace removes a whole one, not the
		// last byte of a multi-byte character.
		if len(m.filterBuf) > 0 {
			_, size := utf8.DecodeLastRuneInString(m.filterBuf)
			m.filterBuf = m.filterBuf[:len(m.filterBuf)-size]
		}
	default:
		// As produce.go: a key of type KeyRunes is typed text, "é" and a
		// paste included; msg.String() of a non-ASCII rune is not 1 byte long.
		if msg.Type == tea.KeyRunes {
			m.filterBuf += string(msg.Runes)
		}
	}
	return m, nil
}

// helpBindings are the keys that act on the screen as it is: the active
// tab's view first, then the tab bar's own.
func (m Model) helpBindings() []key.Binding {
	k := tabsKeys()
	if m.filtering {
		return []key.Binding{k.FilterType, k.FilterApply, k.FilterErase, k.FilterClear}
	}
	var view []key.Binding
	switch m.active {
	case tabBrokers:
		view = m.brokers.helpBindings()
	case tabTopics:
		view = m.topics.helpBindings()
	case tabGroups:
		view = m.groups.helpBindings()
	case tabProduce:
		view = m.produce.helpBindings()
		if m.produce.view == produceInput {
			return view
		}
	}
	return append(view, k.NextTab, k.PrevTab, k.JumpTab, k.Quit)
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}

	if TooSmall(m.width, m.height) {
		return TooSmallCard(m.width, m.height)
	}

	tabBar := m.renderTabs()
	var body string
	switch m.active {
	case tabBrokers:
		body = m.brokers.View()
	case tabTopics:
		body = m.topics.View()
	case tabGroups:
		body = m.groups.View()
	case tabProduce:
		body = m.produce.View()
	}

	help := renderHelp(m.helpBindings(), m.width)
	filterLine := ""
	if m.filtering {
		filterLine = "\n" + filterActiveStyle.Render(fmt.Sprintf("  / %s%s", m.filterBuf, glyphs().Caret))
	}

	content := lipgloss.JoinVertical(lipgloss.Left,
		tabBar,
		"",
		body,
		filterLine,
	)

	// The help sits on the last rows; the body is padded down to it.
	bodyHeight := m.height - 1 - (strings.Count(help, "\n") + 1)
	contentLines := strings.Count(content, "\n") + 1
	if contentLines < bodyHeight {
		content += strings.Repeat("\n", bodyHeight-contentLines)
	}
	content += "\n" + help

	return content
}

func (m Model) renderTabs() string {
	var tabs []string
	for i, t := range m.tabs {
		if tab(i) == m.active {
			tabs = append(tabs, activeTabStyle.Render(t))
		} else {
			tabs = append(tabs, inactiveTabStyle.Render(t))
		}
	}
	row := lipgloss.JoinHorizontal(lipgloss.Bottom, tabs...)
	gap := tabGapStyle.Width(m.width - lipgloss.Width(row)).Render("")
	return lipgloss.JoinHorizontal(lipgloss.Bottom, row, gap)
}
