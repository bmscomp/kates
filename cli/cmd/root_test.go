package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bmscomp/kates/cli/output"
)

// TestSaveConfig_FileMode checks that ~/.kates.yaml, which holds API keys, is
// readable by its owner only: when saveConfig creates it, and when it rewrites
// one an older kates left at 0644.
func TestSaveConfig_FileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	tests := []struct {
		name     string
		existing os.FileMode // 0 when the file does not exist yet
	}{
		{"new file", 0},
		{"existing 0644 file is tightened", 0o644},
		{"existing 0666 file is tightened", 0o666},
		{"existing 0600 file stays 0600", 0o600},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			path := filepath.Join(home, ".kates.yaml")
			if tt.existing != 0 {
				if err := os.WriteFile(path, []byte("current-context: old\n"), tt.existing); err != nil {
					t.Fatal(err)
				}
				// WriteFile's mode passes through the umask; set it exactly.
				if err := os.Chmod(path, tt.existing); err != nil {
					t.Fatal(err)
				}
			}

			cfg := Config{CurrentContext: "lab", Contexts: map[string]Context{
				"lab": {URL: "http://localhost:8080", APIKey: "secret-key"},
			}}
			if err := saveConfig(cfg); err != nil {
				t.Fatalf("saveConfig: %v", err)
			}

			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != 0o600 {
				t.Errorf("mode = %04o, want 0600", got)
			}
			if got := loadConfig(); got.CurrentContext != "lab" || got.Contexts["lab"].APIKey != "secret-key" {
				t.Errorf("reloaded config = %+v, want what was saved", got)
			}
		})
	}
}

// TestSaveConfig_KeepsSymlink checks that a config kept elsewhere and linked
// into HOME (a dotfiles checkout) stays a link, and that its target is the
// file tightened to 0600.
func TestSaveConfig_KeepsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlinks and permission bits")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	target := filepath.Join(t.TempDir(), "kates.yaml")
	if err := os.WriteFile(target, []byte("current-context: old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".kates.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := saveConfig(Config{CurrentContext: "lab", Contexts: map[string]Context{"lab": {URL: "u"}}}); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("~/.kates.yaml is no longer a symlink (err %v)", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("target mode = %04o, want 0600", got)
	}
	if data, _ := os.ReadFile(target); !strings.Contains(string(data), "current-context: lab") {
		t.Errorf("target not rewritten:\n%s", data)
	}
}

// TestSaveConfig_KeepsDanglingSymlink checks that a link whose target does
// not exist yet (a dotfiles checkout without the file) stays a link, and the
// first save creates the file it points to.
func TestSaveConfig_KeepsDanglingSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlinks")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	target := filepath.Join(t.TempDir(), "kates.yaml")
	link := filepath.Join(home, ".kates.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := saveConfig(Config{CurrentContext: "lab", Contexts: map[string]Context{"lab": {URL: "u"}}}); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}

	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("~/.kates.yaml is no longer a symlink (err %v)", err)
	}
	if got := loadConfig().CurrentContext; got != "lab" {
		t.Errorf("current context through the link = %q, want lab", got)
	}
}

// TestSaveConfig_FailureKeepsOldFile checks that a save that cannot complete
// leaves the config as it was. The file used to be emptied before the new
// contents were written, so a failure part way lost every context.
func TestSaveConfig_FailureKeepsOldFile(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions that bind the user")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	old := Config{CurrentContext: "lab", Contexts: map[string]Context{"lab": {URL: "u", APIKey: "old-key"}}}
	if err := saveConfig(old); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(home, ".kates.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	// No new file can be created beside the config.
	if err := os.Chmod(home, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0o700) })

	if err := saveConfig(Config{CurrentContext: "other", Contexts: map[string]Context{}}); err == nil {
		t.Fatal("saveConfig into a read-only directory succeeded, want an error")
	}
	after, err := os.ReadFile(filepath.Join(home, ".kates.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("config changed by a failed save:\n got  %s\n want %s", after, before)
	}
}

// TestSaveConfig_RespectsAReadOnlyFile checks that a config made read-only
// is not replaced. The rename that writes the file needs write access to the
// directory only, so saveConfig checks the file itself first.
func TestSaveConfig_RespectsAReadOnlyFile(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions that bind the user")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".kates.yaml")
	const pinned = "current-context: lab\n"
	if err := os.WriteFile(path, []byte(pinned), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}

	if err := saveConfig(Config{CurrentContext: "other", Contexts: map[string]Context{}}); err == nil {
		t.Error("saveConfig over a read-only config succeeded, want an error")
	}
	if data, _ := os.ReadFile(path); string(data) != pinned {
		t.Errorf("read-only config rewritten:\n%s", data)
	}
}

// TestSaveConfig_LeavesNoTemporaryFiles checks that a save leaves only the
// config and its lock file beside it.
func TestSaveConfig_LeavesNoTemporaryFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for i := range 3 {
		if err := saveConfig(Config{CurrentContext: "lab", Contexts: map[string]Context{"lab": {URL: strconv.Itoa(i)}}}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if want := []string{".kates.yaml", ".kates.yaml.lock"}; !reflect.DeepEqual(names, want) {
		t.Errorf("files in HOME = %q, want %q", names, want)
	}
}

// TestUpdateConfig_SerialisesWriters runs many updates at once, as several
// kates processes would, and checks that none is lost: each reads the config
// under the lock the others write it under.
func TestUpdateConfig_SerialisesWriters(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const writers = 20
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("ctx-%02d", i)
			err := updateConfig(func(cfg *Config) error {
				cfg.Contexts[name] = Context{URL: "http://" + name}
				return nil
			})
			if err != nil {
				t.Errorf("update %s: %v", name, err)
			}
		}()
	}
	wg.Wait()

	cfg := loadConfig()
	for i := range writers {
		if name := fmt.Sprintf("ctx-%02d", i); cfg.Contexts[name].URL != "http://"+name {
			t.Errorf("context %s lost", name)
		}
	}
}

// TestUpdateConfig_WaitsForTheLock checks that an update waits for another
// holder of the config lock, and gives up with an error that names the lock
// file when the holder keeps it too long.
func TestUpdateConfig_WaitsForTheLock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	orig := configLockWait
	t.Cleanup(func() { configLockWait = orig })
	configLockWait = 100 * time.Millisecond

	unlock, err := lockConfig()
	if err != nil {
		t.Fatal(err)
	}
	add := func(cfg *Config) error {
		cfg.Contexts["lab"] = Context{URL: "u"}
		return nil
	}
	err = updateConfig(add)
	if err == nil || !strings.Contains(err.Error(), ".kates.yaml.lock") {
		t.Errorf("update under a held lock: err = %v, want one naming the lock file", err)
	}
	if _, ok := loadConfig().Contexts["lab"]; ok {
		t.Error("update under a held lock was written")
	}

	configLockWait = 5 * time.Second
	go func() {
		time.Sleep(100 * time.Millisecond)
		unlock()
	}()
	if err := updateConfig(add); err != nil {
		t.Fatalf("update once the lock is released: %v", err)
	}
	if _, ok := loadConfig().Contexts["lab"]; !ok {
		t.Error("update once the lock is released was not written")
	}
}

// TestUpdateConfig_RefusesAConfigItCannotLoad checks that an update does not
// save the defaults over a config that fails to parse, which would lose every
// context in it.
func TestUpdateConfig_RefusesAConfigItCannotLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".kates.yaml")
	broken := "current-context: lab\ncontexts:\n  lab: [not, a, context\n"
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}

	err := updateConfig(func(cfg *Config) error {
		cfg.Contexts["other"] = Context{URL: "u"}
		return nil
	})
	if err == nil {
		t.Fatal("update over an unparseable config succeeded, want an error")
	}
	if data, _ := os.ReadFile(path); string(data) != broken {
		t.Errorf("config rewritten:\n%s", data)
	}
}

// TestUpdateConfig_Unchanged checks that a change returning
// errConfigUnchanged writes nothing, not even a file that did not exist.
func TestUpdateConfig_Unchanged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	err := updateConfig(func(cfg *Config) error { return errConfigUnchanged })
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".kates.yaml")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("config file exists after an unchanged update (stat err %v)", err)
	}
}

// TestConfig_KeySourceIsBackwardCompatible checks both directions of the
// key-source field: a config written before it existed loads with every key
// counted as the user's, and the field is written only for keys that carry it,
// so the file an older kates reads back looks as it always did.
func TestConfig_KeySourceIsBackwardCompatible(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	legacy := `current-context: local
contexts:
  local:
    url: http://localhost:8080
    output: table
    api-key: legacy-key
`
	path := filepath.Join(home, ".kates.yaml")
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := loadConfig()
	if got := cfg.Contexts["local"]; got.APIKey != "legacy-key" || got.KeySource != "" {
		t.Fatalf("legacy context = %+v, want its key with no key-source", got)
	}

	cfg.Contexts[portsContextName] = Context{URL: "http://localhost:8080", APIKey: "k", KeySource: secretKeySource("k")}
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "key-source:"); n != 1 {
		t.Errorf("key-source written %d times, want once (only for ports):\n%s", n, data)
	}
	if got := loadConfig().Contexts[portsContextName].KeySource; got != secretKeySource("k") {
		t.Errorf("ports key-source after reload = %q, want %q", got, secretKeySource("k"))
	}
}

// TestCtxImport_ClearsKeySource checks that an imported context never carries
// a key-source: a key that arrives in a file was not stored by kates ports on
// this machine, so kates ports must treat it as the user's.
func TestCtxImport_ClearsKeySource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	file := filepath.Join(t.TempDir(), "contexts.yaml")
	incoming := `contexts:
  ports:
    url: http://localhost:8080
    api-key: imported-key
    key-source: kates-ports
`
	if err := os.WriteFile(file, []byte(incoming), 0o600); err != nil {
		t.Fatal(err)
	}
	origFile, origOut, origErr := ctxImportFile, output.Out, output.Err
	t.Cleanup(func() { ctxImportFile, output.Out, output.Err = origFile, origOut, origErr })
	ctxImportFile = file
	output.ResetForTesting()
	ctxImportCmd.Run(ctxImportCmd, nil)

	got := loadConfig().Contexts[portsContextName]
	if got.APIKey != "imported-key" || got.KeySource != "" {
		t.Errorf("imported ports context = %+v, want the key with no key-source", got)
	}
}

// TestCtxCommands runs the context commands that change the config, which now
// read it under the config lock (updateConfig), and checks each one's effect
// on the file and its report, including a name the config does not have.
func TestCtxCommands(t *testing.T) {
	start := Config{CurrentContext: "local", Contexts: map[string]Context{
		"local": {URL: "http://localhost:8080", Output: "table", APIKey: "local-key"},
		"mcp":   {URL: "http://localhost:8080", Output: "json", APIKey: "agent-key"},
	}}
	tests := []struct {
		name        string
		run         func() error
		wantCurrent string
		wantNames   []string
		wantOut     string
		unchanged   bool // the file must be left byte for byte as it was
	}{
		{
			name: "set adds a context without switching to it",
			run: func() error {
				ctxSetURL, ctxSetAPIKey = "https://lab.example.com", "lab-key"
				return ctxSetCmd.RunE(ctxSetCmd, []string{"lab"})
			},
			wantCurrent: "local", wantNames: []string{"default", "lab", "local", "mcp"},
			wantOut: "Context 'lab' → https://lab.example.com",
		},
		{
			name:        "use switches the current context",
			run:         func() error { ctxUseCmd.Run(ctxUseCmd, []string{"mcp"}); return nil },
			wantCurrent: "mcp", wantNames: []string{"default", "local", "mcp"},
			wantOut: "Switched to 'mcp' → http://localhost:8080",
		},
		{
			name:        "use of a missing context changes nothing",
			run:         func() error { ctxUseCmd.Run(ctxUseCmd, []string{"nope"}); return nil },
			wantCurrent: "local", wantNames: []string{"default", "local", "mcp"},
			wantOut: "Context 'nope' not found", unchanged: true,
		},
		{
			name:        "delete of the current context picks another",
			run:         func() error { ctxDeleteCmd.Run(ctxDeleteCmd, []string{"local"}); return nil },
			wantNames:   []string{"default", "mcp"},
			wantOut:     "Context 'local' deleted",
			wantCurrent: "", // any remaining context; checked below
		},
		{
			name:        "delete of a missing context changes nothing",
			run:         func() error { ctxDeleteCmd.Run(ctxDeleteCmd, []string{"nope"}); return nil },
			wantCurrent: "local", wantNames: []string{"default", "local", "mcp"},
			wantOut: "Context 'nope' not found", unchanged: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			origURL, origKey, origOut, origErr := ctxSetURL, ctxSetAPIKey, output.Out, output.Err
			t.Cleanup(func() { ctxSetURL, ctxSetAPIKey, output.Out, output.Err = origURL, origKey, origOut, origErr })
			buf := output.ResetForTesting()
			mustSaveConfig(t, start)
			before, err := os.ReadFile(filepath.Join(home, ".kates.yaml"))
			if err != nil {
				t.Fatal(err)
			}

			if err := tt.run(); err != nil {
				t.Fatalf("command: %v", err)
			}

			if out := stripAnsi(buf.String()); !strings.Contains(out, tt.wantOut) {
				t.Errorf("output lacks %q:\n%s", tt.wantOut, out)
			}
			after, err := os.ReadFile(filepath.Join(home, ".kates.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			if tt.unchanged && string(after) != string(before) {
				t.Errorf("config changed:\n got  %s\n want %s", after, before)
			}
			cfg := loadConfig()
			var names []string
			for name := range cfg.Contexts {
				names = append(names, name)
			}
			sort.Strings(names)
			if !reflect.DeepEqual(names, tt.wantNames) {
				t.Errorf("contexts = %v, want %v", names, tt.wantNames)
			}
			switch {
			case tt.wantCurrent != "" && cfg.CurrentContext != tt.wantCurrent:
				t.Errorf("current context = %q, want %q", cfg.CurrentContext, tt.wantCurrent)
			case tt.wantCurrent == "" && cfg.Contexts[cfg.CurrentContext].URL == "":
				t.Errorf("current context = %q, want one that exists", cfg.CurrentContext)
			}
			if got := cfg.Contexts["mcp"]; got != start.Contexts["mcp"] {
				t.Errorf("mcp context = %+v, want it unchanged", got)
			}
		})
	}
}
