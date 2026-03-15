package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type ThemeConfig struct {
	Primary   string `yaml:"primary"`
	Secondary string `yaml:"secondary"`
	Success   string `yaml:"success"`
	Warning   string `yaml:"warning"`
	Error     string `yaml:"error"`
	Muted     string `yaml:"muted"`
	Text      string `yaml:"text"`
}

var builtinThemes = map[string]ThemeConfig{
	"default": {
		Primary: "#7C3AED", Secondary: "#06B6D4", Success: "#22C55E",
		Warning: "#F59E0B", Error: "#EF4444", Muted: "#6B7280", Text: "#E5E7EB",
	},
	"dracula": {
		Primary: "#BD93F9", Secondary: "#8BE9FD", Success: "#50FA7B",
		Warning: "#F1FA8C", Error: "#FF5555", Muted: "#6272A4", Text: "#F8F8F2",
	},
	"gruvbox": {
		Primary: "#D3869B", Secondary: "#83A598", Success: "#B8BB26",
		Warning: "#FABD2F", Error: "#FB4934", Muted: "#928374", Text: "#EBDBB2",
	},
	"monokai": {
		Primary: "#AE81FF", Secondary: "#66D9EF", Success: "#A6E22E",
		Warning: "#E6DB74", Error: "#F92672", Muted: "#75715E", Text: "#F8F8F2",
	},
	"solarized": {
		Primary: "#6C71C4", Secondary: "#2AA198", Success: "#859900",
		Warning: "#B58900", Error: "#DC322F", Muted: "#586E75", Text: "#93A1A1",
	},
}

var themeCmd = &cobra.Command{
	Use:   "theme",
	Short: "Manage CLI color themes",
}

var themeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available themes",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := loadConfig()
		currentTheme := ""
		cfgData, err := os.ReadFile(configPath())
		if err == nil {
			var raw map[string]interface{}
			if yaml.Unmarshal(cfgData, &raw) == nil {
				if t, ok := raw["theme"].(string); ok {
					currentTheme = t
				}
			}
		}
		_ = cfg

		fmt.Println()
		output.Header("Available Themes")
		fmt.Println()

		for name, t := range builtinThemes {
			active := ""
			if name == currentTheme {
				active = " (active)"
			}

			swatch := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Primary)).Render("■") + " " +
				lipgloss.NewStyle().Foreground(lipgloss.Color(t.Secondary)).Render("■") + " " +
				lipgloss.NewStyle().Foreground(lipgloss.Color(t.Success)).Render("■") + " " +
				lipgloss.NewStyle().Foreground(lipgloss.Color(t.Warning)).Render("■") + " " +
				lipgloss.NewStyle().Foreground(lipgloss.Color(t.Error)).Render("■")

			label := output.PadRight(
				lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(t.Primary)).Render(name), 14)
			fmt.Printf("  %s %s%s\n", label, swatch,
				lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280")).Render(active))
		}
		fmt.Println()
		output.Hint("Set theme: add 'theme: <name>' to ~/.kates.yaml")
		return nil
	},
}

var themePreviewCmd = &cobra.Command{
	Use:   "preview <name>",
	Short: "Preview a theme's color palette",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := strings.ToLower(args[0])
		t, ok := builtinThemes[name]
		if !ok {
			return cmdErr("unknown theme: " + name + " (use: default, dracula, gruvbox, monokai, solarized)")
		}

		fmt.Println()
		fmt.Println("  " + lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(t.Primary)).Render("Theme: "+name))
		fmt.Println()

		colors := []struct{ name, hex string }{
			{"Primary", t.Primary},
			{"Secondary", t.Secondary},
			{"Success", t.Success},
			{"Warning", t.Warning},
			{"Error", t.Error},
			{"Muted", t.Muted},
			{"Text", t.Text},
		}

		for _, c := range colors {
			block := lipgloss.NewStyle().Background(lipgloss.Color(c.hex)).Render("      ")
			label := lipgloss.NewStyle().Foreground(lipgloss.Color(c.hex)).Render(c.name)
			fmt.Printf("  %s  %-14s %s\n", block, label,
				lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280")).Render(c.hex))
		}

		fmt.Println()
		fmt.Println("  " + lipgloss.NewStyle().Foreground(lipgloss.Color(t.Success)).Render("✓ PASS") +
			"  " + lipgloss.NewStyle().Foreground(lipgloss.Color(t.Error)).Render("✖ FAIL") +
			"  " + lipgloss.NewStyle().Foreground(lipgloss.Color(t.Warning)).Render("⚠ WARN") +
			"  " + lipgloss.NewStyle().Foreground(lipgloss.Color(t.Muted)).Render("dim text"))
		fmt.Println()

		return nil
	},
}

func init() {
	themeCmd.AddCommand(themeListCmd, themePreviewCmd)
	rootCmd.AddCommand(themeCmd)
	registerThemeCompletions()
}
