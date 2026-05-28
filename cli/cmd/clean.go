package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove all Kates-managed resources and namespaces",
	Long: `Tears down the entire Kates stack by uninstalling all Helm releases
and deleting all managed namespaces. This is the inverse of 'kates deploy'.

Examples:
  kates clean            # interactive, with confirmation
  kates clean --force    # skip confirmation prompt`,
	RunE: runClean,
}

var (
	cleanForce         bool
	cleanVerbose       bool
	cleanTopology      string
	cleanNamespace     string
	cleanKafkaNS       string
	cleanDbNS          string
	cleanAppNS         string
	cleanChaosNS       string
	cleanMonitoringNS  string
)

func init() {
	cleanCmd.Flags().BoolVar(&cleanForce, "force", false, "Skip confirmation prompt")
	cleanCmd.Flags().BoolVarP(&cleanVerbose, "verbose", "v", false, "Show full command output during cleanup")
	cleanCmd.Flags().StringVar(&cleanTopology, "topology", "", "Topology to clean: 'isolated' or 'single'. If empty, cleans both.")
	cleanCmd.Flags().StringVar(&cleanNamespace, "namespace", "kates-stack", "Target namespace when topology is 'single'")
	cleanCmd.Flags().StringVar(&cleanKafkaNS, "kafka-ns", "kafka", "Namespace for Kafka when topology is 'isolated'")
	cleanCmd.Flags().StringVar(&cleanDbNS, "db-ns", "database", "Namespace for PostgreSQL Database when topology is 'isolated'")
	cleanCmd.Flags().StringVar(&cleanAppNS, "app-ns", "kates", "Namespace for Kates Backend when topology is 'isolated'")
	cleanCmd.Flags().StringVar(&cleanChaosNS, "chaos-ns", "litmus", "Namespace for Chaos Engine when topology is 'isolated'")
	cleanCmd.Flags().StringVar(&cleanMonitoringNS, "monitoring-ns", "monitoring", "Namespace for monitoring components when topology is 'isolated'")
	rootCmd.AddCommand(cleanCmd)
}

// helmRelease pairs a release name with its namespace.
type helmRelease struct {
	Name      string
	Namespace string
}

var (
	cleanRunFn       = cleanRunDefault
	cleanRunOutputFn = cleanRunOutputDefault
	cleanSleepFn     = time.Sleep
)

func cleanRunDefault(ctx context.Context, name string, args ...string) error {
	if cleanVerbose {
		fmt.Printf("    \033[2m$ %s %s\033[0m\n", name, strings.Join(args, " "))
	}
	cmd := exec.CommandContext(ctx, name, args...)
	if cleanVerbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd.Run()
}

func cleanRunOutputDefault(ctx context.Context, name string, args ...string) ([]byte, error) {
	if cleanVerbose {
		fmt.Printf("    \033[2m$ %s %s\033[0m\n", name, strings.Join(args, " "))
	}
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if cleanVerbose && len(out) > 0 {
		fmt.Print(string(out))
	}
	return out, err
}

func cleanRun(ctx context.Context, name string, args ...string) error {
	return cleanRunFn(ctx, name, args...)
}

func cleanRunOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return cleanRunOutputFn(ctx, name, args...)
}

func runClean(cmd *cobra.Command, args []string) error {
	startTime := time.Now()
	// ── Banner ──
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrRed).
		Render("⎈ Kates Clean — Teardown"))
	fmt.Println(lipgloss.NewStyle().Foreground(clrDim).
		Render(strings.Repeat("─", 35)))

	// ── Clean up active port forwards ──
	fmt.Printf("    %s Terminating background port-forwards...\n", lipgloss.NewStyle().Foreground(clrDim).Render("🧹"))
	myPid := os.Getpid()
	if out, err := exec.Command("pgrep", "-f", "ports").Output(); err == nil {
		for _, pidStr := range strings.Fields(string(out)) {
			var pid int
			if _, err := fmt.Sscanf(pidStr, "%d", &pid); err == nil && pid != myPid {
				if cmdOut, err := exec.Command("ps", "-p", pidStr, "-o", "command=").Output(); err == nil {
					cmdLine := string(cmdOut)
					if strings.Contains(cmdLine, "kates") {
						_ = syscall.Kill(pid, syscall.SIGTERM)
					}
				}
			}
		}
	}
	_ = exec.Command("pkill", "-f", "kubectl port-forward").Run()
	time.Sleep(500 * time.Millisecond)

	// Operator CRD resource types that may carry finalizers or webhooks.
	operatorCRDTypes := []string{
		// Strimzi
		"kafkas.kafka.strimzi.io",
		"kafkatopics.kafka.strimzi.io",
		"kafkausers.kafka.strimzi.io",
		"kafkaconnects.kafka.strimzi.io",
		"kafkabridges.kafka.strimzi.io",
		// Litmus Chaos
		"chaosengines.litmuschaos.io",
		"chaosexperiments.litmuschaos.io",
		"chaosresults.litmuschaos.io",
		// Cert-Manager
		"certificates.cert-manager.io",
		"issuers.cert-manager.io",
		"clusterissuers.cert-manager.io",
		// Kyverno
		"clusterpolicies.kyverno.io",
		"policies.kyverno.io",
	}

	// CustomResourceDefinitions (CRDs) deployed by the Kates stack.
	allCRDs := []string{
		// Strimzi CRDs
		"kafkabridges.kafka.strimzi.io",
		"kafkaconnectors.kafka.strimzi.io",
		"kafkaconnects.kafka.strimzi.io",
		"kafkamirrormaker2s.kafka.strimzi.io",
		"kafkanodepools.kafka.strimzi.io",
		"kafkarebalances.kafka.strimzi.io",
		"kafkas.kafka.strimzi.io",
		"kafkatopics.kafka.strimzi.io",
		"kafkausers.kafka.strimzi.io",
		"strimzipodsets.core.strimzi.io",

		// Litmus Chaos CRDs
		"chaosengines.litmuschaos.io",
		"chaosexperiments.litmuschaos.io",
		"chaosresults.litmuschaos.io",

		// Monitoring (Prometheus) CRDs
		"alertmanagerconfigs.monitoring.coreos.com",
		"alertmanagers.monitoring.coreos.com",
		"podmonitors.monitoring.coreos.com",
		"probes.monitoring.coreos.com",
		"prometheusagents.monitoring.coreos.com",
		"prometheuses.monitoring.coreos.com",
		"prometheusrules.monitoring.coreos.com",
		"scrapeconfigs.monitoring.coreos.com",
		"servicemonitors.monitoring.coreos.com",
		"thanosrulers.monitoring.coreos.com",

		// Cert-Manager CRDs
		"certificaterequests.cert-manager.io",
		"certificates.cert-manager.io",
		"challenges.acme.cert-manager.io",
		"clusterissuers.cert-manager.io",
		"issuers.cert-manager.io",
		"orders.acme.cert-manager.io",

		// Kyverno CRDs
		"admissionreports.kyverno.io",
		"backgroundscanreports.kyverno.io",
		"clusteradmissionreports.kyverno.io",
		"clusterbackgroundscanreports.kyverno.io",
		"cleanuppolicies.kyverno.io",
		"clustercleanuppolicies.kyverno.io",
		"clusterpolicies.kyverno.io",
		"policies.kyverno.io",
		"policyexceptions.kyverno.io",
		"updaterequests.kyverno.io",
	}

	var appReleases []helmRelease
	var coreReleases []helmRelease
	var managedNamespaces []string
	var strimziNS []string

	if cleanTopology == "single" || cleanTopology == "" {
		appReleases = append(appReleases,
			helmRelease{"chaos", cleanNamespace},
			helmRelease{"kates", cleanNamespace},
			helmRelease{"apicurio", cleanNamespace},
		)
		coreReleases = append(coreReleases,
			helmRelease{"jaeger", cleanNamespace},
			helmRelease{"krafter", cleanNamespace},
			helmRelease{"monitoring", cleanNamespace},
			helmRelease{"postgresql", cleanNamespace},
		)
		managedNamespaces = append(managedNamespaces, cleanNamespace)
		strimziNS = append(strimziNS, cleanNamespace)
	}

	if cleanTopology == "isolated" || cleanTopology == "" {
		appReleases = append(appReleases,
			helmRelease{"chaos", cleanChaosNS},
			helmRelease{"kates", cleanAppNS},
			helmRelease{"apicurio", cleanKafkaNS},
		)
		coreReleases = append(coreReleases,
			helmRelease{"jaeger", cleanMonitoringNS},
			helmRelease{"krafter", cleanKafkaNS},
			helmRelease{"monitoring", cleanMonitoringNS},
			helmRelease{"postgresql", cleanDbNS},
		)
		managedNamespaces = append(managedNamespaces,
			cleanKafkaNS, cleanAppNS, cleanChaosNS, cleanMonitoringNS, cleanDbNS,
		)
		strimziNS = append(strimziNS, cleanKafkaNS)
	}

	operatorReleases := []helmRelease{
		{"cert-manager", "cert-manager"},
		{"kyverno", "kyverno"},
		{"strimzi-operator", "strimzi-operator"},
	}

	allReleases := append(append(appReleases, coreReleases...), operatorReleases...)

	managedNamespaces = append(managedNamespaces,
		"strimzi-operator", "cert-manager", "kyverno",
	)

	clusterResources := []struct{ Kind, Name string }{
		{"clusterrole", "kates"}, {"clusterrolebinding", "kates"},
		{"clusterrole", "litmus"}, {"clusterrolebinding", "litmus"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// ── Discovery ──
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Foreground(clrText).Render("  Scanning cluster..."))

	var installed []helmRelease
	for _, r := range allReleases {
		c, cn := context.WithTimeout(ctx, 3*time.Second)
		if cleanRun(c, "helm", "status", r.Name, "-n", r.Namespace) == nil {
			installed = append(installed, r)
		}
		cn()
	}

	var existingNS []string
	for _, ns := range managedNamespaces {
		c, cn := context.WithTimeout(ctx, 3*time.Second)
		if cleanRun(c, "kubectl", "get", "namespace", ns) == nil {
			existingNS = append(existingNS, ns)
		}
		cn()
	}

	var existingCRDs []string
	for _, crd := range allCRDs {
		c, cn := context.WithTimeout(ctx, 3*time.Second)
		if cleanRun(c, "kubectl", "get", "crd", crd) == nil {
			existingCRDs = append(existingCRDs, crd)
		}
		cn()
	}

	if len(installed) == 0 && len(existingNS) == 0 && len(existingCRDs) == 0 {
		fmt.Println(lipgloss.NewStyle().Foreground(clrGreen).Bold(true).
			Render("  ✓ Cluster is already clean. Nothing to do."))
		fmt.Println()
		return nil
	}

	// ── Show what will be removed ──
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrOrange).
		Render("  The following resources will be removed:"))

	nameStyle := lipgloss.NewStyle().Bold(true).Foreground(clrText)
	dimStyle := lipgloss.NewStyle().Foreground(clrDim)
	bulletStyle := lipgloss.NewStyle().Foreground(clrRed)

	if len(installed) > 0 {
		fmt.Println()
		fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrCyan).Render("  Helm Releases:"))
		for _, r := range installed {
			fmt.Printf("  %s %s %s\n",
				bulletStyle.Render("✖"),
				nameStyle.Render(fmt.Sprintf("%-20s", r.Name)),
				dimStyle.Render(fmt.Sprintf("→ %s", r.Namespace)),
			)
		}
	}

	if len(existingNS) > 0 {
		fmt.Println()
		fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrCyan).Render("  Namespaces:"))
		for _, ns := range existingNS {
			fmt.Printf("  %s %s\n",
				bulletStyle.Render("✖"),
				nameStyle.Render(ns),
			)
		}
	}

	if len(existingCRDs) > 0 {
		fmt.Println()
		fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrCyan).Render("  CustomResourceDefinitions (CRDs):"))
		for _, crd := range existingCRDs {
			fmt.Printf("  %s %s\n",
				bulletStyle.Render("✖"),
				nameStyle.Render(crd),
			)
		}
	}
	fmt.Println()

	// ── Confirmation ──
	if !cleanForce {
		var confirmed bool
		err := huh.NewConfirm().
			Title("Are you sure you want to delete all Kates resources?").
			Description("This action cannot be undone.").
			Affirmative("Yes, clean everything").
			Negative("Cancel").
			Value(&confirmed).
			WithTheme(ThemeKates()).
			Run()
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Println(lipgloss.NewStyle().Foreground(clrDim).Render("  Cancelled."))
			return nil
		}
	}

	fmt.Println()

	okStyle := lipgloss.NewStyle().Foreground(clrGreen).Bold(true)
	errStyle := lipgloss.NewStyle().Foreground(clrRed)

	// ── 1. Delete Operator CRs FIRST (while operators are still running) ──
	// Operators handle finalizer removal. If we delete operators first,
	// finalizers become orphaned and namespaces hang in Terminating forever.
	fmt.Println(lipgloss.NewStyle().Foreground(clrCyan).Bold(true).
		Render("  Step 1: Removing operator custom resources..."))
	for _, crdType := range operatorCRDTypes {
		for _, ns := range managedNamespaces {
			dCtx, dCancel := context.WithTimeout(ctx, 30*time.Second)
			cleanRun(dCtx, "kubectl", "delete", crdType, "--all", "-n", ns, "--ignore-not-found")
			dCancel()
		}
	}
	// Wait briefly for operators to process finalizer removal.
	cleanSleepFn(5 * time.Second)

	// ── 2. Strip orphaned finalizers on any remaining stuck resources ──
	fmt.Println(lipgloss.NewStyle().Foreground(clrCyan).Bold(true).
		Render("  Step 2: Stripping orphaned finalizers..."))
	for _, crdType := range operatorCRDTypes {
		for _, ns := range managedNamespaces {
			pCtx, pCancel := context.WithTimeout(ctx, 10*time.Second)
			// Get any remaining resources and patch their finalizers to empty
			out, _ := cleanRunOutput(pCtx, "kubectl", "get", crdType, "-n", ns, "-o", "jsonpath={.items[*].metadata.name}")
			pCancel()
			names := strings.Fields(strings.TrimSpace(string(out)))
			for _, name := range names {
				fCtx, fCancel := context.WithTimeout(ctx, 5*time.Second)
				cleanRun(fCtx, "kubectl", "patch", crdType, name, "-n", ns,
					"--type", "merge", "-p", `{"metadata":{"finalizers":[]}}`)
				fCancel()
			}
		}
	}

	// ── 3. Uninstall Helm releases (apps → core → operators) ──
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Foreground(clrCyan).Bold(true).
		Render("  Step 3: Uninstalling Helm releases..."))
	for _, r := range installed {
		label := fmt.Sprintf("  Uninstalling %-16s from %s", r.Name, r.Namespace)
		fmt.Print(lipgloss.NewStyle().Foreground(clrText).Render(label))

		uCtx, uCancel := context.WithTimeout(ctx, 2*time.Minute)
		out, err := cleanRunOutput(uCtx, "helm", "uninstall", r.Name, "-n", r.Namespace)
		uCancel()

		if err != nil {
			fmt.Println(errStyle.Render("  ✖ " + strings.TrimSpace(string(out))))
		} else {
			fmt.Println(okStyle.Render("  ✔"))
		}
	}

	// ── 4. Delete cluster-scoped resources ──
	for _, cr := range clusterResources {
		dCtx, dCancel := context.WithTimeout(ctx, 10*time.Second)
		cleanRun(dCtx, "kubectl", "delete", cr.Kind, cr.Name, "--ignore-not-found")
		dCancel()
	}

	// ── 5. Delete namespaces ──
	if len(existingNS) > 0 {
		fmt.Println()
		fmt.Println(lipgloss.NewStyle().Foreground(clrCyan).Bold(true).
			Render("  Step 4: Deleting namespaces..."))
		for _, ns := range existingNS {
			label := fmt.Sprintf("  Deleting namespace %s", ns)
			fmt.Print(lipgloss.NewStyle().Foreground(clrText).Render(label))

			nCtx, nCancel := context.WithTimeout(ctx, 3*time.Minute)
			err := cleanRun(nCtx, "kubectl", "delete", "namespace", ns, "--ignore-not-found")
			nCancel()

			if err != nil {
				fmt.Println(lipgloss.NewStyle().Foreground(clrOrange).Render("  ⚠ still terminating"))
			} else {
				fmt.Println(okStyle.Render("  ✔"))
			}
		}
	}

	// ── 6. Delete CustomResourceDefinitions (CRDs) ──
	if len(existingCRDs) > 0 {
		fmt.Println()
		fmt.Println(lipgloss.NewStyle().Foreground(clrCyan).Bold(true).
			Render("  Step 5: Deleting CustomResourceDefinitions (CRDs)..."))
		for _, crd := range existingCRDs {
			label := fmt.Sprintf("  Deleting CRD %s", crd)
			fmt.Print(lipgloss.NewStyle().Foreground(clrText).Render(label))

			cCtx, cCancel := context.WithTimeout(ctx, 30*time.Second)
			err := cleanRun(cCtx, "kubectl", "delete", "crd", crd, "--ignore-not-found")
			cCancel()

			if err != nil {
				fmt.Println(errStyle.Render("  ✖"))
			} else {
				fmt.Println(okStyle.Render("  ✔"))
			}
		}
	}

	// ── Done ──
	fmt.Println()
	
	elapsed := time.Since(startTime).Round(time.Second)
	
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrGreen).
		Render(fmt.Sprintf("  ✅ Cluster cleaned successfully in %s.", elapsed)))
	fmt.Println()
	return nil
}

