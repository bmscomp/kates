package cmd

import (
	"context"
	"strings"
	"testing"
)

// TestMCPHelpNamesEveryToolAndPrompt: kates mcp --help lists what the server
// serves, so a tool or prompt added without a line there fails here.
func TestMCPHelpNamesEveryToolAndPrompt(t *testing.T) {
	h := newMCPHarness(t, newMCPFakeBackend(t, "cluster-a"))
	ctx := context.Background()
	help := strings.Join(strings.Fields(mcpCmd.Long), " ")

	tools, err := h.session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		if !strings.Contains(help, tool.Name) {
			t.Errorf("kates mcp --help does not name the tool %s", tool.Name)
		}
	}
	prompts, err := h.session.ListPrompts(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range prompts.Prompts {
		if !strings.Contains(help, p.Name) {
			t.Errorf("kates mcp --help does not name the prompt %s", p.Name)
		}
	}
	if len(tools.Tools) == 0 || len(prompts.Prompts) == 0 {
		t.Fatalf("the server lists %d tools and %d prompts", len(tools.Tools), len(prompts.Prompts))
	}
}
