package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/client"
	tea "github.com/charmbracelet/bubbletea"
)

// asciiScreens renders every screen of the dashboard and the lab.
func asciiScreens(t *testing.T) map[string]string {
	t.Helper()
	screens := map[string]string{}
	m := loadedDashboard(t)
	for _, k := range []string{"1", "2", "3", "4"} {
		m = update(t, m, runes(k))
		screens["tab "+k] = m.View()
	}
	m = update(t, m, runes("2"))
	m = update(t, m, runes("/"))
	m = update(t, m, runes("ord"))
	screens["filter"] = m.View()
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEsc})

	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = update(t, m, m.topics.loadDetail("orders")())
	screens["topic detail"] = m.View()
	m = update(t, m, m.topics.fetchRecords("orders")())
	m.topics.view = topicsConsume
	screens["consume"] = m.View()

	m = update(t, m, runes("3"))
	m = update(t, m, m.groups.loadGroupDetail("billing")())
	screens["group detail"] = m.View()

	m = update(t, m, runes("4"))
	m = update(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = update(t, m, produceSentMsg{topic: "orders", partition: "1", offset: "2"})
	screens["produce input"] = m.View()
	m = update(t, m, produceSentMsg{err: errors.New("refused")})
	screens["produce error"] = m.View()

	m.brokers.check = &client.ClusterHealthReport{KraftQuorum: &client.KraftQuorumHealth{LeaderID: -1, Issue: "NO_LEADER"}}
	m = update(t, m, runes("1"))
	screens["brokers, no leader"] = m.View()

	lab := labWithHistory(t)
	screens["lab"] = lab.View()
	lab.view = labHistory
	screens["lab diff"] = lab.View()
	lab.view = labPinSelect
	screens["lab pins"] = lab.View()
	lab.view = labConfig
	lab.sweepActive = true
	lab.running = true
	lab.liveRecords = 1000
	lab.status = runningStatus(3)
	screens["lab running"] = lab.View()

	screens["too small"] = TooSmallCard(60, 20)
	return screens
}

// labWithHistory is a lab at 120x40 with three iterations and a delta of
// each sign.
func labWithHistory(t *testing.T) LabModel {
	t.Helper()
	lab := NewLab(nil, "http://kates.example")
	next, _ := lab.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	lab = next.(LabModel)
	for _, thr := range []float64{1000, 1500, 1200} {
		run := &client.TestRun{ID: "run", Results: []client.PhaseResult{{ThroughputRecordsPerSec: thr, P99LatencyMs: thr / 100, AvgLatencyMs: 3}}}
		iter := lab.buildIteration(run, &client.ReportSummary{ErrorRate: 0.001})
		lab.iterations = append(lab.iterations, iter)
		lab.params[1].Current++
	}
	return lab
}

// --plain, KATES_ASCII and LC_ALL=C make the TUIs ASCII: every mark comes
// from the glyph set.
func TestScreensAreASCIIInPlainMode(t *testing.T) {
	asciiMode(t)
	for name, s := range asciiScreens(t) {
		if r := nonASCII(plain(s)); r != "" {
			t.Errorf("%s: %q in plain mode:\n%s", name, r, plain(s))
		}
	}
}

// The same screens keep their UTF-8 marks by default.
func TestScreensUseUTF8GlyphsByDefault(t *testing.T) {
	screens := asciiScreens(t)
	mustContain(t, plain(screens["tab 2"]), "▸", "✓", "⚠", "╭")
	mustContain(t, plain(screens["tab 3"]), "●", "○", "✖")
	mustContain(t, plain(screens["lab"]), "▲", "▼", "Δ")
}

func TestGlyphsFollowPlainMode(t *testing.T) {
	if g := glyphs(); g.Star != "★" || g.Check != "✓" || withIcon(g.Warmup, "x") != "🔥 x" {
		t.Errorf("default glyphs: %+v", g)
	}
	if !strings.Contains(rule(3), "─") || boxBorder().TopLeft != "╭" {
		t.Error("default rule or border is not the UTF-8 one")
	}
	asciiMode(t)
	g := glyphs()
	if g.Star != "*" || g.Check != "+" || g.Arrow != ">" || withIcon(g.Warmup, "x") != "x" {
		t.Errorf("ASCII glyphs: %+v", g)
	}
	if rule(3) != "---" || boxBorder().TopLeft != "+" {
		t.Errorf("ASCII rule %q, border corner %q", rule(3), boxBorder().TopLeft)
	}
	if deltaHeader() != "d" || plain(deltaLabel(5, true)) != "+5%" || plain(deltaLabel(-5, true)) != "-5%" {
		t.Error("ASCII deltas are not ASCII")
	}
	if got := sparkline([]float64{1, 2, 3}); nonASCII(got) != "" {
		t.Errorf("ASCII sparkline %q", got)
	}
}
