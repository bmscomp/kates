package cmd

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

// TestMCPCLIDocs reads the index and command pages through the server: they
// come from the cobra tree, read nothing from the backend, and a path that
// names no command is not found.
func TestMCPCLIDocs(t *testing.T) {
	fb := newMCPFakeBackend(t, "cluster-a")
	h := newMCPHarness(t, fb)
	fb.ResetLog()
	ctx := context.Background()
	read := func(uri string) string {
		t.Helper()
		res, err := h.session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		if err != nil {
			t.Fatalf("read %s: %v", uri, err)
		}
		if len(res.Contents) != 1 || res.Contents[0].URI != uri || res.Contents[0].MIMEType != "text/markdown" {
			t.Fatalf("read %s: contents %+v", uri, res.Contents)
		}
		return res.Contents[0].Text
	}
	contains := func(what, text string, wants ...string) {
		t.Helper()
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Errorf("%s lacks %q:\n%s", what, want, text)
			}
		}
	}

	contains("the index", read(mcpCLIDocsURI),
		"- `kates test create`: "+testCreateCmd.Short, "## Global flags", "--context string", "kates://docs/cli/test/create")
	contains("kates test create", read("kates://docs/cli/test/create"),
		"# kates test create\n", testCreateCmd.Short, "kates test create [flags]", "Aliases: run, start",
		"## Examples", "--records int", "## Global flags", "--context string")
	contains("kates test", read("kates://docs/cli/test"), "- `kates test create`: "+testCreateCmd.Short+" (kates://docs/cli/test/create)")
	// kates mcp documents itself. Its help mentions the fence markers, which
	// reach the model as ‹ and ›, not as markers.
	mcpPage := read("kates://docs/cli/mcp")
	contains("kates mcp", mcpPage, "--allow-cluster", "‹untrusted:…›")
	if strings.ContainsAny(mcpPage, "«»") {
		t.Errorf("kates mcp's page has fence markers:\n%s", mcpPage)
	}

	for _, uri := range []string{"kates://docs/cli/", "kates://docs/cli/no-such-command", "kates://docs/cli/test/../mcp",
		"kates://docs/cli/test%20create", "kates://docs/cli/test/create/"} {
		_, err := h.session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		if got := mcpResourceErr(t, err); got.body.Error.Code != mcpErrNotFound || !strings.Contains(got.body.Error.Message, mcpCLIDocsURI) {
			t.Errorf("%s: %+v", uri, got.body)
		}
	}
	if reqs := fb.Requests(); len(reqs) != 0 {
		t.Errorf("the docs read the backend: %v", mcpPaths(reqs))
	}
}

// Every command kates --help would list has a page and a line in the index,
// and no page carries a control character or a fence marker.
func TestMCPCLIDocsCoverEveryCommand(t *testing.T) {
	pages, index := mcpCLIDocsOnce()
	var want []string
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.IsAvailableCommand() && sub.Annotations[pluginAnnotation] == "" {
				want = append(want, mcpCLIDocPath(rootCmd, sub))
				if !strings.Contains(index, "- `"+sub.CommandPath()+"`: ") {
					t.Errorf("the index lacks %s", sub.CommandPath())
				}
				walk(sub)
			}
		}
	}
	walk(rootCmd)
	if len(want) < 100 || len(pages) != len(want) {
		t.Errorf("%d pages for %d commands", len(pages), len(want))
	}
	for _, path := range want {
		page, ok := pages[path]
		if !ok {
			t.Errorf("no page for %s", path)
			continue
		}
		if strings.ContainsAny(page, "\x1b\r«»") {
			t.Errorf("%s: the page has a control character or a fence marker", path)
		}
	}
	if strings.ContainsAny(index, "\x1b\r«»") {
		t.Error("the index has a control character or a fence marker")
	}
}

// Hidden, deprecated and plugin commands, and the help command, get no page,
// and their parent's page does not list them.
func TestMCPCLIDocsLeaveOut(t *testing.T) {
	root := &cobra.Command{Use: "kates"}
	group := &cobra.Command{Use: "group", Short: "A group"}
	root.AddCommand(group,
		&cobra.Command{Use: "plugged", Short: "Plugin: plugged", Run: func(*cobra.Command, []string) {},
			Annotations: map[string]string{pluginAnnotation: "/usr/local/bin/kates-plugged"}})
	group.AddCommand(
		&cobra.Command{Use: "shown", Short: "Shown", Run: func(*cobra.Command, []string) {}},
		&cobra.Command{Use: "hidden", Short: "Hidden", Hidden: true, Run: func(*cobra.Command, []string) {}},
		&cobra.Command{Use: "old", Short: "Old", Deprecated: "use shown", Run: func(*cobra.Command, []string) {}})
	root.InitDefaultHelpCmd()

	pages, index := mcpCLIDocs(root)
	var paths []string
	for p := range pages {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	if strings.Join(paths, ",") != "group,group/shown" {
		t.Errorf("pages = %v", paths)
	}
	for _, text := range []string{index, pages["group"]} {
		for _, gone := range []string{"hidden", "old", "plugged", "help"} {
			if strings.Contains(text, "`kates "+gone) || strings.Contains(text, "`kates group "+gone) {
				t.Errorf("%s is listed:\n%s", gone, text)
			}
		}
	}
}
