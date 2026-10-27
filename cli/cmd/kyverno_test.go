package cmd

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
)

func TestKyvernoDetectRecommendations(t *testing.T) {
	stubRunner(t)
	existing := map[string]string{
		"kates-pod-security-standards": "true",
		"custom-org-policy":            "false",
	}

	recs := generateRecommendations(existing)

	if len(recs) < 2 {
		t.Fatalf("Expected at least 2 recommendations, got %d", len(recs))
	}

	foundPSS := false
	for _, r := range recs {
		if r.Name == "Pod Security Standards" {
			foundPSS = true
			if !r.Recommended {
				t.Error("Pod Security Standards should always be recommended")
			}
		}
	}

	if !foundPSS {
		t.Error("Did not find Pod Security Standards recommendation")
	}
}

// Recommendations follow what the cluster shows: GHCR images bring the
// registry and Cosign policies, Strimzi topics the Kafka standards, and a
// -dev namespace the exceptions.
func TestKyvernoDetectRecommendationsFromTheCluster(t *testing.T) {
	stubRunner(t).
		On("kubectl get pods -A", "ghcr.io/bmscomp/kates:1.31 quay.io/strimzi/kafka:0.45").
		On("kubectl get netpol -A", "kafka deny-all").
		On("kubectl get ns", "default kafka team-dev").
		On("kubectl get crd kafkatopics.kafka.strimzi.io", "kafkatopics.kafka.strimzi.io")

	var names []string
	for _, r := range generateRecommendations(nil) {
		names = append(names, r.Name)
	}
	for _, want := range []string{"Image Registry Restriction", "Cosign Image Verification", "Policy Exceptions", "Kafka Topic Standards"} {
		if !slices.Contains(names, want) {
			t.Errorf("recommendations %q lack %q", names, want)
		}
	}
	if slices.Contains(names, "NetworkPolicy Generation") {
		t.Errorf("recommendations %q include NetworkPolicy Generation although policies exist", names)
	}
}

func TestKyvernoDetectCLI(t *testing.T) {
	buf := output.ResetForTesting()
	defer func() { output.Out = os.Stdout }()
	stubRunner(t).
		On("kubectl get crd clusterpolicies.kyverno.io", "clusterpolicies.kyverno.io").
		On("kubectl get clusterpolicies", "kates-pod-security-standards=true\nthird-party=false\n")

	cmd := kyvernoDetectCmd
	err := cmd.RunE(cmd, []string{})
	if err != nil {
		t.Fatalf("Detect command failed: %v", err)
	}

	outStr := buf.String()
	if !strings.Contains(outStr, "Kyverno Policy Detection") {
		t.Errorf("Expected banner 'Kyverno Policy Detection', got:\n%s", outStr)
	}
	if !strings.Contains(outStr, "Kyverno Admission Controller is installed") {
		t.Errorf("an installed Kyverno was not reported:\n%s", outStr)
	}
	if !strings.Contains(outStr, "Third-Party Policies Detected") || !strings.Contains(outStr, "third-party") {
		t.Errorf("the third-party policy was not listed:\n%s", outStr)
	}
}

// A kubectl failure exits 1. The four commands used to print it and return
// nil, so a script saw a clean exit.
func TestKyvernoCommands_KubectlFailureExitsNonZero(t *testing.T) {
	for _, tt := range []struct {
		cmd  *cobra.Command
		args []string
		want string // the kubectl call the fake must see
	}{
		{kyvernoStatusCmd, nil, "kubectl get clusterpolicies -o json"},
		{kyvernoViolationsCmd, nil, "kubectl get policyreport -o json -A"},
		{kyvernoEnforceCmd, []string{"pss"}, `kubectl patch clusterpolicy pss --type=merge -p {"spec":{"validationFailureAction":"Enforce"}}`},
		{kyvernoAuditCmd, []string{"pss"}, `kubectl patch clusterpolicy pss --type=merge -p {"spec":{"validationFailureAction":"Audit"}}`},
	} {
		fake := stubRunner(t).
			On("kubectl get crd", "clusterpolicies.kyverno.io").
			Fail("kubectl", "Unable to connect to the server")

		_, stderr, err := commandOutput(t, func() error { return tt.cmd.RunE(tt.cmd, tt.args) })
		if _, ok := err.(*silentErr); !ok {
			t.Errorf("kyverno %s: err = %v (%T), want a silentErr", tt.cmd.Name(), err, err)
		}
		if !strings.Contains(stderr, "Unable to connect") {
			t.Errorf("kyverno %s: stderr %q does not say why", tt.cmd.Name(), stderr)
		}
		if !slices.Contains(fake.Lines(), tt.want) {
			t.Errorf("kyverno %s: kubectl calls %q, want %q among them", tt.cmd.Name(), fake.Lines(), tt.want)
		}
	}

	// And success still exits 0.
	stubRunner(t)
	if _, _, err := commandOutput(t, func() error { return kyvernoEnforceCmd.RunE(kyvernoEnforceCmd, []string{"pss"}) }); err != nil {
		t.Errorf("kyverno enforce: err = %v, want nil", err)
	}
}

// kyverno status reads the policies as JSON and the reports as a table.
func TestKyvernoStatus(t *testing.T) {
	stubRunner(t).
		On("kubectl get crd", "clusterpolicies.kyverno.io").
		On("kubectl get clusterpolicies -o json", `{"items":[{"metadata":{"name":"kates-pod-security-standards"},
			"spec":{"validationFailureAction":"Enforce","rules":[{"name":"a","validate":{}},{"name":"b","validate":{}},{"name":"c","mutate":{}}]},
			"status":{"ready":true}}]}`).
		On("kubectl get policyreport -A --no-headers",
			"kafka   polr-ns-kafka   kyverno   1m   7   2   0   0   0\nkates   polr-ns-kates   kyverno   1m   3   0   0   0   0\n")

	stdout, stderr, err := commandOutput(t, func() error { return kyvernoStatusCmd.RunE(kyvernoStatusCmd, nil) })
	if err != nil {
		t.Fatalf("kyverno status: %v", err)
	}
	all := string(stdout) + stderr
	for _, want := range []string{"kates-pod-security-standards", "Enforce", "1 policies active — 2 validate rules, 1 mutate rules", "2 violations detected across 1 namespace(s)"} {
		if !strings.Contains(all, want) {
			t.Errorf("kyverno status output lacks %q:\n%s", want, all)
		}
	}
}

func TestKyvernoApplyBuildCommand(t *testing.T) {
	recs := []policyRec{
		{Name: "Pod Security", Recommended: true, HelmFlag: "kyvernoPolicy.enabled"},
		{Name: "NetPol", Recommended: true, HelmFlag: "kyvernoPolicy.networkPolicyGeneration.enabled"},
		{Name: "Cosign", Recommended: false, HelmFlag: "kyvernoPolicy.cosign.enabled"},
	}

	cmdStr := buildApplyCommand(recs)

	if !strings.Contains(cmdStr, "--with-netpol") {
		t.Error("Expected cmd to contain --with-netpol")
	}
	if strings.Contains(cmdStr, "--with-cosign") {
		t.Error("Expected cmd to NOT contain --with-cosign since it wasn't recommended")
	}
}

// kyvernoApplyCluster stands in for the cluster kyverno apply reads: Kyverno
// is installed, and the kates release is deployed in installedNS (none when
// empty). helm calls through the write seams are recorded in helmCalls.
func kyvernoApplyCluster(t *testing.T, installedNS string, dryRunOut string, dryRunErr error) (helmCalls *[]string) {
	t.Helper()
	origOut, origComb, origHelm := runExecOutputFn, runExecCombinedFn, runHelmFn
	prevDry, prevMode, prevYes := kyvernoApplyDryRun, kyvernoApplyMode, kyvernoApplyYes
	t.Cleanup(func() {
		runExecOutputFn, runExecCombinedFn, runHelmFn = origOut, origComb, origHelm
		kyvernoApplyDryRun, kyvernoApplyMode, kyvernoApplyYes = prevDry, prevMode, prevYes
	})
	kyvernoApplyMode = "Audit"
	// kubectl: the ClusterPolicy CRD is there, and every read is empty.
	stubRunner(t)
	var calls []string
	runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "helm" && args[0] == "list" {
			if installedNS == "" {
				return []byte("[]"), nil
			}
			return []byte(`[{"name":"kates","namespace":"` + installedNS + `","chart":"kates-1.0.0"}]`), nil
		}
		return nil, errors.New("unexpected read: " + name + " " + strings.Join(args, " "))
	}
	runExecCombinedFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return []byte(dryRunOut), dryRunErr
	}
	runHelmFn = func(_ context.Context, args ...string) error {
		calls = append(calls, "helm "+strings.Join(args, " "))
		return nil
	}
	return &calls
}

// --dry-run runs helm --dry-run against the installed release's namespace.
// It used to return before Helm ran, with -n kates hard-coded in the command
// it printed.
func TestKyvernoApplyDryRun(t *testing.T) {
	calls := kyvernoApplyCluster(t, "team-kates", "kind: ClusterPolicy\n---\nkind: ClusterPolicy\n", nil)
	kyvernoApplyDryRun = true

	stdout, _, err := commandOutput(t, func() error { return kyvernoApplyCmd.RunE(kyvernoApplyCmd, nil) })
	if err != nil {
		t.Fatalf("apply --dry-run: %v", err)
	}
	if len(*calls) != 1 || !strings.HasPrefix((*calls)[0], "helm upgrade --install kates ") || !strings.Contains((*calls)[0], " -n team-kates ") || !strings.HasSuffix((*calls)[0], " --dry-run") {
		t.Errorf("helm must run once, with --dry-run, against the installed release's namespace: %q", *calls)
	}
	if out := string(stdout); !strings.Contains(out, "Dry run complete: Helm rendered 2 resources for release kates in team-kates") {
		t.Errorf("dry run summary missing:\n%s", out)
	}

	// A render error is the point of a dry run: it fails the command.
	_ = kyvernoApplyCluster(t, "team-kates", "Error: values don't meet the specifications of the schema", errors.New("exit status 1"))
	kyvernoApplyDryRun = true
	_, stderr, err := commandOutput(t, func() error { return kyvernoApplyCmd.RunE(kyvernoApplyCmd, nil) })
	if _, ok := err.(*silentErr); !ok || !strings.Contains(stderr, "schema") {
		t.Errorf("a failed render: err %v (%T), stderr %q", err, err, stderr)
	}
}

// The policies go to the kates release that is installed; the flags decide
// when they were given, or when nothing or more than one is installed.
func TestKyvernoReleaseNamespace(t *testing.T) {
	orig := runExecOutputFn
	t.Cleanup(func() { runExecOutputFn = orig })
	list := func(releases string) {
		runExecOutputFn = func(_ context.Context, name string, args ...string) ([]byte, error) {
			return []byte(releases), nil
		}
	}

	list(`[]`)
	if ns, err := kyvernoReleaseNamespace(context.Background(), "kates", false); ns != "kates" || err != nil {
		t.Errorf("nothing installed: %q %v", ns, err)
	}
	list(`[{"name":"kates","namespace":"team-kates","chart":"kates-1.0.0"}]`)
	if ns, err := kyvernoReleaseNamespace(context.Background(), "kates", false); ns != "team-kates" || err != nil {
		t.Errorf("one installed elsewhere: %q %v", ns, err)
	}
	if ns, err := kyvernoReleaseNamespace(context.Background(), "kates", true); ns != "kates" || err != nil {
		t.Errorf("flags given: %q %v", ns, err)
	}
	list(`[{"name":"kates","namespace":"a","chart":"kates-1.0.0"},{"name":"kates","namespace":"b","chart":"kates-1.0.0"}]`)
	_, stderr, err := commandOutput(t, func() error {
		_, err := kyvernoReleaseNamespace(context.Background(), "kates", false)
		return err
	})
	if err == nil || !strings.Contains(stderr, "a and b") || !strings.Contains(stderr, "--app-ns") {
		t.Errorf("two installed: err %v, stderr %q", err, stderr)
	}
}
