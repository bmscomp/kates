package cmd

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

type policyRec struct {
	Name           string
	Description    string
	Recommended    bool
	Reason         string
	HelmFlag       string
	HelmValue      string
	Is3rdParty     bool
	CurrentStatus  string
}

var kyvernoDetectCmd = &cobra.Command{
	Use:   "detect",
	Short: "Detect cluster state and recommend Kyverno policies",
	Long: `Introspects the cluster to find third-party policies and recommend built-in Kates policies based on workload signatures, ingress, and namespace structures.`,
	Example: "  kates kyverno detect",
	RunE: func(cmd *cobra.Command, args []string) error {
		output.Banner("Kyverno Policy Detection", "Cluster Introspection & Recommendations")

		// 1. Check Kyverno Installation
		installed := checkKyvernoInstalled()
		if !installed {
			output.Warn("Kyverno is not installed in this cluster")
			output.Hint("Run 'kates deploy --with-kyverno' or 'kates kyverno apply' to install it.")
			fmt.Println()
		} else {
			output.Success("Kyverno Admission Controller is installed")
			fmt.Println()
		}

		// 2. Discover existing policies (including 3rd party)
		existingPolicies := discoverExistingPolicies()
		
		// 3. Generate Recommendations
		recs := generateRecommendations(existingPolicies)

		// 4. Output Results
		renderRecommendations(recs, existingPolicies)

		// 5. Output Application Hint
		if len(recs) > 0 {
			output.SubHeader("Next Steps")
			output.Hint("To automatically apply the recommended policies, run:")
			output.Hint(fmt.Sprintf("  %s", buildApplyCommand(recs)))
		}

		fmt.Println()
		return nil
	},
}

func checkKyvernoInstalled() bool {
	checkCmd := exec.Command("kubectl", "get", "crd", "clusterpolicies.kyverno.io", "--no-headers")
	return checkCmd.Run() == nil
}

func discoverExistingPolicies() map[string]string {
	out, err := exec.Command("kubectl", "get", "clusterpolicies", "-o", "jsonpath={range .items[*]}{.metadata.name}={.status.ready}\n").Output()
	policies := make(map[string]string)
	if err != nil {
		return policies
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "=")
		if len(parts) == 2 {
			policies[parts[0]] = parts[1] // true or false
		}
	}
	return policies
}

func generateRecommendations(existing map[string]string) []policyRec {
	recs := []policyRec{
		{
			Name: "Pod Security Standards",
			Description: "Enforce restricted pod security standards (non-root, read-only fs)",
			Recommended: true,
			Reason: "Always recommended for baseline security",
			HelmFlag: "kyvernoPolicy.enabled",
			HelmValue: "true",
		},
		{
			Name: "Workload Standards",
			Description: "Require liveness/readiness probes and standard labels",
			Recommended: true,
			Reason: "Ensures operational reliability",
			HelmFlag: "kyvernoPolicy.enabled",
			HelmValue: "true",
		},
	}

	// Detect if custom registries are used
	regOut, _ := exec.Command("kubectl", "get", "pods", "-A", "-o", "jsonpath={.items[*].spec.containers[*].image}").Output()
	images := string(regOut)
	if strings.Contains(images, "ghcr.io") || strings.Contains(images, "docker.io") {
		recs = append(recs, policyRec{
			Name: "Image Registry Restriction",
			Description: "Restrict workloads to known container registries",
			Recommended: true,
			Reason: "Detected third-party image sources",
			HelmFlag: "kyvernoPolicy.restrictRegistries",
			HelmValue: "true",
		})
	}

	// Detect if NetworkPolicies exist
	netpolOut, _ := exec.Command("kubectl", "get", "netpol", "-A", "--no-headers").Output()
	if len(strings.TrimSpace(string(netpolOut))) == 0 {
		recs = append(recs, policyRec{
			Name: "NetworkPolicy Generation",
			Description: "Auto-generate default-deny network policies for new namespaces",
			Recommended: true,
			Reason: "No existing NetworkPolicies detected",
			HelmFlag: "kyvernoPolicy.networkPolicyGeneration.enabled",
			HelmValue: "true",
		})
	}

	// Detect dev/staging namespaces for PolicyExceptions
	nsOut, _ := exec.Command("kubectl", "get", "ns", "-o", "jsonpath={.items[*].metadata.name}").Output()
	ns := string(nsOut)
	if strings.Contains(ns, "-dev") || strings.Contains(ns, "-staging") || strings.Contains(ns, "sandbox") {
		recs = append(recs, policyRec{
			Name: "Policy Exceptions",
			Description: "Allow relaxations for dev/staging environments",
			Recommended: true,
			Reason: "Detected non-production namespaces",
			HelmFlag: "kyvernoPolicy.policyExceptions.enabled",
			HelmValue: "true",
		})
	}

	// Cosign Verification
	if strings.Contains(images, "ghcr.io") {
		recs = append(recs, policyRec{
			Name: "Cosign Image Verification",
			Description: "Verify container image signatures",
			Recommended: true,
			Reason: "Detected GHCR images which support Cosign signing",
			HelmFlag: "kyvernoPolicy.cosign.enabled",
			HelmValue: "true",
		})
	}

	// Detect Kafka/Strimzi CRDs
	crdOut, _ := exec.Command("kubectl", "get", "crd", "kafkatopics.kafka.strimzi.io", "--no-headers").Output()
	if len(strings.TrimSpace(string(crdOut))) > 0 {
		recs = append(recs, policyRec{
			Name: "Kafka Topic Standards",
			Description: "Enforce minimum replication factors and retention policies on KafkaTopics",
			Recommended: true,
			Reason: "Detected Strimzi Kafka Topic CRDs",
			HelmFlag: "kyvernoPolicy.kafka.enabled",
			HelmValue: "true",
		})
	}

	return recs
}

func renderRecommendations(recs []policyRec, existing map[string]string) {
	output.SubHeader("Policy Recommendations")
	
	rows := [][]string{}
	for _, rec := range recs {
		status := "NOT INSTALLED"
		if rec.Name == "Pod Security Standards" {
			if ready, ok := existing["kates-pod-security-standards"]; ok {
				status = "ACTIVE"
				if ready != "true" {
					status = "DEGRADED"
				}
			}
		} else if rec.Name == "Kafka Topic Standards" {
			if ready, ok := existing["kates-kafka-standards"]; ok {
				status = "ACTIVE"
				if ready != "true" {
					status = "DEGRADED"
				}
			}
		}

		recStr := "No"
		if rec.Recommended {
			recStr = "Yes"
		}

		rows = append(rows, []string{
			rec.Name,
			status,
			recStr,
			rec.Reason,
		})
	}

	output.Table([]string{"Policy", "Status", "Recommended?", "Reason"}, rows)
	fmt.Println()

	// Third party policies
	thirdPartyRows := [][]string{}
	for name, ready := range existing {
		if !strings.HasPrefix(name, "kates-") {
			status := "ACTIVE"
			if ready != "true" {
				status = "DEGRADED"
			}
			thirdPartyRows = append(thirdPartyRows, []string{
				name, status, "User Managed", "Detected in cluster",
			})
		}
	}

	if len(thirdPartyRows) > 0 {
		output.SubHeader("Third-Party Policies Detected")
		output.Table([]string{"Policy", "Status", "Managed By", "Reason"}, thirdPartyRows)
		fmt.Println()
	}
}

func buildApplyCommand(recs []policyRec) string {
	cmd := "kates kyverno apply"
	flags := make(map[string]bool)
	
	for _, rec := range recs {
		if rec.Recommended {
			if rec.HelmFlag == "kyvernoPolicy.networkPolicyGeneration.enabled" {
				flags["--with-netpol"] = true
			} else if rec.HelmFlag == "kyvernoPolicy.cosign.enabled" {
				flags["--with-cosign"] = true
			} else if rec.HelmFlag == "kyvernoPolicy.kafka.enabled" {
				flags["--with-kafka"] = true
			}
		}
	}
	
	if flags["--with-netpol"] {
		cmd += " --with-netpol"
	}
	if flags["--with-cosign"] {
		cmd += " --with-cosign"
	}
	if flags["--with-kafka"] {
		cmd += " --with-kafka"
	}
	
	return cmd
}

func init() {
	kyvernoCmd.AddCommand(kyvernoDetectCmd)
}
