package cmd

import (
	"strings"

	"github.com/klster/kates-cli/client"
)

// TestCounts holds aggregated test status counts.
type TestCounts struct {
	Running int
	Pending int
	Done    int
	Failed  int
}

// CountStatuses tallies test statuses from a list of typed test runs.
func CountStatuses(tests []client.TestRun) TestCounts {
	var c TestCounts
	for _, t := range tests {
		switch strings.ToUpper(t.Status) {
		case "RUNNING":
			c.Running++
		case "PENDING":
			c.Pending++
		case "DONE", "COMPLETED":
			c.Done++
		case "FAILED", "ERROR":
			c.Failed++
		}
	}
	return c
}
