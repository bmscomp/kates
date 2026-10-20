package conformity

import "fmt"

// The placement rules: where a role's pods can run. The check counts a
// role's pods (count), the zones that host one (zones), the most minus the
// fewest pods per zone (skew) and the pods on each node that has one
// (perNode). zones and skew fail a pod that is not scheduled in a zone.

func init() {
	register(
		check{code: "zones-unreachable", severity: Error, twoPass: true, run: zonesUnreachable},
		check{code: "skew-unreachable", severity: Error, twoPass: true, run: skewUnreachable},
		check{code: "pernode-cannot-place-count", severity: Error, twoPass: true, run: perNodeCannotPlace},
		check{code: "controller-quorum-even", severity: Warning, run: quorumEven},
		check{code: "controller-quorum-zone-loss", severity: Warning, run: quorumZoneLoss},
	)
}

var roles = []string{"controllers", "brokers"}

// zonesFit says whether some allowed count and zones fit: each zone that
// counts hosts a pod, and with any pod every pod must be in a zone.
func zonesFit(count, zones ints) bool {
	return count.has(0) && zones.has(0) || zones.any(func(z int) bool { return z >= 1 && z <= count.hi })
}

func zonesUnreachable(c *ctx) []finding {
	var fs []finding
	for _, role := range roles {
		ids := []string{role + ".count", role + ".zones"}
		zn := c.node(ids[1])
		count, zones := intsOf(c.node(ids[0])), intsOf(zn)
		if zn == nil || zonesFit(count, zones) {
			continue
		}
		fs = append(fs, finding{zn, "spec." + ids[1], fmt.Sprintf("allows %s, but spec.%s allows %s: each zone that counts hosts a pod, "+
			"and with any pod every pod must run in a zone, so no cluster can meet both.", zones, ids[0], count), ids})
	}
	return fs
}

// skewFits says whether some allowed count n, split over exactly z allowed
// zones with one pod or more in each, has an allowed skew: one zone gives 0;
// two give n's parity, up to n-2; more give 0 when z divides n, else 1, and
// every skew from 2 to n-z.
func skewFits(count, zones, skew ints) bool {
	reach := func(n, z, s int) bool {
		switch {
		case z == 1:
			return s == 0
		case s < 0 || s > n-z:
			return false
		case z == 2:
			return s%2 == n%2
		case s >= 2:
			return true
		}
		return (s == 0) == (n%z == 0)
	}
	return count.any(func(n int) bool {
		if n == 0 {
			return zones.has(0) && skew.has(0)
		}
		top := min(n, zones.hi)
		if skew.lo > 0 {
			top = min(top, n-skew.lo) // a skew of s needs z <= n-s
		}
		for z := max(1, zones.lo); z <= top; z++ {
			if !zones.has(z) {
				continue
			}
			if skew.list != nil {
				for _, s := range skew.list {
					if reach(n, z, s) {
						return true
					}
				}
				continue
			}
			// In a range, the reachable skews show within three steps of its least.
			for s := skew.lo; s <= min(skew.hi, skew.lo+2); s++ {
				if reach(n, z, s) {
					return true
				}
			}
		}
		return false
	})
}

func skewUnreachable(c *ctx) []finding {
	var fs []finding
	for _, role := range roles {
		ids := []string{role + ".count", role + ".zones", role + ".skew"}
		sn := c.node(ids[2])
		count, zones, skew := intsOf(c.node(ids[0])), intsOf(c.node(ids[1])), intsOf(sn)
		if sn == nil || !zonesFit(count, zones) || skewFits(count, zones, skew) {
			continue
		}
		fs = append(fs, finding{sn, "spec." + ids[2], fmt.Sprintf("allows %s, but no count and zones this reference allows split with that skew: "+
			"N pods in Z zones have skew 0 only when Z divides N, and with 2 zones the skew has N's parity.", skew), ids})
	}
	return fs
}

// placeable says whether some allowed count of pods, every one scheduled,
// fills at least as many nodes as some allowed zones with each node's count
// in perNode. shortcut: a oneOf perNode reads as the range from its least to
// its greatest member, so {oneOf: [2, 4]} seems to place 3 pods; it only
// fires less, upgrade if such a perNode turns up.
func placeable(count, zones, per ints) bool {
	a, b := max(1, per.lo), per.hi
	z1 := maxCount + 1
	zones.any(func(z int) bool {
		if z >= 1 {
			z1 = z
		}
		return z >= 1
	})
	return count.any(func(n int) bool {
		if n == 0 {
			return zones.has(0)
		}
		most := n / a // the most nodes n pods fill at a or more each
		return b >= a && z1 <= most && (n+b-1)/b <= most
	})
}

func perNodeCannotPlace(c *ctx) []finding {
	var fs []finding
	for _, role := range roles {
		ids := []string{role + ".perNode", role + ".count", role + ".zones", role + ".skew"}
		forcing := ""
		for _, id := range ids[2:] {
			if forcing == "" && c.node(id) != nil {
				forcing = id
			}
		}
		// rackAwareness from the CR fails a broker that runs on no node.
		if role == "brokers" && c.source != "brokers" {
			ids = append(ids, "kafka.rackAwareness")
			if forcing == "" && only(c.node("kafka.rackAwareness"), "true") {
				forcing = "kafka.rackAwareness"
			}
		}
		pn := c.node(ids[0])
		count, zones, per := intsOf(c.node(ids[1])), intsOf(c.node(ids[2])), intsOf(pn)
		if pn == nil || forcing == "" || !zonesFit(count, zones) || placeable(count, zones, per) {
			continue
		}
		fs = append(fs, finding{pn, "spec." + ids[0], fmt.Sprintf("allows %s on each node that hosts one, but spec.%s requires every pod to run on a node, "+
			"and no count of %s this reference allows splits that way over enough nodes for its zones.", per, forcing, role), ids})
	}
	return fs
}

// quorumEven warns of a KRaft quorum that only an even count can make.
func quorumEven(c *ctx) []finding {
	n := c.node("controllers.count")
	cs := intsOf(n)
	if n == nil || cs.lo < 2 || cs.list == nil && cs.lo != cs.hi || cs.any(func(v int) bool { return v%2 == 1 }) {
		return nil
	}
	return []finding{{node: n, path: "spec.controllers.count", message: fmt.Sprintf("is %s: a KRaft quorum of an even size "+
		"tolerates no more failed controllers than one controller fewer, and every commit waits on one more. Use an odd count.", cs)}}
}

// quorumZoneLoss warns when no allowed count and zones keep a KRaft
// majority through the loss of the zone with the most controllers.
func quorumZoneLoss(c *ctx) []finding {
	zn := c.node("controllers.zones")
	count, zones := intsOf(c.node("controllers.count")), intsOf(zn)
	if zn == nil || zones.lo < 2 {
		return nil
	}
	paired := false
	survives := count.any(func(n int) bool {
		return zones.any(func(z int) bool {
			paired = paired || z <= n
			return z <= n && n-(n+z-1)/z >= n/2+1
		})
	})
	if !paired || survives {
		return nil
	}
	return []finding{{node: zn, path: "spec.controllers.zones", message: "no count of controllers spec.controllers.count allows survives losing a zone: " +
		"some zone holds ceil(N/Z) of the N controllers, and the rest are short of the N/2+1 a KRaft quorum needs. " +
		"Use at least 3 zones, with a count they split evenly, such as 3 in 3 or 5 in 3."}}
}
