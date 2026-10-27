package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/output"
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
It uses Helm to apply the exact configuration needed.

The policies are values of the kates release, so they go to the kates release
that is installed, wherever a previous deploy put it; --app-ns (or --topology
single --namespace) names the namespace when none is installed, or when
several are.`,
	Example: `  kates kyverno apply
  kates kyverno apply --dry-run
  kates kyverno apply --mode Enforce --yes --with-netpol
  kates kyverno apply --topology single --namespace team`,
	RunE: func(cmd *cobra.Command, args []string) error {
		kc := newKubectl("")
		ctx := context.Background()

		output.Banner("Kyverno Policy Application", "Deploying Cluster Policies")

		if err := resolveChartRoot(ctx, "", false, "kates"); err != nil {
			return err
		}

		// The release's namespace. This used to be a hard-coded -n kates,
		// which installed a second kates release beside a stack deployed
		// with --app-ns or --topology single.
		scoped := false
		for _, name := range []string{"topology", "namespace", "app-ns"} {
			scoped = scoped || cmd.Flags().Changed(name)
		}
		appNS, err := kyvernoReleaseNamespace(ctx, resolveNamespaces().app, scoped)
		if err != nil {
			return err
		}

		// 1. Detect and build Helm args
		helmArgs := []string{"upgrade", "--install", "kates", chartRootPath("charts/kates"),
			"-n", appNS, "--create-namespace",
			"--set", "kyvernoPolicy.enabled=true",
			"--set", "kyvernoPolicy.action=" + kyvernoApplyMode}

		// Basic check for Kyverno itself
		if !kc.CRDExists(ctx, "clusterpolicies.kyverno.io") {
			output.Warn("Kyverno is not installed.")
			if kyvernoApplyDryRun {
				output.Hint("Kyverno " + kyvernoChartVersion + " would be installed first (helm upgrade --install kyverno kyverno/kyverno -n kyverno).")
			} else {
				output.Hint("Installing Kyverno first...")
				// A repo add/update failure surfaces from the install below.
				_ = runHelmFn(ctx, "repo", "add", "kyverno", "https://kyverno.github.io/kyverno/")
				_ = runHelmFn(ctx, "repo", "update", "kyverno")
				if err := ensureConfiguredDeployNamespace(ctx, "kyverno"); err != nil {
					return err
				}
				if err := runHelmFn(ctx, "upgrade", "--install", "kyverno", "kyverno/kyverno",
					"--version", kyvernoChartVersion, "-n", "kyverno", "--create-namespace",
					"--set", "admissionController.replicas=1",
					"--set", "backgroundController.replicas=1",
					"--set", "cleanupController.replicas=1",
					"--set", "reportsController.replicas=1",
					"--timeout", "5m", "--wait"); err != nil {
					output.Error("Failed to install Kyverno: " + err.Error())
					return err
				}
				output.Success("Kyverno Admission Controller deployed")
				if _, err := kc.Run(ctx, "wait", "--for=condition=Established", "crd", "clusterpolicies.kyverno.io", "--timeout=60s"); err != nil {
					output.Warn("The ClusterPolicy CRD is not Established yet: " + err.Error())
				}
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
		devNamespaces := strings.Contains(ns, "-dev") || strings.Contains(ns, "-staging") || strings.Contains(ns, "sandbox")
		if devNamespaces {
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
		if devNamespaces {
			previewRows = append(previewRows, []string{"Dev/Staging Exceptions", "enabled"})
		}
		output.Table([]string{"Policy Group", "State"}, previewRows)
		fmt.Println()

		if kyvernoApplyDryRun {
			// Helm renders the release against the cluster without writing
			// it, so a chart or values error shows here and not on the real
			// run. The flag used to return before this call, a no-op.
			out, err := runExecCombinedFn(ctx, "helm", helmArgs...)
			if err != nil {
				output.Error("Dry run failed: " + strings.TrimSpace(string(out)))
				return cmdErr("helm --dry-run: " + err.Error())
			}
			output.Success(fmt.Sprintf("Dry run complete: Helm rendered %d resources for release kates in %s; nothing was applied.", countRenderedKinds(out), appNS))
			return nil
		}

		if !kyvernoApplyYes {
			// The old prompt had no TTY guard — a piped run hit Scanln, read
			// EOF, and "aborted" with exit 0, indistinguishable from success.
			// confirm() refuses without a terminal, and declining is now
			// non-zero per the exit-code contract.
			ok, err := confirm("Apply these policies?")
			if err != nil {
				return cmdErr("aborted: " + err.Error())
			}
			if !ok {
				return cmdErr("aborted: policies not applied")
			}
		}

		// 2. Apply via Helm
		if err := ensureConfiguredDeployNamespace(ctx, appNS); err != nil {
			return err
		}
		output.Hint("Applying policies...")
		if err := runHelmFn(ctx, helmArgs...); err != nil {
			output.Error("Failed to apply policies")
			return err
		}

		output.Success("Policies applied successfully")

		// 3. Validation Summary
		output.Hint("Waiting for policies to be ready...")
		time.Sleep(3 * time.Second) // wait for admission controller to process CRs

		statusOut, _ := kc.Output(ctx, "get", "clusterpolicies", "--no-headers")
		// One line per policy; strings.Split counted empty output as one.
		count := 0
		if lines := strings.TrimSpace(string(statusOut)); lines != "" {
			count = len(strings.Split(lines, "\n"))
		}

		output.Success(fmt.Sprintf("%d policies are now active in %s mode.", count, kyvernoApplyMode))
		output.Hint("Run 'kates kyverno status' for details.")
		fmt.Println()

		return nil
	},
}

// kyvernoReleaseNamespace is the namespace of the kates release the policies
// go into: the one the flags name when they were given (scoped), else the
// one installed, else the flags' default. Two installed releases and no flag
// is a question for the user.
func kyvernoReleaseNamespace(ctx context.Context, flagNS string, scoped bool) (string, error) {
	if scoped {
		return flagNS, nil
	}
	installed := deployedReleases(ctx, []DeploySummaryEntry{{Release: "kates", Namespace: flagNS}})
	switch len(installed) {
	case 0:
		return flagNS, nil
	case 1:
		if installed[0].Namespace != flagNS {
			output.Hint("Kates is installed in " + installed[0].Namespace + "; the policies go to that release.")
		}
		return installed[0].Namespace, nil
	}
	namespaces := make([]string, len(installed))
	for i, r := range installed {
		namespaces[i] = r.Namespace
	}
	return "", cmdErr("kates is installed in " + strings.Join(namespaces, " and ") + ": pass --app-ns (or --topology single --namespace) to say which release gets the policies")
}

// countRenderedKinds counts the objects in helm's rendered manifest.
func countRenderedKinds(manifest []byte) int {
	n := 0
	for _, line := range strings.Split(string(manifest), "\n") {
		if strings.HasPrefix(line, "kind: ") {
			n++
		}
	}
	return n
}

func init() {
	kyvernoApplyCmd.Flags().StringVar(&kyvernoApplyMode, "mode", "Audit", "Validation mode: Audit or Enforce")
	kyvernoApplyCmd.Flags().BoolVar(&kyvernoApplyCosign, "with-cosign", false, "Enable Cosign image signature verification")
	kyvernoApplyCmd.Flags().BoolVar(&kyvernoApplyNetpol, "with-netpol", false, "Enable NetworkPolicy generation")
	kyvernoApplyCmd.Flags().BoolVar(&kyvernoApplyKafka, "with-kafka", false, "Enable Kafka-specific policies (Strimzi)")
	kyvernoApplyCmd.Flags().BoolVarP(&kyvernoApplyYes, "yes", "y", false, "Skip confirmation prompt")
	kyvernoApplyCmd.Flags().BoolVar(&kyvernoApplyDryRun, "dry-run", false, "Render the release with helm --dry-run and apply nothing")
	addLabelsFileFlag(kyvernoApplyCmd)
	// Where the kates release is (deploy_flags.go): the same flags as deploy.
	addDeployScopeFlags(kyvernoApplyCmd)

	kyvernoCmd.AddCommand(kyvernoApplyCmd)
}
