package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/internal/conformity"
	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
)

var (
	conformityFmtWrite bool
	conformityFmtCheck bool
)

var conformityLintCmd = &cobra.Command{
	Use:   "lint <reference.yaml>...",
	Short: "Check KafkaReference files offline, as a compiler does",
	Long: `Checks KafkaReference files without a Kates API or a cluster, phase by
phase: YAML syntax, duplicates, the JSON Schema editors use, the checks the
Kates API makes beyond it, then incoherence between constraints, such as
an expired waiver. Each finding is printed as
file:line:col: severity: path: message [code], with the source line under
it; -o json lists the findings instead.

Exit codes: 0 when no file has an error (warnings are printed), 1 when one
does, 2 when a file cannot be read.`,
	Example: `  kates conformity lint platform/prod-3az.yaml
  kates conformity lint platform/*.yaml
  kates conformity lint -o json platform/*.yaml`,
	Args: cobra.MinimumNArgs(1),
	RunE: runConformityLint,
}

var conformityFmtCmd = &cobra.Command{
	Use:   "fmt <reference.yaml>...",
	Short: "Format KafkaReference files canonically, removing duplicates",
	Long: `Writes KafkaReference files in one canonical form: keys in the schema's
order, two-space indentation, a blank line between the sections of spec, each
constraint in its shortest form ({equals: 3} becomes 3, severity: fail goes),
and no exact duplicate: a key set twice to the same value, a value repeated in
a list, a waiver or listener listed twice. Comments stay.

It refuses a file that does not parse or sets one key to two values. Without
-w it prints the result; --check lists the files that are not formatted.

Exit codes: 0 on success, 1 when a file is refused or, with --check, not
formatted, 2 when a file cannot be read or written.`,
	Example: `  kates conformity fmt prod-3az.yaml
  kates conformity fmt -w platform/*.yaml
  kates conformity fmt --check platform/*.yaml`,
	Args: cobra.MinimumNArgs(1),
	RunE: runConformityFmt,
}

// lintFinding is a diagnostic with its file, as -o json lists it.
type lintFinding struct {
	File string `json:"file"`
	conformity.Diagnostic
}

func runConformityLint(_ *cobra.Command, files []string) error {
	errs, warns := 0, 0
	findings := []lintFinding{}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			return &exitError{code: 2, msg: err.Error()}
		}
		lines := strings.Split(string(src), "\n")
		for _, d := range conformity.Compile(src, time.Now().UTC()).Diagnostics {
			findings = append(findings, lintFinding{File: f, Diagnostic: d})
			if outputMode != "json" {
				fmt.Fprintln(output.Out, d.Report(f, lines))
			}
			if d.Severity == conformity.Error {
				errs++
			} else {
				warns++
			}
		}
	}
	if outputMode == "json" {
		output.JSON(findings)
		if errs > 0 {
			return &exitError{code: 1}
		}
		return nil
	}
	if errs+warns == 0 {
		fmt.Fprintf(output.Out, "%s: no problems\n", plural(len(files), "file", "files"))
		return nil
	}
	fmt.Fprintf(output.Out, "%s, %s in %s\n", plural(errs, "error", "errors"), plural(warns, "warning", "warnings"), plural(len(files), "file", "files"))
	if errs > 0 {
		return &exitError{code: 1}
	}
	return nil
}

func runConformityFmt(_ *cobra.Command, files []string) error {
	refused := false
	for _, f := range files {
		if strings.HasSuffix(f, ".json") {
			fmt.Fprintf(output.Out, "%s: not formatted: fmt writes YAML, and this file is JSON\n", f)
			refused = true
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			return &exitError{code: 2, msg: err.Error()}
		}
		out, ds := conformity.Format(src)
		if out == nil {
			for _, d := range ds {
				fmt.Fprintln(output.Out, d.Format(f))
			}
			refused = true
			continue
		}
		switch {
		case conformityFmtCheck:
			if !bytes.Equal(src, out) {
				fmt.Fprintln(output.Out, f)
				refused = true
			}
		case conformityFmtWrite:
			if !bytes.Equal(src, out) {
				if err := os.WriteFile(f, out, 0o644); err != nil {
					return &exitError{code: 2, msg: err.Error()}
				}
			}
		default:
			if _, err := io.Copy(output.Out, bytes.NewReader(out)); err != nil {
				return &exitError{code: 2, msg: err.Error()}
			}
		}
	}
	if refused {
		return &exitError{code: 1}
	}
	return nil
}

// lintBeforeCheck prints what lint finds in a reference about to be checked,
// to stderr since stdout may be the JSON report, and stops on an error.
func lintBeforeCheck(file string, src []byte) error {
	ds := conformity.Compile(src, time.Now().UTC()).Diagnostics
	lines := strings.Split(string(src), "\n")
	for _, d := range ds {
		fmt.Fprintln(output.Err, d.Report(file, lines))
	}
	if conformity.HasErrors(ds) {
		return &exitError{code: 2, msg: file + ": the reference has errors; kates conformity lint lists them"}
	}
	return nil
}

func init() {
	conformityFmtCmd.Flags().BoolVarP(&conformityFmtWrite, "write", "w", false, "Write the result back to each file")
	conformityFmtCmd.Flags().BoolVar(&conformityFmtCheck, "check", false, "List the files that are not formatted, and change nothing")
	conformityCmd.AddCommand(conformityLintCmd, conformityFmtCmd)
}
