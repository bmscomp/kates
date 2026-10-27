package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/client"
)

// reportExportBackend serves run-1's CSV and its report.
func reportExportBackend(t *testing.T) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tests/run-1/report/csv":
			_, _ = io.WriteString(w, "metric,value\np99,12.5\n")
		case "/api/tests/run-1/report":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"summary":{"avgThroughputRecPerSec":31000,"p99LatencyMs":12.5,"totalRecords":1000}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(ts.Close)
	prevClient, prevOutput, prevInteractive := apiClient, outputMode, interactiveAllowedFn
	apiClient, outputMode = client.New(ts.URL), "table"
	t.Cleanup(func() {
		apiClient, outputMode, interactiveAllowedFn = prevClient, prevOutput, prevInteractive
		exportFormat, exportFile = "csv", ""
	})
}

// One writer for every format: -f names the file; without it the export goes
// to stdout when nothing is watching a terminal, and to a file named after
// the run when something is. html used to go to the file even when piped.
func TestReportExport_FileOrStdout(t *testing.T) {
	reportExportBackend(t)
	t.Chdir(t.TempDir())

	t.Run("-f names the file", func(t *testing.T) {
		exportFormat, exportFile = "csv", ""
		out := filepath.Join(t.TempDir(), "sub", "r.csv")
		stdout, _, err := commandOutput(t, func() error { return runCommandLine(t, "report", "export", "run-1", "-f", out) })
		if err != nil {
			t.Fatal(err)
		}
		if data, err := os.ReadFile(out); err != nil || string(data) != "metric,value\np99,12.5\n" {
			t.Errorf("file = %q, %v", data, err)
		}
		if strings.Contains(string(stdout), "p99,12.5") || !strings.Contains(string(stdout), "Exported to "+out) {
			t.Errorf("stdout should name the file and carry no data:\n%s", stdout)
		}
	})

	t.Run("piped goes to stdout, html too", func(t *testing.T) {
		for _, format := range []string{"csv", "html", "md"} {
			exportFormat, exportFile = "csv", ""
			stdout, _, err := commandOutput(t, func() error {
				return runCommandLine(t, "report", "export", "run-1", "--format", format)
			})
			if err != nil {
				t.Fatalf("%s: %v", format, err)
			}
			if !strings.Contains(string(stdout), "12.5") || strings.Contains(string(stdout), "Exported to") {
				t.Errorf("%s: stdout should carry the export and nothing else:\n%s", format, stdout)
			}
			if entries, _ := os.ReadDir("."); len(entries) != 0 {
				t.Errorf("%s: wrote %v although piped", format, entries)
			}
		}
	})

	t.Run("-f - is stdout", func(t *testing.T) {
		exportFormat, exportFile = "csv", ""
		stdout, _, err := commandOutput(t, func() error { return runCommandLine(t, "report", "export", "run-1", "-f", "-") })
		if err != nil || !strings.HasPrefix(string(stdout), "metric,value") {
			t.Errorf("stdout = %q, %v", stdout, err)
		}
	})

	t.Run("in a terminal the file is named after the run", func(t *testing.T) {
		interactiveAllowedFn = func() bool { return true }
		defer func() { interactiveAllowedFn = func() bool { return false } }()
		exportFormat, exportFile = "csv", ""
		stdout, _, err := commandOutput(t, func() error {
			return runCommandLine(t, "report", "export", "run-1", "--format", "html")
		})
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile("kates-report-run-1.html")
		if err != nil || !strings.Contains(string(data), "12.5") {
			t.Errorf("kates-report-run-1.html = %v, %v", err, len(data))
		}
		if !strings.Contains(string(stdout), "Exported to kates-report-run-1.html") || !strings.Contains(string(stdout), "Open:") {
			t.Errorf("stdout:\n%s", stdout)
		}
	})
}
