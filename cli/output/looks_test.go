package output

import (
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/pkg/theme"
	"github.com/charmbracelet/lipgloss"
)

func TestEveryLookShowsTheWholeTable(t *testing.T) {
	prev := theme.CurrentLook().Name
	t.Cleanup(func() { _ = theme.UseLook(prev) })
	for _, name := range theme.LookNames() {
		_ = theme.UseLook(name)
		buf := ResetForTesting()
		Header("Runs")
		Table([]string{"RUN", "STATUS"}, [][]string{{"first", "PASS"}, {"second", "FAIL", "extra cell"}})
		out := buf.String()
		for _, want := range []string{"Runs", "RUN", "STATUS", "first", "PASS", "second", "FAIL"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: %q missing:\n%s", name, want, out)
			}
		}
		if strings.Contains(out, "extra cell") {
			t.Errorf("%s: a cell past the headers is shown:\n%s", name, out)
		}
	}
}

// Plain output is for machines: it keeps the Classic shape whatever look
// was asked for.
func TestPlainDrawsClassic(t *testing.T) {
	prev, prevPlain, prevProfile := theme.CurrentLook().Name, plainMode, lipgloss.ColorProfile()
	t.Cleanup(func() {
		_ = theme.UseLook(prev)
		plainMode = prevPlain
		lipgloss.SetColorProfile(prevProfile)
	})
	_ = theme.UseLook(theme.Grid)
	SetPlain(true)
	if got := theme.CurrentLook().Name; got != theme.Classic {
		t.Errorf("--plain draws %s, want classic", got)
	}
}
