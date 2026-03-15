package cmd

import (
	"strings"
	"testing"
)

func TestHealthCmd(t *testing.T) {
	mockResponse := `{
		"status": "UP",
		"version": "1.0.0",
		"uptimeSeconds": 3600,
		"status": "UP",
		"version": "1.0.0",
		"uptimeSeconds": 3600,
		"kafka": {
			"status": "UP",
			"bootstrapServers": "localhost:9092"
		},
		"engine": {
			"activeBackend": "native"
		}
	}`
	ts, buf := setupTest(t, "GET", "/api/health", 200, mockResponse)
	defer ts.Close()

	err := healthCmd.RunE(healthCmd, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stripAnsi(buf.String())
	if !strings.Contains(out, "Kates Health Dashboard") {
		t.Errorf("missing header: %s", out)
	}
	if !strings.Contains(out, "UP") {
		t.Errorf("missing UP status: %s", out)
	}
}

func TestHealthCmd_Degraded(t *testing.T) {
	mockResponse := `{
		"status": "DEGRADED",
		"version": "1.0.0",
		"uptimeSeconds": 3600,
		"kafkaStatus": "DOWN",
		"engineStatus": "UP",
		"databaseStatus": "UP"
	}`
	ts, _ := setupTest(t, "GET", "/api/health", 503, mockResponse)
	defer ts.Close()

	err := healthCmd.RunE(healthCmd, nil)
	// Even though the API responded with 503 HTTP, we expect our command to parse it and print the degraded state.
	// However, our generic get[*] client returns the error for HTTP >= 400.
	// So we expect an error to be returned here, but we still want to read whatever it printed.
	if err == nil {
		t.Fatalf("expected error due to 503 response, got nil")
	}

	out := err.Error() // APIError will return the formatted HTTP error string
	if !strings.Contains(out, "DEGRADED") {
		t.Errorf("expected DEGRADED error string, got: %s", out)
	}
}

func TestStatusCmd(t *testing.T) {
	mockResponse := `{
		"status": "UP",
		"version": "1.0.0",
		"uptimeSeconds": 3600,
		"status": "UP",
		"version": "1.0.0",
		"uptimeSeconds": 3600,
		"kafka": {
			"status": "UP",
			"bootstrapServers": "localhost:9092"
		}
	}`
	// The status command makes two calls: one to /api/health and one to /api/tests.
	// Our simple httptest server will just route all GETs to the same response for this test.
	ts, buf := setupTest(t, "GET", "", 200, mockResponse)
	defer ts.Close()

	// Capture output
	err := statusCmd.RunE(statusCmd, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stripAnsi(buf.String())
	if !strings.Contains(out, "UP") {
		t.Errorf("expected output to contain UP, got: %s", out)
	}
	if !strings.Contains(out, "Kafka ✓") {
		t.Errorf("expected output to contain Kafka ✓, got: %s", out)
	}
}
