package cmd

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
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
	mcpCaveatReaper30Minutes,
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

// TestMCPCaveatRefsExist checks each source ref against the repository: the
// file exists and every cited line is inside it. It cannot check that the
// text is still true, but it fails when code moves far enough to make a ref
// point at nothing.
func TestMCPCaveatRefsExist(t *testing.T) {
	root := filepath.Join("..", "..")
	refRE := regexp.MustCompile(`^([^:]+):([0-9]+(?:-[0-9]+)?(?:,[0-9]+(?:-[0-9]+)?)*)$`)
	for _, c := range mcpCaveats {
		for _, ref := range c.Refs {
			m := refRE.FindStringSubmatch(ref)
			if m == nil {
				t.Errorf("%s: ref %q is not path:lines", c.ID, ref)
				continue
			}
			lines, err := mcpCountLines(filepath.Join(root, filepath.FromSlash(m[1])))
			if err != nil {
				t.Errorf("%s: ref %q: %v", c.ID, ref, err)
				continue
			}
			for _, part := range strings.Split(m[2], ",") {
				bounds := strings.SplitN(part, "-", 2)
				lo, _ := strconv.Atoi(bounds[0])
				hi := lo
				if len(bounds) == 2 {
					hi, _ = strconv.Atoi(bounds[1])
				}
				if lo < 1 || hi < lo || hi > lines {
					t.Errorf("%s: ref %q cites lines %s, the file has %d", c.ID, ref, part, lines)
				}
			}
		}
	}
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
