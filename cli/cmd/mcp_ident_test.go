package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// mcpTestTypes and mcpRunStatuses must list exactly the backend's enums.
func TestMCPRunEnumsMatchTheBackend(t *testing.T) {
	root := filepath.Join("..", "..", filepath.FromSlash(mcpJava))
	types := mcpJavaEnum(t, filepath.Join(root, "domain", "TestType.java"), "TestType")
	if fmt.Sprint(types) != fmt.Sprint(mcpTestTypes) {
		t.Errorf("TestType.java has %v, mcpTestTypes %v", types, mcpTestTypes)
	}
	statuses := mcpJavaEnum(t, filepath.Join(root, "domain", "TestResult.java"), "TaskStatus")
	if fmt.Sprint(statuses) != fmt.Sprint(mcpRunStatuses) {
		t.Errorf("TestResult.TaskStatus has %v, mcpRunStatuses %v", statuses, mcpRunStatuses)
	}
}

// preview_disruption's schema spells the topic rule differently from
// mcpKafkaTopicPattern; the two must accept the same names.
func TestMCPPreviewTopicPatternIsKafkas(t *testing.T) {
	preview := regexp.MustCompile(mcpPreviewTopicPattern)
	names := []string{
		"", "a", "A", "orders", "Orders.v1", "a_b-c.d", "0", ".", "..", "a b", "a/b", "é", "a\n",
		strings.Repeat("x", 249), strings.Repeat("x", 250),
	}
	for c := 0; c < 128; c++ {
		names = append(names, string(rune(c)), "a"+string(rune(c)))
	}
	for _, n := range names {
		if preview.MatchString(n) != mcpKafkaTopicRE.MatchString(n) {
			t.Errorf("%q: preview pattern %v, Kafka pattern %v", n, preview.MatchString(n), mcpKafkaTopicRE.MatchString(n))
		}
	}
}

// mcpShrink applies, each round, the first step that can still shrink, marks
// the call truncated only when the result did not fit, and reports whether it
// fits in the end.
func TestMCPShrinkOrder(t *testing.T) {
	type data struct {
		A []string `json:"a"`
		B []string `json:"b"`
	}
	big := func() []string {
		items := make([]string, 2000)
		for i := range items {
			items[i] = strings.Repeat("x", 100)
		}
		return items
	}
	var order []string
	var fitsSmall, fitsBig, fitsStuck bool
	fb := newMCPFakeBackend(t, "cluster-a")
	h := newMCPHarness(t, fb, withMCPTools(
		mcpTestTool("small", func(ctx context.Context, call *mcpCall, _ struct{}) (*data, error) {
			out := &data{A: []string{"a"}, B: []string{"b"}}
			fitsSmall = mcpShrink(call, out, func() bool { t.Error("a result that fits was shrunk"); return false })
			return out, nil
		}),
		mcpTestTool("big", func(ctx context.Context, call *mcpCall, _ struct{}) (*data, error) {
			out := &data{A: []string{"keep"}, B: big()}
			fitsBig = mcpShrink(call, out,
				func() bool { order = append(order, "a"); return false },
				func() bool { order = append(order, "b"); return mcpHalve(&out.B, 0) },
			)
			return out, nil
		}),
		mcpTestTool("stuck", func(ctx context.Context, call *mcpCall, _ struct{}) (*data, error) {
			out := &data{A: big()}
			fitsStuck = mcpShrink(call, out, func() bool { return false })
			return out, nil
		}),
	))

	if env := h.callOK("small", nil); env.Truncated || !fitsSmall {
		t.Errorf("small: truncated %v, fits %v", env.Truncated, fitsSmall)
	}
	env := h.callOK("big", nil)
	got := mcpData[data](t, env)
	if !env.Truncated || !fitsBig || len(got.A) != 1 || len(got.B) == 0 || len(got.B) == 2000 {
		t.Errorf("big: truncated %v, fits %v, a %d, b %d", env.Truncated, fitsBig, len(got.A), len(got.B))
	}
	if len(order) < 2 || order[0] != "a" || order[1] != "b" {
		t.Errorf("big: steps ran in order %v, want a then b each round", order)
	}
	if e := h.callErr("stuck", nil); e.Error.Code != mcpErrResultTooLarge || fitsStuck {
		t.Errorf("stuck: got %s and fits %v, want %s", e.Error.Code, fitsStuck, mcpErrResultTooLarge)
	}
}
