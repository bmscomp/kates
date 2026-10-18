package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
)

// Every top-level command must say which profiles enable it, so a new
// command cannot slip past them.
func TestCommandProfilesCoverEveryCommand(t *testing.T) {
	registered := map[string]bool{}
	for _, c := range rootCmd.Commands() {
		if c.Annotations[pluginAnnotation] != "" || strings.HasPrefix(c.Name(), "__") {
			continue
		}
		registered[c.Name()] = true
		if _, ok := commandProfiles[c.Name()]; !ok {
			t.Errorf("kates %s is not in commandProfiles", c.Name())
		}
	}
	for name, profiles := range commandProfiles {
		// cobra adds help and completion only when kates runs.
		if !registered[name] && name != "help" && name != "completion" {
			t.Errorf("commandProfiles names %q, which is not a kates command", name)
		}
		for _, p := range profiles {
			if !slices.Contains(knownProfiles, p) {
				t.Errorf("kates %s names unknown profile %q", name, p)
			}
		}
	}
}

func TestActiveProfiles(t *testing.T) {
	for _, tt := range []struct {
		name, env         string
		setEnv            bool
		stored            []string
		want              []string
		wantFrom, wantErr string
	}{
		{name: "none enables everything", wantFrom: `context "lab"`},
		{name: "stored, in profile order", stored: []string{"deploy", "chaos"}, want: []string{"chaos", "deploy"}, wantFrom: `context "lab"`},
		{name: "unknown kept, after the known, with a warning", stored: []string{"chaso", "chaos"}, want: []string{"chaos", "chaso"}, wantErr: `Unknown profile "chaso"`},
		{name: "only unknown names enable nothing", stored: []string{"perf"}, want: []string{"perf"}, wantErr: `Unknown profile "perf"`},
		{name: "all enables everything", stored: []string{"chaos", "all"}},
		{name: "KATES_PROFILES overrides", stored: []string{"chaos"}, setEnv: true, env: "topology, security",
			want: []string{"topology", "security"}, wantFrom: "KATES_PROFILES"},
		{name: "empty KATES_PROFILES enables everything", stored: []string{"chaos"}, setEnv: true, wantFrom: "KATES_PROFILES"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				t.Setenv("KATES_PROFILES", tt.env)
			} else {
				unsetEnv(t, "KATES_PROFILES")
			}
			prevOut, prevErr, prevWarned := output.Out, output.Err, profilesWarned
			t.Cleanup(func() { output.Out, output.Err, profilesWarned = prevOut, prevErr, prevWarned })
			buf := output.ResetForTesting()
			profilesWarned = false

			got, from := activeProfiles("lab", Context{Profiles: tt.stored})
			_, _ = activeProfiles("lab", Context{Profiles: tt.stored})
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("profiles = %q, want %q", got, tt.want)
			}
			if tt.wantFrom != "" && from != tt.wantFrom {
				t.Errorf("from = %q, want %q", from, tt.wantFrom)
			}
			if !strings.Contains(buf.String(), tt.wantErr) {
				t.Errorf("stderr %q lacks %q", buf.String(), tt.wantErr)
			}
			if tt.wantErr != "" && strings.Count(buf.String(), tt.wantErr) != 1 {
				t.Errorf("warned %d times, want once: %q", strings.Count(buf.String(), tt.wantErr), buf.String())
			}
		})
	}
}

func TestProfileRefusal(t *testing.T) {
	find := func(args ...string) *cobra.Command {
		c, _, err := rootCmd.Find(args)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	for _, tt := range []struct {
		name      string
		cmd       []string
		profiles  []string
		from      string
		flag      string
		wantRefer []string // empty: the command may run
	}{
		{name: "no profiles run everything", cmd: []string{"tune", "types"}},
		{name: "an enabled command runs", cmd: []string{"disruption", "run"}, profiles: []string{"chaos"}},
		{name: "one of several profiles is enough", cmd: []string{"test", "create"}, profiles: []string{"integrity"}},
		{name: "always enabled", cmd: []string{"ctx", "profiles", "set"}, profiles: []string{"chaos"}},
		{name: "a subcommand follows its parent", cmd: []string{"tune", "types"}, profiles: []string{"chaos", "deploy"},
			wantRefer: []string{
				"kates tune needs the performance profile\n",
				`Enabled:    chaos, deploy (context "lab")`,
				"Enable it:  kates ctx profiles set performance chaos deploy",
				"Run once:   KATES_PROFILES=all kates tune types",
			}},
		{name: "several profiles named", cmd: []string{"kafka", "topics"}, profiles: []string{"chaos"},
			wantRefer: []string{"kates kafka needs the topology or integrity profile"}},
		{name: "--context is kept in the fix", cmd: []string{"security", "audit"}, profiles: []string{"chaos"}, flag: "lab",
			wantRefer: []string{"kates ctx profiles set chaos security --context lab", "KATES_PROFILES=all kates security audit --context lab"}},
		{name: "only unknown profiles", cmd: []string{"tune", "types"}, profiles: []string{"perf"},
			wantRefer: []string{"Enabled:    perf (context", "Enable it:  kates ctx profiles set performance\n"}},
		{name: "from KATES_PROFILES", cmd: []string{"deploy"}, profiles: []string{"chaos"}, from: "KATES_PROFILES",
			wantRefer: []string{"Enabled:    chaos (KATES_PROFILES)", "Enable it:  add deploy to KATES_PROFILES\n"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			prevFlag, prevEnv, prevOut, prevErr := contextFlag, contextFromEnv, output.Out, output.Err
			t.Cleanup(func() { contextFlag, contextFromEnv, output.Out, output.Err = prevFlag, prevEnv, prevOut, prevErr })
			contextFlag, contextFromEnv = tt.flag, false
			buf := output.ResetForTesting()
			var stdout bytes.Buffer
			output.Out = &stdout
			from := tt.from
			if from == "" {
				from = `context "lab"`
			}

			err := profileRefusal(find(tt.cmd...), "lab", tt.profiles, from)
			if len(tt.wantRefer) == 0 {
				if err != nil {
					t.Fatalf("refused: %v\n%s", err, buf)
				}
				return
			}
			if err == nil {
				t.Fatal("ran; want it refused")
			}
			for _, want := range tt.wantRefer {
				if out := stripAnsi(buf.String()); !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
			if stdout.Len() > 0 {
				t.Errorf("a refusal wrote to stdout, which -o json reads: %q", stdout.String())
			}
		})
	}
}

func TestProfileHelp(t *testing.T) {
	prevFlag, prevEnv := contextFlag, contextFromEnv
	t.Cleanup(func() { contextFlag, contextFromEnv = prevFlag, prevEnv })
	contextFlag, contextFromEnv = "", false
	all := profileHelp(helpTemplate, "lab", nil, `context "lab"`)
	if all != helpTemplate {
		t.Errorf("help without profiles is not the help as written:\n%s", all)
	}

	chaos := profileHelp(helpTemplate, "lab", []string{"chaos"}, `context "lab"`)
	for _, want := range []string{
		"\n  Profiles:      chaos (context \"lab\")\n  Change them:   kates ctx profiles set\n  Every command: KATES_PROFILES=all kates\n",
		"trend analysis for Apache Kafka", "  disruption ", "  test create ",
		"  ctx profiles ", "  KATES_PROFILES ", "$ kates health", "Flags:",
	} {
		if !strings.Contains(chaos, want) {
			t.Errorf("chaos help lacks %q", want)
		}
	}
	for _, gone := range []string{"  tune ", "  lab ", "$ kates lab", "Security:", "Deployment:", "  kyverno "} {
		if strings.Contains(chaos, gone) {
			t.Errorf("chaos help still has %q", gone)
		}
	}

	env := profileHelp(helpTemplate, "lab", []string{"deploy"}, "KATES_PROFILES")
	if !strings.Contains(env, "Profiles:      deploy (KATES_PROFILES)") || !strings.Contains(env, "edit or unset KATES_PROFILES") ||
		strings.Contains(env, `context "lab"`) {
		t.Errorf("help credits KATES_PROFILES to the context:\n%s", env)
	}

	contextFlag = "sec"
	if other := profileHelp(helpTemplate, "sec", []string{"chaos"}, `context "sec"`); !strings.Contains(other, "kates ctx profiles set --context sec") {
		t.Errorf("help under --context points at the current context:\n%s", other)
	}
	for _, help := range []string{all, chaos, env} {
		header := help[:strings.Index(help, "Quick Start:")]
		for _, line := range strings.Split(header, "\n") {
			if w := len([]rune(line)); w > 80 {
				t.Errorf("header line is %d columns: %q", w, line)
			}
		}
	}
}

func TestCtxProfilesSet(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	unsetEnv(t, "KATES_PROFILES")
	prevActive, prevURL, prevKey, prevOut, prevErr := activeContext, ctxSetURL, ctxSetAPIKey, output.Out, output.Err
	t.Cleanup(func() {
		activeContext, ctxSetURL, ctxSetAPIKey, output.Out, output.Err = prevActive, prevURL, prevKey, prevOut, prevErr
	})
	output.ResetForTesting()
	mustSaveConfig(t, Config{CurrentContext: "lab", Contexts: map[string]Context{
		"lab": {URL: "http://localhost:30083", APIKey: "lab-key"},
	}})
	activeContext = "lab"
	stored := func() Context { return loadConfig().Contexts["lab"] }

	if err := ctxProfilesSetCmd.RunE(ctxProfilesSetCmd, []string{"topology,chaos"}); err != nil {
		t.Fatal(err)
	}
	if got := stored(); !reflect.DeepEqual(got.Profiles, []string{"chaos", "topology"}) || got.APIKey != "lab-key" {
		t.Errorf("after set = %+v, want chaos, topology and the key kept", got)
	}

	for _, bad := range [][]string{{"chaso"}, {"all", "chaos"}, {""}, {","}} {
		if err := ctxProfilesSetCmd.RunE(ctxProfilesSetCmd, bad); err == nil {
			t.Errorf("set %q succeeded; want it refused", bad)
		}
	}
	if got := stored().Profiles; !reflect.DeepEqual(got, []string{"chaos", "topology"}) {
		t.Errorf("a refused set changed the profiles to %q", got)
	}

	ctxSetURL, ctxSetAPIKey = "http://localhost:8080", ""
	if err := ctxSetCmd.RunE(ctxSetCmd, []string{"lab"}); err != nil {
		t.Fatal(err)
	}
	if got := stored().Profiles; !reflect.DeepEqual(got, []string{"chaos", "topology"}) {
		t.Errorf("kates ctx set dropped the profiles: %q", got)
	}

	if err := ctxProfilesSetCmd.RunE(ctxProfilesSetCmd, []string{"all"}); err != nil {
		t.Fatal(err)
	}
	if got := stored().Profiles; got != nil {
		t.Errorf("after set all = %q, want none", got)
	}

	activeContext = "gone"
	if err := ctxProfilesSetCmd.RunE(ctxProfilesSetCmd, []string{"chaos"}); err == nil {
		t.Error("set on a context the config lacks succeeded")
	}
}

func TestProfilesRefuseAndHideEndToEnd(t *testing.T) {
	out, code, requests := runKatesCLI(t, []string{"KATES_PROFILES=chaos"}, "test", "list")
	if code != 0 || requests == 0 {
		t.Errorf("kates test list under chaos: exit %d, %d requests\n%s", code, requests, out)
	}
	out, code, requests = runKatesCLI(t, []string{"KATES_PROFILES=chaos"}, "tune", "types")
	if code != 1 || requests != 0 || !strings.Contains(out, "kates tune needs the performance profile") {
		t.Errorf("kates tune types under chaos: exit %d, %d requests\n%s", code, requests, out)
	}

	out, code, _ = runKatesCLI(t, []string{"KATES_PROFILES=chaos"}, "tune", "--help")
	out = stripAnsi(out)
	if code != 0 || !strings.Contains(out, "Available Commands:") || !strings.Contains(out, "! kates tune needs the performance profile") {
		t.Errorf("kates tune --help under chaos: exit %d\n%s", code, out)
	}

	out, code, _ = runKatesCLI(t, []string{"KATES_PROFILES=performance"}, "tldr")
	out = stripAnsi(out)
	if code != 0 || strings.Contains(out, "kates kafka") || strings.Contains(out, "Kafka Operations") ||
		!strings.Contains(out, "kates report diff") || !strings.Contains(out, "Profiles: performance (KATES_PROFILES)") ||
		!strings.Contains(out, "\n  Quick Start\n") {
		t.Errorf("kates tldr under performance: exit %d\n%s", code, out)
	}

	out, code, _ = runKatesCLI(t, []string{"KATES_PROFILES=topology"}, "__complete", "t")
	if code != 0 {
		t.Fatalf("completion: exit %d\n%s", code, out)
	}
	offered := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		offered[strings.SplitN(line, "\t", 2)[0]] = true
	}
	if !offered["tldr"] || offered["tune"] || offered["test"] {
		t.Errorf("completion under topology offered:\n%s", out)
	}

	// Completion hides by the context the command line names.
	home := t.TempDir()
	config := "current-context: lab\ncontexts:\n  lab:\n    url: http://127.0.0.1:9\n    profiles: [chaos]\n" +
		"  sec:\n    url: http://127.0.0.1:9\n    profiles: [security]\n"
	if err := os.WriteFile(filepath.Join(home, ".kates.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		args []string
		want bool
	}{
		{[]string{"__complete", "sec"}, false},
		{[]string{"__complete", "--context", "sec", "sec"}, true},
		{[]string{"__complete", "--context=sec", "sec"}, true},
	} {
		out, _, _ := runKatesCLI(t, []string{"KATES_TEST_HOME=" + home}, tt.args...)
		if got := strings.Contains(out, "security"); got != tt.want {
			t.Errorf("kates %s offered security = %v, want %v:\n%s", strings.Join(tt.args, " "), got, tt.want, out)
		}
	}
}

// unsetEnv unsets key for the test and restores it afterwards.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	os.Unsetenv(key)
}

// runCtxProfiles runs kates ctx profiles with args against a config whose
// context lab enables chaos and topology, and returns its output, colours
// stripped.
func runCtxProfiles(t *testing.T, mode string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	unsetEnv(t, "KATES_PROFILES")
	prevActive, prevMode, prevOut, prevErr := activeContext, outputMode, output.Out, output.Err
	t.Cleanup(func() { activeContext, outputMode, output.Out, output.Err = prevActive, prevMode, prevOut, prevErr })
	mustSaveConfig(t, Config{CurrentContext: "lab", Contexts: map[string]Context{
		"lab": {URL: "http://localhost:30083", Profiles: []string{"chaos", "topology"}},
	}})
	activeContext, outputMode = "lab", mode
	buf := output.ResetForTesting()
	err := ctxProfilesCmd.RunE(ctxProfilesCmd, args)
	return stripAnsi(buf.String()), err
}

// The commands of every profile line up in the same columns: each cell
// starts at profileIndent plus a whole number of cells, and holds one command.
func TestCtxProfilesLayout(t *testing.T) {
	out, err := runCtxProfiles(t, "")
	if err != nil {
		t.Fatal(err)
	}
	cell, seen := commandCellWidth(), map[string]int{}
	lead := regexp.MustCompile(`^  (. \S+|  always) +$`)
	for _, line := range strings.Split(out, "\n") {
		r := []rune(line)
		if len(r) <= profileIndent || !lead.MatchString(string(r[:profileIndent])) && strings.TrimSpace(string(r[:profileIndent])) != "" {
			continue
		}
		if label := strings.TrimSpace(line); strings.HasPrefix(label, "Details:") || strings.HasPrefix(label, "Change") || strings.HasPrefix(label, "Every") {
			if i := strings.Index(line, "kates ctx"); i != profileIndent {
				t.Errorf("hint command starts at column %d, the grid at %d: %q", i, profileIndent, line)
			}
			continue
		}
		rest := r[profileIndent:]
		for start := 0; start < len(rest); start += cell {
			text := strings.TrimSpace(string(rest[start:min(start+cell, len(rest))]))
			if rest[start] == ' ' || commandProfiles[text] == nil && !slices.Contains(profileCommands(""), text) {
				t.Errorf("cell at column %d of %q is %q, not a command starting there", profileIndent+start, line, text)
			}
			seen[text]++
		}
	}
	for _, p := range append(slices.Clone(knownProfiles), "") {
		for _, c := range profileCommands(p) {
			if seen[c] == 0 {
				t.Errorf("%s is missing from the profile list", c)
			}
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "  Context ") && strings.Index(line, "lab") != profileIndent {
			t.Errorf("the Context value starts at column %d, the grid at %d: %q", strings.Index(line, "lab"), profileIndent, line)
		}
	}
	on, off := output.Glyphs().Bullet, output.Glyphs().Ring
	for _, want := range []string{"  " + on + " chaos ", "  " + on + " topology ", "  " + off + " performance ", "    always "} {
		if !strings.Contains(out, want) {
			t.Errorf("profile list lacks %q:\n%s", want, out)
		}
	}
}

func TestCtxProfilesDetails(t *testing.T) {
	out, err := runCtxProfiles(t, "", "topology")
	if err != nil {
		t.Fatal(err)
	}
	pad := strings.Repeat(" ", profileIndent)
	for _, want := range []string{
		"  " + output.Glyphs().Bullet + " topology         enabled",
		pad + "cluster" + strings.Repeat(" ", commandCellWidth()-len("cluster")) + clusterCmd.Short[:10],
		pad + "snapshot ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("details lack %q:\n%s", want, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, pad+"cluster ") && !strings.HasSuffix(line, "  (also chaos)") {
			t.Errorf("the cluster line lost its other profiles: %q", line)
		}
	}
	if strings.Contains(out, "performance") {
		t.Errorf("details of topology show another profile:\n%s", out)
	}
	if _, err := runCtxProfiles(t, "", "chaso"); err == nil {
		t.Error("an unknown profile was shown")
	}

	out, err = runCtxProfiles(t, "json", "topology")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Profiles  []string `json:"profiles"`
		Available []struct {
			Profile  string `json:"profile"`
			Enabled  bool   `json:"enabled"`
			Commands []struct {
				Name, Description string
				AlsoIn            []string `json:"alsoIn"`
			} `json:"commands"`
		} `json:"available"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(got.Available) != 1 || got.Available[0].Profile != "topology" || !got.Available[0].Enabled ||
		got.Available[0].Commands[0].Name != "cluster" || !reflect.DeepEqual(got.Available[0].Commands[0].AlsoIn, []string{"chaos"}) ||
		got.Available[0].Commands[0].Description != clusterCmd.Short {
		t.Errorf("JSON = %+v", got)
	}
}

func TestCompleteProfilesOffersAllAlone(t *testing.T) {
	has := func(out []cobra.Completion, name string) bool {
		return slices.ContainsFunc(out, func(c cobra.Completion) bool { return strings.SplitN(c, "\t", 2)[0] == name })
	}
	if out, _ := completeProfiles(nil, nil, ""); !has(out, "all") || !has(out, "chaos") {
		t.Errorf("first word: %q", out)
	}
	if out, _ := completeProfiles(nil, []string{"chaos"}, ""); has(out, "all") || has(out, "chaos") || !has(out, "topology") {
		t.Errorf("after chaos: %q", out)
	}
}

func TestCtxImportKeepsProfilesTheFileLeavesOut(t *testing.T) {
	old := Context{URL: "http://localhost:8080", Profiles: []string{"chaos"}}
	if got, _ := importContext("lab", Context{URL: "http://localhost:8080"}, old, true); !reflect.DeepEqual(got.Profiles, []string{"chaos"}) {
		t.Errorf("profiles = %q, want the context's own", got.Profiles)
	}
	if got, _ := importContext("lab", Context{URL: "http://localhost:8080", Profiles: []string{"deploy"}}, old, true); !reflect.DeepEqual(got.Profiles, []string{"deploy"}) {
		t.Errorf("profiles = %q, want the file's", got.Profiles)
	}
	if got, _ := importContext("new", Context{URL: "http://x"}, Context{}, false); got.Profiles != nil {
		t.Errorf("a new context got profiles %q", got.Profiles)
	}
}

// A refused command's help still prints on stdout, with the note on stderr; an
// enabled command's help has none.
func TestRefusedCommandHelpNote(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KATES_PROFILES", "chaos")
	prevErr, prevWarned := output.Err, profilesWarned
	t.Cleanup(func() { output.Err, profilesWarned = prevErr, prevWarned })
	for _, tt := range []struct {
		name string
		note bool
	}{{"tune", true}, {"test", false}} {
		c, _, err := rootCmd.Find([]string{tt.name})
		if err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		c.SetOut(&stdout)
		output.Err = &stderr
		rootCmd.HelpFunc()(c, nil)
		c.SetOut(nil)
		if !strings.Contains(stdout.String(), "Available Commands:") || strings.Contains(stdout.String(), "needs the") {
			t.Errorf("kates %s --help stdout:\n%s", tt.name, stdout.String())
		}
		if got := strings.Contains(stripAnsi(stderr.String()), "Run once:   KATES_PROFILES=all kates "+tt.name); got != tt.note {
			t.Errorf("kates %s --help note on stderr = %v, want %v:\n%s", tt.name, got, tt.note, stderr.String())
		}
	}
}
