package output

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	Purple  = lipgloss.AdaptiveColor{Light: "#7C3AED", Dark: "#7C3AED"}
	Indigo  = lipgloss.AdaptiveColor{Light: "#4F46E5", Dark: "#6366F1"}
	Cyan    = lipgloss.AdaptiveColor{Light: "#0891B2", Dark: "#06B6D4"}
	Green   = lipgloss.AdaptiveColor{Light: "#059669", Dark: "#10B981"}
	Red     = lipgloss.AdaptiveColor{Light: "#DC2626", Dark: "#EF4444"}
	Amber   = lipgloss.AdaptiveColor{Light: "#D97706", Dark: "#F59E0B"}
	Gray    = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#6B7280"}
	Light   = lipgloss.AdaptiveColor{Light: "#1F2937", Dark: "#E5E7EB"}
	Dim     = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#4B5563"}
	Surface = lipgloss.AdaptiveColor{Light: "#F3F4F6", Dark: "#1F2937"}

	HeaderColor    = lipgloss.AdaptiveColor{Light: "#6D28D9", Dark: "#C4B5FD"}
	KeyColor       = lipgloss.AdaptiveColor{Light: "#7C3AED", Dark: "#A78BFA"}
	BorderColor    = lipgloss.AdaptiveColor{Light: "#D1D5DB", Dark: "#374151"}
	SeparatorColor = lipgloss.AdaptiveColor{Light: "#D1D5DB", Dark: "#374151"}

	HeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(Purple).
			PaddingBottom(1)

	SubHeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(Indigo).
			PaddingLeft(1)

	SuccessStyle = lipgloss.NewStyle().Foreground(Green)
	ErrorStyle   = lipgloss.NewStyle().Foreground(Red).Bold(true)
	WarningStyle = lipgloss.NewStyle().Foreground(Amber)
	DimStyle     = lipgloss.NewStyle().Foreground(Gray)
	AccentStyle  = lipgloss.NewStyle().Foreground(Cyan)
	LightStyle   = lipgloss.NewStyle().Foreground(Light)

	KeyStyle = lipgloss.NewStyle().
			Foreground(KeyColor).
			Width(24)

	ValueStyle = lipgloss.NewStyle().Foreground(Light)

	BoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(BorderColor).
			Padding(0, 2)

	ActiveBadge = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#000000")).
			Background(Green).
			Bold(true).
			Padding(0, 1)

	TagStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#000000")).
			Background(Cyan).
			Padding(0, 1)
)

func StatusBadge(status string) string {
	upper := strings.ToUpper(status)
	switch upper {
	case "UP", "DONE", "PASS", "COMPLETED", "ENABLED":
		return SuccessStyle.Bold(true).Render("● " + upper)
	case "RUNNING", "PENDING":
		return AccentStyle.Bold(true).Render("◉ " + upper)
	case "DEGRADED", "STOPPING":
		return WarningStyle.Bold(true).Render("◈ " + upper)
	case "DOWN", "FAILED", "ERROR", "FAIL":
		return ErrorStyle.Render("✖ " + upper)
	case "DISABLED":
		return DimStyle.Render("○ " + upper)
	default:
		return DimStyle.Render("○ " + status)
	}
}

func Header(text string) {
	bar := lipgloss.NewStyle().Foreground(Purple).Render("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	title := lipgloss.NewStyle().Bold(true).Foreground(HeaderColor).Render("  " + text)
	fmt.Println()
	fmt.Println(bar)
	fmt.Println(title)
	fmt.Println(bar)
}

func SubHeader(text string) {
	fmt.Println()
	fmt.Println(SubHeaderStyle.Render("▸ " + text))
}

func KeyValue(key, value string) {
	fmt.Printf("  %s %s\n", KeyStyle.Render(key), ValueStyle.Render(value))
}

func KeyValueIndent(key, value string, indent int) {
	prefix := strings.Repeat("  ", indent)
	fmt.Printf("%s%s %s\n", prefix, KeyStyle.Render(key), ValueStyle.Render(value))
}

func Success(msg string) {
	fmt.Println(SuccessStyle.Render("  ✓ " + msg))
}

func Warn(msg string) {
	fmt.Println(WarningStyle.Render("  ⚠ " + msg))
}

func Error(msg string) {
	fmt.Fprintln(os.Stderr, ErrorStyle.Render("  ✖ "+msg))
}

func Hint(msg string) {
	fmt.Println(DimStyle.Render("  " + msg))
}

func JSON(v interface{}) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		Error("Failed to format JSON: " + err.Error())
		return
	}
	fmt.Println(string(data))
}

func RawJSON(data []byte) {
	var v interface{}
	if json.Unmarshal(data, &v) == nil {
		JSON(v)
	} else {
		fmt.Println(string(data))
	}
}

func Table(headers []string, rows [][]string) {
	if len(rows) == 0 {
		Hint("No data to display.")
		return
	}

	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) {
				pure := stripAnsi(cell)
				if len(pure) > widths[i] {
					widths[i] = len(pure)
				}
			}
		}
	}

	headerFg := lipgloss.NewStyle().Bold(true).Foreground(KeyColor)
	sepFg := lipgloss.NewStyle().Foreground(SeparatorColor)
	cellFg := lipgloss.NewStyle().Foreground(Light)

	var headerLine, sepLine string
	for i, h := range headers {
		headerLine += "  " + headerFg.Render(padRight(h, widths[i]))
	}
	for i := range headers {
		sepLine += "  " + sepFg.Render(strings.Repeat("─", widths[i]))
	}

	fmt.Println(headerLine)
	fmt.Println(sepLine)

	for _, row := range rows {
		var line string
		for i, cell := range row {
			if i >= len(widths) {
				continue
			}
			pure := stripAnsi(cell)
			upper := strings.ToUpper(pure)
			extraLen := len(cell) - len(pure)

			switch upper {
			case "UP", "DONE", "PASS", "ENABLED":
				line += "  " + padRight(SuccessStyle.Bold(true).Render(pure), widths[i]+extraLen+len(SuccessStyle.Bold(true).Render(pure))-len(pure))
			case "RUNNING", "PENDING":
				line += "  " + padRight(AccentStyle.Bold(true).Render(pure), widths[i]+extraLen+len(AccentStyle.Bold(true).Render(pure))-len(pure))
			case "FAILED", "ERROR", "DOWN", "DISABLED":
				line += "  " + padRight(ErrorStyle.Render(pure), widths[i]+extraLen+len(ErrorStyle.Render(pure))-len(pure))
			case "DEGRADED", "STOPPING":
				line += "  " + padRight(WarningStyle.Render(pure), widths[i]+extraLen+len(WarningStyle.Render(pure))-len(pure))
			case "▲":
				line += "  " + padRight(ErrorStyle.Render("▲"), widths[i]+extraLen+len(ErrorStyle.Render("▲"))-len("▲"))
			case "▼":
				line += "  " + padRight(SuccessStyle.Render("▼"), widths[i]+extraLen+len(SuccessStyle.Render("▼"))-len("▼"))
			case "→":
				line += "  " + padRight(SuccessStyle.Bold(true).Render("→"), widths[i]+extraLen+len(SuccessStyle.Bold(true).Render("→"))-len("→"))
			default:
				if extraLen > 0 {
					line += "  " + padRight(cell, widths[i]+extraLen)
				} else {
					line += "  " + cellFg.Render(padRight(cell, widths[i]))
				}
			}
		}
		fmt.Println(line)
	}
	fmt.Println()
}

func Divider() {
	fmt.Println(DimStyle.Render("  " + strings.Repeat("·", 40)))
}

func Banner(title, subtitle string) {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(HeaderColor).
		Padding(0, 1)
	subStyle := lipgloss.NewStyle().
		Foreground(Cyan).
		Padding(0, 1)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Purple).
		Padding(0, 2)

	content := titleStyle.Render(title) + "\n" + subStyle.Render(subtitle)
	fmt.Println()
	fmt.Println(box.Render(content))
}

func MetricBar(label string, value, max float64) {
	barWidth := 20
	filled := int((value / max) * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}
	if filled < 0 {
		filled = 0
	}

	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)

	barColor := Green
	ratio := value / max
	if ratio > 0.8 {
		barColor = Red
	} else if ratio > 0.5 {
		barColor = Amber
	}

	barStyled := lipgloss.NewStyle().Foreground(barColor).Render(bar)
	fmt.Printf("  %-20s %s  %.1f\n", label, barStyled, value)
}

func Sparkline(values []float64) string {
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
	span := max - min
	if span == 0 {
		span = 1
	}
	var sb strings.Builder
	for _, v := range values {
		idx := int(((v - min) / span) * 7)
		if idx > 7 {
			idx = 7
		}
		if idx < 0 {
			idx = 0
		}
		sb.WriteRune(blocks[idx])
	}
	return AccentStyle.Render(sb.String())
}

func SparklineColored(values []float64, higherIsBetter bool) string {
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
	span := max - min
	if span == 0 {
		span = 1
	}
	var sb strings.Builder
	for _, v := range values {
		idx := int(((v - min) / span) * 7)
		if idx > 7 {
			idx = 7
		}
		if idx < 0 {
			idx = 0
		}
		ratio := (v - min) / span
		var color lipgloss.AdaptiveColor
		if higherIsBetter {
			if ratio > 0.66 {
				color = Green
			} else if ratio > 0.33 {
				color = Amber
			} else {
				color = Red
			}
		} else {
			if ratio > 0.66 {
				color = Red
			} else if ratio > 0.33 {
				color = Amber
			} else {
				color = Green
			}
		}
		sb.WriteString(lipgloss.NewStyle().Foreground(color).Render(string(blocks[idx])))
	}
	return sb.String()
}

func Panel(title, content string, width int) string {
	titleStyled := lipgloss.NewStyle().Bold(true).Foreground(HeaderColor).Render(title)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(BorderColor).
		Width(width).
		Padding(0, 1)
	return box.Render(titleStyled + "\n" + content)
}

func padRight(s string, width int) string {
	pure := stripAnsi(s)
	if len(pure) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(pure))
}

func stripAnsi(s string) string {
	var result []byte
	inEsc := false
	for i := 0; i < len(s); i++ {
		if s[i] == '\033' {
			inEsc = true
			continue
		}
		if inEsc {
			if (s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z') {
				inEsc = false
			}
			continue
		}
		result = append(result, s[i])
	}
	return string(result)
}
