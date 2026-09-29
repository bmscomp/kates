package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPConfigFromFlags(t *testing.T) {
	cfg := Config{Contexts: map[string]Context{"lab": {URL: "http://localhost:30083"}}}
	tests := []struct {
		name      string
		context   string
		overrides []string
		allow     []string
		label     string
		wantErr   string
	}{
		{"no context", "", nil, []string{"c1"}, "lab", "explicit context"},
		{"context not in the config", "prod", nil, []string{"c1"}, "lab", `context "prod" is not in`},
		{"URL from --url", "lab", []string{"--url"}, []string{"c1"}, "lab", "drop --url"},
		{"URL from KATES_URL", "lab", []string{"KATES_URL"}, []string{"c1"}, "lab", "unset KATES_URL"},
		{"key from --api-key", "lab", []string{"--api-key"}, []string{"c1"}, "lab", "does not take --api-key"},
		{"no allowed cluster", "lab", nil, nil, "lab", "at least one --allow-cluster"},
		{"allowed cluster with a space", "lab", nil, []string{"a b"}, "lab", "is not a clusterId"},
		{"empty allowed cluster", "lab", nil, []string{"  "}, "lab", "is not a clusterId"},
		{"label with a space", "lab", nil, []string{"c1"}, "my lab", "--cluster-label"},
		{"label too long", "lab", nil, []string{"c1"}, strings.Repeat("x", 33), "--cluster-label"},
		{"empty label", "lab", nil, []string{"c1"}, "", "--cluster-label"},
		{"accepted", "lab", nil, []string{" c1 ", "c2"}, "lab-2", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mcpConfigFromFlags(tt.context, cfg, tt.overrides, tt.allow, tt.label)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if strings.Join(got.allowed, ",") != "c1,c2" || got.label != "lab-2" || got.context != "lab" ||
					got.url != "http://localhost:30083" {
					t.Errorf("config = %+v", got)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestMCPContextClient: the server reads the API the context names, as
// written. When the root command's port fallback has swapped 8080 for 30083
// (or back), it refuses to start rather than send the key to the other port,
// and the refusal names both URLs without their user information.
func TestMCPContextClient(t *testing.T) {
	for _, tt := range []struct{ built, context string }{
		{"http://localhost:8080", "http://localhost:8080"},
		{"http://localhost:8080", "http://localhost:8080/"},
		{"https://kates.example/base", "https://kates.example/base/"},
	} {
		base := client.New(tt.built)
		if got, err := mcpContextClient(base, "lab", tt.context); err != nil || got != base {
			t.Errorf("built %s, context %s: got %v, %v; want the same client", tt.built, tt.context, got, err)
		}
	}
	for _, tt := range []struct{ built, context string }{
		{"http://localhost:30083", "http://localhost:8080"},
		{"http://127.0.0.1:8080", "http://127.0.0.1:30083"},
		{"http://tok3n-user:pw@localhost:30083", "http://tok3n-user:pw@localhost:8080"},
	} {
		got, err := mcpContextClient(client.New(tt.built), "lab", tt.context)
		if err == nil || got != nil {
			t.Errorf("built %s, context %s: got %v, want a refusal", tt.built, tt.context, got)
			continue
		}
		msg := err.Error()
		if !strings.Contains(msg, "does not answer") || !strings.Contains(msg, "kates ctx set lab --url") ||
			!strings.Contains(msg, mcpRedactURL(tt.built)) || !strings.Contains(msg, mcpRedactURL(tt.context)) {
			t.Errorf("refusal = %q", msg)
		}
		if strings.Contains(msg, "tok3n-user") || strings.Contains(msg, "pw@") {
			t.Errorf("the refusal names the URL with its user information: %q", msg)
		}
	}
}

func TestMCPPinCluster(t *testing.T) {
	fb := newMCPFakeBackend(t, "cluster-a")
	c, err := newMCPClient(client.New(fb.URL()))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if id, err := mcpPinCluster(ctx, c, []string{"other", "cluster-a"}); err != nil || id != "cluster-a" {
		t.Errorf("allowed cluster: id=%q err=%v", id, err)
	}

	_, err = mcpPinCluster(ctx, c, []string{"cluster-x"})
	if err == nil || !strings.Contains(err.Error(), "cluster cluster-a is not allowed") ||
		!strings.Contains(err.Error(), "add --allow-cluster cluster-a") {
		t.Errorf("not allowed: %v", err)
	}

	// A hostile clusterId is cleaned before it is printed.
	fb.SetClusterID("evil\x1b[2J\nid")
	if _, err := mcpPinCluster(ctx, c, []string{"cluster-a"}); err == nil || strings.ContainsAny(err.Error(), "\x1b\n") {
		t.Errorf("error prints the live id raw: %q", err)
	}

	fb.SetClusterID("")
	if _, err := mcpPinCluster(ctx, c, []string{"cluster-a"}); err == nil || !strings.Contains(err.Error(), "no clusterId") {
		t.Errorf("empty clusterId: %v", err)
	}

	// The refusal goes to the MCP client's log: a password in the URL must
	// not.
	fb.SetClusterID("cluster-a")
	u, _ := url.Parse(fb.URL())
	u.User = url.UserPassword("kates", "s3cret-token")
	withUser, err := newMCPClient(client.New(u.String()))
	if err != nil {
		t.Fatal(err)
	}
	_, err = mcpPinCluster(ctx, withUser, []string{"cluster-x"})
	if err == nil || strings.Contains(err.Error(), "s3cret-token") || !strings.Contains(err.Error(), "redacted@"+u.Host) {
		t.Errorf("the refusal must name the URL without its user information: %v", err)
	}
	assertReadOnly(t, fb.Requests())

	// A token in the user name, which http.Client does not mask, with the
	// API down: the refusal quotes the transport error, and neither copy of
	// the URL in it may carry the token.
	down := newMCPFakeBackend(t, "cluster-a")
	du, _ := url.Parse(down.URL())
	down.srv.Close()
	du.User = url.UserPassword("tok3n-user-SECRET", "pa55word")
	downClient := client.New(du.String())
	downClient.MaxRetries = 1
	c2, err := newMCPClient(downClient)
	if err != nil {
		t.Fatal(err)
	}
	_, err = mcpPinCluster(ctx, c2, []string{"cluster-a"})
	if err == nil || strings.Contains(err.Error(), "tok3n-user-SECRET") || strings.Contains(err.Error(), "pa55word") ||
		strings.Count(err.Error(), "redacted@"+du.Host) != 2 {
		t.Errorf("the refusal must name the URL twice without its user information: %v", err)
	}
}

// TestMCPErrorText: the URL of a url.Error in the chain loses its user
// information, query and fragment, wherever the chain was wrapped.
func TestMCPErrorText(t *testing.T) {
	ue := &url.Error{Op: "Get", URL: "http://tok3n-user:***@127.0.0.1:1/api/tests?page=0", Err: errors.New("dial tcp: connection refused")}
	for _, tt := range []struct {
		err  error
		want string
	}{
		{ue, `Get "http://redacted@127.0.0.1:1/api/tests": dial tcp: connection refused`},
		{fmt.Errorf("connection failed: %w", ue), `connection failed: Get "http://redacted@127.0.0.1:1/api/tests": dial tcp: connection refused`},
		// A wrapper that does not print the url.Error: its text is rebuilt.
		{&mcpOpaqueError{"failed at http://tok3n-user@127.0.0.1:1", ue}, `Get "http://redacted@127.0.0.1:1/api/tests": dial tcp: connection refused`},
		{&url.Error{Op: "parse", URL: "http://tok3n-user@[bad", Err: errors.New("missing ']' in host")}, `parse "(an unparsable URL)": missing ']' in host`},
		{errors.New("HTTP 404: no such run"), "HTTP 404: no such run"},
		{nil, ""},
	} {
		got := mcpErrorText(tt.err)
		if got != tt.want {
			t.Errorf("mcpErrorText(%v) = %q, want %q", tt.err, got, tt.want)
		}
		if strings.Contains(got, "tok3n-user") {
			t.Errorf("mcpErrorText(%v) keeps the user name: %q", tt.err, got)
		}
	}
}

type mcpOpaqueError struct {
	text string
	err  error
}

func (e *mcpOpaqueError) Error() string { return e.text }
func (e *mcpOpaqueError) Unwrap() error { return e.err }

// TestMCPLoggerRedactsErrors: the tools log errors as they come, and the log
// goes to the MCP client, which keeps it.
func TestMCPLoggerRedactsErrors(t *testing.T) {
	var buf bytes.Buffer
	ue := &url.Error{Op: "Get", URL: "http://tok3n-user:***@127.0.0.1:1/api/tests", Err: errors.New("connection refused")}
	newMCPLogger(&buf).Warn("a list is unavailable", "error", fmt.Errorf("connection failed: %w", ue), "list", 2)
	got := buf.String()
	if strings.Contains(got, "tok3n-user") || !strings.Contains(got, "redacted@127.0.0.1:1") ||
		!strings.Contains(got, "component=kates-mcp") || !strings.Contains(got, "list=2") {
		t.Errorf("log line = %q", got)
	}
}

func TestMCPRedactURL(t *testing.T) {
	for in, want := range map[string]string{
		"http://localhost:30083":                        "http://localhost:30083",
		"https://kates.example/base/":                   "https://kates.example/base/",
		"http://user:pass@localhost:8080":               "http://redacted@localhost:8080",
		"http://tokenInUserName@localhost:8080/api":     "http://redacted@localhost:8080/api",
		"http://localhost:8080/?token=abc#frag":         "http://localhost:8080/",
		"http://user:p%40ss@host:1/kates?x=y&token=zzz": "http://redacted@host:1/kates",
		"://no-scheme":                                  "(an unparsable URL)",
	} {
		if got := mcpRedactURL(in); got != want {
			t.Errorf("mcpRedactURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// mcpIsolateCommand lets a test run kates mcp through rootCmd, flags,
// PersistentPreRun and all: it saves and restores every global the command
// touches, and points HOME at a config whose current context is NOT the one
// the test asks for, so a server that fell back to it would show.
func mcpIsolateCommand(t *testing.T, labURL string) {
	t.Helper()
	savedURL, savedMode, savedCtx, savedKey, savedPlain, savedClient := apiURL, outputMode, contextFlag, apiKeyFlag, plainOutput, apiClient
	savedOut, savedErr := output.Out, output.Err
	savedStdin, savedStdout, savedStderr := os.Stdin, os.Stdout, os.Stderr
	savedFlags := mcpFlags
	// pflag never resets Changed, and kates mcp reads it to refuse --url and
	// --api-key, so a flag one test passed must not look passed in the next.
	resetChanged := func() {
		for _, name := range []string{"url", "api-key", "context", "output", "plain"} {
			if f := rootCmd.PersistentFlags().Lookup(name); f != nil {
				f.Changed = false
			}
		}
	}
	t.Cleanup(func() {
		apiURL, outputMode, contextFlag, apiKeyFlag, plainOutput, apiClient = savedURL, savedMode, savedCtx, savedKey, savedPlain, savedClient
		output.Out, output.Err = savedOut, savedErr
		os.Stdin, os.Stdout, os.Stderr = savedStdin, savedStdout, savedStderr
		mcpFlags = savedFlags
		rootCmd.SetArgs(nil)
		resetChanged()
	})
	resetChanged()
	apiURL, outputMode, contextFlag, apiKeyFlag, plainOutput = "", "", "", "", false
	mcpFlags.allowClusters, mcpFlags.clusterLabel = nil, "lab"

	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, k := range []string{"KATES_CONTEXT", "KATES_URL", "KATES_API_KEY", "KATES_OUTPUT", "KATES_PLAIN"} {
		t.Setenv(k, "")
	}
	config := fmt.Sprintf("current-context: other\ncontexts:\n  lab:\n    url: %s\n  other:\n    url: http://127.0.0.1:1\n", labURL)
	if err := os.WriteFile(filepath.Join(home, ".kates.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mcpPipe(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		r.Close()
		w.Close()
	})
	return r, w
}

func TestMCPCommandRefusesToStart(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		args    []string
		wantErr string
	}{
		{"no explicit context", nil,
			[]string{"mcp", "--allow-cluster", "cluster-a"}, "explicit context"},
		{"context not in the config", nil,
			[]string{"mcp", "--context", "nope", "--allow-cluster", "cluster-a"}, `context "nope" is not in`},
		{"no allowed cluster", nil,
			[]string{"mcp", "--context", "lab"}, "at least one --allow-cluster"},
		{"cluster not allowed", nil,
			[]string{"mcp", "--context", "lab", "--allow-cluster", "cluster-x"}, "cluster cluster-a is not allowed"},
		{"context from KATES_CONTEXT, cluster not allowed", map[string]string{"KATES_CONTEXT": "lab"},
			[]string{"mcp", "--allow-cluster", "cluster-x", "--allow-cluster", "cluster-y"}, "add --allow-cluster cluster-a"},
		{"bad label", nil,
			[]string{"mcp", "--context", "lab", "--allow-cluster", "cluster-a", "--cluster-label", "a b"}, "--cluster-label"},
		// The allowed cluster is the live one, so only the override stops
		// the server.
		{"URL from --url", nil,
			[]string{"mcp", "--context", "lab", "--allow-cluster", "cluster-a", "--url", "http://127.0.0.1:1"}, "drop --url"},
		{"URL from KATES_URL", map[string]string{"KATES_URL": "http://127.0.0.1:1"},
			[]string{"mcp", "--context", "lab", "--allow-cluster", "cluster-a"}, "unset KATES_URL"},
		{"key from --api-key", nil,
			[]string{"mcp", "--context", "lab", "--allow-cluster", "cluster-a", "--api-key", "k"}, "does not take --api-key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fb := newMCPFakeBackend(t, "cluster-a")
			mcpIsolateCommand(t, fb.URL())
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			// stdin is already closed: a command that wrongly started
			// serving would end at once instead of hanging the test. The
			// output package writes to the pipes too, as it writes to the
			// real stdout and stderr in production.
			inR, inW := mcpPipe(t)
			inW.Close()
			outR, outW := mcpPipe(t)
			_, errW := mcpPipe(t)
			os.Stdin, os.Stdout, os.Stderr = inR, outW, errW
			output.Out, output.Err = outW, errW

			rootCmd.SetArgs(tt.args)
			err := rootCmd.Execute()
			outW.Close()
			stdout, _ := io.ReadAll(outR)

			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want one containing %q", err, tt.wantErr)
			}
			if len(stdout) != 0 {
				t.Errorf("a refusal wrote to stdout: %q", stdout)
			}
			assertReadOnly(t, fb.Requests())
		})
	}
}

// mcpTeeReadCloser records what the client reads from the server's stdout.
type mcpTeeReadCloser struct {
	r io.ReadCloser
	w io.Writer
}

func (t *mcpTeeReadCloser) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 {
		_, _ = t.w.Write(p[:n])
	}
	return n, err
}

func (t *mcpTeeReadCloser) Close() error { return t.r.Close() }

// TestMCPCommandStdoutCarriesOnlyJSONRPC runs the real command, through
// rootCmd and its PersistentPreRun, with stdin, stdout and stderr swapped for
// pipes, and drives it as an MCP client would. Every byte on stdout must be a
// JSON-RPC message; the log must be on stderr.
func TestMCPCommandStdoutCarriesOnlyJSONRPC(t *testing.T) {
	fb := newMCPFakeBackend(t, "cluster-a")
	mcpIsolateCommand(t, fb.URL())

	inR, inW := mcpPipe(t)
	outR, outW := mcpPipe(t)
	errR, errW := mcpPipe(t)
	os.Stdin, os.Stdout, os.Stderr = inR, outW, errW
	// In production the output package writes to the real stdout, the
	// JSON-RPC stream: here that is the pipe, so a banner, hint or success
	// line printed through it during a session fails the test.
	output.Out, output.Err = outW, errW

	var stderr bytes.Buffer
	stderrDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&stderr, errR)
		close(stderrDone)
	}()

	rootCmd.SetArgs([]string{"mcp", "--context", "lab", "--allow-cluster", "cluster-a", "--cluster-label", "e2e"})
	done := make(chan error, 1)
	go func() { done <- rootCmd.Execute() }()
	// However the test ends, the command must be gone before the globals are
	// restored (cleanups run last-registered first).
	exited := false
	t.Cleanup(func() {
		if exited {
			return
		}
		inW.Close()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("kates mcp still running at the end of the test")
		}
	})

	var raw bytes.Buffer
	var rawMu sync.Mutex
	transport := &mcp.IOTransport{Reader: &mcpTeeReadCloser{r: outR, w: &mcpLockedWriter{mu: &rawMu, w: &raw}}, Writer: inW}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "test"}, nil).Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	var tools []string
	for tool, err := range cs.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		tools = append(tools, tool.Name)
	}
	if !strings.Contains(strings.Join(tools, ","), "cluster_overview") {
		t.Errorf("tools = %v", tools)
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "cluster_overview"})
	if err != nil || res.IsError {
		t.Fatalf("cluster_overview: err=%v result=%+v", err, res)
	}
	var env mcpEnvelope
	if err := json.Unmarshal([]byte(mcpResultText(t, res)), &env); err != nil {
		t.Fatal(err)
	}
	if env.Cluster != (mcpClusterRef{ID: "cluster-a", Label: "e2e"}) {
		t.Errorf("envelope cluster = %+v", env.Cluster)
	}
	if _, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: mcpCaveatsURI}); err != nil {
		t.Errorf("read %s: %v", mcpCaveatsURI, err)
	}

	// Closing the client closes the server's stdin; the command then exits
	// cleanly.
	if err := cs.Close(); err != nil {
		t.Logf("client close: %v", err)
	}
	select {
	case err := <-done:
		exited = true
		if err != nil {
			t.Fatalf("kates mcp exited with %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("kates mcp did not exit after its stdin closed")
	}
	if os.Stdout != outW {
		t.Error("kates mcp did not restore os.Stdout when it returned")
	}
	errW.Close()
	<-stderrDone

	rawMu.Lock()
	stdout := raw.String()
	rawMu.Unlock()
	if strings.TrimSpace(stdout) == "" {
		t.Fatal("nothing was read from stdout")
	}
	sc := bufio.NewScanner(strings.NewReader(stdout))
	sc.Buffer(make([]byte, 0, 1<<20), 1<<22)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var msg struct {
			JSONRPC string `json:"jsonrpc"`
		}
		if err := json.Unmarshal(line, &msg); err != nil || msg.JSONRPC != "2.0" {
			t.Errorf("stdout carries something that is not a JSON-RPC message: %q", line)
		}
	}
	for _, want := range []string{"serving on stdio", "cluster=cluster-a", "tool=cluster_overview outcome=ok"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr.String())
		}
	}
	assertReadOnly(t, fb.Requests())
}

// TestMCPStdioTransportRedirectsStdout: once the transport holds the real
// stdout, a stray print anywhere in the process goes to stderr.
func TestMCPStdioTransportRedirectsStdout(t *testing.T) {
	savedStdin, savedStdout, savedStderr := os.Stdin, os.Stdout, os.Stderr
	t.Cleanup(func() { os.Stdin, os.Stdout, os.Stderr = savedStdin, savedStdout, savedStderr })
	inR, _ := mcpPipe(t)
	outR, outW := mcpPipe(t)
	errR, errW := mcpPipe(t)
	os.Stdin, os.Stdout, os.Stderr = inR, outW, errW

	conn, err := (&mcpStdioTransport{}).Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("stray output")
	_ = conn.Close()
	outW.Close()
	errW.Close()

	if got, _ := io.ReadAll(outR); len(got) != 0 {
		t.Errorf("stdout got %q", got)
	}
	if got, _ := io.ReadAll(errR); !strings.Contains(string(got), "stray output") {
		t.Errorf("stderr got %q, want the stray output", got)
	}
}

// TestMCPCommandHelp keeps the help honest about what the command needs.
func TestMCPCommandHelp(t *testing.T) {
	if !strings.Contains(mcpCmd.Short, "read-only") || !strings.Contains(mcpCmd.Short, "experimental") {
		t.Errorf("Short = %q", mcpCmd.Short)
	}
	for _, want := range []string{"--context", "KATES_CONTEXT", "--allow-cluster", "stderr", "kates://caveats",
		"--url, KATES_URL and --api-key", "KATES_API_KEY",
		"claude mcp add --transport stdio kates -- kates mcp --context lab --allow-cluster"} {
		if !strings.Contains(mcpCmd.Long+mcpCmd.Example, want) {
			t.Errorf("help lacks %q", want)
		}
	}
	for _, flag := range []string{"allow-cluster", "cluster-label"} {
		if mcpCmd.Flags().Lookup(flag) == nil {
			t.Errorf("no --%s flag", flag)
		}
	}
	if c, _, err := rootCmd.Find([]string{"mcp"}); err != nil || c != mcpCmd {
		t.Errorf("kates mcp is not registered: %v", err)
	}
}
