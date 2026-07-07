package cmd

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/klster/kates-cli/output"
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
	Match  string // substring to match in "namespace/service-name"
	Label  string
	Remote int
	Local  int
	URL    string
}{
	{"kates/kates", "Kates REST API", 8080, 8080, "http://localhost:8080/api/health"},
	{"kates/kates", "Kates gRPC", 9000, 9000, ""},
	{"kafka/krafter-kafka-bootstrap", "Kafka Bootstrap (plain)", 9092, 9092, ""},
	{"kafka/krafter-kafka-bootstrap", "Kafka Bootstrap (TLS)", 9093, 9093, ""},
	{"kafka/apicurio", "Apicurio Schema Registry", 80, 8081, "http://localhost:8081/ui"},
	{"kafka-ui/kafka-ui", "Kafka UI", 8080, 8085, "http://localhost:8085"},
	{"monitoring/monitoring-grafana", "Grafana", 80, 3000, "http://localhost:3000"},
	{"monitoring/monitoring-kube-prometheus-prometheus", "Prometheus", 9090, 9090, "http://localhost:9090"},
	{"monitoring/monitoring-kube-prometheus-alertmanager", "Alertmanager", 9093, 9094, "http://localhost:9094"},
	{"jaeger/jaeger-query", "Jaeger UI", 16686, 16686, "http://localhost:16686"},
	{"kates/kates-postgresql", "PostgreSQL", 5432, 5432, ""},
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

// ──────────────────────────────────────────────────────────────────────────────

func runPorts(ctx context.Context) {
	// ── 0. Clean up existing port forwards to prevent bind conflicts ────────────
	fmt.Printf("\n    %s Cleaning up existing port-forwards...\n", output.DimStyle.Render("🧹"))

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
	time.Sleep(1000 * time.Millisecond)

	// ── 1. Discover services ─────────────────────────────────────────────────
	fmt.Printf("    %s Discovering services...\n", output.DimStyle.Render("⇄"))

	discovered := discoverServices()

	specs := matchSpecs(discovered)
	if len(specs) == 0 {
		fmt.Printf("    %s No Kates services found in the cluster\n\n", output.ErrorStyle.Render("✖"))
		return
	}

	boldStyle := lipgloss.NewStyle().Bold(true)

	// ── 2. Display table ─────────────────────────────────────────────────────
	fmt.Printf("\n    %s Port Forwarding %s\n", boldStyle.Render("⇄"), output.DimStyle.Render(fmt.Sprintf("(%d services)", len(specs))))
	fmt.Printf("    %s\n", output.DimStyle.Render("────────────────────────────────────────────────────────────────"))
	fmt.Printf("    %s  %s  %s  %s\n",
		output.DimStyle.Render(fmt.Sprintf("%-26s", "SERVICE")),
		output.DimStyle.Render(fmt.Sprintf("%-14s", "NAMESPACE")),
		output.DimStyle.Render(fmt.Sprintf("%-22s", "PORTS")),
		output.DimStyle.Render("URL"))
	fmt.Printf("    %s\n", output.DimStyle.Render("────────────────────────────────────────────────────────────────"))

	for _, s := range specs {
		portStr := fmt.Sprintf(":%d → localhost:%d", s.Remote, s.Local)
		urlStr := ""
		if s.URL != "" {
			urlStr = output.DimStyle.Render(s.URL)
		}
		fmt.Printf("    %s  %s  %s  %s\n",
			output.AccentStyle.Render(fmt.Sprintf("%-26s", s.Label)),
			fmt.Sprintf("%-14s", s.Namespace),
			output.SuccessStyle.Render(fmt.Sprintf("%-22s", portStr)),
			urlStr)
	}

	fmt.Printf("    %s\n", output.DimStyle.Render("────────────────────────────────────────────────────────────────"))
	// ── 3. Start all forwards in the background ──────────────────────────────
	fmt.Printf("\n    %s Establishing port-forwards in the background...\n", boldStyle.Render("⚡"))

	for _, spec := range specs {
		pfArg := fmt.Sprintf("%d:%d", spec.Local, spec.Remote)
		cmd := exec.Command("kubectl", "port-forward",
			spec.Resource,
			pfArg,
			"-n", spec.Namespace,
		)
		// Detach from the current process group so forwards survive after
		// `kates ports` exits and the shell returns to prompt.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err == nil {
			cmd.Stdout = devNull
			cmd.Stderr = devNull
		}
		cmd.Stdin = nil
		if err := cmd.Start(); err == nil && cmd.Process != nil {
			_ = cmd.Process.Release()
		}
	}

	// Verify reachability
	fmt.Printf("    %s Verifying connections...\n", output.DimStyle.Render("⇄"))
	time.Sleep(300 * time.Millisecond)

	okStyle := lipgloss.NewStyle().Foreground(theme.Success).Bold(true)
	errStyle := lipgloss.NewStyle().Foreground(theme.Error).Bold(true)

	allOk := true
	katesAPIForwardActive := false
	katesAPINS := ""
	katesAPILocalPort := 0
	for _, spec := range specs {
		addr := fmt.Sprintf("127.0.0.1:%d", spec.Local)
		success := false
		for i := 0; i < 15; i++ {
			if isPortReachable(addr) {
				success = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}

		if success {
			if spec.Label == "Kates REST API" {
				katesAPIForwardActive = true
				katesAPINS = spec.Namespace
				katesAPILocalPort = spec.Local
			}
			fmt.Printf("    %s  %-26s  localhost:%-5d  %s\n",
				okStyle.Render("✔"),
				spec.Label,
				spec.Local,
				output.SuccessStyle.Render("[ACTIVE]"),
			)
		} else {
			allOk = false
			fmt.Printf("    %s  %-26s  localhost:%-5d  %s\n",
				errStyle.Render("✖"),
				spec.Label,
				spec.Local,
				output.ErrorStyle.Render("[FAILED]"),
			)
		}
	}

	// Keep local CLI context in sync with the forwarded API endpoint so follow-up
	// commands (health/resilience/tests) work without manual ctx/api-key edits.
	if katesAPIForwardActive {
		syncContextToPortForward(ctx, katesAPINS, katesAPILocalPort)
	}

	fmt.Println()
	if allOk {
		fmt.Printf("    %s %s\n", okStyle.Render("✓"), boldStyle.Render("Port forwards established successfully!"))
		fmt.Printf("      %s\n\n", output.DimStyle.Render("They are running in the background. You can continue typing commands in this terminal."))
	} else {
		fmt.Printf("    %s %s\n", errStyle.Render("⚠"), boldStyle.Render("Some port forwards failed to establish."))
		fmt.Printf("      %s\n\n", output.DimStyle.Render("Check if the pods are ready or run 'kates clean' to reset."))
	}
}

func syncContextToPortForward(ctx context.Context, appNS string, localPort int) {
	endpoint := fmt.Sprintf("http://localhost:%d", localPort)
	cfg := loadConfig()
	ctxName := resolveContextName(cfg)
	existingKey := ""
	if existing, ok := cfg.Contexts[ctxName]; ok {
		existingKey = strings.TrimSpace(existing.APIKey)
	}

	apiKey, source, keyErr := resolveWorkingAPIKey(ctx, endpoint, appNS, existingKey)
	cfg = applyPortForwardContext(cfg, ctxName, endpoint, apiKey)
	if err := saveConfig(cfg); err != nil {
		fmt.Printf("    %s Failed to persist context sync: %v\n", output.WarningStyle.Render("⚠"), err)
		return
	}

	if apiKey != "" {
		if source == "" {
			source = "resolved"
		}
		fmt.Printf("    %s Synced context %q → %s (API key source: %s)\n", output.SuccessStyle.Render("✓"), ctxName, endpoint, source)
		return
	}

	fmt.Printf("    %s Synced context %q → %s\n", output.SuccessStyle.Render("✓"), ctxName, endpoint)
	if keyErr != nil {
		fmt.Printf("      %s\n", output.DimStyle.Render("API key was not refreshed automatically (secret not found or not readable)."))
	}
}

func resolveContextName(cfg Config) string {
	name := cfg.CurrentContext
	if contextFlag != "" {
		name = contextFlag
	}
	if strings.TrimSpace(name) == "" {
		return "local"
	}
	return name
}

func resolveWorkingAPIKey(ctx context.Context, endpoint, namespace, existingKey string) (string, string, error) {
	secretKey, secretErr := fetchKatesAPIKey(ctx, namespace)
	if secretKey != "" && isAPIKeyAccepted(endpoint, secretKey) {
		return secretKey, "secret", nil
	}

	podKey, podErr := fetchKatesAPIKeyFromRunningPod(ctx, namespace)
	if podKey != "" && isAPIKeyAccepted(endpoint, podKey) {
		return podKey, "running-pod", nil
	}

	if strings.TrimSpace(existingKey) != "" && isAPIKeyAccepted(endpoint, existingKey) {
		return existingKey, "existing-context", nil
	}

	// Best-effort fallback when validation cannot succeed (e.g., API temporarily down).
	if secretKey != "" {
		if secretErr != nil {
			return secretKey, "secret-unverified", secretErr
		}
		return secretKey, "secret-unverified", nil
	}
	if podKey != "" {
		if podErr != nil {
			return podKey, "running-pod-unverified", podErr
		}
		return podKey, "running-pod-unverified", nil
	}
	if strings.TrimSpace(existingKey) != "" {
		return existingKey, "existing-context-unverified", nil
	}

	if secretErr != nil {
		return "", "", secretErr
	}
	if podErr != nil {
		return "", "", podErr
	}
	return "", "", fmt.Errorf("no usable API key source")
}

func fetchKatesAPIKey(ctx context.Context, namespace string) (string, error) {
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	out, err := exec.CommandContext(
		checkCtx,
		"kubectl",
		"get", "secret", "kates-api-key",
		"-n", namespace,
		"-o", "go-template={{index .data \"api-key\"}}",
	).Output()
	if err != nil {
		return "", err
	}

	encoded := strings.TrimSpace(string(out))
	if encoded == "" {
		return "", fmt.Errorf("secret kates-api-key has empty api-key")
	}

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	apiKey := strings.TrimSpace(string(decoded))
	if apiKey == "" {
		return "", fmt.Errorf("decoded api-key is empty")
	}
	return apiKey, nil
}

func fetchKatesAPIKeyFromRunningPod(ctx context.Context, namespace string) (string, error) {
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	podOut, err := exec.CommandContext(
		checkCtx,
		"kubectl",
		"get", "pods",
		"-n", namespace,
		"-l", "app.kubernetes.io/instance=kates",
		"-o", "jsonpath={.items[0].metadata.name}",
	).Output()
	if err != nil {
		return "", err
	}

	podName := strings.TrimSpace(string(podOut))
	if podName == "" {
		return "", fmt.Errorf("no running kates pod found in namespace %q", namespace)
	}

	out, err := exec.CommandContext(
		checkCtx,
		"kubectl", "exec",
		"-n", namespace,
		podName,
		"--",
		"printenv", "KATES_API_KEY",
	).Output()
	if err != nil {
		return "", err
	}

	key := strings.TrimSpace(string(out))
	if key == "" {
		return "", fmt.Errorf("running pod %q has empty KATES_API_KEY", podName)
	}
	return key, nil
}

func isAPIKeyAccepted(endpoint, apiKey string) bool {
	if strings.TrimSpace(endpoint) == "" || strings.TrimSpace(apiKey) == "" {
		return false
	}

	checkURL := strings.TrimRight(endpoint, "/") + "/api/health"
	req, err := http.NewRequest(http.MethodGet, checkURL, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func applyPortForwardContext(cfg Config, ctxName, endpoint, apiKey string) Config {
	if cfg.Contexts == nil {
		cfg.Contexts = map[string]Context{}
	}
	if strings.TrimSpace(ctxName) == "" {
		ctxName = "local"
	}

	active := cfg.Contexts[ctxName]
	if active.Output == "" {
		active.Output = "table"
	}
	if endpoint != "" {
		active.URL = endpoint
	}
	if apiKey != "" {
		active.APIKey = apiKey
	}

	cfg.Contexts[ctxName] = active
	cfg.CurrentContext = ctxName
	return cfg
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
		case "kafka-ui":
			// kafka-ui can live in kafka, kates, or its own namespace
			actualNS = portsKafkaNS
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
		} else {
			// Fallback: scan all discovered services for this service name
			for key := range discovered {
				if strings.HasSuffix(key, "/"+svc) {
					parts := strings.SplitN(key, "/", 2)
					specs = append(specs, portForwardSpec{
						Label:     wk.Label,
						Namespace: parts[0],
						Resource:  "service/" + svc,
						Remote:    wk.Remote,
						Local:     wk.Local,
						URL:       wk.URL,
					})
					seen[wk.Label] = true
					break
				}
			}
		}
	}
	return specs
}

// runSinglePortForward is deprecated. Port forwards now run directly in the background.

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
