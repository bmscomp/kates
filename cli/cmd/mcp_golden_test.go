package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpGoldenDir holds one file per tool, as tools/list shows it to a client.
// One file per tool, not one for the list, so that people adding tools in
// parallel never edit the same file.
var mcpGoldenDir = filepath.Join("testdata", "mcp", "tools")

type mcpGoldenTool struct {
	Name         string               `json:"name"`
	Title        string               `json:"title,omitempty"`
	Description  string               `json:"description"`
	Annotations  *mcp.ToolAnnotations `json:"annotations"`
	InputSchema  any                  `json:"inputSchema"`
	OutputSchema any                  `json:"outputSchema"`
	Meta         mcp.Meta             `json:"_meta,omitempty"`
}

// TestMCPGoldenToolsList pins what clients see: names, titles, descriptions,
// annotations and both schemas. A change to any of them fails here until the
// golden files are regenerated with
//
//	UPDATE_GOLDEN=1 go test ./cmd -run TestMCPGoldenToolsList
//
// and the diff is reviewed. A tool without a file, or a file without a tool,
// fails too; UPDATE_GOLDEN=1 writes the one and deletes the other.
func TestMCPGoldenToolsList(t *testing.T) {
	h := newMCPHarness(t, newMCPFakeBackend(t, "golden-cluster"))
	update := os.Getenv("UPDATE_GOLDEN") == "1"

	var names []string
	for tool, err := range h.session.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, tool.Name)
	}
	if !sort.StringsAreSorted(names) {
		t.Errorf("tools/list is not in a deterministic (sorted) order: %v", names)
	}
	if len(names) == 0 {
		t.Fatal("tools/list is empty")
	}

	if update {
		if err := os.MkdirAll(mcpGoldenDir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	listed := map[string]bool{}
	for _, name := range names {
		listed[name+".json"] = true
		tool := h.tools[name]
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(mcpGoldenTool{
			Name:         tool.Name,
			Title:        tool.Title,
			Description:  tool.Description,
			Annotations:  tool.Annotations,
			InputSchema:  tool.InputSchema,
			OutputSchema: tool.OutputSchema,
			Meta:         tool.Meta,
		}); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(mcpGoldenDir, name+".json")
		if update {
			if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("tool %s has no golden file %s; run UPDATE_GOLDEN=1 go test ./cmd -run TestMCPGoldenToolsList and review it", name, path)
			continue
		}
		if !bytes.Equal(buf.Bytes(), want) {
			t.Errorf("tool %s differs from %s; if the change is intended, run UPDATE_GOLDEN=1 go test ./cmd -run TestMCPGoldenToolsList and review the diff.\ngot:\n%s", name, path, buf.String())
		}
	}

	entries, err := os.ReadDir(mcpGoldenDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if listed[e.Name()] {
			continue
		}
		path := filepath.Join(mcpGoldenDir, e.Name())
		if update && strings.HasSuffix(e.Name(), ".json") {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			continue
		}
		t.Errorf("%s belongs to no tool in tools/list; delete it or run UPDATE_GOLDEN=1", path)
	}
}

// TestMCPEveryToolIsGuarded is the contract behind the golden files: every
// tool in tools/list went through addReadTool, and every resource and
// resource template through addStaticResource or addReadResourceTemplate, so
// every call that reads the backend passes the guard; and every tool says it
// only reads.
//
// The annotations set only readOnlyHint and openWorldHint (roAnnotations),
// but the golden files show "idempotentHint": false as well: go-sdk v1.8
// always writes that field, and only MCPGODEBUG=hintomitempty=1, read when
// the SDK package initialises, leaves it out. It means nothing next to
// readOnlyHint: true, so the test checks the value, not its absence.
func TestMCPEveryToolIsGuarded(t *testing.T) {
	h := newMCPHarness(t, newMCPFakeBackend(t, "cluster-a"))
	ctx := context.Background()
	listed := map[string]bool{}
	for r, err := range h.session.Resources(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		listed[r.URI] = true
	}
	for rt, err := range h.session.ResourceTemplates(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		listed[rt.URITemplate] = true
	}
	if len(listed) != len(h.deps.resources) {
		t.Errorf("the server lists %d resources and templates, %d were registered through the guard's helpers", len(listed), len(h.deps.resources))
	}
	for uri := range listed {
		if !h.deps.resources[uri] {
			t.Errorf("resource %s was not registered through addStaticResource or addReadResourceTemplate", uri)
		}
	}

	if len(h.tools) != len(h.deps.guarded) {
		t.Errorf("tools/list has %d tools, %d went through the guard", len(h.tools), len(h.deps.guarded))
	}
	for name, tool := range h.tools {
		if !h.deps.guarded[name] {
			t.Errorf("tool %s was not registered through addReadTool", name)
		}
		a := tool.Annotations
		if a == nil || !a.ReadOnlyHint || a.OpenWorldHint == nil || *a.OpenWorldHint || a.DestructiveHint != nil || a.IdempotentHint {
			t.Errorf("tool %s: annotations %+v, want readOnlyHint true, openWorldHint false, nothing else", name, a)
		}
		if tool.Title == "" || a == nil || a.Title != tool.Title || tool.Description == "" {
			t.Errorf("tool %s needs a title (also in its annotations) and a description", name)
		}
		out, _ := tool.OutputSchema.(map[string]any)
		props, _ := out["properties"].(map[string]any)
		for _, p := range []string{"cluster", "tier", "caveats", "truncated", "data"} {
			if _, ok := props[p]; !ok {
				t.Errorf("tool %s: outputSchema lacks the envelope property %q", name, p)
			}
		}
	}
}

// TestMCPUntrustedFieldsAreMarked: every fenced field in an output schema
// tells the model it is third-party text, even when the field has its own
// description.
func TestMCPUntrustedFieldsAreMarked(t *testing.T) {
	h := newMCPHarness(t, newMCPFakeBackend(t, "cluster-a"))
	raw, err := json.Marshal(h.tools["cluster_overview"].OutputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties struct {
			Data struct {
				Properties struct {
					AlertRules struct {
						Properties struct {
							Rules struct {
								Items struct {
									Properties map[string]struct {
										Title       string `json:"title"`
										Description string `json:"description"`
									} `json:"properties"`
								} `json:"items"`
							} `json:"rules"`
						} `json:"properties"`
					} `json:"alertRules"`
				} `json:"properties"`
			} `json:"data"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	props := schema.Properties.Data.Properties.AlertRules.Properties.Rules.Items.Properties
	for _, field := range []string{"group", "for", "expr", "summary", "description"} {
		p := props[field]
		if p.Title != mcpUntrustedTitle || !strings.HasPrefix(p.Description, mcpUntrustedNote) {
			t.Errorf("alert rule field %s is not marked untrusted: %+v", field, p)
		}
	}
	if !strings.Contains(props["expr"].Description, "PromQL") {
		t.Errorf("the field's own description must follow the note: %q", props["expr"].Description)
	}
	if strings.HasPrefix(props["name"].Description, mcpUntrustedNote) {
		t.Error("name is cleaned, not fenced, and must not be marked untrusted")
	}
}
