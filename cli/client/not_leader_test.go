package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
)

func notLeader(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = io.WriteString(w, `{"status":503,"error":"Service Unavailable","message":"`+message+`"}`)
}

const refusedByFollower = "This Kates member does not lead its quorum: kates-0 does, so send it the request"

func TestNotLeaderFollowsAndResendsOnce(t *testing.T) {
	tests := []struct {
		name      string
		refusals  int    // how many requests the server refuses before answering
		message   string // the refusal's message
		hookErr   error
		wantCalls int32
		wantSent  int32
		wantErr   bool
	}{
		{"leader named: follow, resend", 1, refusedByFollower, nil, 1, 2, false},
		{"no leader known: nothing to follow", 1, "This Kates member does not lead its quorum, and no leader is known yet: retry later", nil, 0, 1, true},
		{"follow failed: refusal reported", 1, refusedByFollower, errors.New("no forward"), 1, 1, true},
		{"refused again: follow only once", 2, refusedByFollower, nil, 1, 2, true},
		{"drain refusal: not a leader matter", 1, "Kates is draining: retry later", nil, 0, 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sent atomic.Int32
			c, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if string(body) != `{"type":"LOAD"}` {
					t.Errorf("request %d body = %q", sent.Load()+1, body)
				}
				if int(sent.Add(1)) <= tt.refusals {
					notLeader(w, tt.message)
					return
				}
				_, _ = io.WriteString(w, `{"ok":true}`)
			})
			var calls atomic.Int32
			c.OnNotLeader = func(context.Context) error { calls.Add(1); return tt.hookErr }

			_, err := postJSON[map[string]bool](c, context.Background(), "/api/tests", map[string]string{"type": "LOAD"})
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if calls.Load() != tt.wantCalls || sent.Load() != tt.wantSent {
				t.Errorf("OnNotLeader calls = %d, requests sent = %d; want %d, %d", calls.Load(), sent.Load(), tt.wantCalls, tt.wantSent)
			}
		})
	}
}
