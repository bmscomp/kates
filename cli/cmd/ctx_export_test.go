package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The chart generates 32-character keys; a key set by hand may be short.
const (
	exportLongKey  = "Zk3vQ8pLmN0aBcDeFgHiJkLmNoPqRsTu"
	exportShortKey = "abc"
)

// runCtxCommandLine runs a ctx command line as runCommandLine does, for the
// ctx commands, which use Run rather than RunE. It resets the flags they bind
// first, as a new process would start.
func runCtxCommandLine(t *testing.T, args ...string) {
	t.Helper()
	ctxExportFlag, ctxExportReveal, ctxImportFile = "", false, ""
	t.Cleanup(func() { ctxExportFlag, ctxExportReveal, ctxImportFile = "", false, "" })
	cmd, rest, err := rootCmd.Find(args)
	if err != nil {
		t.Fatalf("no command for %q: %v", args, err)
	}
	if err := cmd.ParseFlags(rest); err != nil {
		t.Fatalf("flags %q: %v", rest, err)
	}
	cmd.Run(cmd, cmd.Flags().Args())
}

func exportTestConfig() Config {
	return Config{CurrentContext: "lab", Contexts: map[string]Context{
		"lab":   {URL: "http://localhost:8080", Output: "table", APIKey: exportLongKey},
		"tiny":  {URL: "http://localhost:30083", Output: "json", APIKey: exportShortKey},
		"nokey": {URL: "https://kates.example.com", Output: "table"},
	}}
}

func TestMaskAPIKey(t *testing.T) {
	for key, want := range map[string]string{
		exportLongKey:      "Zk3v****",
		"0123456789abcdef": "0123****",
		"0123456789abcde":  "****", // under 16: four characters would be a large share
		exportShortKey:     "****",
		"":                 "****",
	} {
		got := maskAPIKey(key)
		if got != want {
			t.Errorf("maskAPIKey(%q) = %q, want %q", key, got, want)
		}
		if !isMaskedAPIKey(got) {
			t.Errorf("isMaskedAPIKey(%q) = false for a masked key", got)
		}
	}
	for _, key := range []string{exportLongKey, exportShortKey, "abcd***", "abcd*****"} {
		if isMaskedAPIKey(key) {
			t.Errorf("isMaskedAPIKey(%q) = true for a key", key)
		}
	}
}

func TestCtxExport_MasksKeys(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mustSaveConfig(t, exportTestConfig())

	stdout, stderr, err := commandOutput(t, func() error { runCtxCommandLine(t, "ctx", "export"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stdout), exportLongKey) || strings.Contains(string(stdout), "api-key: "+exportShortKey) {
		t.Errorf("export prints a key in clear:\n%s", stdout)
	}
	var got Config
	if err := yaml.Unmarshal(stdout, &got); err != nil {
		t.Fatalf("export is not YAML: %v\n%s", err, stdout)
	}
	for name, want := range map[string]string{"lab": "Zk3v****", "tiny": "****", "nokey": ""} {
		if key := got.Contexts[name].APIKey; key != want {
			t.Errorf("%s: api-key = %q, want %q", name, key, want)
		}
	}
	if !strings.Contains(stderr, "--reveal") {
		t.Errorf("stderr does not say the keys are masked:\n%s", stderr)
	}

	// --name masks too.
	stdout, _, _ = commandOutput(t, func() error { runCtxCommandLine(t, "ctx", "export", "--name", "lab"); return nil })
	if strings.Contains(string(stdout), exportLongKey) || !strings.Contains(string(stdout), "api-key: Zk3v****") {
		t.Errorf("export --name lab:\n%s", stdout)
	}
}

func TestCtxExport_Reveal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mustSaveConfig(t, exportTestConfig())

	stdout, stderr, _ := commandOutput(t, func() error { runCtxCommandLine(t, "ctx", "export", "--reveal"); return nil })
	var got Config
	if err := yaml.Unmarshal(stdout, &got); err != nil {
		t.Fatalf("export is not YAML: %v\n%s", err, stdout)
	}
	// loadConfig, not exportTestConfig: the config always carries the
	// built-in default context too.
	if want := loadConfig(); !reflect.DeepEqual(got, want) {
		t.Errorf("export --reveal = %+v, want the config as loaded, %+v", got, want)
	}
	if got.Contexts["lab"].APIKey != exportLongKey || got.Contexts["tiny"].APIKey != exportShortKey {
		t.Errorf("export --reveal keys = %q, %q, want them in clear", got.Contexts["lab"].APIKey, got.Contexts["tiny"].APIKey)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing when nothing is masked", stderr)
	}
}

// TestCtxImport_RoundTrip moves contexts to another machine with a revealed
// export, and checks what a masked export does on import: it never stores
// the mask as a key.
func TestCtxImport_RoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mustSaveConfig(t, exportTestConfig())
	revealed, _, _ := commandOutput(t, func() error { runCtxCommandLine(t, "ctx", "export", "--reveal"); return nil })
	masked, _, _ := commandOutput(t, func() error { runCtxCommandLine(t, "ctx", "export"); return nil })
	dir := t.TempDir()
	revealedFile, maskedFile := filepath.Join(dir, "revealed.yaml"), filepath.Join(dir, "masked.yaml")
	for file, data := range map[string][]byte{revealedFile: revealed, maskedFile: masked} {
		if err := os.WriteFile(file, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("revealed", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		_, _, _ = commandOutput(t, func() error { runCtxCommandLine(t, "ctx", "import", "--file", revealedFile); return nil })
		got := loadConfig().Contexts
		for name, want := range exportTestConfig().Contexts {
			if got[name] != want {
				t.Errorf("%s = %+v, want %+v", name, got[name], want)
			}
		}
	})

	t.Run("masked keeps the keys already there", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		mustSaveConfig(t, Config{CurrentContext: "lab", Contexts: map[string]Context{
			"lab": {URL: "http://localhost:8080", Output: "json", APIKey: exportLongKey, KeySource: "kates-api-key@x"},
		}})
		stdout, _, _ := commandOutput(t, func() error { runCtxCommandLine(t, "ctx", "import", "--file", maskedFile); return nil })
		got := loadConfig().Contexts
		if lab := got["lab"]; lab.APIKey != exportLongKey || lab.KeySource != "kates-api-key@x" || lab.Output != "table" {
			t.Errorf("lab = %+v, want the file's settings with the key and key-source it had", lab)
		}
		if tiny := got["tiny"]; tiny.APIKey != "" || tiny.URL != "http://localhost:30083" {
			t.Errorf("tiny = %+v, want the file's URL and no key", tiny)
		}
		out := stripAnsi(string(stdout))
		for _, want := range []string{"lab: the file masks its API key; kept the key it already had", "tiny: the file masks its API key; no key stored"} {
			if !strings.Contains(out, want) {
				t.Errorf("import output lacks %q:\n%s", want, out)
			}
		}
	})
}

// TestCtxImport_MaskedKeyStaysWithItsServer: a masked file keeps the key a
// context already has only while the file sends it where it went before.
// Import used to keep it whatever the file said, so a shared or crafted file
// that named a context of yours with another URL sent your key to that server.
func TestCtxImport_MaskedKeyStaysWithItsServer(t *testing.T) {
	const (
		labURL = "http://localhost:8080"
		proxy  = "http://alice:s3cretProxyPass@proxy.corp:3128"
	)
	existing := Context{URL: labURL, Output: "table", APIKey: exportLongKey, KeySource: "kates-api-key@x"}
	withProxy := existing
	withProxy.ProxyURL = proxy
	tests := []struct {
		name     string
		existing Context
		file     string // the lab context in the file
		wantKey  bool
		wantWarn string
	}{
		{"same server", existing, "url: " + labURL + "\napi-key: Zk3v****", true,
			"lab: the file masks its API key; kept the key it already had"},
		{"another URL", existing, "url: http://127.0.0.1:18731\napi-key: Zk3v****", false,
			"lab: the file masks its API key and points lab at http://127.0.0.1:18731, not " + labURL},
		{"a proxy added", existing, "url: " + labURL + "\napi-key: Zk3v****\nproxy-url: http://proxy.evil:3128", false,
			"lab: the file masks its API key and changes the proxy or TLS settings of lab"},
		{"TLS checks off", existing, "url: " + labURL + "\napi-key: Zk3v****\ninsecure: true", false,
			"lab: the file masks its API key and changes the proxy or TLS settings of lab"},
		{"another key's mask", existing, "url: " + labURL + "\napi-key: Abcd****", false,
			"lab: the file masks another API key than the one lab has"},
		{"a short key's mask", existing, "url: " + labURL + "\napi-key: '****'", false,
			"lab: the file masks another API key than the one lab has"},
		// A masked export hides the proxy password too; the same proxy keeps
		// the password it had, and the key stays.
		{"same proxy, masked", withProxy,
			"url: " + labURL + "\napi-key: Zk3v****\nproxy-url: http://alice:xxxxx@proxy.corp:3128", true,
			"lab: the file masks its API key; kept the key it already had"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			mustSaveConfig(t, Config{CurrentContext: "lab", Contexts: map[string]Context{"lab": tt.existing}})
			file := filepath.Join(t.TempDir(), "contexts.yaml")
			body := "contexts:\n  lab:\n    " + strings.ReplaceAll(tt.file, "\n", "\n    ") + "\n"
			if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			stdout, _, _ := commandOutput(t, func() error { runCtxCommandLine(t, "ctx", "import", "--file", file); return nil })
			lab := loadConfig().Contexts["lab"]
			if tt.wantKey && (lab.APIKey != exportLongKey || lab.KeySource != "kates-api-key@x") {
				t.Errorf("lab = %+v, want the key and key-source it had", lab)
			}
			if !tt.wantKey && (lab.APIKey != "" || lab.KeySource != "") {
				t.Errorf("lab = %+v, want no key: the file sends it elsewhere", lab)
			}
			if lab.ProxyURL != "" && lab.ProxyURL != tt.existing.ProxyURL && strings.Contains(lab.ProxyURL, "xxxxx") {
				t.Errorf("proxy-url = %q, stored the mask as a password", lab.ProxyURL)
			}
			if tt.name == "same proxy, masked" && lab.ProxyURL != proxy {
				t.Errorf("proxy-url = %q, want the one it had, %q", lab.ProxyURL, proxy)
			}
			if out := stripAnsi(string(stdout)); !strings.Contains(out, tt.wantWarn) {
				t.Errorf("import output lacks %q:\n%s", tt.wantWarn, out)
			}
		})
	}
}

// TestCtxExport_MasksProxyAndKeySource: a masked export hides what would
// give the key or the proxy password away. key-source is a salted digest of
// the key, which lets anyone test guesses offline, and import never reads it.
func TestCtxExport_MasksProxyAndKeySource(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const proxy = "http://alice:s3cretProxyPass@proxy.corp:3128"
	source := secretKeySource(exportLongKey)
	mustSaveConfig(t, Config{CurrentContext: "lab", Contexts: map[string]Context{
		"lab":    {URL: "http://localhost:8080", APIKey: exportLongKey, KeySource: source, ProxyURL: proxy},
		"nopass": {URL: "http://localhost:8080", ProxyURL: "http://proxy.corp:3128"},
	}})

	masked, _, _ := commandOutput(t, func() error { runCtxCommandLine(t, "ctx", "export"); return nil })
	for _, secret := range []string{exportLongKey, "s3cretProxyPass", source, "key-source"} {
		if strings.Contains(string(masked), secret) {
			t.Errorf("masked export prints %q:\n%s", secret, masked)
		}
	}
	var got Config
	if err := yaml.Unmarshal(masked, &got); err != nil {
		t.Fatal(err)
	}
	if p := got.Contexts["lab"].ProxyURL; p != "http://alice:xxxxx@proxy.corp:3128" {
		t.Errorf("lab proxy-url = %q, want the password masked", p)
	}
	if p := got.Contexts["nopass"].ProxyURL; p != "http://proxy.corp:3128" {
		t.Errorf("nopass proxy-url = %q, want it as it is", p)
	}

	revealed, _, _ := commandOutput(t, func() error { runCtxCommandLine(t, "ctx", "export", "--reveal"); return nil })
	for _, want := range []string{exportLongKey, proxy, source} {
		if !strings.Contains(string(revealed), want) {
			t.Errorf("export --reveal lacks %q:\n%s", want, revealed)
		}
	}

	// A masked proxy password never becomes the password: a context new to
	// this machine gets the proxy without one.
	t.Setenv("HOME", t.TempDir())
	file := filepath.Join(t.TempDir(), "masked.yaml")
	if err := os.WriteFile(file, masked, 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, _, _ := commandOutput(t, func() error { runCtxCommandLine(t, "ctx", "import", "--file", file); return nil })
	if p := loadConfig().Contexts["lab"].ProxyURL; p != "http://alice@proxy.corp:3128" {
		t.Errorf("imported proxy-url = %q, want it without the masked password", p)
	}
	if out := stripAnsi(string(stdout)); !strings.Contains(out, "lab: the file masks its proxy password") {
		t.Errorf("import output lacks the proxy warning:\n%s", out)
	}
}

// TestCtxShow_ShortKey used to panic: ctx show sliced the first four
// characters of every key.
func TestCtxShow_ShortKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mustSaveConfig(t, exportTestConfig())
	stdout, _, _ := commandOutput(t, func() error { runCtxCommandLine(t, "ctx", "show"); return nil })
	out := stripAnsi(string(stdout))
	if !tableHasRow(out, []string{"tiny", "http://localhost:30083", "json", "****"}) ||
		!tableHasRow(out, []string{"lab", "http://localhost:8080", "table", "Zk3v****"}) {
		t.Errorf("ctx show:\n%s", out)
	}
	if strings.Contains(out, exportLongKey) {
		t.Errorf("ctx show prints a key in clear:\n%s", out)
	}
}
