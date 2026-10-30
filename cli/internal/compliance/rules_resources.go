package compliance

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// The resource rules: requests, limits and storage that no pod can meet,
// or that a missing unit makes absurdly small.

func init() {
	register(
		check{code: "request-above-limit", severity: Error, twoPass: true, run: requestAboveLimit},
		check{code: "byte-quantity-too-small", severity: Warning, run: byteQuantityTooSmall},
		check{code: "ephemeral-single-node", severity: Warning, run: ephemeralSingleNode},
	)
}

// rat is a number or a Kubernetes quantity as an exact rational, in base
// units, so 700m equals 0.7 and 8Gi equals 8192Mi.
func rat(text string) (*big.Rat, bool) {
	text = strings.TrimSpace(text)
	m := quantityRE.FindStringSubmatch(text)
	if m == nil {
		return new(big.Rat).SetString(text) // a number encoding/json wrote with an exponent
	}
	r, ok := new(big.Rat).SetString(m[1])
	if !ok {
		return nil, false
	}
	return r.Mul(r, quantityRat[m[2]]), true
}

var quantityRat = func() map[string]*big.Rat {
	out := map[string]*big.Rat{}
	for suffix, f := range map[string]string{
		"": "1", "m": "1/1000", "k": "1000", "M": "1000000", "G": "1000000000", "T": "1000000000000",
		"P": "1000000000000000", "E": "1000000000000000000",
		"Ki": "1024", "Mi": "1048576", "Gi": "1073741824", "Ti": "1099511627776",
		"Pi": "1125899906842624", "Ei": "1152921504606846976",
	} {
		out[suffix], _ = new(big.Rat).SetString(f)
	}
	return out
}()

// cmpQuantity orders two quantities; one rat cannot read compares equal.
func cmpQuantity(a, b string) int {
	x, okX := rat(a)
	y, okY := rat(b)
	if !okX || !okY {
		return 0
	}
	return x.Cmp(y)
}

// extremes is the least and greatest value a constraint admits, as the CLI
// sends them: its listed values ordered by cmp, or its min and max; ""
// where it is open.
func extremes(n *yaml.Node, cmp func(a, b string) int) (lo, hi string) {
	if vals := textsOf(n); vals != nil {
		lo, hi = vals[0], vals[0]
		for _, v := range vals[1:] {
			if cmp(v, lo) < 0 {
				lo = v
			}
			if cmp(v, hi) > 0 {
				hi = v
			}
		}
		return lo, hi
	}
	n = resolveAlias(n)
	if m := get(n, "min"); m != nil {
		lo = jsonText(m)
	}
	if m := get(n, "max"); m != nil {
		hi = jsonText(m)
	}
	return lo, hi
}

// requestAboveLimit: Kubernetes refuses a container whose request is above
// its limit, so with a pod of the role one of the two rules always fails.
func requestAboveLimit(c *ctx) []finding {
	var fs []finding
	for _, role := range roles {
		for _, res := range []string{"cpu", "memory"} {
			ids := []string{role + ".requests." + res, role + ".limits." + res, role + ".count"}
			req, lim := c.node(ids[0]), c.node(ids[1])
			if req == nil || lim == nil || intsOf(c.node(ids[2])).lo < 1 {
				continue
			}
			least, _ := extremes(req, cmpQuantity)
			_, most := extremes(lim, cmpQuantity)
			r, okR := rat(least)
			l, okL := rat(most)
			if !okR || !okL || r.Cmp(l) <= 0 {
				continue
			}
			fs = append(fs, finding{req, "spec." + ids[0], fmt.Sprintf("needs at least %s, but spec.%s allows at most %s: "+
				"Kubernetes refuses a request above its limit, so no pod can meet both, and this reference can never pass.", least, ids[1], most), ids})
		}
	}
	return fs
}

var mebibyte = big.NewRat(1<<20, 1)

// byteQuantityTooSmall: memory and storage count bytes, so a value below a
// mebibyte nearly always lost its unit, as 512m for 512Mi or 8 for 8Gi. A
// whole min that small is a deliberate "must be set", so only a fractional
// one is flagged.
func byteQuantityTooSmall(c *ctx) []finding {
	var fs []finding
	for _, role := range roles {
		for _, field := range []string{"requests.memory", "limits.memory", "storage.size"} {
			id := role + "." + field
			n := resolveAlias(c.node(id))
			if n == nil {
				continue
			}
			vals := []*yaml.Node{n}
			var low *yaml.Node
			if n.Kind == yaml.MappingNode {
				low = get(n, "min")
				vals = []*yaml.Node{get(n, "equals"), get(n, "max"), low}
				if list := get(n, "oneOf"); list != nil && list.Kind == yaml.SequenceNode {
					vals = append(vals, list.Content...)
				}
			}
			for _, v := range vals {
				if v == nil || v.Kind != yaml.ScalarNode {
					continue
				}
				text := jsonText(v)
				b, ok := rat(text)
				if !ok || b.Sign() <= 0 || b.Cmp(mebibyte) >= 0 || v == low && b.IsInt() {
					continue
				}
				f, _ := b.Float64()
				msg := "is " + text
				if bytes := strconv.FormatFloat(f, 'f', -1, 64); bytes != text {
					msg += ", which is " + bytes + " bytes"
				}
				msg += ": memory and storage are counted in bytes, so no Kafka JVM or volume fits in it."
				if strings.HasSuffix(text, "m") {
					msg += fmt.Sprintf(" A trailing m means milli, not mega: did you mean %sMi?", strings.TrimSuffix(text, "m"))
				}
				fs = append(fs, finding{node: v, path: "spec." + id, message: msg})
			}
		}
	}
	return fs
}

// ephemeralSingleNode: an emptyDir goes with its pod, and Strimzi rolls by
// deleting pods, so a lone node on ephemeral storage loses its data on every
// roll, with no replica to recover from.
func ephemeralSingleNode(c *ctx) []finding {
	var fs []finding
	for _, role := range roles {
		n := c.node(role + ".storage.type")
		if !only(n, "ephemeral") || intsOf(c.node(role+".count")).hi != 1 {
			continue
		}
		fs = append(fs, finding{node: n, path: "spec." + role + ".storage.type", message: fmt.Sprintf("is ephemeral, and spec.%s.count allows at most one node: "+
			"any roll, eviction or reschedule of that pod loses all its data, with no replica to recover from. "+
			"Allow persistent-claim storage, or more than one node.", role)})
	}
	return fs
}
