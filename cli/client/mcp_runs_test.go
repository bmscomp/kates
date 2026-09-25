package client

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

// The kates mcp run reads put ids in the path escaped and filters in the
// query through url.Values, like every other method.
func TestMCPRunRequests(t *testing.T) {
	tests := []struct {
		name string
		call clientCall
		want string // RequestURI as sent
	}{
		{"run", func(ctx context.Context, c *Client) error { return ignore(c.MCPRun(ctx, hostileID)) },
			"/api/tests/" + hostileIDEscaped},
		{"runs page", func(ctx context.Context, c *Client) error {
			return ignore(c.MCPRunsPage(ctx, "LOAD&status=DONE", "", 2, 25))
		}, "/api/tests?page=2&size=25&type=LOAD%26status%3DDONE"},
		{"runs page by status", func(ctx context.Context, c *Client) error {
			return ignore(c.MCPRunsPage(ctx, "", "FAILED", 0, 10))
		}, "/api/tests?page=0&size=10&status=FAILED"},
		{"summary", func(ctx context.Context, c *Client) error { return ignore(c.MCPRunSummary(ctx, hostileID)) },
			"/api/tests/" + hostileIDEscaped + "/report/summary"},
		{"markdown", func(ctx context.Context, c *Client) error { return ignore(c.MCPRunReportMarkdown(ctx, hostileID)) },
			"/api/tests/" + hostileIDEscaped + "/report/markdown"},
		{"advisor", func(ctx context.Context, c *Client) error { return ignore(c.MCPRunAdvisor(ctx, hostileID)) },
			"/api/tests/" + hostileIDEscaped + "/advisor"},
		{"compare", func(ctx context.Context, c *Client) error {
			return ignore(c.MCPRunsCompare(ctx, []string{"00000001", "0a1b2c3d"}))
		}, "/api/tests/reports/compare?ids=00000001%2C0a1b2c3d"},
		{"disruptions", func(ctx context.Context, c *Client) error { return ignore(c.MCPActivityDisruptions(ctx, 1, 50)) },
			"/api/disruptions?page=1&size=50"},
		{"audit", func(ctx context.Context, c *Client) error {
			return ignore(c.MCPActivityAudit(ctx, "2026-09-25T11:00:00+02:00", 0, 15))
		}, "/api/audit?page=0&since=2026-09-25T11%3A00%3A00%2B02%3A00&size=15"},
		{"audit without since", func(ctx context.Context, c *Client) error { return ignore(c.MCPActivityAudit(ctx, "", 0, 15)) },
			"/api/audit?page=0&size=15"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := firstRequest(t, tt.call)
			if got.Method != http.MethodGet || got.RequestURI != tt.want {
				t.Errorf("sent %s %s, want GET %s", got.Method, got.RequestURI, tt.want)
			}
		})
	}
}

// An id that would change which runs a compare names is refused before
// anything is sent, and so is a path segment escaping cannot make safe.
func TestMCPRunRefusals(t *testing.T) {
	c, requests := recordingServer(t)
	ctx := context.Background()
	for _, ids := range [][]string{{"00000001"}, {"00000001", ""}, {"00000001", "a,b"}, {"00000001", "a b"}} {
		if _, err := c.MCPRunsCompare(ctx, ids); err == nil {
			t.Errorf("compare %q was sent", ids)
		}
	}
	for _, id := range []string{"", ".", ".."} {
		if _, err := c.MCPRun(ctx, id); !errors.Is(err, ErrInvalidPathSegment) {
			t.Errorf("run %q: %v", id, err)
		}
		if _, err := c.MCPRunReportMarkdown(ctx, id); !errors.Is(err, ErrInvalidPathSegment) {
			t.Errorf("markdown %q: %v", id, err)
		}
	}
	if got := requests(); len(got) != 0 {
		t.Errorf("refused calls reached the backend: %+v", got)
	}
}

func TestMCPRunReportMarkdownReturnsTheBody(t *testing.T) {
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte("# Test Report\n"))
	})
	md, err := c.MCPRunReportMarkdown(context.Background(), "0a1b2c3d")
	if err != nil || md != "# Test Report\n" {
		t.Errorf("got %q, %v", md, err)
	}
}
