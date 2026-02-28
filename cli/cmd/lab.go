package cmd

import (
	"github.com/klster/kates-cli/tui"
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
		return tui.RunLab(apiClient)
	},
}

func init() {
	rootCmd.AddCommand(labCmd)
}
