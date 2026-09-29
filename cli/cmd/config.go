package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var ctxCmd = &cobra.Command{
	Use:     "ctx",
	Aliases: []string{"context", "config"},
	Short:   "Manage Kates server connection profiles (not Kubernetes contexts)",
	Long: `Manage named connection profiles for the Kates API server: its URL,
API key, and preferred output format. The --context global flag selects one of
THESE profiles.

Not to be confused with Kubernetes contexts. Which CLUSTER kates deploys to
comes from your kubeconfig — kates deploy detects and lets you choose it, and
kubectl config use-context changes it.`,
}

var ctxShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show all contexts and current selection",
	Run: func(cmd *cobra.Command, args []string) {
		cfg := loadConfig()
		output.Header("Kates Contexts")
		output.KeyValue("Config File", configPath())
		output.KeyValue("Active Context", cfg.CurrentContext)
		fmt.Println()

		if len(cfg.Contexts) == 0 {
			output.Hint("  No contexts configured. Run: kates ctx set <name> --url <url>")
			return
		}

		names := make([]string, 0, len(cfg.Contexts))
		for name := range cfg.Contexts {
			names = append(names, name)
		}
		sort.Strings(names)

		rows := make([][]string, 0, len(names))
		for _, name := range names {
			ctx := cfg.Contexts[name]
			marker := ""
			if name == cfg.CurrentContext {
				marker = "→"
			}
			out := ctx.Output
			if out == "" {
				out = "table"
			}
			key := "—"
			if ctx.APIKey != "" {
				key = maskAPIKey(ctx.APIKey)
			}
			rows = append(rows, []string{marker, name, ctx.URL, out, key})
		}
		output.Table([]string{"", "Name", "URL", "Output", "API Key"}, rows)
	},
}

var ctxSetURL string
var ctxSetOutput string
var ctxSetAPIKey string
var ctxSetProxy string
var ctxSetInsecure bool

var ctxSetCmd = &cobra.Command{
	Use:   "set <name>",
	Short: "Create or update a context",
	Args:  cobra.ExactArgs(1),
	Example: `  kates ctx set local    --url http://localhost:30083
  kates ctx set staging  --url https://kates-staging.company.com
  kates ctx set prod     --url https://kates.company.com --output json --api-key mykey`,
	// RunE, not Run: this validates its input, and a command that rejects what
	// you gave it must exit non-zero. With Run the errors below printed and then
	// returned 0, so a script could not tell a stored context from a refused one.
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if ctxSetURL == "" {
			return cmdErr("--url is required")
		}

		// An --api-key that was PASSED but is empty is almost always a command
		// substitution that failed — most often
		//   --api-key "$(kubectl get secret ... )"
		// against a release that is not installed, where kubectl prints
		// "NotFound" to stderr and nothing to stdout. Storing that quietly and
		// reporting success buys a 401 on the next command with no clue why, so
		// it is rejected here instead.
		if cmd.Flags().Changed("api-key") && strings.TrimSpace(ctxSetAPIKey) == "" {
			output.Hint("  If it came from a command, that command produced nothing.")
			output.Hint("  Check with: kubectl get secret <release>-api-key -n <namespace>")
			output.Hint("  Omit --api-key entirely to store a context without one.")
			return cmdErr("--api-key was given but is empty")
		}

		out := ctxSetOutput
		if out == "" {
			out = "table"
		}
		var cfg Config
		err := updateConfig(func(c *Config) error {
			c.Contexts[name] = Context{
				URL:      ctxSetURL,
				Output:   out,
				APIKey:   ctxSetAPIKey,
				ProxyURL: ctxSetProxy,
				Insecure: ctxSetInsecure,
			}

			// Auto-select if first context
			if len(c.Contexts) == 1 {
				c.CurrentContext = name
			}
			cfg = *c
			return nil
		})
		if err != nil {
			return cmdErr("Failed to save: " + err.Error())
		}
		output.Success(fmt.Sprintf("Context '%s' → %s", name, ctxSetURL))
		if ctxSetAPIKey != "" {
			output.Hint("  API key configured ✓")
		}
		if cfg.CurrentContext == name {
			output.Hint("  (active)")
		} else {
			output.Hint(fmt.Sprintf("  Switch to it: kates ctx use %s", name))
		}
		return nil
	},
}

var ctxUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Switch to a context",
	Args:  cobra.ExactArgs(1),
	Example: `  kates ctx use local
  kates ctx use prod`,
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		var ctx Context
		err := updateConfig(func(cfg *Config) error {
			c, ok := cfg.Contexts[name]
			if !ok {
				return errContextNotFound
			}
			cfg.CurrentContext = name
			ctx = c
			return nil
		})
		switch {
		case errors.Is(err, errContextNotFound):
			output.Error(fmt.Sprintf("Context '%s' not found. Run: kates ctx show", name))
			return
		case err != nil:
			output.Error("Failed to save: " + err.Error())
			return
		}
		output.Success(fmt.Sprintf("Switched to '%s' → %s", name, ctx.URL))
	},
}

var ctxDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Remove a context",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		var current string
		err := updateConfig(func(cfg *Config) error {
			if _, ok := cfg.Contexts[name]; !ok {
				return errContextNotFound
			}
			delete(cfg.Contexts, name)
			if cfg.CurrentContext == name {
				cfg.CurrentContext = ""
				for n := range cfg.Contexts {
					cfg.CurrentContext = n
					break
				}
			}
			current = cfg.CurrentContext
			return nil
		})
		switch {
		case errors.Is(err, errContextNotFound):
			output.Error(fmt.Sprintf("Context '%s' not found", name))
			return
		case err != nil:
			output.Error("Failed to save: " + err.Error())
			return
		}
		output.Success(fmt.Sprintf("Context '%s' deleted", name))
		if current != "" {
			output.Hint(fmt.Sprintf("  Active context: %s", current))
		}
	},
}

var ctxCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Print the active context name and URL",
	Run: func(cmd *cobra.Command, args []string) {
		cfg := loadConfig()
		if cfg.CurrentContext == "" {
			output.Warn("No active context. Run: kates ctx set <name> --url <url>")
			return
		}
		ctx := activeContext(cfg)
		output.Success(fmt.Sprintf("%s → %s", cfg.CurrentContext, ctx.URL))
	},
}

// errContextNotFound ends a context update that names a context the config
// does not have.
var errContextNotFound = errors.New("context not found")

// apiKeyMask ends every masked key, and is the whole of a key too short to
// show any of.
const apiKeyMask = "****"

// maskAPIKey shows enough of a key to tell two keys apart and not enough to
// use one: the first four characters of a key of 16 or more (the chart
// generates 32), and none of a shorter key, where four would be a large share
// of it. kates ctx show used to slice the first four of any key, and panicked
// on a key shorter than that.
func maskAPIKey(key string) string {
	if len(key) < 16 {
		return apiKeyMask
	}
	return key[:4] + apiKeyMask
}

// isMaskedAPIKey reports whether key is what maskAPIKey prints rather than a
// key, as in a file written by kates ctx export without --reveal.
func isMaskedAPIKey(key string) bool {
	return key == apiKeyMask || (len(key) == 4+len(apiKeyMask) && strings.HasSuffix(key, apiKeyMask))
}

// proxyPasswordMask is what url.URL.Redacted puts in place of a password.
const proxyPasswordMask = "xxxxx"

// maskProxyURL hides the password of a proxy URL and keeps the proxy and the
// user, which tell a reader which proxy it is. A value that does not parse is
// left out: the client ignores such a proxy anyway, and it may hold a password
// that cannot be found in it.
func maskProxyURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if _, ok := u.User.Password(); !ok {
		return raw
	}
	return u.Redacted()
}

// isMaskedProxyURL reports whether raw carries the password maskProxyURL
// prints rather than one.
func isMaskedProxyURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	password, ok := u.User.Password()
	return ok && password == proxyPasswordMask
}

// withoutProxyPassword returns a proxy URL with its user but no password.
func withoutProxyPassword(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = url.User(u.User.Username())
	return u.String()
}

var (
	ctxExportFlag   string
	ctxExportReveal bool
)

var ctxExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export contexts as YAML (for sharing with teammates)",
	Long: `Export contexts as YAML, for sharing with teammates or for kates ctx import.

API keys are masked: the file shows at most the first four characters of
each, which is enough to tell keys apart. A proxy password is masked too, and
key-source, a digest of the key, is left out. Pass --reveal to include them,
for instance to move your contexts to another machine.

kates ctx import stores no masked value. For a context you already have, it
keeps the key (and proxy password) you have only while the file leaves the
context's URL, proxy and insecure setting as they are, and masks that same
key; otherwise the context arrives without a key.`,
	Example: `  kates ctx export
  kates ctx export > team-contexts.yaml
  kates ctx export --name staging
  kates ctx export --reveal > my-contexts.yaml`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := loadConfig()
		exportCfg := cfg
		if ctxExportFlag != "" {
			if ctx, ok := cfg.Contexts[ctxExportFlag]; ok {
				exportCfg = Config{
					CurrentContext: ctxExportFlag,
					Contexts:       map[string]Context{ctxExportFlag: ctx},
				}
			} else {
				output.Error(fmt.Sprintf("Context '%s' not found", ctxExportFlag))
				return
			}
		}
		// Keys used to go out in clear, so a file meant for a teammate, or a
		// terminal an agent reads, carried every key in the config. The
		// key-source goes too: it is a salted digest of the key, against which
		// guesses can be tested offline, and import never reads it.
		masked := false
		if !ctxExportReveal {
			contexts := make(map[string]Context, len(exportCfg.Contexts))
			for name, ctx := range exportCfg.Contexts {
				if ctx.APIKey != "" {
					ctx.APIKey = maskAPIKey(ctx.APIKey)
					masked = true
				}
				if p := maskProxyURL(ctx.ProxyURL); p != ctx.ProxyURL {
					ctx.ProxyURL = p
					masked = true
				}
				ctx.KeySource = ""
				contexts[name] = ctx
			}
			exportCfg.Contexts = contexts
		}
		data, err := yaml.Marshal(exportCfg)
		if err != nil {
			output.Error("Failed to marshal config: " + err.Error())
			return
		}
		fmt.Print(string(data))
		// On stderr, so the YAML on stdout stays exactly what import reads.
		if masked {
			fmt.Fprintln(output.Err, output.DimStyle.Render("  API keys and proxy passwords are masked; pass --reveal to include them"))
		}
	},
}

var ctxImportFile string

var ctxImportCmd = &cobra.Command{
	Use:   "import",
	Short: "Import contexts from a YAML file (merges with existing)",
	Example: `  kates ctx import --file team-contexts.yaml
  cat contexts.yaml | kates ctx import`,
	Run: func(cmd *cobra.Command, args []string) {
		var data []byte
		var err error
		if ctxImportFile != "" {
			data, err = os.ReadFile(ctxImportFile)
		} else {
			data, err = io.ReadAll(os.Stdin)
		}
		if err != nil {
			output.Error("Failed to read input: " + err.Error())
			return
		}

		var incoming Config
		if err := yaml.Unmarshal(data, &incoming); err != nil {
			output.Error("Invalid YAML: " + err.Error())
			return
		}

		imported := 0
		warnings := map[string][]string{} // context name → what import did with a masked value
		err = updateConfig(func(cfg *Config) error {
			for name, ctx := range incoming.Contexts {
				existing, had := cfg.Contexts[name]
				cfg.Contexts[name], warnings[name] = importContext(name, ctx, existing, had)
				imported++
			}
			return nil
		})
		if err != nil {
			output.Error("Failed to save: " + err.Error())
			return
		}
		output.Success(fmt.Sprintf("Imported %d context(s)", imported))
		names := make([]string, 0, len(incoming.Contexts))
		for name := range incoming.Contexts {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			output.Hint(fmt.Sprintf("  • %s", name))
			for _, w := range warnings[name] {
				output.Warn(w)
			}
		}
	},
}

// importContext returns what kates ctx import stores for the context name in
// a file, given the one of that name the config has (had is false when there
// is none), and the warnings to print about the masked values it met.
//
// A masked value is never stored: the mask would replace a working key or
// password with one every request is refused with. A masked key keeps the key
// the context has, but only while the file sends it where it went before: the
// same URL, proxy and insecure setting, and the mask of that same key. Import
// used to keep the key whatever the file said, so a shared or crafted file
// that named one of your contexts with another URL sent your key to that
// server on the next command.
func importContext(name string, in, old Context, had bool) (Context, []string) {
	var warnings []string
	if isMaskedProxyURL(in.ProxyURL) {
		if had && maskProxyURL(old.ProxyURL) == in.ProxyURL {
			in.ProxyURL = old.ProxyURL // the same proxy and user
		} else {
			in.ProxyURL = withoutProxyPassword(in.ProxyURL)
			warnings = append(warnings, fmt.Sprintf("%s: the file masks its proxy password; stored the proxy without one "+
				"(kates ctx set %s --url <url> --proxy <proxy-url>)", name, name))
		}
	}

	if !isMaskedAPIKey(in.APIKey) {
		// An imported key was not stored by kates on this machine, whatever
		// the file says, so kates ports and kates deploy must not replace it.
		in.KeySource = ""
		return in, warnings
	}

	mask := in.APIKey
	in.APIKey, in.KeySource = "", ""
	noKey := fmt.Sprintf("; no key stored (kates ctx set %s --url <url> --api-key <key>)", name)
	var why string
	switch {
	case !had || old.APIKey == "":
		why = fmt.Sprintf("%s: the file masks its API key", name)
	case in.URL != old.URL:
		why = fmt.Sprintf("%s: the file masks its API key and points %s at %s, not %s", name, name, in.URL, old.URL)
	case in.ProxyURL != old.ProxyURL || in.Insecure != old.Insecure:
		why = fmt.Sprintf("%s: the file masks its API key and changes the proxy or TLS settings of %s", name, name)
	case mask != maskAPIKey(old.APIKey):
		why = fmt.Sprintf("%s: the file masks another API key than the one %s has", name, name)
	default:
		in.APIKey, in.KeySource = old.APIKey, old.KeySource
		return in, append(warnings, fmt.Sprintf("%s: the file masks its API key; kept the key it already had", name))
	}
	return in, append(warnings, why+noKey)
}

func init() {
	ctxSetCmd.Flags().StringVar(&ctxSetURL, "url", "", "Kates API base URL (required)")
	ctxSetCmd.Flags().StringVar(&ctxSetOutput, "output", "", "Default output format for this context")
	ctxSetCmd.Flags().StringVar(&ctxSetAPIKey, "api-key", "", "API key for authentication")
	ctxSetCmd.Flags().StringVar(&ctxSetProxy, "proxy", "", "Proxy server URL (e.g., http://proxy:8080)")
	ctxSetCmd.Flags().BoolVar(&ctxSetInsecure, "insecure", false, "Skip TLS certificate verification (useful for SSL proxies)")

	ctxExportCmd.Flags().StringVar(&ctxExportFlag, "name", "", "Export only a specific context")
	ctxExportCmd.Flags().BoolVar(&ctxExportReveal, "reveal", false, "Print API keys, proxy passwords and key-source in clear instead of masked")
	ctxImportCmd.Flags().StringVar(&ctxImportFile, "file", "", "YAML file to import (reads stdin if omitted)")

	ctxCmd.AddCommand(ctxShowCmd)
	ctxCmd.AddCommand(ctxSetCmd)
	ctxCmd.AddCommand(ctxUseCmd)
	ctxCmd.AddCommand(ctxDeleteCmd)
	ctxCmd.AddCommand(ctxCurrentCmd)
	ctxCmd.AddCommand(ctxExportCmd)
	ctxCmd.AddCommand(ctxImportCmd)
	rootCmd.AddCommand(ctxCmd)

	registerCtxCompletions()
}
