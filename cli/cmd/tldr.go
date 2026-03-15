package cmd

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

type tldrGroup struct {
	Category string
	Entries  []tldrEntry
}

type tldrEntry struct {
	Desc    string
	Command string
}

var tldrGroups = []tldrGroup{
	{
		Category: "Quick Start",
		Entries: []tldrEntry{
			{"Set up a context", "kates ctx set local --url http://localhost:30083"},
			{"Switch context", "kates ctx use local"},
			{"Check system health", "kates health"},
			{"Initialize a project", "kates init --name my-project"},
		},
	},
	{
		Category: "Test Lifecycle",
		Entries: []tldrEntry{
			{"Run a load test", "kates test create --type LOAD --records 100000"},
			{"Apply a scenario file", "kates test apply -f scenario.yaml --wait"},
			{"List all test runs", "kates test list"},
			{"View test details", "kates test get <id>"},
			{"Clean orphaned tests", "kates test cleanup --dry-run"},
		},
	},
	{
		Category: "Reports & Analysis",
		Entries: []tldrEntry{
			{"View report", "kates report show <id>"},
			{"Export as HTML", "kates report export <id> --format html"},
			{"Compare two runs", "kates diff <id1> <id2>"},
			{"Performance trends", "kates trend --type LOAD --days 14"},
			{"Get recommendations", "kates advisor <id>"},
		},
	},
	{
		Category: "CI/CD Integration",
		Entries: []tldrEntry{
			{"Quality gate", "kates gate --min-grade B --type LOAD"},
			{"Run benchmark", "kates benchmark --records 50000"},
			{"Assert no regression", "kates profile assert v3.2 <id> --max-regression 10"},
			{"Run a pipeline", "kates flow run -f release-qual.yaml"},
		},
	},
	{
		Category: "Kafka Operations",
		Entries: []tldrEntry{
			{"List topics", "kates kafka topics"},
			{"Describe a topic", "kates kafka topic my-topic"},
			{"Consume messages", "kates kafka consume my-topic -f"},
			{"Produce a message", "kates kafka produce my-topic --value '{\"key\":\"val\"}'"},
			{"Interactive TUI", "kates kafka tui"},
		},
	},
	{
		Category: "Observability",
		Entries: []tldrEntry{
			{"Full dashboard", "kates dashboard"},
			{"Live event stream", "kates watch"},
			{"Audit log", "kates audit --limit 20"},
			{"Cost estimate", "kates cost estimate --records 1M --cloud aws"},
		},
	},
}

var (
	tldrTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7C3AED"))

	tldrDescStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6B7280"))

	tldrCmdStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#06B6D4"))

	tldrCatStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#F59E0B")).
			MarginTop(1)
)

var tldrCmd = &cobra.Command{
	Use:   "tldr [category]",
	Short: "Quick command reference cheatsheet",
	Long: `Context-aware quick reference — the essential commands you need,
organized by workflow. Like 'tldr' but for Kates.`,
	Example: `  kates tldr
  kates tldr test
  kates tldr kafka`,
	RunE: func(cmd *cobra.Command, args []string) error {
		filter := ""
		if len(args) > 0 {
			filter = strings.ToLower(args[0])
		}

		fmt.Println()
		fmt.Println("  " + tldrTitleStyle.Render("Kates Quick Reference"))
		fmt.Println()

		for _, g := range tldrGroups {
			if filter != "" && !strings.Contains(strings.ToLower(g.Category), filter) {
				match := false
				for _, e := range g.Entries {
					if strings.Contains(strings.ToLower(e.Command), filter) ||
						strings.Contains(strings.ToLower(e.Desc), filter) {
						match = true
						break
					}
				}
				if !match {
					continue
				}
			}

			fmt.Println("  " + tldrCatStyle.Render(g.Category))
			for _, e := range g.Entries {
				if filter != "" &&
					!strings.Contains(strings.ToLower(e.Desc), filter) &&
					!strings.Contains(strings.ToLower(e.Command), filter) &&
					!strings.Contains(strings.ToLower(g.Category), filter) {
					continue
				}
				fmt.Printf("    %s\n", tldrDescStyle.Render(e.Desc+":"))
				fmt.Printf("      %s\n", tldrCmdStyle.Render("$ "+e.Command))
			}
			fmt.Println()
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(tldrCmd)
}
