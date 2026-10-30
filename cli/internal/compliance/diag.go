// Package compliance compiles KafkaReference files offline: it parses one,
// checks it against the JSON Schema editors use, applies the checks the Kates
// API's reader makes beyond the schema, and looks for incoherence between
// constraints. It also formats one canonically and removes duplicates.
package compliance

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Severity is how bad a diagnostic is: an error stops the check, a warning
// does not.
type Severity int

const (
	Warning Severity = iota
	Error
)

func (s Severity) String() string {
	if s == Error {
		return "error"
	}
	return "warning"
}

// MarshalText writes the severity as lint prints it, error or warning.
func (s Severity) MarshalText() ([]byte, error) {
	return []byte(s.String()), nil
}

// Diagnostic is one finding, at a place in the file.
type Diagnostic struct {
	Line     int      `json:"line,omitempty"`
	Col      int      `json:"col,omitempty"`
	Severity Severity `json:"severity"`
	// Code names the check, as in [unknown-field] or [waiver-expired].
	Code string `json:"code"`
	// Path is where in the reference, as the API names it: spec.brokers.count.
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
	// Fixable says kates compliance fmt removes the cause.
	Fixable bool `json:"fixable,omitempty"`
}

// Format writes the diagnostic as a compiler does: file:line:col: severity: message [code].
func (d Diagnostic) Format(file string) string {
	fix := ""
	if d.Fixable {
		fix = " (kates compliance fmt removes it)"
	}
	where := file
	if d.Line > 0 {
		where = fmt.Sprintf("%s:%d:%d", file, d.Line, d.Col)
	}
	path := ""
	if d.Path != "" {
		path = d.Path + ": "
	}
	return fmt.Sprintf("%s: %s: %s%s%s [%s]", where, d.Severity, path, d.Message, fix, d.Code)
}

// Report is Format with the source line under it and a caret at the
// column, as a compiler shows an error. lines is the file split on newlines;
// a diagnostic without a line in it is Format alone.
func (d Diagnostic) Report(file string, lines []string) string {
	if d.Line < 1 || d.Line > len(lines) {
		return d.Format(file)
	}
	text := strings.TrimRight(lines[d.Line-1], "\r")
	var caret strings.Builder
	for i, r := range []rune(text) {
		if i >= d.Col-1 {
			break
		}
		if r == '\t' {
			caret.WriteRune('\t')
		} else {
			caret.WriteRune(' ')
		}
	}
	num := strconv.Itoa(d.Line)
	return fmt.Sprintf("%s\n  %s | %s\n  %s | %s^", d.Format(file), num, text, strings.Repeat(" ", len(num)), caret.String())
}

func at(n *yaml.Node, sev Severity, code, path, msg string) Diagnostic {
	d := Diagnostic{Severity: sev, Code: code, Path: path, Message: msg}
	if n != nil {
		d.Line, d.Col = n.Line, n.Column
	}
	return d
}

func sortDiagnostics(ds []Diagnostic) {
	sort.SliceStable(ds, func(i, j int) bool {
		if ds[i].Line != ds[j].Line {
			return ds[i].Line < ds[j].Line
		}
		return ds[i].Col < ds[j].Col
	})
}

// HasErrors reports whether any diagnostic is an error.
func HasErrors(ds []Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == Error {
			return true
		}
	}
	return false
}
