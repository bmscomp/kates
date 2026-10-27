package cmd

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// sharedFlagContract is what every command's --records, --timeout, --wait
// and -f/--file must look like: one type, one help text, one shorthand.
var sharedFlagContract = map[string]struct {
	typ, usage, shorthand string
}{
	"records": {"int", recordsFlagHelp, ""},
	"timeout": {"string", timeoutFlagHelp, ""},
	"wait":    {"bool", waitFlagHelp, ""},
	"file":    {"string", outputFileFlagHelp, "f"},
}

// legacySharedFlags are the flags of a shared name that flags.go does not
// register yet. The list only shrinks; a new command with one of these flag
// names registers it through flags.go.
var legacySharedFlags = map[string]bool{
	// -f/--file names the file the command reads, not one it writes.
	"kates test apply --file":       true,
	"kates flow run --file":         true,
	"kates resilience run --file":   true,
	"kates conformity check --file": true,
	"kates ctx import --file":       true,
	// Command families another wave moves: a --timeout in whole seconds or
	// a Helm duration, a --records that sizes an estimate, scaffold's
	// output file.
	"kates cost estimate --records":           true,
	"kates doctor network --timeout":          true,
	"kates test helm --timeout":               true,
	"kates test scaffold export --file":       true,
	"kates migrate cutover --timeout":         true,
	"kates migrate down --timeout":            true,
	"kates migrate mirror cutover --timeout":  true,
	"kates migrate mirror deploy --timeout":   true,
	"kates migrate mirror remove --timeout":   true,
	"kates migrate mirror rollback --timeout": true,
	"kates migrate mirror status --timeout":   true,
	"kates migrate mirror status --wait":      true,
	"kates migrate plan --timeout":            true,
	"kates migrate rollback --timeout":        true,
	"kates migrate run --timeout":             true,
	"kates migrate source deploy --timeout":   true,
	"kates migrate source remove --timeout":   true,
	"kates migrate source status --timeout":   true,
	"kates migrate status --timeout":          true,
	"kates migrate target --timeout":          true,
	"kates migrate up --timeout":              true,
	"kates migrate verify --timeout":          true,
}

// Every flag of a shared name across the whole command tree has the one
// type, help text and shorthand flags.go gives it, unless it is on the legacy
// list, which records a command another wave still has to move.
func TestSharedFlagsAgree(t *testing.T) {
	seen := map[string]bool{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Annotations[pluginAnnotation] != "" {
				continue
			}
			sub.LocalFlags().VisitAll(func(f *pflag.Flag) {
				want, shared := sharedFlagContract[f.Name]
				if !shared {
					return
				}
				key := fmt.Sprintf("%s --%s", sub.CommandPath(), f.Name)
				registered := f.Annotations[sharedFlagAnnotation] != nil
				if legacySharedFlags[key] {
					seen[key] = true
					if registered {
						t.Errorf("%s is registered through flags.go: drop it from legacySharedFlags", key)
					}
					return
				}
				if !registered {
					t.Errorf("%s is not registered through flags.go (add%sFlag)", key, strings.ToUpper(f.Name[:1])+f.Name[1:])
					return
				}
				if f.Value.Type() != want.typ || f.Usage != want.usage || f.Shorthand != want.shorthand {
					t.Errorf("%s: type %q, shorthand %q, help %q; want %q, %q, %q",
						key, f.Value.Type(), f.Shorthand, f.Usage, want.typ, want.shorthand, want.usage)
				}
			})
			walk(sub)
		}
	}
	walk(rootCmd)

	// A legacy entry no command has any more is noise: the list only
	// shrinks.
	var stale []string
	for key := range legacySharedFlags {
		if !seen[key] {
			stale = append(stale, key)
		}
	}
	slices.Sort(stale)
	if len(stale) > 0 {
		t.Errorf("legacySharedFlags names flags no command has; remove: %s", strings.Join(stale, ", "))
	}
}

// The commands that share a flag read it the same way.
func TestSharedFlagsRegistered(t *testing.T) {
	for _, tc := range []struct {
		path []string
		flag string
	}{
		{[]string{"gate"}, "records"}, {[]string{"gate"}, "timeout"},
		{[]string{"benchmark"}, "records"}, {[]string{"benchmark"}, "timeout"},
		{[]string{"test", "create"}, "records"}, {[]string{"test", "create"}, "wait"},
		{[]string{"test", "apply"}, "wait"}, {[]string{"replay"}, "wait"}, {[]string{"flow", "run"}, "wait"},
		{[]string{"report", "export"}, "file"}, {[]string{"test", "export"}, "file"},
	} {
		cmd, _, err := rootCmd.Find(tc.path)
		if err != nil {
			t.Fatalf("kates %s: %v", strings.Join(tc.path, " "), err)
		}
		f := cmd.Flags().Lookup(tc.flag)
		if f == nil || f.Annotations[sharedFlagAnnotation] == nil {
			t.Errorf("kates %s --%s is not a shared flag", strings.Join(tc.path, " "), tc.flag)
		}
	}
}
