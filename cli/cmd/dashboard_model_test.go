package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/theme"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// fakeDashAPI answers the dashboard's three calls. With hang set, each call
// blocks until its context ends.
type fakeDashAPI struct {
	hang  bool
	calls atomic.Int32
	runs  []client.TestRun
}

func (f *fakeDashAPI) wait(ctx context.Context) error {
	f.calls.Add(1)
	if f.hang {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (f *fakeDashAPI) Health(ctx context.Context) (*client.HealthResponse, error) {
	if err := f.wait(ctx); err != nil {
		return nil, err
	}
	return &client.HealthResponse{Status: "UP", Kafka: &client.KafkaInfo{Status: "UP", BootstrapServers: "kafka:9092"}}, nil
}

func (f *fakeDashAPI) ListTests(ctx context.Context, _, _ string, _, _ int) (*client.PagedTests, error) {
	if err := f.wait(ctx); err != nil {
		return nil, err
	}
	return &client.PagedTests{TotalItems: len(f.runs), Content: f.runs}, nil
}

func (f *fakeDashAPI) ClusterCheck(ctx context.Context) (*client.ClusterHealthReport, error) {
	if err := f.wait(ctx); err != nil {
		return nil, err
	}
	return &client.ClusterHealthReport{Status: "HEALTHY", Brokers: 3, Topics: 7}, nil
}

func runningRun(throughput float64) client.TestRun {
	return client.TestRun{ID: "abcdef123456", TestType: "LOAD", Status: "RUNNING",
		Results: []client.PhaseResult{{ThroughputRecordsPerSec: throughput, P99LatencyMs: 4, RecordsSent: 100}}}
}

func dashModelUpdate(t *testing.T, m dashModel, msg tea.Msg) (dashModel, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	return next.(dashModel), cmd
}

// The model refreshes on Init, schedules the next refresh at the interval
// when a refresh lands, and refreshes again on the tick.
func TestDashModelTicksAtTheInterval(t *testing.T) {
	api := &fakeDashAPI{runs: []client.TestRun{runningRun(1000)}}
	m := newDashModel(context.Background(), api, 3*time.Second)
	var asked []time.Duration
	m.tick = func(d time.Duration, f func(time.Time) tea.Msg) tea.Cmd {
		asked = append(asked, d)
		return func() tea.Msg { return f(time.Now()) }
	}
	m, _ = dashModelUpdate(t, m, tea.WindowSizeMsg{Width: 120, Height: 50})
	if !strings.Contains(m.View(), "Loading dashboard") {
		t.Errorf("before the first refresh: %q", m.View())
	}

	msg := m.Init()()
	if _, ok := msg.(dashSnapshotMsg); !ok || api.calls.Load() != 3 {
		t.Fatalf("Init: %T after %d calls, want a snapshot from 3 calls", msg, api.calls.Load())
	}
	m, cmd := dashModelUpdate(t, m, msg)
	if len(asked) != 1 || asked[0] != 3*time.Second {
		t.Fatalf("next refresh asked for %v, want one at 3s", asked)
	}
	if _, ok := cmd().(dashTickMsg); !ok {
		t.Fatal("the scheduled command is not the tick")
	}
	m, cmd = dashModelUpdate(t, m, dashTickMsg{})
	if cmd == nil {
		t.Fatal("the tick fetched nothing")
	}
	api.runs = []client.TestRun{runningRun(2000)}
	m, _ = dashModelUpdate(t, m, cmd())
	if api.calls.Load() != 6 || len(asked) != 2 {
		t.Errorf("after a tick: %d calls, %d schedules; want 6 and 2", api.calls.Load(), len(asked))
	}
	if len(m.hist.throughput) != 2 {
		t.Errorf("throughput history %v, want two points", m.hist.throughput)
	}

	view := stripAnsi(m.View())
	for _, want := range []string{"Kates Dashboard", "Cluster", "kafka:9092", "Tests", "Active Tests", "abcdef12",
		"Throughput", "2.0K rec/s", "HEALTHY", "Refreshing every 3s", "next look", "q quit"} {
		if !strings.Contains(view, want) {
			t.Errorf("want %q in:\n%s", want, view)
		}
	}
}

// Each call has its own timeout: a hung API shows as unreachable within the
// timeout instead of freezing the dashboard.
func TestDashModelTimesOutEachCall(t *testing.T) {
	api := &fakeDashAPI{hang: true}
	m := newDashModel(context.Background(), api, time.Second)
	m.timeout = 50 * time.Millisecond

	start := time.Now()
	msg := m.Init()()
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("a refresh against a hung API took %v", took)
	}
	s := dashSnapshot(msg.(dashSnapshotMsg))
	for name, err := range map[string]error{"health": s.healthErr, "list": s.listErr, "check": s.checkErr} {
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("%s err = %v, want the deadline", name, err)
		}
	}
	if api.calls.Load() != 3 {
		t.Errorf("%d calls, want 3: one call's timeout must not use up the others'", api.calls.Load())
	}
	m, _ = dashModelUpdate(t, m, tea.WindowSizeMsg{Width: 120, Height: 50})
	m, _ = dashModelUpdate(t, m, msg)
	view := stripAnsi(m.View())
	for _, want := range []string{"API unreachable", "Run list unavailable", "Cluster data unavailable"} {
		if !strings.Contains(view, want) {
			t.Errorf("want %q in:\n%s", want, view)
		}
	}
}

// The command's context ends a refresh at once.
func TestDashSnapshotStopsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := fetchDashSnapshot(ctx, &fakeDashAPI{hang: true}, time.Hour)
	if !errors.Is(s.healthErr, context.Canceled) {
		t.Errorf("health err = %v, want cancelled", s.healthErr)
	}
}

func TestDashModelQuits(t *testing.T) {
	for _, k := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("q")}, {Type: tea.KeyCtrlC}} {
		m, cmd := dashModelUpdate(t, newDashModel(context.Background(), &fakeDashAPI{}, time.Second), k)
		if !m.quitting || cmd == nil {
			t.Fatalf("%q: quitting %v", k.String(), m.quitting)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("%q: cmd() = %T, want tea.QuitMsg", k.String(), cmd())
		}
		if m.View() != "" {
			t.Errorf("%q: a quitting dashboard still draws", k.String())
		}
	}
	m, cmd := dashModelUpdate(t, newDashModel(context.Background(), &fakeDashAPI{}, time.Second), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if m.quitting || cmd != nil {
		t.Error("x quit the dashboard")
	}
}

func TestDashModelTooSmall(t *testing.T) {
	m := newDashModel(context.Background(), &fakeDashAPI{}, time.Second)
	m, _ = dashModelUpdate(t, m, tea.WindowSizeMsg{Width: 60, Height: 20})
	m, _ = dashModelUpdate(t, m, m.Init()())
	if view := stripAnsi(m.View()); !strings.Contains(view, "Terminal too small: 60x20") {
		t.Errorf("a 60x20 window drew:\n%s", view)
	}
}

func TestDashHistoryKeepsTheLast30(t *testing.T) {
	var h dashHistory
	for i := range 40 {
		h.observe(dashSnapshot{paged: &client.PagedTests{Content: []client.TestRun{runningRun(float64(i + 1))}}})
	}
	if len(h.throughput) != 30 || h.throughput[0] != 11 || len(h.latency) != 30 {
		t.Errorf("history %d/%d points, first %v", len(h.throughput), len(h.latency), h.throughput[0])
	}
	h.observe(dashSnapshot{listErr: errors.New("down")})
	if len(h.throughput) != 30 {
		t.Error("a failed refresh changed the history")
	}
}

// Without a terminal the dashboard prints a frame per refresh, as before,
// and stops with its context.
func TestRunDashboardFramesPrintsFrames(t *testing.T) {
	orig := output.Out
	var buf bytes.Buffer
	output.Out = &buf
	t.Cleanup(func() { output.Out = orig })

	ctx, cancel := context.WithCancel(context.Background())
	api := &fakeDashAPI{}
	done := make(chan error, 1)
	go func() { done <- runDashboardFrames(ctx, api, 20*time.Millisecond) }()
	for api.calls.Load() < 6 {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the frame loop did not stop with its context")
	}
	out := stripAnsi(buf.String())
	if n := strings.Count(out, "Kates Dashboard"); n < 2 {
		t.Errorf("%d frames printed, want at least 2:\n%s", n, out)
	}
	if !strings.Contains(out, "(Ctrl+C to stop)") {
		t.Errorf("the frame lacks its refresh line:\n%s", out)
	}
}

func TestDashboardFramesAreASCIIInPlainMode(t *testing.T) {
	t.Cleanup(func() { output.SetPlain(false) })
	output.SetPlain(true)
	s := fetchDashSnapshot(context.Background(), &fakeDashAPI{runs: []client.TestRun{
		runningRun(10), {ID: "done1", TestType: "LOAD", Status: "DONE"}}}, time.Second)
	h := dashHistory{throughput: []float64{1, 2, 3}, latency: []float64{3, 2, 1}}
	frame := stripAnsi(renderDashFrame(s, h, 120, 0) + dashFooter(0, time.Second, "Ctrl+C to stop") +
		dashSummaryContent(nil, errors.New("x"), 10))
	// The panel borders (output.Panel) and the spinner (spinnerFrame) are
	// drawn outside this file; every mark the dashboard draws itself is
	// checked.
	for _, mark := range []string{"✓", "✖", "⚠", "○", "◉", "▸", "↗", "↘", "—", "·", "▁", "█", "┃"} {
		if strings.Contains(frame, mark) {
			t.Errorf("%q in a plain frame:\n%s", mark, frame)
		}
	}
	for _, want := range []string{"Kates Dashboard  |", "(up)", "(down)", "* LOAD", "+ "} {
		if !strings.Contains(frame, want) {
			t.Errorf("want %q in the plain frame:\n%s", want, frame)
		}
	}
}

// The frame takes the screen it is given, in every look: no line wider than
// the terminal, no more lines than it has, the header at the top.
func TestDashboardFillsTheScreenInEveryLook(t *testing.T) {
	prev := theme.CurrentLook().Name
	t.Cleanup(func() { _ = theme.UseLook(prev) })
	api := &fakeDashAPI{runs: []client.TestRun{runningRun(2000), runningRun(3000),
		{ID: "done1", TestType: "LOAD", Status: "DONE"}, {ID: "fail1", TestType: "STRESS", Status: "FAILED"}}}
	s := fetchDashSnapshot(context.Background(), api, time.Second)
	for _, name := range theme.LookNames() {
		_ = theme.UseLook(name)
		for _, size := range [][2]int{{80, 24}, {110, 32}, {160, 45}} {
			m := newDashModel(context.Background(), api, time.Second)
			next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			next, _ = next.Update(dashSnapshotMsg(s))
			lines := strings.Split(next.View(), "\n")
			if len(lines) > size[1] {
				t.Errorf("%s %v: %d lines", name, size, len(lines))
			}
			// A boxed title bar has its text on its second line.
			if top := stripAnsi(strings.Join(lines[:3], "\n")); !strings.Contains(top, "Kates Dashboard") {
				t.Errorf("%s %v: the header is not at the top:\n%s", name, size, top)
			}
			for i, l := range lines {
				if w := lipgloss.Width(l); w > size[0] {
					t.Errorf("%s %v: line %d is %d wide", name, size, i, w)
				}
			}
		}
	}
}
