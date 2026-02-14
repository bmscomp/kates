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

Core:
  health       System health, Kafka connectivity, engine status
  cluster      Kafka cluster metadata and topic listing
  test         Create, list, inspect, and delete test runs
  report       View reports, export CSV/JUnit, compare runs

Analysis:
  trend        Historical performance trends with sparkline charts
  resilience   Chaos-performance correlation testing
  schedule     Automated recurring test schedules

Observability:
  dashboard    Full-screen monitoring dashboard (alias: dash)
  top          Live view of running tests (like kubectl top)
  status       Quick one-line system status
  version      CLI, API, and runtime version info

Configuration:
  ctx          Manage server contexts (like kubectl contexts)
  completion   Shell auto-completion (bash, zsh, fish, powershell)

Flags:
  -o, --output     Output format: table or json
      --url        Override API URL for a single call
      --context    Use a named context for a single call
  -h, --help       Show this help

Examples:
  $ kates test create --type LOAD --records 100000
  $ kates test apply -f scenario.yaml --wait
  $ kates report diff <id1> <id2>
  $ kates trend --type LOAD --metric p99LatencyMs --days 30
  $ kates dashboard

Docs & more:  kates <command> --help
`

var rootCmd = &cobra.Command{
	Use:   "kates",
	Short: "KATES — Kafka Advanced Testing & Engineering Suite CLI",
	Long:  helpTemplate,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		cfg := loadConfig()
		ctx := activeContext(cfg)
		if apiURL == "" {
			apiURL = ctx.URL
		}
		if outputMode == "" {
			outputMode = ctx.Output
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
}
