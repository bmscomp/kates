package cmd

import (
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var ctxCmd = &cobra.Command{
	Use:     "ctx",
	Aliases: []string{"context", "config"},
	Short:   "Manage CLI contexts (server connections)",
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
			rows = append(rows, []string{marker, name, ctx.URL, out})
		}
		output.Table([]string{"", "Name", "URL", "Output"}, rows)
	},
}

var ctxSetURL string
var ctxSetOutput string

var ctxSetCmd = &cobra.Command{
	Use:   "set <name>",
	Short: "Create or update a context",
	Args:  cobra.ExactArgs(1),
	Example: `  kates ctx set local    --url http://localhost:30083
  kates ctx set staging  --url https://kates-staging.company.com
  kates ctx set prod     --url https://kates.company.com --output json`,
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		if ctxSetURL == "" {
			output.Error("--url is required")
			return
		}
		cfg := loadConfig()
		out := ctxSetOutput
		if out == "" {
			out = "table"
		}
		cfg.Contexts[name] = Context{URL: ctxSetURL, Output: out}

		// Auto-select if first context
		if len(cfg.Contexts) == 1 {
			cfg.CurrentContext = name
		}

		if err := saveConfig(cfg); err != nil {
			output.Error("Failed to save: " + err.Error())
			return
		}
		output.Success(fmt.Sprintf("Context '%s' → %s", name, ctxSetURL))
		if cfg.CurrentContext == name {
			output.Hint("  (active)")
		} else {
			output.Hint(fmt.Sprintf("  Switch to it: kates ctx use %s", name))
		}
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
		cfg := loadConfig()
		if _, ok := cfg.Contexts[name]; !ok {
			output.Error(fmt.Sprintf("Context '%s' not found. Run: kates ctx show", name))
			return
		}
		cfg.CurrentContext = name
		if err := saveConfig(cfg); err != nil {
			output.Error("Failed to save: " + err.Error())
			return
		}
		ctx := cfg.Contexts[name]
		output.Success(fmt.Sprintf("Switched to '%s' → %s", name, ctx.URL))
	},
}

var ctxDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Remove a context",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		cfg := loadConfig()
		if _, ok := cfg.Contexts[name]; !ok {
			output.Error(fmt.Sprintf("Context '%s' not found", name))
			return
		}
		delete(cfg.Contexts, name)
		if cfg.CurrentContext == name {
			cfg.CurrentContext = ""
			for n := range cfg.Contexts {
				cfg.CurrentContext = n
				break
			}
		}
		if err := saveConfig(cfg); err != nil {
			output.Error("Failed to save: " + err.Error())
			return
		}
		output.Success(fmt.Sprintf("Context '%s' deleted", name))
		if cfg.CurrentContext != "" {
			output.Hint(fmt.Sprintf("  Active context: %s", cfg.CurrentContext))
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

var ctxExportFlag string

var ctxExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export contexts as YAML (for sharing with teammates)",
	Example: `  kates ctx export
  kates ctx export > team-contexts.yaml
  kates ctx export --name staging`,
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
		data, err := yaml.Marshal(exportCfg)
		if err != nil {
			output.Error("Failed to marshal config: " + err.Error())
			return
		}
		fmt.Print(string(data))
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

		cfg := loadConfig()
		imported := 0
		for name, ctx := range incoming.Contexts {
			cfg.Contexts[name] = ctx
			imported++
		}

		if err := saveConfig(cfg); err != nil {
			output.Error("Failed to save: " + err.Error())
			return
		}
		output.Success(fmt.Sprintf("Imported %d context(s)", imported))
		for name := range incoming.Contexts {
			output.Hint(fmt.Sprintf("  • %s", name))
		}
	},
}

func init() {
	ctxSetCmd.Flags().StringVar(&ctxSetURL, "url", "", "Kates API base URL (required)")
	ctxSetCmd.Flags().StringVar(&ctxSetOutput, "output", "", "Default output format for this context")

	ctxExportCmd.Flags().StringVar(&ctxExportFlag, "name", "", "Export only a specific context")
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
