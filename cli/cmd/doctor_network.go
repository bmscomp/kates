package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

// ─── Namespace flags for doctor network ─────────────────────

var (
	doctorKafkaNS   string
	doctorConnectNS string
	doctorAppNS     string
	doctorDbNS      string

	// doctorNetworkTimeout (--timeout) bounds the whole run; doctorProbeTimeout
	// bounds each ephemeral pod, which pulls an image and runs one command.
	doctorNetworkTimeout time.Duration
	doctorProbeTimeout   = 30 * time.Second
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
  kates doctor network --kafka-ns kafka-prod --connect-ns connect-prod --db-ns pg-system

  # Allow more time on a slow cluster
  kates doctor network --timeout 5m`,
	RunE: runDoctorNetwork,
}

func init() {
	doctorNetworkCmd.Flags().StringVar(&doctorKafkaNS, "kafka-ns", "kafka", "Namespace for Kafka and Schema Registry")
	doctorNetworkCmd.Flags().StringVar(&deployKafkaName, "kafka-name", "krafter", "Name of the primary Kafka cluster")
	doctorNetworkCmd.Flags().StringVar(&doctorConnectNS, "connect-ns", "connect", "Namespace for Kafka Connect")
	doctorNetworkCmd.Flags().StringVar(&doctorAppNS, "app-ns", "kates", "Namespace for Kates application")
	doctorNetworkCmd.Flags().StringVar(&doctorDbNS, "db-ns", "database", "Namespace for PostgreSQL database")
	doctorNetworkCmd.Flags().DurationVar(&doctorNetworkTimeout, "timeout", 2*time.Minute, "Time limit for the whole run (each probe pod gets 30s)")

	doctorCmd.AddCommand(doctorNetworkCmd)
}

// ─── Main runner ────────────────────────────────────────────

func runDoctorNetwork(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), doctorNetworkTimeout)
	defer cancel()

	// Detect cluster DNS domain (fall back to "cluster.local" if detection fails)
	clusterDomain := detectClusterDomain(ctx)

	// Styles
	title := lipgloss.NewStyle().Bold(true).Foreground(clrText)
	separator := lipgloss.NewStyle().Foreground(clrDim).Render(strings.Repeat("━", 50))

	fmt.Println()
	fmt.Println(title.Render("🩺 Kates Network Doctor"))
	fmt.Println(separator)
	fmt.Println()

	var checks []networkCheck

	dnsTargets, tcpTargets := networkProbeTargets(clusterDomain)
	dnsResults, tcpResults := runNetworkProbes(ctx, dnsTargets, tcpTargets)

	// ── Phase 1: DNS Resolution ──
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrCyan).Render("📡 DNS Resolution"))
	for _, result := range dnsResults {
		checks = append(checks, result)
		printNetworkCheck(result)
	}

	fmt.Println()

	// ── Phase 2: TCP Connectivity ──
	fmt.Println(lipgloss.NewStyle().Bold(true).Foreground(clrCyan).Render("🔌 TCP Connectivity"))
	for _, result := range tcpResults {
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
	printNetworkSummary(checks)
	fmt.Println()

	return nil
}

// networkProbeTarget is one probe: a host, a port for a TCP probe, what it
// is, and the namespace the probe pod runs in.
type networkProbeTarget struct {
	host string
	port string
	desc string
	ns   string
}

// networkProbeTargets is the DNS and TCP probes for the namespaces the flags
// name, in clusterDomain.
func networkProbeTargets(clusterDomain string) (dns, tcp []networkProbeTarget) {
	bootstrap := fmt.Sprintf("%s-kafka-bootstrap.%s.svc.%s", deployKafkaName, doctorKafkaNS, clusterDomain)
	connectAPI := fmt.Sprintf("connect-cluster-connect-api.%s.svc.%s", doctorConnectNS, clusterDomain)
	registry := fmt.Sprintf("apicurio-apicurio-registry.%s.svc.%s", doctorKafkaNS, clusterDomain)
	postgres := fmt.Sprintf("postgresql.%s.svc.%s", doctorDbNS, clusterDomain)

	dns = []networkProbeTarget{
		{host: bootstrap, desc: "Kafka Bootstrap", ns: doctorAppNS},
		{host: connectAPI, desc: "Connect REST API", ns: doctorAppNS},
		{host: registry, desc: "Schema Registry", ns: doctorAppNS},
	}
	tcp = []networkProbeTarget{
		{host: bootstrap, port: "9092", desc: "Kafka Plain", ns: doctorAppNS},
		{host: bootstrap, port: "9093", desc: "Kafka TLS", ns: doctorAppNS},
		{host: connectAPI, port: "8083", desc: "Connect REST API", ns: doctorAppNS},
		{host: registry, port: "80", desc: "Schema Registry", ns: doctorAppNS},
		{host: postgres, port: "5432", desc: "PostgreSQL", ns: doctorAppNS},
	}
	return dns, tcp
}

// runNetworkProbes runs the probes at once, each under its own deadline
// (doctorProbeTimeout). They used to run one after another under the one
// 120 s context, so a slow image pull on the first left the last ones no
// time at all. The results are in the targets' order.
func runNetworkProbes(ctx context.Context, dnsTargets, tcpTargets []networkProbeTarget) (dnsResults, tcpResults []networkCheck) {
	dnsResults = make([]networkCheck, len(dnsTargets))
	tcpResults = make([]networkCheck, len(tcpTargets))
	g, gctx := errgroup.WithContext(ctx)
	for i, t := range dnsTargets {
		g.Go(func() error {
			dnsResults[i] = runDNSCheck(gctx, t.host, t.ns)
			return nil
		})
	}
	for i, t := range tcpTargets {
		g.Go(func() error {
			tcpResults[i] = runTCPCheck(gctx, t.host, t.port, t.desc, t.ns)
			return nil
		})
	}
	_ = g.Wait() // a probe that fails is a failed check, never an error
	return dnsResults, tcpResults
}

// networkSummary is the summary line's text, and the counts that colour it.
func networkSummary(checks []networkCheck) (line string, warnings, failed int) {
	passed := 0
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
	summaryParts := []string{fmt.Sprintf("%d/%d checks passed", passed, len(checks))}
	if warnings > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d warning(s)", warnings))
	}
	if failed > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d failed", failed))
	}
	return "📊 Summary: " + strings.Join(summaryParts, ", "), warnings, failed
}

func printNetworkSummary(checks []networkCheck) {
	line, warnings, failed := networkSummary(checks)
	summaryStyle := lipgloss.NewStyle().Bold(true).Foreground(clrGreen)
	if failed > 0 {
		summaryStyle = lipgloss.NewStyle().Bold(true).Foreground(clrRed)
	} else if warnings > 0 {
		summaryStyle = lipgloss.NewStyle().Bold(true).Foreground(clrOrange)
	}
	fmt.Println(summaryStyle.Render(line))
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
	kc := newKubectl(ns)
	out, err := kc.Output(checkCtx, "get", "networkpolicy", "-o", "json")
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
		polOut, polErr := kc.Output(checkCtx, "get", "networkpolicy",
			item.Metadata.Name, "-n", item.Metadata.Namespace, "-o", "json")
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
	runCtx, cancel := context.WithTimeout(ctx, doctorProbeTimeout)
	defer cancel()

	out, err := newKubectl(namespace).Combined(runCtx,
		"run", "--rm", "-i", "--restart=Never",
		"--image=busybox:1.36",
		podName,
		"--", "sh", "-c", shellCmd)
	if err != nil {
		// A non-zero exit is a probe that did not run: the shell commands end
		// in `|| echo *_FAIL`, so they exit 0 themselves. Output on a failed
		// run used to count as a result, which reported an image pull or
		// RBAC error as "Could not resolve hostname".
		if last := lastOutputLine(out); last != "" {
			return "", fmt.Errorf("kubectl run failed: %w: %s", err, last)
		}
		return "", fmt.Errorf("kubectl run failed: %w", err)
	}

	return string(out), nil
}

// lastOutputLine is the last non-empty line of a command's output, which is
// where kubectl puts the reason it failed.
func lastOutputLine(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
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

// ─── Cluster domain detection ───────────────────────────────

// detectClusterDomain discovers the cluster DNS domain by reading
// /etc/resolv.conf from an ephemeral pod. Falls back to "cluster.local".
func detectClusterDomain(ctx context.Context) string {
	domain := "cluster.local"

	detectCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	podName := doctorPodName()
	out, err := newKubectl("default").Combined(detectCtx, "run", "--rm", "-i", "--restart=Never",
		"--image=busybox:1.36", podName,
		"--", "cat", "/etc/resolv.conf")
	if err != nil || len(out) == 0 {
		return domain
	}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "search ") {
			continue
		}
		fields := strings.Fields(line)[1:]

		// Priority: find entry starting exactly with "svc."
		var found string
		for _, f := range fields {
			if strings.HasPrefix(f, "svc.") {
				found = strings.TrimPrefix(f, "svc.")
				break
			}
		}

		// Fallback: strip namespace.svc. prefix
		if found == "" {
			for _, f := range fields {
				idx := strings.Index(f, ".svc.")
				if idx != -1 {
					found = f[idx+5:]
					break
				}
			}
		}

		if found != "" {
			domain = found
		}
		break
	}

	return domain
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
