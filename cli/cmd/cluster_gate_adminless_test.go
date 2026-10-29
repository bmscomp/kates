package cmd

import (
	"context"
	"encoding/base64"
	"slices"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/pkg/cluster"
)

func TestResolveCluster_OfferKindAdminlessBootstrapsTheTeamContext(t *testing.T) {
	s := &stubGate{env: dockerReady, tty: true, confirm: true,
		choice: kindChoice{adminless: true, name: "fanon", operators: []string{"strimzi"}}}
	s.install(t)

	got, err := resolveCluster()
	if err != nil {
		t.Fatalf("resolveCluster: %v", err)
	}
	if s.createName != "fanon" {
		t.Errorf("kind created %q, want fanon", s.createName)
	}
	if want := []string{"fanon", "strimzi"}; !slices.Equal(s.bootstrapped, want) {
		t.Errorf("bootstrapped %v, want %v", s.bootstrapped, want)
	}
	if got != "fanon" {
		t.Errorf("deploying to %q, want the team context fanon", got)
	}
}

func TestResolveCluster_OfferKindAdminIsPandaUnbootstrapped(t *testing.T) {
	s := &stubGate{env: dockerReady, tty: true, confirm: true}
	s.install(t)

	if _, err := resolveCluster(); err != nil {
		t.Fatalf("resolveCluster: %v", err)
	}
	if s.createName != cluster.DefaultKindClusterName || s.bootstrapped != nil {
		t.Errorf("created %q, bootstrapped %v; want panda, no bootstrap", s.createName, s.bootstrapped)
	}
}

// stubAdminlessKind answers the bootstrap's kubectl and helm calls, with can-i
// as the team context's answer, and records every call.
func stubAdminlessKind(t *testing.T, canI string) *[]string {
	t.Helper()
	origOut, origComb, origStdin, origHelm, origAdminless := runExecOutputFn, runExecCombinedFn, runExecStdinFn, runHelmFn, deployAdminless
	t.Cleanup(func() {
		runExecOutputFn, runExecCombinedFn, runExecStdinFn, runHelmFn, deployAdminless = origOut, origComb, origStdin, origHelm, origAdminless
	})
	calls := &[]string{}
	record := func(name string, args []string) string {
		c := name + " " + strings.Join(args, " ")
		*calls = append(*calls, c)
		return c
	}
	runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		c := record(name, args)
		switch {
		case strings.Contains(c, "get secret kates-team-token"):
			return []byte(base64.StdEncoding.EncodeToString([]byte("tok"))), nil
		case strings.Contains(c, "auth can-i"):
			return []byte(canI + "\n"), nil
		}
		return nil, nil
	}
	runExecCombinedFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		record(name, args)
		return nil, nil
	}
	runExecStdinFn = func(_ context.Context, name string, args []string, _ string) error {
		record(name, args)
		return nil
	}
	runHelmFn = func(_ context.Context, args ...string) error {
		record("helm", args)
		return nil
	}
	return calls
}

func TestBootstrapAdminlessKind_InstallsAsAdminDeploysAsTeam(t *testing.T) {
	calls := stubAdminlessKind(t, "no")
	deployAdminless = false

	got, err := bootstrapAdminlessKind("fanon", []string{"kyverno", "strimzi"})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if got != "fanon" || !deployAdminless {
		t.Errorf("got context %q adminless=%v, want fanon on the admin-less profile", got, deployAdminless)
	}
	all := strings.Join(*calls, "\n")
	for _, want := range []string{
		"kubectl apply -f - --context=kind-fanon",
		"upgrade --install kyverno",
		"features.policyExceptions.enabled=true",
		"strimzi-kafka-operator.watchNamespaces={",
		"config set-context fanon --cluster=kind-fanon --user=fanon-kates-team",
		"kubectl --context=fanon auth can-i * * --all-namespaces",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no call with %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "upgrade --install monitoring") {
		t.Error("monitoring was not chosen, but was installed")
	}
	for _, c := range *calls {
		if strings.HasPrefix(c, "helm upgrade") && !strings.Contains(c, "--kube-context=kind-fanon") {
			t.Errorf("an operator went in without the admin context: %s", c)
		}
	}
}

func TestBootstrapAdminlessKind_RefusesAnAdminContext(t *testing.T) {
	stubAdminlessKind(t, "yes")

	if _, err := bootstrapAdminlessKind("fanon", nil); err == nil || !strings.Contains(err.Error(), "should not be cluster admin") {
		t.Fatalf("want a refusal of the admin context, got %v", err)
	}
}

func TestAdminlessTeamManifest_BindsAdminInEachNamespace(t *testing.T) {
	m := adminlessTeamManifest("kates-team", "a", []string{"a", "b"})
	if got := strings.Count(m, "kind: RoleBinding"); got != 2 {
		t.Errorf("%d RoleBindings, want one per namespace", got)
	}
	if strings.Contains(m, "ClusterRoleBinding") {
		t.Error("the team account must get no cluster-wide binding")
	}
}
