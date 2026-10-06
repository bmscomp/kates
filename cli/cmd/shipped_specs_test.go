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
	for _, path := range shippedFiles(t) {
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
		cfg, err := parseResilienceFile(path, data)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		test, _ := cfg["testRequest"].(map[string]any)
		raw, err := json.Marshal(test["spec"])
		if err != nil {
			t.Fatal(err)
		}
		spec := &client.TestSpec{}
		if err := json.Unmarshal(raw, spec); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		return []*client.CreateTestRequest{{TestType: strings.ToUpper(mapStrEmpty(test, "type")), Backend: mapStrEmpty(test, "backend"), Spec: spec}}
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

// shippedFiles lists every scenario and resilience file Kates ships: the
// examples, the scenarios at the root of the repository and the templates
// kates test scaffold embeds.
func shippedFiles(t *testing.T) []string {
	t.Helper()
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
	return files
}

// TestShippedScenariosSetOnlyWhatTheirRunsRead fails on a spec key in a
// scenario file Kates ships that kates test apply does not read, and on a
// count the scenario's type does not read. scenarioToRequest reads the keys
// mcpScnSpecKeys lists (TestMCPDraftScenarioSpecKeysMatchScenarioToRequest
// holds the two together) and drops any other without a word, so the run
// takes its type's default: the perf examples once set numRecords, recordSize,
// throughput and numProducers, and perf-load's LOAD run, capped at 10,000
// records/s in the file, ran unthrottled. Only STRESS and CAPACITY start a
// producer per parallelProducers (TestOrchestrator.buildTasks), and no type
// reads numConsumers. draft_scenario warns only about a count above what the
// run starts; a shipped file sets none its type ignores, since a reader who
// raises one gets nothing.
func TestShippedScenariosSetOnlyWhatTheirRunsRead(t *testing.T) {
	for _, path := range shippedFiles(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("\ntestRequest:")) {
			continue // a resilience file
		}
		var sf ScenarioFile
		if err := yaml.Unmarshal(data, &sf); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if len(sf.Scenarios) == 0 {
			t.Errorf("%s holds no scenarios", path)
		}
		for i, s := range sf.Scenarios {
			testType := strings.ToUpper(s.Type)
			for _, key := range mcpScnSortedKeys(s.Spec) {
				fail := func(why string) {
					t.Errorf("%s, scenario %d (%s): spec.%s: %s", path, i+1, s.Name, key, why)
				}
				if _, ok := mcpScnSpecKeys[key]; !ok {
					why := "kates test apply does not read it, so the run takes its type's default"
					if hint := mcpScnSpecKeyHints[key]; hint != "" {
						why += "; " + hint
					}
					fail(why)
				}
				switch {
				case key == "parallelProducers" && testType != "STRESS" && testType != "CAPACITY":
					fail("a " + testType + " run starts one producer whatever it says; only STRESS and CAPACITY start one per parallelProducers")
				case key == "numConsumers":
					fail("no test type reads it; a run starts one consumer at most")
				}
			}
		}
	}
}
