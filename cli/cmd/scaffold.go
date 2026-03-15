package cmd

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

//go:embed scenarios/*.yaml
var scenarioFS embed.FS

type scenarioMeta struct {
	filename    string
	name        string
	testType    string
	description string
}

var builtinScenarios = []scenarioMeta{
	{filename: "quick-load.yaml", name: "quick-load", testType: "LOAD", description: "Quick smoke test — 50k records, 2 producers, p99 < 100ms gate"},
	{filename: "production-load.yaml", name: "production-load", testType: "LOAD", description: "Production-grade — 1M records, 8 producers, acks=all, lz4, strict SLA"},
	{filename: "stress-test.yaml", name: "stress-test", testType: "STRESS", description: "High-throughput stress — 5M records, 16 producers, find breaking points"},
	{filename: "endurance-soak.yaml", name: "endurance-soak", testType: "ENDURANCE", description: "1-hour soak at 5k msg/s — detect GC pauses and log compaction issues"},
	{filename: "exactly-once.yaml", name: "exactly-once", testType: "ROUND_TRIP", description: "E2E integrity — idempotent + transactional, zero-loss, CRC verification"},
	{filename: "integrity-tx.yaml", name: "integrity-tx", testType: "INTEGRITY", description: "Transactional integrity — 4 producers, zstd, CRC, zero-loss verification"},
	{filename: "spike-test.yaml", name: "spike-test", testType: "SPIKE", description: "Burst traffic — 32 producers for 60s, test backpressure handling"},
	{filename: "ci-gate.yaml", name: "ci-gate", testType: "LOAD", description: "CI pipeline gate — fast 10k-record validation with strict zero-error SLA"},
}

var (
	scaffoldTypeFilter string
)

var testScaffoldCmd = &cobra.Command{
	Use:   "scaffold",
	Short: "Browse and export built-in test scenario templates",
	Long: `Browse the curated scenario library with 'list', preview with 'show',
and export ready-to-use YAML files with 'export'.

Use --type to filter by test type (e.g. LOAD, STRESS, INTEGRITY).
Without subcommands, lists all available templates.`,
	Run: func(cmd *cobra.Command, args []string) {
		showScenarioList()
	},
}

var scaffoldListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all available built-in scenario templates",
	Run: func(cmd *cobra.Command, args []string) {
		showScenarioList()
	},
}

func filteredScenarios() []scenarioMeta {
	if scaffoldTypeFilter == "" {
		return builtinScenarios
	}
	filter := strings.ToUpper(scaffoldTypeFilter)
	var result []scenarioMeta
	for _, s := range builtinScenarios {
		if s.testType == filter {
			result = append(result, s)
		}
	}
	return result
}

func showScenarioList() {
	scenarios := filteredScenarios()
	title := fmt.Sprintf("%d templates", len(scenarios))
	if scaffoldTypeFilter != "" {
		title = fmt.Sprintf("%d %s templates", len(scenarios), strings.ToUpper(scaffoldTypeFilter))
	}
	output.Banner("Scenario Library", title)

	rows := make([][]string, 0, len(scenarios))
	for _, s := range scenarios {
		rows = append(rows, []string{s.name, s.testType, s.description})
	}
	output.Table([]string{"Name", "Type", "Description"}, rows)
	fmt.Println()
	output.Hint("Use: kates test scaffold show <name>     — preview a template")
	output.Hint("Use: kates test scaffold export <name>   — write to current directory")
	output.Hint("Use: kates test scaffold export --all     — export all templates")
}

var scaffoldShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Preview a built-in scenario template with syntax highlighting",
	Args:  cobra.ExactArgs(1),
	Example: `  kates test scaffold show quick-load
  kates test scaffold show production-load`,
	RunE: func(cmd *cobra.Command, args []string) error {
		meta := findScenario(args[0])
		if meta == nil {
			return cmdErr(fmt.Sprintf("Unknown scenario '%s'. Use 'kates test scaffold list' to see available templates.", args[0]))
		}

		data, err := scenarioFS.ReadFile("scenarios/" + meta.filename)
		if err != nil {
			return cmdErr("Failed to read template: " + err.Error())
		}

		output.Banner(meta.name, meta.description)
		fmt.Println()

		var parsed interface{}
		if yaml.Unmarshal(data, &parsed) == nil {
			pretty, _ := yaml.Marshal(parsed)
			for _, line := range strings.Split(string(pretty), "\n") {
				if strings.Contains(line, ":") {
					parts := strings.SplitN(line, ":", 2)
					fmt.Printf("  %s:%s\n",
						output.AccentStyle.Render(parts[0]),
						parts[1],
					)
				} else if line != "" {
					fmt.Printf("  %s\n", line)
				}
			}
		} else {
			fmt.Println(string(data))
		}

		fmt.Println()
		output.Hint(fmt.Sprintf("Export: kates test scaffold export %s", meta.name))
		output.Hint(fmt.Sprintf("Run:    kates test apply -f %s --wait", meta.filename))
		return nil
	},
}

var (
	scaffoldExportAll  bool
	scaffoldExportDir  string
	scaffoldExportFile string
)

var scaffoldExportCmd = &cobra.Command{
	Use:   "export [name]",
	Short: "Export scenario template(s) to the current directory",
	Example: `  kates test scaffold export quick-load
  kates test scaffold export --all
  kates test scaffold export production-load --dir ./scenarios/`,
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := scaffoldExportDir
		if dir == "" {
			dir = "."
		}

		if scaffoldExportAll {
			scenarios := filteredScenarios()
			count := 0
			for _, s := range scenarios {
				if err := exportScenario(s, dir, ""); err != nil {
					output.Warn(fmt.Sprintf("Failed to export %s: %s", s.name, err))
				} else {
					count++
				}
			}
			output.Success(fmt.Sprintf("Exported %d scenario templates to %s", count, dir))
			return nil
		}

		if len(args) == 0 {
			return cmdErr("Provide a scenario name or use --all. See 'kates test scaffold list'.")
		}

		meta := findScenario(args[0])
		if meta == nil {
			return cmdErr(fmt.Sprintf("Unknown scenario '%s'. Use 'kates test scaffold list'.", args[0]))
		}

		return exportScenario(*meta, dir, scaffoldExportFile)
	},
}

func exportScenario(s scenarioMeta, dir string, outFile string) error {
	data, err := scenarioFS.ReadFile("scenarios/" + s.filename)
	if err != nil {
		return err
	}

	outPath := outFile
	if outPath == "" {
		outPath = filepath.Join(dir, s.filename)
	} else if !filepath.IsAbs(outPath) {
		outPath = filepath.Join(dir, outPath)
	}

	if _, err := os.Stat(outPath); err == nil {
		output.Warn(fmt.Sprintf("Skipping %s (already exists)", outPath))
		return nil
	}

	parentDir := filepath.Dir(outPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return err
	}

	if err := os.WriteFile(outPath, data, 0644); err != nil {
		return err
	}

	output.Success(fmt.Sprintf("Exported %s → %s", s.name, outPath))
	output.Hint(fmt.Sprintf("  Run: kates test apply -f %s --wait", outPath))
	return nil
}

func findScenario(name string) *scenarioMeta {
	name = strings.TrimSuffix(name, ".yaml")
	for _, s := range builtinScenarios {
		if s.name == name || s.filename == name {
			return &s
		}
	}
	return nil
}

func init() {
	testScaffoldCmd.PersistentFlags().StringVarP(&scaffoldTypeFilter, "type", "t", "", "Filter by test type (LOAD, STRESS, INTEGRITY, etc.)")
	scaffoldExportCmd.Flags().StringVarP(&scaffoldExportDir, "dir", "d", "", "Output directory (default: current)")
	scaffoldExportCmd.Flags().StringVarP(&scaffoldExportFile, "output", "o", "", "Output filename (overrides default)")
	scaffoldExportCmd.Flags().BoolVar(&scaffoldExportAll, "all", false, "Export all templates")

	testScaffoldCmd.AddCommand(scaffoldListCmd)
	testScaffoldCmd.AddCommand(scaffoldShowCmd)
	testScaffoldCmd.AddCommand(scaffoldExportCmd)
	testCmd.AddCommand(testScaffoldCmd)
}
