package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/klster/kates-cli/output"
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
