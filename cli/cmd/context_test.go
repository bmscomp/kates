package cmd

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestResolveContext(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := Config{CurrentContext: "lab", Contexts: map[string]Context{
		"lab":  {URL: "http://localhost:30083"},
		"prod": {URL: "https://kates.example.com", APIKey: "prod-key"},
	}}
	for _, tt := range []struct {
		name, flag, current string
		fromEnv             bool
		wantName, wantURL   string
		wantErr             []string
	}{
		{name: "current context", wantName: "lab", wantURL: "http://localhost:30083"},
		{name: "--context", flag: "prod", wantName: "prod", wantURL: "https://kates.example.com"},
		{name: "unknown --context", flag: "prdo",
			wantErr: []string{`context "prdo", from --context`, "it has lab, prod", "kates ctx set prdo --url"}},
		{name: "unknown KATES_CONTEXT", flag: "prdo", fromEnv: true,
			wantErr: []string{`context "prdo", from KATES_CONTEXT`}},
		{name: "unknown current context", current: "gone",
			wantErr: []string{`context "gone", from the current context`}},
		{name: "no name is the built-in default", current: "-", wantName: "default", wantURL: "http://localhost:8080"},
		{name: "default is the built-in default", flag: "default", wantName: "default", wantURL: "http://localhost:8080"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			prevFlag, prevEnv := contextFlag, contextFromEnv
			t.Cleanup(func() { contextFlag, contextFromEnv = prevFlag, prevEnv })
			contextFlag, contextFromEnv = tt.flag, tt.fromEnv
			c := cfg
			switch tt.current {
			case "":
			case "-":
				c.CurrentContext = ""
			default:
				c.CurrentContext = tt.current
			}

			name, ctx, err := resolveContext(c)
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("= %q, %+v; want an error", name, ctx)
				}
				for _, want := range tt.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q lacks %q", err, want)
					}
				}
				return
			}
			if err != nil || name != tt.wantName || ctx.URL != tt.wantURL {
				t.Errorf("= %q, %q, %v; want %q, %q", name, ctx.URL, err, tt.wantName, tt.wantURL)
			}
		})
	}
}

// runKatesCLI runs the test binary as kates, root pre-run and all, with HOME
// holding a config whose only context, lab, points at a fake API. It returns
// the output, the exit code, and how many requests reached the API.
func runKatesCLI(t *testing.T, env []string, args ...string) (out string, code int, requests int64) {
	t.Helper()
	var hits atomic.Int64
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[],"page":0,"size":100,"total":0,"totalPages":0,"count":0}`))
	}))
	t.Cleanup(api.Close)

	home := t.TempDir()
	config := "current-context: lab\ncontexts:\n  lab:\n    url: " + api.URL + "\n"
	if err := os.WriteFile(filepath.Join(home, ".kates.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperRunKates$")
	cmd.Env = append(os.Environ(),
		"KATES_TEST_RUN_AS_CLI=1",
		"KATES_TEST_ARGS="+strings.Join(args, " "),
		"KATES_TEST_HOME="+home,
		"KATES_URL=", "KATES_API_KEY=", "KATES_CONTEXT=",
	)
	cmd.Env = append(cmd.Env, env...)
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		code = exit.ExitCode()
	case err != nil:
		t.Fatal(err)
	}
	return string(output), code, hits.Load()
}

// A mistyped --context used to send the command to http://localhost:8080,
// whatever answered there, without the context's API key.
func TestUnknownContextReachesNoServer(t *testing.T) {
	out, code, requests := runKatesCLI(t, nil, "--context", "labb", "test", "list")
	if code != 1 || requests != 0 {
		t.Errorf("exit %d after %d requests, want 1 after none:\n%s", code, requests, out)
	}
	// "lab). Create it" ends the list of the contexts there are.
	if !strings.Contains(out, `context "labb", from --context`) || !strings.Contains(out, "lab). Create it") {
		t.Errorf("the error does not name the context and the ones there are:\n%s", out)
	}

	out, code, _ = runKatesCLI(t, []string{"KATES_CONTEXT=labb"}, "test", "list")
	if code != 1 || !strings.Contains(out, "from KATES_CONTEXT") {
		t.Errorf("KATES_CONTEXT=labb: exit %d, want 1 with the variable named:\n%s", code, out)
	}

	if out, code, requests := runKatesCLI(t, nil, "--context", "lab", "test", "list"); code != 0 || requests == 0 {
		t.Errorf("--context lab: exit %d after %d requests, want 0 after some:\n%s", code, requests, out)
	}
}

// Commands that never call the API still run, so a context can be created
// while KATES_CONTEXT already names it.
func TestUnknownContextCanBeCreated(t *testing.T) {
	out, code, _ := runKatesCLI(t, []string{"KATES_CONTEXT=staging"}, "ctx", "set", "staging", "--url", "http://staging:8080")
	if code != 0 || !strings.Contains(out, "Context 'staging'") {
		t.Errorf("exit %d, want 0:\n%s", code, out)
	}
}

// kates status named the file's current context, not the one --context chose.
func TestStatusNamesTheContextUsed(t *testing.T) {
	out, code, _ := runKatesCLI(t, nil, "--context", "labb", "status")
	if code != 1 || !strings.Contains(out, `context "labb"`) {
		t.Errorf("status --context labb: exit %d, want 1 naming labb:\n%s", code, out)
	}
}
