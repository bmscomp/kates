package cmd

import (
	"fmt"
	"strconv"

	"github.com/google/jsonschema-go/jsonschema"
)

// One cursor vocabulary for every paged tool (plan D6): the caller passes
// offset, the first item to return counted from 0, and limit, the most items
// to return; a result that leaves items after its page carries nextOffset,
// the offset of the next page, and leaves it out on the last page. A tool
// may return fewer than limit items when they would not fit one result; the
// caller then pages on with nextOffset all the same.
//
// list_runs (was page and size), cluster_topology (was from_partition and
// nextFromPartition), consumer_group_lag, kates_activity, disruption_report,
// list_topics, list_consumer_groups and security_evidence all use it.

// mcpPageSchema bounds the offset and limit properties of an input schema.
func mcpPageSchema(s *jsonschema.Schema, maxOffset, maxLimit int) {
	mcpBounds(s.Properties["offset"], 0, float64(maxOffset))
	mcpBounds(s.Properties["limit"], 1, float64(maxLimit))
}

// mcpPageArgs checks offset and limit and returns the limit to use: def when
// the caller sent none.
func mcpPageArgs(offset, limit, maxOffset, def, maxLimit int) (int, error) {
	if offset < 0 || offset > maxOffset {
		return 0, mcpInvalidArgument(fmt.Sprintf("offset must be from 0 to %d.", maxOffset), strconv.Itoa(offset))
	}
	if limit == 0 {
		limit = def
	}
	if limit < 1 || limit > maxLimit {
		return 0, mcpInvalidArgument(fmt.Sprintf("limit must be from 1 to %d.", maxLimit), strconv.Itoa(limit))
	}
	return limit, nil
}

// mcpNextOffset is the offset after a page of returned items starting at
// offset, or nil when total items leave none after it.
func mcpNextOffset(offset, returned, total int) *int {
	next := offset + returned
	if next >= total {
		return nil
	}
	return &next
}

// mcpPastEnd refuses an offset past the last of total items, as every paged
// tool does, so a caller that pages past the end hears so instead of reading
// an empty page as "no more".
func mcpPastEnd(offset, total int) error {
	if offset > 0 && offset >= total {
		return mcpInvalidArgument(fmt.Sprintf("offset is past the last item (%d in all); page with nextOffset of the previous result, or start again from 0.", total), strconv.Itoa(offset))
	}
	return nil
}

// mcpBackendRange reads items [offset, offset+limit) from a backend endpoint
// paged by page number and size: one page of size limit, and the next one too
// when offset is not a multiple of limit. fetch returns a page's items and the
// total the backend reports.
func mcpBackendRange[T any](offset, limit int, fetch func(page, size int) ([]T, int64, error)) ([]T, int64, error) {
	page, skip := offset/limit, offset%limit
	items, total, err := fetch(page, limit)
	if err != nil {
		return nil, 0, err
	}
	if skip > 0 && len(items) == limit && int64((page+1)*limit) < total {
		more, t, err := fetch(page+1, limit)
		if err != nil {
			return nil, 0, err
		}
		items, total = append(items, more...), t
	}
	if skip >= len(items) {
		return []T{}, total, nil
	}
	items = items[skip:]
	if len(items) > limit {
		items = items[:limit]
	}
	return items, total, nil
}
