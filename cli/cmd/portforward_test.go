package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// portsTestEnv isolates one syncPortsContext run: a temporary HOME for
// ~/.kates.yaml, a stubbed kubectl for Secret kates-api-key, and a backend
// that, like ApiKeyAuthFilter, serves /api/health to anyone and every other
// path only with validKey.
type portsTestEnv struct {
	endpoint string

	mu        sync.Mutex
	kubectl   [][]string // every kubectl invocation, arguments only
	probes    []string   // "path key" for every request the backend saw
	probeCode int        // when set, protected paths answer this instead

	// duringSecretRead, when set, runs while kubectl reads the Secret: what
	// another command does to the config while kates ports waits on it.
	duringSecretRead func()
}

func newPortsTestEnv(t *testing.T, secretKey string, secretErr error, validKey string) *portsTestEnv {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	env := &portsTestEnv{}

	origExec, origContext := runExecOutputFn, contextFlag
	t.Cleanup(func() { runExecOutputFn, contextFlag = origExec, origContext })
	contextFlag = ""
	runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		env.mu.Lock()
		env.kubectl = append(env.kubectl, append([]string{name}, args...))
		during := env.duringSecretRead
		env.mu.Unlock()
		if during != nil {
			during()
		}
		if secretErr != nil {
			return nil, secretErr
		}
		return []byte(base64.StdEncoding.EncodeToString([]byte(secretKey))), nil
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		env.mu.Lock()
		env.probes = append(env.probes, r.URL.Path+" "+key)
		code := env.probeCode
		env.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/health"):
			_, _ = w.Write([]byte(`{"status":"UP"}`))
		case code != 0:
			w.WriteHeader(code)
		case key == "":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"status":401,"error":"Missing API key","message":"Provide a token"}`))
		case key != validKey:
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"status":403,"error":"Invalid API key","message":"The provided API key is not valid"}`))
		default:
			_, _ = w.Write([]byte(`["LOAD","STRESS"]`))
		}
	}))
	t.Cleanup(ts.Close)
	env.endpoint = ts.URL
	return env
}

func (e *portsTestEnv) kubectlCalls() [][]string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([][]string(nil), e.kubectl...)
}

func (e *portsTestEnv) seenProbes() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.probes...)
}

func mustSaveConfig(t *testing.T, cfg Config) {
	t.Helper()
	if err := saveConfig(cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}
}

// assertOtherContextsUnchanged fails when any context but "ports" differs
// between before and after, or when one was added or removed.
func assertOtherContextsUnchanged(t *testing.T, before, after Config) {
	t.Helper()
	for name, want := range before.Contexts {
		if name == portsContextName {
			continue
		}
		got, ok := after.Contexts[name]
		if !ok {
			t.Errorf("context %q was removed", name)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("context %q changed:\n got  %+v\n want %+v", name, got, want)
		}
	}
	for name := range after.Contexts {
		if _, ok := before.Contexts[name]; !ok && name != portsContextName {
			t.Errorf("context %q was added", name)
		}
	}
}

func TestSyncPortsContext(t *testing.T) {
	const (
		adminKey = "admin-key-from-secret"
		agentKey = "agent-scoped-key"
	)
	// mcp stands for a context holding a narrower key for another tool; it is
	// current in most cases below, which is where kates ports used to write.
	mcp := Context{URL: "https://kates.example.com", Output: "json", APIKey: agentKey}

	// Each key-source is salted, so two calls for one key differ: a context
	// whose key-source must stay as it was shares one string with its want.
	staleSource := secretKeySource("stale")
	stillValidSource := secretKeySource("still-valid")
	adminSource := secretKeySource(adminKey)

	tests := []struct {
		name      string
		ports     *Context // the "ports" context before the run, if any
		secretKey string
		secretErr error
		validKey  string // the key the backend accepts
		probeCode int    // a status the backend answers protected paths with, if set

		wantKey          string
		wantKeySource    string // exactly this key-source, when wantFreshSource is false
		wantFreshSource  bool   // a key-source kates just wrote for wantKey
		wantOutcome      keyOutcome
		wantSecretCheck  keyVerdict
		wantKeptKey      bool
		wantKeptCheck    keyVerdict
		wantSecretLookup bool
	}{
		{
			name:      "creates the context and stores the Secret's key the API accepts",
			secretKey: adminKey, validKey: adminKey,
			wantKey: adminKey, wantFreshSource: true,
			wantOutcome: keyFromSecret, wantSecretCheck: keyAccepted, wantSecretLookup: true,
		},
		{
			name:      "replaces a key it stored on an earlier run",
			ports:     &Context{URL: "http://localhost:30083", Output: "table", APIKey: "stale", KeySource: staleSource},
			secretKey: adminKey, validKey: adminKey,
			wantKey: adminKey, wantFreshSource: true,
			wantOutcome: keyFromSecret, wantSecretCheck: keyAccepted, wantSecretLookup: true,
		},
		{
			name:      "stores the Secret's key when the context has an empty one",
			ports:     &Context{URL: "http://localhost:8080", Output: "table"},
			secretKey: adminKey, validKey: adminKey,
			wantKey: adminKey, wantFreshSource: true,
			wantOutcome: keyFromSecret, wantSecretCheck: keyAccepted, wantSecretLookup: true,
		},
		{
			name:      "keeps a key it did not store, and never reads the Secret",
			ports:     &Context{URL: "http://localhost:8080", Output: "table", APIKey: agentKey},
			secretKey: adminKey, validKey: agentKey,
			wantKey:     agentKey,
			wantOutcome: keyUserOwned, wantKeptKey: true, wantKeptCheck: keyAccepted,
		},
		{
			// The key was edited in the file under the key-source of the one
			// kates stored; the digest no longer matches, so it is the user's.
			name:      "keeps a key typed over one it stored",
			ports:     &Context{URL: "http://localhost:8080", Output: "table", APIKey: agentKey, KeySource: staleSource},
			secretKey: adminKey, validKey: agentKey,
			wantKey: agentKey, wantKeySource: staleSource,
			wantOutcome: keyUserOwned, wantKeptKey: true, wantKeptCheck: keyAccepted,
		},
		{
			// A key-source without the digest, as a hand-edited file may carry.
			name:      "keeps a key whose key-source names no digest",
			ports:     &Context{URL: "http://localhost:8080", Output: "table", APIKey: agentKey, KeySource: "kates-ports"},
			secretKey: adminKey, validKey: agentKey,
			wantKey: agentKey, wantKeySource: "kates-ports",
			wantOutcome: keyUserOwned, wantKeptKey: true, wantKeptCheck: keyAccepted,
		},
		{
			name:      "keeps a key it did not store even when the API rejects it",
			ports:     &Context{URL: "http://localhost:8080", Output: "table", APIKey: agentKey},
			secretKey: adminKey, validKey: adminKey,
			wantKey:     agentKey,
			wantOutcome: keyUserOwned, wantKeptKey: true, wantKeptCheck: keyRejected,
		},
		{
			name:      "does not store a Secret key the API rejects",
			ports:     &Context{URL: "http://localhost:8080", Output: "table", APIKey: "still-valid", KeySource: stillValidSource},
			secretKey: "rotated-key", validKey: "still-valid",
			wantKey: "still-valid", wantKeySource: stillValidSource,
			wantOutcome: keySecretRejected, wantSecretCheck: keyRejected,
			wantKeptKey: true, wantKeptCheck: keyAccepted, wantSecretLookup: true,
		},
		{
			name:      "rejected Secret key and no earlier key leaves the context without one",
			secretKey: "rotated-key", validKey: adminKey,
			wantOutcome: keySecretRejected, wantSecretCheck: keyRejected, wantSecretLookup: true,
		},
		{
			name:      "stores the Secret's key unverified when the API gives no verdict",
			secretKey: adminKey, validKey: adminKey, probeCode: http.StatusServiceUnavailable,
			wantKey: adminKey, wantFreshSource: true,
			wantOutcome: keyFromSecret, wantSecretCheck: keyUnverified, wantSecretLookup: true,
		},
		{
			name:      "an unreadable Secret leaves the context without a key",
			secretErr: errors.New(`secrets "kates-api-key" not found`), validKey: adminKey,
			wantOutcome: keySecretUnread, wantSecretLookup: true,
		},
		{
			name:      "an unreadable Secret keeps a key stored earlier",
			ports:     &Context{URL: "http://localhost:8080", Output: "table", APIKey: adminKey, KeySource: adminSource},
			secretErr: errors.New("forbidden"), validKey: adminKey,
			wantKey: adminKey, wantKeySource: adminSource,
			wantOutcome: keySecretUnread, wantKeptKey: true, wantKeptCheck: keyAccepted, wantSecretLookup: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newPortsTestEnv(t, tt.secretKey, tt.secretErr, tt.validKey)
			env.probeCode = tt.probeCode

			before := Config{CurrentContext: "mcp", Contexts: map[string]Context{
				"mcp":     mcp,
				"default": {URL: "http://localhost:8080", Output: "table"},
			}}
			if tt.ports != nil {
				before.Contexts[portsContextName] = *tt.ports
			}
			mustSaveConfig(t, before)
			before = loadConfig() // as the run will see it, built-in default included

			result := syncPortsContext(context.Background(), "kates", env.endpoint)
			if result.SaveErr != nil {
				t.Fatalf("SaveErr = %v", result.SaveErr)
			}
			after := loadConfig()

			assertOtherContextsUnchanged(t, before, after)
			if after.CurrentContext != portsContextName {
				t.Errorf("current context = %q, want %q", after.CurrentContext, portsContextName)
			}
			got, ok := after.Contexts[portsContextName]
			if !ok {
				t.Fatalf("context %q missing after the run", portsContextName)
			}
			if got.URL != env.endpoint {
				t.Errorf("URL = %q, want %q", got.URL, env.endpoint)
			}
			if got.APIKey != tt.wantKey {
				t.Errorf("APIKey = %q, want %q", got.APIKey, tt.wantKey)
			}
			switch {
			case tt.wantFreshSource:
				if !keySourceMatches(got.KeySource, got.APIKey) {
					t.Errorf("KeySource = %q, want one kates wrote for the stored key", got.KeySource)
				}
			case got.KeySource != tt.wantKeySource:
				t.Errorf("KeySource = %q, want %q", got.KeySource, tt.wantKeySource)
			}

			if result.Created != (tt.ports == nil) {
				t.Errorf("Created = %v, want %v", result.Created, tt.ports == nil)
			}
			if result.Previous != "mcp" {
				t.Errorf("Previous = %q, want %q", result.Previous, "mcp")
			}
			if result.Key != tt.wantOutcome {
				t.Errorf("Key outcome = %v, want %v", result.Key, tt.wantOutcome)
			}
			if tt.wantOutcome == keyFromSecret || tt.wantOutcome == keySecretRejected {
				if result.SecretCheck.Verdict != tt.wantSecretCheck {
					t.Errorf("SecretCheck = %+v, want verdict %v", result.SecretCheck, tt.wantSecretCheck)
				}
			}
			if result.KeptKey != tt.wantKeptKey {
				t.Errorf("KeptKey = %v, want %v", result.KeptKey, tt.wantKeptKey)
			}
			if tt.wantKeptKey && result.KeptCheck.Verdict != tt.wantKeptCheck {
				t.Errorf("KeptCheck = %+v, want verdict %v", result.KeptCheck, tt.wantKeptCheck)
			}
			if looked := len(env.kubectlCalls()) > 0; looked != tt.wantSecretLookup {
				t.Errorf("read the Secret = %v, want %v", looked, tt.wantSecretLookup)
			}
		})
	}
}

// TestSyncPortsContext_LeavesMCPContextKey is the Phase 0a exit check: with an
// MCP context current, pointing at the very address the forward serves, a
// kates ports run (and a second one) leaves that context's key and URL as
// they were, and the shared key lands only in "ports".
func TestSyncPortsContext_LeavesMCPContextKey(t *testing.T) {
	const adminKey = "admin-key-from-secret"
	env := newPortsTestEnv(t, adminKey, nil, adminKey)
	mcp := Context{URL: env.endpoint, Output: "json", APIKey: "agent-scoped-key"}
	mustSaveConfig(t, Config{CurrentContext: "mcp", Contexts: map[string]Context{"mcp": mcp}})

	for run := 1; run <= 2; run++ {
		syncPortsContext(context.Background(), "kates", env.endpoint)
		cfg := loadConfig()
		if got := cfg.Contexts["mcp"]; !reflect.DeepEqual(got, mcp) {
			t.Fatalf("run %d: mcp context = %+v, want it unchanged (%+v)", run, got, mcp)
		}
		if got := cfg.Contexts[portsContextName].APIKey; got != adminKey {
			t.Fatalf("run %d: ports key = %q, want the Secret's key", run, got)
		}
	}
}

// TestSyncPortsContext_KeepsChangesMadeDuringTheRun checks the write against
// a config another command changes while kates ports waits on kubectl. The
// run used to save the whole file it read before that wait, which put back
// the old value of whatever changed during it.
func TestSyncPortsContext_KeepsChangesMadeDuringTheRun(t *testing.T) {
	const adminKey = "admin-key-from-secret"
	setKey := func(t *testing.T, name, key string) func() {
		return func() {
			// What kates ctx set does in another terminal.
			err := updateConfig(func(cfg *Config) error {
				c := cfg.Contexts[name]
				c.URL, c.APIKey, c.KeySource = "https://kates.example.com", key, ""
				cfg.Contexts[name] = c
				return nil
			})
			if err != nil {
				t.Errorf("concurrent update: %v", err)
			}
		}
	}

	t.Run("another context", func(t *testing.T) {
		env := newPortsTestEnv(t, adminKey, nil, adminKey)
		mustSaveConfig(t, Config{CurrentContext: "mcp", Contexts: map[string]Context{
			"mcp": {URL: "https://kates.example.com", APIKey: "old-agent-key"},
		}})
		env.duringSecretRead = setKey(t, "mcp", "new-agent-key")

		result := syncPortsContext(context.Background(), "kates", env.endpoint)
		if result.SaveErr != nil {
			t.Fatal(result.SaveErr)
		}

		cfg := loadConfig()
		if got := cfg.Contexts["mcp"].APIKey; got != "new-agent-key" {
			t.Errorf("mcp key = %q, want the one set during the run", got)
		}
		if got := cfg.Contexts[portsContextName].APIKey; got != adminKey {
			t.Errorf("ports key = %q, want the Secret's", got)
		}
		if result.Previous != "mcp" {
			t.Errorf("Previous = %q, want mcp", result.Previous)
		}
	})

	t.Run("the ports context itself", func(t *testing.T) {
		env := newPortsTestEnv(t, adminKey, nil, adminKey)
		mustSaveConfig(t, Config{CurrentContext: "default", Contexts: map[string]Context{}})
		env.duringSecretRead = setKey(t, portsContextName, "key-set-meanwhile")

		result := syncPortsContext(context.Background(), "kates", env.endpoint)
		if result.SaveErr != nil {
			t.Fatal(result.SaveErr)
		}

		got := loadConfig().Contexts[portsContextName]
		if got.APIKey != "key-set-meanwhile" || got.KeySource != "" {
			t.Errorf("ports = %+v, want the key set during the run, with no key-source", got)
		}
		if got.URL != env.endpoint {
			t.Errorf("ports URL = %q, want %q", got.URL, env.endpoint)
		}
		if result.Key != keyChangedMeanwhile || result.KeptKey {
			t.Errorf("Key = %v, KeptKey = %v, want keyChangedMeanwhile and no kept-key verdict", result.Key, result.KeptKey)
		}
	})
}

// TestSyncPortsContext_IgnoresContextFlag checks that --context (or
// KATES_CONTEXT) no longer chooses the context kates ports writes, and that
// the result says the flag still outranks "ports" for later commands.
func TestSyncPortsContext_IgnoresContextFlag(t *testing.T) {
	const adminKey = "admin-key-from-secret"
	env := newPortsTestEnv(t, adminKey, nil, adminKey)
	lab := Context{URL: "https://lab.example.com", Output: "table", APIKey: "lab-key"}
	mustSaveConfig(t, Config{CurrentContext: "lab", Contexts: map[string]Context{"lab": lab}})
	before := loadConfig()
	contextFlag = "lab"

	result := syncPortsContext(context.Background(), "kates", env.endpoint)
	after := loadConfig()

	assertOtherContextsUnchanged(t, before, after)
	if got := after.Contexts[portsContextName].APIKey; got != adminKey {
		t.Errorf("ports key = %q, want %q", got, adminKey)
	}
	if result.Override != "lab" {
		t.Errorf("Override = %q, want %q", result.Override, "lab")
	}

	var buf bytes.Buffer
	printPortsSync(&buf, result)
	if out := stripAnsi(buf.String()); !strings.Contains(out, `selects "lab"`) {
		t.Errorf("report does not warn that --context outranks ports:\n%s", out)
	}
}

// TestSyncPortsContext_ChecksKeyOnProtectedEndpoint checks where the key is
// tried. /api/health is public, so a check there accepts any key; the old
// check did exactly that and stored keys the API then refused.
func TestSyncPortsContext_ChecksKeyOnProtectedEndpoint(t *testing.T) {
	env := newPortsTestEnv(t, "wrong-key", nil, "right-key")
	mustSaveConfig(t, Config{CurrentContext: "default", Contexts: map[string]Context{}})

	result := syncPortsContext(context.Background(), "kates", env.endpoint)

	if result.Key != keySecretRejected {
		t.Errorf("Key outcome = %v, want keySecretRejected", result.Key)
	}
	for _, probe := range env.seenProbes() {
		if strings.HasPrefix(probe, "/api/health") {
			t.Errorf("key checked on the public health endpoint: %q", probe)
		}
	}
	if want := keyProbePath + " wrong-key"; !reflect.DeepEqual(env.seenProbes(), []string{want}) {
		t.Errorf("probes = %q, want [%q]", env.seenProbes(), want)
	}
}

// TestFetchKatesAPIKey_ReadsOnlyTheSecret checks that the key comes from
// Secret kates-api-key and nowhere else: no pod lookup and no
// `kubectl exec … printenv`, even when the Secret cannot be read.
func TestFetchKatesAPIKey_ReadsOnlyTheSecret(t *testing.T) {
	tests := []struct {
		name    string
		stdout  string
		err     error
		wantKey string
		wantErr string
	}{
		{"decodes the api-key entry", base64.StdEncoding.EncodeToString([]byte(" key-1\n")), nil, "key-1", ""},
		{"missing Secret", "", errors.New(`secrets "kates-api-key" not found`), "", "not found"},
		{"no api-key entry", "", nil, "", "no api-key entry"},
		// What kubectl's go-template prints for a key the Secret lacks.
		{"no api-key entry, as kubectl reports it", "<no value>\n", nil, "", "no api-key entry"},
		{"kubectl's reason, not its exit status",
			"", &exec.ExitError{Stderr: []byte("Error from server (NotFound): secrets \"kates-api-key\" not found\nmore detail\n")},
			"", `Error from server (NotFound): secrets "kates-api-key" not found`},
		{"not base64", "%%%", nil, "", "not base64"},
		{"empty key", base64.StdEncoding.EncodeToString([]byte("  ")), nil, "", "empty api-key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := runExecOutputFn
			t.Cleanup(func() { runExecOutputFn = orig })
			var calls [][]string
			runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
				calls = append(calls, append([]string{name}, args...))
				return []byte(tt.stdout), tt.err
			}

			key, err := fetchKatesAPIKey(context.Background(), "kates-stack")

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("err = %v, want it to mention %q", err, tt.wantErr)
				}
				if err != nil && strings.Contains(err.Error(), "exit status") {
					t.Errorf("err = %v, want kubectl's reason rather than its exit status", err)
				}
			} else if err != nil {
				t.Errorf("err = %v", err)
			}
			if key != tt.wantKey {
				t.Errorf("key = %q, want %q", key, tt.wantKey)
			}
			want := [][]string{{"kubectl", "get", "secret", "kates-api-key", "-n", "kates-stack",
				"-o", `go-template={{index .data "api-key"}}`}}
			if !reflect.DeepEqual(calls, want) {
				t.Errorf("kubectl calls = %q, want only %q", calls, want)
			}
		})
	}
}

func TestCheckAPIKey(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   keyVerdict
	}{
		{"2xx accepts", http.StatusOK, keyAccepted},
		{"401 rejects", http.StatusUnauthorized, keyRejected},
		{"403 rejects", http.StatusForbidden, keyRejected},
		{"404 gives no verdict", http.StatusNotFound, keyUnverified},
		{"500 gives no verdict", http.StatusInternalServerError, keyUnverified},
		{"503 gives no verdict", http.StatusServiceUnavailable, keyUnverified},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath, gotAuth string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
				w.WriteHeader(tt.status)
			}))
			defer ts.Close()

			got := checkAPIKey(context.Background(), ts.URL+"/", "k-1")
			if got.Verdict != tt.want {
				t.Errorf("verdict = %v (%s), want %v", got.Verdict, got.Detail, tt.want)
			}
			if !strings.Contains(got.Detail, http.StatusText(tt.status)) {
				t.Errorf("detail = %q, want the HTTP status", got.Detail)
			}
			if gotPath != keyProbePath || gotAuth != "Bearer k-1" {
				t.Errorf("request = %s with %q, want %s with %q", gotPath, gotAuth, keyProbePath, "Bearer k-1")
			}
		})
	}

	t.Run("unreachable gives no verdict", func(t *testing.T) {
		ts := httptest.NewServer(http.NotFoundHandler())
		endpoint := ts.URL
		ts.Close()
		if got := checkAPIKey(context.Background(), endpoint, "k-1"); got.Verdict != keyUnverified || got.Detail == "" {
			t.Errorf("check = %+v, want unverified with a reason", got)
		}
	})
}

func TestPrintPortsSync(t *testing.T) {
	base := portsSync{Endpoint: "http://localhost:8080", Namespace: "kates"}
	with := func(edit func(*portsSync)) portsSync {
		s := base
		edit(&s)
		return s
	}
	tests := []struct {
		name    string
		sync    portsSync
		want    []string
		notWant []string
	}{
		{
			name: "accepted Secret key",
			sync: with(func(s *portsSync) {
				s.Created, s.Previous = true, "mcp"
				s.Key, s.SecretCheck = keyFromSecret, keyCheck{keyAccepted, "HTTP 200 OK"}
			}),
			want: []string{`Created context "ports" → http://localhost:8080, now the current context`,
				`Context "mcp" was current and is unchanged; switch back with: kates ctx use mcp`,
				"API key from Secret kates-api-key (namespace kates), accepted by the API"},
			notWant: []string{"reject"},
		},
		{
			name: "unverified Secret key",
			sync: with(func(s *portsSync) {
				s.Key, s.SecretCheck = keyFromSecret, keyCheck{keyUnverified, "HTTP 503 Service Unavailable"}
			}),
			want: []string{"stored, but not checked: HTTP 503 Service Unavailable"},
		},
		{
			name: "rejected Secret key",
			sync: with(func(s *portsSync) {
				s.Key, s.SecretCheck = keySecretRejected, keyCheck{keyRejected, "HTTP 403 Forbidden"}
			}),
			want: []string{"The API rejected the key in Secret kates-api-key (namespace kates) with HTTP 403 Forbidden; it was not stored",
				"kubectl rollout restart deployment/kates -n kates",
				`Context "ports" has no API key`},
		},
		{
			name: "user key the API rejects",
			sync: with(func(s *portsSync) {
				s.Key, s.KeptKey, s.KeptCheck = keyUserOwned, true, keyCheck{keyRejected, "HTTP 403 Forbidden"}
			}),
			want: []string{`Kept the API key already in context "ports": kates replaces only a key it copied from the Secret itself`,
				`The API rejects the key in context "ports" (HTTP 403 Forbidden)`,
				"kates ctx set ports --url http://localhost:8080 --api-key <key>",
				"kates ctx delete ports, then kates ports"},
		},
		{
			name: "user key the API accepts",
			sync: with(func(s *portsSync) {
				s.Key, s.KeptKey, s.KeptCheck = keyUserOwned, true, keyCheck{keyAccepted, "HTTP 200 OK"}
			}),
			want:    []string{`The key in context "ports" is accepted by the API.`},
			notWant: []string{"reject"},
		},
		{
			name: "unreadable Secret",
			sync: with(func(s *portsSync) {
				s.Key, s.SecretErr = keySecretUnread, errors.New(`secrets "kates-api-key" not found`)
			}),
			want: []string{`No API key from Secret kates-api-key (namespace kates): secrets "kates-api-key" not found`},
		},
		{
			name: "key changed during the run",
			sync: with(func(s *portsSync) { s.Key = keyChangedMeanwhile }),
			want: []string{`The API key in context "ports" changed while kates ports ran; kept the new key without checking it`,
				"Run kates ports again to check it."},
			notWant: []string{"has no API key"},
		},
		{
			name:    "save failure",
			sync:    with(func(s *portsSync) { s.SaveErr = errors.New("read-only file system") }),
			want:    []string{`Could not save context "ports": read-only file system`},
			notWant: []string{"now the current context"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			printPortsSync(&buf, tt.sync)
			out := stripAnsi(buf.String())
			for _, want := range tt.want {
				if !strings.Contains(out, want) {
					t.Errorf("report lacks %q:\n%s", want, out)
				}
			}
			for _, notWant := range tt.notWant {
				if strings.Contains(out, notWant) {
					t.Errorf("report has %q:\n%s", notWant, out)
				}
			}
		})
	}
}

func TestKeyReplaceable(t *testing.T) {
	tests := []struct {
		name string
		ctx  Context
		want bool
	}{
		{"no key", Context{}, true},
		{"blank key", Context{APIKey: "  "}, true},
		{"a key kates stored", Context{APIKey: "k-1", KeySource: secretKeySource("k-1")}, true},
		{"a key with no key-source", Context{APIKey: "k-1"}, false},
		{"a key typed over one kates stored", Context{APIKey: "k-2", KeySource: secretKeySource("k-1")}, false},
		{"a key-source without a digest", Context{APIKey: "k-1", KeySource: "kates-ports"}, false},
		// The unsalted SHA-256 form an earlier build of this branch wrote.
		{"a key-source in the old sha256 form", Context{APIKey: "k-1", KeySource: apiKeySecretName + "@sha256:0123456789ab"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := keyReplaceable(tt.ctx); got != tt.want {
				t.Errorf("keyReplaceable(%+v) = %v, want %v", tt.ctx, got, tt.want)
			}
		})
	}
}

// TestSecretKeySource checks the digest kates records next to a key it
// copied: salted, slow, bound to that key, and read back only in the form
// kates writes.
func TestSecretKeySource(t *testing.T) {
	if defaultKeySourceIterations < 600_000 {
		t.Errorf("defaultKeySourceIterations = %d, want at least 600000 (OWASP, PBKDF2-HMAC-SHA256)", defaultKeySourceIterations)
	}
	if defaultKeySourceIterations > maxKeySourceIterations {
		t.Errorf("defaultKeySourceIterations = %d is above maxKeySourceIterations = %d, so kates could not read its own digests",
			defaultKeySourceIterations, maxKeySourceIterations)
	}

	src := secretKeySource("k-1")
	if !strings.HasPrefix(src, apiKeySecretName+keySourceScheme) {
		t.Fatalf("secretKeySource = %q, want the prefix %q", src, apiKeySecretName+keySourceScheme)
	}
	if strings.Contains(src, "k-1") {
		t.Error("secretKeySource carries the key itself")
	}
	if !keySourceMatches(src, "k-1") {
		t.Error("the digest does not match the key it was made for")
	}
	if keySourceMatches(src, "k-2") {
		t.Error("the digest matches another key")
	}
	if again := secretKeySource("k-1"); again == src {
		t.Error("two digests of one key are equal: the salt is missing")
	}

	rest := strings.TrimPrefix(src, apiKeySecretName+keySourceScheme)
	parts := strings.Split(rest, ":")
	if len(parts) != 3 {
		t.Fatalf("key-source %q, want work factor, salt and digest", src)
	}
	iterations, salt, digest := parts[0], parts[1], parts[2]
	tampered := []struct{ name, source string }{
		{"empty", ""},
		{"another Secret", "other-secret" + keySourceScheme + rest},
		{"another scheme", apiKeySecretName + "@sha256:" + rest},
		{"a part missing", apiKeySecretName + keySourceScheme + iterations + ":" + salt},
		{"a part too many", apiKeySecretName + keySourceScheme + rest + ":x"},
		{"a work factor that is not a number", apiKeySecretName + keySourceScheme + "many:" + salt + ":" + digest},
		{"a work factor below the least accepted", apiKeySecretName + keySourceScheme + strconv.Itoa(minKeySourceIterations-1) + ":" + salt + ":" + digest},
		{"a work factor above the most accepted", apiKeySecretName + keySourceScheme + strconv.Itoa(maxKeySourceIterations+1) + ":" + salt + ":" + digest},
		{"another work factor", apiKeySecretName + keySourceScheme + strconv.Itoa(minKeySourceIterations+1) + ":" + salt + ":" + digest},
		{"a salt that is not base64", apiKeySecretName + keySourceScheme + iterations + ":!!:" + digest},
		{"a short salt", apiKeySecretName + keySourceScheme + iterations + ":AAAA:" + digest},
		{"a digest that is not base64", apiKeySecretName + keySourceScheme + iterations + ":" + salt + ":!!"},
		{"a short digest", apiKeySecretName + keySourceScheme + iterations + ":" + salt + ":" + digest[:10]},
	}
	for _, tt := range tampered {
		if keySourceMatches(tt.source, "k-1") {
			t.Errorf("%s: keySourceMatches(%q) = true, want false", tt.name, tt.source)
		}
	}
}

// TestIsKatesPortsCommand checks which processes kates ports stops before it
// starts its own forwards: another kates ports, and nothing else.
func TestIsKatesPortsCommand(t *testing.T) {
	tests := []struct {
		cmdLine string
		want    bool
	}{
		{"kates ports\n", true},
		{"/usr/local/bin/kates ports --all", true},
		{"kates --context lab ports", true},
		{"kates -o json ports --kafka-ns kafka", true},
		{"kates --plain ports", true},
		{"kates --url=http://localhost:8080 ports", true},
		{"/opt/dist/kates-darwin-arm64 ports", true},

		{"kates test create --context ports --wait", false},
		{"kates --context ports health", false},
		{"kates lab --context ports", false},
		{"kates report export --out reports/latest.csv", false},
		{"kates deploy -P", false},
		{"kubectl port-forward service/kates 8080:8080 -n kates", false},
		{"vim ports.go", false},
		{"kates", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(strings.TrimSpace(tt.cmdLine), func(t *testing.T) {
			if got := isKatesPortsCommand(tt.cmdLine); got != tt.want {
				t.Errorf("isKatesPortsCommand(%q) = %v, want %v", tt.cmdLine, got, tt.want)
			}
		})
	}
}

// TestLocalPortTaken checks the guard that keeps kates ports from forwarding
// to, and sending the API key to, a port another program listens on.
func TestLocalPortTaken(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	spec := []portForwardSpec{{Label: katesAPILabel, Local: port}}

	if !localPortTaken(port) {
		t.Errorf("port %d has a listener, want it reported taken", port)
	}
	if got := takenLocalPorts(spec, 0); !got[port] {
		t.Errorf("takenLocalPorts = %v, want %d in it", got, port)
	}

	// A port freed while takenLocalPorts waits counts as free, as a forward
	// stopped a moment earlier does.
	go func() {
		time.Sleep(150 * time.Millisecond)
		ln.Close()
	}()
	if got := takenLocalPorts(spec, 5*time.Second); len(got) != 0 {
		t.Errorf("takenLocalPorts after the listener closed = %v, want none", got)
	}
	if localPortTaken(port) {
		t.Errorf("port %d is free, want it reported free", port)
	}
}

func TestPrintAPIPortTaken(t *testing.T) {
	var buf bytes.Buffer
	printAPIPortTaken(&buf, 8080)
	out := stripAnsi(buf.String())
	for _, want := range []string{
		"localhost:8080 is in use by another program, so the API is not forwarded there",
		`Context "ports" is unchanged and no API key was sent`,
		"lsof -nP -iTCP:8080 -sTCP:LISTEN",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}
