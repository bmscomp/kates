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

func TestKyvernoApplyDryRun(t *testing.T) {
	buf := output.ResetForTesting()
	defer func() { output.Out = os.Stdout }()

	kyvernoApplyDryRun = true
	kyvernoApplyMode = "Audit"

	cmd := kyvernoApplyCmd
	err := cmd.RunE(cmd, []string{})
	if err != nil {
		t.Fatalf("Apply dry-run failed: %v", err)
	}

	outStr := buf.String()
	if !strings.Contains(outStr, "Dry run complete") {
		t.Errorf("Expected dry run completion message, got:\n%s", outStr)
	}
}
