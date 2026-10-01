package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// runInit runs `kates init` with args against a home of its own, scaffolding
// into dir, and puts init's flags back to their defaults afterwards: values
// and Changed both stay set between runCommandLine calls otherwise.
func runInit(t *testing.T, dir string, args ...string) (stdout []byte, stderr string, err error) {
	t.Helper()
	defer func() {
		for _, name := range []string{"name", "url", "dir", "no-scenarios", "no-ci"} {
			f := initCmd.Flags().Lookup(name)
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		}
	}()
	line := append([]string{"init", "--dir", dir}, args...)
	return commandOutput(t, func() error { return runCommandLine(t, line...) })
}

func writeKatesConfig(t *testing.T, home string, cfg Config) string {
	t.Helper()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".kates.yaml")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readKatesConfig(t *testing.T, path string) Config {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return cfg
}

// kates init in a second project used to replace ~/.kates.yaml with the one
// context it made, deleting every other context and its API key.
func TestInit_KeepsOtherContexts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := writeKatesConfig(t, home, Config{CurrentContext: "lab", Contexts: map[string]Context{
		"prod": {URL: "https://kates.example.com", Output: "table", APIKey: "prod-key"},
		"lab":  {URL: "http://localhost:30083", Output: "json"},
	}})

	if _, stderr, err := runInit(t, t.TempDir(), "--name", "staging", "--url", "http://staging:8080", "--no-scenarios", "--no-ci"); err != nil {
		t.Fatalf("init: %v\n%s", err, stderr)
	}

	got := readKatesConfig(t, path)
	want := Config{CurrentContext: "staging", Contexts: map[string]Context{
		"prod":    {URL: "https://kates.example.com", Output: "table", APIKey: "prod-key"},
		"lab":     {URL: "http://localhost:30083", Output: "json"},
		"staging": {URL: "http://staging:8080", Output: "table"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("config = %+v\nwant     %+v", got, want)
	}
}

// A context of the same name is kept, API key and all, and the CI script
// points where it does.
func TestInit_KeepsAContextOfTheSameName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	before := Config{CurrentContext: "lab", Contexts: map[string]Context{
		"default": {URL: "https://kates.example.com", Output: "table", APIKey: "prod-key"},
		"lab":     {URL: "http://localhost:30083"},
	}}
	path := writeKatesConfig(t, home, before)
	dir := t.TempDir()

	if _, stderr, err := runInit(t, dir, "--no-scenarios"); err != nil {
		t.Fatalf("init: %v\n%s", err, stderr)
	}

	got := readKatesConfig(t, path)
	if !reflect.DeepEqual(got.Contexts, before.Contexts) || got.CurrentContext != "default" {
		t.Errorf("config = %+v; want the same contexts, with default current", got)
	}
	script, err := os.ReadFile(filepath.Join(dir, "kates-ci.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), `KATES_URL="${KATES_URL:-https://kates.example.com}"`) {
		t.Errorf("kates-ci.sh does not default to the context's URL:\n%s", script)
	}
}

// Pointing an existing context at another server would send its API key
// there; init refuses and leaves the file as it was.
func TestInit_RefusesToRepointAContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := writeKatesConfig(t, home, Config{CurrentContext: "default", Contexts: map[string]Context{
		"default": {URL: "https://kates.example.com", APIKey: "prod-key"},
	}})
	before, _ := os.ReadFile(path)
	dir := t.TempDir()

	_, stderr, err := runInit(t, dir, "--url", "http://localhost:8080")
	if _, ok := err.(*silentErr); !ok {
		t.Fatalf("err = %v (%T), want a silentErr, which exits 1", err, err)
	}
	if !strings.Contains(stderr, "kates ctx set default --url http://localhost:8080") {
		t.Errorf("the refusal does not say how to change the context:\n%s", stderr)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Errorf("config changed:\n%s", after)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("scaffolded %d files after refusing", len(entries))
	}
}

// With no config, init writes the one context it makes, as it always did,
// without the built-in default beside it.
func TestInit_NewConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if _, stderr, err := runInit(t, t.TempDir(), "--name", "lab", "--url", "http://localhost:30083", "--no-scenarios", "--no-ci"); err != nil {
		t.Fatalf("init: %v\n%s", err, stderr)
	}

	got := readKatesConfig(t, filepath.Join(home, ".kates.yaml"))
	want := Config{CurrentContext: "lab", Contexts: map[string]Context{"lab": {URL: "http://localhost:30083", Output: "table"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("config = %+v, want %+v", got, want)
	}
}

// A config init cannot read is left alone; init used to write over it.
func TestInit_LeavesAnUnreadableConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".kates.yaml")
	broken := "contexts: [this is not a map\n"
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := runInit(t, t.TempDir(), "--no-scenarios", "--no-ci"); err == nil {
		t.Error("init succeeded over a config it cannot read")
	}
	if after, _ := os.ReadFile(path); string(after) != broken {
		t.Errorf("config rewritten:\n%s", after)
	}
}
