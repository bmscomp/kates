package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bmscomp/kates/cli/output"
)

func TestContextServes(t *testing.T) {
	for _, tt := range []struct {
		target, context string
		want            bool
	}{
		{"https://kates.example.com", "https://kates.example.com", true},
		{"https://KATES.example.com:8443/api", "https://kates.example.com", true},
		{"http://127.0.0.1:8080", "http://localhost:30083", true},
		{"http://[::1]:8080", "http://localhost:8080", true},
		{"https://localhost:8080", "http://localhost:8080", true},
		{"https://other.example.net", "https://kates.example.com", false},
		{"http://kates.example.com", "https://kates.example.com", false},
		{"http://my-localhost:8080", "http://localhost:8080", false},
		{"http://kates.example.com.evil.net", "http://kates.example.com", false},
		{"not a url", "http://localhost:8080", false},
		{"http://localhost:8080", "", false},
	} {
		if got := contextServes(tt.target, tt.context); got != tt.want {
			t.Errorf("contextServes(%q, %q) = %v, want %v", tt.target, tt.context, got, tt.want)
		}
	}
}

func TestResolveFallbackURL_MatchesTheHostOnly(t *testing.T) {
	only30083 := func(addr string) bool { return addr == "127.0.0.1:30083" }
	for in, want := range map[string]string{
		"http://localhost:8080/api":                      "http://localhost:30083/api",
		"http://[::1]:8080":                              "http://[::1]:30083",
		"http://my-localhost:8080":                       "http://my-localhost:8080",
		"https://kates.example.com/?next=localhost:8080": "https://kates.example.com/?next=localhost:8080",
		"http://localhost:18080":                         "http://localhost:18080",
	} {
		if got := resolveFallbackURL(in, only30083); got != want {
			t.Errorf("resolveFallbackURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// keyServer records the Authorization header of each request it gets.
type keyServer struct {
	*httptest.Server
	mu    sync.Mutex
	auths []string
}

func newKeyServer(t *testing.T) *keyServer {
	t.Helper()
	ks := &keyServer{}
	ks.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ks.mu.Lock()
		ks.auths = append(ks.auths, r.Header.Get("Authorization"))
		ks.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[],"page":0,"size":20,"total":0,"totalPages":0,"count":0}`))
	}))
	t.Cleanup(ks.Close)
	return ks
}

func (ks *keyServer) received() []string {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	return append([]string(nil), ks.auths...)
}

// runWithPreRun runs `kates test list` as the binary would, root pre-run
// included, with HOME holding a config whose current context, lab, points at
// labURL with key lab-key. flagURL is --url ("" for none); env is set for the
// run. It returns what the pre-run wrote to stderr.
func runWithPreRun(t *testing.T, labURL, flagURL string, env map[string]string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	config := "current-context: lab\ncontexts:\n  lab:\n    url: " + labURL + "\n    api-key: lab-key\n    insecure: true\n"
	if err := os.WriteFile(filepath.Join(home, ".kates.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"KATES_URL", "KATES_API_KEY", "KATES_CONTEXT", "KATES_OUTPUT", "KATES_PLAIN"} {
		t.Setenv(k, env[k])
	}

	prevURL, prevMode, prevCtx, prevKey, prevPlain, prevClient := apiURL, outputMode, contextFlag, apiKeyFlag, plainOutput, apiClient
	prevErr := output.Err
	t.Cleanup(func() {
		apiURL, outputMode, contextFlag, apiKeyFlag, plainOutput, apiClient = prevURL, prevMode, prevCtx, prevKey, prevPlain, prevClient
		output.Err = prevErr
		output.SetPlain(prevPlain)
	})
	apiURL, outputMode, contextFlag, apiKeyFlag, plainOutput = flagURL, "", "", "", false
	var stderr bytes.Buffer
	output.Err = &stderr

	rootCmd.PersistentPreRun(rootCmd, nil)
	commandStdout(t, func() error { return runCommandLine(t, "test", "list") })
	return stderr.String()
}

// --url and KATES_URL used to take the context's API key with them to
// whatever host they named.
func TestOverriddenURLKeepsTheContextKeyAtHome(t *testing.T) {
	lab := newKeyServer(t)
	other := newKeyServer(t)
	// Every test server listens on 127.0.0.1, so the context names a host
	// of its own for the cases that need another host.
	labURL := "https://kates-lab.example.com"

	stderr := runWithPreRun(t, labURL, other.URL, nil)
	if got := other.received(); len(got) != 1 || got[0] != "" {
		t.Errorf("--url to another host: Authorization %q, want one request without a key", got)
	}
	if !strings.Contains(stderr, "Not sending the context's API key") {
		t.Errorf("no note that the key stayed behind:\n%s", stderr)
	}
	if apiClient.HTTPClient.Transport.(*http.Transport).TLSClientConfig != nil {
		t.Error("the context's insecure setting went to another host")
	}

	other2 := newKeyServer(t)
	runWithPreRun(t, labURL, "", map[string]string{"KATES_URL": other2.URL})
	if got := other2.received(); len(got) != 1 || got[0] != "" {
		t.Errorf("KATES_URL to another host: Authorization %q, want one request without a key", got)
	}

	// An explicit key goes wherever the URL does.
	other3 := newKeyServer(t)
	stderr = runWithPreRun(t, labURL, other3.URL, map[string]string{"KATES_API_KEY": "staging-key"})
	if got := other3.received(); len(got) != 1 || got[0] != "Bearer staging-key" {
		t.Errorf("with KATES_API_KEY: Authorization %q, want Bearer staging-key", got)
	}
	if stderr != "" {
		t.Errorf("a note although the key was given:\n%s", stderr)
	}

	// The same host on another port is the context's server: kates-ci.sh
	// passes --url http://localhost:8080 for a context on :30083.
	stderr = runWithPreRun(t, strings.Replace(lab.URL, "127.0.0.1", "localhost", 1)+"/", lab.URL, nil)
	if got := lab.received(); len(got) != 1 || got[0] != "Bearer lab-key" {
		t.Errorf("--url to the context's host: Authorization %q, want Bearer lab-key", got)
	}
	if stderr != "" {
		t.Errorf("a note for the context's own host:\n%s", stderr)
	}
}
