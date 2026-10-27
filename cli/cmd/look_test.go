package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmscomp/kates/cli/pkg/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestLookSetKeepsConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KATES_LOOK", "")
	path := filepath.Join(home, ".kates.yaml")
	if err := os.WriteFile(path, []byte("current-context: lab\ncontexts:\n  lab:\n    url: http://lab:8080\ntheme: aurora\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := lookSetCmd.RunE(lookSetCmd, []string{"Panels"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := readConfig()
	if err != nil || cfg.Look != "panels" || cfg.Theme != "aurora" || cfg.Contexts["lab"].URL != "http://lab:8080" {
		t.Fatalf("after set: %+v, %v", cfg, err)
	}
	// Classic is stored as no look at all.
	if err := lookSetCmd.RunE(lookSetCmd, []string{"classic"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "look") {
		t.Errorf("classic must clear the key:\n%s", data)
	}
	if err := lookSetCmd.RunE(lookSetCmd, []string{"neon"}); err == nil {
		t.Error("unknown look must be refused")
	}
}

func TestDashboardFitsInEveryLook(t *testing.T) {
	prev := theme.CurrentLook().Name
	t.Cleanup(func() { _ = theme.UseLook(prev) })
	for _, name := range theme.LookNames() {
		_ = theme.UseLook(name)
		for _, w := range []int{80, 120} {
			m := NewDeployDashboard(context.Background(), 13)
			m = dashUpdate(t, m, tea.WindowSizeMsg{Width: w, Height: 30})
			m = dashUpdate(t, m, logMsg{text: "a log line"})
			for i, line := range strings.Split(m.View(), "\n") {
				if lw := lipgloss.Width(line); lw > w {
					t.Errorf("%s at %d: line %d is %d wide", name, w, i, lw)
				}
			}
		}
	}
	m := NewDeployDashboard(context.Background(), 13)
	_ = theme.UseLook(theme.Classic)
	if dashUpdate(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("T")}); theme.CurrentLook().Name != theme.Panels {
		t.Errorf("T on the dashboard: look %s, want panels", theme.CurrentLook().Name)
	}
}

func TestDashboardTSwitchesTheLook(t *testing.T) {
	prev := theme.CurrentLook().Name
	t.Cleanup(func() { _ = theme.UseLook(prev) })
	_ = theme.UseLook(theme.Classic)
	m := newDashModel(context.Background(), nil, time.Second)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("T")})
	if _, ok := next.(dashModel); !ok || theme.CurrentLook().Name != theme.Panels {
		t.Errorf("T on kates dashboard: look %s, want panels", theme.CurrentLook().Name)
	}
}
