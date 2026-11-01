package cmd

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/output"
)

// A helm run that is killed mid-install or mid-upgrade leaves its release
// pending-install or pending-upgrade, and helm refuses every later operation
// on it: "another operation (install/upgrade/rollback) is in progress". The
// deploy read such a release as not installed and ran helm upgrade --install
// on it, which failed the same way on every re-run; only kates clean got out,
// by removing everything. recoverStuckReleases clears them before the
// pipeline, where it can still ask.

// helmRevision is a row of helm history -o json.
type helmRevision struct {
	Revision int    `json:"revision"`
	Status   string `json:"status"`
	Chart    string `json:"chart"`
}

// stuckRecovery is what to do with a pending release.
type stuckRecovery struct {
	release, namespace, status string
	// action is "uninstall" (an install that never finished), "rollback"
	// (to revision), or "refuse" (a rollback would downgrade state; the user
	// decides).
	action   string
	revision int
}

// statefulCharts are the charts whose rollback is a downgrade of something
// that holds state or rolls pods that do: Kates (its schema may already be
// migrated), the Kafka cluster (its brokers), PostgreSQL and the Strimzi
// operator (deploy refuses operator downgrades). Their stuck upgrades are
// left to the user, with the command.
var statefulCharts = []string{"kates", "kafka-cluster", "cdc-postgresql", "strimzi-operator"}

var chartVersionSuffix = regexp.MustCompile(`-v?\d+\.\d+\.\d+.*$`)

// releaseChart is "kafka-ui" for helm's "kafka-ui-0.3.1".
func releaseChart(chart string) string {
	return chartVersionSuffix.ReplaceAllString(chart, "")
}

// planRecovery says what to do with release r, pending with history.
func planRecovery(r helmRelease, history []helmRevision) stuckRecovery {
	rec := stuckRecovery{release: r.Name, namespace: r.Namespace, status: r.Status}
	latest := 0
	for _, h := range history {
		latest = max(latest, h.Revision)
	}
	// The revision last in service: the deployed one, or else the newest
	// superseded one below the pending revision.
	var deployed, superseded int
	for _, h := range history {
		switch {
		case h.Revision >= latest:
		case h.Status == "deployed":
			deployed = h.Revision
		case h.Status == "superseded":
			superseded = max(superseded, h.Revision)
		}
	}
	rec.revision = cmp.Or(deployed, superseded)
	chart := releaseChart(r.Chart)
	switch {
	case rec.revision == 0:
		// No revision was ever in service (an install that never finished,
		// or one that failed and then stuck upgrading). Kafka's CR and pools
		// are kept by helm.sh/resource-policy: keep, and the reinstall adopts
		// them.
		rec.action = "uninstall"
	case slices.Contains(statefulCharts, chart):
		rec.action = "refuse"
	default:
		rec.action = "rollback"
	}
	return rec
}

// command is the helm command that carries out rec.
func (rec stuckRecovery) command() []string {
	if rec.action == "uninstall" {
		return []string{"uninstall", rec.release, "-n", rec.namespace}
	}
	return []string{"rollback", rec.release, strconv.Itoa(rec.revision), "-n", rec.namespace}
}

// recoverStuckReleases clears the entries' releases an interrupted helm run
// left pending, asking first unless --yes. A pending release can also be
// another deploy still running, which only the user can tell, so each one
// is named with its age.
func recoverStuckReleases(ctx context.Context, entries []DeploySummaryEntry) error {
	for _, r := range entryReleases(ctx, entries, "--pending") {
		if !strings.HasPrefix(r.Status, "pending-") || !slices.ContainsFunc(entries, func(e DeploySummaryEntry) bool {
			return e.Release == r.Name && e.Namespace == r.Namespace
		}) {
			continue
		}
		out, err := runExecOutputFn(ctx, "helm", "history", r.Name, "-n", r.Namespace, "-o", "json")
		if err != nil {
			return fmt.Errorf("reading the history of release %s (namespace %s), which is %s: %w", r.Name, r.Namespace, r.Status, err)
		}
		var history []helmRevision
		if err := json.Unmarshal(out, &history); err != nil {
			return fmt.Errorf("parse helm history of %s: %w", r.Name, err)
		}
		if err := recoverStuckRelease(ctx, planRecovery(r, history), r.Updated); err != nil {
			return err
		}
	}
	return nil
}

// pendingGrace is how long a pending release may be another helm run still
// working: the longest helm --timeout deploy passes, plus a margin.
const pendingGrace = 15 * time.Minute

func recoverStuckRelease(ctx context.Context, rec stuckRecovery, updated string) error {
	cmd := "helm " + strings.Join(rec.command(), " ")
	since, known := releaseSince(updated)
	age := ""
	if known {
		age = " for " + humanDuration(since)
	}
	what := fmt.Sprintf("Helm release %s (namespace %s) is %s%s: a helm run was interrupted, and helm refuses to touch the release until it is cleared.",
		rec.release, rec.namespace, rec.status, age)
	if rec.action == "refuse" {
		how := "uninstall it, or roll it back with helm rollback " + rec.release + " <revision> -n " + rec.namespace
		if rec.revision > 0 {
			how = "if no other deploy is running, roll it back: " + cmd
		}
		return fmt.Errorf("%s Rolling it back downgrades what it runs, so deploy leaves that to you: %s", what, how)
	}
	output.Warn(what)
	if deployYes && known && since < pendingGrace {
		// Unattended, nobody can say whether that run is still going.
		return fmt.Errorf("release %s turned %s %s ago, so another deploy may still be running; wait, or run: %s",
			rec.release, rec.status, humanDuration(since), cmd)
	}
	if !deployYes {
		if !IsInteractive() {
			return fmt.Errorf("refusing to clear release %s without confirmation; check no other deploy is running, then pass --yes or run: %s", rec.release, cmd)
		}
		ok, err := confirmFn(fmt.Sprintf("Run %s? Say no if another deploy may still be running.", cmd))
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("aborted: release " + rec.release + " is still " + rec.status)
		}
	}
	if err := runExecFn(ctx, "helm", rec.command()...); err != nil {
		return fmt.Errorf("%s: %w", cmd, err)
	}
	output.Success(fmt.Sprintf("Cleared release %s (%s)", rec.release, cmd))
	return nil
}

// releaseSince is how long ago helm list's updated time was, and whether it
// parsed.
func releaseSince(updated string) (time.Duration, bool) {
	t, err := time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", updated)
	if err != nil {
		return 0, false
	}
	return time.Since(t), true
}
