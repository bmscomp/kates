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
	watchType   string
	watchID     string
	watchFollow bool

	watchInfoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#06B6D4"))

	watchWarnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F59E0B")).
			Bold(true)

	watchErrStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#EF4444")).
			Bold(true)

	watchOkStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#22C55E"))

	watchTimeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6B7280"))
)

var watchEventsCmd = &cobra.Command{
	Use:   "watch",
	Short: "Live event stream of all KATES activity",
	Long: `Stream a live log of all KATES events: test starts/completions,
chaos experiments, SLA breaches, and configuration changes.

Like 'kubectl get events --watch' but for Kafka testing.`,
	Example: `  kates watch
  kates watch --type test
  kates watch --id abc123`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7C3AED")).Render("  KATES Event Stream"))
		fmt.Println(watchTimeStyle.Render("  Watching for events… (Ctrl+C to stop)"))
		fmt.Println()

		seen := map[string]string{}
		tick := 0

		for {
			paged, err := apiClient.ListTests(context.Background(), watchType, "", 0, 50)
			if err != nil {
				fmt.Printf("  %s %s\n", watchTimeStyle.Render(time.Now().Format("15:04:05")),
					watchErrStyle.Render("API error: "+err.Error()))
				time.Sleep(5 * time.Second)
				continue
			}

			if paged != nil {
				for _, t := range paged.Content {
					if watchID != "" && t.ID != watchID {
						continue
					}

					status := strings.ToUpper(t.Status)
					prevStatus, exists := seen[t.ID]

					if !exists || prevStatus != status {
						seen[t.ID] = status
						if !exists && tick == 0 {
							continue
						}
						printWatchEvent(t.ID, t.TestType, prevStatus, status)
					}
				}
			}

			tick++
			time.Sleep(3 * time.Second)
		}
	},
}

func printWatchEvent(id, testType, prevStatus, status string) {
	ts := time.Now().Format("15:04:05")
	short := id
	if len(short) > 8 {
		short = short[:8]
	}

	var badge, detail string
	switch status {
	case "PENDING":
		badge = watchInfoStyle.Render("▸ TEST    ")
		detail = fmt.Sprintf("%s %s test created", short, testType)
	case "RUNNING":
		badge = watchInfoStyle.Render("▸ TEST    ")
		if prevStatus == "PENDING" {
			detail = fmt.Sprintf("%s %s test started", short, testType)
		} else {
			detail = fmt.Sprintf("%s %s test running", short, testType)
		}
	case "DONE", "COMPLETED":
		badge = watchOkStyle.Render("▸ TEST    ")
		detail = fmt.Sprintf("%s %s test completed", short, testType)
	case "FAILED", "ERROR":
		badge = watchErrStyle.Render("▸ ALERT   ")
		detail = fmt.Sprintf("%s %s test FAILED", short, testType)
	default:
		badge = watchTimeStyle.Render("▸ TEST    ")
		detail = fmt.Sprintf("%s → %s", short, status)
	}

	fmt.Printf("  %s  %s %s\n", watchTimeStyle.Render(ts), badge, detail)
}

func init() {
	watchEventsCmd.Flags().StringVar(&watchType, "type", "", "Filter by test type (LOAD, STRESS, etc.)")
	watchEventsCmd.Flags().StringVar(&watchID, "id", "", "Watch a specific test ID")
	watchEventsCmd.Flags().BoolVarP(&watchFollow, "follow", "f", true, "Continuously follow events")
	rootCmd.AddCommand(watchEventsCmd)
}
