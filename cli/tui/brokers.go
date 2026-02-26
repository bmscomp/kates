package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/klster/kates-cli/client"
)

type brokersModel struct {
	client  *client.Client
	info    *client.ClusterInfo
	cursor  int
	width   int
	height  int
	loading bool
	err     error
}

type brokersLoadedMsg struct {
	info *client.ClusterInfo
	err  error
}

func newBrokersModel(c *client.Client) brokersModel {
	return brokersModel{client: c, loading: true}
}

func (m brokersModel) loadBrokers() tea.Cmd {
	return func() tea.Msg {
		info, err := m.client.KafkaBrokers(context.Background())
		return brokersLoadedMsg{info: info, err: err}
	}
}

func (m brokersModel) Init() tea.Cmd {
	return m.loadBrokers()
}

func (m brokersModel) Update(msg tea.Msg) (brokersModel, tea.Cmd) {
	switch msg := msg.(type) {
	case brokersLoadedMsg:
		m.loading = false
		m.info = msg.info
		m.err = msg.err
	case tea.KeyMsg:
		if m.info == nil {
			break
		}
		count := len(m.info.Brokers)
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < count-1 {
				m.cursor++
			}
		case "r":
			m.loading = true
			return m, m.loadBrokers()
		}
	}
	return m, nil
}

func (m brokersModel) View() string {
	if m.loading {
		return dimStyle.Render("  Loading brokers…")
	}
	if m.err != nil {
		return errorStyle.Render("  Error: " + m.err.Error())
	}
	if m.info == nil || len(m.info.Brokers) == 0 {
		return dimStyle.Render("  No brokers found.")
	}

	controllerID := ""
	if m.info.Controller != nil {
		controllerID = fmt.Sprintf("%v", m.info.Controller.ID)
	}

	listWidth := 32
	var list strings.Builder

	clusterShort := m.info.ClusterID
	if len(clusterShort) > 12 {
		clusterShort = clusterShort[:12] + "…"
	}
	list.WriteString(dimStyle.Render(fmt.Sprintf("  %v broker(s)  %s", m.info.BrokerCount, clusterShort)) + "\n\n")

	for i, b := range m.info.Brokers {
		isCtrl := fmt.Sprintf("%v", b.ID) == controllerID
		rack := fmt.Sprintf("%v", b.Rack)
		if rack == "" || rack == "<nil>" {
			rack = "?"
		}
		badge := ""
		if isCtrl {
			badge = " ★"
		}
		label := fmt.Sprintf("Broker %v · %s%s", b.ID, rack, badge)
		if i == m.cursor {
			list.WriteString(selectedItemStyle.Render("▸ "+label) + "\n")
		} else {
			list.WriteString(listItemStyle.Render("  "+label) + "\n")
		}
	}

	listPane := lipgloss.NewStyle().Width(listWidth).Render(list.String())

	b := m.info.Brokers[m.cursor]
	isCtrl := fmt.Sprintf("%v", b.ID) == controllerID
	role := dimStyle.Render("follower")
	if isCtrl {
		role = healthyStyle.Render("★ CONTROLLER")
	}
	rack := fmt.Sprintf("%v", b.Rack)
	if rack == "" || rack == "<nil>" {
		rack = "-"
	}
	endpoint := fmt.Sprintf("%s:%v", b.Host, b.Port)

	detail := detailTitleStyle.Render(fmt.Sprintf("Broker %v", b.ID)) + "  " + role + "\n\n" +
		detailKeyStyle.Render("Endpoint") + detailValueStyle.Render(endpoint) + "\n" +
		detailKeyStyle.Render("Host") + detailValueStyle.Render(b.Host) + "\n" +
		detailKeyStyle.Render("Port") + detailValueStyle.Render(fmt.Sprintf("%v", b.Port)) + "\n" +
		detailKeyStyle.Render("Rack / AZ") + detailValueStyle.Render(rack) + "\n" +
		detailKeyStyle.Render("Role") + role + "\n\n" +
		dimStyle.Render("Cluster") + "\n" +
		detailKeyStyle.Render("  Cluster ID") + detailValueStyle.Render(m.info.ClusterID) + "\n" +
		detailKeyStyle.Render("  Brokers") + detailValueStyle.Render(fmt.Sprintf("%v", m.info.BrokerCount)) + "\n" +
		detailKeyStyle.Render("  Controller") + detailValueStyle.Render(controllerID)

	detailWidth := m.width - listWidth - 6
	if detailWidth < 20 {
		detailWidth = 40
	}
	detailPane := detailBorderStyle.Width(detailWidth).Render(detail)

	return lipgloss.JoinHorizontal(lipgloss.Top, listPane, detailPane)
}
