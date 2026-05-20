package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/klster/kates-cli/pkg/theme"
	"github.com/spf13/cobra"
)

// portForwardSpec describes a single kubectl port-forward target.
type portForwardSpec struct {
	Label     string // human-readable name shown in the table
	Namespace string // Kubernetes namespace
	Resource  string // e.g. "service/kates" or "service/krafter-kafka-bootstrap"
	Remote    int    // port inside the cluster
	Local     int    // port on localhost
	URL       string // optional browser URL hint
}

// ── Well-known port map ──────────────────────────────────────────────────────
// Each entry maps a service name → the ports to forward.
// The key is matched via strings.Contains against discovered service names.
var wellKnownPorts = []struct {
	Match     string // substring to match in "namespace/service-name"
	Label     string
	Remote    int
	Local     int
	URL       string
}{
	{"kates/kates",                       "Kates REST API",          8080,  8080,  "http://localhost:8080/api/health"},
	{"kates/kates",                       "Kates gRPC",              9000,  9000,  ""},
	{"kafka/krafter-kafka-bootstrap",     "Kafka Bootstrap (plain)", 9092,  9092,  ""},
	{"kafka/krafter-kafka-bootstrap",     "Kafka Bootstrap (TLS)",   9093,  9093,  ""},
	{"kafka/apicurio",                    "Apicurio Schema Registry",80,    8081,  "http://localhost:8081/ui"},
	{"monitoring/monitoring-grafana",      "Grafana",                 80,    3000,  "http://localhost:3000"},
	{"monitoring/monitoring-kube-prometheus-prometheus", "Prometheus",9090,  9090,  "http://localhost:9090"},
	{"monitoring/monitoring-kube-prometheus-alertmanager","Alertmanager",9093,9094, "http://localhost:9094"},
	{"jaeger/jaeger-query",               "Jaeger UI",               16686, 16686, "http://localhost:16686"},
	{"kates/kates-postgresql",            "PostgreSQL",              5432,  5432,  ""},
}

var (
	portsKafkaNS      string
	portsAppNS        string
	portsMonitoringNS string
	portsAll          bool
)

var portsCmd = &cobra.Command{
	Use:   "ports",
	Short: "Port-forward all Kates services to localhost",
	Long: `Discover deployed Kates services and start port-forwards to localhost.

Auto-detects which services are running and forwards only the available ones.
Keeps running until you press Ctrl+C.

Examples:
  kates ports                    # auto-discover and forward all services
  kates ports --all              # include monitoring + tracing ports
  kates ports --kafka-ns kafka   # custom kafka namespace`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		runPorts(ctx)
		return nil
	},
}

func init() {
	portsCmd.Flags().StringVar(&portsKafkaNS, "kafka-ns", "kafka", "Kafka namespace")
	portsCmd.Flags().StringVar(&portsAppNS, "app-ns", "kates", "Application namespace")
	portsCmd.Flags().StringVar(&portsMonitoringNS, "monitoring-ns", "monitoring", "Monitoring namespace")
	portsCmd.Flags().BoolVar(&portsAll, "all", true, "Include monitoring and tracing ports")
	rootCmd.AddCommand(portsCmd)
}

// ── ANSI helpers (reuse from kafka_wait.go) ──────────────────────────────────
const (
	cPBlue  = "\033[38;5;39m"
	cPRed   = "\033[38;5;196m"
	cPAmber = "\033[38;5;208m"
	cPDim   = "\033[38;5;243m"
	cPBold  = "\033[1m"
	cPReset = "\033[0m"
	cPGreen = "\033[38;5;48m"
)

func pBlue(s string) string  { return cPBlue + s + cPReset }
func pDim(s string) string   { return cPDim + s + cPReset }
func pBold(s string) string  { return cPBold + s + cPReset }
func pGreen(s string) string { return cPGreen + s + cPReset }

// ──────────────────────────────────────────────────────────────────────────────

func runPorts(ctx context.Context) {
	// ── 0. Clean up existing port forwards to prevent bind conflicts ───────────
	fmt.Printf("\n    %s Cleaning up existing port-forwards...\n", pDim("🧹"))
	_ = exec.Command("pkill", "-f", "kubectl port-forward").Run()
	time.Sleep(500 * time.Millisecond)

	// ── 1. Discover services ─────────────────────────────────────────────────
	fmt.Printf("    %s Discovering services...\n", pDim("⇄"))

	discovered := discoverServices()

	specs := matchSpecs(discovered)
	if len(specs) == 0 {
		fmt.Printf("    %s No Kates services found in the cluster\n\n", cPRed+"✖"+cPReset)
		return
	}

	// ── 2. Display table ─────────────────────────────────────────────────────
	fmt.Printf("\n    %s Port Forwarding %s\n", pBold("⇄"), pDim(fmt.Sprintf("(%d services)", len(specs))))
	fmt.Printf("    %s\n", pDim("────────────────────────────────────────────────────────────────"))
	fmt.Printf("    %s  %s  %s  %s\n",
		pDim(fmt.Sprintf("%-26s", "SERVICE")),
		pDim(fmt.Sprintf("%-14s", "NAMESPACE")),
		pDim(fmt.Sprintf("%-22s", "PORTS")),
		pDim("URL"))
	fmt.Printf("    %s\n", pDim("────────────────────────────────────────────────────────────────"))

	for _, s := range specs {
		portStr := fmt.Sprintf(":%d → localhost:%d", s.Remote, s.Local)
		urlStr := ""
		if s.URL != "" {
			urlStr = pDim(s.URL)
		}
		fmt.Printf("    %s  %s  %s  %s\n",
			pBlue(fmt.Sprintf("%-26s", s.Label)),
			fmt.Sprintf("%-14s", s.Namespace),
			pGreen(fmt.Sprintf("%-22s", portStr)),
			urlStr)
	}

	fmt.Printf("    %s\n", pDim("────────────────────────────────────────────────────────────────"))
	fmt.Printf("    %s\n\n", pDim("Press Ctrl+C to stop all port-forwards"))

	// ── 3. Start all forwards ────────────────────────────────────────────────
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			fmt.Printf("\n    %s Stopping port-forwards...\n", pDim("⏹"))
			cancel()
		case <-cancelCtx.Done():
		}
	}()

	var wg sync.WaitGroup
	for _, spec := range specs {
		spec := spec
		wg.Add(1)
		go func() {
			defer wg.Done()
			runSinglePortForward(cancelCtx, spec)
		}()
	}

	wg.Wait()
	fmt.Printf("    %s All port-forwards stopped\n\n", pDim("⏹"))
}

// discoverServices returns a set of "namespace/service-name" strings for all
// services currently in the cluster.
func discoverServices() map[string]bool {
	out, err := exec.Command("kubectl", "get", "svc", "-A",
		"--no-headers",
		"-o", "custom-columns=NS:.metadata.namespace,NAME:.metadata.name",
	).Output()
	if err != nil {
		return nil
	}

	result := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			result[fields[0]+"/"+fields[1]] = true
		}
	}
	return result
}

// matchSpecs matches discovered services against the well-known port map
// and returns only the specs for services that actually exist.
func matchSpecs(discovered map[string]bool) []portForwardSpec {
	var specs []portForwardSpec
	seen := make(map[string]bool) // dedup by label

	for _, wk := range wellKnownPorts {
		if seen[wk.Label] {
			continue
		}

		parts := strings.Split(wk.Match, "/")
		defaultNS := parts[0]
		svc := parts[1]

		// Skip monitoring/tracing unless --all
		if !portsAll {
			if defaultNS == "monitoring" || defaultNS == "jaeger" {
				continue
			}
		}

		// Resolve actual namespace
		var actualNS string
		switch defaultNS {
		case "kafka":
			actualNS = portsKafkaNS
		case "kates":
			actualNS = portsAppNS
		case "monitoring", "jaeger":
			actualNS = portsMonitoringNS
		default:
			actualNS = defaultNS
		}
		if actualNS == "" {
			actualNS = defaultNS
		}

		actualKey := actualNS + "/" + svc

		// Check if service exists under actual namespace
		if discovered[actualKey] {
			specs = append(specs, portForwardSpec{
				Label:     wk.Label,
				Namespace: actualNS,
				Resource:  "service/" + svc,
				Remote:    wk.Remote,
				Local:     wk.Local,
				URL:       wk.URL,
			})
			seen[wk.Label] = true
		}
	}
	return specs
}

// runSinglePortForward runs kubectl port-forward with auto-restart.
func runSinglePortForward(ctx context.Context, spec portForwardSpec) {
	pfArg := fmt.Sprintf("%d:%d", spec.Local, spec.Remote)
	attempt := 0

	okStyle := lipgloss.NewStyle().Foreground(theme.Success).Bold(true)
	errStyle := lipgloss.NewStyle().Foreground(theme.Error).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(theme.Muted)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		attempt++
		var stderrBuf strings.Builder
		cmd := exec.CommandContext(ctx,
			"kubectl", "port-forward",
			spec.Resource,
			pfArg,
			"-n", spec.Namespace,
		)
		cmd.Stdout = nil
		cmd.Stderr = &stderrBuf

		if attempt == 1 {
			fmt.Printf("    %s  %s  localhost:%d\n",
				okStyle.Render("▶"),
				dimStyle.Render(spec.Label),
				spec.Local,
			)
		}

		if err := cmd.Run(); err != nil {
			select {
			case <-ctx.Done():
				return
			default:
			}
			errStr := strings.TrimSpace(stderrBuf.String())
			errStr = strings.ReplaceAll(errStr, "error: unable to listen on any of the requested ports:", "")
			errStr = strings.TrimSpace(errStr)
			if errStr != "" {
				fmt.Printf("    %s  %s crashed: %s (attempt %d), restarting in 3s...\n",
					errStyle.Render("✖"),
					spec.Label,
					errStr,
					attempt,
				)
			} else {
				fmt.Printf("    %s  %s crashed (attempt %d), restarting in 3s...\n",
					errStyle.Render("✖"),
					spec.Label,
					attempt,
				)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
		}
	}
}

// ── Legacy bridge ────────────────────────────────────────────────────────────
// RunPortForwards is called by `deploy --port-forward`. It delegates to the
// same discovery + forward logic as `kates ports`.
func RunPortForwards(ctx context.Context, kafkaNS, appNS, jaegerNS string) {
	portsKafkaNS = kafkaNS
	portsAppNS = appNS
	portsMonitoringNS = jaegerNS
	portsAll = true
	runPorts(ctx)
}
