package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/klster/kates-cli/client"
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
		m.width = msg.Width
		m.height = msg.Height
		m.brokers.width = msg.Width
		m.brokers.height = msg.Height
		m.topics.width = msg.Width
		m.topics.height = msg.Height
		m.groups.width = msg.Width
		m.groups.height = msg.Height
		m.produce.width = msg.Width
		m.produce.height = msg.Height
		return m, nil

	case autoRefreshMsg:
		var cmds []tea.Cmd
		cmds = append(cmds, m.brokers.loadBrokers())
		cmds = append(cmds, m.topics.loadTopics())
		cmds = append(cmds, m.groups.loadGroups())
		cmds = append(cmds, scheduleAutoRefresh())
		return m, tea.Batch(cmds...)

	case tea.KeyMsg:
		if m.filtering {
			return m.handleFilter(msg)
		}

		if m.active == tabProduce && m.produce.view == produceInput {
			var cmd tea.Cmd
			m.produce, cmd = m.produce.Update(msg)
			return m, cmd
		}

		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "tab":
			m.active = tab((int(m.active) + 1) % len(m.tabs))
			return m, nil
		case "shift+tab":
			m.active = tab((int(m.active) - 1 + len(m.tabs)) % len(m.tabs))
			return m, nil
		case "1":
			m.active = tabBrokers
			return m, nil
		case "2":
			m.active = tabTopics
			return m, nil
		case "3":
			m.active = tabGroups
			return m, nil
		case "4":
			m.active = tabProduce
			return m, nil
		case "/":
			if m.active == tabTopics && m.topics.view == topicsList {
				m.filtering = true
				m.filterBuf = m.topics.filter
				return m, nil
			}
		}
	}

	var cmd tea.Cmd
	var cmds []tea.Cmd

	switch msg.(type) {
	case tea.KeyMsg:
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
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	default:
		m.brokers, cmd = m.brokers.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.topics, cmd = m.topics.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.groups, cmd = m.groups.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.produce, cmd = m.produce.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handleFilter(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		m.filtering = false
		m.topics.filter = m.filterBuf
		m.topics.cursor = 0
		m.topics.applyFilter()
	case "esc":
		m.filtering = false
		m.filterBuf = ""
		m.topics.filter = ""
		m.topics.cursor = 0
		m.topics.applyFilter()
	case "backspace":
		if len(m.filterBuf) > 0 {
			m.filterBuf = m.filterBuf[:len(m.filterBuf)-1]
		}
	default:
		if len(msg.String()) == 1 {
			m.filterBuf += msg.String()
		}
	}
	return m, nil
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}

	tabBar := m.renderTabs()
	var body string
	var hctx helpContext

	switch m.active {
	case tabBrokers:
		body = m.brokers.View()
		hctx = helpMain
	case tabTopics:
		body = m.topics.View()
		hctx = m.topics.helpContext()
	case tabGroups:
		body = m.groups.View()
		hctx = m.groups.helpCtx()
	case tabProduce:
		body = m.produce.View()
		hctx = m.produce.helpCtx()
	}

	if m.filtering {
		hctx = helpFilter
	}

	help := renderHelp(hctx)
	filterLine := ""
	if m.filtering {
		filterLine = "\n" + filterActiveStyle.Render(fmt.Sprintf("  / %s▌", m.filterBuf))
	}

	content := lipgloss.JoinVertical(lipgloss.Left,
		tabBar,
		"",
		body,
		filterLine,
	)

	bodyHeight := m.height - 2
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
