package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
)

// Ctrl-C, and the commands that start work on the Kates API and wait for it.
//
// Go ends the process on SIGINT or SIGTERM, which is right for a command that
// only reads: nothing is left behind. A command that starts a test or a
// disruption and waits for it is different. Killed while waiting, it left the
// run going without a word, and `test apply --wait` took Ctrl-C in its
// spinner as one scenario's error and went on to start the next scenario.
//
// Such a command is marked interruptible. It runs with a context the first
// SIGINT or SIGTERM cancels (in a terminal, a spinner sees Ctrl-C as a key and
// ends the same way), stops what it started, says what it stopped, and
// returns errInterrupted, which exits 130. The first signal also restores
// Go's default, so a second Ctrl-C ends kates at once. Every other command
// keeps the default: a handler for all of them would leave the ones that
// never look at their context deaf to Ctrl-C.

const interruptibleAnnotation = "kates/interruptible"

// interruptible marks cmd as a command that handles SIGINT and SIGTERM itself.
func interruptible(cmd *cobra.Command) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[interruptibleAnnotation] = "true"
}

func isInterruptible(cmd *cobra.Command) bool {
	return cmd.Annotations[interruptibleAnnotation] == "true"
}

// interruptContext is the context kates runs with: for an interruptible
// command, one the first SIGINT or SIGTERM cancels, and a plain one for every
// other. args are the command line, which it resolves the way cobra will.
func interruptContext(args []string) (context.Context, context.CancelFunc) {
	cmd, _, err := rootCmd.Find(args)
	if err != nil || !isInterruptible(cmd) {
		return context.Background(), func() {}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop() // the default again: a second Ctrl-C ends kates at once
	}()
	return ctx, stop
}

// commandContext is cmd's context. Tests call a command's RunE outside
// cobra's Execute, where it has none.
func commandContext(cmd *cobra.Command) context.Context {
	if ctx := cmd.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}

// errInterrupted ends a command that was interrupted. The command has said
// what it stopped, so Execute adds nothing and exits 130, the code a shell
// gives a process that SIGINT ended.
var errInterrupted = errors.New("interrupted")

const interruptedExitCode = 130

// errStoppedWaiting is returned by a wait that ended because the user
// stopped it (Ctrl-C or q in a spinner) or the command's context was
// cancelled, before the run it followed finished.
var errStoppedWaiting = errors.New("stopped waiting")

// stoppedWaiting reports whether a wait ended because it was stopped, rather
// than because the run finished or following it failed.
func stoppedWaiting(ctx context.Context, err error) bool {
	return errors.Is(err, errStoppedWaiting) || ctx.Err() != nil
}

// interruptNote says what an interrupted command stopped, on stderr: under
// -o json stdout holds the result and nothing else, and output.Warn writes to
// stdout.
func interruptNote(msg string) {
	fmt.Fprintln(output.Err, output.WarningStyle.Render("  "+output.Glyphs().Warn+" "+msg))
}

// cancelTimeout bounds the cancel a stopped command sends for its run.
const cancelTimeout = 15 * time.Second

// cancelStartedRun cancels a test run the command started and has stopped
// waiting for. It returns the status to report the run as, CANCELLED or
// INTERRUPTED when the cancel failed, and a note saying which. The command's
// context may be what was cancelled, so the request gets its own.
func cancelStartedRun(id string) (status, note string) {
	ctx, cancel := context.WithTimeout(context.Background(), cancelTimeout)
	defer cancel()
	if err := apiClient.CancelTest(ctx, id); err != nil {
		return "INTERRUPTED", "the run could not be cancelled (" + err.Error() +
			"); cancel it with: kates test cancel " + output.Printable(id)
	}
	return "CANCELLED", "the run was cancelled"
}

// stopStartedRun ends a command that started test run id and was stopped
// while it waited: it cancels the run, says so, and returns errInterrupted.
func stopStartedRun(id string) error {
	_, note := cancelStartedRun(id)
	interruptNote("Stopped waiting for test " + output.Printable(truncID(id)) + ": " + note + ".")
	return errInterrupted
}

// stopFollowingDisruption ends a command that started disruption id and was
// stopped while it waited. The Kates API cannot cancel a running plan, so the
// plan runs to its end; this says so, and how to follow it.
func stopFollowingDisruption(id string) error {
	id = output.Printable(id)
	interruptNote("Stopped waiting for disruption " + id + ". It keeps running: the Kates API cannot cancel a running plan." +
		" Follow it with: kates disruption status " + id)
	return errInterrupted
}
