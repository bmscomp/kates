package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/theme"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var themeCmd = &cobra.Command{
	Use:   "theme",
	Short: "List, preview and choose the CLI color theme",
	Long: `Kates renders with one color theme, chosen when it starts:
KATES_THEME if set, else "theme:" in ~/.kates.yaml, else "default".`,
}

var themeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the built-in themes",
	RunE: func(cmd *cobra.Command, args []string) error {
		if outputMode == "json" {
			return noJSONForm(cmd)
		}
		fmt.Println()
		output.Header("Themes")
		fmt.Println()
		active := theme.Selected()
		for _, name := range theme.Names() {
			p := theme.Palettes[name]
			mark := "  "
			if name == active {
				mark = lipgloss.NewStyle().Foreground(p.Success).Render("● ")
			}
			label := output.PadRight(lipgloss.NewStyle().Bold(true).Foreground(p.Primary).Render(name), 12)
			fmt.Printf("  %s%s %s  %s\n", mark, label, swatches(p), lipgloss.NewStyle().Foreground(p.Muted).Render(p.Description))
		}
		fmt.Println()
		if req := theme.Requested(); req != "" && req != active {
			output.Warn(fmt.Sprintf("theme %q is not a built-in theme; using %s", req, active))
		}
		output.Hint("kates theme preview <name> shows one; kates theme set <name> uses it.")
		return nil
	},
}

var themePreviewCmd = &cobra.Command{
	Use:   "preview <name>",
	Short: "Preview a theme on sample output",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if outputMode == "json" {
			return noJSONForm(cmd)
		}
		name, p, err := lookupTheme(args[0])
		if err != nil {
			return err
		}
		fg := func(c lipgloss.TerminalColor) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

		fmt.Println()
		fmt.Println("  " + fg(p.Primary).Bold(true).Render("Theme: "+name) + "  " + fg(p.Muted).Render(p.Description))
		fmt.Println()
		roles := []struct {
			name string
			c    lipgloss.AdaptiveColor
		}{
			{"Primary", p.Primary}, {"Secondary", p.Secondary}, {"Accent", p.Accent},
			{"Info", p.Info}, {"Highlight", p.Highlight}, {"Success", p.Success},
			{"Warning", p.Warning}, {"Error", p.Error}, {"Text", p.Text},
			{"Muted", p.Muted}, {"Subtle", p.Subtle}, {"Surface", p.Surface},
		}
		for _, r := range roles {
			block := lipgloss.NewStyle().Background(r.c).Render("      ")
			fmt.Printf("  %s  %s %s\n", block, output.PadRight(fg(r.c).Render(r.name), 10),
				fg(p.Muted).Render(r.c.Light+" light · "+r.c.Dark+" dark"))
		}

		// The same pieces kates draws: a header, a status line, a table row, a badge.
		box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(p.Accent).Padding(0, 1)
		badge := lipgloss.NewStyle().Background(p.Accent).Foreground(p.OnColor).Bold(true).Padding(0, 1)
		sample := strings.Join([]string{
			fg(p.Primary).Bold(true).Render("Kates Lab") + fg(p.Muted).Render(" · ") + fg(p.Text).Render("Interactive Performance Tuning"),
			fg(p.Secondary).Render("Parameters") + "   " + badge.Render("LOAD") + " " + fg(p.Muted).Render("STRESS  SPIKE"),
			fg(p.Text).Render("#2  ") + fg(p.Info).Render("52.1K rec/s  ") + fg(p.Success).Render("8.00ms  ▲15%"),
			fg(p.Text).Render("#3  ") + fg(p.Info).Render("48.7K rec/s  ") + fg(p.Warning).Render("11.00ms ") + fg(p.Error).Render("▼7%"),
			fg(p.Success).Render("✓ PASS") + "  " + fg(p.Error).Render("✖ FAIL") + "  " + fg(p.Warning).Render("⚠ WARN") + "  " + fg(p.Highlight).Render("▸ selected") + "  " + fg(p.Muted).Render("dim text"),
		}, "\n")
		fmt.Println()
		fmt.Println(lipgloss.NewStyle().MarginLeft(2).Render(box.Render(sample)))
		fmt.Println()
		return nil
	},
}

var themeSetCmd = &cobra.Command{
	Use:   "set <name>",
	Short: "Use a theme from now on (saved in ~/.kates.yaml)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _, err := lookupTheme(args[0])
		if err != nil {
			return err
		}
		if err := updateConfig(func(cfg *Config) error {
			stored := name
			if name == theme.DefaultName {
				stored = ""
			}
			if cfg.Theme == stored {
				return errConfigUnchanged
			}
			cfg.Theme = stored
			return nil
		}); err != nil {
			return err
		}
		output.Success("Theme set to " + name + "; the next kates command uses it.")
		if env := strings.ToLower(strings.TrimSpace(os.Getenv("KATES_THEME"))); env != "" && env != name {
			output.Hint("KATES_THEME=" + env + " is set and overrides it in this shell.")
		}
		return nil
	},
}

func lookupTheme(arg string) (string, theme.Palette, error) {
	name := strings.ToLower(strings.TrimSpace(arg))
	p, ok := theme.Palettes[name]
	if !ok {
		return "", p, cmdErr("unknown theme: " + arg + " (use: " + strings.Join(theme.Names(), ", ") + ")")
	}
	return name, p, nil
}

func swatches(p theme.Palette) string {
	var b strings.Builder
	for _, c := range []lipgloss.AdaptiveColor{p.Primary, p.Secondary, p.Success, p.Warning, p.Error} {
		b.WriteString(lipgloss.NewStyle().Foreground(c).Render("■") + " ")
	}
	return b.String()
}

func init() {
	themeCmd.AddCommand(themeListCmd, themePreviewCmd, themeSetCmd)
	rootCmd.AddCommand(themeCmd)
	registerThemeCompletions()
}
