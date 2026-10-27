package theme

import (
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Palette is one complete set of design tokens. Every role has a light and a
// dark shade; lipgloss picks one from the terminal's background.
type Palette struct {
	Description string

	Accent, Text, Muted, Subtle, Surface lipgloss.AdaptiveColor
	Success, Warning, Error, Info        lipgloss.AdaptiveColor
	Primary, Secondary, Highlight        lipgloss.AdaptiveColor
	OnColor, OnDark                      lipgloss.AdaptiveColor
}

// DefaultName is the palette kates renders with unless told otherwise.
const DefaultName = "default"

// defaultPalette is the Kates look. Its values must not change: users who
// never pick a theme see exactly this.
var defaultPalette = Palette{
	Description: "The Kates look: blue for OK, red for KO",
	Accent:      adaptive("#2563EB", "#60A5FA"),
	Text:        adaptive("#1E293B", "#E2E8F0"),
	Muted:       adaptive("#6B7280", "#9CA3AF"),
	Subtle:      adaptive("#D1D5DB", "#4B5563"),
	Surface:     adaptive("#F3F4F6", "#1F2937"),
	// Blue (not green) — aligns with the kates blue=OK / red=KO palette.
	Success:   adaptive("#2563EB", "#60A5FA"),
	Warning:   adaptive("#C2410C", "#FB923C"),
	Error:     adaptive("#DC2626", "#F87171"),
	Info:      adaptive("#0891B2", "#22D3EE"),
	Primary:   adaptive("#1D4ED8", "#93C5FD"),
	Secondary: adaptive("#6D28D9", "#C4B5FD"),
	Highlight: adaptive("#059669", "#34D399"),
	OnColor:   adaptive("#000000", "#000000"),
	OnDark:    adaptive("#FFFFFF", "#F9FAFB"),
}

// Palettes are the built-in themes, by name.
var Palettes = map[string]Palette{
	DefaultName: defaultPalette,

	// aurora is tuned on both backgrounds: 700-weight shades on light
	// terminals, 300–400 on dark, so every role keeps its contrast.
	"aurora": {
		Description: "Teal and violet, tuned for light and dark terminals",
		Accent:      adaptive("#0F766E", "#2DD4BF"),
		Text:        adaptive("#0F172A", "#E6EDF3"),
		Muted:       adaptive("#64748B", "#94A3B8"),
		Subtle:      adaptive("#CBD5E1", "#334155"),
		Surface:     adaptive("#F1F5F9", "#111827"),
		Success:     adaptive("#15803D", "#4ADE80"),
		Warning:     adaptive("#B45309", "#FBBF24"),
		Error:       adaptive("#BE123C", "#FB7185"),
		Info:        adaptive("#0369A1", "#7DD3FC"),
		Primary:     adaptive("#4338CA", "#A5B4FC"),
		Secondary:   adaptive("#7E22CE", "#D8B4FE"),
		Highlight:   adaptive("#0E7490", "#67E8F9"),
		OnColor:     adaptive("#0B1020", "#0B1020"),
		OnDark:      adaptive("#FFFFFF", "#F8FAFC"),
	},

	"dracula":   darkOnly("Dracula", [8]string{"#BD93F9", "#8BE9FD", "#50FA7B", "#F1FA8C", "#FF5555", "#6272A4", "#F8F8F2", "#44475A"}),
	"gruvbox":   darkOnly("Gruvbox dark", [8]string{"#D3869B", "#83A598", "#B8BB26", "#FABD2F", "#FB4934", "#928374", "#EBDBB2", "#504945"}),
	"monokai":   darkOnly("Monokai", [8]string{"#AE81FF", "#66D9EF", "#A6E22E", "#E6DB74", "#F92672", "#75715E", "#F8F8F2", "#49483E"}),
	"solarized": darkOnly("Solarized dark", [8]string{"#6C71C4", "#2AA198", "#859900", "#B58900", "#DC322F", "#586E75", "#93A1A1", "#073642"}),
}

// darkOnly builds a palette from a dark-terminal scheme's primary, secondary,
// success, warning, error, muted, text and subtle colors. On a light terminal
// those colors are unreadable, so the light shades stay the default's.
func darkOnly(desc string, c [8]string) Palette {
	d := defaultPalette
	mix := func(light lipgloss.AdaptiveColor, dark string) lipgloss.AdaptiveColor {
		return adaptive(light.Light, dark)
	}
	return Palette{
		Description: desc + " (dark terminals; light ones keep the default shades)",
		Accent:      mix(d.Accent, c[0]),
		Text:        mix(d.Text, c[6]),
		Muted:       mix(d.Muted, c[5]),
		Subtle:      mix(d.Subtle, c[7]),
		Surface:     mix(d.Surface, c[7]),
		Success:     mix(d.Success, c[2]),
		Warning:     mix(d.Warning, c[3]),
		Error:       mix(d.Error, c[4]),
		Info:        mix(d.Info, c[1]),
		Primary:     mix(d.Primary, c[0]),
		Secondary:   mix(d.Secondary, c[1]),
		Highlight:   mix(d.Highlight, c[2]),
		OnColor:     d.OnColor,
		OnDark:      d.OnDark,
	}
}

// Names lists the built-in themes, default first.
func Names() []string {
	names := make([]string, 0, len(Palettes))
	for n := range Palettes {
		if n != DefaultName {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return append([]string{DefaultName}, names...)
}

// Requested is the theme asked for, before checking it exists: KATES_THEME,
// else `theme:` in ~/.kates.yaml, else "".
func Requested() string { return pick(os.Getenv("KATES_THEME"), readUserConfig().Theme) }

// ActivePalette names the palette kates draws with now.
func ActivePalette() string { return activeName }

// UsePalette switches the palette: every style built from the tokens draws
// with it from now on. Call it from the goroutine that renders.
func UsePalette(name string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	p, ok := Palettes[name]
	if !ok {
		return fmt.Errorf("unknown theme: %s (use: %s)", name, strings.Join(Names(), ", "))
	}
	Active, activeName = p, name
	paneFill = paneFillOf(Active.Surface)
	return nil
}

// NextPalette switches to the palette after the active one and returns its
// name.
func NextPalette() string {
	names := Names()
	name := names[(slices.Index(names, activeName)+1)%len(names)]
	_ = UsePalette(name)
	return name
}

// Selected is the theme this process uses: the requested one when it exists,
// else the default.
func Selected() string {
	if _, ok := Palettes[Requested()]; ok {
		return Requested()
	}
	return DefaultName
}
