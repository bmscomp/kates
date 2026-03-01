package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/klster/kates-cli/client"
	"github.com/klster/kates-cli/output"
)

// setupTest creates an httptest.Server that returns the given statusCode and body.
// It overrides the global apiClient to point to the test server.
// It also overrides output.Out to a bytes.Buffer, capturing all CLI output.
// Returns the server (caller must defer ts.Close()) and the output buffer.
func setupTest(t *testing.T, expectedMethod, expectedPath string, statusCode int, responseBody string) (*httptest.Server, *bytes.Buffer) {
	t.Helper()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if expectedMethod != "" && r.Method != expectedMethod {
			t.Errorf("Expected method %s, got %s", expectedMethod, r.Method)
		}
		if expectedPath != "" && r.URL.Path != expectedPath {
			t.Errorf("Expected path %s, got %s", expectedPath, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		w.Write([]byte(responseBody))
	}))

	// Override the global apiClient used by Cobra commands
	apiClient = client.New(ts.URL)

	// Set CLI output mode to table so we can assert on text formatting
	outputMode = "table"

	// Override output.Out and return the buffer for assertions
	buf := output.ResetForTesting()

	return ts, buf
}

// stripAnsi removes all ANSI escape codes from a string to simplify assertions.
func stripAnsi(s string) string {
	var result []byte
	inEsc := false
	for i := 0; i < len(s); i++ {
		if s[i] == '\033' {
			inEsc = true
			continue
		}
		if inEsc {
			if (s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z') {
				inEsc = false
			}
			continue
		}
		result = append(result, s[i])
	}
	return string(result)
}
