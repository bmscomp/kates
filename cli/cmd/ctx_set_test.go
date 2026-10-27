package cmd

import (
	"reflect"
	"testing"

	"github.com/spf13/pflag"
)

// runCtxSet runs kates ctx set with args, with its flags reset first and
// after, as a new process would start: pflag keeps Changed between parses.
func runCtxSet(t *testing.T, args ...string) error {
	t.Helper()
	reset := func() {
		ctxSetCmd.Flags().VisitAll(func(f *pflag.Flag) {
			f.Changed = false
			_ = f.Value.Set(f.DefValue)
		})
	}
	reset()
	t.Cleanup(reset)
	_, _, err := commandOutput(t, func() error {
		return runCtxCommandLine(t, append([]string{"ctx", "set"}, args...)...)
	})
	return err
}

func ctxSetTestConfig() Config {
	return Config{CurrentContext: "prod", Contexts: map[string]Context{
		"prod": {URL: "https://old.example.com", Output: "json", APIKey: exportLongKey,
			ProxyURL: "http://proxy:8080", Insecure: true, KeySource: "secret:abc", Profiles: []string{"ops"}},
	}}
}

// ctx set on an existing context changes only what its flags pass. It used
// to rebuild the context from the flags, so `ctx set prod --url …` dropped
// the API key, proxy and TLS setting.
func TestCtxSet_MergesIntoAnExistingContext(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want Context
	}{
		{"url alone keeps the rest", []string{"prod", "--url", "https://new.example.com"},
			Context{URL: "https://new.example.com", Output: "json", APIKey: exportLongKey,
				ProxyURL: "http://proxy:8080", Insecure: true, KeySource: "secret:abc", Profiles: []string{"ops"}}},
		{"api-key alone keeps the url", []string{"prod", "--api-key", "newkey"},
			Context{URL: "https://old.example.com", Output: "json", APIKey: "newkey",
				ProxyURL: "http://proxy:8080", Insecure: true, Profiles: []string{"ops"}}},
		{"insecure=false is a change", []string{"prod", "--insecure=false", "--proxy", ""},
			Context{URL: "https://old.example.com", Output: "json", APIKey: exportLongKey,
				KeySource: "secret:abc", Profiles: []string{"ops"}}},
		{"--replace builds it from the flags", []string{"prod", "--url", "https://new.example.com", "--replace"},
			Context{URL: "https://new.example.com", Output: "table", Profiles: []string{"ops"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			mustSaveConfig(t, ctxSetTestConfig())

			if err := runCtxSet(t, tt.args...); err != nil {
				t.Fatalf("ctx set %v: %v", tt.args, err)
			}
			if got := loadConfig().Contexts["prod"]; !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ctx set %v stored\n%+v\nwant\n%+v", tt.args, got, tt.want)
			}
		})
	}
}

// --url is required to create a context, and with --replace; an update of an
// existing one does without it.
func TestCtxSet_URLRequiredToCreate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mustSaveConfig(t, ctxSetTestConfig())

	if err := runCtxSet(t, "staging", "--api-key", "k"); err == nil {
		t.Error("ctx set staging --api-key k: no error, want --url required")
	}
	if _, ok := loadConfig().Contexts["staging"]; ok {
		t.Error("ctx set staging without --url stored a context")
	}
	if err := runCtxSet(t, "prod", "--api-key", "k", "--replace"); err == nil {
		t.Error("ctx set prod --replace without --url: no error")
	}
	if err := runCtxSet(t, "staging", "--url", "http://staging:8080"); err != nil {
		t.Fatalf("ctx set staging --url: %v", err)
	}
	want := Context{URL: "http://staging:8080", Output: "table"}
	if got := loadConfig().Contexts["staging"]; !reflect.DeepEqual(got, want) {
		t.Errorf("created %+v, want %+v", got, want)
	}
}

// Deleting the current context makes the first by name current. It used to
// take the first the map yielded, so two runs could pick two contexts.
func TestCtxDelete_CurrentPicksTheFirstByName(t *testing.T) {
	for range 5 {
		t.Setenv("HOME", t.TempDir())
		mustSaveConfig(t, Config{CurrentContext: "mid", Contexts: map[string]Context{
			"zeta":  {URL: "http://z"},
			"mid":   {URL: "http://m"},
			"alpha": {URL: "http://a"},
			"beta":  {URL: "http://b"},
		}})
		if _, _, err := commandOutput(t, func() error { return runCtxCommandLine(t, "ctx", "delete", "mid") }); err != nil {
			t.Fatal(err)
		}
		if cfg := loadConfig(); cfg.CurrentContext != "alpha" {
			t.Fatalf("current context = %q after deleting the current one, want alpha", cfg.CurrentContext)
		}
	}
}
