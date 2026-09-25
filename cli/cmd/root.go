package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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
	// KeySource records that kates copied APIKey from Secret kates-api-key,
	// and which key it copied (see secretKeySource). kates ports and kates
	// deploy replace a key only while this still matches it. Every other key
	// counts as the user's and stays: one set with kates ctx set or brought in
	// with kates ctx import, one changed in the file by hand, and every key in
	// a config written before this field existed.
	KeySource string `yaml:"key-source,omitempty"`
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
	cfg, err := readConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not load config %s: %v\n", configPath(), err)
	}
	return cfg
}

// readConfig returns the config on disk, or the built-in default context when
// there is no file. It reports a file it cannot read or parse, which
// loadConfig only warns about but updateConfig refuses to write over: saving
// the defaults in its place would lose every context in it for good.
func readConfig() (Config, error) {
	cfg := Config{
		CurrentContext: "default",
		Contexts:       map[string]Context{"default": {URL: "http://localhost:8080", Output: "table"}},
	}
	data, err := os.ReadFile(configPath())
	switch {
	case errors.Is(err, fs.ErrNotExist):
		err = nil
	case err == nil:
		err = yaml.Unmarshal(data, &cfg)
	}
	if cfg.Contexts == nil {
		cfg.Contexts = map[string]Context{}
	}
	return cfg, err
}

// configFileMode keeps ~/.kates.yaml, which holds API keys, readable by its
// owner only. It used to be written 0644, so every local account could read
// the keys. 0600 keeps out other users, not other processes of the same one.
const configFileMode os.FileMode = 0o600

// saveConfig replaces the whole config with cfg. A command that changes part
// of the config calls updateConfig instead, which reads it under the same lock.
func saveConfig(cfg Config) error {
	unlock, err := lockConfig()
	if err != nil {
		return err
	}
	defer unlock()
	return writeConfigFile(cfg)
}

// updateConfig applies change to the config as it is on disk and saves the
// result, holding the config lock from the read to the write.
//
// Commands used to load the config, do their work and then save all of it, so
// a change another kates process saved in the meantime was put back. kates
// ports made that window long: it reads a Secret and checks keys against the
// API before it saves, and a key set with kates ctx set in another terminal
// during those seconds was quietly restored. change runs on a copy read just
// before the write; slow work belongs before the call.
func updateConfig(change func(cfg *Config) error) error {
	unlock, err := lockConfig()
	if err != nil {
		return err
	}
	defer unlock()
	cfg, err := readConfig()
	if err != nil {
		return fmt.Errorf("not saving over %s, which cannot be loaded: %w", configPath(), err)
	}
	if err := change(&cfg); err != nil {
		if errors.Is(err, errConfigUnchanged) {
			return nil
		}
		return err
	}
	return writeConfigFile(cfg)
}

// errConfigUnchanged, returned by an updateConfig change, ends the update
// without writing the file.
var errConfigUnchanged = errors.New("config unchanged")

// configLockWait bounds how long a save waits for another kates process to
// finish its own. Holders keep the lock for one read and one write.
var configLockWait = 5 * time.Second

// lockConfig takes an exclusive advisory lock on ~/.kates.yaml.lock and
// returns the function that releases it. The lock lives in a file of its own
// because writeConfigFile replaces the config file, and a lock on the old file
// would not bind the new one. The kernel releases it when the process exits,
// so a kates that crashed never leaves it held.
//
// Only another kates holding the lock past configLockWait is an error. A lock
// file that cannot be opened (one root created) or locked (a filesystem
// without flock) leaves the save unlocked, as every save was before.
func lockConfig() (unlock func(), err error) {
	path := configPath() + ".lock"
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, configFileMode)
	if err != nil {
		return func() {}, nil
	}
	fd := int(f.Fd())
	deadline := time.Now().Add(configLockWait)
	for {
		err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		switch {
		case err == nil:
			return func() {
				_ = syscall.Flock(fd, syscall.LOCK_UN)
				f.Close()
			}, nil
		case !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR):
			f.Close()
			return func() {}, nil
		case time.Now().After(deadline):
			f.Close()
			return nil, fmt.Errorf("another kates process has held %s for %s; try again once it finishes", path, configLockWait)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// writeConfigFile replaces the config file with cfg in one step.
//
// It writes a new file beside the config and renames it over the old one, so
// a reader sees the old contents or the new, never a half-written file, and a
// write that fails leaves the old file whole. kates used to rewrite the file
// in place, emptying it first: a failure after that left an empty config,
// which loads as no contexts, and the next save made the loss permanent. A
// symlinked config (a dotfiles checkout) is followed first, so the rename
// replaces the file it points to and the link stays a link.
func writeConfigFile(cfg Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	target, err := followSymlinks(configPath())
	if err != nil {
		return err
	}
	// The rename needs write access to the directory, not the file, so check
	// the file as the in-place write did: a config made read-only to keep
	// kates from changing it stays unchanged. Opening without O_TRUNC leaves
	// it intact.
	if f, err := os.OpenFile(target, os.O_WRONLY, 0); err == nil {
		f.Close()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	name := filepath.Base(target)
	if !strings.HasPrefix(name, ".") {
		name = "." + name
	}
	f, err := os.CreateTemp(filepath.Dir(target), name+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if tmp != "" {
			os.Remove(tmp)
		}
	}()
	// CreateTemp already uses 0600, less whatever the umask removes; set it
	// exactly, so an unusual umask cannot leave the owner without write access.
	if err := f.Chmod(configFileMode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		return err
	}
	tmp = ""
	return nil
}

// followSymlinks returns the file path names once every symlink on it is
// followed, including a link whose target does not exist yet, which the
// first save then creates. filepath.EvalSymlinks fails on such a link.
func followSymlinks(path string) (string, error) {
	for range 40 {
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			return path, nil
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return path, nil
		}
		link, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(link) {
			link = filepath.Join(filepath.Dir(path), link)
		}
		path = link
	}
	return "", fmt.Errorf("%s: too many levels of symbolic links", path)
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
		fmt.Printf("Use \"%s [command] --help\" for more information about a command.\n", cmd.CommandPath())
	})
}
