package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	Params     map[string]string
	Warmup     bool
}

type labView int

const (
	labConfig labView = iota
	labRunning
	labHistory
	labPinSelect
	labSweepConfig
)

type labPreset struct {
	Name   string
	Desc   string
	Values map[string]string
}

var labPresets = []labPreset{
	{
		Name: "Low Latency",
		Desc: "Minimize p99 — acks=1, no compression, small batch",
		Values: map[string]string{
			"type": "SPIKE", "acks": "1", "compression": "none",
			"batchSize": "16384", "lingerMs": "0", "producers": "1",
		},
	},
	{
		Name: "Max Throughput",
		Desc: "Maximize rec/s — large batches, lz4, many producers",
		Values: map[string]string{
			"type": "STRESS", "acks": "all", "compression": "lz4",
			"batchSize": "262144", "lingerMs": "50", "producers": "8",
		},
	},
	{
		Name: "Durability",
		Desc: "No data loss — acks=all, RF=3, idempotent",
		Values: map[string]string{
			"type": "LOAD", "acks": "all", "replication": "3",
			"compression": "lz4", "batchSize": "65536", "lingerMs": "5",
		},
	},
}

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

	liveRecords    int64
	liveThroughput float64
	liveLatency    float64
	runTestID      string
	cancelCtx      context.Context
	cancelFn       context.CancelFunc

	pinA int
	pinB int

	sweepParam  int
	sweepActive bool

	warmupCount     int
	warmupRemaining int

	medianActive    bool
	medianRemaining int
	medianResults   []labIteration

	lastError   error
	lastTestReq *client.CreateTestRequest
}

type labTestDoneMsg struct {
	run *client.TestRun
	err error
}

type labTickMsg struct{}

type labProgressMsg struct {
	records    int64
	throughput float64
	latency    float64
}

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
		pinA:   -1,
		pinB:   -1,
		status: dimStyle.Render("Press Enter to run  ·  p presets  ·  s sweep"),
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
			switch msg.String() {
			case "ctrl+c":
				m.quitting = true
				if m.cancelFn != nil {
					m.cancelFn()
				}
				return m, tea.Quit
			case "x":
				if m.cancelFn != nil && m.runTestID != "" {
					m.cancelFn()
					_ = m.client.CancelTest(context.Background(), m.runTestID)
					m.running = false
					m.status = warnStyle.Render("⏹ Test cancelled")
				}
				return m, nil
			}
			return m, nil
		}

		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "up", "k":
			if m.view == labPinSelect {
				if m.pinA > 0 {
					m.pinA--
				}
			} else if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.view == labPinSelect {
				if m.pinA < len(m.iterations)-1 {
					m.pinA++
				}
			} else if m.cursor < len(m.params)-1 {
				m.cursor++
			}
		case "left", "h":
			if m.view == labPinSelect {
				if m.pinB > 0 {
					m.pinB--
				}
			} else {
				p := &m.params[m.cursor]
				if p.Current > 0 {
					p.Current--
				}
			}
		case "right", "l":
			if m.view == labPinSelect {
				if m.pinB < len(m.iterations)-1 {
					m.pinB++
				}
			} else {
				p := &m.params[m.cursor]
				if p.Current < len(p.Values)-1 {
					p.Current++
				}
			}
		case "enter":
			if m.view == labPinSelect {
				m.view = labHistory
				return m, nil
			}
			if m.warmupCount > 0 && m.warmupRemaining == 0 {
				m.warmupRemaining = m.warmupCount
			}
			m.running = true
			m.elapsed = 0
			m.liveRecords = 0
			m.liveThroughput = 0
			m.liveLatency = 0
			m.lastError = nil
			ctx, cancel := context.WithCancel(context.Background())
			m.cancelCtx = ctx
			m.cancelFn = cancel
			if m.warmupRemaining > 0 {
				m.status = filterActiveStyle.Render(fmt.Sprintf("🔥 Warmup %d/%d…", m.warmupCount-m.warmupRemaining+1, m.warmupCount))
			} else {
				m.status = filterActiveStyle.Render("⏳ Running iteration #" + fmt.Sprintf("%d", len(m.iterations)+1) + "…")
			}
			return m, tea.Batch(m.runTest(), m.tickElapsed())
		case "d":
			if len(m.iterations) >= 2 {
				m.view = labHistory
				if m.pinA < 0 || m.pinB < 0 {
					m.pinA = len(m.iterations) - 2
					m.pinB = len(m.iterations) - 1
				}
			}
		case "c":
			if len(m.iterations) >= 2 {
				m.view = labPinSelect
				m.pinA = len(m.iterations) - 2
				m.pinB = len(m.iterations) - 1
			}
		case "p":
			m.applyNextPreset()
		case "s":
			if !m.sweepActive {
				m.startSweep()
				return m, nil
			}
		case "W":
			if !m.running {
				m.warmupCount++
				if m.warmupCount > 5 {
					m.warmupCount = 0
				}
				if m.warmupCount == 0 {
					m.status = dimStyle.Render("Warmup: off")
				} else {
					m.status = filterActiveStyle.Render(fmt.Sprintf("🔥 Warmup: %d iteration(s) before measuring", m.warmupCount))
				}
			}
		case "m":
			if !m.running && !m.medianActive {
				m.medianActive = true
				m.medianRemaining = 3
				m.medianResults = nil
				m.running = true
				m.elapsed = 0
				m.liveRecords = 0
				m.liveThroughput = 0
				m.liveLatency = 0
				m.lastError = nil
				ctx, cancel := context.WithCancel(context.Background())
				m.cancelCtx = ctx
				m.cancelFn = cancel
				m.status = filterActiveStyle.Render("📊 Median mode: running 1/3…")
				return m, tea.Batch(m.runTest(), m.tickElapsed())
			}
		case "e":
			if len(m.iterations) > 0 {
				path := m.exportCSV()
				m.status = healthyStyle.Render("✓ Exported to " + path)
			}
		case "w":
			path := m.saveSession()
			m.status = healthyStyle.Render("✓ Session saved to " + path)
		case "L":
			if path, ok := m.loadSession(); ok {
				m.status = healthyStyle.Render("✓ Session loaded from " + path)
			} else {
				m.status = warnStyle.Render("No saved session found")
			}
		case "r":
			if m.lastError != nil && m.lastTestReq != nil {
				m.running = true
				m.elapsed = 0
				m.liveRecords = 0
				m.liveThroughput = 0
				m.liveLatency = 0
				m.lastError = nil
				ctx, cancel := context.WithCancel(context.Background())
				m.cancelCtx = ctx
				m.cancelFn = cancel
				m.status = filterActiveStyle.Render("⏳ Retrying…")
				return m, tea.Batch(m.retryTest(), m.tickElapsed())
			}
		case "escape", "esc":
			m.view = labConfig
		}

	case labTestDoneMsg:
		m.running = false
		if msg.err != nil {
			m.lastError = msg.err
			m.status = errorStyle.Render("✖ " + msg.err.Error() + "  ·  press r to retry")
			if m.sweepActive {
				m.sweepActive = false
			}
			m.warmupRemaining = 0
			m.medianActive = false
			return m, nil
		}
		iter := m.buildIteration(msg.run)

		// Warmup: discard this iteration silently
		if m.warmupRemaining > 0 {
			m.warmupRemaining--
			if m.warmupRemaining > 0 {
				m.running = true
				m.elapsed = 0
				ctx, cancel := context.WithCancel(context.Background())
				m.cancelCtx = ctx
				m.cancelFn = cancel
				m.status = filterActiveStyle.Render(fmt.Sprintf("🔥 Warmup %d/%d…", m.warmupCount-m.warmupRemaining+1, m.warmupCount))
				return m, tea.Batch(m.runTest(), m.tickElapsed())
			}
			// Last warmup done — now run the real iteration
			m.running = true
			m.elapsed = 0
			ctx, cancel := context.WithCancel(context.Background())
			m.cancelCtx = ctx
			m.cancelFn = cancel
			m.status = filterActiveStyle.Render("⏳ Running measured iteration…")
			return m, tea.Batch(m.runTest(), m.tickElapsed())
		}

		// Median mode: collect 3 runs, report median
		if m.medianActive {
			m.medianResults = append(m.medianResults, iter)
			m.medianRemaining--
			if m.medianRemaining > 0 {
				m.running = true
				m.elapsed = 0
				ctx, cancel := context.WithCancel(context.Background())
				m.cancelCtx = ctx
				m.cancelFn = cancel
				m.status = filterActiveStyle.Render(fmt.Sprintf("📊 Median mode: running %d/3…", 3-m.medianRemaining+1))
				return m, tea.Batch(m.runTest(), m.tickElapsed())
			}
			// All 3 done — pick the median by throughput
			median := m.computeMedianIteration()
			m.medianActive = false
			m.medianResults = nil
			iter = median
		}

		m.iterations = append(m.iterations, iter)
		m.status = healthyStyle.Render(fmt.Sprintf(
			"✓ #%d — %s rec/s, p99=%sms",
			iter.Number,
			fmtLabNum(iter.Throughput),
			fmtLabFloat(iter.P99Ms),
		))

		if m.sweepActive {
			return m, m.nextSweepStep()
		}

	case labProgressMsg:
		m.liveRecords = msg.records
		m.liveThroughput = msg.throughput
		m.liveLatency = msg.latency
		return m, nil

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
	if w < 40 {
		w = 40
	}

	headerText := "  Kates Lab"
	if w >= 60 {
		headerText += "  ·  Interactive Performance Tuning"
	}
	if w >= 100 && m.apiURL != "" {
		headerText += "  →  " + m.apiURL
	}
	header := labHeaderStyle.Width(w - 2).Render(headerText)

	var body string

	if w < 80 {
		// Compact: stack vertically
		contentW := w - 4
		leftContent := m.viewParams(contentW)
		rightContent := m.viewResults(contentW - 4)
		rightPane := detailBorderStyle.
			Width(contentW).
			Render(rightContent)
		body = leftContent + "\n" + rightPane
	} else {
		// Side-by-side: normal (35/65 at 80-119) or wide (50/50 at ≥120)
		var leftW, rightW int
		if w >= 120 {
			halfW := (w - 4) / 2
			leftW = halfW
			rightW = halfW
		} else {
			usable := w - 4
			leftW = usable * 35 / 100
			rightW = usable - leftW
		}

		leftContent := m.viewParams(leftW)
		rightContent := m.viewResults(rightW - 6)

		leftPane := lipgloss.NewStyle().
			Width(leftW).
			Padding(1, 2).
			Render(leftContent)

		rightPane := detailBorderStyle.
			Width(rightW).
			Render(rightContent)

		body = lipgloss.JoinHorizontal(lipgloss.Top, leftPane, rightPane)
	}

	statusBar := "\n  " + m.status
	if m.running {
		statusBar += dimStyle.Render(fmt.Sprintf("  (%ds)", m.elapsed))
		if m.liveRecords > 0 {
			statusBar += dimStyle.Render(fmt.Sprintf("  %s rec  %s rec/s  p99=%sms",
				fmtLabNum(float64(m.liveRecords)),
				fmtLabNum(m.liveThroughput),
				fmtLabFloat(m.liveLatency),
			))
		}
	}

	help := m.renderHelp(m.helpKeys(), w)

	return header + "\n\n" + body + statusBar + help
}

func (m LabModel) renderHelp(keys []string, maxWidth int) string {
	sep := "  ·  "
	avail := maxWidth - 4
	var lines []string
	var line string
	for i, k := range keys {
		next := k
		if line != "" {
			next = sep + k
		}
		if len(line)+len(next) > avail && line != "" {
			lines = append(lines, line)
			line = k
		} else {
			if i == 0 {
				line = k
			} else {
				line += next
			}
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString("\n  " + dimStyle.Render(l))
	}
	return sb.String()
}

func (m LabModel) helpKeys() []string {
	if m.running {
		return []string{"x cancel", "ctrl+c quit"}
	}
	if m.view == labPinSelect {
		return []string{"↑↓ select A", "←→ select B", "Enter confirm", "Esc back"}
	}
	keys := []string{"↑↓ navigate", "←→ change", "Enter run", "p preset", "d diff"}
	if len(m.iterations) >= 2 {
		keys = append(keys, "c compare")
	}
	keys = append(keys, "s sweep", "m median", "W warmup", "e export", "w save", "L load")
	if m.lastError != nil {
		keys = append(keys, "r retry")
	}
	keys = append(keys, "q quit")
	return keys
}

func (m LabModel) viewParams(width int) string {
	compact := width < 50
	labelCol := 16
	valPad := 8
	if compact {
		labelCol = 10
		valPad = 6
	}

	var b strings.Builder

	if m.sweepActive {
		b.WriteString(filterActiveStyle.Render("⟳ Auto-sweep active") + "\n\n")
	}

	for i, p := range m.params {
		prefix := "  "
		if i == m.cursor {
			prefix = "▸ "
		}

		label := p.Label
		if compact {
			label = compactLabel(label)
		}
		paddedLabel := label
		for len(paddedLabel) < labelCol {
			paddedLabel += " "
		}

		if i == m.cursor {
			b.WriteString(filterActiveStyle.Render(prefix + paddedLabel))
		} else {
			b.WriteString(dimStyle.Render(prefix + paddedLabel))
		}

		if i == m.cursor {
			maxVals := len(p.Values)
			if compact && maxVals > 4 {
				// Show a window of 4 values centered on current
				start := p.Current - 1
				if start < 0 {
					start = 0
				}
				end := start + 4
				if end > len(p.Values) {
					end = len(p.Values)
					start = end - 4
					if start < 0 {
						start = 0
					}
				}
				for vi := start; vi < end; vi++ {
					padded := fmt.Sprintf(" %-*s", valPad, p.Values[vi])
					if vi == p.Current {
						b.WriteString(selectedValueStyle.Render(padded))
					} else {
						b.WriteString(dimStyle.Render(padded))
					}
				}
			} else {
				for vi, v := range p.Values {
					padded := fmt.Sprintf(" %-*s", valPad, v)
					if vi == p.Current {
						b.WriteString(selectedValueStyle.Render(padded))
					} else {
						b.WriteString(dimStyle.Render(padded))
					}
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

func compactLabel(label string) string {
	switch label {
	case "Compression":
		return "Comp"
	case "Replication":
		return "Repl"
	case "Partitions":
		return "Parts"
	case "Test Type":
		return "Type"
	case "Record Size":
		return "RecSize"
	case "Batch Size":
		return "Batch"
	case "Producers":
		return "Prod"
	case "Linger ms":
		return "Linger"
	default:
		if len(label) > 8 {
			return label[:8]
		}
		return label
	}
}

func (m LabModel) viewResults(width int) string {
	if m.view == labHistory && len(m.iterations) >= 2 {
		return m.viewDiff(width)
	}
	if m.view == labPinSelect {
		return m.viewPinSelect()
	}

	var b strings.Builder

	if len(m.iterations) == 0 {
		b.WriteString(dimStyle.Render("No iterations yet.\n\nTweak parameters on the left and\npress Enter to run your first test."))
		return b.String()
	}

	b.WriteString(detailTitleStyle.Render("Iteration History") + "\n\n")

	numW := 4
	deltaW := 8
	remaining := width - numW - deltaW - 6
	if remaining < 20 {
		remaining = 20
	}
	thrW := remaining * 40 / 100
	p99W := remaining * 30 / 100
	errW := remaining - thrW - p99W

	showErr := width >= 50

	if showErr {
		b.WriteString(fmt.Sprintf("  %s%s%s%s%s\n",
			dimStyle.Render(padRight("#", numW)),
			dimStyle.Render(padRight("Throughput", thrW)),
			dimStyle.Render(padRight("P99", p99W)),
			dimStyle.Render(padRight("Err", errW)),
			dimStyle.Render(padRight("Δ", deltaW)),
		))
	} else {
		b.WriteString(fmt.Sprintf("  %s%s%s%s\n",
			dimStyle.Render(padRight("#", numW)),
			dimStyle.Render(padRight("Throughput", thrW+errW)),
			dimStyle.Render(padRight("P99", p99W)),
			dimStyle.Render(padRight("Δ", deltaW)),
		))
	}

	sepW := width - 4
	if sepW < 10 {
		sepW = 10
	}
	b.WriteString(dimStyle.Render("  "+strings.Repeat("─", sepW)) + "\n\n")

	maxVisible := 10
	if m.height > 0 {
		maxVisible = (m.height - 14) / 2
		if maxVisible < 3 {
			maxVisible = 3
		}
		if maxVisible > 20 {
			maxVisible = 20
		}
	}
	start := 0
	if len(m.iterations) > maxVisible {
		start = len(m.iterations) - maxVisible
	}

	for _, iter := range m.iterations[start:] {
		delta := iter.Delta
		if delta == "" {
			delta = dimStyle.Render("—")
		}

		numStr := padRight(fmt.Sprintf("%d", iter.Number), numW)
		if showErr {
			thrStr := padRight(fmtLabNum(iter.Throughput)+" rec/s", thrW)
			p99Str := padRight(fmtLabFloat(iter.P99Ms)+"ms", p99W)
			errStr := padRight(fmtLabFloat(iter.ErrorRate), errW)
			b.WriteString(fmt.Sprintf("  %s%s%s%s%s\n",
				numStr,
				healthyStyle.Render(thrStr),
				warnStyle.Render(p99Str),
				dimStyle.Render(errStr),
				delta,
			))
		} else {
			thrStr := padRight(fmtLabNum(iter.Throughput), thrW+errW)
			p99Str := padRight(fmtLabFloat(iter.P99Ms), p99W)
			b.WriteString(fmt.Sprintf("  %s%s%s%s\n",
				numStr,
				healthyStyle.Render(thrStr),
				warnStyle.Render(p99Str),
				delta,
			))
		}
	}

	if len(m.iterations) > 1 {
		b.WriteString("\n")
		b.WriteString("  " + dimStyle.Render("Throughput: ") + sparkline(extractMetric(m.iterations, func(i labIteration) float64 { return i.Throughput })) + "\n")
		b.WriteString("  " + dimStyle.Render("P99 ms:     ") + sparkline(extractMetric(m.iterations, func(i labIteration) float64 { return i.P99Ms })) + "\n")
	}

	if len(m.iterations) > 0 {
		last := m.iterations[len(m.iterations)-1]
		b.WriteString("\n" + m.latencyHistogram(last.P99Ms, width))
	}

	return b.String()
}

func (m LabModel) latencyHistogram(p99 float64, width int) string {
	if p99 <= 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("  " + dimStyle.Render("Latency Distribution") + "\n")

	buckets := []struct {
		label string
		pct   float64
	}{
		{"<1ms ", 0.10},
		{"1-5ms", 0.25},
		{"5-10 ", 0.30},
		{"10-50", 0.20},
		{"50+ms", 0.15},
	}

	if p99 < 5 {
		buckets[0].pct = 0.50
		buckets[1].pct = 0.30
		buckets[2].pct = 0.15
		buckets[3].pct = 0.04
		buckets[4].pct = 0.01
	} else if p99 > 100 {
		buckets[0].pct = 0.02
		buckets[1].pct = 0.08
		buckets[2].pct = 0.15
		buckets[3].pct = 0.35
		buckets[4].pct = 0.40
	}

	maxBar := width - 16
	if maxBar < 8 {
		maxBar = 8
	}
	if maxBar > 40 {
		maxBar = 40
	}

	for _, bk := range buckets {
		barLen := int(bk.pct * float64(maxBar))
		if barLen < 1 {
			barLen = 1
		}
		bar := strings.Repeat("█", barLen)
		pctStr := fmt.Sprintf("%4.0f%%", bk.pct*100)
		sb.WriteString(fmt.Sprintf("  %s %s %s\n",
			dimStyle.Render(bk.label),
			healthyStyle.Render(bar),
			dimStyle.Render(pctStr),
		))
	}
	return sb.String()
}

func (m LabModel) viewPinSelect() string {
	var sb strings.Builder
	sb.WriteString(detailTitleStyle.Render("Select Iterations to Compare") + "\n\n")

	sb.WriteString(dimStyle.Render("  ↑↓ = iteration A    ←→ = iteration B") + "\n\n")

	for i, iter := range m.iterations {
		marker := "  "
		style := dimStyle
		if i == m.pinA {
			marker = "A "
			style = filterActiveStyle
		}
		if i == m.pinB {
			marker = "B "
			style = healthyStyle
		}
		if i == m.pinA && i == m.pinB {
			marker = "AB"
			style = warnStyle
		}
		sb.WriteString(style.Render(fmt.Sprintf("  %s #%-3d  %s rec/s  p99=%sms\n",
			marker, iter.Number, fmtLabNum(iter.Throughput), fmtLabFloat(iter.P99Ms))))
	}

	return sb.String()
}

func (m LabModel) viewDiff(width int) string {
	if len(m.iterations) < 2 {
		return dimStyle.Render("Need at least 2 iterations to diff")
	}

	idxA, idxB := m.pinA, m.pinB
	if idxA < 0 {
		idxA = len(m.iterations) - 2
	}
	if idxB < 0 {
		idxB = len(m.iterations) - 1
	}
	if idxA >= len(m.iterations) {
		idxA = len(m.iterations) - 1
	}
	if idxB >= len(m.iterations) {
		idxB = len(m.iterations) - 1
	}

	a := m.iterations[idxA]
	b := m.iterations[idxB]

	colW := (width - 8) / 4
	if colW < 8 {
		colW = 8
	}

	var sb strings.Builder
	sb.WriteString(detailTitleStyle.Render(fmt.Sprintf("Diff: #%d vs #%d", a.Number, b.Number)) + "\n\n")

	sb.WriteString(fmt.Sprintf("  %-*s %-*s %-*s %-*s\n",
		colW, dimStyle.Render("Metric"),
		colW, dimStyle.Render(fmt.Sprintf("#%d", a.Number)),
		colW, dimStyle.Render(fmt.Sprintf("#%d", b.Number)),
		colW, dimStyle.Render("Change"),
	))
	sepW := width - 4
	if sepW < 10 {
		sepW = 10
	}
	sb.WriteString(dimStyle.Render("  "+strings.Repeat("─", sepW)) + "\n")

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

		sb.WriteString(fmt.Sprintf("  %-*s %-*s %-*s %s\n",
			colW, label,
			colW, fmtLabNum(aVal)+unit,
			colW, fmtLabNum(bVal)+unit,
			changeStr,
		))
	}

	writeDiffRow("Throughput", a.Throughput, b.Throughput, " rec/s", true)
	writeDiffRow("P99 Latency", a.P99Ms, b.P99Ms, " ms", false)
	writeDiffRow("Avg Latency", a.ErrorRate, b.ErrorRate, " ms", false)

	if a.Params != nil && b.Params != nil {
		sb.WriteString("\n" + dimStyle.Render("  Parameter Changes") + "\n")
		paramSepW := width - 8
		if paramSepW < 10 {
			paramSepW = 10
		}
		sb.WriteString(dimStyle.Render("  "+strings.Repeat("─", paramSepW)) + "\n")
		for key, aVal := range a.Params {
			bVal := b.Params[key]
			if aVal != bVal {
				sb.WriteString(fmt.Sprintf("  %-*s %s → %s\n",
					colW,
					dimStyle.Render(key),
					warnStyle.Render(aVal),
					healthyStyle.Render(bVal),
				))
			}
		}
	}

	sb.WriteString("\n  " + dimStyle.Render("Press Esc to go back  ·  c to pick iterations"))

	return sb.String()
}

func (m LabModel) buildSpec() (*client.TestSpec, *client.CreateTestRequest) {
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

	durationMs := spec.Records / 1000 * 1000
	if durationMs < 60000 {
		durationMs = 60000
	}
	spec.DurationSeconds = durationMs

	req := &client.CreateTestRequest{
		TestType: m.paramVal("type"),
		Spec:     spec,
	}
	return spec, req
}

func (m LabModel) currentParams() map[string]string {
	params := make(map[string]string)
	for _, p := range m.params {
		params[p.Key] = p.Values[p.Current]
	}
	return params
}

func (m LabModel) runTest() tea.Cmd {
	return func() tea.Msg {
		spec, req := m.buildSpec()
		m.lastTestReq = req

		run, err := m.client.CreateTest(m.cancelCtx, req)
		if err != nil {
			return labTestDoneMsg{err: err}
		}
		m.runTestID = run.ID

		return m.pollTest(run.ID, spec.Records)
	}
}

func (m LabModel) retryTest() tea.Cmd {
	return func() tea.Msg {
		if m.lastTestReq == nil {
			return labTestDoneMsg{err: fmt.Errorf("no previous test to retry")}
		}

		run, err := m.client.CreateTest(m.cancelCtx, m.lastTestReq)
		if err != nil {
			return labTestDoneMsg{err: err}
		}
		m.runTestID = run.ID

		records := 50000
		if m.lastTestReq.Spec != nil {
			records = m.lastTestReq.Spec.Records
		}
		return m.pollTest(run.ID, records)
	}
}

func (m LabModel) pollTest(testID string, records int) tea.Msg {
	maxWait := 6 * time.Minute
	testType := strings.ToUpper(m.paramVal("type"))

	switch {
	case testType == "ENDURANCE":
		maxWait = 20 * time.Minute
	case records >= 1_000_000:
		maxWait = 15 * time.Minute
	case records >= 500_000:
		maxWait = 10 * time.Minute
	}

	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		select {
		case <-m.cancelCtx.Done():
			return labTestDoneMsg{err: fmt.Errorf("test cancelled")}
		case <-time.After(2 * time.Second):
		}

		updated, err := m.client.GetTest(context.Background(), testID)
		if err != nil {
			continue
		}

		if len(updated.Results) > 0 {
			r := updated.Results[0]
			// Send progress but we can't in this architecture — it updates on done
			_ = r
		}

		status := strings.ToUpper(updated.Status)
		if status == "DONE" || status == "COMPLETED" || status == "FAILED" || status == "ERROR" {
			return labTestDoneMsg{run: updated}
		}
	}
	return labTestDoneMsg{err: fmt.Errorf("test timed out after %s", maxWait.Truncate(time.Minute))}
}

func (m *LabModel) buildIteration(run *client.TestRun) labIteration {
	iter := labIteration{
		Number: len(m.iterations) + 1,
		TestID: truncID(run.ID),
		Params: m.currentParams(),
	}
	if len(run.Results) > 0 {
		var totalThroughput, totalP99 float64
		for _, res := range run.Results {
			totalThroughput += res.ThroughputRecordsPerSec
			totalP99 += res.P99LatencyMs
		}
		iter.Throughput = totalThroughput / float64(len(run.Results))
		iter.P99Ms = totalP99 / float64(len(run.Results))
		iter.ErrorRate = run.Results[len(run.Results)-1].AvgLatencyMs
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
	return iter
}

func (m *LabModel) computeMedianIteration() labIteration {
	// Sort by throughput and pick the middle element
	results := make([]labIteration, len(m.medianResults))
	copy(results, m.medianResults)
	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Throughput < results[i].Throughput {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
	median := results[len(results)/2]
	median.Number = len(m.iterations) + 1
	median.Delta = ""
	if len(m.iterations) > 0 {
		prev := m.iterations[len(m.iterations)-1]
		if prev.Throughput > 0 {
			pct := ((median.Throughput - prev.Throughput) / prev.Throughput) * 100
			if pct > 0 {
				median.Delta = healthyStyle.Render(fmt.Sprintf("▲%.0f%%", pct))
			} else if pct < 0 {
				median.Delta = errorStyle.Render(fmt.Sprintf("▼%.0f%%", -pct))
			}
		}
	}
	return median
}

func (m *LabModel) applyNextPreset() {
	current := -1
	for i, preset := range labPresets {
		if m.paramVal("type") == preset.Values["type"] &&
			m.paramVal("acks") == preset.Values["acks"] {
			current = i
			break
		}
	}
	next := (current + 1) % len(labPresets)
	preset := labPresets[next]

	for key, val := range preset.Values {
		for pi := range m.params {
			if m.params[pi].Key == key {
				for vi, v := range m.params[pi].Values {
					if v == val {
						m.params[pi].Current = vi
						break
					}
				}
			}
		}
	}
	m.status = filterActiveStyle.Render("⚡ Preset: " + preset.Name + " — " + preset.Desc)
}

func (m *LabModel) startSweep() {
	m.sweepParam = m.cursor
	m.sweepActive = true
	p := &m.params[m.sweepParam]
	p.Current = 0

	m.status = filterActiveStyle.Render(fmt.Sprintf("⟳ Sweeping %s (%d values)…", p.Label, len(p.Values)))

	m.running = true
	m.elapsed = 0
	m.liveRecords = 0
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelCtx = ctx
	m.cancelFn = cancel
}

func (m *LabModel) nextSweepStep() tea.Cmd {
	p := &m.params[m.sweepParam]
	if p.Current < len(p.Values)-1 {
		p.Current++
		m.running = true
		m.elapsed = 0
		m.liveRecords = 0
		ctx, cancel := context.WithCancel(context.Background())
		m.cancelCtx = ctx
		m.cancelFn = cancel
		m.status = filterActiveStyle.Render(fmt.Sprintf("⟳ Sweep %s = %s (%d/%d)",
			p.Label, p.Values[p.Current], p.Current+1, len(p.Values)))
		return m.runTest()
	}
	m.sweepActive = false
	m.status = healthyStyle.Render(fmt.Sprintf("✓ Sweep complete — %d iterations", len(p.Values)))
	return nil
}

func (m LabModel) exportCSV() string {
	dir, _ := os.UserHomeDir()
	path := filepath.Join(dir, fmt.Sprintf("kates-lab-%s.csv", time.Now().Format("20060102-150405")))

	var sb strings.Builder
	sb.WriteString("iteration,throughput_rec_s,p99_ms,avg_latency_ms,delta,test_id")
	for _, p := range m.params {
		sb.WriteString("," + p.Key)
	}
	sb.WriteString("\n")

	for _, iter := range m.iterations {
		sb.WriteString(fmt.Sprintf("%d,%.2f,%.2f,%.2f,%s,%s",
			iter.Number, iter.Throughput, iter.P99Ms, iter.ErrorRate,
			stripAnsi(iter.Delta), iter.TestID))
		for _, p := range m.params {
			val := ""
			if iter.Params != nil {
				val = iter.Params[p.Key]
			}
			sb.WriteString("," + val)
		}
		sb.WriteString("\n")
	}

	_ = os.WriteFile(path, []byte(sb.String()), 0644)
	return path
}

type labSession struct {
	Iterations []labSessionIter `json:"iterations"`
	Params     []labSessionParam `json:"params"`
}

type labSessionIter struct {
	Number     int               `json:"number"`
	Throughput float64           `json:"throughput"`
	P99Ms      float64           `json:"p99Ms"`
	ErrorRate  float64           `json:"errorRate"`
	TestID     string            `json:"testId"`
	Params     map[string]string `json:"params"`
}

type labSessionParam struct {
	Key     string `json:"key"`
	Current int    `json:"current"`
}

func (m LabModel) saveSession() string {
	dir, _ := os.UserHomeDir()
	path := filepath.Join(dir, ".kates-lab-session.json")

	session := labSession{}
	for _, iter := range m.iterations {
		session.Iterations = append(session.Iterations, labSessionIter{
			Number:     iter.Number,
			Throughput: iter.Throughput,
			P99Ms:      iter.P99Ms,
			ErrorRate:  iter.ErrorRate,
			TestID:     iter.TestID,
			Params:     iter.Params,
		})
	}
	for _, p := range m.params {
		session.Params = append(session.Params, labSessionParam{
			Key:     p.Key,
			Current: p.Current,
		})
	}

	data, _ := json.MarshalIndent(session, "", "  ")
	_ = os.WriteFile(path, data, 0644)
	return path
}

func (m *LabModel) loadSession() (string, bool) {
	dir, _ := os.UserHomeDir()
	path := filepath.Join(dir, ".kates-lab-session.json")

	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}

	var session labSession
	if err := json.Unmarshal(data, &session); err != nil {
		return "", false
	}

	m.iterations = nil
	for i, si := range session.Iterations {
		iter := labIteration{
			Number:     si.Number,
			Throughput: si.Throughput,
			P99Ms:      si.P99Ms,
			ErrorRate:  si.ErrorRate,
			TestID:     si.TestID,
			Params:     si.Params,
		}
		if i > 0 {
			prev := m.iterations[i-1]
			if prev.Throughput > 0 {
				pct := ((iter.Throughput - prev.Throughput) / prev.Throughput) * 100
				if pct > 0 {
					iter.Delta = healthyStyle.Render(fmt.Sprintf("▲%.0f%%", pct))
				} else if pct < 0 {
					iter.Delta = errorStyle.Render(fmt.Sprintf("▼%.0f%%", -pct))
				}
			}
		}
		m.iterations = append(m.iterations, iter)
	}

	for _, sp := range session.Params {
		for pi := range m.params {
			if m.params[pi].Key == sp.Key {
				m.params[pi].Current = sp.Current
			}
		}
	}

	return path, true
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

func extractMetric(iterations []labIteration, fn func(labIteration) float64) []float64 {
	vals := make([]float64, len(iterations))
	for i, it := range iterations {
		vals[i] = fn(it)
	}
	return vals
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

func stripAnsi(s string) string {
	var sb strings.Builder
	inEsc := false
	for _, c := range s {
		if c == '\033' {
			inEsc = true
			continue
		}
		if inEsc {
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
				inEsc = false
			}
			continue
		}
		sb.WriteRune(c)
	}
	return sb.String()
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
