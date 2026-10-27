package cmd

// Exit codes, the policy. A script reads the code before it reads a line of
// output, so the code says what happened, in every output mode:
//
//	0   what was asked for happened: the test finished, the deploy applied,
//	    the check passed, the list printed.
//	1   it failed, or the answer is no: a followed run ended FAILED, a gate or
//	    a check found a gap, a confirmation was declined, a request failed.
//	2   kates could not tell: the check could not run, the input could not be
//	    read, the flags do not make sense, the command has no form of the
//	    output asked for. Neither yes nor no, so a script does not take it
//	    for either.
//	130 Ctrl-C or SIGTERM stopped an interruptible command (see interrupt.go),
//	    after it stopped what it had started.
//
// A command returns a non-nil error for anything but 0: cmdErr for 1 once the
// message is printed, couldNotTell for 2, errInterrupted for 130. A command
// that prints output.Error and then returns nil exits 0 and lies to the
// script; TestNoExitZeroAfterError in exitcodes_test.go refuses that, and a
// `// exit0: <reason>` comment on the return marks the few cases where the
// error is a note and the command did what was asked.
const (
	okExitCode           = 0
	failedExitCode       = 1
	couldNotTellExitCode = 2
	interruptedExitCode  = 130
)

// couldNotTell ends a command with exit 2 and msg on stderr: it could neither
// do nor refuse what was asked.
func couldNotTell(msg string) error {
	return &exitError{code: couldNotTellExitCode, msg: msg}
}
