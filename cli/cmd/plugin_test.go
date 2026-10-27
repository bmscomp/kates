package cmd

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
)

func TestDiscoverPlugins_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if err := os.MkdirAll(filepath.Join(dir, ".kates", "plugins"), 0755); err != nil {
		t.Fatal(err)
	}

	plugins := discoverPlugins()
	for _, p := range plugins {
		if p.path == dir {
			t.Error("should not have discovered any plugins in empty dir")
		}
	}
}

func TestDiscoverPlugins_FindsKatesPrefix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	dir := t.TempDir()
	pluginPath := filepath.Join(dir, "kates-hello")
	if err := os.WriteFile(pluginPath, []byte("#!/bin/sh\necho hello"), 0755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", dir)
	pluginDir := filepath.Join(dir, ".kates", "plugins")
	os.MkdirAll(pluginDir, 0755)
	os.WriteFile(filepath.Join(pluginDir, "kates-hello"), []byte("#!/bin/sh\necho hello"), 0755)

	plugins := discoverPlugins()
	found := false
	for _, p := range plugins {
		if p.name == "hello" {
			found = true
		}
	}
	if !found {
		t.Error("expected to find kates-hello plugin")
	}
}

func TestDiscoverPlugins_IgnoresNonExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	dir := t.TempDir()
	pluginDir := filepath.Join(dir, ".kates", "plugins")
	os.MkdirAll(pluginDir, 0755)
	os.WriteFile(filepath.Join(pluginDir, "kates-noperm"), []byte("#!/bin/sh"), 0644)
	t.Setenv("HOME", dir)

	plugins := discoverPlugins()
	for _, p := range plugins {
		if p.name == "noperm" {
			t.Error("should not find non-executable kates- file")
		}
	}
}

// A kates-version on the PATH used to shadow kates version: registerPlugins
// ran from init, before the built-ins of files sorted after plugin.go, and
// cobra takes the first command of a name.
func TestRegisterPlugins_SkipsBuiltInNames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "")
	pluginDir := filepath.Join(home, ".kates", "plugins")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"kates-version", "kates-ci", "kates-wave1-plugin"} {
		if err := os.WriteFile(filepath.Join(pluginDir, name), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	buf := output.ResetForTesting()
	defer func() { output.Out, output.Err = os.Stdout, os.Stderr }()

	before := len(rootCmd.Commands())
	registerPlugins()
	var added []*cobra.Command
	for _, c := range rootCmd.Commands() {
		if c.Annotations[pluginAnnotation] != "" {
			added = append(added, c)
		}
	}
	t.Cleanup(func() {
		for _, c := range added {
			rootCmd.RemoveCommand(c)
		}
	})

	if len(added) != 1 || added[0].Name() != "wave1-plugin" {
		names := make([]string, 0, len(added))
		for _, c := range added {
			names = append(names, c.Name())
		}
		t.Fatalf("plugins registered = %v, want wave1-plugin alone: version is a built-in and ci an alias of gate", names)
	}
	if got := len(rootCmd.Commands()); got != before+1 {
		t.Errorf("root has %d commands, want %d", got, before+1)
	}
	for _, name := range []string{"version", "ci"} {
		cmd, _, err := rootCmd.Find([]string{name})
		if err != nil || cmd.Annotations[pluginAnnotation] != "" {
			t.Errorf("kates %s resolves to %v (plugin %q), want the built-in", name, cmd, cmd.Annotations[pluginAnnotation])
		}
	}
	for _, want := range []string{"kates-version", "kates-ci", "built-in"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, buf.String())
		}
	}
}

// A plugin's exit code is kates' exit code: "exit status 7" and exit 1 told a
// script nothing it could act on.
func TestRunPlugin_ExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "kates-exit7")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 7\n"), 0755); err != nil {
		t.Fatal(err)
	}
	err := runPlugin(script, nil)
	var exit *exitError
	if !errors.As(err, &exit) || exit.code != 7 || exit.msg != "" {
		t.Fatalf("err = %v (%T), want an exitError with code 7 and no message", err, err)
	}

	ok := filepath.Join(dir, "kates-ok")
	if err := os.WriteFile(ok, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := runPlugin(ok, nil); err != nil {
		t.Errorf("a plugin that exits 0: err = %v", err)
	}
}

func TestPluginExecution_Integration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows")
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "kates-test-plugin")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"plugin-works\""), 0755); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(script).Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "plugin-works\n" {
		t.Errorf("expected 'plugin-works', got %q", string(out))
	}
}

// runEnvPlugin runs kates with a plugin kates-envdump on the PATH that writes
// the KATES_* variables, NO_COLOR and its arguments to a file and exits 3. It
// returns what the plugin saw, nil when it did not run, and kates' output and
// exit code.
func runEnvPlugin(t *testing.T, env []string, args ...string) (seen map[string]string, out string, code int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the plugin is a shell script")
	}
	dir := t.TempDir()
	dump := filepath.Join(dir, "env")
	script := "#!/bin/sh\n{\n" +
		"  echo \"KATES_URL=$KATES_URL\"\n" +
		"  echo \"KATES_API_KEY=$KATES_API_KEY\"\n" +
		"  echo \"KATES_CONTEXT=$KATES_CONTEXT\"\n" +
		"  echo \"KATES_OUTPUT=$KATES_OUTPUT\"\n" +
		"  echo \"KATES_PLAIN=$KATES_PLAIN\"\n" +
		"  echo \"NO_COLOR=$NO_COLOR\"\n" +
		"  echo \"ARGS=$*\"\n" +
		"} > '" + dump + "'\nexit 3\n"
	if err := os.WriteFile(filepath.Join(dir, "kates-envdump"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	env = append([]string{
		"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"KATES_OUTPUT=", "KATES_PLAIN=", "NO_COLOR=",
	}, env...)
	out, code, _ = runKatesCLIConfig(t, func(apiURL string) string {
		return "current-context: lab\ncontexts:\n" +
			"  lab:\n    url: " + apiURL + "\n    api-key: lab-secret-key\n" +
			"  prod:\n    url: https://kates.prod.example\n    api-key: prod-secret-key\n    output: json\n"
	}, env, args...)
	data, err := os.ReadFile(dump)
	if os.IsNotExist(err) {
		return nil, out, code
	}
	if err != nil {
		t.Fatal(err)
	}
	seen = map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		k, v, _ := strings.Cut(line, "=")
		seen[k] = v
	}
	return seen, out, code
}

// A plugin gets the context kates resolved, from the flags before its name,
// the variables and the config, and every argument after its name untouched.
func TestPluginGetsResolvedContext(t *testing.T) {
	seen, out, code := runEnvPlugin(t, nil, "envdump", "a", "--context", "b")
	if code != 3 || seen == nil {
		t.Fatalf("exit %d, plugin ran: %v; want the plugin's exit 3:\n%s", code, seen != nil, out)
	}
	if !strings.HasPrefix(seen["KATES_URL"], "http://127.0.0.1:") || seen["KATES_API_KEY"] != "lab-secret-key" ||
		seen["KATES_CONTEXT"] != "lab" || seen["KATES_OUTPUT"] != "table" || seen["KATES_PLAIN"] != "" || seen["NO_COLOR"] != "" {
		t.Errorf("current context: plugin saw %v", seen)
	}
	if seen["ARGS"] != "a --context b" {
		t.Errorf("args = %q, want the ones after the plugin's name, flags included", seen["ARGS"])
	}

	seen, out, code = runEnvPlugin(t, nil, "--context", "prod", "--plain", "envdump", "-o", "x")
	if code != 3 || seen == nil {
		t.Fatalf("--context prod: exit %d:\n%s", code, out)
	}
	want := map[string]string{
		"KATES_URL": "https://kates.prod.example", "KATES_API_KEY": "prod-secret-key", "KATES_CONTEXT": "prod",
		"KATES_OUTPUT": "json", "KATES_PLAIN": "1", "NO_COLOR": "1", "ARGS": "-o x",
	}
	for k, v := range want {
		if seen[k] != v {
			t.Errorf("--context prod --plain: %s = %q, want %q", k, seen[k], v)
		}
	}
	for _, key := range []string{"lab-secret-key", "prod-secret-key"} {
		if strings.Contains(out, key) {
			t.Errorf("kates printed the API key %s:\n%s", key, out)
		}
	}
}

// What kates resolved wins over what the plugin would have inherited: -o
// over an exported KATES_OUTPUT, and a KATES_URL away from the context's
// server goes without the context's key.
func TestPluginEnvResolvedValuesWin(t *testing.T) {
	seen, out, code := runEnvPlugin(t, []string{"KATES_OUTPUT=json", "KATES_CONTEXT=prod"}, "-o", "table", "--context=lab", "envdump")
	if code != 3 || seen == nil {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if seen["KATES_OUTPUT"] != "table" || seen["KATES_CONTEXT"] != "lab" || seen["KATES_API_KEY"] != "lab-secret-key" {
		t.Errorf("flags over exported variables: plugin saw %v", seen)
	}

	seen, out, code = runEnvPlugin(t, []string{"KATES_URL=http://elsewhere.example:9"}, "envdump")
	if code != 3 || seen == nil {
		t.Fatalf("KATES_URL elsewhere: exit %d:\n%s", code, out)
	}
	if seen["KATES_URL"] != "http://elsewhere.example:9" || seen["KATES_API_KEY"] != "" {
		t.Errorf("KATES_URL away from the context's server: plugin saw %v, want that URL and no key", seen)
	}
}

// A context kates cannot resolve has no URL or key to give: the plugin does
// not run, and kates exits 1 as every API command does.
func TestPluginRefusedForUnknownContext(t *testing.T) {
	seen, out, code := runEnvPlugin(t, nil, "--context", "nope", "envdump")
	if seen != nil || code != 1 || !strings.Contains(out, `context "nope"`) {
		t.Errorf("unknown context: plugin ran %v, exit %d, want not run and 1:\n%s", seen != nil, code, out)
	}
}

func TestSplitPluginArgs(t *testing.T) {
	for _, tt := range []struct {
		line          []string
		globals, rest []string
		ok            bool
	}{
		{[]string{"p", "x"}, nil, []string{"x"}, true},
		{[]string{"--context", "c", "-o", "json", "--plain", "p", "-o", "y"}, []string{"--context", "c", "-o", "json", "--plain"}, []string{"-o", "y"}, true},
		{[]string{"--url=http://h", "-ojson", "p"}, []string{"--url=http://h", "-ojson"}, nil, true},
		{[]string{"--verbose", "p"}, nil, nil, false},
		{[]string{"other", "p"}, nil, nil, false},
		{[]string{"--context", "p"}, nil, nil, false},
	} {
		g, r, ok := splitPluginArgs(tt.line, "p")
		if ok != tt.ok || strings.Join(g, " ") != strings.Join(tt.globals, " ") || strings.Join(r, " ") != strings.Join(tt.rest, " ") {
			t.Errorf("splitPluginArgs(%q) = %q, %q, %v; want %q, %q, %v", tt.line, g, r, ok, tt.globals, tt.rest, tt.ok)
		}
	}
}
