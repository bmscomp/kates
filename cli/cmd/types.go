package cmd

import "encoding/json"

// PagedResponse is the standard paginated response from the KATES API.
type PagedResponse struct {
	Content    []map[string]interface{} `json:"content"`
	Page       int                      `json:"page"`
	Size       int                      `json:"size"`
	TotalItems int                      `json:"totalItems"`
	TotalPages int                      `json:"totalPages"`
}

// TestCounts holds aggregated test status counts.
type TestCounts struct {
	Running int
	Pending int
	Done    int
	Failed  int
}

// CountStatuses tallies test statuses from a list of test maps.
func CountStatuses(tests []map[string]interface{}) TestCounts {
	var c TestCounts
	for _, t := range tests {
		switch mapStr(t, "status") {
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

// ParsePaged unmarshals raw JSON into a PagedResponse.
func ParsePaged(data json.RawMessage) (PagedResponse, error) {
	var p PagedResponse
	err := json.Unmarshal(data, &p)
	return p, err
}
