package cmd

import "strings"

// A test run's final statuses, as every poller reads them.
//
// The Kates API stores a run DONE or FAILED (TestResult.TaskStatus); a
// cancelled run is FAILED with the reason "Cancelled by user". COMPLETED,
// ERROR and CANCELLED are what older backends and the CLI's own result rows
// say. Gate and benchmark used to stop only on DONE or FAILED, where watch
// and apply stopped on the other two as well, so a run that ended one way
// was still polled by the other command until its timeout. Case does not
// matter.

// terminalRunStatus reports whether status is one a run will not leave, so
// a poller that follows the run can stop.
func terminalRunStatus(status string) bool {
	switch strings.ToUpper(status) {
	case "DONE", "COMPLETED", "FAILED", "ERROR", "CANCELLED":
		return true
	}
	return false
}

// succeededRunStatus reports whether a terminal status means the run went to
// its end: DONE or COMPLETED.
func succeededRunStatus(status string) bool {
	switch strings.ToUpper(status) {
	case "DONE", "COMPLETED":
		return true
	}
	return false
}

// isFailedStatus reports whether a terminal status means the run did not go
// to its end: FAILED, ERROR or CANCELLED.
func isFailedStatus(status string) bool {
	switch strings.ToUpper(status) {
	case "FAILED", "ERROR", "CANCELLED":
		return true
	}
	return false
}
