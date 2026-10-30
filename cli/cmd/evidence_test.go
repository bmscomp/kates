package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
)

// evidenceAPI is a stub Kates API: each list is paged as the real one pages
// it, with page and size, and each item answers at its own path.
type evidenceAPI struct {
	lists map[string][]string // path → items, newest first
	items map[string]string   // path → body
}

func (a evidenceAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if body, ok := a.items[r.URL.Path]; ok {
		_, _ = w.Write([]byte(body))
		return
	}
	list, ok := a.lists[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	start, end := min(page*size, len(list)), min((page+1)*size, len(list))
	// No total: the export must page on without one.
	fmt.Fprintf(w, `{"items":[%s]}`, strings.Join(list[start:end], ","))
}

func evidenceRun(id, createdAt string) string {
	return fmt.Sprintf(`{"id":%q,"createdAt":%q,"testType":"LOAD","extra":{"kept":true}}`, id, createdAt)
}

func evidenceStub(t *testing.T, verify string) evidenceAPI {
	t.Helper()
	return evidenceAPI{
		lists: map[string][]string{
			"/api/tests": {
				evidenceRun("after", "2026-10-01T00:00:00Z"),
				evidenceRun("in-2", "2026-09-15T00:00:00.5Z"),
				evidenceRun("in-1", "2026-07-01T00:00:00Z"),
				evidenceRun("before", "2026-06-30T23:59:59Z"),
				evidenceRun("never-read", "2026-01-01T00:00:00Z"),
			},
			"/api/disruptions":       {`{"id":"d-1","planName":"broker-kill","createdAt":"2026-08-01T00:00:00Z"}`},
			"/api/conformity/checks": {`{"id":"c-1","checkedAt":"2026-08-02T00:00:00Z"}`},
			"/api/audit": {
				`{"id":9,"action":"RUN","timestamp":"2026-08-02T00:00:00Z","rowHash":"ab"}`,
				`{"id":8,"action":"RUN","timestamp":"2026-08-01T00:00:00Z","rowHash":"cd"}`,
			},
		},
		items: map[string]string{
			"/api/disruptions/d-1":       `{"id":"d-1","steps":[{"name":"kill-broker-0"}],"slaGrade":"A"}`,
			"/api/conformity/checks/c-1": `{"id":"c-1","report":{"verdict":"PASS"}}`,
			"/api/audit/verify":          verify,
		},
	}
}

func exportTo(t *testing.T, api http.Handler) (string, error) {
	t.Helper()
	ts := httptest.NewServer(api)
	t.Cleanup(ts.Close)
	apiClient = client.New(ts.URL)
	outputMode = "table"
	output.ResetForTesting()
	evidenceFrom, evidenceTo = "2026-07-01T00:00:00Z", "2026-10-01T00:00:00Z"
	evidenceOut = filepath.Join(t.TempDir(), "pack")
	evidenceSign = false
	return evidenceOut, evidenceExportCmd.RunE(evidenceExportCmd, nil)
}

func readItems(t *testing.T, dir, name string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	var items []map[string]any
	if err := json.Unmarshal(data, &items); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return items
}

func TestEvidenceExportWritesThePeriodAsTheAPIAnsweredIt(t *testing.T) {
	dir, err := exportTo(t, evidenceStub(t, `{"intact":true,"headId":9,"headHash":"ab"}`))
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	runs := readItems(t, dir, "runs.json")
	if len(runs) != 2 || runs[0]["id"] != "in-2" || runs[1]["id"] != "in-1" {
		t.Fatalf("runs = %v, want in-2 and in-1: the period is [from, to)", runs)
	}
	if runs[0]["extra"] == nil {
		t.Errorf("a field the CLI doesn't name was dropped: %v", runs[0])
	}
	if d := readItems(t, dir, "disruptions.json"); len(d) != 1 || d[0]["slaGrade"] != "A" {
		t.Errorf("disruptions = %v, want d-1's full report", d)
	}
	if c := readItems(t, dir, "compliance-checks.json"); len(c) != 1 || c[0]["report"] == nil {
		t.Errorf("compliance checks = %v, want c-1 with its report", c)
	}
	if a := readItems(t, dir, "audit.json"); len(a) != 2 {
		t.Errorf("audit = %v, want 2 rows", a)
	}

	sums, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(sums)), "\n")
	if len(lines) != len(evidenceFiles) {
		t.Fatalf("SHA256SUMS has %d lines, want one per file:\n%s", len(lines), sums)
	}
	for _, line := range lines {
		sum, name, _ := strings.Cut(line, "  ")
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		got := sha256.Sum256(data)
		if hex.EncodeToString(got[:]) != sum {
			t.Errorf("SHA256SUMS: %s does not match its file", name)
		}
	}
}

func TestEvidenceExportPagesPastOnePageAndKeepsEachRunOnce(t *testing.T) {
	api := evidenceStub(t, `{"intact":true}`)
	var runs []string
	start := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 450; i++ {
		runs = append(runs, evidenceRun("r-"+strconv.Itoa(i), start.Add(-time.Duration(i)*time.Minute).Format(time.RFC3339)))
	}
	// A run created during the read pushes the second page down by one.
	runs = append(runs[:201], runs[200:]...)
	api.lists["/api/tests"] = runs

	dir, err := exportTo(t, api)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if got := len(readItems(t, dir, "runs.json")); got != 450 {
		t.Errorf("runs = %d, want each of the 450 once", got)
	}
}

func TestEvidenceExportReadsEveryAuditPageWithoutATotal(t *testing.T) {
	api := evidenceStub(t, `{"intact":true}`)
	var rows []string
	for i := 0; i < 401; i++ {
		rows = append(rows, fmt.Sprintf(`{"id":%d,"timestamp":"2026-08-01T00:00:00Z"}`, 1000-i))
	}
	api.lists["/api/audit"] = rows

	dir, err := exportTo(t, api)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if got := len(readItems(t, dir, "audit.json")); got != 401 {
		t.Errorf("audit rows = %d, want all 401 across three pages", got)
	}
}

func TestEvidenceExportThatFailsLeavesNoDirectory(t *testing.T) {
	api := evidenceStub(t, `{"intact":true}`)
	delete(api.lists, "/api/conformity/checks")
	dir, err := exportTo(t, api)
	if err == nil {
		t.Fatal("want the export to fail")
	}
	if _, statErr := os.Stat(dir); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("a failed export left %s behind, so a retry would be refused: %v", dir, statErr)
	}
}

func TestEvidenceExportExits1OnABrokenChainWithThePackWritten(t *testing.T) {
	dir, err := exportTo(t, evidenceStub(t, `{"intact":false,"brokenAt":8,"problem":"row 8 does not match its hash"}`))
	var exit *exitError
	if !errors.As(err, &exit) || exit.code != failedExitCode {
		t.Fatalf("err = %v, want exit code 1", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "SHA256SUMS")); err != nil {
		t.Errorf("the pack must be written all the same: %v", err)
	}
}

func TestEvidenceExportRefusesAnAuditListOutsideThePeriod(t *testing.T) {
	api := evidenceStub(t, `{"intact":true}`)
	api.lists["/api/audit"] = []string{`{"id":1,"timestamp":"2026-10-05T00:00:00Z"}`}
	_, err := exportTo(t, api)
	if err == nil || !strings.Contains(err.Error(), "ignores until") {
		t.Fatalf("err = %v, want the refusal that names an old Kates API", err)
	}
}

func TestEvidenceExportNamesTheUpgradeAnOldAPINeeds(t *testing.T) {
	api := evidenceStub(t, `{"intact":true}`)
	delete(api.lists, "/api/conformity/checks")
	_, err := exportTo(t, api)
	if err == nil || !strings.Contains(err.Error(), "upgrade it") {
		t.Fatalf("err = %v, want the upgrade hint", err)
	}
}

func TestEvidenceExportRefusesADirectoryThatExists(t *testing.T) {
	ts := httptest.NewServer(evidenceStub(t, `{"intact":true}`))
	defer ts.Close()
	apiClient = client.New(ts.URL)
	output.ResetForTesting()
	evidenceFrom, evidenceTo, evidenceOut = "2026-07-01T00:00:00Z", "", t.TempDir()
	if err := evidenceExportCmd.RunE(evidenceExportCmd, nil); err == nil {
		t.Fatal("an existing directory must be refused, so no pack mixes with older files")
	}
}

func TestEvidencePeriod(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	from, to, err := evidencePeriod("2026-10-01T00:00:00+02:00", "", now)
	if err != nil || !to.Equal(now) || from.Location() != time.UTC || from.Hour() != 22 {
		t.Errorf("from %v to %v err %v: want --to to default to now, both in UTC", from, to, err)
	}
	for _, bad := range [][2]string{{"yesterday", ""}, {"2026-10-01T00:00:00Z", "noon"}, {"2026-10-02T00:00:00Z", "2026-10-01T00:00:00Z"}} {
		if _, _, err := evidencePeriod(bad[0], bad[1], now); err == nil {
			t.Errorf("--from %q --to %q: want an error", bad[0], bad[1])
		}
	}
}
