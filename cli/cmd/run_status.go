package cmd

import "strings"

// terminalRunStatus reports whether a test run's status is one it will not
// leave, so a poller that follows the run can stop. The Kates API stores a
// run DONE or FAILED (TestResult.TaskStatus); a cancelled run is FAILED with
// the reason "Cancelled by user". COMPLETED, ERROR and CANCELLED are what
// older backends and the CLI's own result rows say, and gate and benchmark
// used to stop only on DONE or FAILED, where watch and apply stopped on the
// other two as well, so a run that ended one way was still polled by the
// other command until its timeout. Case does not matter.
func terminalRunStatus(status string) bool {
	switch strings.ToUpper(status) {
	case "DONE", "COMPLETED", "FAILED", "ERROR", "CANCELLED":
		return true
	}
	return false
}
