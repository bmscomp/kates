package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/pkg/theme"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	k := groupsKeys()
	count := len(m.groups)
	switch {
	case key.Matches(msg, k.Up):
		if m.cursor > 0 {
			m.cursor--
		}
	case key.Matches(msg, k.Down):
		if m.cursor < count-1 {
			m.cursor++
		}
	case key.Matches(msg, k.Detail):
		if m.cursor < count {
			m.loading = true
			return m, m.loadGroupDetail(m.groups[m.cursor].GroupID)
		}
	case key.Matches(msg, k.Refresh):
		m.loading = true
		return m, m.loadGroups()
	}
	return m, nil
}

func (m groupsModel) updateDetail(msg tea.KeyMsg) (groupsModel, tea.Cmd) {
	if key.Matches(msg, groupsKeys().Back) {
		m.view = groupsList
		m.detail = nil
	}
	return m, nil
}

func (m groupsModel) View() string {
	if m.loading {
		return dimStyle.Render("  Loading consumer groups" + glyphs().Ellipsis)
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

	// A window of the list, as topics.go: without one a cluster with more
	// groups than terminal rows scrolls the cursor off the screen.
	start, end := scrollWindow(m.cursor, len(m.groups), listRows(m.height))
	for i := start; i < end; i++ {
		g := m.groups[i]
		label := fmt.Sprintf("%s %s", groupStateIcon(g.State), g.GroupID)
		list.WriteString(listRow(label, i == m.cursor) + "\n")
	}

	listPane := theme.OpenPane(lipgloss.NewStyle().Width(listWidth), true, list.String())

	var detailStr string
	if m.cursor < len(m.groups) {
		g := m.groups[m.cursor]
		detailStr = detailTitleStyle.Render(g.GroupID) + "\n\n" +
			detailKeyStyle.Render("State") + groupStateLabel(g.State, true) + "\n" +
			detailKeyStyle.Render("Members") + detailValueStyle.Render(g.Members) + "\n\n" +
			dimStyle.Render(hint(groupsKeys().Detail))
	} else {
		detailStr = dimStyle.Render("No group selected")
	}

	detailWidth := m.width - listWidth - 6
	if detailWidth < 20 {
		detailWidth = 40
	}
	detailPane := theme.Box(detailBox().Width(detailWidth), false, detailStr)

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

	var b strings.Builder
	b.WriteString(detailTitleStyle.Render("Group: "+groupID) + "\n\n")
	b.WriteString(detailKeyStyle.Render("State") + groupStateLabel(state, false) + "\n")
	b.WriteString(detailKeyStyle.Render("Members") + detailValueStyle.Render(members) + "\n")
	b.WriteString(detailKeyStyle.Render("Total Lag") + lagLabel(totalLag) + "\n")

	if offsets, ok := m.detail["offsets"].([]interface{}); ok && len(offsets) > 0 {
		b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("Partition Offsets (%d)", len(offsets))) + "\n")
		b.WriteString(dimStyle.Render("  Topic            Part  Current    End        Lag") + "\n")
		for _, o := range offsets {
			om, ok := o.(map[string]interface{})
			if !ok {
				continue
			}
			fmt.Fprintf(&b, "  %-17v%-6v%-11v%-11v%s\n",
				om["topic"], om["partition"], om["currentOffset"], om["endOffset"],
				lagLabel(fmt.Sprintf("%v", om["lag"])))
		}
	}

	b.WriteString("\n" + dimStyle.Render(hint(groupsKeys().Back)))

	return theme.Box(detailBox().Width(m.width-4), true, b.String())
}

// lagLabel colours a lag: green at zero, amber otherwise.
func lagLabel(lag string) string {
	if lag != "0" {
		return warnStyle.Render(lag)
	}
	return healthyStyle.Render("0")
}

// groupStateIcon marks a group's state in the list.
func groupStateIcon(state string) string {
	g := glyphs()
	switch strings.ToUpper(state) {
	case "STABLE":
		return healthyStyle.Render(g.Bullet)
	case "EMPTY":
		return dimStyle.Render(g.Ring)
	case "DEAD":
		return errorStyle.Render(g.Cross)
	default:
		return warnStyle.Render(g.Warn)
	}
}

// groupStateLabel is a group's state with its mark, or the bare state when
// it has none. The list's summary pane labels DEAD; the detail page never
// has, so withDead says which this is.
func groupStateLabel(state string, withDead bool) string {
	g := glyphs()
	switch strings.ToUpper(state) {
	case "STABLE":
		return healthyStyle.Render(g.Bullet + " STABLE")
	case "EMPTY":
		return dimStyle.Render(g.Ring + " EMPTY")
	case "DEAD":
		if withDead {
			return errorStyle.Render(g.Cross + " DEAD")
		}
	}
	return state
}

// helpBindings are the keys of the view on screen.
func (m groupsModel) helpBindings() []key.Binding {
	k := groupsKeys()
	if m.view == groupsDetail {
		return []key.Binding{k.Back}
	}
	return []key.Binding{k.Up, k.Down, k.Detail, k.Refresh}
}
