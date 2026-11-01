package cmd

import (
	"bufio"
	"context"
	"errors"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPlanRecovery(t *testing.T) {
	rev := func(n int, status, chart string) helmRevision {
		return helmRevision{Revision: n, Status: status, Chart: chart}
	}
	cases := []struct {
		name    string
		release helmRelease
		history []helmRevision
		action  string
		rev     int
	}{
		{"an install that never finished is uninstalled",
			helmRelease{Name: "kafka-ui", Status: "pending-install", Chart: "kafka-ui-0.3.2"},
			[]helmRevision{rev(1, "pending-install", "kafka-ui-0.3.2")}, "uninstall", 0},
		{"a Kafka install that never finished too: its CR is kept",
			helmRelease{Name: "krafter", Status: "pending-install", Chart: "kafka-cluster-1.0.18"},
			[]helmRevision{rev(1, "pending-install", "kafka-cluster-1.0.18")}, "uninstall", 0},
		{"a stuck upgrade rolls back to the deployed revision",
			helmRelease{Name: "kafka-ui", Status: "pending-upgrade", Chart: "kafka-ui-0.3.2"},
			[]helmRevision{rev(1, "superseded", "kafka-ui-0.3.0"), rev(2, "deployed", "kafka-ui-0.3.1"), rev(3, "pending-upgrade", "kafka-ui-0.3.2")},
			"rollback", 2},
		{"with no deployed revision, the newest superseded one",
			helmRelease{Name: "mm2", Status: "pending-rollback", Chart: "mirror-maker2-0.2.0"},
			[]helmRevision{rev(1, "superseded", "mirror-maker2-0.1.0"), rev(2, "superseded", "mirror-maker2-0.2.0"), rev(3, "failed", "mirror-maker2-0.2.0"), rev(4, "pending-rollback", "mirror-maker2-0.2.0")},
			"rollback", 2},
		{"Kates is left to the user: a rollback downgrades its schema",
			helmRelease{Name: "kates", Status: "pending-upgrade", Chart: "kates-0.14.1"},
			[]helmRevision{rev(1, "deployed", "kates-0.14.0"), rev(2, "pending-upgrade", "kates-0.14.1")}, "refuse", 1},
		{"so is the Kafka cluster, whatever its release is called",
			helmRelease{Name: "orders", Status: "pending-upgrade", Chart: "kafka-cluster-1.0.18-rc.1"},
			[]helmRevision{rev(1, "deployed", "kafka-cluster-1.0.17"), rev(2, "pending-upgrade", "kafka-cluster-1.0.18-rc.1")}, "refuse", 1},
		{"an upgrade with nothing ever in service is uninstalled",
			helmRelease{Name: "kafka-ui", Status: "pending-upgrade", Chart: "kafka-ui-0.3.2"},
			[]helmRevision{rev(1, "failed", "kafka-ui-0.3.1"), rev(2, "pending-upgrade", "kafka-ui-0.3.2")}, "uninstall", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := planRecovery(tc.release, tc.history)
			if got.action != tc.action || got.revision != tc.rev {
				t.Errorf("got %s %d, want %s %d", got.action, got.revision, tc.action, tc.rev)
			}
		})
	}
}

// A stuck release of this deploy is cleared with helm's own command, after
// --yes; a pending release of the same name in another namespace is someone
// else's and is left alone.
func TestRecoverStuckReleases(t *testing.T) {
	origOut, origExec, origYes := runExecOutputFn, runExecFn, deployYes
	t.Cleanup(func() { runExecOutputFn, runExecFn, deployYes = origOut, origExec, origYes })
	deployYes = true

	runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		got := name + " " + strings.Join(args, " ")
		switch got {
		case "helm list -A --pending -o json":
			return []byte(`[
				{"name":"kafka-ui","namespace":"kafka","status":"pending-upgrade","chart":"kafka-ui-0.3.2","updated":"2026-01-01T10:00:00Z"},
				{"name":"kafka-ui","namespace":"other-team","status":"pending-upgrade","chart":"kafka-ui-0.3.2"}]`), nil
		case "helm history kafka-ui -n kafka -o json":
			return []byte(`[{"revision":4,"status":"deployed","chart":"kafka-ui-0.3.1"},{"revision":5,"status":"pending-upgrade","chart":"kafka-ui-0.3.2"}]`), nil
		}
		t.Fatalf("unexpected read: %q", got)
		return nil, nil
	}
	var ran []string
	runExecFn = func(_ context.Context, name string, args ...string) error {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return nil
	}

	entries := []DeploySummaryEntry{{Release: "kafka-ui", Namespace: "kafka"}, {Release: "kates", Namespace: "kates"}}
	if err := recoverStuckReleases(context.Background(), entries); err != nil {
		t.Fatalf("recoverStuckReleases: %v", err)
	}
	if want := []string{"helm rollback kafka-ui 4 -n kafka"}; !slices.Equal(ran, want) {
		t.Errorf("ran %q, want %q", ran, want)
	}
}

// A stuck Kates stops the deploy before anything runs, with the command.
func TestRecoverStuckReleases_RefusesKates(t *testing.T) {
	origOut, origExec, origYes := runExecOutputFn, runExecFn, deployYes
	t.Cleanup(func() { runExecOutputFn, runExecFn, deployYes = origOut, origExec, origYes })
	deployYes = true
	runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if args[0] == "list" {
			return []byte(`[{"name":"kates","namespace":"kates","status":"pending-upgrade","chart":"kates-0.14.1"}]`), nil
		}
		return []byte(`[{"revision":7,"status":"deployed","chart":"kates-0.14.0"},{"revision":8,"status":"pending-upgrade","chart":"kates-0.14.1"}]`), nil
	}
	runExecFn = func(context.Context, string, ...string) error {
		t.Fatal("deploy ran a command on a stuck Kates")
		return nil
	}
	err := recoverStuckReleases(context.Background(), []DeploySummaryEntry{{Release: "kates", Namespace: "kates"}})
	if err == nil || !strings.Contains(err.Error(), "helm rollback kates 7 -n kates") {
		t.Errorf("err = %v, want the rollback command", err)
	}
}

// Without --yes and without a terminal, nothing is cleared.
func TestRecoverStuckReleases_AsksFirst(t *testing.T) {
	origOut, origExec, origYes := runExecOutputFn, runExecFn, deployYes
	t.Cleanup(func() { runExecOutputFn, runExecFn, deployYes = origOut, origExec, origYes })
	deployYes = false
	runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if args[0] == "list" {
			return []byte(`[{"name":"kafka-ui","namespace":"kafka","status":"pending-install","chart":"kafka-ui-0.3.2"}]`), nil
		}
		return []byte(`[{"revision":1,"status":"pending-install","chart":"kafka-ui-0.3.2"}]`), nil
	}
	runExecFn = func(context.Context, string, ...string) error {
		t.Fatal("cleared a release without asking")
		return nil
	}
	err := recoverStuckReleases(context.Background(), []DeploySummaryEntry{{Release: "kafka-ui", Namespace: "kafka"}})
	if err == nil || !strings.Contains(err.Error(), "helm uninstall kafka-ui -n kafka") {
		t.Errorf("err = %v, want a refusal naming the command", err)
	}
}

// A cancelled deploy asks its children to stop with SIGTERM, which helm
// handles by recording its release state; it used to SIGKILL them.
func TestDeployCommand_CancelSendsSIGTERM(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no SIGTERM on Windows")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := deployCommand(ctx, "sh", "-c", `trap 'exit 7' TERM; echo trapped; while :; do sleep 0.1; done`)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Cancel only once the trap is in place.
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "trapped\n" {
		t.Fatalf("read %q, %v", line, err)
	}
	cancel()
	err = cmd.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Errorf("wait = %v, want exit 7 from the TERM trap", err)
	}
}

// A webhook an earlier run left fail-open is put back; one at Fail is not
// touched.
func TestRestoreFailOpenCertMgrWebhook(t *testing.T) {
	origOut, origExec := runExecOutputFn, runExecFn
	t.Cleanup(func() { runExecOutputFn, runExecFn = origOut, origExec })
	runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if args[1] == "validatingwebhookconfiguration" {
			return []byte("Ignore"), nil
		}
		return []byte("Fail"), nil
	}
	var patched []string
	runExecFn = func(_ context.Context, name string, args ...string) error {
		patched = append(patched, args[1])
		return nil
	}
	restoreFailOpenCertMgrWebhook(context.Background())
	if want := []string{"validatingwebhookconfiguration"}; !slices.Equal(patched, want) {
		t.Errorf("patched %q, want %q", patched, want)
	}
}

// A helm list that fails twice, then answers, still finds the release: one
// slow answer used to read as "not installed".
func TestIsHelmReleaseDeployed_Retries(t *testing.T) {
	origOut := runExecOutputFn
	t.Cleanup(func() { runExecOutputFn = origOut })
	calls := 0
	runExecOutputFn = func(context.Context, string, ...string) ([]byte, error) {
		calls++
		if calls < 3 {
			return nil, context.DeadlineExceeded
		}
		return []byte(`[{"name":"kafka-ui","namespace":"kafka","status":"deployed"}]`), nil
	}
	if !isHelmReleaseDeployedDefault(context.Background(), "kafka-ui", "kafka") {
		t.Error("release not found after two failed reads")
	}
	if calls != 3 {
		t.Errorf("helm list ran %d times, want 3", calls)
	}
}

// Under --yes, a release that turned pending minutes ago may be another
// deploy still at work: it is left alone.
func TestRecoverStuckReleases_YesSparesARecentOne(t *testing.T) {
	origOut, origExec, origYes := runExecOutputFn, runExecFn, deployYes
	t.Cleanup(func() { runExecOutputFn, runExecFn, deployYes = origOut, origExec, origYes })
	deployYes = true
	updated := time.Now().Add(-2 * time.Minute).UTC().Format("2006-01-02 15:04:05.999999999 -0700 MST")
	runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if args[0] == "list" {
			return []byte(`[{"name":"kafka-ui","namespace":"kafka","status":"pending-upgrade","chart":"kafka-ui-0.3.2","updated":"` + updated + `"}]`), nil
		}
		return []byte(`[{"revision":1,"status":"deployed","chart":"kafka-ui-0.3.1"},{"revision":2,"status":"pending-upgrade","chart":"kafka-ui-0.3.2"}]`), nil
	}
	runExecFn = func(context.Context, string, ...string) error {
		t.Fatal("cleared a release another deploy may hold")
		return nil
	}
	err := recoverStuckReleases(context.Background(), []DeploySummaryEntry{{Release: "kafka-ui", Namespace: "kafka"}})
	if err == nil || !strings.Contains(err.Error(), "may still be running") {
		t.Errorf("err = %v, want a refusal", err)
	}
}

// Under --yes, a pending release whose age helm does not give is spared too.
func TestRecoverStuckReleases_YesSparesAnUnknownAge(t *testing.T) {
	origOut, origExec, origYes := runExecOutputFn, runExecFn, deployYes
	t.Cleanup(func() { runExecOutputFn, runExecFn, deployYes = origOut, origExec, origYes })
	deployYes = true
	runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if args[0] == "list" {
			return []byte(`[{"name":"kafka-ui","namespace":"kafka","status":"pending-install","chart":"kafka-ui-0.3.2","updated":"yesterday"}]`), nil
		}
		return []byte(`[{"revision":1,"status":"pending-install","chart":"kafka-ui-0.3.2"}]`), nil
	}
	runExecFn = func(context.Context, string, ...string) error {
		t.Fatal("cleared a release of unknown age")
		return nil
	}
	err := recoverStuckReleases(context.Background(), []DeploySummaryEntry{{Release: "kafka-ui", Namespace: "kafka"}})
	if err == nil || !strings.Contains(err.Error(), "may still be running") {
		t.Errorf("err = %v, want a refusal", err)
	}
}

// Once a namespace's read has failed every try, the next lookup there gets
// one try, not three: the entries' lookups run one after another.
func TestIsHelmReleaseDeployed_UnreadableNamespaceTriedOnce(t *testing.T) {
	origOut := runExecOutputFn
	t.Cleanup(func() { runExecOutputFn = origOut; unreadableNamespaces.Delete("slow") })
	calls := 0
	runExecOutputFn = func(context.Context, string, ...string) ([]byte, error) {
		calls++
		return nil, context.DeadlineExceeded
	}
	isHelmReleaseDeployedDefault(context.Background(), "kafka-ui", "slow")
	isHelmReleaseDeployedDefault(context.Background(), "mm2", "slow")
	if calls != 4 {
		t.Errorf("helm list ran %d times, want 3 then 1", calls)
	}
}
