package cmd

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

type DocFlag struct {
	Name    string
	Short   string
	Type    string
	Default string
	Desc    string
}

type DocEntry struct {
	Name        string
	Synopsis    string
	Short       string
	Description string
	Flags       []DocFlag
	Examples    []string
	SeeAlso     []string
	Category    string
}

var docsSearch string

var docsCmd = &cobra.Command{
	Use:   "docs [command...]",
	Short: "Man-style documentation for all KATES commands",
	Long: strings.TrimSpace(`
Show detailed man-page-style documentation for any KATES command.
Without arguments, lists all available documented commands grouped by category.`),
	RunE: func(cmd *cobra.Command, args []string) error {
		width := 80

		if docsSearch != "" {
			return searchDocs(docsSearch, width)
		}

		if len(args) == 0 {
			fmt.Println(renderDocsIndex(width))
			return nil
		}

		name := strings.Join(args, " ")
		for _, e := range docEntries {
			if strings.EqualFold(e.Name, name) {
				fmt.Println(renderManPage(e, width))
				return nil
			}
		}
		return cmdErr(fmt.Sprintf("No documentation found for %q. Run 'kates docs' to see all commands.", name))
	},
}

func searchDocs(query string, width int) error {
	q := strings.ToLower(query)
	var matches []DocEntry
	for _, e := range docEntries {
		if strings.Contains(strings.ToLower(e.Name), q) ||
			strings.Contains(strings.ToLower(e.Short), q) ||
			strings.Contains(strings.ToLower(e.Description), q) {
			matches = append(matches, e)
		}
	}
	if len(matches) == 0 {
		output.Hint(fmt.Sprintf("No results for %q", query))
		return nil
	}
	output.Header(fmt.Sprintf("Search results for %q (%d)", query, len(matches)))
	fmt.Println()
	for _, e := range matches {
		fmt.Printf("  %s  %s\n", manCmdStyle.Render(e.Name), manDimStyle.Render("— "+e.Short))
	}
	fmt.Println()
	output.Hint("Run 'kates docs <command>' for full documentation")
	return nil
}

var (
	manSectionStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7C3AED")).
			MarginTop(1)

	manCmdStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#06B6D4"))

	manFlagStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#22C55E"))

	manDefaultStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F59E0B"))

	manDimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6B7280"))

	manExampleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E5E7EB"))

	manCategoryStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#6366F1")).
				MarginTop(1)
)

func renderManPage(e DocEntry, width int) string {
	var b strings.Builder

	b.WriteString(manSectionStyle.Render("NAME") + "\n")
	b.WriteString(fmt.Sprintf("    kates %s — %s\n", manCmdStyle.Render(e.Name), e.Short))

	b.WriteString("\n" + manSectionStyle.Render("SYNOPSIS") + "\n")
	b.WriteString(fmt.Sprintf("    %s\n", manCmdStyle.Render(e.Synopsis)))

	if e.Description != "" {
		b.WriteString("\n" + manSectionStyle.Render("DESCRIPTION") + "\n")
		for _, line := range strings.Split(e.Description, "\n") {
			b.WriteString("    " + line + "\n")
		}
	}

	if len(e.Flags) > 0 {
		b.WriteString("\n" + manSectionStyle.Render("FLAGS") + "\n")
		for _, f := range e.Flags {
			nameStr := manFlagStyle.Render(f.Name)
			if f.Short != "" {
				nameStr = manFlagStyle.Render(f.Short + ", " + f.Name)
			}
			typeStr := ""
			if f.Type != "" && f.Type != "bool" {
				typeStr = " <" + f.Type + ">"
			}
			defStr := ""
			if f.Default != "" {
				defStr = manDefaultStyle.Render(fmt.Sprintf(" (default: %s)", f.Default))
			}
			b.WriteString(fmt.Sprintf("    %s%s%s\n", nameStr, typeStr, defStr))
			b.WriteString(fmt.Sprintf("        %s\n", f.Desc))
		}
	}

	if len(e.Examples) > 0 {
		b.WriteString("\n" + manSectionStyle.Render("EXAMPLES") + "\n")
		for _, ex := range e.Examples {
			b.WriteString("    " + manExampleStyle.Render("$ "+ex) + "\n")
		}
	}

	if len(e.SeeAlso) > 0 {
		b.WriteString("\n" + manSectionStyle.Render("SEE ALSO") + "\n")
		refs := make([]string, len(e.SeeAlso))
		for i, s := range e.SeeAlso {
			refs[i] = manCmdStyle.Render(s)
		}
		b.WriteString("    " + strings.Join(refs, ", ") + "\n")
	}

	return b.String()
}

func renderDocsIndex(width int) string {
	var b strings.Builder

	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7C3AED")).Render("KATES Command Reference")
	b.WriteString("\n  " + title + "\n")
	b.WriteString("  " + manDimStyle.Render("Run 'kates docs <command>' for full man-page documentation") + "\n")

	categories := []string{
		"Core", "Cluster", "Kafka", "Test", "Report",
		"Analysis", "Disruption", "Scheduling", "Resilience",
		"Config", "Observability", "Toolbox",
	}

	for _, cat := range categories {
		var entries []DocEntry
		for _, e := range docEntries {
			if e.Category == cat {
				entries = append(entries, e)
			}
		}
		if len(entries) == 0 {
			continue
		}

		b.WriteString("\n" + manCategoryStyle.Render("  "+cat) + "\n")
		for _, e := range entries {
			cmd := manCmdStyle.Width(30).Render(e.Name)
			b.WriteString(fmt.Sprintf("    %s %s\n", cmd, manDimStyle.Render(e.Short)))
		}
	}

	b.WriteString("\n")
	return b.String()
}

func init() {
	docsCmd.Flags().StringVar(&docsSearch, "search", "", "Search across all command documentation")
}
