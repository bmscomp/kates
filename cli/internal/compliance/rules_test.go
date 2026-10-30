package compliance

import "testing"

// skewFits agrees with every way to split n pods over exactly z zones.
func TestSkewFitsMatchesEverySplit(t *testing.T) {
	var splits func(n, z, least int) [][]int
	splits = func(n, z, least int) [][]int {
		if z == 0 {
			if n == 0 {
				return [][]int{{}}
			}
			return nil
		}
		var out [][]int
		for first := least; first <= n; first++ {
			for _, rest := range splits(n-first, z-1, first) {
				out = append(out, append([]int{first}, rest...))
			}
		}
		return out
	}
	for n := 0; n <= 14; n++ {
		for z := 0; z <= n+1; z++ {
			seen := map[int]bool{}
			for _, p := range splits(n, z, 1) {
				if len(p) == 0 {
					seen[0] = true
				} else {
					seen[p[len(p)-1]-p[0]] = true
				}
			}
			for s := 0; s <= n+1; s++ {
				if got := skewFits(just(n), just(z), just(s)); got != seen[s] {
					t.Errorf("%d pods in %d zones, skew %d: skewFits says %v", n, z, s, got)
				}
				// As a range, it fits when any skew from s to s+3 does.
				want := seen[s] || seen[s+1] || seen[s+2] || seen[s+3]
				if got := skewFits(just(n), just(z), ints{lo: s, hi: s + 3}); got != want {
					t.Errorf("%d pods in %d zones, skew %d to %d: skewFits says %v", n, z, s, s+3, got)
				}
			}
		}
	}
}

func just(v int) ints { return ints{list: []int{v}, lo: v, hi: v} }

// Each role gets its own two-pass verdict: one hard error, one tolerated.
func TestTwoPassJudgesEachFindingAlone(t *testing.T) {
	ds := diagnose(t, `  brokers:
    count: 2
    zones: {min: 3}
  controllers:
    count: 1
    zones: {min: 2, severity: warn}
`)
	var hard, tolerated []string
	for _, d := range ds {
		switch d.Code {
		case "zones-unreachable":
			hard = append(hard, d.Path)
		case "rule-never-passes":
			tolerated = append(tolerated, d.Path+": "+d.Message)
		}
	}
	if len(hard) != 1 || hard[0] != "spec.brokers.zones" {
		t.Errorf("errors at %v, want spec.brokers.zones", hard)
	}
	if len(tolerated) != 1 || tolerated[0] != "spec.controllers.zones: allows at least 2, but spec.controllers.count allows 1: "+
		"each zone that counts hosts a pod, and with any pod every pod must run in a zone, so no cluster can meet both [zones-unreachable]; "+
		"it is tolerated, so the check does not fail, but it can never pass: spec.controllers.zones has severity warn, so the reference passes only while it warns" {
		t.Errorf("rule-never-passes: %q", tolerated)
	}
}

// Quantities compare exactly, and only a fractional min below a mebibyte
// is a lost unit: a whole one is a deliberate "must be set".
func TestQuantityEdges(t *testing.T) {
	for _, tc := range []struct{ spec, code string }{
		{"  brokers:\n    count: 3\n    requests: {cpu: 700m}\n    limits: {cpu: {max: \"0.7\"}}\n", ""},
		{"  brokers:\n    count: 3\n    requests: {cpu: 701m}\n    limits: {cpu: {max: \"0.7\"}}\n", "request-above-limit"},
		{"  brokers:\n    count: {min: 0}\n    requests: {cpu: 2}\n    limits: {cpu: 1}\n", ""},
		{"  brokers:\n    requests: {memory: {min: 0.5}}\n", "byte-quantity-too-small"},
		{"  brokers:\n    requests: {memory: {min: 1Ki}}\n", ""},
	} {
		ds := diagnose(t, tc.spec)
		for _, code := range []string{"request-above-limit", "byte-quantity-too-small"} {
			if got := find(ds, code) != nil; got != (code == tc.code) {
				t.Errorf("%s: %s fires %v; got %v", tc.spec, code, got, codes(ds))
			}
		}
	}
}
