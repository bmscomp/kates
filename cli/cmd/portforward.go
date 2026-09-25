package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/theme"
	"github.com/charmbracelet/lipgloss"
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
	{"kates/kates", katesAPILabel, 8080, 8080, "http://localhost:8080/api/health"},
	{"kates/kates", "Kates gRPC", 9000, 9000, ""},
	{"kafka/" + kafkaBootstrapPlaceholder, "Kafka Bootstrap (plain)", 9092, 9092, ""},
	{"kafka/" + kafkaBootstrapPlaceholder, "Kafka Bootstrap (TLS)", 9093, 9093, ""},
	{"kafka/apicurio", "Apicurio Schema Registry", 80, 8081, "http://localhost:8081/ui"},
	{"kafka-ui/kafka-ui", "Kafka UI", 8080, 8085, "http://localhost:8085"},
	{"monitoring/monitoring-grafana", "Grafana", 80, 3000, "http://localhost:3000"},
	{"monitoring/monitoring-kube-prometheus-prometheus", "Prometheus", 9090, 9090, "http://localhost:9090"},
	{"monitoring/monitoring-kube-prometheus-alertmanager", "Alertmanager", 9093, 9094, "http://localhost:9094"},
	{"jaeger/jaeger-query", "Jaeger UI", 16686, 16686, "http://localhost:16686"},
	{"kates/kates-postgresql", "PostgreSQL", 5432, 5432, ""},
}

// katesAPILabel names the forward of the Kates REST API, the one whose local
// port the "ports" context points at.
const katesAPILabel = "Kates REST API"

// kafkaBootstrapPlaceholder stands for "<primary name>-kafka-bootstrap" in
// wellKnownPorts and is substituted at match time from --kafka-name.
const kafkaBootstrapPlaceholder = "{kafka-bootstrap}"

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
The forwards keep running in the background after the command returns.

Points the CLI context "ports" at the forwarded API and makes it the current
context, creating it if needed. Stores the API key from Secret kates-api-key
in that context, unless it holds a key you set yourself, and checks the key
against the API. No other context is changed. A local port that another
program already listens on is not forwarded, and no key is sent to it.

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
	portsCmd.Flags().StringVar(&deployKafkaName, "kafka-name", "krafter", "Name of the primary Kafka cluster")
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
					if isKatesPortsCommand(string(cmdOut)) {
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

	// A port another program listens on cannot be forwarded, yet connecting
	// to it succeeds, so the check below would call it active and the API
	// key would go to that program. Such ports are left out.
	taken := takenLocalPorts(specs, 2*time.Second)

	// ── 3. Start all forwards in the background ──────────────────────────────
	fmt.Printf("\n    %s Establishing port-forwards in the background...\n", boldStyle.Render("⚡"))

	for _, spec := range specs {
		if taken[spec.Local] {
			continue
		}
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
	katesAPIPortTaken := 0
	for _, spec := range specs {
		if taken[spec.Local] {
			allOk = false
			if spec.Label == katesAPILabel {
				katesAPIPortTaken = spec.Local
			}
			fmt.Printf("    %s  %-26s  localhost:%-5d  %s\n",
				errStyle.Render("✖"),
				spec.Label,
				spec.Local,
				output.ErrorStyle.Render("[IN USE]"),
			)
			continue
		}
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
			if spec.Label == katesAPILabel {
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

	// Point the "ports" context at the forwarded API and make it current, so
	// follow-up commands (health, resilience, tests) work without manual
	// ctx/api-key edits.
	if katesAPIForwardActive {
		endpoint := fmt.Sprintf("http://localhost:%d", katesAPILocalPort)
		printPortsSync(os.Stdout, syncPortsContext(ctx, katesAPINS, endpoint))
	} else if katesAPIPortTaken != 0 {
		printAPIPortTaken(os.Stdout, katesAPIPortTaken)
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

// isKatesPortsCommand reports whether cmdLine, as ps prints it, runs kates
// ports: a kates binary whose subcommand, after any global flags, is ports.
//
// runPorts stops such a process before it starts forwards of its own. It used
// to stop every process whose command line merely held "kates" and "ports",
// which took in `kates test create --context ports`, run against the very
// context kates ports writes, and any command with a path such as reports/.
func isKatesPortsCommand(cmdLine string) bool {
	fields := strings.Fields(cmdLine)
	if len(fields) < 2 || !strings.HasPrefix(filepath.Base(fields[0]), "kates") {
		return false
	}
	for i := 1; i < len(fields); i++ {
		arg := fields[i]
		if !strings.HasPrefix(arg, "-") {
			return arg == "ports"
		}
		if globalFlagTakesValue(arg) {
			i++ // its value is the next field
		}
	}
	return false
}

// globalFlagTakesValue reports whether arg is a global flag, --context or -o
// for instance, whose value follows as a separate argument.
func globalFlagTakesValue(arg string) bool {
	if strings.Contains(arg, "=") {
		return false
	}
	if name, ok := strings.CutPrefix(arg, "--"); ok {
		f := rootCmd.PersistentFlags().Lookup(name)
		return f != nil && f.NoOptDefVal == ""
	}
	if len(arg) == 2 {
		f := rootCmd.PersistentFlags().ShorthandLookup(arg[1:])
		return f != nil && f.NoOptDefVal == ""
	}
	return false
}

// takenLocalPorts returns the local ports of specs that another program
// listens on. It waits up to wait for them to come free, since the forwards
// runPorts stopped a moment ago may still be closing.
func takenLocalPorts(specs []portForwardSpec, wait time.Duration) map[int]bool {
	deadline := time.Now().Add(wait)
	for {
		taken := map[int]bool{}
		for _, s := range specs {
			if localPortTaken(s.Local) {
				taken[s.Local] = true
			}
		}
		if len(taken) == 0 || time.Now().After(deadline) {
			return taken
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// localPortTaken reports whether a program listens on port at localhost, on
// IPv4 or IPv6. kubectl port-forward cannot bind such a port, but a
// connection to it succeeds all the same, and before this check the key
// check sent the API key from Secret kates-api-key to whatever program held
// the port. An error other than "address in use" (no IPv6, for one) says
// nothing about the port.
func localPortTaken(port int) bool {
	for _, host := range []string{"127.0.0.1", "::1"} {
		ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err == nil {
			ln.Close()
			continue
		}
		if errors.Is(err, syscall.EADDRINUSE) {
			return true
		}
	}
	return false
}

// printAPIPortTaken reports that the API was not forwarded because another
// program holds its local port, and that nothing was sent to that program.
func printAPIPortTaken(w io.Writer, port int) {
	fmt.Fprintf(w, "    %s localhost:%d is in use by another program, so the API is not forwarded there\n",
		output.ErrorStyle.Render("✖"), port)
	fmt.Fprintf(w, "      %s\n", output.DimStyle.Render(fmt.Sprintf(
		"Context %q is unchanged and no API key was sent. Find the program with: lsof -nP -iTCP:%d -sTCP:LISTEN", portsContextName, port)))
}

// portsContextName is the one CLI context `kates ports` writes.
//
// kates ports used to rewrite whichever context was current, or named by
// --context: its URL, and its key with the one in Secret kates-api-key
// whenever the API answered. A remote context became a localhost one, and a
// context holding a narrower key for another tool (an MCP server, a CI job)
// was handed the shared admin key without a word. The forward's settings now
// live in a context of their own, and every other context is left as it is.
const portsContextName = "ports"

// apiKeySecretName is the Secret the kates chart generates the API key into.
// kates ports reads the key from it and nowhere else. It used to fall back to
// `kubectl exec … printenv KATES_API_KEY` in a running backend pod, which
// needs exec rights on that pod and copies out whatever key it was started
// with.
const apiKeySecretName = "kates-api-key"

// secretKeySource is the key-source kates records next to a key it copied
// from Secret kates-api-key: the Secret's name and the first 12 hex digits of
// the key's SHA-256. The digest ties the record to that one key. A mark on the
// context alone would still claim a key typed over the copied one, in the
// file or by a command that leaves the mark in place, and kates would replace
// it; with the digest, such a key no longer matches and counts as the user's.
func secretKeySource(apiKey string) string {
	sum := sha256.Sum256([]byte(apiKey))
	return apiKeySecretName + "@sha256:" + hex.EncodeToString(sum[:6])
}

// keyReplaceable reports whether kates may put the Secret's key into c: c has
// no key, or holds the very key kates copied there. kates ports and kates
// deploy, the two commands that copy the Secret's key, both go by it.
func keyReplaceable(c Context) bool {
	return strings.TrimSpace(c.APIKey) == "" || c.KeySource == secretKeySource(c.APIKey)
}

// storeSecretKey puts a key read from Secret kates-api-key into c and records
// that kates put it there.
func storeSecretKey(c *Context, apiKey string) {
	c.APIKey = apiKey
	c.KeySource = secretKeySource(apiKey)
}

// sameKey reports whether a and b hold the same key with the same key-source.
func sameKey(a, b Context) bool {
	return a.APIKey == b.APIKey && a.KeySource == b.KeySource
}

// keyProbePath is where kates ports checks a key: a protected, read-only
// endpoint that answers from memory. The check used to call /api/health,
// which is public, so every key passed it and a wrong key was stored as a
// working one.
const keyProbePath = "/api/tests/types"

// keyVerdict is what the API said about a key.
type keyVerdict int

const (
	keyUnverified keyVerdict = iota // the API could not be asked, or answered neither way
	keyAccepted
	keyRejected
)

type keyCheck struct {
	Verdict keyVerdict
	Detail  string // the HTTP status or the error behind the verdict
}

// keyOutcome is what syncPortsContext did with the key of the "ports" context.
type keyOutcome int

const (
	keyFromSecret       keyOutcome = iota // the Secret's key was stored
	keySecretRejected                     // the API rejected the Secret's key, so it was not stored
	keySecretUnread                       // the Secret could not be read
	keyUserOwned                          // a key kates did not store was there, and was left alone
	keyChangedMeanwhile                   // someone set the key while kates ports ran, and theirs was kept
)

// portsSync is what syncPortsContext did, for printPortsSync to report.
type portsSync struct {
	Endpoint  string
	Namespace string
	Created   bool   // the "ports" context did not exist before
	Previous  string // the context that was current before, when it was not "ports"
	Override  string // a --context or KATES_CONTEXT value, which outranks the current context

	Key         keyOutcome
	SecretCheck keyCheck // the verdict on the Secret's key (keyFromSecret, keySecretRejected)
	SecretErr   error    // why the Secret could not be read (keySecretUnread)
	// KeptKey is set when the context ends up holding a key other than the
	// Secret's: the user's, or one kates ports stored on an earlier run.
	// KeptCheck is the API's verdict on it.
	KeptKey   bool
	KeptCheck keyCheck
	SaveErr   error
}

// syncPortsContext points the "ports" context at the forwarded API, stores the
// key from Secret kates-api-key in it when it may, and makes it current. It
// changes no other context.
//
// "When it may" is keyReplaceable: the context holds no key yet, or holds the
// one kates stored there. A key the user put there stays, whatever the Secret
// says; the API's verdict on it is reported instead.
func syncPortsContext(ctx context.Context, appNS, endpoint string) portsSync {
	result := portsSync{Endpoint: endpoint, Namespace: appNS}
	if contextFlag != "" && contextFlag != portsContextName {
		result.Override = contextFlag
	}

	// Decide on a snapshot of "ports" and do the slow part, reading the Secret
	// and checking keys against the API (up to about 20 seconds), before
	// anything is written. The write below re-reads the file and changes only
	// "ports" and the current context, so whatever another command saved in
	// the meantime stays. This used to save the whole file as it was read
	// before those calls, and put back the old value of any context changed
	// during them.
	before := loadConfig().Contexts[portsContextName]
	want := before // the key "ports" should end up holding, with its key-source
	if !keyReplaceable(before) {
		result.Key = keyUserOwned
	} else if secretKey, err := fetchKatesAPIKey(ctx, appNS); err != nil {
		result.Key = keySecretUnread
		result.SecretErr = err
	} else {
		result.SecretCheck = checkAPIKey(ctx, endpoint, secretKey)
		if result.SecretCheck.Verdict == keyRejected {
			// Storing a key the API refuses would replace one that may still
			// work: a backend not restarted since the Secret changed still
			// accepts the key kates ports stored before.
			result.Key = keySecretRejected
		} else {
			result.Key = keyFromSecret
			storeSecretKey(&want, secretKey)
		}
	}
	if result.Key != keyFromSecret && strings.TrimSpace(want.APIKey) != "" {
		result.KeptKey = true
		result.KeptCheck = checkAPIKey(ctx, endpoint, want.APIKey)
	}

	result.SaveErr = updateConfig(func(cfg *Config) error {
		pc, exists := cfg.Contexts[portsContextName]
		result.Created = !exists
		result.Previous = ""
		if cfg.CurrentContext != portsContextName {
			result.Previous = cfg.CurrentContext
		}
		switch {
		case sameKey(pc, before), sameKey(pc, want):
			pc.APIKey, pc.KeySource = want.APIKey, want.KeySource
		case result.Key == keyFromSecret && keyReplaceable(pc):
			// Another kates stored a key meanwhile, or the key was removed:
			// still one this run may replace.
			pc.APIKey, pc.KeySource = want.APIKey, want.KeySource
		default:
			// The key changed during the run to one kates did not store, so
			// someone set it, and it is newer than anything decided above.
			result.Key = keyChangedMeanwhile
			result.KeptKey = false
		}
		pc.URL = endpoint
		if pc.Output == "" {
			pc.Output = "table"
		}
		cfg.Contexts[portsContextName] = pc
		cfg.CurrentContext = portsContextName
		return nil
	})
	return result
}

// checkAPIKey asks the API whether it accepts apiKey. Only 401 and 403 count
// as a rejection, because the auth filter answers before any resource runs;
// anything else short of a 2xx leaves the key unverified.
func checkAPIKey(ctx context.Context, endpoint, apiKey string) keyCheck {
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, strings.TrimRight(endpoint, "/")+keyProbePath, nil)
	if err != nil {
		return keyCheck{Verdict: keyUnverified, Detail: err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return keyCheck{Verdict: keyUnverified, Detail: err.Error()}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))

	detail := "HTTP " + resp.Status
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return keyCheck{Verdict: keyAccepted, Detail: detail}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return keyCheck{Verdict: keyRejected, Detail: detail}
	default:
		return keyCheck{Verdict: keyUnverified, Detail: detail}
	}
}

// fetchKatesAPIKey reads the API key from Secret kates-api-key.
func fetchKatesAPIKey(ctx context.Context, namespace string) (string, error) {
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	out, err := runExecOutputFn(checkCtx,
		"kubectl",
		"get", "secret", apiKeySecretName,
		"-n", namespace,
		"-o", "go-template={{index .data \"api-key\"}}",
	)
	if err != nil {
		// kubectl explains itself on stderr ("secrets … not found",
		// "forbidden"); "exit status 1" alone does not say which.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if msg := strings.TrimSpace(strings.SplitN(string(exitErr.Stderr), "\n", 2)[0]); msg != "" {
				return "", errors.New(msg)
			}
		}
		return "", err
	}

	// The go-template prints "<no value>" for a key the Secret lacks.
	encoded := strings.TrimSpace(string(out))
	if encoded == "" || encoded == "<no value>" {
		return "", fmt.Errorf("secret %s has no api-key entry", apiKeySecretName)
	}

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("secret %s: api-key is not base64: %w", apiKeySecretName, err)
	}
	apiKey := strings.TrimSpace(string(decoded))
	if apiKey == "" {
		return "", fmt.Errorf("secret %s has an empty api-key", apiKeySecretName)
	}
	return apiKey, nil
}

// printPortsSync reports what syncPortsContext did. A key the API rejects,
// the Secret's or the one left in the context, gets a red line of its own that
// names the key and the status: the old report said "synced" in every case,
// and the rejection only surfaced as a 403 on the next command.
func printPortsSync(w io.Writer, s portsSync) {
	okMark := output.SuccessStyle.Render("✓")
	warnMark := output.WarningStyle.Render("⚠")
	errMark := output.ErrorStyle.Render("✖")
	note := func(format string, args ...any) {
		fmt.Fprintf(w, "      %s\n", output.DimStyle.Render(fmt.Sprintf(format, args...)))
	}

	if s.SaveErr != nil {
		fmt.Fprintf(w, "    %s Could not save context %q: %v\n", warnMark, portsContextName, s.SaveErr)
		return
	}

	verb := "Updated"
	if s.Created {
		verb = "Created"
	}
	fmt.Fprintf(w, "    %s %s context %q → %s, now the current context\n", okMark, verb, portsContextName, s.Endpoint)
	if s.Previous != "" {
		note("Context %q was current and is unchanged; switch back with: kates ctx use %s", s.Previous, s.Previous)
	}

	secret := fmt.Sprintf("Secret %s (namespace %s)", apiKeySecretName, s.Namespace)
	switch s.Key {
	case keyFromSecret:
		if s.SecretCheck.Verdict == keyAccepted {
			fmt.Fprintf(w, "    %s API key from %s, accepted by the API\n", okMark, secret)
		} else {
			fmt.Fprintf(w, "    %s API key from %s stored, but not checked: %s\n", warnMark, secret, s.SecretCheck.Detail)
		}
	case keySecretRejected:
		fmt.Fprintf(w, "    %s The API rejected the key in %s with %s; it was not stored\n", errMark, secret, s.SecretCheck.Detail)
		note("If the Secret changed after the backend started, the backend still expects the old key; restart it: kubectl rollout restart deployment/kates -n %s", s.Namespace)
	case keySecretUnread:
		fmt.Fprintf(w, "    %s No API key from %s: %v\n", warnMark, secret, s.SecretErr)
	case keyUserOwned:
		fmt.Fprintf(w, "    %s Kept the API key already in context %q: kates replaces only a key it copied from the Secret itself\n", okMark, portsContextName)
	case keyChangedMeanwhile:
		fmt.Fprintf(w, "    %s The API key in context %q changed while kates ports ran; kept the new key without checking it\n", warnMark, portsContextName)
		note("Run kates ports again to check it.")
	}

	switch {
	case s.Key == keyFromSecret, s.Key == keyChangedMeanwhile:
	case !s.KeptKey:
		note("Context %q has no API key. If the API requires one, every command but kates health fails until it has one.", portsContextName)
	case s.KeptCheck.Verdict == keyAccepted:
		note("The key in context %q is accepted by the API.", portsContextName)
	case s.KeptCheck.Verdict == keyRejected:
		fmt.Fprintf(w, "    %s The API rejects the key in context %q (%s)\n", errMark, portsContextName, s.KeptCheck.Detail)
		note("Set a working key with: kates ctx set %s --url %s --api-key <key>", portsContextName, s.Endpoint)
		if s.Key == keyUserOwned {
			note("or let kates ports store the Secret's key: kates ctx delete %s, then kates ports", portsContextName)
		}
	default:
		note("Could not check the key in context %q: %s", portsContextName, s.KeptCheck.Detail)
	}

	if s.Override != "" {
		fmt.Fprintf(w, "    %s --context or KATES_CONTEXT selects %q, which outranks the current context:\n", warnMark, s.Override)
		note("commands use %q, not %q, until you pass --context %s or unset KATES_CONTEXT", s.Override, portsContextName, portsContextName)
	}
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
		// The primary's bootstrap Service is named after the cluster, which
		// is a flag (--kafka-name), not a constant.
		wk.Match = strings.Replace(wk.Match, kafkaBootstrapPlaceholder, deployKafkaName+"-kafka-bootstrap", 1)
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
