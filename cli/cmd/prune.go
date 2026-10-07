package cmd

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
)

var (
	pruneOlderThan string
	pruneStatuses  []string
	pruneYes       bool
	pruneDryRun    bool
)

const (
	// prunePerCall is the most runs one DELETE /api/tests deletes, the most
	// the Kates API allows a call.
	prunePerCall = 1000
	// pruneMaxCalls bounds one kates test prune at 100,000 runs. Past that it
	// stops and says how many are left, rather than run on unattended
	// against an API that keeps finding more.
	pruneMaxCalls = 100
)

// defaultPruneStatuses are the statuses a run ends in: a cancelled run is
// stored as FAILED, so it is pruned with the others.
var defaultPruneStatuses = []string{"DONE", "FAILED"}

var testPruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Delete finished test runs created before a cutoff",
	Long: `Delete the finished test runs, DONE or FAILED, created more than --older-than
ago, with their results. A cancelled run is stored as FAILED, so it goes too.
A run still PENDING, RUNNING or STOPPING is never pruned; kates test cleanup
deletes the ones stuck in RUNNING.

kates counts the runs first and asks before deleting them; without a terminal,
pass --yes. The Kates API deletes the oldest first, at most 1000 a call, and
kates calls it until none is left, at most 100 times. The chart's cleanup
CronJob (cleanup.enabled) makes the same calls on a schedule.

-o json prints the answer DELETE /api/tests gives, for the whole command:
matched as the first delete found them, deleted in all, and remaining after
the last call. With --dry-run, or when no run matches, it is the count.

It needs a Kates API with DELETE /api/tests; kates says so when the API it
reaches is older.`,
	Example: `  kates test prune --older-than 30d --dry-run
  kates test prune --older-than 720h
  kates test prune --older-than 7d --status FAILED --yes -o json`,
	RunE: runTestPrune,
}

func runTestPrune(cmd *cobra.Command, args []string) error {
	olderThan, err := parseOlderThan(pruneOlderThan)
	if err != nil {
		return cmdErr(err.Error())
	}
	statuses, err := parsePruneStatuses(pruneStatuses)
	if err != nil {
		return cmdErr(err.Error())
	}
	jsonOut := outputMode == "json"
	ctx := commandContext(cmd)

	// One cutoff for every call, so the runs deleted are the runs counted
	// however long the question waits for its answer.
	cutoff := time.Now().UTC().Add(-olderThan).Truncate(time.Second)
	before := cutoff.Format(time.RFC3339)

	count, err := apiClient.PruneTests(ctx, cutoff, statuses, prunePerCall, true)
	if err != nil {
		return cmdErr("Failed to count finished runs: " + err.Error())
	}
	found := fmt.Sprintf("Found %s created before %s", pruneRuns(count.Matched, statuses), before)
	if count.Matched == 0 || pruneDryRun {
		switch {
		case jsonOut:
			output.JSON(count)
		case count.Matched == 0:
			output.Success(fmt.Sprintf("No %s run was created before %s.", strings.Join(statuses, " or "), before))
		default:
			output.SubHeader(found)
			output.Hint("Dry run: nothing deleted. Run without --dry-run to delete them.")
		}
		return nil
	}

	if !jsonOut {
		output.SubHeader(found)
	}
	if !pruneYes {
		// The question names the count itself: with -o json nothing above
		// it has.
		question := fmt.Sprintf("Delete %s and their results?", pruneRuns(count.Matched, statuses))
		if count.Matched == 1 {
			question = fmt.Sprintf("Delete %s and its results?", pruneRuns(1, statuses))
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

	total, calls, last, err := pruneAll(ctx, cutoff, statuses, func(res *client.PruneResult) {
		// Only a prune that takes more than one call says how it goes.
		if !jsonOut && res.Remaining > 0 && res.Deleted > 0 {
			output.Hint(fmt.Sprintf("Deleted %s, %d left", plural(int(res.Deleted), "run", "runs"), res.Remaining))
		}
	})
	if jsonOut && calls > 0 {
		output.JSON(total)
	}
	deleted := pruneRuns(total.Deleted, statuses)
	switch {
	case err != nil && calls == 0:
		return cmdErr("Failed to delete finished runs: " + err.Error())
	case err != nil:
		return cmdErr(fmt.Sprintf("Deleted %s, then failed: %s", deleted, err.Error()))
	case total.Remaining > 0 && last.Deleted == 0:
		return cmdErr(fmt.Sprintf("Deleted %s; %d more still match, and the Kates API deleted none of them in the last call. Its log may say why.",
			deleted, total.Remaining))
	case total.Remaining > 0:
		return cmdErr(fmt.Sprintf("Deleted %s in %d calls and stopped with %d left; run kates test prune again to delete them.",
			deleted, calls, total.Remaining))
	}
	if !jsonOut {
		fmt.Println()
		output.Success(fmt.Sprintf("Deleted %s created before %s.", deleted, before))
	}
	return nil
}

// pruneAll calls DELETE /api/tests until no run is left to delete, a call
// deletes none, or pruneMaxCalls calls have been made, and passes each answer
// to progress. It returns the answers added up in one PruneResult: Matched
// from the first, Deleted over all of them, Remaining from the last. last is
// the last answer, nil when no call succeeded.
func pruneAll(ctx context.Context, cutoff time.Time, statuses []string, progress func(*client.PruneResult)) (total client.PruneResult, calls int, last *client.PruneResult, err error) {
	for calls < pruneMaxCalls {
		res, err := apiClient.PruneTests(ctx, cutoff, statuses, prunePerCall, false)
		if err != nil {
			return total, calls, last, err
		}
		if calls == 0 {
			total.Matched = res.Matched
		}
		calls++
		last = res
		total.CreatedBefore, total.Statuses = res.CreatedBefore, res.Statuses
		total.Deleted += res.Deleted
		total.Remaining = res.Remaining
		progress(res)
		// A call that deleted none would be followed by one that finds the
		// same runs and deletes none of them either.
		if res.Remaining == 0 || res.Deleted == 0 {
			break
		}
	}
	return total, calls, last, nil
}

// pruneRuns says "1500 DONE or FAILED runs" or "1 FAILED run".
func pruneRuns(n int64, statuses []string) string {
	noun := "runs"
	if n == 1 {
		noun = "run"
	}
	return fmt.Sprintf("%d %s %s", n, strings.Join(statuses, " or "), noun)
}

// maxOlderThanDays is the most days a time.Duration holds, about 292 years.
const maxOlderThanDays = int64(time.Duration(math.MaxInt64) / (24 * time.Hour))

// parseOlderThan reads --older-than: a Go duration such as 720h or 90m, or a
// whole number of days such as 30d, which time.ParseDuration does not read.
func parseOlderThan(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("--older-than is required: a duration such as 30d, 720h or 90m")
	}
	bad := fmt.Errorf("--older-than %q is not a duration such as 30d, 720h or 90m", s)
	var d time.Duration
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.ParseInt(days, 10, 64)
		if err != nil || n > maxOlderThanDays || n < -maxOlderThanDays {
			return 0, bad
		}
		d = time.Duration(n) * 24 * time.Hour
	} else {
		var err error
		if d, err = time.ParseDuration(s); err != nil {
			return 0, bad
		}
	}
	if d <= 0 {
		return 0, errors.New("--older-than must be more than 0")
	}
	return d, nil
}

// parsePruneStatuses reads --status, DONE, FAILED or both in any case, and
// returns them as the Kates API names them, DONE before FAILED, once each.
func parsePruneStatuses(values []string) ([]string, error) {
	var done, failed bool
	for _, v := range values {
		switch strings.ToUpper(strings.TrimSpace(v)) {
		case "DONE":
			done = true
		case "FAILED":
			failed = true
		default:
			return nil, fmt.Errorf("--status must be DONE or FAILED, not %q: only finished runs can be pruned", v)
		}
	}
	var statuses []string
	if done {
		statuses = append(statuses, "DONE")
	}
	if failed {
		statuses = append(statuses, "FAILED")
	}
	if len(statuses) == 0 {
		return nil, errors.New("--status must name DONE, FAILED or both")
	}
	return statuses, nil
}

func init() {
	testPruneCmd.Flags().StringVar(&pruneOlderThan, "older-than", "",
		"Delete the runs created more than this long ago, such as 30d, 720h or 90m (required)")
	testPruneCmd.Flags().StringSliceVar(&pruneStatuses, "status", defaultPruneStatuses,
		"Statuses of the finished runs to delete: DONE, FAILED or both")
	testPruneCmd.Flags().BoolVarP(&pruneYes, "yes", "y", false, "Delete without asking")
	testPruneCmd.Flags().BoolVar(&pruneDryRun, "dry-run", false, "Count the runs without deleting them")
	_ = testPruneCmd.RegisterFlagCompletionFunc("status", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return defaultPruneStatuses, cobra.ShellCompDirectiveNoFileComp
	})
	testCmd.AddCommand(testPruneCmd)
}
