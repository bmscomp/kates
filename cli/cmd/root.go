package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/klster/kates-cli/client"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	apiURL      string
	outputMode  string
	contextFlag string
	apiClient   *client.Client
)

type Context struct {
	URL    string `yaml:"url"`
	Output string `yaml:"output,omitempty"`
}

type Config struct {
	CurrentContext string             `yaml:"current-context"`
	Contexts       map[string]Context `yaml:"contexts"`
}

func configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kates.yaml")
}

func loadConfig() Config {
	cfg := Config{
		CurrentContext: "default",
		Contexts:       map[string]Context{"default": {URL: "http://localhost:8080", Output: "table"}},
	}
	data, err := os.ReadFile(configPath())
	if err == nil {
		yaml.Unmarshal(data, &cfg)
	}
	if cfg.Contexts == nil {
		cfg.Contexts = map[string]Context{}
	}
	return cfg
}

func saveConfig(cfg Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), data, 0644)
}

func activeContext(cfg Config) Context {
	name := cfg.CurrentContext
	if contextFlag != "" {
		name = contextFlag
	}
	if ctx, ok := cfg.Contexts[name]; ok {
		return ctx
	}
	return Context{URL: "http://localhost:8080", Output: "table"}
}

const helpTemplate = `
  ╦╔═  ╔═╗  ╔╦╗  ╔═╗  ╔═╗
  ╠╩╗  ╠═╣   ║   ║╣   ╚═╗
  ╩ ╩  ╩ ╩   ╩   ╚═╝  ╚═╝
  Kafka Advanced Testing & Engineering Suite

  Performance testing, chaos engineering, and
  trend analysis for Apache Kafka — from your terminal.

Quick Start:
  $ kates ctx set local --url http://localhost:30083
  $ kates ctx use local
  $ kates health

Health & Status:
  health         System health, Kafka connectivity, engine status
  status         Quick one-line system status
  doctor         Pre-flight cluster readiness checklist
  version        CLI, API, and runtime version info
  cluster        Kafka cluster metadata and topic listing
  test list      List test runs with optional filters
  test get       Show details of a specific test run

Testing:
  test create    Start a new performance test
  test apply     Apply a YAML test scenario definition
  test delete    Delete a test run
  test cleanup   Delete orphaned RUNNING tests
  test compare   Side-by-side metric diff of two runs
  test summary   Aggregate stats across all tests
  test export    Export results to CSV or JSON file
  test flame     ASCII latency histogram

Tuning:
  lab            Interactive performance tuning laboratory
  advisor        Analyze test results & recommend config improvements
  profile        Save, compare, and assert performance profiles
  flow           Declarative multi-step pipeline orchestrator
  benchmark      Run full test battery with letter-grade scorecard
  tune           Parameter sweep tests for optimal configuration

Analysis:
  report         View reports, export CSV/JUnit/Markdown/HTML
  trend          Historical performance trends with sparkline charts
  diff           Side-by-side comparison of two test run reports
  explain        Plain-English test narrative with verdict
  replay         Re-run a previous test with the same parameters
  scenario-diff  Compare scenario YAML against a test run
  gate           CI quality gate — exit non-zero if grade < threshold

Kafka Client:
  kafka          Interactive Kafka — brokers, topics, groups, produce, consume

Observability:
  dashboard      Full-screen monitoring dashboard (alias: dash)
  top            Live view of running tests (like kubectl top)
  watch          Live event stream of all KATES activity
  audit          Audit log of all mutating operations
  webhook        Manage test-completion webhook notifications
  cost           Estimate cloud costs for test configurations
  snapshot       Capture, compare, and diff cluster state
  changelog      Generate changelog from audit events
  badge          Generate status badges for README files

Toolbox:
  help           Help about any command
  init           Initialize workspace with config, scenarios, and CI gate
  test scaffold  Browse and export built-in scenario templates
  plugin         Discover and run external plugin commands
  tldr           Quick command reference cheatsheet
  docs           Man-style documentation for all commands
  theme          Manage CLI color themes

Configuration:
  ctx            Manage server contexts (like kubectl contexts)
  completion     Shell auto-completion (bash, zsh, fish, powershell)

Disruption & Chaos:
  disruption     Run, list, and monitor disruption experiments
  chaos          Chaos experiment history and probe analysis
  resilience     Combined performance + chaos resilience testing
  schedule       Automated recurring test schedules

Flags:
  -o, --output     Output format: table or json
      --url        Override API URL for a single call
      --context    Use a named context for a single call
  -h, --help       Show this help

Environment Variables:
  KATES_URL        Override API URL (lower priority than --url)
  KATES_OUTPUT     Override output format (lower priority than -o)
  KATES_CONTEXT    Override context (lower priority than --context)

Examples:
  $ kates health
  $ kates test create --type LOAD --records 100000
  $ kates test apply -f scenario.yaml --wait
  $ kates lab
  $ kates advisor abc123
  $ kates flow run -f release-qual.yaml
  $ kates benchmark --records 50000
  $ kates gate --min-grade B --type STRESS
  $ kates cost estimate --records 1M --cloud aws

Docs & more:  kates <command> --help
`

var rootCmd = &cobra.Command{
	Use:   "kates",
	Short: "Kates — Kafka Advanced Testing & Engineering Suite CLI",
	Long:  helpTemplate,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if contextFlag == "" {
			if envCtx := os.Getenv("KATES_CONTEXT"); envCtx != "" {
				contextFlag = envCtx
			}
		}
		cfg := loadConfig()
		ctx := activeContext(cfg)
		if apiURL == "" {
			if envURL := os.Getenv("KATES_URL"); envURL != "" {
				apiURL = envURL
			} else {
				apiURL = ctx.URL
			}
		}
		if outputMode == "" {
			if envOut := os.Getenv("KATES_OUTPUT"); envOut != "" {
				outputMode = envOut
			} else {
				outputMode = ctx.Output
			}
		}
		if outputMode == "" {
			outputMode = "table"
		}
		apiClient = client.New(apiURL)
	},
	SilenceUsage:  true,
	SilenceErrors: true,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		if _, ok := err.(*silentErr); !ok {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&apiURL, "url", "", "Override API URL for this call")
	rootCmd.PersistentFlags().StringVarP(&outputMode, "output", "o", "", "Output format: table or json")
	rootCmd.PersistentFlags().StringVar(&contextFlag, "context", "", "Use a specific context instead of current")
	rootCmd.AddCommand(docsCmd)

	defaultHelp := rootCmd.HelpFunc()
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if cmd != rootCmd {
			defaultHelp(cmd, args)
			return
		}
		fmt.Println(cmd.Long)
		fmt.Println()
		fmt.Println("Usage:")
		fmt.Printf("  %s [command]\n", cmd.CommandPath())
		fmt.Println()
		fmt.Printf("Use \"%s [command] --help\" for more information about a command.\n", cmd.CommandPath())
	})
}
