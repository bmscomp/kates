package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
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
