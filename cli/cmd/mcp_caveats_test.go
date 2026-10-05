package cmd

import (
	"bufio"
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpAllCaveatIDs lists every constant, so a constant added without an entry
// in the catalogue fails here rather than at the first call that names it.
// Each tool group lists its own in its test file (mcpCaveatIDsCluster, …).
var mcpAllCaveatIDs = mcpJoinCaveatIDs(mcpCaveatIDsCore, mcpCaveatIDsCluster, mcpCaveatIDsRuns, mcpCaveatIDsChaos, mcpCaveatIDsSecurity)

func mcpJoinCaveatIDs(lists ...[]mcpCaveatID) []mcpCaveatID {
	var all []mcpCaveatID
	for _, l := range lists {
		all = append(all, l...)
	}
	return all
}

var mcpCaveatIDsCore = []mcpCaveatID{
	mcpCaveatLoadSingleProducer,
	mcpCaveatReaperDeadline,
	mcpCaveatMergedSpecOnly,
	mcpCaveatPentestConfigOnly,
	mcpCaveatCVEFixedList,
	mcpCaveatSecurityTrendInMemory,
	mcpCaveatTuningOneMeasurement,
	mcpCaveatTrendsMixSpecs,
	mcpCaveatMinISRBrokerLevel,
	mcpCaveatPrometheusUnreachable,
	mcpCaveatChaosTimesApproximate,
	mcpCaveatAlertRulesNotFiring,
	mcpCaveatClusterDataCached,
}

func TestMCPCaveatCatalogue(t *testing.T) {
	if len(mcpCaveatIndex) != len(mcpCaveats) {
		t.Errorf("the catalogue has %d entries but %d distinct ids", len(mcpCaveats), len(mcpCaveatIndex))
	}
	if len(mcpCaveats) != len(mcpAllCaveatIDs) {
		t.Errorf("the catalogue has %d entries, the constants %d", len(mcpCaveats), len(mcpAllCaveatIDs))
	}
	idRE := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	for _, id := range mcpAllCaveatIDs {
		c, ok := mcpCaveatIndex[id]
		if !ok {
			t.Errorf("caveat %q has no catalogue entry", id)
			continue
		}
		if !idRE.MatchString(string(id)) {
			t.Errorf("caveat id %q is not kebab-case", id)
		}
		if c.Text == "" || len(c.Refs) == 0 {
			t.Errorf("caveat %q needs text and at least one source ref", id)
		}
		if strings.ContainsAny(c.Text, "«»") {
			t.Errorf("caveat %q: fence markers do not belong in caveat text", id)
		}
	}
}

// TestMCPCaveatRetiredIDs: an id whose caveat changed meaning stays retired,
// so a client keying on it never reads the new meaning under the old id.
func TestMCPCaveatRetiredIDs(t *testing.T) {
	for _, id := range []mcpCaveatID{"reaper-30-minutes", "scenario-base-spec-only", "recovery-times-from-request"} {
		if _, ok := mcpCaveatIndex[id]; ok {
			t.Errorf("caveat id %q is retired; give a changed caveat a new id", id)
		}
	}
}

func TestMCPPlannedDurationMs(t *testing.T) {
	for _, tt := range []struct {
		typ        string
		durationMs int64
		want       int64
		ok         bool
	}{
		{"LOAD", 600_000, 600_000, true},
		{"ENDURANCE", 3_600_000, 3_600_000, true},
		{"INTEGRITY", 900_000, 1_800_000, true},
		{"LOAD", -5, 0, true},
		// A scenario's stored spec is not validated: the double holds at the
		// largest value rather than wrapping to a short run.
		{"INTEGRITY", math.MaxInt64/2 + 1, math.MaxInt64, true},
		{"INTEGRATION_CDC", 600_000, 0, false},
	} {
		got, ok := mcpPlannedDurationMs(tt.typ, tt.durationMs)
		if got != tt.want || ok != tt.ok {
			t.Errorf("mcpPlannedDurationMs(%s, %d) = %d, %v; want %d, %v", tt.typ, tt.durationMs, got, ok, tt.want, tt.ok)
		}
	}
}

// TestMCPReaperLimitsMatchTheBackend holds mcpReaperMaxDurationMs to
// kates.engine.max-duration-ms as Kates ships it: application.properties, and
// the @ConfigProperty fallbacks of the reaper and of the orchestrator, which
// refuses a longer run. Neither the kates chart nor kates/k8s sets it.
func TestMCPReaperLimitsMatchTheBackend(t *testing.T) {
	read := func(path string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	want := strconv.Itoa(mcpReaperMaxDurationMs)
	props := read("kates/src/main/resources/application.properties")
	if m := regexp.MustCompile(`(?m)^kates\.engine\.max-duration-ms=(.*)$`).FindStringSubmatch(props); m == nil || strings.TrimSpace(m[1]) != want {
		t.Errorf("application.properties sets kates.engine.max-duration-ms as %q; the tools assume %s", m, want)
	}
	fallback := regexp.MustCompile(`name = "kates\.engine\.max-duration-ms", defaultValue = "([^"]*)"`)
	for _, f := range []string{"engine/TestTimeoutReaper.java", "engine/TestOrchestrator.java"} {
		if m := fallback.FindStringSubmatch(read(mcpJava + f)); m == nil || m[1] != want {
			t.Errorf("%s falls back to %q for kates.engine.max-duration-ms; the tools assume %s", f, m, want)
		}
	}
	for _, f := range []string{"charts/kates/values.yaml", "charts/kates/templates/configmap.yaml", "kates/k8s/configmap.yaml"} {
		if s := read(f); strings.Contains(s, "max-duration") || strings.Contains(s, "MAX_DURATION") {
			t.Errorf("%s sets the longest run; the tools assume the %s ms application.properties ships", f, want)
		}
	}
}

// TestMCPCaveatRefsExist checks each source ref against the repository: the
// file exists and every cited line is inside it. It cannot check that the
// text is still true, but it fails when code moves far enough to make a ref
// point at nothing.
func TestMCPCaveatRefsExist(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, c := range mcpCaveats {
		for _, ref := range c.Refs {
			path, ranges, ok := mcpParseRef(ref)
			if !ok {
				t.Errorf("%s: ref %q is not path:lines", c.ID, ref)
				continue
			}
			lines, err := mcpCountLines(filepath.Join(root, filepath.FromSlash(path)))
			if err != nil {
				t.Errorf("%s: ref %q: %v", c.ID, ref, err)
				continue
			}
			for _, r := range ranges {
				if r.lo < 1 || r.hi < r.lo || r.hi > lines {
					t.Errorf("%s: ref %q cites lines %s, the file has %d", c.ID, ref, r.part, lines)
				}
			}
		}
	}
}

// TestMCPCaveatRefAnchors checks each ref made with mcpAnchoredRef against
// the code it cites: every anchor is in the cited lines, and every range
// holds one. TestMCPCaveatRefsExist passes a ref whose lines still exist
// even when the code it meant has moved and other code sits there, as
// happened to the TestOrchestrator.java refs; here the ref fails instead. To
// fix it, find the code the caveat means and re-point the ref.
func TestMCPCaveatRefAnchors(t *testing.T) {
	root := filepath.Join("..", "..")
	files := map[string][]string{}
	anchored := 0
	for _, c := range mcpCaveats {
		for _, ref := range c.Refs {
			anchors, ok := mcpRefAnchors[ref]
			if !ok {
				continue
			}
			anchored++
			// Every caveat citing these lines recorded its anchors; check each once.
			anchors = slices.Compact(slices.Sorted(slices.Values(anchors)))
			path, ranges, ok := mcpParseRef(ref)
			if !ok || len(anchors) == 0 || slices.Contains(anchors, "") {
				t.Errorf("%s: anchored ref %q needs the form path:lines and anchors that are not empty", c.ID, ref)
				continue
			}
			lines, ok := files[path]
			if !ok {
				b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
				if err != nil {
					t.Errorf("%s: ref %q: %v", c.ID, ref, err)
					continue
				}
				lines = strings.Split(string(b), "\n")
				files[path] = lines
			}
			mcpCheckAnchors(t, c.ID, ref, lines, ranges, anchors)
		}
	}
	if anchored == 0 {
		t.Error("no caveat ref has anchors")
	}
}

// mcpCheckAnchors reports each anchor missing from the lines ref cites, and
// each range that holds none of them.
func mcpCheckAnchors(t *testing.T, id mcpCaveatID, ref string, lines []string, ranges []mcpRefRange, anchors []string) {
	t.Helper()
	found := make([]bool, len(anchors))
	for _, r := range ranges {
		if r.lo < 1 || r.hi < r.lo || r.hi > len(lines) {
			continue // TestMCPCaveatRefsExist reports it
		}
		cited := strings.Join(lines[r.lo-1:r.hi], "\n")
		held := false
		for i, a := range anchors {
			if strings.Contains(cited, a) {
				found[i], held = true, true
			}
		}
		if !held {
			t.Errorf("%s: ref %q: lines %s hold none of its anchors %q", id, ref, r.part, anchors)
		}
	}
	for i, a := range anchors {
		if !found[i] {
			t.Errorf("%s: ref %q: %q is not in the lines it cites", id, ref, a)
		}
	}
}

// mcpRefRange is one part of a ref's lines, a line or a range: as written,
// and as its first and last line.
type mcpRefRange struct {
	part   string
	lo, hi int
}

var mcpRefRE = regexp.MustCompile(`^([^:]+):([0-9]+(?:-[0-9]+)?(?:,[0-9]+(?:-[0-9]+)?)*)$`)

// mcpParseRef splits a source ref into its path and the parts of its lines;
// ok is false when the ref is not path:lines.
func mcpParseRef(ref string) (path string, ranges []mcpRefRange, ok bool) {
	m := mcpRefRE.FindStringSubmatch(ref)
	if m == nil {
		return "", nil, false
	}
	for _, part := range strings.Split(m[2], ",") {
		bounds := strings.SplitN(part, "-", 2)
		lo, _ := strconv.Atoi(bounds[0])
		hi := lo
		if len(bounds) == 2 {
			hi, _ = strconv.Atoi(bounds[1])
		}
		ranges = append(ranges, mcpRefRange{part, lo, hi})
	}
	return m[1], ranges, true
}

func mcpCountLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		n++
	}
	return n, sc.Err()
}

func TestMCPCaveatsResource(t *testing.T) {
	fb := newMCPFakeBackend(t, "cluster-a")
	h := newMCPHarness(t, fb)
	ctx := context.Background()

	var listed bool
	for r, err := range h.session.Resources(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		if r.URI == mcpCaveatsURI {
			listed = r.MIMEType == "text/markdown"
		}
	}
	if !listed {
		t.Fatalf("%s is not listed as text/markdown", mcpCaveatsURI)
	}

	res, err := h.session.ReadResource(ctx, &mcp.ReadResourceParams{URI: mcpCaveatsURI})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Contents) != 1 || res.Contents[0].MIMEType != "text/markdown" {
		t.Fatalf("contents = %+v", res.Contents)
	}
	text := res.Contents[0].Text
	for _, c := range mcpCaveats {
		if !strings.Contains(text, "## "+string(c.ID)+"\n") || !strings.Contains(text, c.Text) {
			t.Errorf("the resource lacks caveat %s", c.ID)
		}
		for _, r := range c.Refs {
			if !strings.Contains(text, "`"+r+"`") {
				t.Errorf("the resource lacks ref %s of %s", r, c.ID)
			}
		}
	}
	// Reading a static resource touches no backend.
	if got := fb.Requests(); len(got) != 0 {
		t.Errorf("reading the caveats reached the backend: %v", mcpPaths(got))
	}
}

// TestMCPUnknownResourceCode pins the JSON-RPC code of a missing resource in
// each protocol era a client may negotiate. The specification shows -32002
// before 2026-07-28 and -32602 from it; go-sdk v1.8 sends -32602 (invalid
// params) in every era, following SEP-2164, and restores -32002 only under
// MCPGODEBUG=customresnotfounderrcode=1, for every session at once. Plan §4.2
// keeps the SDK's code; the test holds the server to it, so an SDK change
// shows here.
func TestMCPUnknownResourceCode(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25", "2025-06-18"} {
		t.Run(version, func(t *testing.T) {
			h := newMCPHarness(t, newMCPFakeBackend(t, "cluster-a"), withMCPProtocol(version))
			_, err := h.session.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "kates://nope"})
			var we *jsonrpc.Error
			if !errors.As(err, &we) {
				t.Fatalf("want a JSON-RPC error, got %v", err)
			}
			if we.Code != jsonrpc.CodeInvalidParams {
				t.Errorf("code %d, want %d", we.Code, jsonrpc.CodeInvalidParams)
			}
		})
	}
}
