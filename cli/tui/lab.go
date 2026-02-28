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

type labParam struct {
	Label   string
	Key     string
	Values  []string
	Current int
}

type labIteration struct {
	Number     int
	Throughput float64
	P99Ms      float64
	ErrorRate  float64
	TestID     string
	Delta      string
}

type labView int

const (
	labConfig labView = iota
	labRunning
	labHistory
)

type LabModel struct {
	client     *client.Client
	apiURL     string
	params     []labParam
	cursor     int
	iterations []labIteration
	view       labView
	width      int
	height     int
	status     string
	running    bool
	elapsed    int
	quitting   bool
}

type labTestDoneMsg struct {
	run *client.TestRun
	err error
}

type labTickMsg struct{}

func NewLab(c *client.Client, url string) LabModel {
	return LabModel{
		client: c,
		apiURL: url,
		params: []labParam{
			{Label: "Test Type", Key: "type", Values: []string{"LOAD", "STRESS", "SPIKE", "ENDURANCE"}, Current: 0},
			{Label: "Producers", Key: "producers", Values: []string{"1", "2", "4", "8", "16", "32"}, Current: 2},
			{Label: "Records", Key: "records", Values: []string{"10000", "50000", "100000", "500000", "1000000"}, Current: 1},
			{Label: "Record Size", Key: "recordSize", Values: []string{"128", "256", "512", "1024", "2048", "4096"}, Current: 2},
			{Label: "Acks", Key: "acks", Values: []string{"0", "1", "all"}, Current: 2},
			{Label: "Compression", Key: "compression", Values: []string{"none", "gzip", "snappy", "lz4", "zstd"}, Current: 3},
			{Label: "Batch Size", Key: "batchSize", Values: []string{"16384", "32768", "65536", "131072", "262144"}, Current: 0},
			{Label: "Linger ms", Key: "lingerMs", Values: []string{"0", "5", "10", "20", "50", "100"}, Current: 0},
			{Label: "Partitions", Key: "partitions", Values: []string{"1", "3", "6", "12", "24", "48"}, Current: 2},
			{Label: "Replication", Key: "replication", Values: []string{"1", "2", "3"}, Current: 2},
		},
		status: dimStyle.Render("Press Enter to run a test iteration"),
	}
}

func RunLab(c *client.Client, url string) error {
	m := NewLab(c, url)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func (m LabModel) Init() tea.Cmd {
	return nil
}

func (m LabModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.running {
			if msg.String() == "ctrl+c" {
				m.quitting = true
				return m, tea.Quit
			}
			return m, nil
		}

		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.params)-1 {
				m.cursor++
			}
		case "left", "h":
			p := &m.params[m.cursor]
			if p.Current > 0 {
				p.Current--
			}
		case "right", "l":
			p := &m.params[m.cursor]
			if p.Current < len(p.Values)-1 {
				p.Current++
			}
		case "enter":
			m.running = true
			m.elapsed = 0
			m.status = filterActiveStyle.Render("⏳ Running iteration #" + fmt.Sprintf("%d", len(m.iterations)+1) + "…")
			return m, tea.Batch(m.runTest(), m.tickElapsed())
		case "d":
			if len(m.iterations) >= 2 {
				m.view = labHistory
			}
		case "escape", "esc":
			m.view = labConfig
		}

	case labTestDoneMsg:
		m.running = false
		if msg.err != nil {
			m.status = errorStyle.Render("✖ " + msg.err.Error())
			return m, nil
		}
		iter := labIteration{
			Number: len(m.iterations) + 1,
			TestID: truncID(msg.run.ID),
		}
		if len(msg.run.Results) > 0 {
			r := msg.run.Results[0]
			iter.Throughput = r.ThroughputRecordsPerSec
			iter.P99Ms = r.P99LatencyMs
			iter.ErrorRate = r.AvgLatencyMs
			if len(msg.run.Results) > 0 {
				var totalThroughput, totalP99 float64
				for _, res := range msg.run.Results {
					totalThroughput += res.ThroughputRecordsPerSec
					totalP99 += res.P99LatencyMs
				}
				iter.Throughput = totalThroughput / float64(len(msg.run.Results))
				iter.P99Ms = totalP99 / float64(len(msg.run.Results))
				iter.ErrorRate = msg.run.Results[len(msg.run.Results)-1].AvgLatencyMs
			}
		}
		if len(m.iterations) > 0 {
			prev := m.iterations[len(m.iterations)-1]
			if prev.Throughput > 0 {
				pct := ((iter.Throughput - prev.Throughput) / prev.Throughput) * 100
				if pct > 0 {
					iter.Delta = healthyStyle.Render(fmt.Sprintf("▲%.0f%%", pct))
				} else if pct < 0 {
					iter.Delta = errorStyle.Render(fmt.Sprintf("▼%.0f%%", -pct))
				} else {
					iter.Delta = dimStyle.Render("—")
				}
			}
		}
		m.iterations = append(m.iterations, iter)
		m.status = healthyStyle.Render(fmt.Sprintf(
			"✓ Iteration #%d done — %s rec/s, p99=%sms",
			iter.Number,
			fmtLabNum(iter.Throughput),
			fmtLabFloat(iter.P99Ms),
		))

	case labTickMsg:
		if m.running {
			m.elapsed++
			return m, m.tickElapsed()
		}
	}
	return m, nil
}

func (m LabModel) View() string {
	if m.quitting {
		return ""
	}

	w := m.width
	if w < 80 {
		w = 80
	}

	headerText := "  Kates Lab  ·  Interactive Performance Tuning"
	if m.apiURL != "" {
		headerText += "  →  " + m.apiURL
	}
	header := labHeaderStyle.Width(w - 2).Render(headerText)

	halfW := (w - 4) / 2
	leftW := halfW
	rightW := halfW

	leftContent := m.viewParams(leftW)
	rightContent := m.viewResults(rightW - 6)

	leftPane := lipgloss.NewStyle().
		Width(leftW).
		Padding(1, 2).
		Render(leftContent)

	rightPane := detailBorderStyle.
		Width(rightW).
		Render(rightContent)

	body := lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)

	statusBar := "\n  " + m.status
	if m.running {
		statusBar += dimStyle.Render(fmt.Sprintf("  (%ds)", m.elapsed))
	}

	helpKeys := []string{"↑↓ navigate", "←→ change value", "Enter run", "d diff", "q quit"}
	help := "\n  " + dimStyle.Render(strings.Join(helpKeys, "  ·  "))

	return header + "\n\n" + body + statusBar + help
}

func (m LabModel) viewParams(width int) string {
	const labelCol = 16

	var b strings.Builder

	for i, p := range m.params {
		prefix := "  "
		if i == m.cursor {
			prefix = "▸ "
		}

		paddedLabel := p.Label
		for len(paddedLabel) < labelCol {
			paddedLabel += " "
		}

		if i == m.cursor {
			b.WriteString(filterActiveStyle.Render(prefix + paddedLabel))
		} else {
			b.WriteString(dimStyle.Render(prefix + paddedLabel))
		}

		if i == m.cursor {
			for vi, v := range p.Values {
				padded := fmt.Sprintf(" %-8s", v)
				if vi == p.Current {
					b.WriteString(selectedValueStyle.Render(padded))
				} else {
					b.WriteString(dimStyle.Render(padded))
				}
			}
		} else {
			b.WriteString(activeValueStyle.Render("[" + p.Values[p.Current] + "]"))
		}

		b.WriteString("\n")
		if i == m.cursor {
			b.WriteString("\n")
		}
	}

	return b.String()
}

func (m LabModel) viewResults(width int) string {
	if m.view == labHistory && len(m.iterations) >= 2 {
		return m.viewDiff()
	}

	var b strings.Builder

	if len(m.iterations) == 0 {
		b.WriteString(dimStyle.Render("No iterations yet.\n\nTweak parameters on the left and\npress Enter to run your first test."))
		return b.String()
	}

	b.WriteString(detailTitleStyle.Render("Iteration History") + "\n\n")

	b.WriteString(fmt.Sprintf("  %s%s%s%s%s\n",
		dimStyle.Render(padRight("#", 6)),
		dimStyle.Render(padRight("Throughput", 18)),
		dimStyle.Render(padRight("P99", 14)),
		dimStyle.Render(padRight("Err Rate", 14)),
		dimStyle.Render(padRight("Delta", 10)),
	))
	b.WriteString(dimStyle.Render("  "+strings.Repeat("─", 60)) + "\n\n")

	start := 0
	if len(m.iterations) > 12 {
		start = len(m.iterations) - 12
	}

	for _, iter := range m.iterations[start:] {
		delta := iter.Delta
		if delta == "" {
			delta = dimStyle.Render("—")
		}

		numStr := padRight(fmt.Sprintf("%d", iter.Number), 6)
		thrStr := padRight(fmtLabNum(iter.Throughput)+" rec/s", 18)
		p99Str := padRight(fmtLabFloat(iter.P99Ms)+"ms", 14)
		errStr := padRight(fmtLabFloat(iter.ErrorRate), 14)

		b.WriteString(fmt.Sprintf("  %s%s%s%s%s\n",
			numStr,
			healthyStyle.Render(thrStr),
			warnStyle.Render(p99Str),
			dimStyle.Render(errStr),
			delta,
		))
	}

	if len(m.iterations) > 1 {
		sparkVals := make([]float64, len(m.iterations))
		for i, it := range m.iterations {
			sparkVals[i] = it.Throughput
		}
		b.WriteString("\n  " + dimStyle.Render("Trend: ") + sparkline(sparkVals))
	}

	return b.String()
}

func (m LabModel) viewDiff() string {
	if len(m.iterations) < 2 {
		return dimStyle.Render("Need at least 2 iterations to diff")
	}

	a := m.iterations[len(m.iterations)-2]
	b := m.iterations[len(m.iterations)-1]

	var sb strings.Builder
	sb.WriteString(detailTitleStyle.Render(fmt.Sprintf("Diff: #%d vs #%d", a.Number, b.Number)) + "\n\n")

	sb.WriteString(fmt.Sprintf("  %-16s %-14s %-14s %-10s\n",
		dimStyle.Render("Metric"),
		dimStyle.Render(fmt.Sprintf("#%d", a.Number)),
		dimStyle.Render(fmt.Sprintf("#%d", b.Number)),
		dimStyle.Render("Change"),
	))
	sb.WriteString(dimStyle.Render("  "+strings.Repeat("─", 54)) + "\n")

	writeDiffRow := func(label string, aVal, bVal float64, unit string, higherIsBetter bool) {
		delta := bVal - aVal
		pct := float64(0)
		if aVal != 0 {
			pct = (delta / aVal) * 100
		}
		var changeStr string
		if pct > 1 {
			if higherIsBetter {
				changeStr = healthyStyle.Render(fmt.Sprintf("▲%.0f%%", pct))
			} else {
				changeStr = errorStyle.Render(fmt.Sprintf("▲%.0f%%", pct))
			}
		} else if pct < -1 {
			if higherIsBetter {
				changeStr = errorStyle.Render(fmt.Sprintf("▼%.0f%%", -pct))
			} else {
				changeStr = healthyStyle.Render(fmt.Sprintf("▼%.0f%%", -pct))
			}
		} else {
			changeStr = dimStyle.Render("≈")
		}

		sb.WriteString(fmt.Sprintf("  %-16s %-14s %-14s %s\n",
			label,
			fmtLabNum(aVal)+unit,
			fmtLabNum(bVal)+unit,
			changeStr,
		))
	}

	writeDiffRow("Throughput", a.Throughput, b.Throughput, " rec/s", true)
	writeDiffRow("P99 Latency", a.P99Ms, b.P99Ms, " ms", false)

	sb.WriteString("\n  " + dimStyle.Render("Press Esc to go back"))

	return sb.String()
}

func (m LabModel) runTest() tea.Cmd {
	return func() tea.Msg {
		req := &client.CreateTestRequest{
			TestType: m.paramVal("type"),
		}

		spec := &client.TestSpec{}
		spec.ParallelProducers = labParseInt(m.paramVal("producers"))
		spec.Records = labParseInt(m.paramVal("records"))
		spec.RecordSizeBytes = labParseInt(m.paramVal("recordSize"))
		spec.Acks = m.paramVal("acks")
		spec.CompressionType = m.paramVal("compression")
		spec.BatchSize = labParseInt(m.paramVal("batchSize"))
		spec.LingerMs = labParseInt(m.paramVal("lingerMs"))
		spec.Partitions = labParseInt(m.paramVal("partitions"))
		spec.ReplicationFactor = labParseInt(m.paramVal("replication"))
		req.Spec = spec

		run, err := m.client.CreateTest(context.Background(), req)
		if err != nil {
			return labTestDoneMsg{err: err}
		}

		for i := 0; i < 180; i++ {
			time.Sleep(2 * time.Second)
			updated, err := m.client.GetTest(context.Background(), run.ID)
			if err != nil {
				continue
			}
			status := strings.ToUpper(updated.Status)
			if status == "DONE" || status == "COMPLETED" || status == "FAILED" || status == "ERROR" {
				return labTestDoneMsg{run: updated}
			}
		}
		return labTestDoneMsg{err: fmt.Errorf("test timed out after 6 minutes")}
	}
}

func (m LabModel) tickElapsed() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		return labTickMsg{}
	})
}

func (m LabModel) paramVal(key string) string {
	for _, p := range m.params {
		if p.Key == key {
			return p.Values[p.Current]
		}
	}
	return ""
}

func sparkline(values []float64) string {
	if len(values) == 0 {
		return ""
	}
	blocks := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	min, max := values[0], values[0]
	for _, v := range values {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	rng := max - min
	if rng == 0 {
		rng = 1
	}
	var sb strings.Builder
	for _, v := range values {
		idx := int((v - min) / rng * float64(len(blocks)-1))
		if idx >= len(blocks) {
			idx = len(blocks) - 1
		}
		sb.WriteRune(blocks[idx])
	}
	return sb.String()
}

func fmtLabNum(v float64) string {
	if v >= 1_000_000 {
		return fmt.Sprintf("%.1fM", v/1_000_000)
	}
	if v >= 1_000 {
		return fmt.Sprintf("%.1fK", v/1_000)
	}
	return fmt.Sprintf("%.0f", v)
}

func fmtLabFloat(v float64) string {
	return fmt.Sprintf("%.2f", v)
}

func truncID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func padRight(s string, w int) string {
	for len(s) < w {
		s += " "
	}
	return s
}

func labParseInt(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}

var (
	labHeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#7C3AED")).
			Padding(0, 1)

	selectedValueStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#000000")).
				Background(lipgloss.Color("#06B6D4")).
				Padding(0, 0)

	activeValueStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#06B6D4")).
				Bold(true)
)
