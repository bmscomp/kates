package cmd

import (
	"github.com/bmscomp/kates/cli/tui"
	"github.com/spf13/cobra"
)

var labCmd = &cobra.Command{
	Use:   "lab",
	Short: "Interactive performance tuning laboratory",
	Long: `Launch a full-screen interactive environment for iterative
performance tuning. Adjust parameters (producers, compression,
batch size, etc.) and run quick test iterations to find the
optimal configuration.

Each iteration creates a real test run and records the results.
Use ←/→ to change values, Enter to run, and d to diff results.`,
	Example: `  kates lab`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Unguarded, this crashed with bubbletea's raw "could not open a new
		// TTY" when run piped or in CI — a library internals error where the
		// user needed an explanation.
		if !IsInteractive() {
			return cmdErr("kates lab is a full-screen TUI and needs a terminal.\n" +
				"  For scripted runs use: kates test create --wait  (and kates report)")
		}
		return tui.RunLab(apiClient, apiURL)
	},
}

func init() {
	rootCmd.AddCommand(labCmd)
}
