package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/internal/kubectl"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var doctorDnsFix bool

var doctorDnsCmd = &cobra.Command{
	Use:   "dns",
	Short: "Diagnose and fix cluster DNS configurations",
	Long: `Diagnose cluster DNS configuration (like ndots and search domains) and
automatically adapt existing Kates deployments to resolve cross-namespace
Kafka broker connectivity issues.

Examples:
  # Check DNS configuration without applying fixes
  kates doctor dns

  # Diagnose and fix existing deployments
  kates doctor dns --fix`,
	RunE: runDoctorDns,
}

func init() {
	doctorDnsCmd.Flags().BoolVar(&doctorDnsFix, "fix", false, "Automatically patch Deployments and KafkaConnect CRs to add required DNS search domains")
	doctorDnsCmd.Flags().StringVar(&doctorKafkaNS, "kafka-ns", "kafka", "Namespace for Kafka brokers")
	doctorDnsCmd.Flags().StringVar(&doctorConnectNS, "connect-ns", "connect", "Namespace for Kafka Connect")
	doctorDnsCmd.Flags().StringVar(&doctorAppNS, "app-ns", "kates", "Namespace for Kates application")

	// Ensure we only add it if not already added by another init (though flags can be redefined if on different commands)
	doctorCmd.AddCommand(doctorDnsCmd)
}

func runDoctorDns(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	kc := newKubectl("")

	title := lipgloss.NewStyle().Bold(true).Foreground(clrText)
	separator := lipgloss.NewStyle().Foreground(clrDim).Render(strings.Repeat("━", 50))
	fail := lipgloss.NewStyle().Foreground(clrRed).Render("❌")
	info := lipgloss.NewStyle().Foreground(clrCyan).Render("ℹ️ ")

	fmt.Println()
	fmt.Println(title.Render("🩺 Kates DNS Doctor"))
	fmt.Println(separator)
	fmt.Println()

	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrCyan).Render("📡 Cluster DNS Configuration"))

	// 1. Diagnose cluster DNS configuration
	podName := fmt.Sprintf("kates-dns-test-%d", rand.Intn(100000))

	// We use the "default" namespace because the application namespace might not exist yet
	resolvConfOut, err := kc.Combined(ctx, "run", "--rm", "-i", "--restart=Never",
		"--image=busybox:1.36", podName, "-n", "default",
		"--", "cat", "/etc/resolv.conf")

	if err != nil {
		fmt.Printf("  %s Failed to run ephemeral pod in namespace default: %v\n", fail, err)
		fmt.Printf("  Output: %s\n", string(resolvConfOut))
		return err
	}

	searches, ndots := parseResolvConf(string(resolvConfOut))

	fmt.Printf("  %s Default Search Domains: %s\n", info, strings.Join(searches, ", "))
	if ndots != "" {
		fmt.Printf("  %s Default ndots: %s\n", info, ndots)
	}

	clusterDomain := clusterDomainFromSearches(searches)

	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrCyan).Render("🔍 Deployment Audit & Adaptation"))

	requiredKafkaSearch := fmt.Sprintf("%s.svc.%s", doctorKafkaNS, clusterDomain)

	// We need to check if connect and kates are in a different namespace than kafka
	components := []dnsAuditComponent{
		{label: "kates backend", patchName: "kates deployment", ns: doctorAppNS,
			check: func(ctx context.Context, kc *kubectl.Client, ns, required string) bool {
				return checkDeploymentDNS(ctx, kc, "kates", ns, required)
			},
			patch: func(ctx context.Context, kc *kubectl.Client, ns, required string) error {
				return patchDeploymentDNS(ctx, kc, "kates", ns, required)
			}},
		{label: "connect-cluster", patchName: "connect-cluster", ns: doctorConnectNS,
			check: func(ctx context.Context, kc *kubectl.Client, ns, required string) bool {
				return checkKafkaConnectDNS(ctx, kc, "connect-cluster", ns, required)
			},
			patch: func(ctx context.Context, kc *kubectl.Client, ns, required string) error {
				return patchKafkaConnectDNS(ctx, kc, "connect-cluster", ns, required)
			}},
	}
	var fixCount int
	for _, c := range components {
		if auditComponentDNS(ctx, kc, c, requiredKafkaSearch) {
			fixCount++
		}
	}
	checkCount := len(components)

	fmt.Println(separator)
	if doctorDnsFix {
		fmt.Printf("📊 Summary: Audited %d components. Applied %d fixes.\n", checkCount, fixCount)
	} else {
		fmt.Printf("📊 Summary: Audited %d components. Run with --fix to apply missing adaptations.\n", checkCount)
	}

	return nil
}

// parseResolvConf reads the search domains and the ndots option from a
// resolv.conf.
func parseResolvConf(content string) (searches []string, ndots string) {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "search ") {
			searches = strings.Fields(line)[1:]
		}
		if strings.HasPrefix(line, "options ") {
			opts := strings.Fields(line)[1:]
			for _, opt := range opts {
				if strings.HasPrefix(opt, "ndots:") {
					ndots = strings.TrimPrefix(opt, "ndots:")
				}
			}
		}
	}
	return searches, ndots
}

// clusterDomainFromSearches is the cluster's DNS domain: the last search
// domain that is neither a namespace's svc domain nor "default", else
// cluster.local.
func clusterDomainFromSearches(searches []string) string {
	clusterDomain := "cluster.local"
	for _, s := range searches {
		if !strings.Contains(s, "svc.") && s != "default" {
			clusterDomain = s
		}
	}
	return clusterDomain
}

// dnsAuditComponent is a workload doctor dns audits: its label, the name a
// patch reports, its namespace, and how to check and patch its search
// domains.
type dnsAuditComponent struct {
	label     string
	patchName string
	ns        string
	check     func(ctx context.Context, kc *kubectl.Client, ns, required string) bool
	patch     func(ctx context.Context, kc *kubectl.Client, ns, required string) error
}

// auditComponentDNS checks one component's search domains and, with --fix,
// patches a missing one in. It reports whether it applied a fix.
func auditComponentDNS(ctx context.Context, kc *kubectl.Client, c dnsAuditComponent, required string) bool {
	success := lipgloss.NewStyle().Foreground(clrGreen).Render("✅")
	warning := lipgloss.NewStyle().Foreground(clrOrange).Render("⚠️ ")
	fail := lipgloss.NewStyle().Foreground(clrRed).Render("❌")

	if c.ns == doctorKafkaNS {
		fmt.Printf("  %s %s is in the same namespace as Kafka, no DNS search domain adaptation needed.\n", success, c.label)
		return false
	}
	if !c.check(ctx, kc, c.ns, required) {
		fmt.Printf("  %s %s (namespace: %s) has correct DNS search domain.\n", success, c.label, c.ns)
		return false
	}
	if !doctorDnsFix {
		fmt.Printf("  %s %s (namespace: %s) is missing DNS search domain (run with --fix to resolve).\n", warning, c.label, c.ns)
		return false
	}
	fmt.Printf("  %s %s (namespace: %s) is missing DNS search domain.\n", warning, c.label, c.ns)
	if err := c.patch(ctx, kc, c.ns, required); err != nil {
		fmt.Printf("     %s Failed to patch %s: %v\n", fail, c.patchName, err)
		return false
	}
	fmt.Printf("     %s Successfully patched %s.\n", success, c.patchName)
	return true
}

func checkDeploymentDNS(ctx context.Context, kc *kubectl.Client, name, ns, requiredSearch string) bool {
	out, err := kc.Output(ctx, "get", "deployment", name, "-n", ns, "-o", "jsonpath={.spec.template.spec.dnsConfig.searches}")
	if err != nil {
		// Ignore if deployment doesn't exist
		return false
	}
	return !strings.Contains(string(out), requiredSearch)
}

func checkKafkaConnectDNS(ctx context.Context, kc *kubectl.Client, name, ns, requiredSearch string) bool {
	out, err := kc.Output(ctx, "get", "kafkaconnect", name, "-n", ns, "-o", "jsonpath={.spec.template.pod.dnsConfig.searches}")
	if err != nil {
		return false
	}
	return !strings.Contains(string(out), requiredSearch)
}

// currentDNSSearches returns the search domains the resource has at jsonPath.
// kubectl prints a list as a JSON array, and nothing for a missing field.
func currentDNSSearches(ctx context.Context, kc *kubectl.Client, kind, name, ns, jsonPath string) ([]string, error) {
	out, err := kc.Output(ctx, "get", kind, name, "-n", ns, "-o", "jsonpath="+jsonPath)
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, nil
	}
	var searches []string
	if err := json.Unmarshal([]byte(raw), &searches); err != nil {
		return nil, fmt.Errorf("parsing %s %s/%s dnsConfig.searches %q: %w", kind, ns, name, raw, err)
	}
	return searches, nil
}

// dnsSearchesPatch returns the searches list the patch sets: the current one
// with searchDomain appended. Both a strategic and a merge patch replace the
// whole list, so patching ["<domain>"] alone dropped every entry the resource
// had.
func dnsSearchesPatch(current []string, searchDomain string) ([]byte, error) {
	searches := append([]string{}, current...)
	if !slices.Contains(searches, searchDomain) {
		searches = append(searches, searchDomain)
	}
	return json.Marshal(searches)
}

func patchDeploymentDNS(ctx context.Context, kc *kubectl.Client, name, ns, searchDomain string) error {
	current, err := currentDNSSearches(ctx, kc, "deployment", name, ns, "{.spec.template.spec.dnsConfig.searches}")
	if err != nil {
		return err
	}
	searches, err := dnsSearchesPatch(current, searchDomain)
	if err != nil {
		return err
	}
	patch := fmt.Sprintf(`{"spec":{"template":{"spec":{"dnsConfig":{"searches":%s}}}}}`, searches)
	_, err = kc.Run(ctx, "patch", "deployment", name, "-n", ns, "--type=strategic", "-p", patch)
	return err
}

func patchKafkaConnectDNS(ctx context.Context, kc *kubectl.Client, name, ns, searchDomain string) error {
	current, err := currentDNSSearches(ctx, kc, "kafkaconnect", name, ns, "{.spec.template.pod.dnsConfig.searches}")
	if err != nil {
		return err
	}
	searches, err := dnsSearchesPatch(current, searchDomain)
	if err != nil {
		return err
	}
	patch := fmt.Sprintf(`{"spec":{"template":{"pod":{"dnsConfig":{"searches":%s}}}}}`, searches)
	_, err = kc.Run(ctx, "patch", "kafkaconnect", name, "-n", ns, "--type=merge", "-p", patch)
	return err
}
