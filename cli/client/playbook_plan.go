package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// PlaybookPlan returns the disruption plan a named playbook runs, as the
// backend resolves it from the playbook's YAML.
//
// The plan comes back as raw JSON rather than a struct so that it can go
// straight to RunDryRun: a struct would drop every field it does not name,
// and the dry run would then preview a different plan from the one the
// playbook runs.
func (c *Client) PlaybookPlan(ctx context.Context, name string) (json.RawMessage, error) {
	// pathf keeps the name one path segment and refuses "", "." and "..":
	// "" would fetch the playbook list and ".." GET /api/disruptions, a list
	// of reports, either of which would come back as if it were a plan.
	path, err := pathf("/api/disruptions/playbooks/%s", name)
	if err != nil {
		return nil, err
	}

	plan, err := get[json.RawMessage](c, ctx, path)
	if err != nil {
		// A backend without this endpoint still serves POST on the same path
		// (it runs the playbook), so a GET there is refused as 405, not 404.
		var httpErr *HTTPError
		if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusMethodNotAllowed {
			return nil, fmt.Errorf("this Kates backend cannot return a playbook's plan (GET %s is not supported): %w", path, err)
		}
		return nil, err
	}
	if trimmed := bytes.TrimSpace(plan); len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("GET %s returned %.40q, not a disruption plan", path, plan)
	}
	return plan, nil
}
