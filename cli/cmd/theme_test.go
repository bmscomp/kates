package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestThemeSetKeepsConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KATES_THEME", "")
	path := filepath.Join(home, ".kates.yaml")
	if err := os.WriteFile(path, []byte("current-context: lab\ncontexts:\n  lab:\n    url: http://lab:8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := themeSetCmd.RunE(themeSetCmd, []string{"Aurora"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := readConfig()
	if err != nil || cfg.Theme != "aurora" || cfg.Contexts["lab"].URL != "http://lab:8080" {
		t.Fatalf("after set: %+v, %v", cfg, err)
	}
	// Default is stored as no theme at all.
	if err := themeSetCmd.RunE(themeSetCmd, []string{"default"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "theme") {
		t.Errorf("default must clear the key:\n%s", data)
	}
	if err := themeSetCmd.RunE(themeSetCmd, []string{"nope"}); err == nil {
		t.Error("unknown theme must be refused")
	}
}
