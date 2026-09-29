package cmd

import "fmt"

// confirm is the CLI's one yes/no prompt. Every command-level confirmation
// goes through here — the repo previously had three hand-rolled
// implementations (bufio.Scanner, two fmt.Scanln variants), one of which
// failed OPEN on closed stdin and deleted a Kafka topic without consent.
//
// Policy, uniform everywhere:
//   - Default No. Enter, q, esc, ctrl+c, or anything that is not an explicit
//     yes declines.
//   - Without a terminal it REFUSES with an error rather than assuming either
//     answer. Unattended runs state intent with the command's --yes flag.
//
// Callers decide what a decline means, but per the exit-code contract a
// declined destructive action returns a non-nil error: a script that forgot
// --yes must fail loudly, not report success.
func confirm(question string) (bool, error) {
	if !IsInteractive() {
		return false, fmt.Errorf("cannot prompt for confirmation without a terminal — pass --yes to proceed unattended")
	}
	// confirmPrompt (cluster_picker.go) is the single rendering engine: a
	// themed bubbletea model, already default-No.
	return confirmFn(question)
}
