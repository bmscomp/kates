package cmd

import (
	"fmt"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

// The kates://docs/cli resources: the help of every kates command, generated
// from the cobra tree of this binary when the server starts (plan §4.2), so
// it lists the commands and flags the CLI beside the server has. Not
// cli/cmd/data/doc_entries.yaml, which kates docs reads: it names commands
// that do not exist and misses most that do. Like kates://caveats, the text is
// the CLI's own and reads nothing from the backend. Plugin commands are left
// out: they run kates-<name> executables found on the PATH, whose names are
// not Kates text and whose help is their own.

const (
	mcpCLIDocsURI         = "kates://docs/cli"
	mcpCLIDocsURITemplate = "kates://docs/cli/{+command}"
)

// mcpCLIDocsOnce builds the pages once per process. The cobra tree does not
// change once the CLI runs, and building them writes to it: LocalFlags and
// InheritedFlags merge each command's flag sets on first use, so servers built
// at the same time, as parallel tests build them, must not each build them.
var mcpCLIDocsOnce = sync.OnceValues(func() (map[string]string, string) { return mcpCLIDocs(rootCmd) })

func registerMCPDocsResources(s *mcp.Server, deps *mcpDeps) {
	pages, index := mcpCLIDocsOnce()
	addStaticResource(s, deps, &mcp.Resource{
		URI:   mcpCLIDocsURI,
		Name:  "cli-docs",
		Title: "kates commands",
		Description: "Every kates command with what it does, and the global flags. The help of one command is at " +
			"kates://docs/cli/<path>, its words after kates joined by /: kates://docs/cli/test/create.",
		MIMEType: "text/markdown",
	}, index)
	addStaticResourceTemplate(s, deps, &mcp.ResourceTemplate{
		URITemplate: mcpCLIDocsURITemplate,
		Name:        "cli-command-docs",
		Title:       "kates command help",
		Description: "The help of one kates command, as kates <command> --help shows it: what it does, usage, " +
			"examples, flags and subcommands. command is its words after kates joined by /, such as test/create; " +
			"kates://docs/cli lists them all.",
		MIMEType: "text/markdown",
	}, pages, "No kates command has this path. kates://docs/cli lists every command; the path is the command's "+
		"words after kates joined by /, such as test/create.")
}

// mcpCLIDocs renders the help of every command under root that its help
// lists (cobra's IsAvailableCommand: not hidden, deprecated or the help
// command), plugins aside, keyed by mcpCLIDocPath, and the index of them all.
func mcpCLIDocs(root *cobra.Command) (pages map[string]string, index string) {
	pages = map[string]string{}
	var rows []string
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if !mcpCLIDocumented(sub) {
				continue
			}
			pages[mcpCLIDocPath(root, sub)] = mcpSanitize(mcpCLIDocPage(root, sub), 0)
			rows = append(rows, fmt.Sprintf("- `%s`: %s", sub.CommandPath(), sub.Short))
			walk(sub)
		}
	}
	walk(root)

	var b strings.Builder
	fmt.Fprintf(&b, "# %s commands\n\n", root.Name())
	fmt.Fprintf(&b, "Every command of this %s binary (%s), with what it does. The help of one command is at "+
		"%s/<path>, where <path> is its words after %s joined by /: %s/test/create for %s test create.\n\n",
		root.Name(), Version, mcpCLIDocsURI, root.Name(), mcpCLIDocsURI, root.Name())
	b.WriteString("## Global flags\n\nEvery command takes these.\n\n")
	mcpCLIDocCode(&b, root.PersistentFlags().FlagUsages())
	b.WriteString("## Commands\n\n")
	b.WriteString(strings.Join(rows, "\n"))
	b.WriteString("\n")
	return pages, mcpSanitize(b.String(), 0)
}

func mcpCLIDocumented(c *cobra.Command) bool {
	return c.IsAvailableCommand() && c.Annotations[pluginAnnotation] == ""
}

// mcpCLIDocPath is c's words after the root's name, joined by /.
func mcpCLIDocPath(root, c *cobra.Command) string {
	words := strings.Fields(c.CommandPath())
	return strings.Join(words[len(strings.Fields(root.CommandPath())):], "/")
}

// mcpCLIDocPage is c's help as Markdown, from the fields kates <command>
// --help prints.
func mcpCLIDocPage(root, c *cobra.Command) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", c.CommandPath())
	if c.Short != "" {
		fmt.Fprintf(&b, "%s\n\n", c.Short)
	}
	if long := strings.TrimSpace(c.Long); long != "" && long != strings.TrimSpace(c.Short) {
		fmt.Fprintf(&b, "%s\n\n", long)
	}
	b.WriteString("## Usage\n\n")
	mcpCLIDocCode(&b, c.UseLine())
	if len(c.Aliases) > 0 {
		fmt.Fprintf(&b, "Aliases: %s\n\n", strings.Join(c.Aliases, ", "))
	}
	if ex := strings.TrimRight(c.Example, " \n"); ex != "" {
		b.WriteString("## Examples\n\n")
		mcpCLIDocCode(&b, ex)
	}
	if flags := c.LocalFlags().FlagUsages(); flags != "" {
		b.WriteString("## Flags\n\n")
		mcpCLIDocCode(&b, flags)
	}
	if flags := c.InheritedFlags().FlagUsages(); flags != "" {
		b.WriteString("## Global flags\n\n")
		mcpCLIDocCode(&b, flags)
	}
	var subs []string
	for _, sub := range c.Commands() {
		if mcpCLIDocumented(sub) {
			subs = append(subs, fmt.Sprintf("- `%s`: %s (%s/%s)", sub.CommandPath(), sub.Short, mcpCLIDocsURI, mcpCLIDocPath(root, sub)))
		}
	}
	if len(subs) > 0 {
		b.WriteString("## Subcommands\n\n")
		b.WriteString(strings.Join(subs, "\n"))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// mcpCLIDocCode writes text as a fenced code block.
func mcpCLIDocCode(b *strings.Builder, text string) {
	fmt.Fprintf(b, "```text\n%s\n```\n\n", strings.TrimRight(text, "\n"))
}
