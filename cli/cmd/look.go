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

var lookCmd = &cobra.Command{
	Use:   "look",
	Short: "List, preview and choose the CLI look: frames, tables, key hints",
	Long: `A look is the shape kates draws with: frames, fills, title bars, the selected
row, key hints and tables. Colors still come from the theme (kates theme).

Kates starts with KATES_LOOK if set, else "look:" in ~/.kates.yaml, else
"classic", the look kates has always had. --look draws one command in another
look; --plain and ASCII-only terminals always draw classic. In kates kafka tui,
kates lab and the deploy dashboard, T switches to the next look on the spot.`,
}

var lookListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the looks",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println()
		output.Header("Looks")
		fmt.Println()
		active := theme.CurrentLook().Name
		for _, name := range theme.LookNames() {
			mark := "  "
			if name == active {
				mark = output.SuccessStyle.Render(output.Glyphs().Bullet + " ")
			}
			label := output.PadRight(lipgloss.NewStyle().Bold(true).Foreground(theme.Primary).Render(name), 12)
			fmt.Printf("  %s%s %s\n", mark, label, output.DimStyle.Render(theme.Looks[name].Description))
		}
		fmt.Println()
		if req := theme.RequestedLook(); req != "" && theme.Looks[req] == nil {
			output.Warn(fmt.Sprintf("look %q is not a built-in look; using %s", req, theme.SelectedLook()))
		}
		output.Hint("kates look preview <name> shows one; kates look set <name> uses it.")
		return nil
	},
}

var lookPreviewCmd = &cobra.Command{
	Use:   "preview <name>",
	Short: "Preview a look on sample output",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		prev := theme.CurrentLook().Name
		if err := theme.UseLook(args[0]); err != nil {
			return cmdErr(err.Error())
		}
		defer func() { _ = theme.UseLook(prev) }()

		l := theme.CurrentLook()
		output.Header("Look: " + l.Name)
		output.Hint(l.Description)
		fmt.Println()
		output.Table([]string{"#", "THROUGHPUT", "P99", "STATUS"}, [][]string{
			{"1", "45.2K rec/s", "12.00ms", "PASS"},
			{"2", "52.1K rec/s", "8.00ms", "PASS"},
			{"3", "48.7K rec/s", "11.00ms", "FAIL"},
		})
		fmt.Println(output.Panel("Best run", "#2  52.1K rec/s  p99 8.00ms", 40))
		fmt.Println()
		fmt.Println("  " + strings.Join([]string{output.StatusBadge("UP"), output.StatusBadge("RUNNING"),
			output.StatusBadge("DEGRADED"), output.StatusBadge("FAILED")}, "  "))
		fmt.Println()
		keys := []string{"↑↓ navigate", "Enter run", "T look", "q quit"}
		if l.Classic() {
			fmt.Println("  " + output.DimStyle.Render(strings.Join(keys, "  ·  ")))
		} else {
			fmt.Println("  " + theme.KeyHints(keys, 0)[0])
		}
		fmt.Println()
		return nil
	},
}

var lookSetCmd = &cobra.Command{
	Use:   "set <name>",
	Short: "Use a look from now on (saved in ~/.kates.yaml)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := strings.ToLower(strings.TrimSpace(args[0]))
		if theme.Looks[name] == nil {
			return cmdErr("unknown look: " + args[0] + " (use: " + strings.Join(theme.LookNames(), ", ") + ")")
		}
		if err := updateConfig(func(cfg *Config) error {
			stored := name
			if name == theme.Classic {
				stored = ""
			}
			if cfg.Look == stored {
				return errConfigUnchanged
			}
			cfg.Look = stored
			return nil
		}); err != nil {
			return err
		}
		output.Success("Look set to " + name + "; the next kates command uses it.")
		if env := strings.ToLower(strings.TrimSpace(os.Getenv("KATES_LOOK"))); env != "" && env != name {
			output.Hint("KATES_LOOK=" + env + " is set and overrides it in this shell.")
		}
		return nil
	},
}

func init() {
	lookCmd.AddCommand(lookListCmd, lookPreviewCmd, lookSetCmd)
	rootCmd.AddCommand(lookCmd)
}
