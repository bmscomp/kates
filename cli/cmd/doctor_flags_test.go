package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bmscomp/kates/cli/client"
)

// doctorAPI is a healthy Kates API with three brokers; a request to a path
// in hang waits until the client gives up on it.
func doctorAPI(t *testing.T, hang ...string) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, h := range hang {
			if r.URL.Path == h {
				<-r.Context().Done()
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		body := map[string]string{
			"/api/health":         `{"status":"UP","kafka":{"status":"UP","bootstrapServers":"k:9092"}}`,
			"/api/cluster/info":   `{"clusterId":"c","brokers":[{"id":0},{"id":1},{"id":2}]}`,
			"/api/cluster/check":  `{"clusterId":"c","partitionHealth":{"underReplicated":0,"offline":0}}`,
			"/api/cluster/topics": `{"items":["a"]}`,
			"/api/tests/backends": `["native"]`,
		}[r.URL.Path]
		if body == "" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(ts.Close)
	prev := apiClient
	t.Cleanup(func() { apiClient = prev })
	apiClient = client.New(ts.URL)
}

// runDoctor runs kates doctor -o json with the flags given and returns its
// checks and error.
func runDoctor(t *testing.T, timeout string, kyverno bool) ([]checkResult, error) {
	t.Helper()
	prevTimeout, prevKyverno, prevMode := doctorTimeout, doctorKyverno, outputMode
	t.Cleanup(func() { doctorTimeout, doctorKyverno, outputMode = prevTimeout, prevKyverno, prevMode })
	doctorTimeout, doctorKyverno, outputMode = timeout, kyverno, "json"
	var err error
	stdout, _, _ := commandOutput(t, func() error {
		err = doctorCmd.RunE(doctorCmd, nil)
		return nil
	})
	var checks []checkResult
	if len(stdout) > 0 {
		if jerr := json.Unmarshal(stdout, &checks); jerr != nil {
			t.Fatalf("stdout is not the checks: %v\n%s", jerr, stdout)
		}
	}
	return checks, err
}

// --kyverno=false leaves the Kyverno checks out, and kubectl is not run.
func TestDoctorKyvernoFalseSkipsKyverno(t *testing.T) {
	doctorAPI(t)
	f := stubRunner(t)
	f.Unmatched = errors.New("kubectl must not run")

	checks, err := runDoctor(t, "2m", false)
	if err != nil {
		t.Fatalf("a healthy cluster: err = %v", err)
	}
	for _, c := range checks {
		if strings.Contains(c.Name, "Kyverno") {
			t.Errorf("--kyverno=false ran %q", c.Name)
		}
	}
	if len(checks) != 6 {
		t.Errorf("%d checks, want the 6 API checks: %+v", len(checks), checks)
	}
	if calls := f.Lines(); len(calls) != 0 {
		t.Errorf("--kyverno=false ran %q", calls)
	}

	// The default runs them.
	checks, _ = runDoctor(t, "2m", true)
	if !strings.Contains(checks[len(checks)-1].Name, "Kyverno") {
		t.Errorf("the default skipped Kyverno: %+v", checks)
	}
}

// A --timeout that runs out fails the checks still waiting, says so, and
// ends doctor in about that time with exit 1.
func TestDoctorTimeoutBoundsTheChecklist(t *testing.T) {
	doctorAPI(t, "/api/cluster/check")
	stubRunner(t)

	start := time.Now()
	checks, err := runDoctor(t, "300ms", false)
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("doctor took %s with --timeout 300ms", took)
	}
	var exit *exitError
	if !errors.As(err, &exit) || exit.code != failedExitCode {
		t.Errorf("err = %v, want exit 1", err)
	}
	byName := map[string]checkResult{}
	for _, c := range checks {
		byName[c.Name] = c
	}
	if c := byName["Kafka Connected"]; !c.Passed {
		t.Errorf("a check before the timeout: %+v, want passed", c)
	}
	for _, name := range []string{"ISR Health", "Topics Available"} {
		if c := byName[name]; c.Passed || !strings.Contains(c.Detail, "timed out") {
			t.Errorf("%s after the timeout: %+v, want failed with timed out", name, c)
		}
	}
}

func TestDoctorRefusesABadTimeout(t *testing.T) {
	_, err := runDoctor(t, "soon", false)
	var exit *exitError
	if !errors.As(err, &exit) || exit.code != couldNotTellExitCode {
		t.Errorf("--timeout soon: err = %v, want exit 2", err)
	}
}

// doctorDetail names the timeout only when it is what ran out.
func TestDoctorDetail(t *testing.T) {
	if got := doctorDetail(context.Background(), "x"); got != "x" {
		t.Errorf("no deadline: %q", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := doctorDetail(ctx, "x"); got != "x" {
		t.Errorf("cancelled, not timed out: %q", got)
	}
}
