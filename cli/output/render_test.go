package output

import (
	"bytes"
	"strings"
	"testing"
)

// splitStreams points Out and Err at two buffers for the test and restores
// them, and JSON mode, when it ends.
func splitStreams(t *testing.T) (stdout, stderr *bytes.Buffer) {
	t.Helper()
	prevOut, prevErr, prevJSON := Out, Err, jsonMode
	t.Cleanup(func() { Out, Err, jsonMode = prevOut, prevErr, prevJSON })
	stdout, stderr = new(bytes.Buffer), new(bytes.Buffer)
	Out, Err = stdout, stderr
	return stdout, stderr
}

// Under -o json stdout is the JSON document and nothing else: the notes a
// command prints beside its result move to stderr, and a banner is dropped.
func TestJSONModeKeepsStdoutForTheResult(t *testing.T) {
	stdout, stderr := splitStreams(t)
	JSONMode(true)
	if !IsJSON() {
		t.Fatal("IsJSON() = false after JSONMode(true)")
	}

	Banner("Kates Doctor", "Pre-flight")
	Success("saved")
	Warn("slow")
	Hint("run kates ports")
	Error("broken")
	JSON(map[string]int{"n": 1})

	if got := stdout.String(); strings.TrimSpace(got) != "{\n  \"n\": 1\n}" {
		t.Errorf("stdout carries more than the document:\n%s", got)
	}
	for _, want := range []string{"saved", "slow", "run kates ports", "broken"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr.String())
		}
	}
	if strings.Contains(stderr.String(), "Kates Doctor") {
		t.Errorf("the banner went to stderr:\n%s", stderr.String())
	}
}

// Table mode is unchanged: notes on stdout, errors on stderr, banners drawn.
func TestTableModeNotesStayOnStdout(t *testing.T) {
	stdout, stderr := splitStreams(t)
	JSONMode(false)

	Banner("Kates Doctor", "Pre-flight")
	Success("saved")
	Warn("slow")
	Hint("run kates ports")
	Error("broken")

	for _, want := range []string{"Kates Doctor", "saved", "slow", "run kates ports"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "broken") || !strings.Contains(stderr.String(), "broken") {
		t.Errorf("the error is not on stderr alone: stdout %q, stderr %q", stdout.String(), stderr.String())
	}
}
