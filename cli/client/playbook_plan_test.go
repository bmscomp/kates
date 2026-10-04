package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// A plan as GET /api/disruptions/playbooks/{name} returns it, trimmed, with a
// field no client struct names: whatever the backend adds must reach the dry
// run too.
const leaderCascadePlan = `{"name":"playbook:leader-cascade","description":"Kill partition leaders",` +
	`"steps":[{"name":"kill-leader-partition-0","faultSpec":{"experimentName":"leader-cascade-p0",` +
	`"disruptionType":"POD_KILL","targetTopic":"__consumer_offsets","targetPartition":0},` +
	`"steadyStateSec":30,"observationWindowSec":60,"requireRecovery":true}],` +
	`"maxAffectedBrokers":2,"autoRollback":true,"notYetInAnyClientStruct":{"x":1}}`

func TestPlaybookPlan(t *testing.T) {
	tests := []struct {
		name        string
		playbook    string
		status      int
		contentType string
		body        string
		wantPath    string // escaped, as it goes on the wire
		wantErr     string
		wantStatus  int // for errors that must stay an *HTTPError
	}{
		{
			name:     "returns the plan as sent",
			playbook: "leader-cascade",
			status:   http.StatusOK,
			body:     leaderCascadePlan,
			wantPath: "/api/disruptions/playbooks/leader-cascade",
		},
		{
			name:     "escapes a name into one path segment",
			playbook: "../security/pentest",
			status:   http.StatusOK,
			body:     `{}`,
			wantPath: "/api/disruptions/playbooks/..%2Fsecurity%2Fpentest",
		},
		{
			name:     "escapes a query and a space",
			playbook: "a b?dryRun=false",
			status:   http.StatusOK,
			body:     `{}`,
			wantPath: "/api/disruptions/playbooks/a%20b%3FdryRun=false",
		},
		{
			// Unlike "", a name of spaces is still one segment: it names no
			// playbook, and the backend says so.
			name:       "a name of spaces stays one segment",
			playbook:   "  ",
			status:     http.StatusNotFound,
			body:       `{"status":404,"error":"Not Found","message":"Playbook not found:   "}`,
			wantPath:   "/api/disruptions/playbooks/%20%20",
			wantErr:    "Playbook not found",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "unknown playbook",
			playbook:   "not-a-playbook",
			status:     http.StatusNotFound,
			body:       `{"status":404,"error":"Not Found","message":"Playbook not found: not-a-playbook"}`,
			wantPath:   "/api/disruptions/playbooks/not-a-playbook",
			wantErr:    "Playbook not found: not-a-playbook",
			wantStatus: http.StatusNotFound,
		},
		{
			name:        "backend without the endpoint",
			playbook:    "leader-cascade",
			status:      http.StatusMethodNotAllowed,
			contentType: "text/plain",
			body:        "",
			wantPath:    "/api/disruptions/playbooks/leader-cascade",
			wantErr:     "this Kates backend cannot return a playbook's plan",
			wantStatus:  http.StatusMethodNotAllowed,
		},
		{
			name:     "a body that is not a plan",
			playbook: "leader-cascade",
			status:   http.StatusOK,
			body:     `[{"name":"leader-cascade","steps":2}]`,
			wantPath: "/api/disruptions/playbooks/leader-cascade",
			wantErr:  "not a disruption plan",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("method = %s, want GET", r.Method)
				}
				if got := r.URL.EscapedPath(); got != tt.wantPath {
					t.Errorf("path = %s, want %s", got, tt.wantPath)
				}
				if r.URL.RawQuery != "" {
					t.Errorf("query = %q, want none: the name leaked out of its path segment", r.URL.RawQuery)
				}
				ct := tt.contentType
				if ct == "" {
					ct = "application/json"
				}
				w.Header().Set("Content-Type", ct)
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			})

			plan, err := c.PlaybookPlan(context.Background(), tt.playbook)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("got plan %s, want error containing %q", plan, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
				}
				var httpErr *HTTPError
				if tt.wantStatus != 0 && (!errors.As(err, &httpErr) || httpErr.StatusCode != tt.wantStatus) {
					t.Errorf("error %q does not carry HTTP status %d", err, tt.wantStatus)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !bytes.Equal(plan, []byte(tt.body)) {
				t.Errorf("plan = %s, want the body as sent: %s", plan, tt.body)
			}
		})
	}
}

// The names escaping cannot keep inside one segment are refused before any
// request: ".." would resolve to GET /api/disruptions, and an empty name to
// the playbook list, and either would come back as a "plan".
func TestPlaybookPlan_RefusesNamesThatAreNotOneSegment(t *testing.T) {
	var requests atomic.Int32
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	})

	for _, name := range []string{"", ".", ".."} {
		if _, err := c.PlaybookPlan(context.Background(), name); !errors.Is(err, ErrInvalidPathSegment) {
			t.Errorf("PlaybookPlan(%q) error = %v, want ErrInvalidPathSegment", name, err)
		}
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("%d requests reached the backend, want none", n)
	}
}

// The plan goes to the dry run exactly as the backend returned it, with
// fields no client struct knows about, which is why PlaybookPlan returns raw
// JSON.
func TestPlaybookPlan_PassesUnchangedToRunDryRun(t *testing.T) {
	var (
		mu     sync.Mutex // written by the handler's goroutine
		posted []byte
	)
	c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/disruptions/playbooks/leader-cascade":
			_, _ = io.WriteString(w, leaderCascadePlan)
		case r.Method == http.MethodPost && r.URL.Path == "/api/disruptions" && r.URL.Query().Get("dryRun") == "true":
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			posted = body
			mu.Unlock()
			_, _ = io.WriteString(w, `{"wouldSucceed":true,"totalBrokers":3,"steps":[{"name":"kill-leader-partition-0",`+
				`"disruptionType":"POD_KILL","resolvedLeaderId":1,"affectedPods":["krafter-broker-1"]}]}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	plan, err := c.PlaybookPlan(context.Background(), "leader-cascade")
	if err != nil {
		t.Fatalf("PlaybookPlan: %v", err)
	}
	result, err := c.RunDryRun(context.Background(), plan)
	if err != nil {
		t.Fatalf("RunDryRun: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	var want, got bytes.Buffer
	if err := json.Compact(&want, []byte(leaderCascadePlan)); err != nil {
		t.Fatal(err)
	}
	if err := json.Compact(&got, posted); err != nil {
		t.Fatalf("dry run received invalid JSON %q: %v", posted, err)
	}
	if got.String() != want.String() {
		t.Errorf("dry run received\n  %s\nwant the plan as the backend returned it\n  %s", got.String(), want.String())
	}
	if !result.WouldSucceed || len(result.Steps) != 1 || result.Steps[0].ResolvedLeaderId == nil {
		t.Errorf("dry-run result not decoded: %+v", result)
	}
}
