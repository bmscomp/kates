package detect

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// Every Deployment in the cluster is one big JSON document; the Strimzi,
// network and admission probes share one fetch of it per Collect.
func TestCollectFetchesDeploymentsOnce(t *testing.T) {
	m := setupHealthyMock()
	if _, err := NewCollector(m).Collect(context.Background()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	n := 0
	for _, call := range m.Calls() {
		if call == "kubectl get deployment -A -o json" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("kubectl get deployment -A ran %d times, want 1", n)
	}
}

// blockingExecutor answers kubectl cluster-info and blocks every other call
// until its context ends, the way a hung API server would.
type blockingExecutor struct {
	mu      sync.Mutex
	started int
}

func (b *blockingExecutor) Exec(name string, args ...string) (string, error) {
	return b.ExecContext(context.Background(), name, args...)
}

func (b *blockingExecutor) ExecContext(ctx context.Context, name string, args ...string) (string, error) {
	b.mu.Lock()
	b.started++
	b.mu.Unlock()
	<-ctx.Done()
	return "", ctx.Err()
}

func (b *blockingExecutor) LookPath(file string) (string, error) { return "/bin/" + file, nil }

// Cancelling Collect's context (Ctrl-C) stops the calls in flight, and
// Collect says it was interrupted instead of returning a hollow report.
func TestCollectStopsOnCancel(t *testing.T) {
	b := &blockingExecutor{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := NewCollector(b).Collect(ctx)
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Collect = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Collect still running 5s after its context was cancelled")
	}
}

// Each call is bounded by the collector's per-call timeout.
func TestCollectorCallTimeout(t *testing.T) {
	c := NewCollector(&blockingExecutor{})
	c.CallTimeout = 20 * time.Millisecond
	start := time.Now()
	_, err := c.run(context.Background(), "kubectl", "get", "nodes")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("run = %v, want a deadline error", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("run took %v under a 20ms timeout", time.Since(start))
	}
}

// An executor without ExecContext (the test double, a wrapper) still
// returns when the context ends.
func TestExecContextWithPlainExecutor(t *testing.T) {
	m := NewMockExecutor()
	m.Set("ok", "kubectl", "version")
	out, err := execContext(context.Background(), m, time.Second, "kubectl", "version")
	if err != nil || out != "ok" {
		t.Errorf("execContext = (%q, %v), want (ok, nil)", out, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := execContext(ctx, m, time.Second, "kubectl", "version"); !errors.Is(err, context.Canceled) {
		t.Errorf("execContext on a cancelled context = %v, want context.Canceled", err)
	}
}

// The OS executor kills the process when the context ends.
func TestOSExecutorExecContextKills(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := NewOSExecutor().ExecContext(ctx, "sleep", "10")
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "sleep 10") {
		t.Errorf("ExecContext = %v, want the command and a deadline error", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("sleep ran %v past its deadline", time.Since(start))
	}
}
