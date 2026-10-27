package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
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
			RunE: func(cmd *cobra.Command, args []string) error {
				return runPlugin(pluginPath, args)
			},
		})
	}
}

// runPlugin runs the plugin at path on the terminal's own streams. A plugin
// that exits non-zero ends kates with that code and no message of its own:
// the plugin has said what went wrong, and "exit status 7" with exit code 1
// told a script nothing.
func runPlugin(path string, args []string) error {
	c := exec.Command(path, args...)
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
