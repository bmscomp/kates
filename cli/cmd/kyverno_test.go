package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
)

func TestKyvernoDetectRecommendations(t *testing.T) {
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

func TestKyvernoDetectCLI(t *testing.T) {
	buf := output.ResetForTesting()
	defer func() { output.Out = os.Stdout }()

	cmd := kyvernoDetectCmd
	err := cmd.RunE(cmd, []string{})
	if err != nil {
		t.Fatalf("Detect command failed: %v", err)
	}

	outStr := buf.String()
	if !strings.Contains(outStr, "Kyverno Policy Detection") {
		t.Errorf("Expected banner 'Kyverno Policy Detection', got:\n%s", outStr)
	}
}

// fakeKyvernoKubectl answers every kubectl call with err, or with JSON/output
// when err is nil, and records what it was asked.
type fakeKyvernoKubectl struct {
	err   error
	json  string
	calls []string
}

func (f *fakeKyvernoKubectl) record(args ...string) {
	f.calls = append(f.calls, strings.Join(args, " "))
}

func (f *fakeKyvernoKubectl) Run(_ context.Context, args ...string) (string, error) {
	f.record(args...)
	return "", f.err
}

func (f *fakeKyvernoKubectl) Output(_ context.Context, args ...string) ([]byte, error) {
	f.record(args...)
	if f.err != nil {
		return nil, f.err
	}
	return []byte(f.json), nil
}

func (f *fakeKyvernoKubectl) JSON(_ context.Context, result interface{}, args ...string) error {
	f.record(args...)
	if f.err != nil {
		return f.err
	}
	return json.Unmarshal([]byte(f.json), result)
}

func (f *fakeKyvernoKubectl) CRDExists(context.Context, string) bool { return true }

// A kubectl failure exits 1. The four commands used to print it and return
// nil, so a script saw a clean exit.
func TestKyvernoCommands_KubectlFailureExitsNonZero(t *testing.T) {
	orig := newKyvernoKubectlFn
	t.Cleanup(func() { newKyvernoKubectlFn = orig })

	for _, tt := range []struct {
		cmd  *cobra.Command
		args []string
		want string // the kubectl call the fake must see
	}{
		{kyvernoStatusCmd, nil, "get clusterpolicies"},
		{kyvernoViolationsCmd, nil, "get policyreport -o json -A"},
		{kyvernoEnforceCmd, []string{"pss"}, `patch clusterpolicy pss --type=merge -p {"spec":{"validationFailureAction":"Enforce"}}`},
		{kyvernoAuditCmd, []string{"pss"}, `patch clusterpolicy pss --type=merge -p {"spec":{"validationFailureAction":"Audit"}}`},
	} {
		fake := &fakeKyvernoKubectl{err: errors.New("kubectl get: exit status 1\nUnable to connect to the server")}
		newKyvernoKubectlFn = func() kyvernoKubectl { return fake }

		_, stderr, err := commandOutput(t, func() error { return tt.cmd.RunE(tt.cmd, tt.args) })
		if _, ok := err.(*silentErr); !ok {
			t.Errorf("kyverno %s: err = %v (%T), want a silentErr", tt.cmd.Name(), err, err)
		}
		if !strings.Contains(stderr, "Unable to connect") {
			t.Errorf("kyverno %s: stderr %q does not say why", tt.cmd.Name(), stderr)
		}
		if len(fake.calls) == 0 || fake.calls[0] != tt.want {
			t.Errorf("kyverno %s: kubectl calls %q, want first %q", tt.cmd.Name(), fake.calls, tt.want)
		}
	}

	// And success still exits 0.
	fake := &fakeKyvernoKubectl{}
	newKyvernoKubectlFn = func() kyvernoKubectl { return fake }
	if _, _, err := commandOutput(t, func() error { return kyvernoEnforceCmd.RunE(kyvernoEnforceCmd, []string{"pss"}) }); err != nil {
		t.Errorf("kyverno enforce: err = %v, want nil", err)
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
	origK, origOut, origComb, origHelm := newKyvernoKubectlFn, runExecOutputFn, runExecCombinedFn, runHelmFn
	prevDry, prevMode, prevYes := kyvernoApplyDryRun, kyvernoApplyMode, kyvernoApplyYes
	t.Cleanup(func() {
		newKyvernoKubectlFn, runExecOutputFn, runExecCombinedFn, runHelmFn = origK, origOut, origComb, origHelm
		kyvernoApplyDryRun, kyvernoApplyMode, kyvernoApplyYes = prevDry, prevMode, prevYes
	})
	kyvernoApplyMode = "Audit"
	newKyvernoKubectlFn = func() kyvernoKubectl { return &fakeKyvernoKubectl{} }
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
