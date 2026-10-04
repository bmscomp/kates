package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

var mcpFlags struct {
	allowClusters []string
	clusterLabel  string
}

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Serve Kates to AI agents over MCP (stdio, read-only, experimental)",
	Long: `Serve Kates to AI agents over the Model Context Protocol (MCP).

An MCP client such as Claude Code, Cursor, VS Code or Claude Desktop starts
this command itself and talks JSON-RPC over its stdin and stdout. Nothing else
is written to stdout; the log goes to stderr.

The server is read-only: its tools read from the Kates API and never start
load or faults. Every result names the Kafka cluster it describes (its
clusterId and --cluster-label), the tier ("observe") and the caveats that
apply to the data; the kates://caveats resource lists all of them. Text that
third parties control, such as alert rule annotations, arrives inside
«untrusted:…» fences. The server is experimental.

The tools cover the cluster (cluster_overview, cluster_topology,
consumer_group_lag), what Kates is doing (kates_activity), test runs
(list_runs, get_run, assess_run), chaos (list_chaos_catalog,
preview_disruption, disruption_report), and security posture and scenarios
(security_evidence, draft_scenario). preview_disruption runs the backend's
dry run, which injects nothing; draft_scenario checks a scenario file and
saves nothing. The resources are kates://caveats, the run reports,
disruption timelines, playbook plans and the built-in scenario templates;
the prompts are diagnose_run, did_kates_cause_this, plan_game_day,
debrief_disruption and security_posture_check.

The command refuses to start without:
  --context, or KATES_CONTEXT     the context to use. The server never falls
                                  back to the current context, which
                                  kates ctx use and kates ports change.
  --allow-cluster <clusterId>     a Kafka clusterId it may serve (repeatable).
                                  At start it reads the live clusterId from
                                  GET /api/cluster/info and refuses to start
                                  when it is not listed.

It also refuses --url, KATES_URL and --api-key. The API URL comes from the
context alone, as written, so the context names the API the server reads:
other commands switch between localhost:8080 and localhost:30083 when the
context's port does not answer and the other does, and kates mcp refuses to
start instead. A key on the command line shows in the process list and is
stored in the MCP client's configuration. The key is KATES_API_KEY when it is
set in the environment the client starts the server with, and the context's
key otherwise.

Before every tool call it reads the clusterId again and refuses the call if
the context's URL now reaches a different cluster, as it does when a
port-forward is pointed elsewhere. Calls are limited to 60 a minute and 4 at
a time; a call over either limit gets a retryable error instead of waiting.

These checks prevent accidents, not misuse. The key grants every endpoint,
and an agent that can run shell commands can read the same key and call the
API directly.`,
	Example: `  # The clusterId to allow
  kates cluster info --context lab

  # Register the server with Claude Code
  claude mcp add --transport stdio kates -- kates mcp --context lab --allow-cluster <clusterId>`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runMCP(cmd.Context(), mcpOverrides(cmd))
	},
}

func init() {
	mcpCmd.Flags().StringArrayVar(&mcpFlags.allowClusters, "allow-cluster", nil,
		"Kafka clusterId the server may serve (repeatable, at least one)")
	mcpCmd.Flags().StringVar(&mcpFlags.clusterLabel, "cluster-label", "lab",
		"Short label for the cluster, shown in every result")
	rootCmd.AddCommand(mcpCmd)
}

// mcpLabelRE keeps the label short and plain: it appears in every result and
// in messages the model reads.
var mcpLabelRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)

// mcpServeConfig is what runMCP needs from flags, environment and config
// file, checked before anything touches the network.
type mcpServeConfig struct {
	context string
	url     string // the context's URL, as the config file has it
	allowed []string
	label   string
}

// mcpOverrides names the flags and environment variables in use that would
// replace what the context says: the root command's PersistentPreRun lets
// --url and KATES_URL replace the context's URL, and --api-key its key.
func mcpOverrides(cmd *cobra.Command) []string {
	var in []string
	for _, flag := range []string{"url", "api-key"} {
		if f := cmd.Flags().Lookup(flag); f != nil && f.Changed {
			in = append(in, "--"+flag)
		}
	}
	if os.Getenv("KATES_URL") != "" {
		in = append(in, "KATES_URL")
	}
	return in
}

func mcpConfigFromFlags(contextName string, cfg Config, overrides []string, allowClusters []string, label string) (mcpServeConfig, error) {
	if contextName == "" {
		return mcpServeConfig{}, errors.New("kates mcp needs an explicit context: pass --context <name> or set KATES_CONTEXT. " +
			"It never falls back to the current context, which kates ctx use and kates ports change")
	}
	if _, ok := cfg.Contexts[contextName]; !ok {
		return mcpServeConfig{}, fmt.Errorf("context %q is not in %s; create it with: kates ctx set %s --url <url>",
			contextName, configPath(), contextName)
	}
	// --url or KATES_URL would serve an API other than the one the context
	// names, with the context's key, while the log still named the context.
	for _, o := range overrides {
		switch o {
		case "--api-key":
			return mcpServeConfig{}, fmt.Errorf("kates mcp does not take --api-key: a key on its command line shows in the "+
				"process list and is stored in the MCP client's configuration. Keep the key in the context "+
				"(kates ctx set %s --api-key <key>) or in KATES_API_KEY", contextName)
		case "KATES_URL":
			return mcpServeConfig{}, fmt.Errorf("kates mcp takes the API URL from the context only: unset KATES_URL "+
				"where the MCP client starts it, or point the context at that URL: kates ctx set %s --url <url>", contextName)
		default:
			return mcpServeConfig{}, fmt.Errorf("kates mcp takes the API URL from the context only: drop %s, "+
				"or point the context at that URL: kates ctx set %s --url <url>", o, contextName)
		}
	}
	if len(allowClusters) == 0 {
		return mcpServeConfig{}, errors.New("kates mcp needs at least one --allow-cluster <clusterId>; " +
			"kates cluster info --context " + contextName + " prints it")
	}
	allowed := make([]string, 0, len(allowClusters))
	for _, id := range allowClusters {
		id = strings.TrimSpace(id)
		if !mcpPrintableID(id) {
			return mcpServeConfig{}, fmt.Errorf("--allow-cluster %q is not a clusterId: it must be 1 to 128 printable characters without spaces", id)
		}
		allowed = append(allowed, id)
	}
	if !mcpLabelRE.MatchString(label) {
		return mcpServeConfig{}, fmt.Errorf("--cluster-label %q must be 1 to 32 letters, digits, '.', '_' or '-', starting with a letter or digit", label)
	}
	return mcpServeConfig{context: contextName, url: cfg.Contexts[contextName].URL, allowed: allowed, label: label}, nil
}

func mcpPrintableID(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// mcpContextClient returns base, the client the root command built, when its
// URL is the context's as written. The root command's PersistentPreRun swaps
// localhost:8080 for localhost:30083, and back, when the context's port does
// not answer and the other one does (resolveFallbackURL). For kates mcp that
// would send the key to whatever listens on the other port, and the start-up
// pin check would send it before the allowlist is checked, while the help and
// the log say the context names the API. So the swap stops the server.
func mcpContextClient(base *client.Client, contextName, contextURL string) (*client.Client, error) {
	want := strings.TrimRight(contextURL, "/")
	if base.BaseURL == want {
		return base, nil
	}
	return nil, fmt.Errorf("the URL of context %s, %s, does not answer, and kates mcp uses it as written: it does not "+
		"switch to %s as other kates commands do. Start what serves the context's URL (kates ports starts the "+
		"port-forward), or point the context at the URL you mean: kates ctx set %s --url <url>",
		contextName, mcpRedactURL(want), mcpRedactURL(base.BaseURL), contextName)
}

// mcpPinCluster reads the live clusterId and returns it when it is allowed.
func mcpPinCluster(ctx context.Context, c *client.Client, allowed []string) (string, error) {
	base := mcpRedactURL(c.BaseURL)
	info, err := c.ClusterInfo(ctx)
	if err != nil {
		return "", fmt.Errorf("cannot read the Kafka clusterId from %s/api/cluster/info: %s", base, mcpErrorText(err))
	}
	if info == nil || info.ClusterID == "" {
		return "", fmt.Errorf("%s/api/cluster/info returned no clusterId, so kates mcp cannot tell which cluster it would serve", base)
	}
	for _, id := range allowed {
		if info.ClusterID == id {
			return id, nil
		}
	}
	live := mcpSanitizeLine(info.ClusterID, 128)
	return "", fmt.Errorf("cluster %s is not allowed: %s reaches Kafka cluster %s, which is not in --allow-cluster. "+
		"If it is the cluster you mean, add --allow-cluster %s", live, base, live, live)
}

// mcpRedactURL is raw as the log and error messages show it, which the MCP
// client may keep: without user information (a password, or a token in the
// user name) or any query or fragment.
func mcpRedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(an unparsable URL)"
	}
	if u.User != nil {
		u.User = url.User("redacted")
	}
	u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = "", false, "", ""
	return u.String()
}

// mcpErrorText is err's text with the URL of a *url.Error in its chain
// redacted as mcpRedactURL does. http.Client masks only the password when it
// reports a failed request, so a token in the user name would otherwise reach
// the model in an error's detail, and the log. url.Error prints its URL
// quoted; when the text holds it some other way, the text is rebuilt from the
// error's parts rather than passed on.
func mcpErrorText(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	var ue *url.Error
	if !errors.As(err, &ue) || ue == nil || ue.URL == "" {
		return s
	}
	redacted := strconv.Quote(mcpRedactURL(ue.URL))
	if quoted := strconv.Quote(ue.URL); strings.Contains(s, quoted) {
		return strings.ReplaceAll(s, quoted, redacted)
	}
	return fmt.Sprintf("%s %s: %v", ue.Op, redacted, ue.Err)
}

// newMCPLogger writes the server's log to w, with every error in it passed
// through mcpErrorText: the MCP client keeps what the server writes to stderr.
func newMCPLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if err, ok := a.Value.Any().(error); ok {
				return slog.String(a.Key, mcpErrorText(err))
			}
			return a
		},
	})).With("component", "kates-mcp")
}

// runMCP serves on stdio until the client closes stdin or the process gets
// SIGINT or SIGTERM. overrides are the flags and variables that would replace
// the context's URL or key (mcpOverrides); any of them stops it.
func runMCP(ctx context.Context, overrides []string) error {
	// stdout belongs to JSON-RPC from here on. Anything the rest of the CLI
	// prints through the output package goes to stderr; os.Stdout itself is
	// redirected once the transport holds the real one (mcpStdioTransport).
	realStdout, outputOut := os.Stdout, output.Out
	output.Out = os.Stderr
	defer func() {
		os.Stdout = realStdout
		output.Out = outputOut
	}()

	cfg, err := mcpConfigFromFlags(contextFlag, loadConfig(), overrides, mcpFlags.allowClusters, mcpFlags.clusterLabel)
	if err != nil {
		return err
	}
	base, err := mcpContextClient(apiClient, cfg.context, cfg.url)
	if err != nil {
		return err
	}
	c, err := newMCPClient(base)
	if err != nil {
		return err
	}
	logger := newMCPLogger(os.Stderr)

	pinCtx, cancel := context.WithTimeout(ctx, mcpDefaultLimits.CallTimeout)
	clusterID, err := mcpPinCluster(pinCtx, c, cfg.allowed)
	cancel()
	if err != nil {
		return err
	}

	// The first SIGINT or SIGTERM cancels the calls in flight (Lifetime) and
	// stops the server. Its handler is then removed, so a second signal ends
	// the process at once whatever is still running.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	context.AfterFunc(ctx, stop)

	deps, err := newMCPDeps(mcpDepsConfig{
		Client:   c,
		Cluster:  mcpClusterRef{ID: clusterID, Label: cfg.label},
		Limits:   mcpDefaultLimits,
		Logger:   logger,
		Lifetime: ctx,
	})
	if err != nil {
		return err
	}
	server := newMCPServer(deps)

	logger.Info("serving on stdio (read-only, experimental)",
		"context", cfg.context, "url", mcpRedactURL(c.BaseURL), "cluster", clusterID, "label", cfg.label)
	err = server.Run(ctx, &mcpStdioTransport{})
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// mcpStdioTransport is mcp.StdioTransport plus one guarantee: once the
// connection holds the real stdout, os.Stdout points at stderr, so a stray
// fmt.Print anywhere in the process lands in the log instead of corrupting the
// JSON-RPC stream. The swap has to wait for Connect, which is when the SDK
// reads os.Stdout. runMCP restores it.
type mcpStdioTransport struct {
	mcp.StdioTransport
}

func (t *mcpStdioTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	conn, err := t.StdioTransport.Connect(ctx)
	if err != nil {
		return nil, err
	}
	os.Stdout = os.Stderr
	return conn, nil
}
