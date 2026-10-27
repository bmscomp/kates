package theme

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"gopkg.in/yaml.v3"
)

// The looks, in the order T steps through them.
const (
	Classic   = "classic"
	Panels    = "panels"
	Minimal   = "minimal"
	Grid      = "grid"
	Commander = "commander"
	Chips     = "chips"
)

// Look is the shape kates draws with: frames, fills, title bars, the selected
// row, key hints and tables. Colors still come from the palette; a look only
// decides where they go. Classic is what kates has always drawn, and every
// helper below draws Classic exactly as its caller did before looks existed.
type Look struct {
	Name, Description string
	// Border replaces the border of every frame the look redraws.
	Border *lipgloss.Border
	// Edge and Focus color those frames; Focus is the focused pane's.
	Edge, Focus lipgloss.TerminalColor
	// Fill paints inside frames; Screen paints a whole full-screen view.
	Fill, Screen lipgloss.TerminalColor
	// FrameAll frames the panes Classic leaves open, like the lab's parameters.
	FrameAll bool
	// TitleInBorder sets a panel's title into its top border.
	TitleInBorder bool
}

// Classic reports whether l is the Classic look.
func (l *Look) Classic() bool { return l.Name == Classic }

var (
	rounded = lipgloss.RoundedBorder()
	square  = lipgloss.NormalBorder()
	double  = lipgloss.DoubleBorder()
	hidden  = lipgloss.HiddenBorder()

	// Commander paints its own blue screen, so its colors do not follow the
	// palette: they are chosen to read on that blue.
	CommanderBlue = lipgloss.Color("#102A6B")
	CommanderEdge = lipgloss.Color("#7DD3FC")
	CommanderBar  = lipgloss.Color("#22D3EE")
	CommanderInk  = lipgloss.Color("#0B1220")
	CommanderKey  = lipgloss.Color("#F1F5F9")

	// PaneFill is the fill of panes, striped rows and tab bars: the active
	// palette's Surface. Downsampled, Surface reads as navy on 256 colors and
	// as blue on 16, which is Commander's screen, so those terminals get a
	// gray, or nothing.
	PaneFill lipgloss.TerminalColor = fill{&paneFill}
	paneFill                        = paneFillOf(Active.Surface)
)

type fill struct {
	*lipgloss.CompleteAdaptiveColor
}

func paneFillOf(surface lipgloss.AdaptiveColor) lipgloss.CompleteAdaptiveColor {
	return lipgloss.CompleteAdaptiveColor{
		Light: lipgloss.CompleteColor{TrueColor: surface.Light, ANSI256: "255", ANSI: "7"},
		Dark:  lipgloss.CompleteColor{TrueColor: surface.Dark, ANSI256: "235", ANSI: "0"},
	}
}

// Looks are the built-in looks, by name.
var Looks = map[string]*Look{
	Classic: {Description: "The Kates look: what kates has always drawn"},
	Panels: {Description: "Every pane boxed and filled, the focused one in the accent color",
		Border: &rounded, Edge: Subtle, Focus: Accent, Fill: PaneFill, FrameAll: true},
	Minimal: {Description: "No frames, no fills: thin rules and spacing",
		Border: &hidden},
	Grid: {Description: "Square frames, titles in the border, column lines and striped rows",
		Border: &square, Edge: Subtle, Focus: Subtle, FrameAll: true, TitleInBorder: true},
	Commander: {Description: "A blue screen, double frames and a cyan selection bar",
		Border: &double, Edge: CommanderEdge, Focus: CommanderEdge, Screen: CommanderBlue, FrameAll: true, TitleInBorder: true},
	Chips: {Description: "No frames: status, values and keys as filled pills",
		Border: &hidden},
}

var lookOrder = []string{Classic, Panels, Minimal, Grid, Commander, Chips}

var (
	current atomic.Pointer[Look]
	// fullScreen is set once a full-screen view starts; termDark is the
	// terminal's own background, read before a screen color overrides it.
	fullScreen bool
	termDark   *bool
	// The look and palette when the first full-screen view started.
	screenLook, screenPalette string
)

func init() {
	for name, l := range Looks {
		l.Name = name
	}
	current.Store(Looks[SelectedLook()])
}

// FullScreen marks the process as drawing full-screen views, where a look
// with a screen color paints the whole terminal. Call it before the view
// starts, from the goroutine that renders it.
func FullScreen() {
	if !fullScreen {
		screenLook, screenPalette = CurrentLook().Name, activeName
	}
	fullScreen = true
	use(CurrentLook())
}

// SwitchedOnScreen is the look and the palette switched to in full-screen
// views with T and C, each "" when it is the one they started with.
func SwitchedOnScreen() (look, palette string) {
	if !fullScreen {
		return "", ""
	}
	if l := CurrentLook().Name; l != screenLook {
		look = l
	}
	if activeName != screenPalette {
		palette = activeName
	}
	return look, palette
}

// use switches to l. A painted screen is dark whatever the terminal is, so
// while one is shown the dark shades are drawn on it; leaving it gives the
// terminal's own background back.
func use(l *Look) {
	current.Store(l)
	painted := fullScreen && l.Screen != nil
	if !painted && termDark == nil {
		return // never overridden: lipgloss still asks the terminal
	}
	if termDark == nil {
		dark := lipgloss.HasDarkBackground()
		termDark = &dark
	}
	lipgloss.SetHasDarkBackground(painted || *termDark)
}

// LookNames lists the looks, Classic first, in the order T steps through them.
func LookNames() []string { return slices.Clone(lookOrder) }

// CurrentLook is the look kates draws with now.
func CurrentLook() *Look { return current.Load() }

// UseLook switches the look: everything drawn from now on uses it.
func UseLook(name string) error {
	l, ok := Looks[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return fmt.Errorf("unknown look: %s (use: %s)", name, strings.Join(lookOrder, ", "))
	}
	use(l)
	return nil
}

// NextLook switches to the look after the current one and returns its name.
func NextLook() string {
	i := slices.Index(lookOrder, CurrentLook().Name)
	name := lookOrder[(i+1)%len(lookOrder)]
	use(Looks[name])
	return name
}

// RequestedLook is the look asked for, before checking it exists: KATES_LOOK,
// else `look:` in ~/.kates.yaml, else "".
func RequestedLook() string { return pick(os.Getenv("KATES_LOOK"), readUserConfig().Look) }

// SelectedLook is the look a process starts with: the requested one when it
// exists, else Classic.
func SelectedLook() string {
	if _, ok := Looks[RequestedLook()]; ok {
		return RequestedLook()
	}
	return Classic
}

type userConfig struct {
	Theme string `yaml:"theme"`
	Look  string `yaml:"look"`
}

func readUserConfig() userConfig {
	var cfg userConfig
	home, err := os.UserHomeDir()
	if err != nil {
		return cfg
	}
	data, err := os.ReadFile(filepath.Join(home, ".kates.yaml"))
	if err != nil {
		return cfg
	}
	// A config that does not parse is the CLI's to report, not the theme's.
	_ = yaml.Unmarshal(data, &cfg)
	return cfg
}

// pick is the environment's value when set, else the config's, lower-cased.
func pick(env, cfg string) string {
	if v := strings.TrimSpace(env); v != "" {
		return strings.ToLower(v)
	}
	return strings.ToLower(strings.TrimSpace(cfg))
}

// ─── Drawing ─────────────────────────────────────────────────────────────────

// Box draws content in s's frame, redrawn in the current look: its border,
// edge color and fill. Every look keeps the frame's size.
func Box(s lipgloss.Style, focused bool, content string) string {
	l := CurrentLook()
	if l.Classic() {
		return s.Render(content)
	}
	s = s.Border(*l.Border)
	if edge := l.edge(focused); edge != nil {
		s = s.BorderForeground(edge)
	}
	if l.Fill != nil {
		// Wrap before filling: lipgloss breaks filled lines mid-word.
		if w := s.GetWidth() - s.GetHorizontalPadding(); w > 0 {
			content = lipgloss.NewStyle().Width(w).Render(content)
		}
		s = s.Background(l.Fill)
		content = Fill(content, l.Fill)
	}
	return s.Render(content)
}

func (l *Look) edge(focused bool) lipgloss.TerminalColor {
	if focused {
		return l.Focus
	}
	return l.Edge
}

// TitledBox is Box with a title: set into the top border when the look does
// that, else on the first line as Classic draws it.
func TitledBox(s lipgloss.Style, focused bool, title, content string) string {
	l := CurrentLook()
	if !l.TitleInBorder {
		return Box(s, focused, title+"\n"+content)
	}
	out := Box(s, focused, content)
	nl := strings.IndexByte(out, '\n')
	if nl < 0 {
		return out
	}
	t := " " + title + " "
	rest := lipgloss.Width(out[:nl]) - 3 - lipgloss.Width(t)
	if rest < 1 {
		return out
	}
	edge := lipgloss.NewStyle().Foreground(l.edge(focused))
	b := *l.Border
	return edge.Render(b.TopLeft+b.Top) + t + edge.Render(strings.Repeat(b.Top, rest)+b.TopRight) + out[nl:]
}

// OpenPaneInset is how many columns OpenPane takes from a pane's inside in
// the current look: its frame and the gap before the next pane.
func OpenPaneInset() int {
	if CurrentLook().FrameAll {
		return 3
	}
	return 0
}

// OpenPane draws a pane Classic leaves unframed. Looks that frame every pane
// box it inside s's width, a column short of the next pane; the others draw
// it as Classic does.
func OpenPane(s lipgloss.Style, focused bool, content string) string {
	if !CurrentLook().FrameAll {
		return s.Render(content)
	}
	return Box(s.Width(s.GetWidth()-OpenPaneInset()).MarginRight(1), focused, content)
}

// TitleBar draws a screen's title line, width columns wide, in the current
// look: title stands out and rest follows it. Classic callers keep their own
// bar, which differs from screen to screen.
func TitleBar(title, rest string, width int) string {
	l := CurrentLook()
	bold := lipgloss.NewStyle().Bold(true)
	// One line in every look: rest is cut to what the title, the padding and
	// a frame leave.
	rest = ansi.Truncate(rest, max(width-lipgloss.Width(title)-4, 0), "…")
	switch l.Name {
	case Panels, Grid:
		return Box(lipgloss.NewStyle().Padding(0, 1).Width(width-2), true,
			bold.Foreground(Primary).Render(title)+rest)
	case Minimal:
		return bold.Foreground(Text).Render(" "+title) + lipgloss.NewStyle().Foreground(Muted).Render(rest) +
			"\n" + lipgloss.NewStyle().Foreground(Subtle).Render(" "+strings.Repeat(square.Top, max(width-2, 0)))
	case Commander:
		return bold.Foreground(CommanderInk).Background(CommanderBar).Width(width).Render(" " + title + rest)
	case Chips:
		return bold.Foreground(OnColor).Background(Accent).Render(" "+title+" ") + lipgloss.NewStyle().Foreground(Text).Render(rest)
	}
	return bold.Render(title) + rest
}

// Pick draws the selected row or value in the current look; s is how Classic
// draws it, and every look keeps its width. Outside Classic the row is drawn
// in one style: a colored icon inside it would end the fill, or vanish when
// it has the fill's color.
func Pick(s lipgloss.Style, text string) string {
	if !CurrentLook().Classic() {
		text = ansi.Strip(text)
	}
	return PickStyle(s).Render(text)
}

// PickStyle is the style Pick draws with: s in Classic, else s with the
// current look's colors for a selection.
func PickStyle(s lipgloss.Style) lipgloss.Style {
	switch CurrentLook().Name {
	case Panels:
		return s.Foreground(Text).Background(Subtle).Bold(true)
	case Minimal:
		return s.UnsetBackground().Foreground(Text).Bold(true)
	case Grid:
		return s.Foreground(OnColor).Background(Info)
	case Commander:
		return s.Foreground(CommanderInk).Background(CommanderBar)
	case Chips:
		return s.Foreground(OnColor).Background(Accent).Bold(true)
	}
	return s
}

// Pill draws text as a filled pill in color c, for the Chips look.
func Pill(c lipgloss.TerminalColor, text string) string {
	return lipgloss.NewStyle().Foreground(OnColor).Background(c).Bold(true).Padding(0, 1).Render(text)
}

// KeyHints draws key hints such as "q quit" or "q: quit" in the current look,
// wrapped into lines no wider than width (0: one line). Classic callers keep
// their own hint lines.
func KeyHints(items []string, width int) []string {
	l := CurrentLook()
	keyStyle, labelStyle := lipgloss.NewStyle().Foreground(Muted), lipgloss.NewStyle().Foreground(Muted)
	sep, keyPad, labelPad := "  ", " ", " "
	switch l.Name {
	case Panels:
		keyStyle, labelStyle = keyStyle.Foreground(Accent).Bold(true), labelStyle.Foreground(Text)
		keyPad, labelPad = "", " "
	case Grid:
		keyStyle = keyStyle.Foreground(Text).Bold(true)
		sep, keyPad, labelPad = lipgloss.NewStyle().Foreground(Subtle).Render(" │ "), "", " "
	case Commander:
		keyStyle = keyStyle.Foreground(CommanderKey).Background(CommanderInk).Bold(true)
		labelStyle = labelStyle.Foreground(CommanderInk).Background(CommanderBar)
		sep = " "
	case Chips:
		keyStyle = keyStyle.Foreground(Text).Background(Subtle)
	default:
		keyPad = ""
	}

	var lines []string
	line := ""
	for _, it := range items {
		k, label, ok := strings.Cut(it, ": ")
		if !ok {
			k, label, _ = strings.Cut(it, " ")
		}
		h := keyStyle.Render(keyPad+k+keyPad) + labelStyle.Render(labelPad+label+keyPad)
		next := h
		if line != "" {
			next = line + sep + h
		}
		if width > 0 && line != "" && lipgloss.Width(next) > width {
			lines = append(lines, line)
			line = h
			continue
		}
		line = next
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// Screen paints a full-screen view edge to edge with the look's screen color,
// width columns by at least height lines. Looks without one return view.
func Screen(view string, width, height int) string {
	l := CurrentLook()
	if l.Screen == nil {
		return view
	}
	lines := strings.Split(Fill(view, l.Screen), "\n")
	for len(lines) < height {
		lines = append(lines, "")
	}
	bg := lipgloss.NewStyle().Background(l.Screen)
	for i, ln := range lines {
		if pad := width - lipgloss.Width(ln); pad > 0 {
			lines[i] = ln + bg.Render(strings.Repeat(" ", pad))
		}
	}
	return strings.Join(lines, "\n")
}

// Fill keeps bg behind every character of s: it sets bg at the start of each
// line and again after every reset, so styled spans inside a filled pane do
// not punch holes in it.
func Fill(s string, bg lipgloss.TerminalColor) string {
	seq, _, _ := strings.Cut(lipgloss.NewStyle().Background(bg).Render(" "), " ")
	if seq == "" {
		return s // no colors in this terminal: nothing to keep
	}
	// Both spellings of a reset: lipgloss writes the long one, its wrapping
	// the short one.
	reset, short := termenv.CSI+termenv.ResetSeq+"m", termenv.CSI+"m"
	again := strings.NewReplacer(reset, reset+seq, short, short+seq)
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = seq + again.Replace(ln) + reset
	}
	return strings.Join(lines, "\n")
}
