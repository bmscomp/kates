package cmd

import (
	"testing"
	"time"
)

func TestParseTimeoutFlag(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want time.Duration
	}{
		{"30m", 30 * time.Minute}, {"90", 90 * time.Second}, {"1h30m", 90 * time.Minute},
	} {
		if got, err := parseTimeoutFlag(tc.in); err != nil || got != tc.want {
			t.Errorf("parseTimeoutFlag(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []string{"", "0", "-5m", "soon"} {
		if _, err := parseTimeoutFlag(bad); err == nil {
			t.Errorf("parseTimeoutFlag(%q): no error", bad)
		}
	}
}
