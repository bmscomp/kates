package cmd

import (
	"context"
	"fmt"
	"math/rand"
	"os/exec"
	"strings"
	"time"

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

	title := lipgloss.NewStyle().Bold(true).Foreground(clrText)
	separator := lipgloss.NewStyle().Foreground(clrDim).Render(strings.Repeat("━", 50))
	success := lipgloss.NewStyle().Foreground(clrGreen).Render("✅")
	warning := lipgloss.NewStyle().Foreground(clrOrange).Render("⚠️ ")
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
	resolvConfOut, err := exec.CommandContext(ctx, "kubectl", "run", "--rm", "-i", "--restart=Never",
		"--image=busybox:1.36", podName, "-n", "default",
		"--", "cat", "/etc/resolv.conf").CombinedOutput()

	if err != nil {
		fmt.Printf("  %s Failed to run ephemeral pod in namespace default: %v\n", fail, err)
		fmt.Printf("  Output: %s\n", string(resolvConfOut))
		return err
	}

	resolvContent := string(resolvConfOut)
	
	var searches []string
	var ndots string
	
	for _, line := range strings.Split(resolvContent, "\n") {
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

	fmt.Printf("  %s Default Search Domains: %s\n", info, strings.Join(searches, ", "))
	if ndots != "" {
		fmt.Printf("  %s Default ndots: %s\n", info, ndots)
	}

	clusterDomain := "cluster.local"
	for _, s := range searches {
		if !strings.Contains(s, "svc.") && s != "default" {
			clusterDomain = s
		}
	}
	
	fmt.Println()
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrCyan).Render("🔍 Deployment Audit & Adaptation"))
	
	requiredKafkaSearch := fmt.Sprintf("%s.svc.%s", doctorKafkaNS, clusterDomain)

	// We need to check if connect and kates are in a different namespace than kafka
	var fixCount int
	var checkCount int

	// Check Kates backend Deployment
	checkCount++
	if doctorAppNS != doctorKafkaNS {
		katesPatchNeeded := checkDeploymentDNS(ctx, "kates", doctorAppNS, requiredKafkaSearch)
		if katesPatchNeeded {
			if doctorDnsFix {
				fmt.Printf("  %s kates backend (namespace: %s) is missing DNS search domain.\n", warning, doctorAppNS)
				err := patchDeploymentDNS(ctx, "kates", doctorAppNS, requiredKafkaSearch)
				if err != nil {
					fmt.Printf("     %s Failed to patch kates deployment: %v\n", fail, err)
				} else {
					fmt.Printf("     %s Successfully patched kates deployment.\n", success)
					fixCount++
				}
			} else {
				fmt.Printf("  %s kates backend (namespace: %s) is missing DNS search domain (run with --fix to resolve).\n", warning, doctorAppNS)
			}
		} else {
			fmt.Printf("  %s kates backend (namespace: %s) has correct DNS search domain.\n", success, doctorAppNS)
		}
	} else {
		fmt.Printf("  %s kates backend is in the same namespace as Kafka, no DNS search domain adaptation needed.\n", success)
	}

	// Check KafkaConnect CR
	checkCount++
	if doctorConnectNS != doctorKafkaNS {
		connectPatchNeeded := checkKafkaConnectDNS(ctx, "connect-cluster", doctorConnectNS, requiredKafkaSearch)
		if connectPatchNeeded {
			if doctorDnsFix {
				fmt.Printf("  %s connect-cluster (namespace: %s) is missing DNS search domain.\n", warning, doctorConnectNS)
				err := patchKafkaConnectDNS(ctx, "connect-cluster", doctorConnectNS, requiredKafkaSearch)
				if err != nil {
					fmt.Printf("     %s Failed to patch connect-cluster: %v\n", fail, err)
				} else {
					fmt.Printf("     %s Successfully patched connect-cluster.\n", success)
					fixCount++
				}
			} else {
				fmt.Printf("  %s connect-cluster (namespace: %s) is missing DNS search domain (run with --fix to resolve).\n", warning, doctorConnectNS)
			}
		} else {
			fmt.Printf("  %s connect-cluster (namespace: %s) has correct DNS search domain.\n", success, doctorConnectNS)
		}
	} else {
		fmt.Printf("  %s connect-cluster is in the same namespace as Kafka, no DNS search domain adaptation needed.\n", success)
	}

	fmt.Println(separator)
	if doctorDnsFix {
		fmt.Printf("📊 Summary: Audited %d components. Applied %d fixes.\n", checkCount, fixCount)
	} else {
		fmt.Printf("📊 Summary: Audited %d components. Run with --fix to apply missing adaptations.\n", checkCount)
	}

	return nil
}

func checkDeploymentDNS(ctx context.Context, name, ns, requiredSearch string) bool {
	out, err := exec.CommandContext(ctx, "kubectl", "get", "deployment", name, "-n", ns, "-o", "jsonpath={.spec.template.spec.dnsConfig.searches}").Output()
	if err != nil {
		// Ignore if deployment doesn't exist
		return false
	}
	searches := string(out)
	if !strings.Contains(searches, requiredSearch) {
		return true
	}
	return false
}

func checkKafkaConnectDNS(ctx context.Context, name, ns, requiredSearch string) bool {
	out, err := exec.CommandContext(ctx, "kubectl", "get", "kafkaconnect", name, "-n", ns, "-o", "jsonpath={.spec.template.pod.dnsConfig.searches}").Output()
	if err != nil {
		return false
	}
	searches := string(out)
	if !strings.Contains(searches, requiredSearch) {
		return true
	}
	return false
}

func patchDeploymentDNS(ctx context.Context, name, ns, searchDomain string) error {
	patch := fmt.Sprintf(`{"spec":{"template":{"spec":{"dnsConfig":{"searches":["%s"]}}}}}`, searchDomain)
	_, err := exec.CommandContext(ctx, "kubectl", "patch", "deployment", name, "-n", ns, "--type=strategic", "-p", patch).CombinedOutput()
	return err
}

func patchKafkaConnectDNS(ctx context.Context, name, ns, searchDomain string) error {
	patch := fmt.Sprintf(`{"spec":{"template":{"pod":{"dnsConfig":{"searches":["%s"]}}}}}`, searchDomain)
	_, err := exec.CommandContext(ctx, "kubectl", "patch", "kafkaconnect", name, "-n", ns, "--type=merge", "-p", patch).CombinedOutput()
	return err
}
