package cmd

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func TestMCPBackendRange(t *testing.T) {
	all := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	var calls []string
	fetch := func(page, size int) ([]int, int64, error) {
		calls = append(calls, strings.Repeat("p", page+1))
		start, end := min(page*size, len(all)), min(page*size+size, len(all))
		return all[start:end], int64(len(all)), nil
	}
	for _, tc := range []struct {
		offset, limit int
		want          []int
		reads         int
	}{
		{0, 4, []int{0, 1, 2, 3}, 1},
		{4, 4, []int{4, 5, 6, 7}, 1},
		{2, 4, []int{2, 3, 4, 5}, 2},
		{9, 4, []int{9, 10}, 1},
		{6, 4, []int{6, 7, 8, 9}, 2},
		{10, 3, []int{10}, 1},
		{11, 3, []int{}, 1},
	} {
		calls = nil
		got, total, err := mcpBackendRange(tc.offset, tc.limit, fetch)
		if err != nil || total != 11 || !slices.Equal(got, tc.want) || len(calls) != tc.reads {
			t.Errorf("offset %d limit %d: %v (total %d, %d reads, err %v), want %v in %d reads", tc.offset, tc.limit, got, total, len(calls), err, tc.want, tc.reads)
		}
	}
	if n := mcpNextOffset(4, 4, 11); n == nil || *n != 8 {
		t.Errorf("nextOffset = %v, want 8", n)
	}
	if n := mcpNextOffset(8, 3, 11); n != nil {
		t.Errorf("nextOffset on the last page = %d", *n)
	}
}

// TestMCPCaveatsExemptFromBudget: the caveats a result carries do not count
// against the size cap, so caveats never make a result shrink or fail.
func TestMCPCaveatsExemptFromBudget(t *testing.T) {
	refs := []mcpCaveatRef{{ID: "a", Text: strings.Repeat("x", 100)}, {ID: "b", Text: "y"}}
	withRefs, _ := json.Marshal(mcpResult[any]{Caveats: refs, Data: map[string]int{"n": 1}})
	without, _ := json.Marshal(mcpResult[any]{Caveats: []mcpCaveatRef{}, Data: map[string]int{"n": 1}})
	if got, want := mcpBudgetBytes(withRefs, refs), mcpWireBytes(without); got != want {
		t.Errorf("budget with caveats = %d, want %d, the budget without them", got, want)
	}
	if got := mcpBudgetBytes(without, nil); got != mcpWireBytes(without) {
		t.Errorf("budget without caveats = %d", got)
	}
}

func TestMCPConsumerGroupLagPages(t *testing.T) {
	fb := newMCPFakeBackend(t, "cluster-a")
	fb.JSON("GET", "/api/cluster/groups/orders-consumer", http.StatusOK, mcpClusterGroupBody("orders-consumer", "Stable", 3,
		map[string][]int64{"orders": {0, 500, 20, 900, 0, 7}}))
	fb.JSON("GET", "/api/kafka/topics/orders", http.StatusOK, mcpClusterTopicBody("orders", 6, 3, nil))
	h := newMCPHarness(t, fb)

	got := mcpData[mcpConsumerGroupLagOut](t, h.callOK("consumer_group_lag", map[string]any{"group": "orders-consumer", "limit": 2}))
	if mcpLagPartitions(got.Partitions) != "orders/3:900@0 orders/1:500@1" || got.NextOffset == nil || *got.NextOffset != 2 {
		t.Fatalf("first page: %s next=%v", mcpLagPartitions(got.Partitions), got.NextOffset)
	}
	got = mcpData[mcpConsumerGroupLagOut](t, h.callOK("consumer_group_lag", map[string]any{"group": "orders-consumer", "limit": 2, "offset": 4}))
	if mcpLagPartitions(got.Partitions) != "orders/0:0@0 orders/4:0@1" || got.NextOffset != nil || got.Offset != 4 || got.PartitionCount != 6 {
		t.Errorf("last page: %s next=%v offset=%d", mcpLagPartitions(got.Partitions), got.NextOffset, got.Offset)
	}
	if e := h.callErr("consumer_group_lag", map[string]any{"group": "orders-consumer", "offset": 6}); e.Error.Code != mcpErrInvalidArgument {
		t.Errorf("offset past the last partition: %s", e.Error.Code)
	}
	assertReadOnly(t, fb.Requests())
}

func TestMCPKatesActivityPagesAudit(t *testing.T) {
	fb := newMCPFakeBackend(t, "cluster-a")
	mcpServeRuns(fb, nil)
	fb.JSON("GET", "/api/disruptions", http.StatusOK, map[string]any{"items": []any{}})
	rows := []map[string]any{}
	for i := 5; i >= 1; i-- {
		rows = append(rows, map[string]any{"id": i, "action": "READ", "eventType": "test", "target": "0000000c", "timestamp": "2026-09-25T11:40:00Z"})
	}
	mcpServePaged(fb, "/api/audit", rows)
	h := newMCPHarness(t, fb, withMCPClock(newMCPFakeClock().Now))

	got := mcpData[mcpActivityOut](t, h.callOK("kates_activity", map[string]any{"limit": 2}))
	if len(got.Audit.Rows) != 2 || got.Audit.Rows[0].ID != 5 || got.Audit.NextOffset == nil || *got.Audit.NextOffset != 2 {
		t.Fatalf("first page: %+v", got.Audit)
	}
	if got.RunningTests.NextOffset != nil || got.TestsSince.NextOffset != nil || got.Disruptions.NextOffset != nil {
		t.Error("an empty list has a next page")
	}
	got = mcpData[mcpActivityOut](t, h.callOK("kates_activity", map[string]any{"limit": 2, "offset": 3}))
	if len(got.Audit.Rows) != 2 || got.Audit.Rows[0].ID != 2 || got.Audit.Rows[1].ID != 1 || got.Audit.NextOffset != nil || got.Offset != 3 {
		t.Errorf("last page: %+v", got.Audit)
	}
	if e := h.callErr("kates_activity", map[string]any{"offset": mcpActivityMaxOffset + 1}); e.Error.Code != mcpErrInvalidArgument {
		t.Errorf("offset over the cap: %s", e.Error.Code)
	}
}
