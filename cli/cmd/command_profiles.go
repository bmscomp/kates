package cmd

import (
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/bmscomp/kates/cli/output"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"
)

// knownProfiles are the command profiles a context can enable, in the order
// kates lists them.
var knownProfiles = []string{"performance", "integrity", "chaos", "topology", "security", "deploy"}

// commandProfiles names the profiles that enable each top-level command; a
// subcommand follows its top-level command. A command with none is always
// enabled: the ones every profile needs, kates ctx among them, so a context
// can always change its profiles. A context with no profiles enables every
// command. TestCommandProfilesCoverEveryCommand fails on a command missing
// here.
var commandProfiles = map[string][]string{
	"advisor":       {"performance"},
	"audit":         nil,
	"auto":          {"deploy"},
	"badge":         {"performance"},
	"benchmark":     {"performance"},
	"changelog":     nil,
	"chaos":         {"chaos"},
	"clean":         {"deploy"},
	"cluster":       {"topology", "chaos"},
	"completion":    nil,
	"conformity":    {"deploy", "topology"},
	"cost":          {"performance"},
	"ctx":           nil,
	"dashboard":     {"performance", "integrity"},
	"deploy":        {"deploy"},
	"detect":        {"deploy"},
	"diff":          {"topology"},
	"disruption":    {"chaos"},
	"docs":          nil,
	"doctor":        {"deploy", "topology"},
	"explain":       {"performance", "integrity"},
	"flow":          {"performance", "chaos"},
	"gate":          {"performance"},
	"health":        nil,
	"help":          nil,
	"init":          nil,
	"kafka":         {"topology", "integrity"},
	"charts":        {"deploy"},
	"kyverno":       {"security", "deploy"},
	"lab":           {"performance"},
	"mcp":           nil,
	"migrate":       {"deploy", "integrity"},
	"operators":     {"deploy"},
	"plugin":        nil,
	"ports":         nil,
	"profile":       {"performance"},
	"replay":        {"performance", "integrity"},
	"report":        {"performance", "integrity", "chaos"},
	"resilience":    {"chaos"},
	"scenario-diff": {"performance", "integrity"},
	"schedule":      {"performance", "integrity"},
	"security":      {"security"},
	"snapshot":      {"topology"},
	"status":        nil,
	"test":          {"performance", "integrity", "chaos"},
	"theme":         nil,
	"tldr":          nil,
	"top":           {"performance", "integrity"},
	"trend":         {"performance"},
	"tune":          {"performance"},
	"upgrade":       nil,
	"version":       nil,
	"versions":      {"deploy"},
	"watch":         {"performance", "integrity", "chaos"},
	"webhook":       {"performance", "integrity"},
}

// activeProfiles returns the profiles in force and where they come from:
// KATES_PROFILES when it is set, else the context's. Unknown names are
// warned about and enable nothing. None, or "all" among them, enables every
// command.
func activeProfiles(ctxName string, ctx Context) (profiles []string, from string) {
	names, from := ctx.Profiles, fmt.Sprintf("context %q", ctxName)
	if env, ok := os.LookupEnv("KATES_PROFILES"); ok {
		names, from = splitProfiles(env), "KATES_PROFILES"
	}
	if slices.Contains(names, "all") {
		return nil, from
	}
	for _, n := range names {
		if !slices.Contains(knownProfiles, n) && !profilesWarned {
			fmt.Fprintf(output.Err, "  ! Unknown profile %q from %s: kates knows %s\n", n, from, strings.Join(knownProfiles, ", "))
		}
	}
	profilesWarned = true
	// Unknown names stay, so a typo shows wherever the profiles are listed, and
	// enable nothing: a context naming only unknown ones runs the
	// always-enabled commands alone, not every command.
	profiles = inProfileOrder(names)
	for _, n := range names {
		if !slices.Contains(knownProfiles, n) && !slices.Contains(profiles, n) {
			profiles = append(profiles, n)
		}
	}
	return profiles, from
}

// activeProfileNames and activeProfilesFrom are what the pre-run's
// activeProfiles returned, for commands that list other commands (kates tldr).
var (
	activeProfileNames []string
	activeProfilesFrom string
)

// profilesWarned keeps activeProfiles' warnings to one round a process: the
// help and kates ctx profiles read the profiles again after the pre-run.
var profilesWarned bool

func splitProfiles(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
}

// inProfileOrder returns names in knownProfiles' order, once each.
func inProfileOrder(names []string) []string {
	var out []string
	for _, p := range knownProfiles {
		if slices.Contains(names, p) {
			out = append(out, p)
		}
	}
	return out
}

// commandEnabled reports whether the top-level command name runs under
// profiles. Plugins and cobra's __complete are not in commandProfiles, and
// always run.
func commandEnabled(name string, profiles []string) bool {
	needs := commandProfiles[name]
	return len(profiles) == 0 || len(needs) == 0 ||
		slices.ContainsFunc(needs, func(p string) bool { return slices.Contains(profiles, p) })
}

// topLevel is cmd's ancestor just below the root, or cmd itself.
func topLevel(cmd *cobra.Command) *cobra.Command {
	for cmd.HasParent() && cmd.Parent().HasParent() {
		cmd = cmd.Parent()
	}
	return cmd
}

// profileRefusal prints why cmd may not run under profiles, with the two ways
// to run it, and returns the error that exits 1; nil when it may run.
func profileRefusal(cmd *cobra.Command, ctxName string, profiles []string, from string) error {
	top := topLevel(cmd)
	if commandEnabled(top.Name(), profiles) {
		return nil
	}
	err := cmdErr(fmt.Sprintf("kates %s needs the %s profile", top.Name(), orList(commandProfiles[top.Name()])))
	profileFix(cmd, ctxName, profiles, from)
	return err
}

// profileFix prints on stderr, where the error goes, the profiles in force and
// the two ways to run cmd, so that a refusal under -o json leaves stdout empty.
func profileFix(cmd *cobra.Command, ctxName string, profiles []string, from string) {
	needs := commandProfiles[topLevel(cmd).Name()]
	enable := "kates ctx profiles set " + strings.Join(inProfileOrder(append(slices.Clone(profiles), needs[0])), " ") + contextArg(ctxName)
	if from == "KATES_PROFILES" {
		enable = "add " + needs[0] + " to KATES_PROFILES"
	}
	for _, line := range []string{
		"Enabled:    " + strings.Join(profiles, ", ") + " (" + from + ")",
		"Enable it:  " + enable,
		"Run once:   KATES_PROFILES=all " + cmd.CommandPath() + contextArg(ctxName) + " " + ellipsis(),
	} {
		fmt.Fprintln(output.Err, output.DimStyle.Render("    "+line))
	}
}

// contextArg is " --context <name>" when --context chose the context, so a
// command printed for the user changes that context, not the current one.
func contextArg(ctxName string) string {
	if contextFlag != "" && !contextFromEnv {
		return " --context " + ctxName
	}
	return ""
}

// orList joins names as "a", "a or b", "a, b or c".
func orList(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// hideDisabledCommands keeps shell completion from offering the top-level
// commands profiles do not enable.
func hideDisabledCommands(root *cobra.Command, profiles []string) {
	for _, c := range root.Commands() {
		if !commandEnabled(c.Name(), profiles) {
			c.Hidden = true
		}
	}
}

// profileHelp is the root help: long with lines naming the profiles, and
// where they come from, before Quick Start, and without the lines of commands
// they do not enable, nor a section left with none. With no profiles it is
// long as written.
func profileHelp(long, ctxName string, profiles []string, from string) string {
	if len(profiles) == 0 {
		return long
	}
	change := "kates ctx profiles set" + contextArg(ctxName)
	if from == "KATES_PROFILES" {
		change = "edit or unset KATES_PROFILES"
	}
	// The values start where the help's descriptions do.
	header := fmt.Sprintf("  %-15s%s (%s)\n  %-15s%s\n  %-15s%s\n\n",
		"Profiles:", strings.Join(profiles, ", "), from, "Change them:", change, "Every command:", "KATES_PROFILES=all kates")
	var blocks []string
	for _, block := range strings.Split(long, "\n\n") {
		lines := strings.Split(block, "\n")
		// Only a section's lines start with a command; the banner's words may
		// name one too.
		if !strings.HasSuffix(lines[0], ":") || strings.HasPrefix(lines[0], " ") {
			blocks = append(blocks, block)
			continue
		}
		kept := lines[:1]
		for _, l := range lines[1:] {
			word := strings.Fields(strings.TrimPrefix(strings.TrimSpace(l), "$ kates "))
			if len(word) > 0 && !commandEnabled(word[0], profiles) {
				continue
			}
			kept = append(kept, l)
		}
		if len(kept) > 1 {
			blocks = append(blocks, strings.Join(kept, "\n"))
		}
	}
	help := strings.Join(blocks, "\n\n")
	if i := strings.Index(help, "Quick Start:"); i >= 0 {
		help = help[:i] + header + help[i:]
	}
	return help
}

// profileCommands are the top-level commands profile enables, sorted; for
// "", the ones always enabled.
func profileCommands(profile string) []string {
	var names []string
	for name, needs := range commandProfiles {
		if (profile == "" && len(needs) == 0) || (profile != "" && slices.Contains(needs, profile)) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

var ctxProfilesCmd = &cobra.Command{
	Use:   "profiles [profile...|all]",
	Short: "Show the command profiles a context enables",
	Long: `Show which command profiles the current context enables, or the one
--context names, and the commands each profile enables. Name profiles, or all,
to list their commands with what each one does.

A context with profiles enables only their commands, plus the ones every
profile needs (kates ctx, health, help and a few more). kates lists only those,
and refuses the others with the command that enables them. A context with no
profiles enables every command. KATES_PROFILES overrides the context's
profiles for one call; KATES_PROFILES=all enables every command.`,
	Example: `  kates ctx profiles
  kates ctx profiles chaos topology
  kates ctx profiles all
  kates ctx profiles --context lab -o json`,
	ValidArgsFunction: completeProfiles,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, err := profilesContext()
		if err != nil {
			return err
		}
		shown := knownProfiles
		if len(args) > 0 {
			named, err := parseProfiles(args)
			if err != nil {
				return cmdErr(err.Error())
			}
			if named != nil {
				shown = named
			}
		}
		active, from := activeProfiles(activeContext, ctx)
		short := map[string]string{}
		for _, c := range cmd.Root().Commands() {
			short[c.Name()] = c.Short
		}

		if outputMode == "json" {
			available := make([]map[string]any, 0, len(shown))
			for _, p := range shown {
				commands := []map[string]any{}
				for _, c := range profileCommands(p) {
					commands = append(commands, map[string]any{"name": c, "description": short[c], "alsoIn": alsoIn(c, p)})
				}
				available = append(available, map[string]any{"profile": p, "enabled": slices.Contains(active, p), "commands": commands})
			}
			source := "context"
			if from == "KATES_PROFILES" {
				source = from
			}
			output.JSON(map[string]any{
				"context": activeContext, "profiles": nonNil(active), "from": source,
				"available": available, "alwaysEnabled": profileCommands(""),
			})
			return nil
		}

		output.Header("Command Profiles")
		// The values start where the grid's commands do.
		key := func(k, v string) {
			fmt.Fprintf(output.Out, "  %s%s\n", output.KeyStyle.Width(profileIndent-2).Render(k), output.ValueStyle.Render(v))
		}
		key("Context", activeContext)
		if len(active) == 0 {
			key("Profiles", "none, every command is enabled")
		} else {
			key("Profiles", strings.Join(active, ", "))
		}
		if from == "KATES_PROFILES" {
			output.Hint("  KATES_PROFILES is set, and overrides the context's profiles in this shell.")
		}
		fmt.Fprintln(output.Out)
		// Hint indents by 2, so the commands start where the grid's do.
		hint := func(label, command string) {
			output.Hint(fmt.Sprintf("  %-*s%s%s", profileIndent-4, label, command, contextArg(activeContext)))
		}
		if len(args) == 0 {
			printProfileGrid(active)
			hint("Details:", "kates ctx profiles <profile>...")
		} else {
			for _, p := range shown {
				printProfileDetails(p, active, short)
			}
		}
		hint("Change them:", "kates ctx profiles set [profile...]")
		hint("Every command:", "kates ctx profiles set all")
		return nil
	},
}

// The profile list's layout: "  ● ", the profile name, a gap, and then the
// commands, in cells as wide as the longest command name and two spaces, so
// that every profile's commands line up in the same columns.
const (
	profileNameWidth = 13
	profileIndent    = 4 + profileNameWidth + 4
)

func commandCellWidth() int {
	w := 0
	for name := range commandProfiles {
		w = max(w, len(name))
	}
	return w + 2
}

// printProfileGrid prints each profile with its commands in a grid, the
// enabled commands highlighted and the refused ones dimmed.
func printProfileGrid(active []string) {
	cell := commandCellWidth()
	cols := max(1, (output.ColumnWidth(profileIndent, cell)+2)/cell) // the last cell needs no gap
	block := func(lead string, commands []string) {
		for i, row := range slices.Collect(slices.Chunk(commands, cols)) {
			if i > 0 {
				lead = strings.Repeat(" ", profileIndent)
			}
			var b strings.Builder
			b.WriteString(lead)
			for _, c := range row {
				b.WriteString(styleCommand(c, active) + strings.Repeat(" ", cell-len(c)))
			}
			fmt.Fprintln(output.Out, strings.TrimRight(b.String(), " "))
		}
		fmt.Fprintln(output.Out)
	}
	for _, p := range knownProfiles {
		block(profileLead(p, active), profileCommands(p))
	}
	block("    "+output.DimStyle.Render(output.PadRight("always", profileNameWidth))+"    ", profileCommands(""))
}

// printProfileDetails prints a profile's commands one a line, aligned with
// the grid, with what each does and the other profiles that enable it.
func printProfileDetails(profile string, active []string, short map[string]string) {
	state := "not enabled"
	switch {
	case slices.Contains(active, profile):
		state = "enabled"
	case len(active) == 0:
		state = "enabled, as every command is"
	}
	fmt.Fprintln(output.Out, profileLead(profile, active)+output.DimStyle.Render(state))
	cell := commandCellWidth()
	for _, c := range profileCommands(profile) {
		note := ""
		if also := alsoIn(c, profile); len(also) > 0 {
			note = output.DimStyle.Render("  (also " + strings.Join(also, ", ") + ")")
		}
		// The description is cut to the terminal, not the other profiles.
		room := max(10, output.TermWidth()-profileIndent-cell-ansi.StringWidth(note))
		fmt.Fprintln(output.Out, strings.Repeat(" ", profileIndent)+styleCommand(c, active)+strings.Repeat(" ", cell-len(c))+
			ansi.Truncate(short[c], room, ellipsis())+note)
	}
	fmt.Fprintln(output.Out)
}

// profileLead is a profile's marker and name, padded to where its commands
// start. A selected profile stands out; the others are dimmed when some are
// selected.
func profileLead(profile string, active []string) string {
	marker, name := output.DimStyle.Render(output.Glyphs().Ring), output.PadRight(profile, profileNameWidth)
	switch {
	case slices.Contains(active, profile):
		marker, name = output.SuccessStyle.Render(output.Glyphs().Bullet), output.SuccessStyle.Bold(true).Render(name)
	case len(active) > 0:
		name = output.DimStyle.Render(name)
	}
	return "  " + marker + " " + name + "    "
}

// styleCommand highlights a command the profiles enable, and dims one they
// refuse.
func styleCommand(name string, active []string) string {
	if commandEnabled(name, active) {
		return output.AccentStyle.Render(name)
	}
	return output.DimStyle.Render(name)
}

// alsoIn are the profiles other than profile that enable command.
func alsoIn(command, profile string) []string {
	return slices.DeleteFunc(slices.Clone(commandProfiles[command]), func(p string) bool { return p == profile })
}

// ellipsis is "…", or "..." where the output must stay ASCII.
func ellipsis() string {
	if output.ASCII() {
		return "..."
	}
	return "…"
}

var ctxProfilesSetCmd = &cobra.Command{
	Use:   "set [profile...|all]",
	Short: "Choose the command profiles a context enables",
	Long: `Set the command profiles of the current context, or of the one --context
names. The profiles given replace the ones it had; all enables every command.
With no profile named, in a terminal, it asks which ones to enable.

Profiles: ` + strings.Join(knownProfiles, ", ") + `.`,
	Example: `  kates ctx profiles set chaos topology
  kates ctx profiles set performance --context lab
  kates ctx profiles set all
  kates ctx profiles set            # choose from a list`,
	ValidArgsFunction: completeProfiles,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, err := profilesContext()
		if err != nil {
			return err
		}
		names := args
		if len(names) == 0 {
			if !IsInteractive() {
				return cmdErr("name the profiles to enable (" + strings.Join(knownProfiles, ", ") + "), or all")
			}
			if names, err = pickProfiles(ctx.Profiles); err != nil {
				return err
			}
		}
		profiles, err := parseProfiles(names)
		if err != nil {
			return cmdErr(err.Error())
		}

		err = updateConfig(func(c *Config) error {
			stored, ok := c.Contexts[activeContext]
			if !ok {
				return fmt.Errorf("context %q is not in %s", activeContext, configPath())
			}
			if slices.Equal(stored.Profiles, profiles) {
				return errConfigUnchanged
			}
			stored.Profiles = profiles
			c.Contexts[activeContext] = stored
			return nil
		})
		if err != nil {
			return cmdErr("Failed to save: " + err.Error())
		}
		if len(profiles) == 0 {
			output.Success(fmt.Sprintf("Context '%s' enables every command", activeContext))
		} else {
			output.Success(fmt.Sprintf("Context '%s' enables %s", activeContext, strings.Join(profiles, ", ")))
		}
		if _, ok := os.LookupEnv("KATES_PROFILES"); ok {
			output.Hint("  KATES_PROFILES is set, and overrides these in this shell.")
		}
		return nil
	},
}

// profilesContext is the context kates ctx profiles shows or changes: the one
// --context or KATES_CONTEXT names, else the current one. It must be in the
// config, which holds the built-in default when there is no file yet.
func profilesContext() (Context, error) {
	cfg, err := readConfig()
	if err != nil {
		return Context{}, cmdErr(fmt.Sprintf("Cannot load %s: %v", configPath(), err))
	}
	_, ctx, err := resolveContext(cfg)
	if err != nil {
		return Context{}, cmdErr(err.Error())
	}
	return ctx, nil
}

// parseProfiles checks names, each a profile or a comma-separated list of
// them, against knownProfiles. all, alone, enables every command and is
// stored as none.
func parseProfiles(names []string) ([]string, error) {
	var profiles []string
	for _, n := range names {
		profiles = append(profiles, splitProfiles(n)...)
	}
	if len(profiles) == 0 {
		return nil, fmt.Errorf("name a profile (%s), or all", strings.Join(knownProfiles, ", "))
	}
	if slices.Contains(profiles, "all") {
		if len(profiles) > 1 {
			return nil, fmt.Errorf("all enables every command; name it alone")
		}
		return nil, nil
	}
	for _, p := range profiles {
		if !slices.Contains(knownProfiles, p) {
			return nil, fmt.Errorf("unknown profile %q; kates knows %s, and all", p, strings.Join(knownProfiles, ", "))
		}
	}
	return inProfileOrder(profiles), nil
}

// pickProfiles asks which profiles to enable, the current ones ticked. None
// ticked means every command.
func pickProfiles(current []string) ([]string, error) {
	// One line an option, cut to the terminal after huh's "┃ ▸ ✓ ": the name,
	// how many commands it enables, and their names.
	// Ticked profiles first: huh scrolls the list to the first ticked option,
	// which hid the ones above it.
	ticked := inProfileOrder(current)
	order := append(slices.Clone(ticked), slices.DeleteFunc(slices.Clone(knownProfiles), func(p string) bool { return slices.Contains(ticked, p) })...)
	options := make([]huh.Option[string], 0, len(order))
	for _, p := range order {
		commands := profileCommands(p)
		label := fmt.Sprintf("%s%2d commands   %s", output.PadRight(p, profileNameWidth), len(commands), strings.Join(commands, ", "))
		options = append(options, huh.NewOption(ansi.Truncate(label, output.ColumnWidth(7, 30), ellipsis()), p).
			Selected(slices.Contains(ticked, p)))
	}
	var picked []string
	err := huh.NewForm(huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("Command profiles for context " + activeContext).
			Description("Space or x toggles, enter saves. None ticked enables every command.").
			Options(options...).
			Value(&picked),
	)).WithTheme(ThemeKates()).Run()
	if err != nil {
		return nil, err
	}
	if len(picked) == 0 {
		return []string{"all"}, nil
	}
	return picked, nil
}

// completeProfiles completes the profile names args has not named yet, and
// all.
func completeProfiles(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	var out []cobra.Completion
	for _, p := range knownProfiles {
		if !slices.Contains(args, p) {
			out = append(out, cobra.CompletionWithDesc(p, strings.Join(profileCommands(p), ", ")))
		}
	}
	if len(args) == 0 { // all stands alone
		out = append(out, cobra.CompletionWithDesc("all", "every command"))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func init() {
	ctxProfilesCmd.AddCommand(ctxProfilesSetCmd)
	ctxCmd.AddCommand(ctxProfilesCmd)
}
