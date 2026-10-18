package cmd

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bmscomp/kates/cli/client"
	"github.com/spf13/cobra"
)

// At --interval 0 the refresh loops never slept: top sent 20,347 requests in
// 3 s to a stub API.
func TestRefreshCommandsRefuseIntervalUnderOneSecond(t *testing.T) {
	origClient, origTop, origDash, origWatch := apiClient, topInterval, dashInterval, clusterWatchInterval
	t.Cleanup(func() {
		apiClient, topInterval, dashInterval, clusterWatchInterval = origClient, origTop, origDash, origWatch
	})
	apiClient = client.Failing(errors.New("no API in this test"))

	for _, tc := range []struct {
		name     string
		cmd      *cobra.Command
		interval *int
	}{
		{"top", topCmd, &topInterval},
		{"dashboard", dashboardCmd, &dashInterval},
		{"cluster watch", clusterWatchCmd, &clusterWatchInterval},
	} {
		for _, seconds := range []int{0, -1} {
			*tc.interval = seconds
			done := make(chan error, 1)
			go func() { done <- tc.cmd.RunE(tc.cmd, nil) }()
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), "--interval must be at least 1 second") {
					t.Errorf("%s --interval %d: err = %v, want the interval refused", tc.name, seconds, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("%s --interval %d is still refreshing instead of refusing", tc.name, seconds)
			}
		}
	}

	if err := checkInterval(1); err != nil {
		t.Errorf("checkInterval(1) = %v, want nil", err)
	}
}
