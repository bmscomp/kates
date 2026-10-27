package cmd

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// Two components with no dependency between them run side by side; one that
// depends on them starts only once both are done; one that only needs
// another's install submitted (after) starts before that one is ready.
func TestRunComponents_ParallelAndOrdered(t *testing.T) {
	origDL := dl
	t.Cleanup(func() { dl = origDL })
	dl = &DashboardController{}
	dc := &deployContext{ctx: context.Background(), advanceStep: func() {}}

	var mu sync.Mutex
	var events []string
	record := func(e string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
	}
	// a's install does not return until b has started: if they ran one
	// after the other, a would never finish.
	bStarted := make(chan struct{})
	cReleased := make(chan struct{})
	comps := []component{
		{id: "a", name: "A", install: func(ctx context.Context) error {
			record("a: install")
			select {
			case <-bStarted:
			case <-time.After(5 * time.Second):
				return errors.New("b never started while a was installing: no parallelism")
			}
			return nil
		}, wait: func(context.Context) error { record("a: ready"); return nil }},
		{id: "b", name: "B", install: func(context.Context) error {
			close(bStarted)
			record("b: install")
			return nil
		}, wait: func(context.Context) error {
			// b is slow to become ready; d may start, c may not.
			<-cReleased
			record("b: ready")
			return nil
		}},
		{id: "c", name: "C", deps: []string{"a", "b"}, install: func(context.Context) error { record("c: install"); return nil }},
		{id: "d", name: "D", after: []string{"b"}, install: func(context.Context) error {
			record("d: install")
			close(cReleased)
			return nil
		}},
		{id: "e", name: "E", enabled: func() bool { return false }, install: func(context.Context) error {
			t.Error("a disabled component ran")
			return nil
		}},
		{id: "f", name: "F", deps: []string{"e"}, install: func(context.Context) error { record("f: install"); return nil }},
	}
	if err := runComponents(context.Background(), dc, comps); err != nil {
		t.Fatalf("runComponents: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	index := func(e string) int {
		for i, got := range events {
			if got == e {
				return i
			}
		}
		t.Fatalf("%q never happened: %v", e, events)
		return -1
	}
	if index("c: install") < index("a: ready") || index("c: install") < index("b: ready") {
		t.Errorf("c started before both its dependencies were ready: %v", events)
	}
	if index("d: install") > index("b: ready") {
		t.Errorf("d waited for b to be ready; it only needs b installed: %v", events)
	}
	index("f: install") // a dependency the flags left out is ignored
}

// A failed component stops the deploy: its error comes back, named, and a
// component waiting on it never starts.
func TestRunComponents_FailureStopsDependents(t *testing.T) {
	origDL := dl
	t.Cleanup(func() { dl = origDL })
	dl = &DashboardController{}
	dc := &deployContext{ctx: context.Background(), advanceStep: func() {}}

	ran := false
	err := runComponents(context.Background(), dc, []component{
		{id: "a", name: "Strimzi Operator", install: func(context.Context) error { return errors.New("helm: timed out") }},
		{id: "b", name: "Kafka Cluster", deps: []string{"a"}, install: func(context.Context) error { ran = true; return nil }},
	})
	if err == nil || !strings.Contains(err.Error(), "Strimzi Operator: helm: timed out") {
		t.Errorf("err = %v, want the failed component named", err)
	}
	if ran {
		t.Error("a component ran after its dependency failed")
	}
}

// The deploy pipeline's dependencies say the install order the stack needs:
// operators before any custom resource, the monitoring CRDs before anything
// that renders against them, Kafka (and its users) before Connect,
// MirrorMaker 2 and the UI, Kates after Kafka and PostgreSQL.
func TestDeployComponents_Dependencies(t *testing.T) {
	dc := &deployContext{ctx: context.Background(), ns: resolveNamespaces()}
	byID := map[string]component{}
	for _, c := range deployComponents(dc) {
		key := c.id
		if key == "" {
			key = c.name
		}
		byID[key] = c
	}
	// reaches says whether from depends, transitively, on to (deps or after).
	var reaches func(from, to string) bool
	reaches = func(from, to string) bool {
		c, ok := byID[from]
		if !ok {
			return false
		}
		for _, d := range append(append([]string(nil), c.deps...), c.after...) {
			if d == to || reaches(d, to) {
				return true
			}
		}
		return false
	}
	for _, tc := range [][2]string{
		{"kafka", "strimzi"}, {"kafka", "cert-manager"}, {"kafka", "kyverno"},
		{"kafka", "monitoring"}, {"kafka-connect", "monitoring"}, {"chaos", "monitoring"}, {"mirror-maker2", "monitoring"}, {"kates", "monitoring"},
		{"kafka-connect", "kafka"}, {"kafka-connect", "postgres"}, {"kafka-connector", "kafka-connect"},
		{"mirror-maker2", "kafka"}, {"kafka-ui", "kafka"}, {"apicurio", "kafka"},
		{"kates", "kafka"}, {"kates", "postgres"},
		{"kyverno-scrape", "kyverno"}, {"kyverno-scrape", "monitoring"},
	} {
		if !reaches(tc[0], tc[1]) {
			t.Errorf("%s does not wait for %s", tc[0], tc[1])
		}
	}
	for id, c := range byID {
		for _, d := range append(append([]string(nil), c.deps...), c.after...) {
			if _, ok := byID[d]; !ok {
				t.Errorf("%s depends on %q, which no component is", id, d)
			}
		}
	}
}

// A readiness wait that times out fails the deploy with the documented
// message, naming the component and the selector, and runDeploy returns the
// error (a non-zero exit). The waits used to be skipped in tests, so this
// path had never run.
func TestDeploy_ReadinessTimeoutFailsTheDeploy(t *testing.T) {
	origExec, origStdin, origHelm, origOutput, origCombined, origDeployed, origExecutor := runExecFn, runExecStdinFn, runHelmFn, runExecOutputFn, runExecCombinedFn, isHelmReleaseDeployedFn, defaultExecutor
	t.Cleanup(func() {
		runExecFn, runExecStdinFn, runHelmFn, runExecOutputFn, runExecCombinedFn, isHelmReleaseDeployedFn, defaultExecutor = origExec, origStdin, origHelm, origOutput, origCombined, origDeployed, origExecutor
	})
	setDeployFlags(t, func() {
		deployTopology, deployNamespace, deployWithSchemaRegistry = "single", "kates-test", "none"
		deployWithChaos, deployWithMonitoring, deployWithCertManager = false, false, false
		deployWithKyverno, deployWithStrimzi, deployWithKafkaConnect = false, false, false
		deployWithKafkaUI, deployWithMirrorMaker2, deployDryRun = false, false, false
	})
	t.Chdir(t.TempDir())

	var mu sync.Mutex
	var helmCalls []string
	runExecFn = func(context.Context, string, ...string) error { return nil }
	runExecStdinFn = func(context.Context, string, []string, string) error { return nil }
	runHelmFn = func(_ context.Context, args ...string) error {
		mu.Lock()
		defer mu.Unlock()
		helmCalls = append(helmCalls, strings.Join(args, " "))
		return nil
	}
	isHelmReleaseDeployedFn = func(context.Context, string, string) bool { return false }
	defaultExecutor = &MockExecutor{}
	polls := 0
	runExecOutputFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		joined := name + " " + strings.Join(args, " ")
		// The backend's pods never leave Pending.
		if strings.Contains(joined, "get pods") && strings.Contains(joined, "app.kubernetes.io/instance=kates") {
			mu.Lock()
			polls++
			mu.Unlock()
			return []byte("Pending,Pending,"), nil
		}
		return stubClusterRead(ctx, name, args...)
	}
	runExecCombinedFn = runExecOutputFn

	start := time.Now()
	var err error
	out := captureStdout(t, func() { err = runDeploy(deployCmd, nil) })
	if err == nil {
		t.Fatalf("runDeploy returned nil over a backend that never became ready\n%s", out)
	}
	for _, want := range []string{"Kates Backend", "timeout waiting for pods (selector=app.kubernetes.io/instance=kates) to become ready"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if polls < 100 {
		t.Errorf("the wait polled %d times before its 8 minute timeout; it gave up early", polls)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("the timeout took %s of real time; the fake clock was not read", elapsed)
	}
	mu.Lock()
	defer mu.Unlock()
	if !sliceContainsPrefix(helmCalls, "upgrade --install kates ") {
		t.Errorf("the backend was never installed, so the wait that timed out was not its: %v", helmCalls)
	}
}

// A helm dependency update that fails is the error the deploy reports, with
// helm's stderr, before any install. Its result used to be discarded, and
// the install that followed failed later with "chart not found".
func TestDeploy_FailedDependencyUpdateSurfacesStderr(t *testing.T) {
	origExec, origStdin, origHelm, origDeployed, origExecutor := runExecFn, runExecStdinFn, runHelmFn, isHelmReleaseDeployedFn, defaultExecutor
	t.Cleanup(func() {
		runExecFn, runExecStdinFn, runHelmFn, isHelmReleaseDeployedFn, defaultExecutor = origExec, origStdin, origHelm, origDeployed, origExecutor
	})
	setDeployFlags(t, func() {
		deployTopology, deployNamespace, deployWithSchemaRegistry = "single", "kates-test", "none"
		deployWithChaos, deployWithMonitoring, deployWithCertManager = false, false, false
		deployWithKyverno, deployWithStrimzi, deployWithKafkaConnect = false, false, false
		deployWithKafkaUI, deployWithMirrorMaker2, deployDryRun = false, false, false
	})
	t.Chdir(t.TempDir())

	const stderr = "Error: no repository definition for https://strimzi.io/charts/. Please add the missing repos via 'helm repo add'"
	var mu sync.Mutex
	var helmCalls []string
	runExecFn = func(context.Context, string, ...string) error { return nil }
	runExecStdinFn = func(context.Context, string, []string, string) error { return nil }
	runHelmFn = func(_ context.Context, args ...string) error {
		mu.Lock()
		defer mu.Unlock()
		helmCalls = append(helmCalls, strings.Join(args, " "))
		if args[0] == "dependency" {
			// What runExecDefault returns: the exit status and the stderr.
			return errors.New("exit status 1: " + stderr)
		}
		return nil
	}
	isHelmReleaseDeployedFn = func(context.Context, string, string) bool { return false }
	defaultExecutor = &MockExecutor{}

	var err error
	out := captureStdout(t, func() { err = runDeploy(deployCmd, nil) })
	if err == nil {
		t.Fatalf("runDeploy returned nil over a failed dependency update\n%s", out)
	}
	for _, want := range []string{"Kafka Cluster", "helm dependency update charts/kafka-cluster", stderr} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, c := range helmCalls {
		if strings.HasPrefix(c, "upgrade --install krafter") {
			t.Errorf("Kafka was installed after its dependency update failed: %v", helmCalls)
		}
	}
}
