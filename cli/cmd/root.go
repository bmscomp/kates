package cmd

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	apiURL      string
	outputMode  string
	contextFlag string
	apiKeyFlag  string
	plainOutput bool
	apiClient   *client.Client
)

type Context struct {
	URL      string `yaml:"url"`
	Output   string `yaml:"output,omitempty"`
	APIKey   string `yaml:"api-key,omitempty"`
	ProxyURL string `yaml:"proxy-url,omitempty"`
	Insecure bool   `yaml:"insecure,omitempty"`
}

type Config struct {
	CurrentContext string             `yaml:"current-context"`
	Contexts       map[string]Context `yaml:"contexts"`
}

func configPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not determine home directory: %v\n", err)
		return ".kates.yaml"
	}
	return filepath.Join(home, ".kates.yaml")
}

func loadConfig() Config {
	cfg := Config{
		CurrentContext: "default",
		Contexts:       map[string]Context{"default": {URL: "http://localhost:8080", Output: "table"}},
	}
	data, err := os.ReadFile(configPath())
	if err == nil {
		if yamlErr := yaml.Unmarshal(data, &cfg); yamlErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to parse config %s: %v\n", configPath(), yamlErr)
		}
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
  detect         Deep cluster compatibility report for Kafka
  auto           Auto-detect cluster config and deploy Kafka
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
  watch          Live event stream of all Kates activity
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

Security:
  security audit      Full security posture scan with A-F grading
  security tls        TLS protocol, cipher, and certificate inspection
  security auth-test  ACL authorization probing per user
  security pentest    Adversarial penetration tests
  security compliance CIS Kafka, SOC2, PCI-DSS compliance report
  security baseline   Save security snapshot for drift detection
  security drift      Compare posture against saved baseline
  security gate       CI/CD gate — exit non-zero if below grade

Policy Engine (Kyverno):
  kyverno status      Show all ClusterPolicies with mode and rule counts
  kyverno violations  Pretty-print policy violations by namespace
  kyverno enforce     Switch a policy from Audit to Enforce mode
  kyverno audit       Switch a policy from Enforce to Audit mode

Flags:
  -o, --output     Output format: table or json
      --url        Override API URL for a single call
      --context    Use a named context for a single call
      --api-key    API key for authenticating with the server
  -h, --help       Show this help

Environment Variables:
  KATES_URL        Override API URL (lower priority than --url)
  KATES_OUTPUT     Override output format (lower priority than -o)
  KATES_CONTEXT    Override context (lower priority than --context)
  KATES_API_KEY    API key for authentication (lower priority than --api-key)

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

		// Apply dynamic local port fallback if using localhost/127.0.0.1
		apiURL = resolveFallbackURL(apiURL, isPortReachable)

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
		if !plainOutput {
			if envPlain := os.Getenv("KATES_PLAIN"); envPlain == "true" || envPlain == "1" {
				plainOutput = true
			}
		}

		output.SetPlain(plainOutput)

		if apiKeyFlag == "" {
			if envKey := os.Getenv("KATES_API_KEY"); envKey != "" {
				apiKeyFlag = envKey
			} else if ctx.APIKey != "" {
				apiKeyFlag = ctx.APIKey
			}
		}
		opts := client.ClientOptions{
			BaseURL:  apiURL,
			APIKey:   apiKeyFlag,
			ProxyURL: ctx.ProxyURL,
			Insecure: ctx.Insecure,
		}
		apiClient = client.NewWithOptions(opts)
	},
	SilenceUsage:  true,
	SilenceErrors: true,
}

func isPortReachable(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func resolveFallbackURL(url string, checkPort func(string) bool) string {
	if strings.Contains(url, "localhost:8080") || strings.Contains(url, "127.0.0.1:8080") {
		if !checkPort("127.0.0.1:8080") {
			if checkPort("127.0.0.1:30083") {
				return strings.Replace(url, "8080", "30083", 1)
			}
		}
	} else if strings.Contains(url, "localhost:30083") || strings.Contains(url, "127.0.0.1:30083") {
		if !checkPort("127.0.0.1:30083") {
			if checkPort("127.0.0.1:8080") {
				return strings.Replace(url, "30083", "8080", 1)
			}
		}
	}
	return url
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		// silentErr was already printed (styled) by cmdErr. Everything else
		// goes through the same styled channel, so a plain fmt.Errorf from a
		// RunE reads like every other kates error instead of a bare line —
		// one error voice for the whole CLI.
		if _, ok := err.(*silentErr); !ok {
			output.Error(err.Error())
		}
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&apiURL, "url", "", "Override API URL for this call")
	rootCmd.PersistentFlags().StringVarP(&outputMode, "output", "o", "", "Output format: table or json")
	rootCmd.PersistentFlags().StringVar(&contextFlag, "context", "", "Use a specific context instead of current")
	rootCmd.PersistentFlags().StringVar(&apiKeyFlag, "api-key", "", "API key for authenticating with the server")
	rootCmd.PersistentFlags().BoolVar(&plainOutput, "plain", false, "Disable interactive prompts and fancy UI formatting")
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
		// The curated template above highlights common workflows, but it is
		// hand-maintained and had drifted to listing 21 of 48 commands —
		// hiding deploy, clean, connect, upgrade, and portforward entirely.
		// This complete list is generated from the registered commands, so it
		// cannot rot.
		fmt.Println("All commands:")
		for _, c := range cmd.Commands() {
			if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
				continue
			}
			fmt.Printf("  %-16s %s\n", c.Name(), c.Short)
		}
		fmt.Println()
		fmt.Printf("Use \"%s [command] --help\" for more information about a command.\n", cmd.CommandPath())
	})
}
