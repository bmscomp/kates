package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var (
	changelogSince string
	changelogUntil string

	clTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7C3AED"))

	clSecStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#06B6D4"))

	clDimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6B7280"))
)

var changelogCmd = &cobra.Command{
	Use:   "changelog",
	Short: "Generate changelog from audit events",
	Long: `Auto-generate release notes from audit events between two dates.
Groups events by category with statistics.`,
	Example: `  kates changelog --since 2024-01-01 --until 2024-01-31
  kates changelog --since 2024-06-01`,
	RunE: func(cmd *cobra.Command, args []string) error {
		entries, err := apiClient.Audit(context.Background(), 500, "", changelogSince)
		if err != nil {
			return err
		}

		var untilTime time.Time
		if changelogUntil != "" {
			untilTime, _ = time.Parse("2006-01-02", changelogUntil)
		}

		type stat struct {
			count  int
			types  map[string]int
			failed int
		}
		categories := map[string]*stat{}

		for _, e := range entries {
			if !untilTime.IsZero() {
				if t, err := time.Parse(time.RFC3339, e.Timestamp); err == nil {
					if t.After(untilTime.AddDate(0, 0, 1)) {
						continue
					}
				}
			}

			cat := e.EventType
			if cat == "" {
				cat = "other"
			}
			if categories[cat] == nil {
				categories[cat] = &stat{types: map[string]int{}}
			}
			s := categories[cat]
			s.count++
			action := e.Action
			if action == "" {
				action = "unknown"
			}
			s.types[action]++
		}

		period := changelogSince
		if changelogUntil != "" {
			period += " → " + changelogUntil
		} else {
			period += " → now"
		}

		fmt.Println()
		fmt.Println("  " + clTitleStyle.Render(fmt.Sprintf("Kates Changelog — %s", period)))
		fmt.Println()

		if len(categories) == 0 {
			fmt.Println("  " + clDimStyle.Render("No events in the specified period"))
			return nil
		}

		for cat, s := range categories {
			fmt.Printf("  %s (%d events)\n", clSecStyle.Render(strings.Title(cat)), s.count)
			for action, count := range s.types {
				badge := clDimStyle.Render("·")
				switch strings.ToUpper(action) {
				case "CREATE":
					badge = lipgloss.NewStyle().Foreground(lipgloss.Color("#22C55E")).Render("+")
				case "DELETE":
					badge = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444")).Render("-")
				case "UPDATE":
					badge = lipgloss.NewStyle().Foreground(lipgloss.Color("#06B6D4")).Render("~")
				}
				fmt.Printf("    %s %d × %s\n", badge, count, action)
			}
			fmt.Println()
		}

		totalEvents := 0
		for _, s := range categories {
			totalEvents += s.count
		}
		fmt.Printf("  %s %d total events across %d categories\n\n",
			clDimStyle.Render("Total:"), totalEvents, len(categories))

		return nil
	},
}

func init() {
	changelogCmd.Flags().StringVar(&changelogSince, "since", time.Now().AddDate(0, -1, 0).Format("2006-01-02"), "Start date (YYYY-MM-DD)")
	changelogCmd.Flags().StringVar(&changelogUntil, "until", "", "End date (YYYY-MM-DD, default: now)")
	rootCmd.AddCommand(changelogCmd)
}
