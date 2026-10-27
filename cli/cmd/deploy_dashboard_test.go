package cmd

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func dashUpdate(t *testing.T, m deployDashboardModel, msg tea.Msg) deployDashboardModel {
	t.Helper()
	next, _ := m.Update(msg)
	dm, ok := next.(deployDashboardModel)
	if !ok {
		t.Fatalf("Update returned %T, want deployDashboardModel", next)
	}
	return dm
}

// Root cause 1 of the ragged border: emoji + VS16 (U+FE0F) sequences. The
// padding stack (viewport/lipgloss via go-runewidth) counts them as ONE cell
// while terminals render TWO, so every such line pushed the pane's right
// border out by a column. No VS16 may survive into the log ring.
func TestDashboardLog_StripsVariationSelectors(t *testing.T) {
	m := NewDeployDashboard(context.Background(), 13)
	m = dashUpdate(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = dashUpdate(t, m, logMsg{text: "⏭️  Kyverno already deployed. Skipping."})
	m = dashUpdate(t, m, logMsg{text: "⚠️ warning with selector"})

	for i, l := range m.logs {
		if strings.ContainsRune(l, '️') {
			t.Errorf("logs[%d] still contains VS16: %q", i, l)
		}
	}
}

// Root cause 2: over-long lines. Nothing truncated them, so one long helm
// message stretched the whole bordered box past the terminal edge and the
// border wrapped into fragments. Every line in the pane content must fit.
func TestDashboardLog_TruncatesToPaneWidth(t *testing.T) {
	m := NewDeployDashboard(context.Background(), 13)
	m = dashUpdate(t, m, tea.WindowSizeMsg{Width: 100, Height: 40})

	long := "📦 Deploying Strimzi Operator (Namespace: strimzi-operator) with a very long trailing explanation that exceeds any reasonable pane width by a comfortable margin"
	m = dashUpdate(t, m, logMsg{text: long})
	m = dashUpdate(t, m, logMsg{text: "\x1b[31mstyled and also much much much much much much much much much much too long to fit in the pane\x1b[0m"})

	w := m.logsViewport.Width
	if w <= 0 {
		t.Fatal("viewport width not set by WindowSizeMsg")
	}
	for i, l := range strings.Split(m.buildLogContent(), "\n") {
		if got := ansi.StringWidth(l); got > w {
			t.Errorf("content line %d width %d exceeds pane width %d: %q", i, got, w, l)
		}
	}
}

// Resizing must re-truncate: lines stored under a wide pane may not fit a
// narrower one.
func TestDashboardLog_RetruncatesOnResize(t *testing.T) {
	m := NewDeployDashboard(context.Background(), 13)
	m = dashUpdate(t, m, tea.WindowSizeMsg{Width: 160, Height: 40})
	m = dashUpdate(t, m, logMsg{text: strings.Repeat("x", 150)})

	m = dashUpdate(t, m, tea.WindowSizeMsg{Width: 100, Height: 40})
	w := m.logsViewport.Width
	for i, l := range strings.Split(m.buildLogContent(), "\n") {
		if got := ansi.StringWidth(l); got > w {
			t.Errorf("after shrink, line %d width %d exceeds %d", i, got, w)
		}
	}
}

// The header box and the panes row must end at the same column. The header
// rendered at m.width while the panes row filled m.width-4, so the dashboard's
// right edge was a staircase: header sticking out past the panes below it.
func TestDashboardRowsShareOneWidth(t *testing.T) {
	for _, w := range []int{80, 100, 120, 160} {
		m := NewDeployDashboard(context.Background(), 13)
		m = dashUpdate(t, m, tea.WindowSizeMsg{Width: w, Height: 40})
		m = dashUpdate(t, m, logMsg{text: "a log line"})

		lines := strings.Split(m.View(), "\n")
		header := ansi.StringWidth(lines[0])
		panes := -1
		for _, line := range lines {
			if strings.Contains(line, "╭") {
				panes = ansi.StringWidth(line)
				break
			}
		}
		if panes < 0 {
			t.Fatalf("width %d: no panes row:\n%s", w, strings.Join(lines, "\n"))
		}
		if header != panes {
			t.Errorf("width %d: header renders %d cols, panes row %d — right edges misaligned", w, header, panes)
		}
	}
}

// The summary's table and footer must agree. They previously interpreted the
// (never-recorded) empty status independently: the table rendered "Skipped"
// while the footer counted "deployed" — eight Skipped rows above
// "8 components deployed successfully!".
func TestClassifyStatus_UnrecordedIsUnknownNotBoth(t *testing.T) {
	if got := classifyStatus(""); got != "unknown" {
		t.Errorf("classifyStatus(\"\") = %q — the empty status must be visibly unknown, not silently success or skip", got)
	}
	for _, s := range []string{"deployed", "skipped", "failed"} {
		if got := classifyStatus(s); got != s {
			t.Errorf("classifyStatus(%q) = %q", s, got)
		}
	}
}

func TestMarkEntryStatuses(t *testing.T) {
	entries := []DeploySummaryEntry{
		{Release: "strimzi-operator", Namespace: "strimzi-operator"},
		{Release: "krafter", Namespace: "kafka"},
		{Release: "kates", Namespace: "kates"},
	}

	// Everything already present: components are skipped — except Strimzi,
	// which is reconciled unconditionally and therefore always does work.
	markEntryStatuses(entries, func(string, string) bool { return true })
	if entries[0].Status != "deployed" {
		t.Errorf("strimzi = %q, want deployed (reconciled every run)", entries[0].Status)
	}
	for _, e := range entries[1:] {
		if e.Status != "skipped" {
			t.Errorf("%s = %q, want skipped when already present", e.Release, e.Status)
		}
	}

	// Nothing present: everything deploys.
	markEntryStatuses(entries, func(string, string) bool { return false })
	for _, e := range entries {
		if e.Status != "deployed" {
			t.Errorf("%s = %q, want deployed on fresh cluster", e.Release, e.Status)
		}
	}
}

// The pane printed "◯ Pending..." for a component that had not started,
// without its name. Its content lived only in View's copy of the viewport, so
// the keys could not scroll it, and it followed the deploy only while the logs
// had focus: below the fold, components were never seen.
func TestDashboardComponentsPane(t *testing.T) {
	m := NewDeployDashboard(context.Background(), 8)
	for _, id := range []string{"strimzi", "cert-manager", "kyverno", "kafka", "kafka-users", "kates", "kafka-ui", "chaos"} {
		m.RegisterComponent(id, "Component "+id, "A", Target{"ns-" + id, ""})
	}
	m = dashUpdate(t, m, tea.WindowSizeMsg{Width: 120, Height: 12})
	if content, _ := m.componentsContent(); !strings.Contains(content, "Component kafka-ui") {
		t.Errorf("a pending component is not named:\n%s", content)
	}

	m = dashUpdate(t, m, compStatusMsg{id: "chaos", active: true, timeout: time.Minute})
	followed := m.componentsViewport.YOffset
	if followed == 0 {
		t.Fatal("the pane does not follow the component being deployed")
	}

	m = dashUpdate(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if m.componentsViewport.YOffset != followed-1 {
		t.Fatalf("up scrolled the pane to %d, want %d", m.componentsViewport.YOffset, followed-1)
	}
	m = dashUpdate(t, m, compStatusMsg{id: "chaos", done: true, success: true})
	if m.componentsViewport.YOffset != followed-1 {
		t.Errorf("the pane moved under the user's scroll, to %d", m.componentsViewport.YOffset)
	}
}

// A tick polled every component's workloads, then each workload's pods again,
// and ticked again whatever the poll's progress: on a slow API server the
// kubectl calls stacked up. A healthy workload showed its first pod's phase,
// "Running", in amber.
func TestDashboardPollsOneAtATime(t *testing.T) {
	m := NewDeployDashboard(context.Background(), 2)
	var reads []string
	m.read = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		reads = append(reads, strings.Join(args, " "))
		return []byte(`{"items":[{"kind":"Deployment","metadata":{"name":"kates"},"spec":{"replicas":2},"status":{"readyReplicas":2}}]}`), nil
	}
	m.RegisterComponent("kafka", "Kafka Cluster", "B", Target{"kafka", "strimzi.io/cluster=krafter"})
	m.RegisterComponent("kates", "Kates Backend", "C", Target{"kates", "app.kubernetes.io/instance=kates"})
	m = dashUpdate(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	m = dashUpdate(t, m, compStatusMsg{id: "kates", active: true, timeout: time.Minute})

	_, cmd := m.Update(tickMsg(time.Now()))
	msg, ok := cmd().(allWorkloadsMsg)
	if !ok {
		t.Fatal("a tick should run the poll and nothing else")
	}
	if len(reads) != 1 || !strings.Contains(reads[0], "-n kates") {
		t.Errorf("want one read, of the started component's workloads; got %q", reads)
	}

	next, cmd := m.Update(msg)
	if cmd == nil {
		t.Error("the poll's answer should schedule the next tick")
	}
	content, _ := next.(deployDashboardModel).componentsContent()
	if !strings.Contains(content, "2/2") || !strings.Contains(content, "Ready") || strings.Contains(content, "Waiting") {
		t.Errorf("a ready workload should read Ready:\n%s", content)
	}
}

// The poll ran on a Bubble Tea goroutine over the same *componentStat
// Update was writing: a race under -race, and a torn read in the wild. The
// poll now works from copies, so Update may change a component mid-poll.
func TestDashboardPollDoesNotShareComponentState(t *testing.T) {
	m := NewDeployDashboard(context.Background(), 2)
	started := make(chan struct{})
	release := make(chan struct{})
	m.read = func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		return []byte(`{"items":[]}`), nil
	}
	m.RegisterComponent("kates", "Kates Backend", "C", Target{"kates", "app.kubernetes.io/instance=kates"})
	m = dashUpdate(t, m, compStatusMsg{id: "kates", active: true, timeout: time.Minute})

	_, cmd := m.Update(tickMsg(time.Now()))
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	<-started
	// Update writes the component the poll was scheduled from.
	m = dashUpdate(t, m, compStatusMsg{id: "kates", done: true, success: true})
	m.components[0].Targets = append(m.components[0].Targets, Target{"other", "x=y"})
	close(release)
	if _, ok := (<-done).(allWorkloadsMsg); !ok {
		t.Fatal("the poll did not answer")
	}
	if !m.components[0].Done {
		t.Error("the update was lost")
	}
}

// The deploy dashboard is a full-screen TUI like the others: below 80x24 it
// shows the shared too-small card instead of clipping its panes.
func TestDashboardTooSmallCard(t *testing.T) {
	m := NewDeployDashboard(context.Background(), 2)
	m = dashUpdate(t, m, tea.WindowSizeMsg{Width: 60, Height: 20})
	if view := m.View(); !strings.Contains(view, "Terminal too small") {
		t.Errorf("a 60x20 window renders the dashboard, not the card:\n%s", view)
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if !next.(deployDashboardModel).interrupted || cmd == nil {
		t.Error("q on the too-small card should close the window, as the card says")
	}
	m = dashUpdate(t, m, tea.WindowSizeMsg{Width: 120, Height: 40})
	if view := m.View(); strings.Contains(view, "Terminal too small") {
		t.Error("a 120x40 window still shows the card")
	}
}

// Ctrl-C closed the window and nothing else: the deploy went on with nowhere
// to print, and the terminal looked hung until it ended.
func TestDashboardCtrlCClosesWindowOnly(t *testing.T) {
	m := NewDeployDashboard(context.Background(), 2)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if dm := next.(deployDashboardModel); !dm.interrupted || dm.ctx.Err() == nil || cmd == nil {
		t.Errorf("Ctrl-C should mark the window interrupted, stop the poller and quit")
	}

	c := &DashboardController{p: &tea.Program{}}
	c.closed.Store(true)
	if c.live() {
		t.Error("a closed window should send the deploy's lines to the terminal")
	}
}
