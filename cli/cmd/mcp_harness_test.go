package cmd

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmscomp/kates/cli/client"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The harness every kates mcp test uses: a fake Kates API with a route table
// and a request log, a server built with real deps against it, and a client
// session over the SDK's in-memory transports. Tool tests call
// h.callOK/h.callErr and may call assertReadOnly(t, h.backend.Requests())
// themselves; newMCPHarness checks it again when the test ends, so no tool
// test can leave it out.

// mcpRequest is one request the fake backend received. Path is the path as
// sent, still escaped, so that a test can tell a group id "a/b" sent as a%2Fb
// from two segments a and b; routes match on the same form.
type mcpRequest struct {
	Method   string
	Path     string
	RawQuery string
}

type mcpFakeBackend struct {
	srv *httptest.Server

	mu        sync.Mutex
	clusterID string
	routes    map[string]http.HandlerFunc
	log       []mcpRequest
}

// newMCPFakeBackend serves a healthy three-broker cluster whose clusterId is
// clusterID: /api/cluster/info, /check and /alerts answer by default; other
// routes are added with JSON or Handle, by escaped path, and anything else is
// a JSON 404.
func newMCPFakeBackend(t *testing.T, clusterID string) *mcpFakeBackend {
	t.Helper()
	fb := &mcpFakeBackend{clusterID: clusterID, routes: map[string]http.HandlerFunc{}}
	fb.srv = httptest.NewServer(http.HandlerFunc(fb.serve))
	t.Cleanup(fb.srv.Close)

	fb.Handle("GET", "/api/cluster/info", func(w http.ResponseWriter, r *http.Request) {
		mcpWriteJSON(w, http.StatusOK, map[string]any{
			"clusterId":   fb.ClusterID(),
			"brokerCount": 3,
			"controller":  map[string]any{"id": 1, "host": "kafka-1.kafka.svc", "port": 9092},
			"brokers": []map[string]any{
				{"id": 2, "host": "kafka-2.kafka.svc", "port": 9092, "rack": "b"},
				{"id": 0, "host": "kafka-0.kafka.svc", "port": 9092, "rack": "a"},
				{"id": 1, "host": "kafka-1.kafka.svc", "port": 9092, "rack": "a"},
			},
		})
	})
	fb.Handle("GET", "/api/cluster/check", func(w http.ResponseWriter, r *http.Request) {
		mcpWriteJSON(w, http.StatusOK, fb.HealthyCheck())
	})
	fb.JSON("GET", "/api/cluster/alerts", http.StatusOK, map[string]any{
		"totalRulesScanned": 0, "criticalCount": 0, "warningCount": 0, "count": 0, "alerts": []any{},
	})
	return fb
}

func (fb *mcpFakeBackend) serve(w http.ResponseWriter, r *http.Request) {
	path := r.URL.EscapedPath()
	fb.mu.Lock()
	fb.log = append(fb.log, mcpRequest{Method: r.Method, Path: path, RawQuery: r.URL.RawQuery})
	h := fb.routes[r.Method+" "+path]
	fb.mu.Unlock()
	if h == nil {
		mcpWriteJSON(w, http.StatusNotFound, map[string]any{"status": 404, "error": "Not Found", "message": "no route for " + r.URL.Path})
		return
	}
	h(w, r)
}

func (fb *mcpFakeBackend) URL() string { return fb.srv.URL }

// HealthyCheck is the /api/cluster/check body the backend serves by default,
// for handlers that change one thing about it.
func (fb *mcpFakeBackend) HealthyCheck() map[string]any {
	return map[string]any{
		"clusterId": fb.ClusterID(), "brokers": 3, "controllerId": 1, "topics": 4, "partitions": 12,
		"consumerGroups":  2,
		"partitionHealth": map[string]any{"underReplicated": 0, "offline": 0, "problems": []any{}},
		"kraftQuorum":     map[string]any{"leaderId": 1, "voters": 3, "observers": 3, "hasLeader": true},
		"status":          "HEALTHY",
	}
}

func (fb *mcpFakeBackend) ClusterID() string {
	fb.mu.Lock()
	defer fb.mu.Unlock()
	return fb.clusterID
}

// SetClusterID makes the backend report another cluster from the next
// request on, as a port-forward pointed elsewhere would.
func (fb *mcpFakeBackend) SetClusterID(id string) {
	fb.mu.Lock()
	fb.clusterID = id
	fb.mu.Unlock()
}

// Handle sets the handler for one method and path, replacing any default.
func (fb *mcpFakeBackend) Handle(method, path string, h http.HandlerFunc) {
	fb.mu.Lock()
	fb.routes[method+" "+path] = h
	fb.mu.Unlock()
}

// JSON answers one method and path with a fixed status and JSON body.
func (fb *mcpFakeBackend) JSON(method, path string, status int, body any) {
	fb.Handle(method, path, func(w http.ResponseWriter, r *http.Request) { mcpWriteJSON(w, status, body) })
}

// Requests is the request log so far.
func (fb *mcpFakeBackend) Requests() []mcpRequest {
	fb.mu.Lock()
	defer fb.mu.Unlock()
	return append([]mcpRequest(nil), fb.log...)
}

// ResetLog empties the request log, so a test can look at one call alone.
func (fb *mcpFakeBackend) ResetLog() {
	fb.mu.Lock()
	fb.log = nil
	fb.mu.Unlock()
}

func mcpWriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// assertReadOnly is the contract every tool test ends with: the server sent
// nothing but GETs, and POST /api/disruptions with exactly dryRun=true; no
// GET reached a surface plan §4.4 never exposes; and no path held a dot
// segment, an empty segment or a backslash, which a server could route
// somewhere other than where the path appears to go. It is written out here
// rather than calling mcpIsReadOnlyRequest, so a bug in the production check
// cannot hide itself.
func assertReadOnly(t *testing.T, log []mcpRequest) {
	t.Helper()
	neverRead := []string{"/api/kafka/consume", "/api/security/secrets", "/api/security/acl-map", "/api/security/auth-test"}
	for _, r := range log {
		sent := r.Method + " " + r.Path
		if r.RawQuery != "" {
			sent += "?" + r.RawQuery
		}
		decoded, err := url.PathUnescape(r.Path)
		if err != nil || mcpAmbiguousPath(r.Path) || mcpAmbiguousPath(decoded) {
			t.Errorf("kates mcp sent %s: a path with a dot segment, an empty segment or a backslash", sent)
			continue
		}
		switch {
		case r.Method == http.MethodGet:
			var segs []string
			for _, seg := range strings.Split(decoded, "/") {
				seg, _, _ = strings.Cut(seg, ";") // JAX-RS drops matrix parameters when it routes
				segs = append(segs, seg)
			}
			routed := strings.ToLower(strings.Join(segs, "/"))
			for _, p := range neverRead {
				if routed == p || strings.HasPrefix(routed, p+"/") {
					t.Errorf("kates mcp sent %s: plan §4.4 never exposes %s", sent, p)
				}
			}
		case r.Method == http.MethodPost && r.Path == "/api/disruptions" && r.RawQuery == "dryRun=true":
		default:
			t.Errorf("kates mcp sent %s: only GET and POST /api/disruptions?dryRun=true are allowed", sent)
		}
	}
}

func mcpAmbiguousPath(p string) bool {
	if strings.Contains(p, `\`) {
		return true
	}
	segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, seg := range segs {
		seg, _, _ = strings.Cut(seg, ";")
		if seg == "." || seg == ".." || (seg == "" && i < len(segs)-1) {
			return true
		}
	}
	return false
}

// mcpTestLimits are the production limits with a short timeout, so a test
// that waits on one does not wait long.
var mcpTestLimits = mcpLimits{
	CallsPerMinute: 6000,
	Burst:          1000,
	MaxInFlight:    4,
	CallTimeout:    5 * time.Second,
	MaxResultBytes: mcpDefaultLimits.MaxResultBytes,
}

type mcpHarnessConfig struct {
	limits   mcpLimits
	now      func() time.Time
	protocol string
	extra    []func(s *mcp.Server, d *mcpDeps)
	logger   *slog.Logger
	base     func(*client.Client)
}

type mcpHarnessOption func(*mcpHarnessConfig)

func withMCPLimits(l mcpLimits) mcpHarnessOption { return func(c *mcpHarnessConfig) { c.limits = l } }
func withMCPClock(now func() time.Time) mcpHarnessOption {
	return func(c *mcpHarnessConfig) { c.now = now }
}

// withMCPProtocol pins the protocol version the client asks for.
func withMCPProtocol(v string) mcpHarnessOption { return func(c *mcpHarnessConfig) { c.protocol = v } }

// withMCPTools registers extra tools (test-only) after the real ones.
func withMCPTools(fs ...func(s *mcp.Server, d *mcpDeps)) mcpHarnessOption {
	return func(c *mcpHarnessConfig) { c.extra = append(c.extra, fs...) }
}

// withMCPBase changes the CLI client the server's client is copied from, as
// PersistentPreRun would have built it.
func withMCPBase(f func(*client.Client)) mcpHarnessOption {
	return func(c *mcpHarnessConfig) { c.base = f }
}

// mcpTestTool registers a test-only read tool through the guard, the way a
// real one is registered.
func mcpTestTool[In, Out any](name string, h mcpReadHandler[In, Out], caveats ...mcpCaveatID) func(*mcp.Server, *mcpDeps) {
	return func(s *mcp.Server, d *mcpDeps) {
		addReadTool(s, d, &mcp.Tool{Name: name, Title: "Test " + name, Description: "A test-only tool."}, h, caveats...)
	}
}

// mcpTestResource registers a test-only resource template, with one id
// variable, through the guard.
func mcpTestResource(template string, h mcpResourceHandler, caveats ...mcpCaveatID) func(*mcp.Server, *mcpDeps) {
	return func(s *mcp.Server, d *mcpDeps) {
		addReadResourceTemplate(s, d, &mcp.ResourceTemplate{
			URITemplate: template,
			Name:        "test",
			Title:       "Test resource",
			Description: "A test-only resource.",
			MIMEType:    "text/markdown",
		}, map[string]mcpResourceVar{"id": mcpIDVar}, h, caveats...)
	}
}

// mcpFakeClock drives the rate limiter in tests.
type mcpFakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newMCPFakeClock() *mcpFakeClock {
	return &mcpFakeClock{now: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)}
}

func (c *mcpFakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mcpFakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func withMCPLogger(l *slog.Logger) mcpHarnessOption {
	return func(c *mcpHarnessConfig) { c.logger = l }
}

type mcpHarness struct {
	t       *testing.T
	backend *mcpFakeBackend
	deps    *mcpDeps
	session *mcp.ClientSession
	tools   map[string]*mcp.Tool
}

// newMCPHarness builds the server exactly as kates mcp does (newMCPClient,
// newMCPDeps, newMCPServer) against fb, pinned to fb's current clusterId, and
// connects a client to it.
func newMCPHarness(t *testing.T, fb *mcpFakeBackend, opts ...mcpHarnessOption) *mcpHarness {
	t.Helper()
	cfg := mcpHarnessConfig{limits: mcpTestLimits}
	for _, o := range opts {
		o(&cfg)
	}
	// Registered first, so it runs after the session has closed and every
	// request has been logged.
	t.Cleanup(func() { assertReadOnly(t, fb.Requests()) })
	base := client.New(fb.URL())
	// One attempt per request: the retry policy belongs to cli/client and is
	// tested there; here it would only make error-path tests slow.
	base.MaxRetries = 1
	if cfg.base != nil {
		cfg.base(base)
	}
	c, err := newMCPClient(base)
	if err != nil {
		t.Fatal(err)
	}
	logger := cfg.logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	deps, err := newMCPDeps(mcpDepsConfig{
		Client:  c,
		Cluster: mcpClusterRef{ID: fb.ClusterID(), Label: "test"},
		Limits:  cfg.limits,
		Now:     cfg.now,
		Logger:  logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := newMCPServer(deps)
	for _, f := range cfg.extra {
		f(server, deps)
	}

	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "kates-test", Version: "test"}, nil).
		Connect(ctx, ct, &mcp.ClientSessionOptions{ProtocolVersion: cfg.protocol})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cs.Close()
		_ = ss.Wait()
	})

	h := &mcpHarness{t: t, backend: fb, deps: deps, session: cs, tools: map[string]*mcp.Tool{}}
	for tool, err := range cs.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		h.tools[tool.Name] = tool
	}
	return h
}

// call makes one tools/call; a protocol-level error fails the test.
func (h *mcpHarness) call(name string, args map[string]any) *mcp.CallToolResult {
	h.t.Helper()
	res, err := h.session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		h.t.Fatalf("tools/call %s: %v", name, err)
	}
	return res
}

// mcpEnvelope is a successful result as a client decodes it.
type mcpEnvelope struct {
	Cluster   mcpClusterRef   `json:"cluster"`
	Tier      string          `json:"tier"`
	Caveats   []mcpCaveatRef  `json:"caveats"`
	Truncated bool            `json:"truncated"`
	Data      json.RawMessage `json:"data"`
}

// callOK calls a tool that must succeed. It checks the result shape every
// tool promises: structuredContent valid against the tool's outputSchema, a
// text block holding the same JSON, and the envelope around the data.
func (h *mcpHarness) callOK(name string, args map[string]any) mcpEnvelope {
	h.t.Helper()
	res := h.call(name, args)
	if res.IsError {
		h.t.Fatalf("%s failed: %s", name, mcpResultText(h.t, res))
	}
	structured, err := json.Marshal(res.StructuredContent)
	if err != nil {
		h.t.Fatal(err)
	}
	h.validateOutput(name, structured)

	var fromStructured, fromText any
	if err := json.Unmarshal(structured, &fromStructured); err != nil {
		h.t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(mcpResultText(h.t, res)), &fromText); err != nil {
		h.t.Fatalf("%s: the text block is not the JSON result: %v", name, err)
	}
	a, _ := json.Marshal(fromStructured)
	b, _ := json.Marshal(fromText)
	if string(a) != string(b) {
		h.t.Fatalf("%s: text block and structuredContent differ:\n text: %s\n structured: %s", name, b, a)
	}

	var env mcpEnvelope
	if err := json.Unmarshal(structured, &env); err != nil {
		h.t.Fatal(err)
	}
	if env.Cluster != h.deps.cluster {
		h.t.Errorf("%s: envelope cluster = %+v, want %+v", name, env.Cluster, h.deps.cluster)
	}
	if env.Tier != mcpTierObserve {
		h.t.Errorf("%s: tier = %q, want %q", name, env.Tier, mcpTierObserve)
	}
	if env.Caveats == nil {
		h.t.Errorf("%s: caveats is null, want a list", name)
	}
	return env
}

// callErr calls a tool that must fail, and returns the error it carries. An
// error result has no structuredContent and names the pinned cluster.
func (h *mcpHarness) callErr(name string, args map[string]any) mcpErrorResult {
	h.t.Helper()
	res := h.call(name, args)
	if !res.IsError {
		h.t.Fatalf("%s succeeded, want an error: %s", name, mcpResultText(h.t, res))
	}
	if res.StructuredContent != nil {
		h.t.Errorf("%s: error result carries structuredContent %v", name, res.StructuredContent)
	}
	var e mcpErrorResult
	if err := json.Unmarshal([]byte(mcpResultText(h.t, res)), &e); err != nil {
		h.t.Fatalf("%s: error text is not the error JSON: %v", name, err)
	}
	if e.Cluster != h.deps.cluster || e.Tier != mcpTierObserve {
		h.t.Errorf("%s: error names cluster %+v tier %q, want %+v %q", name, e.Cluster, e.Tier, h.deps.cluster, mcpTierObserve)
	}
	return e
}

func (h *mcpHarness) validateOutput(name string, structured []byte) {
	h.t.Helper()
	tool := h.tools[name]
	if tool == nil || tool.OutputSchema == nil {
		h.t.Fatalf("%s: no outputSchema in tools/list", name)
	}
	raw, err := json.Marshal(tool.OutputSchema)
	if err != nil {
		h.t.Fatal(err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		h.t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		h.t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(structured, &v); err != nil {
		h.t.Fatal(err)
	}
	if err := resolved.Validate(v); err != nil {
		h.t.Fatalf("%s: structuredContent does not match its outputSchema: %v", name, err)
	}
}

func mcpResultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) != 1 {
		t.Fatalf("want exactly one content block, got %d", len(res.Content))
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content block is %T, want text", res.Content[0])
	}
	return tc.Text
}

// mcpData decodes an envelope's data.
func mcpData[T any](t *testing.T, env mcpEnvelope) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(env.Data, &v); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	return v
}

// mcpPaths lists method and path of each logged request, for readable
// assertions on what a call read.
func mcpPaths(log []mcpRequest) []string {
	out := make([]string, 0, len(log))
	for _, r := range log {
		p := r.Method + " " + r.Path
		if r.RawQuery != "" {
			p += "?" + r.RawQuery
		}
		out = append(out, p)
	}
	return out
}
