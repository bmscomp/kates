package client

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// hostileID reaches /api/security/pentest if the client joins it into a path
// unescaped and the server resolves the dot segments.
const (
	hostileID        = "../security/pentest"
	hostileIDEscaped = "..%2Fsecurity%2Fpentest"
)

// recordedRequest is what the backend saw. RequestURI is the target exactly as
// it came off the wire, before the server decodes anything.
type recordedRequest struct {
	Method     string
	RequestURI string
	Path       string
	Query      url.Values
}

// recordingServer answers every request with a small JSON object and records
// each request in order. The body satisfies the object-shaped results and
// makes PlaybookRun's accept-then-poll finish at once; methods that expect an
// array fail to decode it, which these tests ignore because they check only
// the request.
func recordingServer(t *testing.T) (*Client, func() []recordedRequest) {
	t.Helper()
	var mu sync.Mutex
	var seen []recordedRequest
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, recordedRequest{
			Method:     r.Method,
			RequestURI: r.RequestURI,
			Path:       r.URL.Path,
			Query:      r.URL.Query(),
		})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"abcd1234","status":"COMPLETED"}`))
	})
	return c, func() []recordedRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]recordedRequest(nil), seen...)
	}
}

// clientCall is one client method with its arguments bound.
type clientCall func(ctx context.Context, c *Client) error

// ignore drops a method's result: these tests check the request, not the reply.
func ignore[T any](_ T, err error) error { return err }

// firstRequest runs call against a recording backend and returns the first
// request it sent, failing the test when none arrived.
func firstRequest(t *testing.T, call clientCall) recordedRequest {
	t.Helper()
	c, requests := recordingServer(t)
	_ = call(context.Background(), c)
	seen := requests()
	if len(seen) == 0 {
		t.Fatal("no request reached the backend")
	}
	return seen[0]
}

func TestPathf(t *testing.T) {
	tests := []struct {
		name    string
		segment string
		want    string
		wantErr bool
	}{
		{"plain id", "abcd1234", "/api/tests/abcd1234", false},
		{"traversal stays one segment", hostileID, "/api/tests/" + hostileIDEscaped, false},
		{"reserved characters", "team a/b?c#d%e", "/api/tests/team%20a%2Fb%3Fc%23d%25e", false},
		{"dots inside a name", "orders.v1", "/api/tests/orders.v1", false},
		{"three dots are a name", "...", "/api/tests/...", false},
		{"empty", "", "", true},
		{"dot segment", ".", "", true},
		{"dot-dot segment", "..", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pathf("/api/tests/%s", tt.segment)
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidPathSegment) {
					t.Fatalf("pathf(%q) = %q, %v; want an error wrapping ErrInvalidPathSegment", tt.segment, got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("pathf(%q): %v", tt.segment, err)
			}
			if got != tt.want {
				t.Errorf("pathf(%q) = %q, want %q", tt.segment, got, tt.want)
			}
		})
	}
}

// TestPathSegmentsAreEscaped sends the same hostile id through every client
// method that puts caller input into a path, and checks the request target the
// backend receives: the id must arrive as one escaped segment of the intended
// endpoint.
func TestPathSegmentsAreEscaped(t *testing.T) {
	const id = hostileIDEscaped
	tests := []struct {
		name       string
		wantMethod string
		wantURI    string
		call       clientCall
	}{
		{"TopicDetail", http.MethodGet, "/api/cluster/topics/" + id,
			func(ctx context.Context, c *Client) error { return ignore(c.TopicDetail(ctx, hostileID)) }},
		{"ConsumerGroupDetail", http.MethodGet, "/api/cluster/groups/" + id,
			func(ctx context.Context, c *Client) error { return ignore(c.ConsumerGroupDetail(ctx, hostileID)) }},
		{"GetTest", http.MethodGet, "/api/tests/" + id,
			func(ctx context.Context, c *Client) error { return ignore(c.GetTest(ctx, hostileID)) }},
		{"DeleteTest", http.MethodDelete, "/api/tests/" + id,
			func(ctx context.Context, c *Client) error { return c.DeleteTest(ctx, hostileID) }},
		{"CancelTest", http.MethodPost, "/api/tests/" + id + "/cancel",
			func(ctx context.Context, c *Client) error { return c.CancelTest(ctx, hostileID) }},
		{"Report", http.MethodGet, "/api/tests/" + id + "/report",
			func(ctx context.Context, c *Client) error { return ignore(c.Report(ctx, hostileID)) }},
		{"ReportSummary", http.MethodGet, "/api/tests/" + id + "/report/summary",
			func(ctx context.Context, c *Client) error { return ignore(c.ReportSummary(ctx, hostileID)) }},
		{"ExportCSV", http.MethodGet, "/api/tests/" + id + "/report/csv",
			func(ctx context.Context, c *Client) error { return ignore(c.ExportCSV(ctx, hostileID)) }},
		{"ExportHeatmap", http.MethodGet, "/api/tests/" + id + "/report/heatmap",
			func(ctx context.Context, c *Client) error { return ignore(c.ExportHeatmap(ctx, hostileID, "")) }},
		{"ExportJUnit", http.MethodGet, "/api/tests/" + id + "/report/junit",
			func(ctx context.Context, c *Client) error { return ignore(c.ExportJUnit(ctx, hostileID)) }},
		{"GetSchedule", http.MethodGet, "/api/schedules/" + id,
			func(ctx context.Context, c *Client) error { return ignore(c.GetSchedule(ctx, hostileID)) }},
		{"UpdateSchedule", http.MethodPut, "/api/schedules/" + id,
			func(ctx context.Context, c *Client) error {
				return ignore(c.UpdateSchedule(ctx, hostileID, &CreateScheduleRequest{}))
			}},
		{"DeleteSchedule", http.MethodDelete, "/api/schedules/" + id,
			func(ctx context.Context, c *Client) error { return c.DeleteSchedule(ctx, hostileID) }},
		{"ReportBrokers", http.MethodGet, "/api/tests/" + id + "/report/brokers",
			func(ctx context.Context, c *Client) error { return ignore(c.ReportBrokers(ctx, hostileID)) }},
		{"ReportSnapshot", http.MethodGet, "/api/tests/" + id + "/report/snapshot",
			func(ctx context.Context, c *Client) error { return ignore(c.ReportSnapshot(ctx, hostileID)) }},
		{"PlaybookPlan", http.MethodGet, "/api/disruptions/playbooks/" + id,
			func(ctx context.Context, c *Client) error { return ignore(c.PlaybookPlan(ctx, hostileID)) }},
		{"DisruptionStatus", http.MethodGet, "/api/disruptions/" + id,
			func(ctx context.Context, c *Client) error { return ignore(c.DisruptionStatus(ctx, hostileID)) }},
		{"DisruptionTimelineData", http.MethodGet, "/api/disruptions/" + id + "/timeline",
			func(ctx context.Context, c *Client) error { return ignore(c.DisruptionTimelineData(ctx, hostileID)) }},
		{"DisruptionKafkaMetrics", http.MethodGet, "/api/disruptions/" + id + "/kafka-metrics",
			func(ctx context.Context, c *Client) error { return ignore(c.DisruptionKafkaMetrics(ctx, hostileID)) }},
		{"PlaybookRun", http.MethodPost, "/api/disruptions/playbooks/" + id,
			func(ctx context.Context, c *Client) error { return ignore(c.PlaybookRun(ctx, hostileID)) }},
		{"DisruptionScheduleDelete", http.MethodDelete, "/api/disruptions/schedules/" + id,
			func(ctx context.Context, c *Client) error { return c.DisruptionScheduleDelete(ctx, hostileID) }},
		{"DeleteWebhook", http.MethodDelete, "/api/webhooks/" + id,
			func(ctx context.Context, c *Client) error { return c.DeleteWebhook(ctx, hostileID) }},
		{"KafkaTopicDetail", http.MethodGet, "/api/kafka/topics/" + id,
			func(ctx context.Context, c *Client) error { return ignore(c.KafkaTopicDetail(ctx, hostileID)) }},
		{"KafkaGroupDetail", http.MethodGet, "/api/kafka/groups/" + id,
			func(ctx context.Context, c *Client) error { return ignore(c.KafkaGroupDetail(ctx, hostileID)) }},
		{"KafkaConsume", http.MethodGet, "/api/kafka/consume/" + id + "?limit=5&offset=earliest",
			func(ctx context.Context, c *Client) error {
				return ignore(c.KafkaConsume(ctx, hostileID, "earliest", 5))
			}},
		{"KafkaProduce", http.MethodPost, "/api/kafka/produce/" + id,
			func(ctx context.Context, c *Client) error { return ignore(c.KafkaProduce(ctx, hostileID, "k", "v")) }},
		{"KafkaAlterTopic", http.MethodPatch, "/api/kafka/topics/" + id,
			func(ctx context.Context, c *Client) error {
				return ignore(c.KafkaAlterTopic(ctx, hostileID, &AlterTopicRequest{}))
			}},
		{"KafkaDeleteTopic", http.MethodDelete, "/api/kafka/topics/" + id,
			func(ctx context.Context, c *Client) error { return c.KafkaDeleteTopic(ctx, hostileID) }},
		{"BaselineSet", http.MethodPut, "/api/tests/baselines/" + id,
			func(ctx context.Context, c *Client) error { return ignore(c.BaselineSet(ctx, hostileID, "run1")) }},
		{"BaselineUnset", http.MethodDelete, "/api/tests/baselines/" + id,
			func(ctx context.Context, c *Client) error { return c.BaselineUnset(ctx, hostileID) }},
		{"BaselineGet", http.MethodGet, "/api/tests/baselines/" + id,
			func(ctx context.Context, c *Client) error { return ignore(c.BaselineGet(ctx, hostileID)) }},
		{"ReportRegression", http.MethodGet, "/api/tests/" + id + "/report/regression",
			func(ctx context.Context, c *Client) error { return ignore(c.ReportRegression(ctx, hostileID)) }},
		{"ReportTuning", http.MethodGet, "/api/tests/" + id + "/report/tuning",
			func(ctx context.Context, c *Client) error { return ignore(c.ReportTuning(ctx, hostileID)) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := firstRequest(t, tt.call)
			if got.Method != tt.wantMethod {
				t.Errorf("method = %s, want %s", got.Method, tt.wantMethod)
			}
			if got.RequestURI != tt.wantURI {
				t.Errorf("request URI = %q, want %q", got.RequestURI, tt.wantURI)
			}
			// Decoded, the id is still whole: one segment of the intended endpoint.
			if !strings.Contains(got.Path, "/"+hostileID) {
				t.Errorf("decoded path = %q, want it to carry %q whole", got.Path, hostileID)
			}
		})
	}
}

// TestPathSegmentsRefuseDotAndEmpty checks that the values escaping cannot
// neutralise never leave the client, for a read and for a destructive call.
func TestPathSegmentsRefuseDotAndEmpty(t *testing.T) {
	for _, id := range []string{"", ".", ".."} {
		t.Run("id "+strconv.Quote(id), func(t *testing.T) {
			c, requests := recordingServer(t)
			ctx := context.Background()
			if _, err := c.GetTest(ctx, id); !errors.Is(err, ErrInvalidPathSegment) {
				t.Errorf("GetTest: err = %v, want ErrInvalidPathSegment", err)
			}
			if err := c.DeleteTest(ctx, id); !errors.Is(err, ErrInvalidPathSegment) {
				t.Errorf("DeleteTest: err = %v, want ErrInvalidPathSegment", err)
			}
			if _, err := c.DisruptionStreamURL(id); !errors.Is(err, ErrInvalidPathSegment) {
				t.Errorf("DisruptionStreamURL: err = %v, want ErrInvalidPathSegment", err)
			}
			if seen := requests(); len(seen) != 0 {
				t.Errorf("requests sent = %+v, want none", seen)
			}
		})
	}
}

func TestDisruptionStreamURL(t *testing.T) {
	c := New("http://kates.example:8080/")
	got, err := c.DisruptionStreamURL(hostileID)
	if err != nil {
		t.Fatal(err)
	}
	want := "http://kates.example:8080/api/disruptions/" + hostileIDEscaped + "/stream"
	if got != want {
		t.Errorf("DisruptionStreamURL = %q, want %q", got, want)
	}
}

// TestQueryValuesAreEscaped sends values carrying "&", "=", "#" and "+"
// through every method that builds a query string, and checks the backend
// decodes exactly the parameters the method meant to send: each value intact,
// nothing injected.
func TestQueryValuesAreEscaped(t *testing.T) {
	const (
		injected  = "LOAD&status=DONE#frag"
		timestamp = "2026-09-25T10:00:00+02:00" // "+" sent unescaped reads back as a space
	)
	tests := []struct {
		name      string
		wantPath  string
		wantQuery url.Values
		call      clientCall
	}{
		{"ListTests", "/api/tests",
			url.Values{"page": {"2"}, "size": {"10"}, "type": {injected}, "status": {"RUNNING"}},
			func(ctx context.Context, c *Client) error {
				return ignore(c.ListTests(ctx, injected, "RUNNING", 2, 10))
			}},
		{"ListTests without filters", "/api/tests",
			url.Values{"page": {"0"}, "size": {"20"}},
			func(ctx context.Context, c *Client) error { return ignore(c.ListTests(ctx, "", "", 0, 20)) }},
		{"Compare", "/api/tests/reports/compare",
			url.Values{"ids": {"a,b&ids=c"}},
			func(ctx context.Context, c *Client) error { return ignore(c.Compare(ctx, "a,b&ids=c")) }},
		{"ExportHeatmap", "/api/tests/run1/report/heatmap",
			url.Values{"format": {"csv&format=json"}},
			func(ctx context.Context, c *Client) error {
				return ignore(c.ExportHeatmap(ctx, "run1", "csv&format=json"))
			}},
		{"Trends", "/api/trends",
			url.Values{"type": {injected}, "metric": {"p99 latency"}, "days": {"7"}, "baselineWindow": {"5"}, "phase": {"phase #1"}},
			func(ctx context.Context, c *Client) error {
				return ignore(c.Trends(ctx, injected, "p99 latency", 7, 5, "phase #1"))
			}},
		{"Trends keeps empty type and metric", "/api/trends",
			url.Values{"type": {""}, "metric": {""}, "days": {"30"}, "baselineWindow": {"5"}},
			func(ctx context.Context, c *Client) error { return ignore(c.Trends(ctx, "", "", 30, 5, "")) }},
		{"TrendPhases", "/api/trends/phases",
			url.Values{"type": {injected}, "days": {"7"}},
			func(ctx context.Context, c *Client) error { return ignore(c.TrendPhases(ctx, injected, 7)) }},
		{"TrendBreakdown", "/api/trends/breakdown",
			url.Values{"type": {injected}, "metric": {"m&x=1"}, "days": {"7"}, "baselineWindow": {"5"}},
			func(ctx context.Context, c *Client) error {
				return ignore(c.TrendBreakdown(ctx, injected, "m&x=1", 7, 5))
			}},
		{"BrokerTrend", "/api/trends/broker",
			url.Values{"type": {injected}, "metric": {"m"}, "brokerId": {"2"}, "days": {"7"}, "baselineWindow": {"5"}},
			func(ctx context.Context, c *Client) error { return ignore(c.BrokerTrend(ctx, injected, "m", 2, 7, 5)) }},
		{"RunDryRun", "/api/disruptions",
			url.Values{"dryRun": {"true"}},
			func(ctx context.Context, c *Client) error { return ignore(c.RunDryRun(ctx, map[string]string{})) }},
		{"DisruptionList", "/api/disruptions",
			url.Values{"limit": {"20"}},
			func(ctx context.Context, c *Client) error { return ignore(c.DisruptionList(ctx, 20)) }},
		{"KafkaConsume", "/api/kafka/consume/orders",
			url.Values{"offset": {"earliest&limit=100000"}, "limit": {"5"}},
			func(ctx context.Context, c *Client) error {
				return ignore(c.KafkaConsume(ctx, "orders", "earliest&limit=100000", 5))
			}},
		{"Audit", "/api/audit",
			url.Values{"page": {"0"}, "size": {"10"}, "type": {"test&size=1000"}, "since": {timestamp}},
			func(ctx context.Context, c *Client) error {
				return ignore(c.Audit(ctx, 10, "test&size=1000", timestamp))
			}},
		{"SecurityAuthTest", "/api/security/auth-test",
			url.Values{"user": {"alice&user=admin"}},
			func(ctx context.Context, c *Client) error { return ignore(c.SecurityAuthTest(ctx, "alice&user=admin")) }},
		{"SecurityPentest", "/api/security/pentest",
			url.Values{"test": {"all&test=x"}},
			func(ctx context.Context, c *Client) error { return ignore(c.SecurityPentest(ctx, "all&test=x")) }},
		{"SecurityGate", "/api/security/gate",
			url.Values{"min-grade": {"B+"}},
			func(ctx context.Context, c *Client) error { return ignore(c.SecurityGate(ctx, "B+")) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := firstRequest(t, tt.call)
			if got.Path != tt.wantPath {
				t.Errorf("path = %q, want %q", got.Path, tt.wantPath)
			}
			if !reflect.DeepEqual(got.Query, tt.wantQuery) {
				t.Errorf("query = %v, want %v (request URI %q)", got.Query, tt.wantQuery, got.RequestURI)
			}
		})
	}
}

// TestRequestPathsAreNotBuiltByHand keeps pathf and withQuery the only ways
// the client puts caller input into a request: it fails when client code joins
// an /api/ path with "+", leaves one open-ended for a segment to be appended,
// or passes one to fmt.Sprintf with a verb other than %d. The tables above
// cover the methods that exist; this covers the ones added later.
func TestRequestPathsAreNotBuiltByHand(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, found := range handBuiltPaths(fset, f) {
			t.Errorf("%s builds a request path by hand; use pathf for path segments and withQuery for query values", found)
		}
	}
}

// TestHandBuiltPaths checks the check above against code it must reject and
// code it must accept.
func TestHandBuiltPaths(t *testing.T) {
	const src = `package client

func bad(c *Client, id, name string, n int) {
	_ = "/api/tests/" + id
	_ = "/api/tests/" + id + "/report"
	_ = c.BaseURL + "/api/tests?type=" + name
	_ = fmt.Sprintf("/api/tests/%s/report", id)
	_ = fmt.Sprintf("/api/kafka/topics/%v", name)
	_ = fmt.Sprintf("/api/tests?type=%q", name)
	p := "/api/tests/"
	p += id
}

func good(c *Client, id string, n int) {
	_, _ = pathf("/api/tests/%s/report", id)
	_ = withQuery("/api/tests", url.Values{"type": {id}})
	_ = fmt.Sprintf("/api/cluster/brokers/%d/configs", n)
	_ = c.BaseURL + path
	_ = "/api/health"
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "snippet.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var lines []int
	for _, found := range handBuiltPaths(fset, f) {
		_, pos, _ := strings.Cut(found, ":")
		line, _, _ := strings.Cut(pos, ":")
		n, _ := strconv.Atoi(line)
		lines = append(lines, n)
	}
	// Lines 4-9 each build a path by hand, and line 10 leaves one open.
	if want := []int{4, 5, 6, 7, 8, 9, 10}; !reflect.DeepEqual(lines, want) {
		t.Errorf("flagged lines %v, want %v", lines, want)
	}
}

// formatVerb matches one fmt verb, with any flags, width and precision.
var formatVerb = regexp.MustCompile(`%[-+# 0]*[0-9*]*(?:\.[0-9*]*)?([a-zA-Z])`)

// handBuiltPaths returns the position of every expression in f that builds an
// /api/ request path other than through pathf or withQuery.
func handBuiltPaths(fset *token.FileSet, f *ast.File) []string {
	var found []string
	flag := func(n ast.Node) { found = append(found, fset.Position(n.Pos()).String()) }
	ast.Inspect(f, func(n ast.Node) bool {
		switch e := n.(type) {
		case *ast.BinaryExpr:
			_, left := apiPathLiteral(e.X)
			_, right := apiPathLiteral(e.Y)
			if e.Op == token.ADD && (left || right) {
				flag(e)
				return false // one report for a chain of "+"
			}
		case *ast.BasicLit:
			// "/api/tests/" is waiting for a segment to be appended.
			if path, ok := apiPathLiteral(e); ok && strings.HasSuffix(path, "/") {
				flag(e)
			}
		case *ast.CallExpr:
			sel, ok := e.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Sprintf" || len(e.Args) == 0 {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "fmt" {
				return true
			}
			format, ok := apiPathLiteral(e.Args[0])
			if !ok {
				return true
			}
			for _, m := range formatVerb.FindAllStringSubmatch(format, -1) {
				if m[1] != "d" {
					flag(e)
					break
				}
			}
		}
		return true
	})
	return found
}

// apiPathLiteral returns the value of a string literal that starts "/api/".
func apiPathLiteral(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil || !strings.HasPrefix(value, "/api/") {
		return "", false
	}
	return value, true
}
