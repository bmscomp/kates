package cmd

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// The flags several commands share, registered once each.
//
// --records, --timeout, --wait and -f/--file used to be declared by hand in
// each command, and they drifted: gate's --timeout took whole seconds and
// benchmark's a duration, and four --wait flags described the same thing four
// ways. Each helper here registers one name with one type, one help text and
// the shorthand, so `kates gate --timeout 10m` and `kates benchmark --timeout
// 10m` read the same; only the default is the command's. TestSharedFlagsAgree
// holds every command to it.

// sharedFlagAnnotation marks a flag one of these helpers registered.
const sharedFlagAnnotation = "kates/shared-flag"

// The one help text of each shared flag.
const (
	recordsFlagHelp    = "Number of records each test run sends"
	timeoutFlagHelp    = "How long to wait for a run to finish: a duration such as 10m, or a number of seconds"
	waitFlagHelp       = "Wait for each run the command starts to finish and report how it ended; Ctrl-C cancels the run"
	outputFileFlagHelp = "Write to this file instead of the command's default; - writes to stdout"
)

// addRecordsFlag registers --records with def as its default.
func addRecordsFlag(cmd *cobra.Command, p *int, def int) {
	cmd.Flags().IntVar(p, "records", def, recordsFlagHelp)
	markShared(cmd, "records")
}

// addTimeoutFlag registers --timeout, read with parseTimeoutFlag: a duration
// such as 10m, or a plain number of seconds as gate took it before it shared
// the flag, so `--timeout 300` keeps working.
func addTimeoutFlag(cmd *cobra.Command, p *string, def string) {
	cmd.Flags().StringVar(p, "timeout", def, timeoutFlagHelp)
	markShared(cmd, "timeout")
}

// addWaitFlag registers --wait with def as its default.
func addWaitFlag(cmd *cobra.Command, p *bool, def bool) {
	cmd.Flags().BoolVar(p, "wait", def, waitFlagHelp)
	markShared(cmd, "wait")
}

// addOutputFileFlag registers -f/--file for a command that writes a file.
// The default stays "", and the command says what it does without one.
func addOutputFileFlag(cmd *cobra.Command, p *string) {
	cmd.Flags().StringVarP(p, "file", "f", "", outputFileFlagHelp)
	markShared(cmd, "file")
}

func markShared(cmd *cobra.Command, name string) {
	_ = cmd.Flags().SetAnnotation(name, sharedFlagAnnotation, []string{"true"})
}

// parseTimeoutFlag reads a --timeout: a duration such as 10m, or a plain
// number of seconds. Zero and negative values are refused: a wait of nothing
// is not one.
func parseTimeoutFlag(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if secs, err := strconv.Atoi(s); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("--timeout %q is not a duration such as 10m, or a number of seconds", s)
	}
	return d, nil
}
