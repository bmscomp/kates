package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/klster/kates-cli/client"
)

type groupsView int

const (
	groupsList groupsView = iota
	groupsDetail
)

type groupsModel struct {
	client  *client.Client
	groups  []groupEntry
	detail  map[string]interface{}
	cursor  int
	view    groupsView
	width   int
	height  int
	loading bool
	err     error
}

type groupEntry struct {
	GroupID string
	State   string
	Members string
}

type groupsLoadedMsg struct {
	groups []groupEntry
	err    error
}

type groupDetailMsg struct {
	detail map[string]interface{}
	err    error
}

func newGroupsModel(c *client.Client) groupsModel {
	return groupsModel{client: c, loading: true}
}

func (m groupsModel) loadGroups() tea.Cmd {
	return func() tea.Msg {
		raw, err := m.client.KafkaGroups(context.Background())
		if err != nil {
			return groupsLoadedMsg{err: err}
		}
		var groups []groupEntry
		for _, g := range raw {
			groups = append(groups, groupEntry{
				GroupID: fmt.Sprintf("%v", g["groupId"]),
				State:   fmt.Sprintf("%v", g["state"]),
				Members: fmt.Sprintf("%v", g["members"]),
			})
		}
		return groupsLoadedMsg{groups: groups}
	}
}

func (m groupsModel) loadGroupDetail(id string) tea.Cmd {
	return func() tea.Msg {
		detail, err := m.client.KafkaGroupDetail(context.Background(), id)
		return groupDetailMsg{detail: detail, err: err}
	}
}

func (m groupsModel) Init() tea.Cmd {
	return m.loadGroups()
}

func (m groupsModel) Update(msg tea.Msg) (groupsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case groupsLoadedMsg:
		m.loading = false
		m.groups = msg.groups
		m.err = msg.err

	case groupDetailMsg:
		m.loading = false
		m.detail = msg.detail
		m.err = msg.err
		if msg.err == nil {
			m.view = groupsDetail
		}

	case tea.KeyMsg:
		switch m.view {
		case groupsList:
			return m.updateList(msg)
		case groupsDetail:
			return m.updateDetail(msg)
		}
	}
	return m, nil
}

func (m groupsModel) updateList(msg tea.KeyMsg) (groupsModel, tea.Cmd) {
	count := len(m.groups)
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
			m.loading = true
			return m, m.loadGroupDetail(m.groups[m.cursor].GroupID)
		}
	case "r":
		m.loading = true
		return m, m.loadGroups()
	}
	return m, nil
}

func (m groupsModel) updateDetail(msg tea.KeyMsg) (groupsModel, tea.Cmd) {
	switch msg.String() {
	case "esc", "backspace":
		m.view = groupsList
		m.detail = nil
	}
	return m, nil
}

func (m groupsModel) View() string {
	if m.loading {
		return dimStyle.Render("  Loading consumer groups…")
	}
	if m.err != nil {
		return errorStyle.Render("  Error: " + m.err.Error())
	}

	switch m.view {
	case groupsDetail:
		return m.viewDetail()
	default:
		return m.viewList()
	}
}

func (m groupsModel) viewList() string {
	if len(m.groups) == 0 {
		return dimStyle.Render("  No consumer groups found.")
	}

	listWidth := 40
	var list strings.Builder
	list.WriteString(dimStyle.Render(fmt.Sprintf("  Consumer Groups (%d)", len(m.groups))) + "\n\n")

	for i, g := range m.groups {
		stateIcon := dimStyle.Render("○")
		switch strings.ToUpper(g.State) {
		case "STABLE":
			stateIcon = healthyStyle.Render("●")
		case "EMPTY":
			stateIcon = dimStyle.Render("○")
		case "DEAD":
			stateIcon = errorStyle.Render("✖")
		default:
			stateIcon = warnStyle.Render("⚠")
		}
		label := fmt.Sprintf("%s %s", stateIcon, g.GroupID)
		if i == m.cursor {
			list.WriteString(selectedItemStyle.Render("▸ "+label) + "\n")
		} else {
			list.WriteString(listItemStyle.Render("  "+label) + "\n")
		}
	}

	listPane := lipgloss.NewStyle().Width(listWidth).Render(list.String())

	var detailStr string
	if m.cursor < len(m.groups) {
		g := m.groups[m.cursor]
		stateLabel := g.State
		switch strings.ToUpper(g.State) {
		case "STABLE":
			stateLabel = healthyStyle.Render("● STABLE")
		case "EMPTY":
			stateLabel = dimStyle.Render("○ EMPTY")
		case "DEAD":
			stateLabel = errorStyle.Render("✖ DEAD")
		}
		detailStr = detailTitleStyle.Render(g.GroupID) + "\n\n" +
			detailKeyStyle.Render("State") + stateLabel + "\n" +
			detailKeyStyle.Render("Members") + detailValueStyle.Render(g.Members) + "\n\n" +
			dimStyle.Render("Enter: view offsets & lag")
	} else {
		detailStr = dimStyle.Render("No group selected")
	}

	detailWidth := m.width - listWidth - 6
	if detailWidth < 20 {
		detailWidth = 40
	}
	detailPane := detailBorderStyle.Width(detailWidth).Render(detailStr)

	return lipgloss.JoinHorizontal(lipgloss.Top, listPane, detailPane)
}

func (m groupsModel) viewDetail() string {
	if m.detail == nil {
		return dimStyle.Render("  No detail available.")
	}

	groupID := fmt.Sprintf("%v", m.detail["groupId"])
	state := fmt.Sprintf("%v", m.detail["state"])
	members := fmt.Sprintf("%v", m.detail["members"])
	totalLag := fmt.Sprintf("%v", m.detail["totalLag"])

	stateLabel := state
	switch strings.ToUpper(state) {
	case "STABLE":
		stateLabel = healthyStyle.Render("● STABLE")
	case "EMPTY":
		stateLabel = dimStyle.Render("○ EMPTY")
	}

	lagLabel := totalLag
	if totalLag != "0" {
		lagLabel = warnStyle.Render(totalLag)
	} else {
		lagLabel = healthyStyle.Render("0")
	}

	var b strings.Builder
	b.WriteString(detailTitleStyle.Render("Group: "+groupID) + "\n\n")
	b.WriteString(detailKeyStyle.Render("State") + stateLabel + "\n")
	b.WriteString(detailKeyStyle.Render("Members") + detailValueStyle.Render(members) + "\n")
	b.WriteString(detailKeyStyle.Render("Total Lag") + lagLabel + "\n")

	if offsets, ok := m.detail["offsets"].([]interface{}); ok && len(offsets) > 0 {
		b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("Partition Offsets (%d)", len(offsets))) + "\n")
		b.WriteString(dimStyle.Render("  Topic            Part  Current    End        Lag") + "\n")
		for _, o := range offsets {
			om, ok := o.(map[string]interface{})
			if !ok {
				continue
			}
			lag := fmt.Sprintf("%v", om["lag"])
			lagCol := lag
			if lag != "0" {
				lagCol = warnStyle.Render(lag)
			} else {
				lagCol = healthyStyle.Render("0")
			}
			b.WriteString(fmt.Sprintf("  %-17v%-6v%-11v%-11v%s\n",
				om["topic"], om["partition"], om["currentOffset"], om["endOffset"], lagCol))
		}
	}

	b.WriteString("\n" + dimStyle.Render("Esc: back"))

	return detailBorderStyle.Width(m.width - 4).Render(b.String())
}

func (m groupsModel) helpCtx() helpContext {
	switch m.view {
	case groupsDetail:
		return helpDetail
	default:
		return helpMain
	}
}
