package cmd

import (
	"fmt"
	"time"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
)

var (
	cleanupDryRun    bool
	cleanupYes       bool
	cleanupOlderThan time.Duration
)

// defaultCleanupOlderThan is how far past its planned end a RUNNING run must
// be before cleanup calls it orphaned, which leaves the Kates API's timeout
// reaper room to fail it first. A current API fails a run five minutes
// (kates.engine.reaper-grace-ms) after the duration it is set to last, counted
// from its creation, and an older one every run 30 minutes after its creation
// (kates.engine.max-duration-ms as it then shipped). The threshold used to be
// 5 minutes from the run's start, so cleanup deleted a healthy 10-minute
// ENDURANCE run halfway through, with its results.
const defaultCleanupOlderThan = 30 * time.Minute

var testCleanupCmd = &cobra.Command{
	Use: "cleanup",
	// prune was an alias too, until kates test prune, which deletes finished
	// runs by age, took the name.
	Aliases: []string{"gc"},
	Short:   "Delete orphaned tests stuck in RUNNING state",
	Long: `Delete test runs that still say RUNNING long after they should have ended.

A run is orphaned when it is RUNNING more than --older-than after its planned
end: its start plus the duration in its spec, twice that for INTEGRITY, which
reads its records back for up to as long again. A run of a phased scenario sent
to POST /api/tests shows none of its phases in its spec, and they run one after
another, so its planned end is its start plus two hours, the longest the Kates
API lets one last as Kates ships it (kates.engine.max-duration-ms). A run inside
that window is left alone, however long it has been running.

kates lists the runs it would delete and asks before deleting them. Deleting a
run stops it and removes it with its results; kates test cancel stops a run and
keeps it. Without a terminal, pass --yes.`,
	Example: `  kates test cleanup --dry-run
  kates test cleanup
  kates test cleanup --older-than 2h --yes`,
	RunE: runTestCleanup,
}

func runTestCleanup(cmd *cobra.Command, args []string) error {
	if cleanupOlderThan <= 0 {
		return cmdErr("--older-than must be more than 0")
	}
	ctx := commandContext(cmd)
	paged, err := apiClient.ListTests(ctx, "", "RUNNING", 0, 100)
	if err != nil {
		return cmdErr("Failed to list tests: " + err.Error())
	}

	if len(paged.Content) == 0 {
		output.Success("No orphaned tests found — cluster is clean.")
		return nil
	}

	orphans, unreadable := findOrphanedRuns(paged.Content, time.Now(), cleanupOlderThan)
	for _, run := range unreadable {
		// Unknown age is not proven staleness. This used to add the run to
		// the DELETE list — destroying data because we could not read its
		// timestamp.
		output.Warn("Skipping " + truncID(run.ID) + ": unparseable createdAt " + fmt.Sprintf("%q", run.CreatedAt))
	}

	if len(orphans) == 0 {
		output.Success(fmt.Sprintf("Found %d RUNNING tests, none more than %s past its planned end.",
			len(paged.Content), shortDuration(cleanupOlderThan)))
		return nil
	}

	output.SubHeader("Found " + plural(len(orphans), "orphaned test", "orphaned tests"))
	rows := make([][]string, 0, len(orphans))
	for _, o := range orphans {
		rows = append(rows, []string{
			truncID(o.Run.ID), o.Run.TestType, o.Started.Local().Format("2006-01-02 15:04"),
			shortDuration(o.Planned), shortDuration(o.Overdue),
		})
	}
	output.Table([]string{"ID", "Type", "Started", "Planned", "Overdue"}, rows)

	if cleanupDryRun {
		output.Hint("Dry run: nothing deleted. Run without --dry-run to delete them.")
		return nil
	}

	if !cleanupYes {
		question := fmt.Sprintf("Stop and delete these %d runs and their results?", len(orphans))
		if len(orphans) == 1 {
			question = "Stop and delete this run and its results?"
		}
		ok, err := confirm(question)
		if err != nil {
			return cmdErr("aborted: " + err.Error())
		}
		if !ok {
			// A declined destructive action exits non-zero so scripts that
			// forgot --yes fail loudly instead of reporting success.
			return cmdErr("aborted: no test deleted")
		}
	}

	deleted := 0
	for _, o := range orphans {
		if err := apiClient.DeleteTest(ctx, o.Run.ID); err != nil {
			output.Warn(fmt.Sprintf("  Failed to delete %s: %s", truncID(o.Run.ID), err.Error()))
		} else {
			output.Success(fmt.Sprintf("  Deleted %s", truncID(o.Run.ID)))
			deleted++
		}
	}

	fmt.Println()
	if deleted < len(orphans) {
		return cmdErr(fmt.Sprintf("Cleaned up %d/%d orphaned tests.", deleted, len(orphans)))
	}
	output.Success(fmt.Sprintf("Cleaned up %d/%d orphaned tests.", deleted, len(orphans)))
	return nil
}

// orphanedRun is a RUNNING run past its planned end by more than the
// threshold.
type orphanedRun struct {
	Run     client.TestRun
	Started time.Time
	Planned time.Duration // the spec's duration, twice that for INTEGRITY, the cap for a scenario; 0 when it has none
	Overdue time.Duration // how long past its planned end it still runs
}

// findOrphanedRuns returns the runs that are more than olderThan past their
// planned end, and those whose createdAt cannot be read, which it never
// counts as orphaned.
func findOrphanedRuns(runs []client.TestRun, now time.Time, olderThan time.Duration) (orphans []orphanedRun, unreadable []client.TestRun) {
	for _, run := range runs {
		started, err := parseCreatedAt(run.CreatedAt)
		if err != nil {
			unreadable = append(unreadable, run)
			continue
		}
		var planned time.Duration
		if run.ScenarioName != "" {
			// A scenario's phases run one after another, so it lasts their
			// durations added up, and its spec is the base spec, which shows
			// none of them. Judged by that spec, a scenario that outlasted it
			// by --older-than counted as orphaned while it still ran. A
			// current Kates API refuses one set to last longer than
			// kates.engine.max-duration-ms, and an older one fails every run
			// after 30 minutes, so that cap, as Kates ships it, is the longest
			// a scenario can be set to last.
			planned = mcpReaperMaxDurationMs * time.Millisecond
		} else if run.Spec != nil && run.Spec.DurationMs > 0 {
			planned = time.Duration(run.Spec.DurationMs) * time.Millisecond
			// An INTEGRITY run reads its records back for up to its duration
			// again once it has produced them, and the Kates API allows it
			// both (TestOrchestrator.plannedDurationMs). Counted once, a run
			// producing for more than 25 minutes would be called orphaned
			// while it was still reading.
			if run.TestType == "INTEGRITY" {
				planned *= 2
			}
		}
		overdue := now.Sub(started.Add(planned))
		if overdue > olderThan {
			orphans = append(orphans, orphanedRun{Run: run, Started: started, Planned: planned, Overdue: overdue})
		}
	}
	return orphans, unreadable
}

// parseCreatedAt reads a run's createdAt: RFC 3339, or the same without a
// zone, which is taken as UTC.
func parseCreatedAt(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	// Guard the slice: a short or empty CreatedAt panicked here.
	if err != nil && len(s) >= 19 {
		t, err = time.Parse("2006-01-02T15:04:05", s[:19])
	}
	return t, err
}

// shortDuration formats a duration as 2h05m, 40m or 45s, or "-" for none.
func shortDuration(d time.Duration) string {
	switch {
	case d <= 0:
		return "-"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Round(time.Second)/time.Second))
	}
	d = d.Round(time.Minute)
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return fmt.Sprintf("%dh%02dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
}

func init() {
	testCleanupCmd.Flags().BoolVar(&cleanupDryRun, "dry-run", false, "Preview what would be deleted without actually deleting")
	testCleanupCmd.Flags().BoolVarP(&cleanupYes, "yes", "y", false, "Delete without asking")
	testCleanupCmd.Flags().DurationVar(&cleanupOlderThan, "older-than", defaultCleanupOlderThan,
		"How long past its planned end a RUNNING test must be to count as orphaned")
	testCmd.AddCommand(testCleanupCmd)
}
