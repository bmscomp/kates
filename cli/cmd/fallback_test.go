package cmd

import (
	"net"
	"testing"
)

func TestResolveFallbackURL(t *testing.T) {
	tests := []struct {
		name     string
		inputURL string
		ports    map[string]bool // address -> reachable
		wantURL  string
	}{
		{
			name:     "default 8080 active, stays 8080",
			inputURL: "http://localhost:8080",
			ports: map[string]bool{
				"127.0.0.1:8080":  true,
				"127.0.0.1:30083": false,
			},
			wantURL: "http://localhost:8080",
		},
		{
			name:     "default 8080 closed, 30083 active, falls back to 30083",
			inputURL: "http://localhost:8080",
			ports: map[string]bool{
				"127.0.0.1:8080":  false,
				"127.0.0.1:30083": true,
			},
			wantURL: "http://localhost:30083",
		},
		{
			name:     "default 8080 closed, 30083 closed, stays 8080",
			inputURL: "http://localhost:8080",
			ports: map[string]bool{
				"127.0.0.1:8080":  false,
				"127.0.0.1:30083": false,
			},
			wantURL: "http://localhost:8080",
		},
		{
			name:     "kind 30083 active, stays 30083",
			inputURL: "http://127.0.0.1:30083",
			ports: map[string]bool{
				"127.0.0.1:8080":  false,
				"127.0.0.1:30083": true,
			},
			wantURL: "http://127.0.0.1:30083",
		},
		{
			name:     "kind 30083 closed, 8080 active, falls back to 8080",
			inputURL: "http://localhost:30083",
			ports: map[string]bool{
				"127.0.0.1:8080":  true,
				"127.0.0.1:30083": false,
			},
			wantURL: "http://localhost:8080",
		},
		{
			name:     "remote address, stays unchanged",
			inputURL: "http://kates.internal:8080",
			ports: map[string]bool{
				"127.0.0.1:8080":  false,
				"127.0.0.1:30083": true,
			},
			wantURL: "http://kates.internal:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkPortMock := func(addr string) bool {
				return tt.ports[addr]
			}
			got := resolveFallbackURL(tt.inputURL, checkPortMock)
			if got != tt.wantURL {
				t.Errorf("resolveFallbackURL(%q) = %q, want %q", tt.inputURL, got, tt.wantURL)
			}
		})
	}
}

func TestIsPortReachable_Closed(t *testing.T) {
	closed := isPortReachable("127.0.0.1:64531")
	if closed {
		t.Error("expected port 64531 to be closed/unreachable")
	}
}

func TestIsPortReachable_Open(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on local port: %v", err)
	}
	defer l.Close()

	if !isPortReachable(l.Addr().String()) {
		t.Errorf("expected port %s to be reachable", l.Addr().String())
	}
}
