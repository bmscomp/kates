package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/bmscomp/kates/cli/pkg/theme"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"
	"golang.org/x/term"
)

func TermWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}
	return 120
}

var plainMode bool

// SetPlain only ever forces the color profile DOWN. With plain=false it leaves
// lipgloss's automatic termenv detection in charge, which is what honors
// NO_COLOR, TERM=dumb, non-truecolor terminals (tmux without Tc, Terminal.app
// get ANSI-256/16 conversions instead of raw 38;2 sequences), and the
// downgrade to no styling when stdout is a pipe. The previous version forced
// TrueColor on every run, which defeated all of that at once: escape codes in
// piped output and CI logs, and NO_COLOR silently ignored.
func SetPlain(plain bool) {
	plainMode = plain
	if plain {
		lipgloss.SetColorProfile(termenv.Ascii)
	}
}

// ColumnWidth returns the width available for a column after reserving
// 'reserved' characters, with a guaranteed minimum of 'minWidth'.
// Use this instead of duplicating terminal-width calculations.
func ColumnWidth(reserved, minWidth int) int {
	w := TermWidth() - reserved
	if w < minWidth {
		return minWidth
	}
	return w
}

var (
	// Out is the standard output writer, defaulting to os.Stdout.
	// Can be overridden for testing.
	Out io.Writer = os.Stdout
	// Err is the standard error writer, defaulting to os.Stderr.
	// Can be overridden for testing.
	Err io.Writer = os.Stderr
)

// ResetForTesting redirects output and error streams to a bytes.Buffer
// and returns it so that test assertions can be made against the CLI output.
func ResetForTesting() *bytes.Buffer {
	buf := new(bytes.Buffer)
	Out = buf
	Err = buf
	return buf
}

// Exported color aliases — backed by theme tokens so all packages share
// a single adaptive palette. Use these (not raw lipgloss.Color) everywhere.
var (
	Purple  = theme.Secondary                                           // violet/purple tones
	Indigo  = theme.Primary                                             // deep blue headings
	Cyan    = theme.Info                                                // teal accents
	Green   = theme.Success                                             // positive states
	Red     = theme.Error                                               // error / danger
	Amber   = theme.Warning                                             // warnings
	Gray    = theme.Muted                                               // secondary text
	Light   = theme.Text                                                // primary text
	Dim     = lipgloss.AdaptiveColor{Light: "#9CA3AF", Dark: "#6B7280"} // truly visible dim
	Surface = theme.Surface                                             // panel backgrounds

	HeaderColor    = theme.Secondary
	KeyColor       = theme.Secondary
	BorderColor    = theme.Subtle
	SeparatorColor = theme.Subtle

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
	LightStyle   = lipgloss.NewStyle()

	KeyStyle = lipgloss.NewStyle().
			Foreground(KeyColor).
			Width(24)

	ValueStyle = lipgloss.NewStyle()

	BoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(BorderColor).
			Padding(0, 2)

	ActiveBadge = lipgloss.NewStyle().
			Foreground(theme.OnColor).
			Background(Green).
			Bold(true).
			Padding(0, 1)

	TagStyle = lipgloss.NewStyle().
			Foreground(theme.OnColor).
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
	fmt.Fprintln(Out)
	fmt.Fprintln(Out, bar)
	fmt.Fprintln(Out, title)
	fmt.Fprintln(Out, bar)
}

func SubHeader(text string) {
	fmt.Fprintln(Out)
	fmt.Fprintln(Out, SubHeaderStyle.Render("▸ "+text))
}

func KeyValue(key, value string) {
	fmt.Fprintf(Out, "  %s %s\n", KeyStyle.Render(key), ValueStyle.Render(value))
}

func KeyValueIndent(key, value string, indent int) {
	prefix := strings.Repeat("  ", indent)
	fmt.Fprintf(Out, "%s%s %s\n", prefix, KeyStyle.Render(key), ValueStyle.Render(value))
}

func Success(msg string) {
	fmt.Fprintln(Out, SuccessStyle.Render("  ✓ "+msg))
}

func Warn(msg string) {
	fmt.Fprintln(Out, WarningStyle.Render("  ⚠ "+msg))
}

func Error(msg string) {
	fmt.Fprintln(Err, ErrorStyle.Render("  ✖ "+msg))
}

func Hint(msg string) {
	fmt.Fprintln(Out, DimStyle.Render("  "+msg))
}

func JSON(v interface{}) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		Error("Failed to format JSON: " + err.Error())
		return
	}
	fmt.Fprintln(Out, string(data))
}

func RawJSON(data []byte) {
	var v interface{}
	if json.Unmarshal(data, &v) == nil {
		JSON(v)
	} else {
		fmt.Fprintln(Out, string(data))
	}
}

func Table(headers []string, rows [][]string) {
	if len(rows) == 0 {
		Hint("No data to display.")
		return
	}

	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = runewidth.StringWidth(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) {
				pure := stripAnsi(cell)
				if w := runewidth.StringWidth(pure); w > widths[i] {
					widths[i] = w
				}
			}
		}
	}

	headerFg := lipgloss.NewStyle().Bold(true).Foreground(KeyColor)
	sepFg := lipgloss.NewStyle().Foreground(SeparatorColor)
	cellFg := lipgloss.NewStyle()

	var headerLine, sepLine string
	for i, h := range headers {
		headerLine += "  " + headerFg.Render(PadRight(h, widths[i]))
	}
	for i := range headers {
		sepLine += "  " + sepFg.Render(strings.Repeat("─", widths[i]))
	}

	fmt.Fprintln(Out, headerLine)
	fmt.Fprintln(Out, sepLine)

	for _, row := range rows {
		var line string
		for i, cell := range row {
			if i >= len(widths) {
				continue
			}
			pure := stripAnsi(cell)
			upper := strings.ToUpper(pure)
			padded := PadRight(pure, widths[i])

			var rendered string
			switch upper {
			case "UP", "DONE", "PASS", "ENABLED":
				rendered = SuccessStyle.Bold(true).Render(pure) + padded[len(pure):]
			case "RUNNING", "PENDING":
				rendered = AccentStyle.Bold(true).Render(pure) + padded[len(pure):]
			case "FAILED", "ERROR", "DOWN", "DISABLED", "FAIL":
				rendered = ErrorStyle.Render(pure) + padded[len(pure):]
			case "DEGRADED", "STOPPING":
				rendered = WarningStyle.Render(pure) + padded[len(pure):]
			case "▲":
				rendered = ErrorStyle.Render("▲") + padded[len("▲"):]
			case "▼":
				rendered = SuccessStyle.Render("▼") + padded[len("▼"):]
			case "→":
				rendered = SuccessStyle.Bold(true).Render("→") + padded[len("→"):]
			default:
				rendered = cellFg.Render(padded)
			}
			line += "  " + rendered
		}
		fmt.Fprintln(Out, line)
	}
	fmt.Fprintln(Out)
}

func Divider() {
	fmt.Fprintln(Out, DimStyle.Render("  "+strings.Repeat("·", 40)))
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
	fmt.Fprintln(Out)
	fmt.Fprintln(Out, box.Render(content))
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
	fmt.Fprintf(Out, "  %-20s %s  %.1f\n", label, barStyled, value)
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

type ConfigEntry struct {
	Key    string
	Value  string
	Suffix string
}

func ConfigList(title string, entries []ConfigEntry) {
	if len(entries) == 0 {
		return
	}

	maxKey := 0
	for _, e := range entries {
		if len(e.Key) > maxKey {
			maxKey = len(e.Key)
		}
	}

	titleStyled := lipgloss.NewStyle().Bold(true).Foreground(Indigo).Render("▸ " + title)
	countStyled := lipgloss.NewStyle().Foreground(Gray).Render(fmt.Sprintf("  (%d)", len(entries)))
	sep := lipgloss.NewStyle().Foreground(SeparatorColor).Render(strings.Repeat("─", 50))

	fmt.Fprintln(Out)
	fmt.Fprintln(Out, titleStyled+countStyled)
	fmt.Fprintln(Out, "  "+sep)

	keyFg := lipgloss.NewStyle().Foreground(Cyan)
	valFg := lipgloss.NewStyle().Foreground(Light)
	eqFg := lipgloss.NewStyle().Foreground(Dim)
	suffFg := lipgloss.NewStyle().Foreground(Gray)
	bulletFg := lipgloss.NewStyle().Foreground(Purple)

	for _, e := range entries {
		key := keyFg.Render(e.Key)
		paddedKey := PadRight(key, maxKey+len(key)-len(e.Key))

		val := e.Value
		if val == "" {
			val = suffFg.Render("(empty)")
		} else if len(val) > 60 {
			val = val[:57] + "..."
		}

		line := "  " + bulletFg.Render("•") + "  " + paddedKey + eqFg.Render("  =  ") + valFg.Render(val)
		if e.Suffix != "" {
			line += "  " + suffFg.Render(e.Suffix)
		}
		fmt.Fprintln(Out, line)
	}
}

func PadRight(s string, width int) string {
	pure := stripAnsi(s)
	w := runewidth.StringWidth(pure)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
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
