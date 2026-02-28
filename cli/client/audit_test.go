package client

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestAudit(t *testing.T) {
	c, srv := testServer(t, jsonHandler(t, "GET", "/api/audit", []AuditEntry{
		{ID: 1, Action: "CREATE", EventType: "test", Target: "abc123", Details: "LOAD test", Timestamp: "2025-01-15T10:30:00Z"},
		{ID: 2, Action: "DELETE", EventType: "topic", Target: "my-topic", Details: "Topic deleted", Timestamp: "2025-01-15T10:31:00Z"},
	}))
	defer srv.Close()

	events, err := c.Audit(context.Background(), 50, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Errorf("expected 2 events, got %d", len(events))
	}
	if events[0].Action != "CREATE" {
		t.Errorf("expected CREATE, got %s", events[0].Action)
	}
	if events[1].EventType != "topic" {
		t.Errorf("expected topic, got %s", events[1].EventType)
	}
}

func TestAudit_WithFilter(t *testing.T) {
	c, srv := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if !strings.Contains(r.URL.String(), "type=test") {
			t.Error("expected type=test in query params")
		}
		if !strings.Contains(r.URL.String(), "since=2025-01-01") {
			t.Error("expected since param in query")
		}
		if !strings.Contains(r.URL.String(), "limit=10") {
			t.Error("expected limit=10 in query")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	})
	defer srv.Close()

	events, err := c.Audit(context.Background(), 10, "test", "2025-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

func TestAudit_Empty(t *testing.T) {
	c, srv := testServer(t, jsonHandler(t, "GET", "/api/audit", []AuditEntry{}))
	defer srv.Close()

	events, err := c.Audit(context.Background(), 50, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}

func TestAudit_Error(t *testing.T) {
	c := New("http://127.0.0.1:1")
	c.MaxRetries = 1
	_, err := c.Audit(context.Background(), 50, "", "")
	if err == nil {
		t.Error("expected error for unreachable server")
	}
}
