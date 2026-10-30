package cmd

import (
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/internal/compliance"
	"github.com/bmscomp/kates/cli/output"
	"github.com/bmscomp/kates/cli/pkg/grade"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const fileLintExitCodes = `Exit codes: 0 when no file has an error (warnings are printed), 1 when one
does, 2 when a file cannot be read.`

var testLintCmd = &cobra.Command{
	Use:   "lint <scenario.yaml>...",
	Short: "Check scenario files offline, as kates test apply reads them",
	Long: `Checks scenario files without a Kates API: YAML or JSON syntax, a
scenarios list or one scenario at the top level, every key against the ones
kates test apply reads (a typo is an error, with the key it is closest to),
each type, each spec value apply could not send as written (a quoted number,
1.5 for a whole number, yes for true), and each validate gate. A gate that
checks nothing, such as maxErrorRate or a maxP99LatencyMs of 0, is a warning.
Each finding is printed as file:line:col: severity: path: message [code],
with the source line under it; -o json lists the findings instead.

kates test apply runs the same checks over the whole file before it starts
the first scenario, and starts none when there is an error.

` + fileLintExitCodes,
	Example: `  kates test lint load-test.yaml
  kates test lint scenarios/*.yaml
  kates test lint -o json scenarios/*.yaml`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(_ *cobra.Command, files []string) error {
		return runFileLint(files, lintScenarioFile)
	},
}

var flowLintCmd = &cobra.Command{
	Use:   "lint <flow.yaml>...",
	Short: "Check flow files offline, as kates flow run reads them",
	Long: `Checks flow files without a Kates API, after expanding environment
variables as kates flow run does: YAML syntax, every key against the ones
flow run reads (a typo is an error, with the key it is closest to), each
step's action (test or wait; expect, webhook and notify are not implemented
and fail the step), each test step's type, gate and spec, and onFail. Each
finding is printed as file:line:col: severity: path: message [code], with
the source line under it; -o json lists the findings instead.

kates flow run runs the same checks before its first step, and runs none
when there is an error.

` + fileLintExitCodes,
	Example: `  kates flow lint release-qual.yaml
  kates flow lint -o json flows/*.yaml`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(_ *cobra.Command, files []string) error {
		return runFileLint(files, func(_ string, src []byte) []compliance.Diagnostic { return lintFlowFile(src) })
	},
}

// runFileLint prints what lint finds in each file, as kates compliance lint
// does, and exits 1 when a file has an error.
func runFileLint(files []string, lint func(name string, src []byte) []compliance.Diagnostic) error {
	errs, warns := 0, 0
	findings := []lintFinding{}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			return &exitError{code: 2, msg: err.Error()}
		}
		lines := strings.Split(string(src), "\n")
		for _, d := range lint(f, src) {
			findings = append(findings, lintFinding{File: f, Diagnostic: d})
			if outputMode != "json" {
				fmt.Fprintln(output.Out, d.Report(f, lines))
			}
			if d.Severity == compliance.Error {
				errs++
			} else {
				warns++
			}
		}
	}
	if outputMode == "json" {
		output.JSON(findings)
	} else if errs+warns == 0 {
		fmt.Fprintf(output.Out, "%s: no problems\n", plural(len(files), "file", "files"))
	} else {
		fmt.Fprintf(output.Out, "%s, %s in %s\n", plural(errs, "error", "errors"), plural(warns, "warning", "warnings"), plural(len(files), "file", "files"))
	}
	if errs > 0 {
		return &exitError{code: 1}
	}
	return nil
}

// lintBefore prints what lint found in a file about to run, to stderr since
// stdout may carry the JSON summary, and stops on an error with exit 2:
// nothing has started.
func lintBefore(file string, src []byte, ds []compliance.Diagnostic, command string) error {
	lines := strings.Split(string(src), "\n")
	errs := 0
	for _, d := range ds {
		fmt.Fprintln(output.Err, d.Report(file, lines))
		if d.Severity == compliance.Error {
			errs++
		}
	}
	if errs > 0 {
		return couldNotTell(fmt.Sprintf("%s: %s, so nothing was started; %s lists them",
			file, plural(errs, "error", "errors"), command))
	}
	return nil
}

func lintBeforeApply(file string, src []byte) error {
	return lintBefore(file, src, lintScenarioFile(file, src), "kates test lint")
}

func lintBeforeFlow(file string, src []byte) error {
	return lintBefore(file, src, lintFlowFile(src), "kates flow lint")
}

// The actions kates flow run runs, and the ones it fails as not implemented.
var (
	flowActions         = []string{"test", "wait"}
	flowNotImplemented  = []string{"expect", "webhook", "notify"}
	flowOnFailValues    = []string{"continue", "stop"}
	flowTestOnlyFields  = []string{"type", "gate", "spec", "expect"}
	flowUnusedForAction = map[string][]string{"wait": flowTestOnlyFields}
)

// lintFlowFile checks a flow file as kates flow run reads it, after the
// environment expansion it makes.
func lintFlowFile(src []byte) []compliance.Diagnostic {
	src = []byte(os.ExpandEnv(string(src)))
	l := &fileLinter{}
	doc := l.parseNode(src)
	if doc == nil {
		if !l.hasErrors() {
			l.errorAt(nil, "no-steps", "", "the file is empty: a flow needs steps")
		}
		return l.sorted()
	}
	known, _ := yamlFields(reflect.TypeOf(FlowPipeline{}))
	fields := l.mapping(doc, "", known, nil)
	if fields == nil {
		return l.sorted()
	}
	steps, ok := fields["steps"]
	switch {
	case !ok || steps.Tag == "!!null" || (steps.Kind == yaml.SequenceNode && len(steps.Content) == 0):
		l.errorAt(doc, "no-steps", "steps", "the flow has no steps")
	case steps.Kind != yaml.SequenceNode:
		l.errorAt(steps, "wrong-type", "steps", "steps is "+nodeKind(steps)+"; write a list of steps")
	default:
		for i, step := range steps.Content {
			l.flowStep(step, fmt.Sprintf("steps[%d]", i))
		}
	}
	if !l.hasErrors() {
		var p FlowPipeline
		if err := yaml.Unmarshal(src, &p); err != nil {
			l.decodeError("decode", "", err)
		}
	}
	return l.sorted()
}

func (l *fileLinter) flowStep(n *yaml.Node, path string) {
	known, types := yamlFields(reflect.TypeOf(FlowStep{}))
	fields := l.mapping(n, path, known, nil)
	if fields == nil {
		return
	}
	action := ""
	if a, ok := fields["action"]; ok {
		action = a.Value
		l.flowAction(a, childPath(path, "action"))
	} else {
		l.errorAt(n, "missing-action", path, "the step has no action; it is "+strings.Join(flowActions, " or "))
	}
	for key, v := range fields {
		p := childPath(path, key)
		switch key {
		case "type":
			l.checkTestType(v, p)
		case "gate":
			l.flowGate(v, p)
		case "spec":
			l.flowSpec(v, p)
		case "expect":
			l.errorAt(v, "not-implemented", p, "expect is not implemented: kates flow run fails the step")
		case "onFail":
			if v.Value != "" && !slices.Contains(flowOnFailValues, v.Value) {
				l.errorAt(v, "wrong-value", p, fmt.Sprintf("onFail is %s; a failed step stops the flow unless it is continue", nodeKind(v)))
			}
		default:
			l.decodeAs(v, types[key], p)
		}
		if slices.Contains(flowUnusedForAction[action], key) {
			l.warnAt(v, "unused-field", p, "a "+action+" step does not read "+key)
		}
	}
}

func (l *fileLinter) flowAction(n *yaml.Node, path string) {
	switch {
	case slices.Contains(flowActions, n.Value):
	case slices.Contains(flowNotImplemented, n.Value):
		l.errorAt(n, "not-implemented", path, "the action "+n.Value+" is not implemented: kates flow run fails the step")
	default:
		msg := fmt.Sprintf("there is no action %s; it is %s", nodeKind(n), strings.Join(flowActions, " or "))
		if guess := compliance.Closest(n.Value, flowActions); guess != "" {
			msg += fmt.Sprintf(" (did you mean %q?)", guess)
		}
		l.errorAt(n, "unknown-action", path, msg)
	}
}

func (l *fileLinter) flowGate(n *yaml.Node, path string) {
	known, types := yamlFields(reflect.TypeOf(FlowGate{}))
	for key, v := range l.mapping(n, path, known, nil) {
		p := childPath(path, key)
		if key == "minGrade" {
			if _, err := grade.Parse(v.Value); err != nil {
				l.errorAt(v, "wrong-value", p, err.Error())
			}
			continue
		}
		l.decodeAs(v, types[key], p)
	}
}

// flowSpec checks a test step's spec, which takes the request's own field
// names (numRecords, recordSize), not a scenario file's.
func (l *fileLinter) flowSpec(n *yaml.Node, path string) {
	known, types := yamlFields(reflect.TypeOf(client.TestSpec{}))
	hints := map[string]string{
		"records":           "a flow spec takes the request's names: numRecords",
		"parallelProducers": "a flow spec takes the request's names: numProducers",
		"recordSizeBytes":   "a flow spec takes the request's names: recordSize",
		"durationSeconds":   "a flow spec takes the request's names: durationMs, in milliseconds",
	}
	for key, v := range l.mapping(n, path, known, hints) {
		l.decodeAs(v, types[key], childPath(path, key))
	}
}

func init() {
	testCmd.AddCommand(testLintCmd)
	flowCmd.AddCommand(flowLintCmd)
}
