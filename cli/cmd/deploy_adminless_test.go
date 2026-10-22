package cmd

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/pflag"
)

func TestIsClusterAdmin_ReadsTheAnswer(t *testing.T) {
	orig := runExecOutputFn
	t.Cleanup(func() { runExecOutputFn = orig })

	for out, want := range map[string][2]bool{"yes\n": {true, true}, "no\n": {false, true}, "": {false, false}} {
		runExecOutputFn = func(context.Context, string, ...string) ([]byte, error) { return []byte(out), nil }
		if admin, ok := isClusterAdmin(context.Background()); admin != want[0] || ok != want[1] {
			t.Errorf("can-i %q: got admin=%v ok=%v, want %v", out, admin, ok, want)
		}
	}
}

// resetDeployFlagsChanged makes the deploy command's flags look unset, since
// other tests Set them and pflag never forgets.
func resetDeployFlagsChanged(t *testing.T) {
	t.Helper()
	deployCmd.Flags().VisitAll(func(f *pflag.Flag) { f.Changed = false })
}

func TestRunDeploy_NonAdminIsToldAboutTheAdminlessProfile(t *testing.T) {
	origAdmin, origHelm := clusterAdminFn, runHelmFn
	t.Cleanup(func() { clusterAdminFn, runHelmFn = origAdmin, origHelm })
	setDeployFlags(t, func() { deployAdminless = false })
	resetDeployFlagsChanged(t)

	clusterAdminFn = func(context.Context) (bool, bool) { return false, true }
	helmCalls := 0
	runHelmFn = func(context.Context, ...string) error { helmCalls++; return nil }

	err := runDeploy(deployCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "kates deploy --adminless") {
		t.Fatalf("want the admin-less suggestion, got %v", err)
	}
	if helmCalls != 0 {
		t.Errorf("%d helm calls before the profile was settled", helmCalls)
	}
}

// adminlessClusterRead is a team account on a cluster with Strimzi: every
// create in its namespace allowed, nothing cluster-wide.
func adminlessClusterRead(refuse string) func(context.Context, string, ...string) ([]byte, error) {
	return func(ctx context.Context, name string, args ...string) ([]byte, error) {
		joined := name + " " + strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "auth can-i * *"):
			return []byte("no\n"), nil
		case strings.Contains(joined, "auth can-i create"):
			if refuse != "" && strings.Contains(joined, refuse) {
				return []byte("no\n"), nil
			}
			return []byte("yes\n"), nil
		case strings.Contains(joined, "api-resources --api-group=kafka.strimzi.io"):
			return []byte("kafkas.kafka.strimzi.io\nkafkanodepools.kafka.strimzi.io\n"), nil
		case strings.Contains(joined, "config view --minify"):
			return []byte("team"), nil
		}
		return stubClusterRead(ctx, name, args...)
	}
}

func TestRunDeploy_AdminlessInstallsTheStackInOneNamespace(t *testing.T) {
	origOutput, origCombined, origHelm, origExec := runExecOutputFn, runExecCombinedFn, runHelmFn, runExecFn
	origStdin, origDeployed := runExecStdinFn, isHelmReleaseDeployedFn
	t.Cleanup(func() {
		runExecOutputFn, runExecCombinedFn, runHelmFn, runExecFn = origOutput, origCombined, origHelm, origExec
		runExecStdinFn, isHelmReleaseDeployedFn = origStdin, origDeployed
	})
	setDeployFlags(t, func() {
		deployAdminless, deployWithSchemaRegistry, deployDryRun = true, "apicurio", false
		deployWithChaos, deployWithMonitoring, deployWithCertManager, deployWithStrimzi = true, true, true, true
		deployWithKafkaConnect, deployWithMirrorMaker2, deployWithKafkaUI = false, false, false
	})
	resetDeployFlagsChanged(t)
	t.Chdir(t.TempDir())

	runExecOutputFn, runExecCombinedFn = adminlessClusterRead(""), adminlessClusterRead("")
	var mu sync.Mutex
	var installs []string
	runHelmFn = func(_ context.Context, args ...string) error {
		if len(args) > 2 && args[0] == "upgrade" {
			mu.Lock()
			installs = append(installs, strings.Join(args, " "))
			mu.Unlock()
		}
		return nil
	}
	runExecFn = func(context.Context, string, ...string) error { return nil }
	runExecStdinFn = func(context.Context, string, []string, string) error { return nil }
	isHelmReleaseDeployedFn = func(context.Context, string, string) bool { return false }

	if err := runDeploy(deployCmd, nil); err != nil {
		t.Fatalf("runDeploy: %v", err)
	}

	installed := map[string]string{}
	for _, c := range installs {
		installed[strings.Fields(c)[2]] = c
	}
	for _, release := range []string{"krafter", "postgresql", "connect-cluster", "mm2", "apicurio", "kafka-ui", "kates"} {
		c, ok := installed[release]
		if !ok {
			t.Errorf("release %s not installed; installs: %v", release, installs)
			continue
		}
		if !strings.Contains(c, "-n team") {
			t.Errorf("release %s not in namespace team: %s", release, c)
		}
	}
	for _, release := range []string{"strimzi-operator", "cert-manager", "kyverno", "monitoring", "chaos"} {
		if _, ok := installed[release]; ok {
			t.Errorf("cluster-scoped release %s installed admin-less", release)
		}
	}
	if !strings.Contains(installed["kates"], "--set rbac.scope=namespace") {
		t.Errorf("kates without rbac.scope=namespace: %s", installed["kates"])
	}
}

func TestCheckAdminlessPrerequisites(t *testing.T) {
	orig := runExecOutputFn
	t.Cleanup(func() { runExecOutputFn = orig })

	runExecOutputFn = adminlessClusterRead("")
	if err := checkAdminlessPrerequisites(context.Background(), "team"); err != nil {
		t.Errorf("everything granted: %v", err)
	}

	runExecOutputFn = adminlessClusterRead("kafkamirrormaker2s")
	err := checkAdminlessPrerequisites(context.Background(), "team")
	if err == nil || !strings.Contains(err.Error(), "kafkamirrormaker2s.kafka.strimzi.io") || strings.Contains(err.Error(), "kafkas.kafka") {
		t.Errorf("want only the refused grant named, got %v", err)
	}

	runExecOutputFn = func(context.Context, string, ...string) ([]byte, error) { return nil, nil }
	if err := checkAdminlessPrerequisites(context.Background(), "team"); err == nil || !strings.Contains(err.Error(), "no Strimzi operator is installed") {
		t.Errorf("want the missing operator named, got %v", err)
	}
}

func TestApplyAdminlessProfile_RefusesClusterScopedFlags(t *testing.T) {
	setDeployFlags(t, func() {})
	resetDeployFlagsChanged(t)
	t.Cleanup(func() { resetDeployFlagsChanged(t) })

	_ = deployCmd.Flags().Set("with-chaos", "true")
	if err := applyAdminlessProfile(context.Background(), deployCmd); err == nil || !strings.Contains(err.Error(), "--with-chaos") {
		t.Errorf("want --with-chaos refused, got %v", err)
	}
}
