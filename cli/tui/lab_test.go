package tui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/client"
	tea "github.com/charmbracelet/bubbletea"
)

func keyMsg(k string) tea.KeyMsg {
	if k == "enter" {
		return tea.KeyMsg{Type: tea.KeyEnter}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

func updateLab(t *testing.T, m LabModel, msg tea.Msg) (LabModel, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	lab, ok := next.(LabModel)
	if !ok {
		t.Fatalf("Update returned %T, want LabModel", next)
	}
	return lab, cmd
}

// newLabServerClient returns a client whose requests all succeed against a
// stub server, so commands that hit the API can be executed safely in tests.
func newLabServerClient(t *testing.T, body string) *client.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return client.New(srv.URL)
}

func newRunningLab(t *testing.T, c *client.Client) LabModel {
	t.Helper()
	m := NewLab(c, "http://test")
	m.running = true
	m.runTestID = "t1"
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelCtx = ctx
	m.cancelFn = cancel
	t.Cleanup(cancel)
	return m
}

// assertBareRunTest executes a continuation command and verifies it is a
// single runTest, not a Batch: the tick chain from the initial run is still
// alive, so a continuation that batches another tick makes the elapsed
// counter run 2x/3x fast (the PR #69 bug).
func assertBareRunTest(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("cmd = nil, want runTest continuation")
	}
	got := cmd()
	if _, ok := got.(tea.BatchMsg); ok {
		t.Fatal("continuation batched extra commands; it must not start a second tick chain")
	}
	if _, ok := got.(labTestStartedMsg); !ok {
		t.Fatalf("cmd() = %T, want labTestStartedMsg from runTest", got)
	}
}

func TestLabStartKeys(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		setup func(*LabModel)
		check func(*testing.T, LabModel)
	}{
		{
			name: "enter starts a run",
			key:  "enter",
			check: func(t *testing.T, m LabModel) {
				if m.elapsed != 0 {
					t.Errorf("elapsed = %d, want 0", m.elapsed)
				}
			},
		},
		{
			name:  "enter with warmup configured arms warmupRemaining",
			key:   "enter",
			setup: func(m *LabModel) { m.warmupCount = 2 },
			check: func(t *testing.T, m LabModel) {
				if m.warmupRemaining != 2 {
					t.Errorf("warmupRemaining = %d, want 2", m.warmupRemaining)
				}
			},
		},
		{
			name:  "s starts a sweep over the cursor param",
			key:   "s",
			setup: func(m *LabModel) { m.cursor = 1; m.params[1].Current = 3 },
			check: func(t *testing.T, m LabModel) {
				if !m.sweepActive {
					t.Error("sweepActive = false, want true")
				}
				if m.sweepParam != 1 {
					t.Errorf("sweepParam = %d, want 1", m.sweepParam)
				}
				if m.params[1].Current != 0 {
					t.Errorf("params[1].Current = %d, want 0 (sweep starts at first value)", m.params[1].Current)
				}
			},
		},
		{
			name: "m starts median mode",
			key:  "m",
			check: func(t *testing.T, m LabModel) {
				if !m.medianActive {
					t.Error("medianActive = false, want true")
				}
				if m.medianRemaining != 3 {
					t.Errorf("medianRemaining = %d, want 3", m.medianRemaining)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewLab(client.New("http://127.0.0.1:1"), "")
			if tt.setup != nil {
				tt.setup(&m)
			}
			m, cmd := updateLab(t, m, keyMsg(tt.key))
			if !m.running {
				t.Error("running = false, want true")
			}
			if m.cancelFn == nil {
				t.Error("cancelFn = nil, want set")
			}
			if cmd == nil {
				t.Fatal("cmd = nil, want batch of runTest + tick")
			}
			// tea.Batch wraps its children without executing them, so this
			// never touches the network.
			batch, ok := cmd().(tea.BatchMsg)
			if !ok {
				t.Fatalf("cmd() = %T, want tea.BatchMsg", cmd())
			}
			if len(batch) != 2 {
				t.Errorf("batch len = %d, want 2 (runTest + exactly one tick)", len(batch))
			}
			if tt.check != nil {
				tt.check(t, m)
			}
		})
	}
}

func TestLabKeysIgnoredWhileRunning(t *testing.T) {
	for _, key := range []string{"enter", "s", "m", "r", "q", "p", "e", "w"} {
		t.Run(key, func(t *testing.T) {
			m := newRunningLab(t, nil)
			m, cmd := updateLab(t, m, keyMsg(key))
			if cmd != nil {
				t.Errorf("cmd = non-nil for %q during a run, want nil", key)
			}
			if !m.running {
				t.Error("running = false, want true (only x/ctrl+c act during a run)")
			}
			if m.quitting {
				t.Error("quitting = true, want false")
			}
		})
	}
}

func TestLabCancelKey(t *testing.T) {
	t.Run("x during run resets all run state", func(t *testing.T) {
		m := newRunningLab(t, newLabServerClient(t, `{}`))
		m.sweepActive = true
		m.medianActive = true
		m.medianResults = []labIteration{{Throughput: 1}}
		m.warmupRemaining = 2

		m, cmd := updateLab(t, m, keyMsg("x"))
		if cmd != nil {
			t.Error("cmd = non-nil, want nil after cancel")
		}
		if m.running {
			t.Error("running = true, want false")
		}
		if m.runTestID != "" {
			t.Errorf("runTestID = %q, want empty", m.runTestID)
		}
		if m.sweepActive {
			t.Error("sweepActive = true, want false")
		}
		if m.medianActive {
			t.Error("medianActive = true, want false")
		}
		if m.medianResults != nil {
			t.Errorf("medianResults = %v, want nil", m.medianResults)
		}
		if m.warmupRemaining != 0 {
			t.Errorf("warmupRemaining = %d, want 0", m.warmupRemaining)
		}
		if m.cancelCtx.Err() == nil {
			t.Error("cancelCtx not cancelled")
		}
	})

	t.Run("x without a cancel context is a no-op", func(t *testing.T) {
		m := NewLab(nil, "")
		m.running = true
		m.runTestID = "t1"
		m, cmd := updateLab(t, m, keyMsg("x"))
		if cmd != nil {
			t.Error("cmd = non-nil, want nil")
		}
		if !m.running {
			t.Error("running = false, want true (nothing to cancel)")
		}
	})

	t.Run("ctrl+c during run quits", func(t *testing.T) {
		m := newRunningLab(t, nil)
		m, cmd := updateLab(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
		if !m.quitting {
			t.Error("quitting = false, want true")
		}
		if cmd == nil {
			t.Fatal("cmd = nil, want tea.Quit")
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("cmd() = %T, want tea.QuitMsg", cmd())
		}
	})
}

func TestLabRetryKey(t *testing.T) {
	req := &client.CreateTestRequest{TestType: "LOAD"}
	tests := []struct {
		name        string
		lastError   error
		lastTestReq *client.CreateTestRequest
		wantRun     bool
	}{
		{"retries when error and request are set", errors.New("boom"), req, true},
		{"no retry without an error", nil, req, false},
		{"no retry without a request", errors.New("boom"), nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewLab(nil, "")
			m.lastError = tt.lastError
			m.lastTestReq = tt.lastTestReq

			m, cmd := updateLab(t, m, keyMsg("r"))
			if m.running != tt.wantRun {
				t.Errorf("running = %v, want %v", m.running, tt.wantRun)
			}
			if !tt.wantRun {
				if cmd != nil {
					t.Error("cmd = non-nil, want nil")
				}
				return
			}
			if m.lastError != nil {
				t.Errorf("lastError = %v, want nil after retry starts", m.lastError)
			}
			if cmd == nil {
				t.Fatal("cmd = nil, want batch of retryTest + tick")
			}
			batch, ok := cmd().(tea.BatchMsg)
			if !ok {
				t.Fatalf("cmd() = %T, want tea.BatchMsg", cmd())
			}
			if len(batch) != 2 {
				t.Errorf("batch len = %d, want 2 (retryTest + exactly one tick)", len(batch))
			}
		})
	}
}

func TestLabTestStartedMsg(t *testing.T) {
	req := &client.CreateTestRequest{TestType: "LOAD"}
	msg := labTestStartedMsg{run: &client.TestRun{ID: "run-42"}, req: req, records: 50000}

	t.Run("records run ID and starts polling", func(t *testing.T) {
		m := newRunningLab(t, nil)
		m.runTestID = ""
		m, cmd := updateLab(t, m, msg)
		if m.runTestID != "run-42" {
			t.Errorf("runTestID = %q, want %q", m.runTestID, "run-42")
		}
		if m.lastTestReq != req {
			t.Errorf("lastTestReq = %v, want the request from the message", m.lastTestReq)
		}
		if cmd == nil {
			t.Error("cmd = nil, want pollTestOnce")
		}
	})

	t.Run("dropped when the run was cancelled during creation", func(t *testing.T) {
		m := NewLab(nil, "")
		m, cmd := updateLab(t, m, msg)
		if m.runTestID != "" {
			t.Errorf("runTestID = %q, want empty", m.runTestID)
		}
		if m.lastTestReq != nil {
			t.Error("lastTestReq set from a dropped message")
		}
		if cmd != nil {
			t.Error("cmd = non-nil, want nil")
		}
	})
}

func TestLabTestDoneMsg(t *testing.T) {
	run := &client.TestRun{
		ID: "t1",
		Results: []client.PhaseResult{
			{ThroughputRecordsPerSec: 100, P99LatencyMs: 10, AvgLatencyMs: 4},
			{ThroughputRecordsPerSec: 200, P99LatencyMs: 20, AvgLatencyMs: 6},
		},
	}

	t.Run("success appends an iteration with averaged metrics", func(t *testing.T) {
		m := newRunningLab(t, nil)
		m.iterations = []labIteration{{Number: 1, Throughput: 100}}
		msg := labTestDoneMsg{testID: "t1", run: run, summary: &client.ReportSummary{ErrorRate: 0.005}}

		m, cmd := updateLab(t, m, msg)
		if cmd != nil {
			t.Error("cmd = non-nil, want nil")
		}
		if m.running {
			t.Error("running = true, want false")
		}
		if m.runTestID != "" {
			t.Errorf("runTestID = %q, want empty", m.runTestID)
		}
		if len(m.iterations) != 2 {
			t.Fatalf("iterations len = %d, want 2", len(m.iterations))
		}
		iter := m.iterations[1]
		if iter.Number != 2 {
			t.Errorf("Number = %d, want 2", iter.Number)
		}
		if iter.Throughput != 150 {
			t.Errorf("Throughput = %v, want 150 (mean of phases)", iter.Throughput)
		}
		if iter.P99Ms != 15 {
			t.Errorf("P99Ms = %v, want 15 (mean of phases)", iter.P99Ms)
		}
		if iter.AvgMs != 6 {
			t.Errorf("AvgMs = %v, want 6 (last phase)", iter.AvgMs)
		}
		if iter.ErrorRate != 0.5 {
			t.Errorf("ErrorRate = %v, want 0.5 (summary rate as percent)", iter.ErrorRate)
		}
		if stripAnsi(iter.Delta) != "▲50%" {
			t.Errorf("Delta = %q, want ▲50%% vs previous iteration", stripAnsi(iter.Delta))
		}
	})

	t.Run("stale result from a cancelled run is dropped", func(t *testing.T) {
		m := newRunningLab(t, nil)
		m.runTestID = "t2"
		m, cmd := updateLab(t, m, labTestDoneMsg{testID: "t1", run: run})
		if cmd != nil {
			t.Error("cmd = non-nil, want nil")
		}
		if len(m.iterations) != 0 {
			t.Errorf("iterations len = %d, want 0 (stale result kept)", len(m.iterations))
		}
		if !m.running {
			t.Error("running = false, want true (current run still in flight)")
		}
	})

	t.Run("create failure after cancel is dropped", func(t *testing.T) {
		m := NewLab(nil, "")
		m, cmd := updateLab(t, m, labTestDoneMsg{testID: "", err: errors.New("boom")})
		if cmd != nil {
			t.Error("cmd = non-nil, want nil")
		}
		if m.lastError != nil {
			t.Errorf("lastError = %v, want nil for a post-cancel failure", m.lastError)
		}
	})

	t.Run("error stores retry state and stops all modes", func(t *testing.T) {
		req := &client.CreateTestRequest{TestType: "LOAD"}
		m := newRunningLab(t, nil)
		m.sweepActive = true
		m.medianActive = true
		m.warmupRemaining = 1

		m, cmd := updateLab(t, m, labTestDoneMsg{testID: "t1", req: req, err: errors.New("boom")})
		if cmd != nil {
			t.Error("cmd = non-nil, want nil")
		}
		if m.running {
			t.Error("running = true, want false")
		}
		if m.lastError == nil {
			t.Error("lastError = nil, want the failure stored for retry")
		}
		if m.lastTestReq != req {
			t.Error("lastTestReq not stored from the failed request")
		}
		if m.sweepActive || m.medianActive || m.warmupRemaining != 0 {
			t.Errorf("modes not reset: sweep=%v median=%v warmup=%d",
				m.sweepActive, m.medianActive, m.warmupRemaining)
		}
	})
}

func TestLabProgressMsg(t *testing.T) {
	tests := []struct {
		name       string
		running    bool
		runTestID  string
		msg        labProgressMsg
		wantCmd    bool
		wantRecs   int64
		wantThrput float64
	}{
		{
			name:    "current test updates live stats and re-polls",
			running: true, runTestID: "t1",
			msg:     labProgressMsg{testID: "t1", hasStats: true, records: 500, throughput: 42.5, latency: 3},
			wantCmd: true, wantRecs: 500, wantThrput: 42.5,
		},
		{
			name:    "current test without stats still re-polls",
			running: true, runTestID: "t1",
			msg:     labProgressMsg{testID: "t1"},
			wantCmd: true,
		},
		{
			name:    "stale test ID is dropped and does not re-poll",
			running: true, runTestID: "t2",
			msg: labProgressMsg{testID: "t1", hasStats: true, records: 500, throughput: 42.5},
		},
		{
			name: "not running drops the poll",
			msg:  labProgressMsg{testID: "t1", hasStats: true, records: 500},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewLab(nil, "")
			m.running = tt.running
			m.runTestID = tt.runTestID
			m.cancelCtx = context.Background()

			m, cmd := updateLab(t, m, tt.msg)
			if (cmd != nil) != tt.wantCmd {
				t.Errorf("cmd non-nil = %v, want %v", cmd != nil, tt.wantCmd)
			}
			if m.liveRecords != tt.wantRecs {
				t.Errorf("liveRecords = %d, want %d", m.liveRecords, tt.wantRecs)
			}
			if m.liveThroughput != tt.wantThrput {
				t.Errorf("liveThroughput = %v, want %v", m.liveThroughput, tt.wantThrput)
			}
		})
	}
}

func TestLabTickMsg(t *testing.T) {
	t.Run("running advances elapsed and continues the chain", func(t *testing.T) {
		m := NewLab(nil, "")
		m.running = true
		m.elapsed = 3
		m, cmd := updateLab(t, m, labTickMsg{})
		if m.elapsed != 4 {
			t.Errorf("elapsed = %d, want 4", m.elapsed)
		}
		if cmd == nil {
			t.Error("cmd = nil, want next tick")
		}
	})

	t.Run("not running lets the chain die", func(t *testing.T) {
		m := NewLab(nil, "")
		m.elapsed = 3
		m, cmd := updateLab(t, m, labTickMsg{})
		if m.elapsed != 3 {
			t.Errorf("elapsed = %d, want 3 (unchanged)", m.elapsed)
		}
		if cmd != nil {
			t.Error("cmd = non-nil, want nil so the tick chain stops")
		}
	})
}

func TestLabWarmupContinuation(t *testing.T) {
	done := labTestDoneMsg{testID: "t1", run: &client.TestRun{ID: "t1"}}

	t.Run("mid-warmup discards the result and reruns", func(t *testing.T) {
		m := newRunningLab(t, newLabServerClient(t, `{"id":"next-run"}`))
		m.warmupCount = 2
		m.warmupRemaining = 2

		m, cmd := updateLab(t, m, done)
		if m.warmupRemaining != 1 {
			t.Errorf("warmupRemaining = %d, want 1", m.warmupRemaining)
		}
		if !m.running {
			t.Error("running = false, want true")
		}
		if len(m.iterations) != 0 {
			t.Errorf("iterations len = %d, want 0 (warmup results are discarded)", len(m.iterations))
		}
		assertBareRunTest(t, cmd)
	})

	t.Run("last warmup starts the measured run", func(t *testing.T) {
		m := newRunningLab(t, newLabServerClient(t, `{"id":"next-run"}`))
		m.warmupCount = 2
		m.warmupRemaining = 1

		m, cmd := updateLab(t, m, done)
		if m.warmupRemaining != 0 {
			t.Errorf("warmupRemaining = %d, want 0", m.warmupRemaining)
		}
		if !m.running {
			t.Error("running = false, want true")
		}
		if len(m.iterations) != 0 {
			t.Errorf("iterations len = %d, want 0", len(m.iterations))
		}
		assertBareRunTest(t, cmd)
	})
}

func TestLabMedianContinuation(t *testing.T) {
	t.Run("intermediate run collects the result and reruns", func(t *testing.T) {
		m := newRunningLab(t, newLabServerClient(t, `{"id":"next-run"}`))
		m.medianActive = true
		m.medianRemaining = 3

		msg := labTestDoneMsg{testID: "t1", run: &client.TestRun{
			ID:      "t1",
			Results: []client.PhaseResult{{ThroughputRecordsPerSec: 100}},
		}}
		m, cmd := updateLab(t, m, msg)
		if m.medianRemaining != 2 {
			t.Errorf("medianRemaining = %d, want 2", m.medianRemaining)
		}
		if len(m.medianResults) != 1 {
			t.Errorf("medianResults len = %d, want 1", len(m.medianResults))
		}
		if len(m.iterations) != 0 {
			t.Errorf("iterations len = %d, want 0 until all runs finish", len(m.iterations))
		}
		if !m.running {
			t.Error("running = false, want true")
		}
		assertBareRunTest(t, cmd)
	})

	t.Run("final run appends the median iteration", func(t *testing.T) {
		m := newRunningLab(t, nil)
		m.medianActive = true
		m.medianRemaining = 1
		m.medianResults = []labIteration{{Throughput: 100}, {Throughput: 300}}

		msg := labTestDoneMsg{testID: "t1", run: &client.TestRun{
			ID:      "t1",
			Results: []client.PhaseResult{{ThroughputRecordsPerSec: 200}},
		}}
		m, cmd := updateLab(t, m, msg)
		if cmd != nil {
			t.Error("cmd = non-nil, want nil after median mode finishes")
		}
		if m.running {
			t.Error("running = true, want false")
		}
		if m.medianActive {
			t.Error("medianActive = true, want false")
		}
		if m.medianResults != nil {
			t.Errorf("medianResults = %v, want nil", m.medianResults)
		}
		if len(m.iterations) != 1 {
			t.Fatalf("iterations len = %d, want 1", len(m.iterations))
		}
		if m.iterations[0].Throughput != 200 {
			t.Errorf("Throughput = %v, want 200 (median of 100/200/300)", m.iterations[0].Throughput)
		}
	})
}

func TestLabSweepContinuation(t *testing.T) {
	done := labTestDoneMsg{testID: "t1", run: &client.TestRun{ID: "t1"}}

	t.Run("advances to the next value and reruns", func(t *testing.T) {
		m := newRunningLab(t, newLabServerClient(t, `{"id":"next-run"}`))
		m.sweepActive = true
		m.sweepParam = 1
		m.params[1].Current = 0

		m, cmd := updateLab(t, m, done)
		if m.params[1].Current != 1 {
			t.Errorf("params[1].Current = %d, want 1", m.params[1].Current)
		}
		if !m.running {
			t.Error("running = false, want true")
		}
		if len(m.iterations) != 1 {
			t.Errorf("iterations len = %d, want 1", len(m.iterations))
		}
		assertBareRunTest(t, cmd)
	})

	t.Run("last value finishes the sweep", func(t *testing.T) {
		m := newRunningLab(t, nil)
		m.sweepActive = true
		m.sweepParam = 1
		m.params[1].Current = len(m.params[1].Values) - 1

		m, cmd := updateLab(t, m, done)
		if cmd != nil {
			t.Error("cmd = non-nil, want nil after the sweep completes")
		}
		if m.sweepActive {
			t.Error("sweepActive = true, want false")
		}
		if m.running {
			t.Error("running = true, want false")
		}
		if len(m.iterations) != 1 {
			t.Errorf("iterations len = %d, want 1", len(m.iterations))
		}
	})
}

func TestExportCSVColumnAlignment(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	m := NewLab(nil, "")
	params := m.currentParams()
	params["acks"] = "1"
	m.iterations = []labIteration{{
		Number:     1,
		Throughput: 1234.5,
		P99Ms:      9.87,
		AvgMs:      4.56,
		ErrorRate:  0.12,
		TestID:     "abc123",
		Delta:      "\033[32m▲5%\033[0m",
		Params:     params,
	}}

	path := m.exportCSV()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading exported CSV: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("CSV lines = %d, want header + 1 row", len(lines))
	}
	header := strings.Split(lines[0], ",")
	row := strings.Split(lines[1], ",")
	if len(header) != len(row) {
		t.Fatalf("header has %d fields, row has %d", len(header), len(row))
	}
	if want := 7 + len(m.params); len(header) != want {
		t.Errorf("header fields = %d, want %d", len(header), want)
	}

	col := func(name string) string {
		t.Helper()
		for i, h := range header {
			if h == name {
				return row[i]
			}
		}
		t.Fatalf("column %q not found in header %v", name, header)
		return ""
	}

	// AvgMs and ErrorRate values are distinct so a column swap (the PR #68
	// bug) fails these checks.
	if got := col("avg_latency_ms"); got != "4.56" {
		t.Errorf("avg_latency_ms = %q, want %q", got, "4.56")
	}
	if got := col("error_rate"); got != "0.12" {
		t.Errorf("error_rate = %q, want %q", got, "0.12")
	}
	if got := col("throughput_rec_s"); got != "1234.50" {
		t.Errorf("throughput_rec_s = %q, want %q", got, "1234.50")
	}
	if got := col("p99_ms"); got != "9.87" {
		t.Errorf("p99_ms = %q, want %q", got, "9.87")
	}
	if got := col("delta"); got != "▲5%" {
		t.Errorf("delta = %q, want ANSI stripped %q", got, "▲5%")
	}
	if got := col("test_id"); got != "abc123" {
		t.Errorf("test_id = %q, want %q", got, "abc123")
	}
	if got := col("acks"); got != "1" {
		t.Errorf("acks param column = %q, want %q", got, "1")
	}
}

func TestSaveLoadSessionRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	a := NewLab(nil, "")
	a.params[1].Current = 4
	a.params[4].Current = 0
	a.iterations = []labIteration{
		{Number: 1, Throughput: 100, P99Ms: 5, AvgMs: 2.5, ErrorRate: 0.1, TestID: "t1", Params: map[string]string{"acks": "all"}},
		{Number: 2, Throughput: 200, P99Ms: 6, AvgMs: 3.5, ErrorRate: 0.2, TestID: "t2", Params: map[string]string{"acks": "1"}},
	}
	savedPath := a.saveSession()

	b := NewLab(nil, "")
	loadedPath, ok := b.loadSession()
	if !ok {
		t.Fatal("loadSession ok = false, want true")
	}
	if loadedPath != savedPath {
		t.Errorf("loaded path = %q, want %q", loadedPath, savedPath)
	}
	if len(b.iterations) != 2 {
		t.Fatalf("iterations len = %d, want 2", len(b.iterations))
	}
	for i, want := range a.iterations {
		got := b.iterations[i]
		if got.Number != want.Number || got.Throughput != want.Throughput ||
			got.P99Ms != want.P99Ms || got.AvgMs != want.AvgMs ||
			got.ErrorRate != want.ErrorRate || got.TestID != want.TestID {
			t.Errorf("iteration %d = %+v, want fields of %+v", i, got, want)
		}
		if got.Params["acks"] != want.Params["acks"] {
			t.Errorf("iteration %d acks = %q, want %q", i, got.Params["acks"], want.Params["acks"])
		}
	}
	if stripAnsi(b.iterations[1].Delta) != "▲100%" {
		t.Errorf("Delta = %q, want recomputed ▲100%%", stripAnsi(b.iterations[1].Delta))
	}
	if b.params[1].Current != 4 {
		t.Errorf("params[1].Current = %d, want 4", b.params[1].Current)
	}
	if b.params[4].Current != 0 {
		t.Errorf("params[4].Current = %d, want 0", b.params[4].Current)
	}
}

func TestLoadSessionBoundsChecking(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	session := `{
		"iterations": [{"number":1,"throughput":50,"p99Ms":1,"avgMs":0.5,"errorRate":0,"testId":"t","params":{"acks":"1"}}],
		"params": [
			{"key":"producers","current":99},
			{"key":"acks","current":-1},
			{"key":"compression","current":1}
		]
	}`
	if err := os.WriteFile(filepath.Join(home, ".kates-lab-session.json"), []byte(session), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewLab(nil, "")
	if _, ok := m.loadSession(); !ok {
		t.Fatal("loadSession ok = false, want true")
	}
	// Out-of-range indices keep the defaults instead of panicking.
	if m.paramVal("producers") != "4" {
		t.Errorf("producers = %q, want default %q for out-of-range index", m.paramVal("producers"), "4")
	}
	if m.paramVal("acks") != "all" {
		t.Errorf("acks = %q, want default %q for negative index", m.paramVal("acks"), "all")
	}
	if m.paramVal("compression") != "gzip" {
		t.Errorf("compression = %q, want %q from the valid index", m.paramVal("compression"), "gzip")
	}
	if len(m.iterations) != 1 || m.iterations[0].AvgMs != 0.5 {
		t.Errorf("iterations = %+v, want 1 entry with AvgMs 0.5", m.iterations)
	}

	t.Run("missing file", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		m := NewLab(nil, "")
		if _, ok := m.loadSession(); ok {
			t.Error("loadSession ok = true, want false for a missing file")
		}
	})

	t.Run("corrupt JSON", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		if err := os.WriteFile(filepath.Join(home, ".kates-lab-session.json"), []byte("{"), 0644); err != nil {
			t.Fatal(err)
		}
		m := NewLab(nil, "")
		if _, ok := m.loadSession(); ok {
			t.Error("loadSession ok = true, want false for corrupt JSON")
		}
	})
}

func TestComputeMedianIteration(t *testing.T) {
	tests := []struct {
		name        string
		throughputs []float64
		want        float64
	}{
		{"already sorted", []float64{100, 200, 300}, 200},
		{"reverse sorted", []float64{300, 200, 100}, 200},
		{"median arrives last", []float64{300, 100, 200}, 200},
		{"tied low values", []float64{100, 100, 300}, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewLab(nil, "")
			m.iterations = []labIteration{{Number: 1, Throughput: 50}}
			for _, thr := range tt.throughputs {
				m.medianResults = append(m.medianResults, labIteration{Throughput: thr})
			}
			got := m.computeMedianIteration()
			if got.Throughput != tt.want {
				t.Errorf("Throughput = %v, want %v", got.Throughput, tt.want)
			}
			if got.Number != 2 {
				t.Errorf("Number = %d, want 2 (next iteration number)", got.Number)
			}
			if got.Delta == "" {
				t.Error("Delta empty, want recomputed against the previous iteration")
			}
		})
	}
}

func TestSparkline(t *testing.T) {
	tests := []struct {
		name   string
		values []float64
		want   string
	}{
		{"empty", nil, ""},
		{"single value", []float64{5}, "▁"},
		{"all equal", []float64{2, 2, 2}, "▁▁▁"},
		{"min and max", []float64{0, 7}, "▁█"},
		{"low mid high", []float64{0, 3.5, 7}, "▁▄█"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sparkline(tt.values); got != tt.want {
				t.Errorf("sparkline(%v) = %q, want %q", tt.values, got, tt.want)
			}
		})
	}
}

func TestFmtLabNum(t *testing.T) {
	tests := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1.0K"},
		{1536, "1.5K"},
		{999_999, "1000.0K"},
		{1_000_000, "1.0M"},
		{2_345_678, "2.3M"},
	}
	for _, tt := range tests {
		if got := fmtLabNum(tt.in); got != tt.want {
			t.Errorf("fmtLabNum(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestLabParseInt(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"0", 0},
		{"123", 123},
		{"262144", 262144},
		{"all", 0},
		{"12a3", 123},
	}
	for _, tt := range tests {
		if got := labParseInt(tt.in); got != tt.want {
			t.Errorf("labParseInt(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestStripAnsi(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain", "plain", "plain"},
		{"color wrapped", "\033[32mok\033[0m", "ok"},
		{"multi-param escape", "\033[1;31mred\033[0m text", "red text"},
		{"escape only", "\033[0m", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripAnsi(tt.in); got != tt.want {
				t.Errorf("stripAnsi(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
