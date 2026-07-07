package cmd

import "testing"

func TestApplyPortForwardContext_UpdatesExistingContext(t *testing.T) {
	cfg := Config{
		CurrentContext: "local",
		Contexts: map[string]Context{
			"local": {
				URL:      "http://localhost:30083",
				Output:   "json",
				APIKey:   "old-key",
				ProxyURL: "http://proxy:8080",
				Insecure: true,
			},
		},
	}

	updated := applyPortForwardContext(cfg, "local", "http://localhost:8080", "new-key")
	got := updated.Contexts["local"]

	if updated.CurrentContext != "local" {
		t.Fatalf("CurrentContext = %q, want %q", updated.CurrentContext, "local")
	}
	if got.URL != "http://localhost:8080" {
		t.Fatalf("URL = %q, want %q", got.URL, "http://localhost:8080")
	}
	if got.APIKey != "new-key" {
		t.Fatalf("APIKey = %q, want %q", got.APIKey, "new-key")
	}
	if got.Output != "json" {
		t.Fatalf("Output = %q, want %q", got.Output, "json")
	}
	if got.ProxyURL != "http://proxy:8080" {
		t.Fatalf("ProxyURL = %q, want %q", got.ProxyURL, "http://proxy:8080")
	}
	if !got.Insecure {
		t.Fatal("Insecure should remain true")
	}
}

func TestApplyPortForwardContext_CreatesMissingContext(t *testing.T) {
	cfg := Config{
		CurrentContext: "",
		Contexts:       map[string]Context{},
	}

	updated := applyPortForwardContext(cfg, "", "http://localhost:8080", "key-1")
	got, ok := updated.Contexts["local"]
	if !ok {
		t.Fatal("expected local context to be created")
	}
	if updated.CurrentContext != "local" {
		t.Fatalf("CurrentContext = %q, want %q", updated.CurrentContext, "local")
	}
	if got.URL != "http://localhost:8080" {
		t.Fatalf("URL = %q, want %q", got.URL, "http://localhost:8080")
	}
	if got.APIKey != "key-1" {
		t.Fatalf("APIKey = %q, want %q", got.APIKey, "key-1")
	}
	if got.Output != "table" {
		t.Fatalf("Output = %q, want %q", got.Output, "table")
	}
}

func TestApplyPortForwardContext_KeepExistingAPIKeyWhenNewOneEmpty(t *testing.T) {
	cfg := Config{
		CurrentContext: "local",
		Contexts: map[string]Context{
			"local": {
				URL:    "http://localhost:30083",
				Output: "table",
				APIKey: "existing",
			},
		},
	}

	updated := applyPortForwardContext(cfg, "local", "http://localhost:8080", "")
	got := updated.Contexts["local"]

	if got.APIKey != "existing" {
		t.Fatalf("APIKey = %q, want %q", got.APIKey, "existing")
	}
	if got.URL != "http://localhost:8080" {
		t.Fatalf("URL = %q, want %q", got.URL, "http://localhost:8080")
	}
}
