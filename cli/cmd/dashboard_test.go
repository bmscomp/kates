package cmd

import (
	"errors"
	"math"
	"regexp"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/charmbracelet/lipgloss"
)

// The Cluster Detail panel takes its verdict from /api/cluster/check. The
// check makes a cluster CRITICAL for offline partitions or a KRaft quorum with
// no leader, and WARNING for under-replicated partitions; the panel used to
// read only the under-replicated count, so a cluster with offline partitions
// showed a green tick.
func TestDashClusterContentReadsTheCheckVerdict(t *testing.T) {
	g := output.Glyphs()
	cases := []struct {
		name  string
		check client.ClusterHealthReport
		want  []string
		never []string
	}{
		{
			name: "offline partitions are critical",
			check: client.ClusterHealthReport{Status: "CRITICAL", Brokers: 3, Topics: 42, ControllerID: 3,
				PartitionHealth: client.PartitionHealthReport{Offline: 2}},
			want:  []string{g.Cross + " CRITICAL", g.Cross + " 2 offline"},
			never: []string{g.Check, "0 under-replicated"},
		},
		{
			name: "offline and under-replicated partitions together",
			check: client.ClusterHealthReport{Status: "CRITICAL", Brokers: 3,
				PartitionHealth: client.PartitionHealthReport{Offline: 2, UnderReplicated: 3}},
			want:  []string{g.Cross + " CRITICAL", g.Cross + " 2 offline", g.Warn + " 3 under-replicated"},
			never: []string{g.Check},
		},
		{
			name: "a KRaft quorum with no leader is critical",
			check: client.ClusterHealthReport{Status: "CRITICAL", Brokers: 3, ControllerID: 3,
				KraftQuorum: &client.KraftQuorumHealth{LeaderID: -1, Voters: 3, Issue: "NO_LEADER"}},
			want:  []string{g.Cross + " CRITICAL", g.Cross + " no KRaft leader"},
			never: []string{"Broker 3"},
		},
		{
			name: "a KRaft quorum the check could not describe has an unknown leader",
			check: client.ClusterHealthReport{Status: "WARNING", Brokers: 3, ControllerID: 3,
				KraftQuorum: &client.KraftQuorumHealth{LeaderID: -1, Issue: "UNAVAILABLE: TimeoutException"}},
			want:  []string{g.Warn + " WARNING", g.Warn + " KRaft leader unknown"},
			never: []string{g.Cross, "Broker 3"},
		},
		{
			name: "under-replicated partitions are a warning",
			check: client.ClusterHealthReport{Status: "WARNING", Brokers: 3,
				PartitionHealth: client.PartitionHealthReport{UnderReplicated: 4}},
			want:  []string{g.Warn + " WARNING", g.Warn + " 4 under-replicated"},
			never: []string{g.Check, g.Cross},
		},
		{
			name: "healthy",
			check: client.ClusterHealthReport{Status: "HEALTHY", Brokers: 3, Topics: 42, ControllerID: 4,
				KraftQuorum: &client.KraftQuorumHealth{LeaderID: 3, Voters: 3, HasLeader: true}},
			want:  []string{g.Check + " HEALTHY", g.Check + " all in sync", "KRaft leader 3", "42"},
			never: []string{g.Cross, g.Warn, "Broker 4"},
		},
		{
			name: "the largest KRaft leader id still fits the panel",
			check: client.ClusterHealthReport{Status: "HEALTHY", Brokers: 3,
				KraftQuorum: &client.KraftQuorumHealth{LeaderID: math.MaxInt32, Voters: 3, HasLeader: true}},
			want:  []string{"KRaft leader 2147483647"},
			never: []string{"Broker 0"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := dashClusterContent(&tc.check, nil)
			// 36 columns: what a panel holds in an 80-column terminal.
			for _, l := range strings.Split(raw, "\n") {
				if lipgloss.Width(l) > 36 {
					t.Errorf("row wider than an 80-column panel (%d): %q", lipgloss.Width(l), stripAnsi(l))
				}
			}
			out := stripAnsi(raw)
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("want %q in:\n%s", w, out)
				}
			}
			for _, n := range tc.never {
				if strings.Contains(out, n) {
					t.Errorf("did not want %q in:\n%s", n, out)
				}
			}
		})
	}
}

func TestDashClusterContentWithoutACheck(t *testing.T) {
	out := stripAnsi(dashClusterContent(nil, errors.New("connection refused")))
	if !strings.Contains(out, "Cluster data unavailable") {
		t.Errorf("want the unavailable note, got:\n%s", out)
	}
	if g := output.Glyphs(); strings.Contains(out, g.Check) {
		t.Errorf("an unknown cluster must not show %q:\n%s", g.Check, out)
	}
}

var aZero = regexp.MustCompile(`(^|\s)0(\s|$)`)

// A run list the API did not return is unknown, not empty: its counts show —.
func TestDashSummaryContentShowsUnknownCountsAsDashes(t *testing.T) {
	out := stripAnsi(dashSummaryContent(nil, errors.New("connection refused"), 20))
	if aZero.MatchString(out) {
		t.Errorf("a failed run list must not count 0:\n%s", out)
	}
	if got := strings.Count(out, "—"); got != 5 {
		t.Errorf("want — for Running, Pending, Done, Failed and Total, got %d in:\n%s", got, out)
	}

	var runs []client.TestRun
	for status, n := range map[string]int{"RUNNING": 1, "PENDING": 2, "DONE": 3, "FAILED": 4} {
		for range n {
			runs = append(runs, client.TestRun{Status: status})
		}
	}
	out = stripAnsi(dashSummaryContent(&client.PagedTests{TotalItems: 214, Content: runs}, nil, 20))
	for _, w := range []string{"1 Running", "2 Pending", "3 Done", "4 Failed", "214 Total tests"} {
		if !strings.Contains(out, w) {
			t.Errorf("want %q in:\n%s", w, out)
		}
	}
	if strings.Contains(out, "—") {
		t.Errorf("a returned run list has no unknown counts:\n%s", out)
	}
}

// A STOPPING run has not ended: it belongs with the active runs and never
// gets the completed tick.
func TestDashRunsContentKeepsStoppingRunsActive(t *testing.T) {
	g := output.Glyphs()
	paged := &client.PagedTests{Content: []client.TestRun{
		{ID: "e2d4f6a8", TestType: "ENDURANCE", Status: "STOPPING"},
		{ID: "0c55a7f1", TestType: "SPIKE", Status: "PENDING"},
		{ID: "9a8b7c6d", TestType: "LOAD", Status: "DONE"},
		{ID: "5e4f3a2b", TestType: "INTEGRITY", Status: "FAILED"},
		{ID: "ab12cd34", TestType: "VOLUME", Status: "CANCELLED"},
	}}
	active, recent, _, _ := dashRunsContent(paged, nil)
	active, recent = stripAnsi(active), stripAnsi(recent)

	for _, w := range []string{"e2d4f6a8", "stopping", "0c55a7f1", "pending"} {
		if !strings.Contains(active, w) {
			t.Errorf("want %q among the active runs:\n%s", w, active)
		}
	}
	for _, id := range []string{"9a8b7c6d", "5e4f3a2b", "ab12cd34"} {
		if strings.Contains(active, id) {
			t.Errorf("finished run %s is not active:\n%s", id, active)
		}
	}
	if strings.Contains(recent, "e2d4f6a8") {
		t.Errorf("a STOPPING run is not completed:\n%s", recent)
	}
	// The icon comes before the test type; the status badge after it.
	for _, row := range [][]string{
		{g.Check, "LOAD", "9a8b7c6d"},
		{g.Cross, "INTEGRITY", "5e4f3a2b"},
		{g.Ring, "VOLUME", "ab12cd34"},
	} {
		if !tableHasRow(recent, row) {
			t.Errorf("want a recent row %q in:\n%s", row, recent)
		}
	}
}

func TestDashRunsContentWithoutARunList(t *testing.T) {
	active, recent, _, _ := dashRunsContent(nil, errors.New("connection refused"))
	for name, out := range map[string]string{"active": stripAnsi(active), "recent": stripAnsi(recent)} {
		if !strings.Contains(out, "Run list unavailable") {
			t.Errorf("%s: want the unavailable note, got:\n%s", name, out)
		}
		if strings.Contains(out, "No ") {
			t.Errorf("%s: an unknown run list is not an empty one:\n%s", name, out)
		}
	}
}
