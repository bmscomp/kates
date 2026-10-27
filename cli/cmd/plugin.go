package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var pluginCmd = &cobra.Command{
	Use:   "plugin",
	Short: "Manage CLI plugins",
}

var pluginListCmd = &cobra.Command{
	Use:   "list",
	Short: "List discovered plugins",
	RunE: func(cmd *cobra.Command, args []string) error {
		if outputMode == "json" {
			return noJSONForm(cmd)
		}
		plugins := discoverPlugins()
		if len(plugins) == 0 {
			output.Hint("No plugins found.")
			output.Hint("Place executables named kates-<name> in ~/.kates/plugins/ or on your PATH.")
			return nil
		}
		output.Header(fmt.Sprintf("Plugins (%d)", len(plugins)))
		rows := make([][]string, 0, len(plugins))
		for _, p := range plugins {
			rows = append(rows, []string{p.name, p.path})
		}
		output.Table([]string{"Name", "Path"}, rows)
		return nil
	},
}

type pluginInfo struct {
	name string
	path string
}

func discoverPlugins() []pluginInfo {
	seen := map[string]bool{}
	var plugins []pluginInfo

	pluginDir := pluginDirPath()
	if entries, err := os.ReadDir(pluginDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasPrefix(name, "kates-") {
				continue
			}
			p := filepath.Join(pluginDir, name)
			info, err := os.Stat(p)
			if err != nil || info.Mode()&0111 == 0 {
				continue
			}
			cmdName := strings.TrimPrefix(name, "kates-")
			if !seen[cmdName] {
				seen[cmdName] = true
				plugins = append(plugins, pluginInfo{name: cmdName, path: p})
			}
		}
	}

	pathDirs := filepath.SplitList(os.Getenv("PATH"))
	for _, dir := range pathDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasPrefix(name, "kates-") {
				continue
			}
			cmdName := strings.TrimPrefix(name, "kates-")
			if seen[cmdName] {
				continue
			}
			p := filepath.Join(dir, name)
			info, err := os.Stat(p)
			if err != nil || info.Mode()&0111 == 0 {
				continue
			}
			seen[cmdName] = true
			plugins = append(plugins, pluginInfo{name: cmdName, path: p})
		}
	}

	return plugins
}

// pluginAnnotation marks the commands registerPlugins adds, which run an
// executable named after a file on the PATH rather than Kates code.
const pluginAnnotation = "kates.plugin"

// registerPlugins adds a command for each discovered plugin. Execute calls it
// once every built-in is registered: from init it ran before the built-ins of
// files sorted after plugin.go, and cobra takes the first command of a name,
// so a kates-version on the PATH shadowed kates version. A plugin named like
// a built-in, or one of its aliases, is skipped with a note on stderr.
func registerPlugins() {
	// help and completion are cobra's own, added only as Execute starts.
	taken := map[string]bool{"help": true, "completion": true}
	for _, c := range rootCmd.Commands() {
		taken[c.Name()] = true
		for _, a := range c.Aliases {
			taken[a] = true
		}
	}
	for _, p := range discoverPlugins() {
		pluginPath := p.path
		pluginName := p.name
		if taken[pluginName] {
			fmt.Fprintf(output.Err, "Warning: ignoring plugin %s: kates %s is a built-in command\n", pluginPath, pluginName)
			continue
		}
		taken[pluginName] = true
		rootCmd.AddCommand(&cobra.Command{
			Use:                pluginName,
			Short:              fmt.Sprintf("Plugin: %s", pluginName),
			Annotations:        map[string]string{pluginAnnotation: pluginPath},
			DisableFlagParsing: true,
			SilenceUsage:       true,
			SilenceErrors:      true,
			PersistentPreRunE:  pluginPreRun,
			RunE: func(cmd *cobra.Command, args []string) error {
				// A context kates cannot resolve has no URL or key to pass:
				// the plugin would run against whatever its defaults are.
				if contextErr != nil {
					return contextErr
				}
				return runPlugin(pluginPath, pluginArgs(cmd.Name(), args))
			},
		})
	}
}

// pluginCommandLine is the command line kates was started with, less the
// program name.
var pluginCommandLine = func() []string { return os.Args[1:] }

// pluginPreRun reads the global flags given before the plugin's name, then
// runs the root's pre-run, so a plugin starts with the context, URL, API key
// and output mode kates would use itself. A plugin command parses no flags,
// so without this `kates --context prod mything` resolved the current
// context and handed the plugin "--context prod" instead. Cobra runs only
// the nearest PersistentPreRun, so this one calls the root's.
func pluginPreRun(cmd *cobra.Command, args []string) error {
	if globals, _, ok := splitPluginArgs(pluginCommandLine(), cmd.Name()); ok && len(globals) > 0 {
		if err := cmd.Root().PersistentFlags().Parse(globals); err != nil {
			return couldNotTell(err.Error())
		}
	}
	if rootPreRun := cmd.Root().PersistentPreRunE; rootPreRun != nil {
		return rootPreRun(cmd, args)
	}
	return nil
}

// pluginArgs is what the plugin gets as its arguments: everything after its
// name on the command line. args, cobra's, also holds the global flags given
// before the name, which kates has read.
func pluginArgs(name string, args []string) []string {
	if _, rest, ok := splitPluginArgs(pluginCommandLine(), name); ok {
		return rest
	}
	return args
}

// splitPluginArgs splits a command line at the plugin's name: the kates
// global flags before it, and the plugin's own arguments after it. ok is
// false when anything but a global flag comes before the name, as when the
// command line is not the one cobra ran (a test calling RunE).
func splitPluginArgs(line []string, name string) (globals, rest []string, ok bool) {
	flags := rootCmd.PersistentFlags()
	for i := 0; i < len(line); i++ {
		a := line[i]
		if a == name {
			return line[:i], line[i+1:], true
		}
		if len(a) < 2 || a[0] != '-' || a == "--" {
			return nil, nil, false
		}
		var f *pflag.Flag
		inline := false
		if long, isLong := strings.CutPrefix(a, "--"); isLong {
			n, _, hasValue := strings.Cut(long, "=")
			f, inline = flags.Lookup(n), hasValue
		} else {
			f, inline = flags.ShorthandLookup(a[1:2]), len(a) > 2
		}
		if f == nil {
			return nil, nil, false
		}
		if !inline && f.NoOptDefVal == "" {
			i++ // the flag's value
		}
	}
	return nil, nil, false
}

// pluginEnv is the environment a plugin runs with: kates' own, with what the
// pre-run resolved in place of any KATES_* value it inherited. Those are what
// kates itself would use, flags over variables over the context, so a plugin
// that reads them talks to the same server as the command line around it.
//
//	KATES_URL      the API URL, after --url, KATES_URL and the context
//	KATES_API_KEY  the API key; unset when there is none, as when --url
//	               points away from the context's server
//	KATES_CONTEXT  the context's name
//	KATES_OUTPUT   table or json
//	KATES_PLAIN    1 under --plain or KATES_PLAIN, else unset; NO_COLOR=1
//	               comes with it unless NO_COLOR is already set
func pluginEnv(base []string) []string {
	set := map[string]string{
		"KATES_URL":     apiURL,
		"KATES_CONTEXT": activeContext,
		"KATES_OUTPUT":  outputMode,
	}
	if apiKeyFlag != "" {
		set["KATES_API_KEY"] = apiKeyFlag
	}
	if plainOutput {
		set["KATES_PLAIN"] = "1"
		if os.Getenv("NO_COLOR") == "" {
			set["NO_COLOR"] = "1"
		}
	}
	replaced := []string{"KATES_URL", "KATES_CONTEXT", "KATES_OUTPUT", "KATES_API_KEY", "KATES_PLAIN"}
	env := make([]string, 0, len(base)+len(set))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if slices.ContainsFunc(replaced, func(r string) bool { return sameEnvKey(k, r) }) {
			continue
		}
		if _, ok := set[k]; ok {
			continue
		}
		env = append(env, kv)
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+set[k])
	}
	return env
}

// sameEnvKey compares variable names as the OS does: Windows ignores case.
func sameEnvKey(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// runPlugin runs the plugin at path on the terminal's own streams, with the
// environment pluginEnv gives it. A plugin that exits non-zero ends kates
// with that code and no message of its own: the plugin has said what went
// wrong, and "exit status 7" with exit code 1 told a script nothing.
func runPlugin(path string, args []string) error {
	c := exec.Command(path, args...)
	c.Env = pluginEnv(os.Environ())
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	err := c.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() > 0 {
		return &exitError{code: exit.ExitCode()}
	}
	return err
}

func pluginDirPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kates", "plugins")
}

func init() {
	pluginCmd.AddCommand(pluginListCmd)
	rootCmd.AddCommand(pluginCmd)
}
