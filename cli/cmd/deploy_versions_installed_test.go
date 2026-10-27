package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/pkg/strimzi"
)

// An operator list that cannot be read is not "no operator". It used to be,
// which sent an RBAC refusal or a cluster that did not answer down the
// install path, past the downgrade rail.
func TestResolveVersionPlan_UnreadableOperators(t *testing.T) {
	root := versionsRepo(t, "1.1.0", "4.2.0")
	failing := func(msg string) strimzi.Runner {
		return strimzi.RunnerFunc(func(_ context.Context, name string, args ...string) (string, error) {
			joined := name + " " + strings.Join(args, " ")
			switch {
			case strings.HasPrefix(joined, "kubectl get pods -A"):
				return "", errors.New(msg)
			case strings.HasPrefix(joined, "kubectl get crd"):
				return `{"items":[]}`, nil
			}
			return "", fmt.Errorf("unexpected %s", joined)
		})
	}
	opts := func(adminless, yes bool) versionOptions {
		return versionOptions{RepoRoot: root, CacheDir: t.TempDir(), Offline: true, KafkaNS: "kafka", FollowInstalled: adminless, Yes: yes}
	}
	forbidden := `Error from server (Forbidden): pods is forbidden: User "dev" cannot list resource "pods" in API group "" at the cluster scope`
	transient := "Unable to connect to the server: dial tcp: i/o timeout"

	if _, err := resolveVersionPlan(context.Background(), failing(forbidden), opts(false, false)); err == nil || !strings.Contains(err.Error(), "refused") || !strings.Contains(err.Error(), "--adminless") {
		t.Errorf("forbidden must refuse the full profile and point at --adminless: %v", err)
	}
	vp, err := resolveVersionPlan(context.Background(), failing(forbidden), opts(true, false))
	if err != nil || vp.OperatorAction != "install" || !strings.Contains(strings.Join(vp.Notes, "\n"), "not permitted") {
		t.Errorf("forbidden, admin-less: err %v, plan %+v", err, vp)
	}

	if _, err := resolveVersionPlan(context.Background(), failing(transient), opts(false, false)); err == nil || !strings.Contains(err.Error(), "--yes") || !strings.Contains(err.Error(), "i/o timeout") {
		t.Errorf("an unexplained failure must refuse and name --yes: %v", err)
	}
	stdout, _, err := commandOutput(t, func() error {
		var rerr error
		vp, rerr = resolveVersionPlan(context.Background(), failing(transient), opts(false, true))
		return rerr
	})
	if err != nil || vp.OperatorAction != "install" || !strings.Contains(strings.Join(vp.Notes, "\n"), "WARNING") || !strings.Contains(string(stdout), "i/o timeout") {
		t.Errorf("--yes deploys as if none were installed, with a warning: err %v, stdout %q, plan %+v", err, stdout, vp)
	}

	// A chart-only read (kates versions, the wizard) never consults kubectl.
	vp, err = resolveVersionPlan(context.Background(), chartOnlyRunner{}, opts(false, false))
	if err != nil || vp.OperatorAction != "install" {
		t.Errorf("chart-only: err %v, plan %+v", err, vp)
	}
}
