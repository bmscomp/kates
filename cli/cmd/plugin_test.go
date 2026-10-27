package cmd

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
)

func TestDiscoverPlugins_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, ".kates", "plugins"), 0755); err != nil {
		t.Fatal(err)
	}

	plugins := discoverPlugins()
	for _, p := range plugins {
		if p.path == dir {
			t.Error("should not have discovered any plugins in empty dir")
		}
	}
}

func TestDiscoverPlugins_FindsKatesPrefix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	dir := t.TempDir()
	pluginPath := filepath.Join(dir, "kates-hello")
	if err := os.WriteFile(pluginPath, []byte("#!/bin/sh\necho hello"), 0755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", dir)
	pluginDir := filepath.Join(dir, ".kates", "plugins")
	os.MkdirAll(pluginDir, 0755)
	os.WriteFile(filepath.Join(pluginDir, "kates-hello"), []byte("#!/bin/sh\necho hello"), 0755)

	plugins := discoverPlugins()
	found := false
	for _, p := range plugins {
		if p.name == "hello" {
			found = true
		}
	}
	if !found {
		t.Error("expected to find kates-hello plugin")
	}
}

func TestDiscoverPlugins_IgnoresNonExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	dir := t.TempDir()
	pluginDir := filepath.Join(dir, ".kates", "plugins")
	os.MkdirAll(pluginDir, 0755)
	os.WriteFile(filepath.Join(pluginDir, "kates-noperm"), []byte("#!/bin/sh"), 0644)
	t.Setenv("HOME", dir)

	plugins := discoverPlugins()
	for _, p := range plugins {
		if p.name == "noperm" {
			t.Error("should not find non-executable kates- file")
		}
	}
}

// A kates-version on the PATH used to shadow kates version: registerPlugins
// ran from init, before the built-ins of files sorted after plugin.go, and
// cobra takes the first command of a name.
func TestRegisterPlugins_SkipsBuiltInNames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")
	pluginDir := filepath.Join(home, ".kates", "plugins")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"kates-version", "kates-ci", "kates-wave1-plugin"} {
		if err := os.WriteFile(filepath.Join(pluginDir, name), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	buf := output.ResetForTesting()
	defer func() { output.Out, output.Err = os.Stdout, os.Stderr }()

	before := len(rootCmd.Commands())
	registerPlugins()
	var added []*cobra.Command
	for _, c := range rootCmd.Commands() {
		if c.Annotations[pluginAnnotation] != "" {
			added = append(added, c)
		}
	}
	t.Cleanup(func() {
		for _, c := range added {
			rootCmd.RemoveCommand(c)
		}
	})

	if len(added) != 1 || added[0].Name() != "wave1-plugin" {
		names := make([]string, 0, len(added))
		for _, c := range added {
			names = append(names, c.Name())
		}
		t.Fatalf("plugins registered = %v, want wave1-plugin alone: version is a built-in and ci an alias of gate", names)
	}
	if got := len(rootCmd.Commands()); got != before+1 {
		t.Errorf("root has %d commands, want %d", got, before+1)
	}
	for _, name := range []string{"version", "ci"} {
		cmd, _, err := rootCmd.Find([]string{name})
		if err != nil || cmd.Annotations[pluginAnnotation] != "" {
			t.Errorf("kates %s resolves to %v (plugin %q), want the built-in", name, cmd, cmd.Annotations[pluginAnnotation])
		}
	}
	for _, want := range []string{"kates-version", "kates-ci", "built-in"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, buf.String())
		}
	}
}

// A plugin's exit code is kates' exit code: "exit status 7" and exit 1 told a
// script nothing it could act on.
func TestRunPlugin_ExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "kates-exit7")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 7\n"), 0755); err != nil {
		t.Fatal(err)
	}
	err := runPlugin(script, nil)
	var exit *exitError
	if !errors.As(err, &exit) || exit.code != 7 || exit.msg != "" {
		t.Fatalf("err = %v (%T), want an exitError with code 7 and no message", err, err)
	}

	ok := filepath.Join(dir, "kates-ok")
	if err := os.WriteFile(ok, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := runPlugin(ok, nil); err != nil {
		t.Errorf("a plugin that exits 0: err = %v", err)
	}
}

func TestPluginExecution_Integration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "kates-test-plugin")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"plugin-works\""), 0755); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(script).Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "plugin-works\n" {
		t.Errorf("expected 'plugin-works', got %q", string(out))
	}
}
