package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/klster/kates-cli/internal/helm"
	"github.com/klster/kates-cli/internal/kubectl"
	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var (
	kyvernoApplyMode   string
	kyvernoApplyCosign bool
	kyvernoApplyNetpol bool
	kyvernoApplyKafka  bool
	kyvernoApplyYes    bool
	kyvernoApplyDryRun bool
)

var kyvernoApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Automatically apply Kyverno policies based on cluster detection",
	Long: `Applies Kyverno policies (and installs Kyverno if missing) by auto-detecting cluster state.
It uses Helm to apply the exact configuration needed.`,
	Example: `  kates kyverno apply
  kates kyverno apply --dry-run
  kates kyverno apply --mode Enforce --yes --with-netpol`,
	RunE: func(cmd *cobra.Command, args []string) error {
		kc := kubectl.New("")
		ctx := context.Background()

		output.Banner("Kyverno Policy Application", "Deploying Cluster Policies")

		// 1. Detect and build Helm args
		helmArgs := []string{"upgrade", "--install", "kates", "./charts/kates",
			"-n", "kates", "--create-namespace",
			"--set", "kyvernoPolicy.enabled=true",
			"--set", "kyvernoPolicy.action=" + kyvernoApplyMode}

		// Basic check for Kyverno itself
		if !checkKyvernoInstalled() {
			output.Warn("Kyverno is not installed.")
			output.Hint("Installing Kyverno first...")

			if !kyvernoApplyDryRun {
				hc := helm.New("")
				hc.Run(ctx, "repo", "add", "kyverno", "https://kyverno.github.io/kyverno/")
				hc.Run(ctx, "repo", "update", "kyverno")
				out, err := hc.Run(ctx, "upgrade", "--install", "kyverno", "kyverno/kyverno",
					"--version", "3.6.4", "-n", "kyverno", "--create-namespace",
					"--set", "admissionController.replicas=1",
					"--set", "backgroundController.replicas=1",
					"--set", "cleanupController.replicas=1",
					"--set", "reportsController.replicas=1",
					"--timeout", "5m", "--wait")
				if err != nil {
					output.Error("Failed to install Kyverno: " + out + err.Error())
					return err
				}
				output.Success("Kyverno Admission Controller deployed")
				kc.Run(ctx, "wait", "--for=condition=Established", "crd", "clusterpolicies.kyverno.io", "--timeout=60s")
			}
		}

		if kyvernoApplyCosign {
			helmArgs = append(helmArgs, "--set", "kyvernoPolicy.cosign.enabled=true")
		}
		if kyvernoApplyNetpol {
			helmArgs = append(helmArgs, "--set", "kyvernoPolicy.networkPolicyGeneration.enabled=true")
		}

		if kyvernoApplyKafka {
			helmArgs = append(helmArgs, "--set", "kyvernoPolicy.kafka.enabled=true")
		}

		// Also check dev namespaces
		nsOut, _ := kc.Output(ctx, "get", "ns", "-o", "jsonpath={.items[*].metadata.name}")
		ns := string(nsOut)
		if strings.Contains(ns, "-dev") || strings.Contains(ns, "-staging") || strings.Contains(ns, "sandbox") {
			helmArgs = append(helmArgs, "--set", "kyvernoPolicy.policyExceptions.enabled=true")
		}

		if kyvernoApplyDryRun {
			helmArgs = append(helmArgs, "--dry-run")
		}

		// Display what will be applied
		output.SubHeader("Helm Command")
		output.Hint("helm " + strings.Join(helmArgs, " "))
		fmt.Println()

		// Render interactive preview table
		output.SubHeader("Policies to Apply")
		previewRows := [][]string{
			{"Pod Security Standards", "enabled"},
			{"Workload Standards", "enabled"},
		}
		if kyvernoApplyCosign {
			previewRows = append(previewRows, []string{"Cosign Verification", "enabled"})
		}
		if kyvernoApplyNetpol {
			previewRows = append(previewRows, []string{"NetworkPolicy Generation", "enabled"})
		}
		if kyvernoApplyKafka {
			previewRows = append(previewRows, []string{"Kafka Topic Standards", "enabled"})
		}
		if strings.Contains(ns, "-dev") || strings.Contains(ns, "-staging") || strings.Contains(ns, "sandbox") {
			previewRows = append(previewRows, []string{"Dev/Staging Exceptions", "enabled"})
		}
		output.Table([]string{"Policy Group", "State"}, previewRows)
		fmt.Println()

		if kyvernoApplyDryRun {
			output.Success("Dry run complete.")
			return nil
		}

		if !kyvernoApplyYes {
			fmt.Print("Apply these policies? [y/N]: ")
			var response string
			fmt.Scanln(&response)
			if strings.ToLower(strings.TrimSpace(response)) != "y" {
				output.Warn("Aborted by user.")
				return nil
			}
		}

		// 2. Apply via Helm
		output.Hint("Applying policies...")
		hc := helm.New("")
		hc.Verbose = true
		if _, err := hc.Run(ctx, helmArgs...); err != nil {
			output.Error("Failed to apply policies")
			return err
		}

		output.Success("Policies applied successfully")

		// 3. Validation Summary
		output.Hint("Waiting for policies to be ready...")
		time.Sleep(3 * time.Second) // wait for admission controller to process CRs

		statusOut, _ := kc.Output(ctx, "get", "clusterpolicies", "--no-headers")
		count := len(strings.Split(strings.TrimSpace(string(statusOut)), "\n"))

		output.Success(fmt.Sprintf("%d policies are now active in %s mode.", count, kyvernoApplyMode))
		output.Hint("Run 'kates kyverno status' for details.")
		fmt.Println()

		return nil
	},
}

func init() {
	kyvernoApplyCmd.Flags().StringVar(&kyvernoApplyMode, "mode", "Audit", "Validation mode: Audit or Enforce")
	kyvernoApplyCmd.Flags().BoolVar(&kyvernoApplyCosign, "with-cosign", false, "Enable Cosign image signature verification")
	kyvernoApplyCmd.Flags().BoolVar(&kyvernoApplyNetpol, "with-netpol", false, "Enable NetworkPolicy generation")
	kyvernoApplyCmd.Flags().BoolVar(&kyvernoApplyKafka, "with-kafka", false, "Enable Kafka-specific policies (Strimzi)")
	kyvernoApplyCmd.Flags().BoolVarP(&kyvernoApplyYes, "yes", "y", false, "Skip confirmation prompt")
	kyvernoApplyCmd.Flags().BoolVar(&kyvernoApplyDryRun, "dry-run", false, "Show what would be applied without executing")

	kyvernoCmd.AddCommand(kyvernoApplyCmd)
}
