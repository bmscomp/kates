package cmd

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
)

var scenarioDiffPick string

var scenarioDiffCmd = &cobra.Command{
	Use:     "scenario-diff <scenario.yaml> <test-id>",
	Aliases: []string{"sdiff"},
	Short:   "Compare a scenario YAML against a completed test run to detect config drift",
	Long: `Compare one scenario of a kates test apply file with a test run, field by
field, to find configuration drift. The file is read as kates test apply reads
it, and the scenario is turned into the request apply would send for it, so the
spec keys are the file's (records, parallelProducers, durationSeconds, ...) and
durationSeconds is compared as the durationMs it is sent as.

A file with more than one scenario needs --scenario, with the scenario's name
or its number in the file, counting from 1.

Only what the scenario sets is compared: its type, its backend, and each spec
field the request carries, against the spec the run reports (targetThroughput
against the rate the run used, its throughput). A field the run's spec does not
report, and a spec key kates test apply does not read or leaves out of the
request, is listed as not compared rather than counted as drift. The validate
block is not compared: kates test apply --wait grades it after the run.`,
	Example: `  kates scenario-diff scenario.yaml 69acdf31
  kates scenario-diff cli/examples/load-test.yaml 69acdf31 --scenario "Endurance Soak"
  kates scenario-diff cli/examples/perf-stress.yaml 69acdf31 --scenario 2 -o json`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		scenarioPath := args[0]
		testID := args[1]

		data, err := os.ReadFile(scenarioPath)
		if err != nil {
			return cmdErr("Failed to read scenario file: " + err.Error())
		}
		sf, err := parseScenarioFile(scenarioPath, data)
		if err != nil {
			return cmdErr("Invalid scenario file: " + err.Error())
		}
		if len(sf.Scenarios) == 0 {
			return cmdErr("No scenarios found in " + scenarioPath + ": it needs a scenarios list, or one scenario with a type at its top level")
		}
		i, err := pickScenario(scenarioPath, sf.Scenarios, scenarioDiffPick)
		if err != nil {
			return cmdErr(err.Error())
		}
		scenario := sf.Scenarios[i]
		// kates test apply refuses such a file, so no run was started from it.
		if problems := scenarioSpecProblems(scenario); len(problems) > 0 {
			return cmdErr(fmt.Sprintf("Invalid scenario file: scenario %d (%s): %s", i+1, scenario.Name, strings.Join(problems, "; ")))
		}

		ctx := context.Background()
		run, err := apiClient.GetTest(ctx, testID)
		if err != nil {
			return cmdErr("Test not found: " + err.Error())
		}

		d := diffScenario(scenario, run)
		d.File, d.Scenario, d.TestID = scenarioPath, scenarioName(scenario, i), cmp.Or(run.ID, testID)
		if outputMode == "json" {
			output.JSON(d)
			return nil
		}
		renderScenarioDiff(d)
		return nil
	},
}

// pickScenario returns the index of the scenario to compare, of the one or
// more a file holds: the one pick names, by the name kates test apply shows
// for it or else by its number in the file, counting from 1. A file of one
// scenario needs no pick.
func pickScenario(file string, scenarios []TestScenario, pick string) (int, error) {
	if pick == "" {
		if len(scenarios) == 1 {
			return 0, nil
		}
		return 0, fmt.Errorf("%s holds %d scenarios; choose one with --scenario <name|number>: %s",
			file, len(scenarios), scenarioChoices(scenarios))
	}
	var named []string
	at := 0
	for i, s := range scenarios {
		if scenarioName(s, i) == pick {
			named = append(named, strconv.Itoa(i+1))
			at = i
		}
	}
	switch {
	case len(named) == 1:
		return at, nil
	case len(named) > 1:
		return 0, fmt.Errorf("%d scenarios in %s are named %q; choose one by number with --scenario: %s",
			len(named), file, pick, strings.Join(named, ", "))
	}
	if n, err := strconv.Atoi(pick); err == nil && n >= 1 && n <= len(scenarios) {
		return n - 1, nil
	}
	return 0, fmt.Errorf("%s has no scenario %q; choose one with --scenario <name|number>: %s",
		file, pick, scenarioChoices(scenarios))
}

// scenarioChoices lists a file's scenarios by number, name and type.
func scenarioChoices(scenarios []TestScenario) string {
	choices := make([]string, len(scenarios))
	for i, s := range scenarios {
		choices[i] = fmt.Sprintf("%d %q", i+1, scenarioName(s, i))
		if s.Type != "" {
			choices[i] += " (" + output.Printable(strings.ToUpper(s.Type)) + ")"
		}
	}
	return strings.Join(choices, ", ")
}

// scenarioDiff is what kates scenario-diff found: the text output shows it,
// and -o json prints it as is. Compared names each field compared, drift or
// not, by where the scenario sets it.
type scenarioDiff struct {
	File        string             `json:"file"`
	Scenario    string             `json:"scenario"`
	TestID      string             `json:"testId"`
	Compared    []string           `json:"compared"`
	Differences []scenarioDiffItem `json:"differences"`
	NotCompared []scenarioDiffSkip `json:"notCompared,omitempty"`
}

// scenarioDiffItem is a field the request kates test apply would send for the
// scenario sets to another value than the run reports. Key is where the
// scenario sets it, Field the name the run reports it under, and both values
// are in the run's terms: a durationSeconds is shown as the durationMs it is
// sent as.
type scenarioDiffItem struct {
	Key      string `json:"key"`
	Field    string `json:"field"`
	Scenario string `json:"scenario"`
	Actual   string `json:"actual"`
	label    string
}

// scenarioDiffSkip is something the scenario sets that was not compared, and
// why.
type scenarioDiffSkip struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
}

// specRead reads one field of a spec as text, and whether the spec holds it.
type specRead func(*client.TestSpec) (string, bool)

// specInt reads an int field whose 0 a request leaves out (omitempty). A
// run's spec decodes a field it does not report as 0 too, so a 0 is a field
// the spec does not hold, never a value to compare.
func specInt(get func(*client.TestSpec) int) specRead {
	return func(s *client.TestSpec) (string, bool) {
		n := get(s)
		return strconv.Itoa(n), n != 0
	}
}

// specIntPtr reads an int field whose 0 is a setting, so it is held unless
// nil.
func specIntPtr(get func(*client.TestSpec) *int) specRead {
	return func(s *client.TestSpec) (string, bool) {
		if p := get(s); p != nil {
			return strconv.Itoa(*p), true
		}
		return "", false
	}
}

func specText(get func(*client.TestSpec) string) specRead {
	return func(s *client.TestSpec) (string, bool) {
		v := get(s)
		return v, v != ""
	}
}

func specBool(get func(*client.TestSpec) *bool) specRead {
	return func(s *client.TestSpec) (string, bool) {
		if p := get(s); p != nil {
			return strconv.FormatBool(*p), true
		}
		return "", false
	}
}

// scenarioDiffField is a spec field scenarioToRequest sets from a scenario:
// the key it reads, the name the run's spec gives the field, and how to read
// the field from the request and from the run's spec.
type scenarioDiffField struct {
	key, field, label string
	fold              bool     // compared ignoring case
	read              specRead // the field in the request, and in the run's spec unless ran is set
	ran               specRead // the field in the run's spec, when it is not the one read reads
}

// scenarioDiffFields are the spec fields scenarioToRequest sets, in the order
// it reads them; TestScenarioDiffFieldsMatchScenarioToRequest holds the two
// to the same keys.
var scenarioDiffFields = []scenarioDiffField{
	{key: "records", field: "numRecords", label: "Records",
		read: specInt(func(s *client.TestSpec) int { return s.Records })},
	{key: "parallelProducers", field: "numProducers", label: "Producers",
		read: specInt(func(s *client.TestSpec) int { return s.ParallelProducers })},
	{key: "recordSizeBytes", field: "recordSize", label: "Record Size",
		read: specInt(func(s *client.TestSpec) int { return s.RecordSizeBytes })},
	{key: "durationSeconds", field: "durationMs", label: "Duration (ms)",
		read: specInt(func(s *client.TestSpec) int { return s.DurationMs })},
	{key: "topic", field: "topic", label: "Topic",
		read: specText(func(s *client.TestSpec) string { return s.Topic })},
	{key: "acks", field: "acks", label: "Acks", fold: true,
		read: specText(func(s *client.TestSpec) string { return s.Acks })},
	{key: "batchSize", field: "batchSize", label: "Batch Size",
		read: specIntPtr(func(s *client.TestSpec) *int { return s.BatchSize })},
	{key: "lingerMs", field: "lingerMs", label: "Linger Ms",
		read: specIntPtr(func(s *client.TestSpec) *int { return s.LingerMs })},
	{key: "compressionType", field: "compressionType", label: "Compression", fold: true,
		read: specText(func(s *client.TestSpec) string { return s.CompressionType })},
	{key: "numConsumers", field: "numConsumers", label: "Consumers",
		read: specInt(func(s *client.TestSpec) int { return s.NumConsumers })},
	{key: "replicationFactor", field: "replicationFactor", label: "Replication Factor",
		read: specInt(func(s *client.TestSpec) int { return s.ReplicationFactor })},
	{key: "partitions", field: "partitions", label: "Partitions",
		read: specInt(func(s *client.TestSpec) int { return s.Partitions })},
	{key: "minInsyncReplicas", field: "minInsyncReplicas", label: "Min In-Sync Replicas",
		read: specInt(func(s *client.TestSpec) int { return s.MinInsyncReplicas })},
	{key: "consumerGroup", field: "consumerGroup", label: "Consumer Group",
		read: specText(func(s *client.TestSpec) string { return s.ConsumerGroup })},
	// A scenario's rate is sent as targetThroughput. The backend runs at it
	// when the request sets no throughput, which a scenario's never does, and
	// its spec reports the rate the run used as throughput: a run started
	// with throughput alone has no targetThroughput to compare.
	{key: "targetThroughput", field: "throughput", label: "Throughput",
		read: specInt(func(s *client.TestSpec) int { return s.TargetThroughput }),
		ran:  specInt(func(s *client.TestSpec) int { return s.Throughput })},
	{key: "fetchMinBytes", field: "fetchMinBytes", label: "Fetch Min Bytes",
		read: specInt(func(s *client.TestSpec) int { return s.FetchMinBytes })},
	{key: "fetchMaxWaitMs", field: "fetchMaxWaitMs", label: "Fetch Max Wait Ms",
		read: specIntPtr(func(s *client.TestSpec) *int { return s.FetchMaxWaitMs })},
	{key: "enableIdempotence", field: "enableIdempotence", label: "Idempotence",
		read: specBool(func(s *client.TestSpec) *bool { return s.EnableIdempotence })},
	{key: "enableTransactions", field: "enableTransactions", label: "Transactions",
		read: specBool(func(s *client.TestSpec) *bool { return s.EnableTransactions })},
	{key: "enableCrc", field: "enableCrc", label: "CRC Checks",
		read: specBool(func(s *client.TestSpec) *bool { return s.EnableCrc })},
}

// diffScenario compares the request kates test apply would send for s with
// the run: its type, its backend and each spec field it carries, against what
// the run reports. Something the scenario sets that the request does not
// carry, or that the run does not report, is not compared.
func diffScenario(s TestScenario, run *client.TestRun) scenarioDiff {
	req := scenarioToRequest(s)
	d := scenarioDiff{Compared: []string{}, Differences: []scenarioDiffItem{}}
	skip := func(key, why string) {
		d.NotCompared = append(d.NotCompared, scenarioDiffSkip{key, why})
	}
	compare := func(key, field, label, want, got string, fold bool) {
		d.Compared = append(d.Compared, key)
		if same := want == got || fold && strings.EqualFold(want, got); !same {
			d.Differences = append(d.Differences, scenarioDiffItem{key, field, want, got, label})
		}
	}
	for _, top := range []struct{ key, field, label, want, got string }{
		{"type", "testType", "Type", req.TestType, run.TestType},
		{"backend", "backend", "Backend", req.Backend, run.Backend},
	} {
		switch {
		case top.want == "":
		case top.got == "":
			skip(top.key, "the run does not report it")
		default:
			compare(top.key, top.field, top.label, top.want, top.got, true)
		}
	}
	if req.Spec == nil {
		return d
	}

	reported := run.Spec
	if reported == nil {
		reported = &client.TestSpec{}
	}
	for _, f := range scenarioDiffFields {
		v, set := s.Spec[f.key]
		if !set {
			continue
		}
		key := "spec." + f.key
		want, sent := f.read(req.Spec)
		if !sent {
			skip(key, scenarioDiffNotSent(v))
			continue
		}
		ran := f.read
		if f.ran != nil {
			ran = f.ran
		}
		got, ok := ran(reported)
		if !ok {
			skip(key, "the run's spec does not report it")
			continue
		}
		compare(key, f.field, f.label, want, got, f.fold)
	}
	for _, key := range slices.Sorted(maps.Keys(s.Spec)) {
		if !slices.ContainsFunc(scenarioDiffFields, func(f scenarioDiffField) bool { return f.key == key }) {
			reason := "kates test apply does not read it"
			if hint := mcpScnSpecKeyHints[key]; hint != "" {
				reason += "; " + hint
			}
			skip("spec."+key, reason)
		}
	}
	return d
}

// scenarioDiffNotSent says why scenarioToRequest leaves out a value the
// scenario sets for a key it reads.
func scenarioDiffNotSent(v any) string {
	switch x := v.(type) {
	case int, float64:
		return "kates test apply reads it as 0 and leaves it out"
	case string:
		if x == "" {
			return "it is empty, so kates test apply leaves it out"
		}
	}
	return "it is not a number, so kates test apply leaves it out"
}

func renderScenarioDiff(d scenarioDiff) {
	output.Banner("Scenario Diff", fmt.Sprintf("%s (%s) vs %s", d.File, output.Printable(d.Scenario), truncID(d.TestID)))
	fmt.Fprintln(output.Out)

	for _, item := range d.Differences {
		printScenarioDiff(item.label, output.Printable(item.Scenario), output.Printable(item.Actual))
	}
	if len(d.NotCompared) > 0 {
		if len(d.Differences) > 0 {
			fmt.Fprintln(output.Out)
		}
		fmt.Fprintf(output.Out, "  %s\n", output.DimStyle.Render("Not compared:"))
		for _, s := range d.NotCompared {
			fmt.Fprintf(output.Out, "    %s %s\n", output.DimStyle.Render(output.Printable(s.Key)+":"), s.Reason)
		}
	}

	fmt.Fprintln(output.Out)
	switch {
	case len(d.Differences) > 0:
		output.Warn(fmt.Sprintf("%d configuration difference(s) detected.", len(d.Differences)))
	case len(d.Compared) == 0:
		output.Warn("Nothing compared: the run reports none of the fields the scenario sets.")
	case len(d.NotCompared) > 0:
		output.Success(fmt.Sprintf("No configuration drift in the %d field(s) compared.", len(d.Compared)))
	default:
		output.Success("No configuration drift — scenario matches test run.")
	}
}

func printScenarioDiff(field, expected, actual string) {
	fmt.Fprintf(output.Out, "  %s\n", output.AccentStyle.Render(field+":"))
	fmt.Fprintf(output.Out, "    %s %s\n", output.ErrorStyle.Render("- scenario:"), expected)
	fmt.Fprintf(output.Out, "    %s %s\n", output.SuccessStyle.Render("+ actual:  "), actual)
}

func init() {
	scenarioDiffCmd.Flags().StringVar(&scenarioDiffPick, "scenario", "",
		"Scenario to compare, by name or number (from 1); required when the file holds more than one")
	rootCmd.AddCommand(scenarioDiffCmd)
}
