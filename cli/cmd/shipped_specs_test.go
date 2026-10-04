package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/client"
	"gopkg.in/yaml.v3"
)

// TestShippedFilesTheBackendAccepts sends every scenario and resilience file
// Kates ships through the checks the backend makes on a test request's spec
// (TestOrchestrator.inapplicableFields, mirrored by mcpScnCheckApplies). The
// files are what users are told to run with kates test apply -f and kates
// resilience run -f, so a field the backend refuses would make the example
// itself the first thing that fails.
func TestShippedFilesTheBackendAccepts(t *testing.T) {
	var files []string
	for _, dir := range []string{"../examples", "../../scenarios", "scenarios"} {
		matches, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, matches...)
	}
	if len(files) < 20 {
		t.Fatalf("found only %d shipped files: %v", len(files), files)
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for i, req := range shippedRequests(t, path, data) {
			var fs mcpScnFindings
			mcpScnCheckApplies(i, req, &fs)
			for _, f := range fs.list {
				t.Errorf("%s, test %d (%s): %s %s: %s", path, i, req.TestType, f.Level, f.Field, f.Message)
			}
		}
	}
}

// shippedRequests reads the requests a shipped file sends: a scenario file's
// through scenarioToRequest, a resilience file's testRequest as written.
func shippedRequests(t *testing.T, path string, data []byte) []*client.CreateTestRequest {
	t.Helper()
	if bytes.Contains(data, []byte("\ntestRequest:")) {
		var cfg ResilienceConfig
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		raw, err := json.Marshal(cfg.TestRequest.Spec)
		if err != nil {
			t.Fatal(err)
		}
		spec := &client.TestSpec{}
		if err := json.Unmarshal(raw, spec); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		return []*client.CreateTestRequest{{TestType: strings.ToUpper(cfg.TestRequest.Type), Backend: cfg.TestRequest.Backend, Spec: spec}}
	}
	var sf ScenarioFile
	if err := yaml.Unmarshal(data, &sf); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if len(sf.Scenarios) == 0 {
		t.Fatalf("%s holds no scenarios and no testRequest", path)
	}
	reqs := make([]*client.CreateTestRequest, 0, len(sf.Scenarios))
	for _, s := range sf.Scenarios {
		reqs = append(reqs, scenarioToRequest(s))
	}
	return reqs
}

// TestRepoScenariosMatchTheEmbeddedTemplates keeps scenarios/ at the root of
// the repository, which the README points at, the same as the templates kates
// scaffold embeds. The two drifted once: a field the backend refuses was
// removed from one copy only.
func TestRepoScenariosMatchTheEmbeddedTemplates(t *testing.T) {
	embedded, err := filepath.Glob(filepath.Join("scenarios", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	repo, err := filepath.Glob(filepath.Join("..", "..", "scenarios", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(embedded) == 0 || len(embedded) != len(repo) {
		t.Fatalf("scenarios/ has %d files, cli/cmd/scenarios/ %d", len(repo), len(embedded))
	}
	for _, e := range embedded {
		want, err := os.ReadFile(e)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join("..", "..", "scenarios", filepath.Base(e)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("scenarios/%s differs from cli/cmd/scenarios/%s", filepath.Base(e), filepath.Base(e))
		}
	}
}
