package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

// ─── Namespace flags for doctor network ─────────────────────

var (
	doctorKafkaNS   string
	doctorConnectNS string
	doctorAppNS     string
	doctorDbNS      string
)

// ─── Check result types ─────────────────────────────────────

type networkCheck struct {
	Category string // "dns", "tcp", "netpol"
	Name     string
	Status   string // "pass", "fail", "warn"
	Detail   string
}

// ─── Command definition ─────────────────────────────────────

var doctorNetworkCmd = &cobra.Command{
	Use:   "network",
	Short: "Diagnose cluster network connectivity for Kates components",
	Long: `Runs DNS resolution, TCP connectivity, and NetworkPolicy checks
from ephemeral pods inside the cluster to verify cross-namespace
networking between Kafka, Connect, Schema Registry, and Database.

Examples:
  # Use default namespaces
  kates doctor network

  # Custom namespaces
  kates doctor network --kafka-ns kafka-prod --connect-ns connect-prod --db-ns pg-system`,
	RunE: runDoctorNetwork,
}

func init() {
	doctorNetworkCmd.Flags().StringVar(&doctorKafkaNS, "kafka-ns", "kafka", "Namespace for Kafka and Schema Registry")
	doctorNetworkCmd.Flags().StringVar(&doctorConnectNS, "connect-ns", "connect", "Namespace for Kafka Connect")
	doctorNetworkCmd.Flags().StringVar(&doctorAppNS, "app-ns", "kates", "Namespace for Kates application")
	doctorNetworkCmd.Flags().StringVar(&doctorDbNS, "db-ns", "database", "Namespace for PostgreSQL database")

	doctorCmd.AddCommand(doctorNetworkCmd)
}

// ─── Main runner ────────────────────────────────────────────

func runDoctorNetwork(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Styles
	title := lipgloss.NewStyle().Bold(true).Foreground(clrText)
	separator := lipgloss.NewStyle().Foreground(clrDim).Render(strings.Repeat("━", 50))

	fmt.Println()
	fmt.Println(title.Render("🩺 Kates Network Doctor"))
	fmt.Println(separator)
	fmt.Println()

	var checks []networkCheck

	// ── Phase 1: DNS Resolution ──
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrCyan).Render("📡 DNS Resolution"))

	dnsTargets := []struct {
		host string
		desc string
		ns   string
	}{
		{
			host: fmt.Sprintf("krafter-kafka-bootstrap.%s.svc.cluster.local", doctorKafkaNS),
			desc: "Kafka Bootstrap",
			ns:   doctorAppNS,
		},
		{
			host: fmt.Sprintf("connect-cluster-connect-api.%s.svc.cluster.local", doctorConnectNS),
			desc: "Connect REST API",
			ns:   doctorAppNS,
		},
		{
			host: fmt.Sprintf("apicurio-apicurio-registry.%s.svc.cluster.local", doctorKafkaNS),
			desc: "Schema Registry",
			ns:   doctorAppNS,
		},
	}

	for _, t := range dnsTargets {
		result := runDNSCheck(ctx, t.host, t.ns)
		checks = append(checks, result)
		printNetworkCheck(result)
	}

	fmt.Println()

	// ── Phase 2: TCP Connectivity ──
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrCyan).Render("🔌 TCP Connectivity"))

	tcpTargets := []struct {
		host string
		port string
		desc string
		ns   string
	}{
		{
			host: fmt.Sprintf("krafter-kafka-bootstrap.%s.svc.cluster.local", doctorKafkaNS),
			port: "9092",
			desc: "Kafka Plain",
			ns:   doctorAppNS,
		},
		{
			host: fmt.Sprintf("krafter-kafka-bootstrap.%s.svc.cluster.local", doctorKafkaNS),
			port: "9093",
			desc: "Kafka TLS",
			ns:   doctorAppNS,
		},
		{
			host: fmt.Sprintf("connect-cluster-connect-api.%s.svc.cluster.local", doctorConnectNS),
			port: "8083",
			desc: "Connect REST API",
			ns:   doctorAppNS,
		},
		{
			host: fmt.Sprintf("apicurio-apicurio-registry.%s.svc.cluster.local", doctorKafkaNS),
			port: "80",
			desc: "Schema Registry",
			ns:   doctorAppNS,
		},
		{
			host: fmt.Sprintf("postgresql.%s.svc.cluster.local", doctorDbNS),
			port: "5432",
			desc: "PostgreSQL",
			ns:   doctorAppNS,
		},
	}

	for _, t := range tcpTargets {
		result := runTCPCheck(ctx, t.host, t.port, t.desc, t.ns)
		checks = append(checks, result)
		printNetworkCheck(result)
	}

	fmt.Println()

	// ── Phase 3: NetworkPolicy Audit ──
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrCyan).Render("🛡️  NetworkPolicy Audit"))

	namespaces := uniqueStrings([]string{doctorKafkaNS, doctorConnectNS, doctorAppNS, doctorDbNS})
	for _, ns := range namespaces {
		results := runNetPolAudit(ctx, ns)
		checks = append(checks, results...)
		for _, r := range results {
			printNetworkCheck(r)
		}
	}

	// ── Summary ──
	fmt.Println()
	fmt.Println(separator)

	passed, warnings, failed := 0, 0, 0
	for _, c := range checks {
		switch c.Status {
		case "pass":
			passed++
		case "warn":
			warnings++
		case "fail":
			failed++
		}
	}
	total := len(checks)
	summaryParts := []string{fmt.Sprintf("%d/%d checks passed", passed, total)}
	if warnings > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d warning(s)", warnings))
	}
	if failed > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d failed", failed))
	}

	summaryStyle := lipgloss.NewStyle().Bold(true).Foreground(clrGreen)
	if failed > 0 {
		summaryStyle = lipgloss.NewStyle().Bold(true).Foreground(clrRed)
	} else if warnings > 0 {
		summaryStyle = lipgloss.NewStyle().Bold(true).Foreground(clrOrange)
	}

	fmt.Println(summaryStyle.Render("📊 Summary: " + strings.Join(summaryParts, ", ")))
	fmt.Println()

	return nil
}

// ─── DNS Check ──────────────────────────────────────────────

func runDNSCheck(ctx context.Context, host, sourceNS string) networkCheck {
	podName := doctorPodName()
	shellCmd := fmt.Sprintf("nslookup %s 2>&1 && echo DNS_OK || echo DNS_FAIL", host)

	out, err := runEphemeralPod(ctx, podName, sourceNS, shellCmd)
	if err != nil {
		return networkCheck{
			Category: "dns",
			Name:     host,
			Status:   "fail",
			Detail:   fmt.Sprintf("Pod execution failed: %v", err),
		}
	}

	if strings.Contains(out, "DNS_OK") {
		return networkCheck{
			Category: "dns",
			Name:     host,
			Status:   "pass",
			Detail:   "",
		}
	}

	return networkCheck{
		Category: "dns",
		Name:     host,
		Status:   "fail",
		Detail:   "Could not resolve hostname",
	}
}

// ─── TCP Check ──────────────────────────────────────────────

func runTCPCheck(ctx context.Context, host, port, desc, sourceNS string) networkCheck {
	podName := doctorPodName()
	shellCmd := fmt.Sprintf("nc -z -w 3 %s %s 2>&1 && echo TCP_OK || echo TCP_FAIL", host, port)

	label := fmt.Sprintf("%s:%s (%s)", shortHost(host), port, desc)

	out, err := runEphemeralPod(ctx, podName, sourceNS, shellCmd)
	if err != nil {
		return networkCheck{
			Category: "tcp",
			Name:     label,
			Status:   "fail",
			Detail:   fmt.Sprintf("Pod execution failed: %v", err),
		}
	}

	if strings.Contains(out, "TCP_OK") {
		return networkCheck{
			Category: "tcp",
			Name:     label,
			Status:   "pass",
			Detail:   "",
		}
	}

	detail := "Connection refused"
	if strings.Contains(out, "timed out") || strings.Contains(out, "timeout") {
		detail = "Connection timed out"
	}
	return networkCheck{
		Category: "tcp",
		Name:     label,
		Status:   "fail",
		Detail:   detail,
	}
}

// ─── NetworkPolicy Audit ────────────────────────────────────

type k8sNetPolList struct {
	Items []struct {
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
	} `json:"items"`
}

func runNetPolAudit(ctx context.Context, ns string) []networkCheck {
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// List NetworkPolicies
	out, err := exec.CommandContext(checkCtx, "kubectl", "get", "networkpolicy", "-n", ns, "-o", "json").Output()
	if err != nil {
		return []networkCheck{{
			Category: "netpol",
			Name:     fmt.Sprintf("%s/*", ns),
			Status:   "pass",
			Detail:   "No NetworkPolicies found (or namespace does not exist)",
		}}
	}

	var list k8sNetPolList
	if err := json.Unmarshal(out, &list); err != nil {
		return []networkCheck{{
			Category: "netpol",
			Name:     fmt.Sprintf("%s/*", ns),
			Status:   "warn",
			Detail:   "Failed to parse NetworkPolicy list",
		}}
	}

	if len(list.Items) == 0 {
		return []networkCheck{{
			Category: "netpol",
			Name:     fmt.Sprintf("%s/*", ns),
			Status:   "pass",
			Detail:   "No NetworkPolicies found",
		}}
	}

	var results []networkCheck
	for _, item := range list.Items {
		policyName := fmt.Sprintf("%s/%s", item.Metadata.Namespace, item.Metadata.Name)

		// Fetch the raw YAML/JSON to check for kubernetes.io/metadata.name usage
		polOut, polErr := exec.CommandContext(checkCtx, "kubectl", "get", "networkpolicy",
			item.Metadata.Name, "-n", item.Metadata.Namespace, "-o", "json").Output()
		if polErr != nil {
			results = append(results, networkCheck{
				Category: "netpol",
				Name:     policyName,
				Status:   "warn",
				Detail:   "Could not inspect policy",
			})
			continue
		}

		raw := string(polOut)
		if strings.Contains(raw, "kubernetes.io/metadata.name") {
			results = append(results, networkCheck{
				Category: "netpol",
				Name:     policyName,
				Status:   "warn",
				Detail:   "uses kubernetes.io/metadata.name (may fail on some clusters)",
			})
		} else {
			results = append(results, networkCheck{
				Category: "netpol",
				Name:     policyName,
				Status:   "pass",
				Detail:   "OK",
			})
		}
	}

	return results
}

// ─── Ephemeral pod runner ───────────────────────────────────

func runEphemeralPod(ctx context.Context, podName, namespace, shellCmd string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	args := []string{
		"run", "--rm", "-i", "--restart=Never",
		"--image=busybox:1.36",
		podName,
		"-n", namespace,
		"--", "sh", "-c", shellCmd,
	}

	cmd := exec.CommandContext(runCtx, "kubectl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// If the pod completed but had a non-zero exit, the output is still
		// useful for parsing DNS_OK/TCP_OK markers.
		if len(out) > 0 {
			return string(out), nil
		}
		return "", fmt.Errorf("kubectl run failed: %w", err)
	}

	return string(out), nil
}

// ─── Output helpers ─────────────────────────────────────────

func printNetworkCheck(c networkCheck) {
	indent := "  "
	switch c.Status {
	case "pass":
		icon := lipgloss.NewStyle().Foreground(clrGreen).Render("✅")
		name := lipgloss.NewStyle().Foreground(clrText).Render(c.Name)
		if c.Detail != "" && c.Detail != "OK" {
			fmt.Printf("%s%s %s %s\n", indent, icon, name,
				lipgloss.NewStyle().Foreground(clrDim).Italic(true).Render("— "+c.Detail))
		} else {
			fmt.Printf("%s%s %s\n", indent, icon, name)
		}
	case "fail":
		icon := lipgloss.NewStyle().Foreground(clrRed).Render("❌")
		name := lipgloss.NewStyle().Foreground(clrText).Render(c.Name)
		fmt.Printf("%s%s %s\n", indent, icon, name)
		if c.Detail != "" {
			arrow := lipgloss.NewStyle().Foreground(clrDim).Render("→")
			detail := lipgloss.NewStyle().Foreground(clrRed).Italic(true).Render(c.Detail)
			fmt.Printf("%s   %s %s\n", indent, arrow, detail)
		}
	case "warn":
		icon := lipgloss.NewStyle().Foreground(clrOrange).Render("⚠️ ")
		name := lipgloss.NewStyle().Foreground(clrText).Render(c.Name)
		detail := lipgloss.NewStyle().Foreground(clrOrange).Italic(true).Render(c.Detail)
		fmt.Printf("%s%s%s %s\n", indent, icon, name, detail)
	}
}

// ─── Utility helpers ────────────────────────────────────────

// doctorPodName generates a unique ephemeral pod name.
func doctorPodName() string {
	return fmt.Sprintf("kates-doctor-%s", randomSuffix(6))
}

// randomSuffix returns a random lowercase alphanumeric string of length n.
func randomSuffix(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rng.Intn(len(letters))]
	}
	return string(b)
}

// shortHost extracts a readable short name from a FQDN.
// "krafter-kafka-bootstrap.kafka.svc.cluster.local" → "kafka"
// It uses the second part (namespace) of the service DNS name.
func shortHost(fqdn string) string {
	parts := strings.SplitN(fqdn, ".", 3)
	if len(parts) >= 2 {
		return parts[1]
	}
	return fqdn
}

// uniqueStrings deduplicates a string slice while preserving order.
func uniqueStrings(input []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, s := range input {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}
