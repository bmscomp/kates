package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var cleanupDryRun bool

var testCleanupCmd = &cobra.Command{
	Use:     "cleanup",
	Aliases: []string{"gc", "prune"},
	Short:   "Delete orphaned tests stuck in RUNNING state",
	Example: `  kates test cleanup
  kates test cleanup --dry-run`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		paged, err := apiClient.ListTests(ctx, "", "RUNNING", 0, 100)
		if err != nil {
			return cmdErr("Failed to list tests: " + err.Error())
		}

		if len(paged.Content) == 0 {
			output.Success("No orphaned tests found — cluster is clean.")
			return nil
		}

		now := time.Now().UTC()
		staleThreshold := 5 * time.Minute
		var stale []string

		for _, run := range paged.Content {
			created, err := time.Parse(time.RFC3339Nano, run.CreatedAt)
			if err != nil {
				created, err = time.Parse("2006-01-02T15:04:05", run.CreatedAt[:19])
			}
			if err != nil {
				stale = append(stale, run.ID)
				continue
			}
			if now.Sub(created) > staleThreshold {
				stale = append(stale, run.ID)
			}
		}

		if len(stale) == 0 {
			output.Success(fmt.Sprintf("Found %d RUNNING tests, none are stale (>%s).", len(paged.Content), staleThreshold))
			return nil
		}

		output.SubHeader(fmt.Sprintf("Found %d orphaned tests", len(stale)))

		if cleanupDryRun {
			for _, id := range stale {
				output.Hint(fmt.Sprintf("  Would delete: %s", truncID(id)))
			}
			output.Hint("Run without --dry-run to delete them.")
			return nil
		}

		deleted := 0
		for _, id := range stale {
			if err := apiClient.DeleteTest(ctx, id); err != nil {
				output.Warn(fmt.Sprintf("  Failed to delete %s: %s", truncID(id), err.Error()))
			} else {
				output.Success(fmt.Sprintf("  Deleted %s", truncID(id)))
				deleted++
			}
		}

		fmt.Println()
		output.Success(fmt.Sprintf("Cleaned up %d/%d orphaned tests.", deleted, len(stale)))
		return nil
	},
}

func init() {
	testCleanupCmd.Flags().BoolVar(&cleanupDryRun, "dry-run", false, "Preview what would be deleted without actually deleting")
	testCmd.AddCommand(testCleanupCmd)
}
