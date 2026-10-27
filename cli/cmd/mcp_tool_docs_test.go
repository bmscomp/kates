package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMCPToolsListSize holds tools/list, as the server sends it, under
// mcpToolsListMaxBytes: every client puts it in the model's context on every
// turn. It was 237 KB of indented golden files (132 KB on the wire) before
// the long descriptions moved to kates://docs/mcp/<tool>.
func TestMCPToolsListSize(t *testing.T) {
	h := newMCPHarness(t, newMCPFakeBackend(t, "cluster-a"))
	res, err := h.session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > mcpToolsListMaxBytes {
		t.Errorf("tools/list is %d bytes, over the %d cap: move prose to the tool's kates://docs/mcp page", len(raw), mcpToolsListMaxBytes)
	}
	t.Logf("tools/list: %d bytes for %d tools", len(raw), len(res.Tools))
	// The golden files are the same tools indented; they hold the wire form
	// to account too.
	var golden int
	entries, err := os.ReadDir(mcpGoldenDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(mcpGoldenDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var v any
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatal(err)
		}
		compact, _ := json.Marshal(v)
		golden += len(compact)
	}
	if golden > mcpToolsListMaxBytes {
		t.Errorf("the golden tools, compacted, add up to %d bytes, over %d", golden, mcpToolsListMaxBytes)
	}
}

// TestMCPToolDocs: every tool has a summary, points at its docs page, and
// that page holds the long description, every argument and every field of
// the result's data.
func TestMCPToolDocs(t *testing.T) {
	fb := newMCPFakeBackend(t, "cluster-a")
	h := newMCPHarness(t, fb)
	ctx := context.Background()
	read := func(uri string) string {
		t.Helper()
		res, err := h.session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		if err != nil {
			t.Fatalf("%s: %v", uri, err)
		}
		return res.Contents[0].Text
	}
	index := read(mcpToolDocsURI)
	if len(mcpToolSummaries) != len(h.tools) {
		t.Errorf("%d summaries for %d tools", len(mcpToolSummaries), len(h.tools))
	}
	for name, tool := range h.tools {
		summary, ok := mcpToolSummaries[name]
		if !ok {
			t.Errorf("tool %s has no summary", name)
			continue
		}
		if !strings.HasPrefix(tool.Description, summary) || !strings.HasSuffix(tool.Description, mcpToolDocsURI+"/"+name+".") {
			t.Errorf("tool %s: description %q is not its summary and docs pointer", name, tool.Description)
		}
		if !strings.Contains(index, "`"+name+"`") {
			t.Errorf("the index lacks %s", name)
		}
		doc := h.deps.toolDocs[name]
		page := read(mcpToolDocsURI + "/" + name)
		if !strings.Contains(page, mcpSanitize(doc.Description, 0)) {
			t.Errorf("%s: the page lacks the long description", name)
		}
		for arg := range doc.In.Properties {
			if !strings.Contains(page, "- `"+arg+"` (") {
				t.Errorf("%s: the page lacks argument %s", name, arg)
			}
		}
		for field := range doc.Out.Properties["data"].Properties {
			if !strings.Contains(page, "- `"+field+"` (") {
				t.Errorf("%s: the page lacks result field %s", name, field)
			}
		}
		for _, c := range doc.Caveats {
			if !strings.Contains(page, "`"+string(c)+"`") {
				t.Errorf("%s: the page lacks caveat %s", name, c)
			}
		}
	}
	if _, err := h.session.ReadResource(ctx, &mcp.ReadResourceParams{URI: mcpToolDocsURI + "/nope"}); err == nil {
		t.Error("a page for an unknown tool")
	}
	if got := fb.Requests(); len(got) != 0 {
		t.Errorf("reading the docs reached the backend: %v", mcpPaths(got))
	}
}

// TestMCPBookListsEveryTool holds the MCP tool table of the CLI reference to
// the tools this server registers.
func TestMCPBookListsEveryTool(t *testing.T) {
	h := newMCPHarness(t, newMCPFakeBackend(t, "cluster-a"))
	book, err := os.ReadFile(filepath.Join("..", "..", "docs", "book", "10-cli-reference.md"))
	if err != nil {
		t.Skipf("the book is not beside the CLI: %v", err)
	}
	_, after, ok := strings.Cut(string(book), "<!-- mcp-tools -->")
	table, _, ok2 := strings.Cut(after, "<!-- /mcp-tools -->")
	if !ok || !ok2 {
		t.Fatal("docs/book/10-cli-reference.md has no <!-- mcp-tools --> ... <!-- /mcp-tools --> table")
	}
	var inBook []string
	for _, m := range regexp.MustCompile("(?m)^\\| `([a-z_]+)` \\|").FindAllStringSubmatch(table, -1) {
		inBook = append(inBook, m[1])
	}
	var registered []string
	for name := range h.tools {
		registered = append(registered, name)
	}
	sort.Strings(registered)
	sort.Strings(inBook)
	if !slices.Equal(inBook, registered) {
		t.Errorf("the book's MCP tool table lists %v, the server registers %v", inBook, registered)
	}
}
