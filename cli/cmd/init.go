package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new Kates workspace with config, scenarios, and CI gate",
	Long: `Sets up a complete Kates project in the current directory:
  1. Adds a named context to ~/.kates.yaml and makes it current
  2. Exports built-in scenario templates (from the embedded library)
  3. Generates a CI gate script (kates-ci.sh) for pipeline integration

The other contexts in ~/.kates.yaml are kept. A context of that name that
already exists is kept as it is; init refuses to point it at a different
--url, which kates ctx set does.`,
	Example: `  kates init
  kates init --url http://kates.internal:8080 --name production
  kates init --no-scenarios --no-ci`,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		url, _ := cmd.Flags().GetString("url")
		noScenarios, _ := cmd.Flags().GetBool("no-scenarios")
		noCi, _ := cmd.Flags().GetBool("no-ci")
		dir, _ := cmd.Flags().GetString("dir")

		if name == "" {
			name = "default"
		}
		if url == "" {
			url = "http://localhost:8080"
		}
		if dir == "" {
			dir = "."
		}

		output.Banner("kates init", "Project scaffolder")
		fmt.Println()

		res, err := initContext(name, url, cmd.Flags().Changed("url"))
		if err != nil {
			return cmdErr(err.Error())
		}
		// The CI script reaches the server the context does, which is the
		// stored URL when the context was already there.
		url = res.URL
		switch {
		case res.Created:
			output.Success(fmt.Sprintf("Created ~/.kates.yaml with context '%s' → %s", name, url))
		case res.Kept:
			output.Success(fmt.Sprintf("Kept context '%s' → %s in ~/.kates.yaml", name, url))
		default:
			output.Success(fmt.Sprintf("Added context '%s' → %s to ~/.kates.yaml", name, url))
		}
		if res.Previous != "" && res.Previous != name {
			output.Hint(fmt.Sprintf("  It is now the current context; it was '%s' (kates ctx use %s to switch back).", res.Previous, res.Previous))
		}

		if !noScenarios {
			scenariosDir := filepath.Join(dir, "scenarios")
			if err := os.MkdirAll(scenariosDir, 0755); err != nil {
				output.Warn("Failed to create scenarios/ directory: " + err.Error())
			} else {
				count := 0
				for _, s := range builtinScenarios {
					data, err := scenarioFS.ReadFile("scenarios/" + s.filename)
					if err != nil {
						continue
					}
					outPath := filepath.Join(scenariosDir, s.filename)
					if _, err := os.Stat(outPath); err == nil {
						continue
					}
					if err := os.WriteFile(outPath, data, 0644); err != nil {
						output.Warn("  Failed: " + s.filename)
						continue
					}
					count++
				}
				output.Success(fmt.Sprintf("Exported %d scenario templates to %s/", count, scenariosDir))
			}
		}

		if !noCi {
			ciPath := filepath.Join(dir, "kates-ci.sh")
			if _, err := os.Stat(ciPath); err != nil {
				ciScript := generateCIScript(name, url)
				if err := os.WriteFile(ciPath, []byte(ciScript), 0755); err != nil {
					output.Warn("Failed to create kates-ci.sh: " + err.Error())
				} else {
					output.Success("Created kates-ci.sh — CI pipeline gate script")
				}
			} else {
				output.Hint("  Skipping kates-ci.sh (already exists)")
			}
		}

		fmt.Println()
		output.SubHeader("Project Structure")
		fmt.Println("  .")
		if !noScenarios {
			fmt.Println("  ├── scenarios/")
			for i, s := range builtinScenarios {
				prefix := "│"
				if i == len(builtinScenarios)-1 && noCi {
					prefix = " "
				}
				fmt.Printf("  │   %s── %s\n", func() string {
					if i == len(builtinScenarios)-1 {
						return "└"
					}
					return "├"
				}(), s.filename)
				_ = prefix
			}
		}
		if !noCi {
			fmt.Println("  └── kates-ci.sh")
		}

		fmt.Println()
		output.SubHeader("Next Steps")
		output.Hint("  1. Verify connection:")
		fmt.Printf("     %s\n", output.AccentStyle.Render("kates test list"))
		output.Hint("  2. Browse scenario templates:")
		fmt.Printf("     %s\n", output.AccentStyle.Render("kates test scaffold list"))
		output.Hint("  3. Run a quick test:")
		fmt.Printf("     %s\n", output.AccentStyle.Render("kates test apply -f scenarios/quick-load.yaml --wait"))
		output.Hint("  4. Add to CI pipeline:")
		fmt.Printf("     %s\n", output.AccentStyle.Render("chmod +x kates-ci.sh && ./kates-ci.sh"))
		output.Hint("  5. Inspect Kafka cluster:")
		fmt.Printf("     %s\n", output.AccentStyle.Render("kates kafka brokers"))

		return nil
	},
}

func generateCIScript(ctxName, apiURL string) string {
	return `#!/usr/bin/env bash
set -euo pipefail

KATES_URL="${KATES_URL:-` + apiURL + `}"
SCENARIO_FILE="${1:-scenarios/ci-gate.yaml}"
EXIT_CODE=0

echo "╭──────────────────────────────────╮"
echo "│   Kates — CI Performance Gate    │"
echo "╰──────────────────────────────────╯"
echo ""
echo "API:      ${KATES_URL}"
echo "Scenario: ${SCENARIO_FILE}"
echo ""

if ! command -v kates &>/dev/null; then
  echo "ERROR: kates CLI not found in PATH"
  echo "Install: go install github.com/bmscomp/kates/cli@latest"
  exit 1
fi

if [ ! -f "${SCENARIO_FILE}" ]; then
  echo "ERROR: Scenario file not found: ${SCENARIO_FILE}"
  echo "Run 'kates init' to generate default scenarios"
  exit 1
fi

echo "Running performance gate..."
echo ""

kates test apply \
  --url "${KATES_URL}" \
  -f "${SCENARIO_FILE}" \
  --wait

EXIT_CODE=$?

if [ ${EXIT_CODE} -eq 0 ]; then
  echo ""
  echo "✓ Performance gate passed"
else
  echo ""
  echo "✖ Performance gate FAILED (exit code ${EXIT_CODE})"
  echo "  Review: kates test list --status FAILED --url ${KATES_URL}"
fi

exit ${EXIT_CODE}
`
}

func init() {
	initCmd.Flags().String("name", "default", "Context name")
	initCmd.Flags().String("url", "http://localhost:8080", "Kates API URL")
	initCmd.Flags().String("dir", ".", "Project directory to scaffold into")
	initCmd.Flags().Bool("no-scenarios", false, "Skip scenario template export")
	initCmd.Flags().Bool("no-ci", false, "Skip CI gate script generation")
	rootCmd.AddCommand(initCmd)
}

// initResult says what initContext did to the config.
type initResult struct {
	URL      string // the context's URL, as stored
	Created  bool   // there was no config file, and init wrote one
	Kept     bool   // the context was there already and was left as it was
	Previous string // the current context before init
}

// initContext adds the context name, for url, to the config and makes it
// current, keeping every other context.
//
// kates init used to write a config holding that one context, so running it
// in a second project deleted every other context and its API key. A context
// of the same name is kept as it is, API key included. When the caller asked
// for a different URL (urlGiven), init refuses rather than send that key to
// another server.
func initContext(name, url string, urlGiven bool) (initResult, error) {
	var res initResult
	err := updateConfig(func(cfg *Config) error {
		if _, err := os.Stat(configPath()); errors.Is(err, fs.ErrNotExist) {
			// A new file holds the context init makes, not the built-in
			// default as well.
			*cfg = Config{Contexts: map[string]Context{}}
			res.Created = true
		}
		res.Previous = cfg.CurrentContext
		if existing, ok := cfg.Contexts[name]; ok {
			if urlGiven && existing.URL != url {
				return fmt.Errorf("context '%s' already points at %s, not %s.\n"+
					"  Use another --name, or change it with: kates ctx set %s --url %s",
					name, existing.URL, url, name, url)
			}
			res.URL, res.Kept = existing.URL, true
		} else {
			cfg.Contexts[name] = Context{URL: url, Output: "table"}
			res.URL = url
		}
		cfg.CurrentContext = name
		return nil
	})
	return res, err
}
