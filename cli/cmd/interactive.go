package cmd

import (
	"os"

	"golang.org/x/term"
)

// interactiveAllowedFn is the seam tests use to force either answer without a
// real terminal. It holds the production implementation by default.
var interactiveAllowedFn = interactiveAllowed

// IsInteractive reports whether it is safe to prompt, launch a bubbletea
// program, or run a clear-screen loop.
//
// This is the single guard for every interactive surface in the CLI. A prompt
// or TUI that starts without a terminal does not degrade — it either hangs
// (fmt.Scanln on a pipe), crashes with a raw charmbracelet error (tea's
// /dev/tty open), or floods a log with clear-screen escapes. Every
// tea.NewProgram, huh form, confirm prompt, and watch loop must sit behind
// this.
//
// It answers false when:
//   - isTesting: tests call command RunE functions directly and must never
//     block on input;
//   - deployYes (--yes): the user asked for no prompts; ambiguity becomes an
//     error, never a guess;
//   - plainOutput (--plain / KATES_PLAIN): plain output is a statement that a
//     machine is reading — forms and alt-screen TUIs have no place in it;
//   - TERM=dumb: the terminal has declared it cannot do cursor addressing;
//   - stdin or stdout is not a TTY.
//
// x/term.IsTerminal is cross-platform (golang.org/x/sys under the hood), so no
// build-tagged probes are needed here.
func IsInteractive() bool {
	return interactiveAllowedFn()
}

func interactiveAllowed() bool {
	if isTesting || deployYes || plainOutput {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}
