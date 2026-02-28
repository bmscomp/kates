package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
)

var pluginCmd = &cobra.Command{
	Use:   "plugin",
	Short: "Manage CLI plugins",
}

var pluginListCmd = &cobra.Command{
	Use:   "list",
	Short: "List discovered plugins",
	Run: func(cmd *cobra.Command, args []string) {
		plugins := discoverPlugins()
		if len(plugins) == 0 {
			output.Hint("No plugins found.")
			output.Hint("Place executables named kates-<name> in ~/.kates/plugins/ or on your PATH.")
			return
		}
		output.Header(fmt.Sprintf("Plugins (%d)", len(plugins)))
		rows := make([][]string, 0, len(plugins))
		for _, p := range plugins {
			rows = append(rows, []string{p.name, p.path})
		}
		output.Table([]string{"Name", "Path"}, rows)
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

func registerPlugins() {
	for _, p := range discoverPlugins() {
		pluginPath := p.path
		pluginName := p.name
		rootCmd.AddCommand(&cobra.Command{
			Use:                pluginName,
			Short:              fmt.Sprintf("Plugin: %s", pluginName),
			DisableFlagParsing: true,
			SilenceUsage:       true,
			SilenceErrors:      true,
			RunE: func(cmd *cobra.Command, args []string) error {
				c := exec.Command(pluginPath, args...)
				c.Stdin = os.Stdin
				c.Stdout = os.Stdout
				c.Stderr = os.Stderr
				return c.Run()
			},
		})
	}
}

func pluginDirPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kates", "plugins")
}

func init() {
	pluginCmd.AddCommand(pluginListCmd)
	rootCmd.AddCommand(pluginCmd)
	registerPlugins()
}
