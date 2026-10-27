package cmd

import (
	"testing"

	"github.com/spf13/cobra"
)

// Every flag completion autocomplete.go registers is in place once the
// package's init functions have run. Registration returns an error for a flag
// that does not exist yet, and every one of those errors used to be dropped,
// so a completion could vanish with no sign of it.
func TestFlagCompletionsRegistered(t *testing.T) {
	for _, tt := range []struct {
		cmd  *cobra.Command
		flag string
	}{
		{testCreateCmd, "type"},
		{testCreateCmd, "backend"},
		{testCreateCmd, "compression"},
		{testListCmd, "status"},
		{testListCmd, "type"},
		{testExportCmd, "format"},
		{badgeCmd, "metric"},
		{badgeCmd, "type"},
		{watchEventsCmd, "type"},
		{kafkaConsumeCmd, "offset"},
		{costEstimateCmd, "cloud"},
	} {
		if _, ok := tt.cmd.GetFlagCompletionFunc(tt.flag); !ok {
			t.Errorf("%s --%s has no completion", tt.cmd.CommandPath(), tt.flag)
		}
	}
}
