package cmd

// mcpSection is the head of a part of a result that the backend may fail to
// answer while the rest of the result is still returned: whether the part
// could be read and, when not, the error code why.
//
// It is embedded where a part has exactly these two fields first, with no
// description of its own (assess_run's brokerSkew and advisor). The other
// parts that say whether they could be read keep their own fields, because
// the output schema publishes what differs: available carries a description
// saying what false means for that part (cluster_overview's alertRules,
// cluster_topology's topology, list_chaos_catalog's sections,
// disruption_report's parts, security_evidence's headlines, kates_activity's
// lists, assess_run's regression), or a field comes between them
// (assess_run's comparison puts reason before errorCode), or the flag is
// called computed (assess_run's band). Embedding there would change the
// published output schema; the golden test in mcp_golden_test.go pins it.
type mcpSection struct {
	Available bool         `json:"available"`
	ErrorCode mcpErrorCode `json:"errorCode,omitempty"`
}

// mcpShrink leaves detail out of out, which the result carries, until the
// result fits under the size cap (mcpCall.Fits). Each round marks the call
// truncated and applies the first step that still finds something to leave
// out; a step reports whether it could. The order of steps is the tool's
// order of what goes first. When no step can shrink, the guard refuses the
// result as too large. It reports whether the result fits.
//
// A tool whose caller cannot page it (one that takes no arguments, or one
// id) uses it rather than fail with KATES_RESULT_TOO_LARGE.
func mcpShrink(call *mcpCall, out any, steps ...func() bool) bool {
	for !call.Fits(out) {
		call.MarkTruncated()
		shrunk := false
		for _, step := range steps {
			if step() {
				shrunk = true
				break
			}
		}
		if !shrunk {
			return false
		}
	}
	return true
}

// mcpHalve cuts *s to half its length, keeping at least keep items, and
// reports whether it cut anything.
func mcpHalve[T any](s *[]T, keep int) bool {
	n := len(*s)
	if n <= keep || n == 0 {
		return false
	}
	*s = (*s)[:max(n/2, keep)]
	return true
}

// mcpHalveEach halves one list field of every item, and reports whether it
// cut any.
func mcpHalveEach[S, T any](items []S, field func(*S) *[]T, keep int) bool {
	cut := false
	for i := range items {
		cut = mcpHalve(field(&items[i]), keep) || cut
	}
	return cut
}

// mcpClearEach empties one fenced field in every item, and reports whether
// any was set.
func mcpClearEach[T any](items []T, field func(*T) *mcpUntrusted) bool {
	cleared := false
	for i := range items {
		if f := field(&items[i]); *f != "" {
			*f, cleared = "", true
		}
	}
	return cleared
}

// mcpShrinkList is one list mcpHalveLongest may cut: its length, and a
// function that cuts it to n items.
type mcpShrinkList struct {
	n   int
	cut func(n int)
}

// mcpHalveLongest halves the longest of lists (the first of the longest on a
// tie) and reports whether it cut anything; it cuts nothing when every list
// has at most keep items.
func mcpHalveLongest(keep int, lists ...mcpShrinkList) bool {
	if len(lists) == 0 {
		return false
	}
	longest := 0
	for i, l := range lists {
		if l.n > lists[longest].n {
			longest = i
		}
	}
	if lists[longest].n <= keep {
		return false
	}
	lists[longest].cut(lists[longest].n / 2)
	return true
}

// mcpShrinkOf is an mcpShrinkList entry for *s.
func mcpShrinkOf[T any](s *[]T) mcpShrinkList {
	return mcpShrinkList{n: len(*s), cut: func(n int) { *s = (*s)[:n] }}
}
