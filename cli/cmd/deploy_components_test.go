package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// TestSyncActiveContextKey checks the key kates deploy stores after a deploy.
// It follows the rule kates ports follows: the Secret's key goes only into a
// context without a key, or in place of a key kates stored there. It used to
// overwrite any key, an MCP context's agent-scoped one included.
func TestSyncActiveContextKey(t *testing.T) {
	const (
		adminKey = "admin-key-from-secret"
		agentKey = "agent-scoped-key"
	)
	// Key-sources are salted: a context whose key-source must stay as it was
	// shares this one string with its want.
	oldSource := secretKeySource("old")
	tests := []struct {
		name        string
		contexts    map[string]Context
		current     string
		flag        string // --context or KATES_CONTEXT
		secretErr   error
		wantOutcome deployKeyOutcome
		wantTarget  string   // the context the outcome is about
		wantCtx     *Context // that context afterwards, when it exists
		freshSource bool     // wantCtx's key-source is one kates just wrote for its key
		wantReport  string
	}{
		{
			name:        "stores the key in a context without one",
			contexts:    map[string]Context{"local": {URL: "http://localhost:8080", Output: "table"}},
			current:     "local",
			wantOutcome: deployKeyStored, wantTarget: "local",
			wantCtx: &Context{URL: "http://localhost:8080", Output: "table", APIKey: adminKey}, freshSource: true,
			wantReport: `API key from Secret kates-api-key (namespace kates) stored in context "local"`,
		},
		{
			name:        "replaces a key it stored before",
			contexts:    map[string]Context{"local": {URL: "u", APIKey: "old", KeySource: oldSource}},
			current:     "local",
			wantOutcome: deployKeyStored, wantTarget: "local",
			wantCtx: &Context{URL: "u", APIKey: adminKey}, freshSource: true,
			wantReport: `stored in context "local"`,
		},
		{
			name: "keeps an agent-scoped key in the current MCP context",
			contexts: map[string]Context{
				"mcp": {URL: "http://localhost:8080", Output: "json", APIKey: agentKey},
			},
			current:     "mcp",
			wantOutcome: deployKeyKept, wantTarget: "mcp",
			wantCtx:    &Context{URL: "http://localhost:8080", Output: "json", APIKey: agentKey},
			wantReport: `Context "mcp" keeps the API key it holds`,
		},
		{
			name:        "keeps a key typed over one it stored",
			contexts:    map[string]Context{"local": {URL: "u", APIKey: agentKey, KeySource: oldSource}},
			current:     "local",
			wantOutcome: deployKeyKept, wantTarget: "local",
			wantCtx:    &Context{URL: "u", APIKey: agentKey, KeySource: oldSource},
			wantReport: `Context "local" keeps the API key it holds`,
		},
		{
			name:        "leaves a context that already holds the Secret's key as it is",
			contexts:    map[string]Context{"local": {URL: "u", APIKey: adminKey}},
			current:     "local",
			wantOutcome: deployKeyPresent, wantTarget: "local",
			wantCtx:    &Context{URL: "u", APIKey: adminKey},
			wantReport: `Context "local" already holds the API key`,
		},
		{
			name: "writes the context --context names, not the current one",
			contexts: map[string]Context{
				"mcp": {URL: "u", APIKey: agentKey},
				"lab": {URL: "http://lab"},
			},
			current: "mcp", flag: "lab",
			wantOutcome: deployKeyStored, wantTarget: "lab",
			wantCtx: &Context{URL: "http://lab", APIKey: adminKey}, freshSource: true,
			wantReport: `stored in context "lab"`,
		},
		{
			name:     "a --context that does not exist stores nothing",
			contexts: map[string]Context{"local": {URL: "u"}},
			current:  "local", flag: "missing",
			wantOutcome: deployKeyNoContext, wantTarget: "missing",
			wantReport: `Context "missing" does not exist`,
		},
		{
			name:        "an unreadable Secret stores nothing",
			contexts:    map[string]Context{"local": {URL: "u"}},
			current:     "local",
			secretErr:   errors.New(`secrets "kates-api-key" is forbidden`),
			wantOutcome: deployKeySecretUnread,
			wantReport:  `No API key from Secret kates-api-key (namespace kates): secrets "kates-api-key" is forbidden`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			origExec, origFlag := runExecOutputFn, contextFlag
			t.Cleanup(func() { runExecOutputFn, contextFlag = origExec, origFlag })
			contextFlag = tt.flag
			runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
				if tt.secretErr != nil {
					return nil, tt.secretErr
				}
				return []byte(base64.StdEncoding.EncodeToString([]byte(adminKey))), nil
			}
			mustSaveConfig(t, Config{CurrentContext: tt.current, Contexts: tt.contexts})
			before := loadConfig()

			result := syncActiveContextKey(context.Background(), "kates")
			if result.SaveErr != nil {
				t.Fatalf("SaveErr = %v", result.SaveErr)
			}
			after := loadConfig()

			if result.Key != tt.wantOutcome {
				t.Errorf("outcome = %v, want %v", result.Key, tt.wantOutcome)
			}
			if result.Context != tt.wantTarget {
				t.Errorf("context = %q, want %q", result.Context, tt.wantTarget)
			}
			if after.CurrentContext != before.CurrentContext {
				t.Errorf("current context = %q, want it unchanged (%q)", after.CurrentContext, before.CurrentContext)
			}
			for name, want := range before.Contexts {
				if name == tt.wantTarget && tt.wantCtx != nil {
					want = *tt.wantCtx
					if tt.freshSource {
						got := after.Contexts[name]
						if !keySourceMatches(got.KeySource, got.APIKey) {
							t.Errorf("context %q key-source = %q, want one kates wrote for its key", name, got.KeySource)
						}
						want.KeySource = got.KeySource
					}
				}
				if got := after.Contexts[name]; !reflect.DeepEqual(got, want) {
					t.Errorf("context %q = %+v, want %+v", name, got, want)
				}
			}

			var buf bytes.Buffer
			printDeployKeySync(&buf, result, "kates")
			out := stripAnsi(buf.String())
			if !strings.Contains(out, tt.wantReport) {
				t.Errorf("report lacks %q:\n%s", tt.wantReport, out)
			}
			// The report used to print the key's first four characters.
			if strings.Contains(out, adminKey[:4]) || strings.Contains(out, agentKey[:4]) {
				t.Errorf("report shows part of a key:\n%s", out)
			}
		})
	}
}
