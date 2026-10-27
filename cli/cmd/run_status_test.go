package cmd

import "testing"

// Every poller stops on the same statuses. gate and benchmark used to stop on
// DONE and FAILED only, watch and apply on COMPLETED and ERROR as well, so a
// run that ended one way was polled by the other command until its timeout.
func TestTerminalRunStatus(t *testing.T) {
	for status, want := range map[string]bool{
		"DONE": true, "COMPLETED": true, "FAILED": true, "ERROR": true, "CANCELLED": true,
		"done": true, "Failed": true,
		"PENDING": false, "RUNNING": false, "STOPPING": false, "": false, "INTERRUPTED": false,
	} {
		if got := terminalRunStatus(status); got != want {
			t.Errorf("terminalRunStatus(%q) = %v, want %v", status, got, want)
		}
	}
	for status, want := range map[string]bool{"FAILED": true, "ERROR": true, "CANCELLED": true, "DONE": false, "COMPLETED": false} {
		if got := isFailedStatus(status); got != want {
			t.Errorf("isFailedStatus(%q) = %v, want %v", status, got, want)
		}
	}
}
