package theme

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// The default palette is the Kates look; these are its values from before
// themes could be chosen, and they must not drift.
func TestDefaultPaletteUnchanged(t *testing.T) {
	want := map[string][2]string{
		"Accent": {"#2563EB", "#60A5FA"}, "Text": {"#1E293B", "#E2E8F0"}, "Muted": {"#6B7280", "#9CA3AF"},
		"Subtle": {"#D1D5DB", "#4B5563"}, "Surface": {"#F3F4F6", "#1F2937"}, "Success": {"#2563EB", "#60A5FA"},
		"Warning": {"#C2410C", "#FB923C"}, "Error": {"#DC2626", "#F87171"}, "Info": {"#0891B2", "#22D3EE"},
		"Primary": {"#1D4ED8", "#93C5FD"}, "Secondary": {"#6D28D9", "#C4B5FD"}, "Highlight": {"#059669", "#34D399"},
		"OnColor": {"#000000", "#000000"}, "OnDark": {"#FFFFFF", "#F9FAFB"},
	}
	v := reflect.ValueOf(Palettes[DefaultName])
	for role, lw := range want {
		c := v.FieldByName(role).Interface().(lipgloss.AdaptiveColor)
		if c.Light != lw[0] || c.Dark != lw[1] {
			t.Errorf("default %s = %s/%s, want %s/%s", role, c.Light, c.Dark, lw[0], lw[1])
		}
	}
}

func TestEveryPaletteIsComplete(t *testing.T) {
	for name, p := range Palettes {
		v := reflect.ValueOf(p)
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			switch c := v.Field(i).Interface().(type) {
			case lipgloss.AdaptiveColor:
				if c.Light == "" || c.Dark == "" {
					t.Errorf("%s.%s has an empty shade", name, f.Name)
				}
			case string:
				if c == "" {
					t.Errorf("%s.%s is empty", name, f.Name)
				}
			}
		}
	}
	if Names()[0] != DefaultName || len(Names()) != len(Palettes) {
		t.Errorf("Names() = %v", Names())
	}
}

func TestSelected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KATES_THEME", "")
	if got := Selected(); got != DefaultName {
		t.Errorf("no config: %q", got)
	}
	if err := os.WriteFile(filepath.Join(home, ".kates.yaml"), []byte("current-context: x\ntheme: Aurora\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Selected(); got != "aurora" {
		t.Errorf("config theme: %q", got)
	}
	t.Setenv("KATES_THEME", "dracula")
	if got := Selected(); got != "dracula" {
		t.Errorf("KATES_THEME must win: %q", got)
	}
	t.Setenv("KATES_THEME", "nope")
	if got := Selected(); got != DefaultName || Requested() != "nope" {
		t.Errorf("unknown theme: selected %q, requested %q", got, Requested())
	}
}
