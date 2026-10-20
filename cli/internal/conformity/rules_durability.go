package conformity

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// The durability rules: broker settings the check can never read, or that
// make replication promise less than it seems.

func init() {
	register(
		check{code: "config-value-kafka-refuses", severity: Error, twoPass: true, run: kafkaRefuses},
		check{code: "sensitive-config-from-brokers", severity: Error, twoPass: true, run: sensitiveFromBrokers},
		check{code: "replication-factor-exceeds-brokers", severity: Warning, run: replicationExceedsBrokers},
		check{code: "min-isr-not-below-rf", severity: Warning, run: minISRNotBelowRF},
		check{code: "isr-lost-with-one-zone", severity: Warning, run: isrLostWithOneZone},
		check{code: "unclean-election-required", severity: Warning, run: uncleanElection},
	)
}

const unclean = "unclean.leader.election.enable"

// kafkaAccepts is the integer range Kafka validates a broker key against.
var kafkaAccepts = map[string][2]int64{
	"min.insync.replicas":                              {1, math.MaxInt32},
	"transaction.state.log.min.isr":                    {1, math.MaxInt32},
	"offsets.topic.replication.factor":                 {1, math.MaxInt16},
	"transaction.state.log.replication.factor":         {1, math.MaxInt16},
	"share.coordinator.state.topic.replication.factor": {1, math.MaxInt16},
	"share.coordinator.state.topic.min.isr":            {1, math.MaxInt16},
	"default.replication.factor":                       {math.MinInt32, math.MaxInt32},
}

// configKeys is each kafka.config key once, in file order.
func configKeys(c *ctx) []string {
	config := lookup(c.spec, "kafka.config")
	if config == nil || config.Kind != yaml.MappingNode {
		return nil
	}
	var keys []string
	seen := map[string]bool{}
	for i := 0; i+1 < len(config.Content); i += 2 {
		if k := config.Content[i].Value; !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	return keys
}

// refused says whether a constraint on a key Kafka validates admits no
// value Kafka accepts, read as Integer.parseInt(text.trim()) does, or for
// unclean.leader.election.enable as true or false.
func refused(key string, n *yaml.Node) bool {
	r, isInt := kafkaAccepts[key]
	if !isInt && key != unclean {
		return false
	}
	vals := textsOf(n)
	if vals == nil && !isInt {
		return true // min or max: a broker reports true or false, never a number
	}
	if vals == nil {
		lo, hi, _ := nums(n)
		return math.Max(math.Ceil(lo), float64(r[0])) > math.Min(math.Floor(hi), float64(r[1]))
	}
	for _, v := range vals {
		v = strings.TrimFunc(v, func(ch rune) bool { return ch <= ' ' })
		if strings.ContainsFunc(v, func(ch rune) bool { return ch > unicode.MaxASCII }) {
			return false // Java also reads other scripts' digits; leave those be
		}
		if !isInt && (strings.EqualFold(v, "true") || strings.EqualFold(v, "false")) {
			return false
		}
		if i, err := strconv.ParseInt(v, 10, 64); isInt && err == nil && i >= r[0] && i <= r[1] {
			return false
		}
	}
	return true
}

// kafkaRefuses: with source brokers, each broker's effective value is
// compared, and Kafka never holds a value it refuses.
func kafkaRefuses(c *ctx) []finding {
	if c.source != "brokers" {
		return nil
	}
	var fs []finding
	for _, key := range configKeys(c) {
		id := childPath("kafka.config", key)
		if n := c.node(id); n != nil && refused(key, n) {
			fs = append(fs, finding{n, "spec." + id, "admits no value Kafka accepts for the key, so no running broker can report one, and this rule can never pass.", []string{id}})
		}
	}
	return fs
}

var sensitiveKeys = []string{"ssl.key.password", "ssl.keystore.password", "ssl.truststore.password", "ssl.keystore.key",
	"ssl.keystore.certificate.chain", "ssl.truststore.certificates", "sasl.jaas.config", "password.encoder.secret",
	"password.encoder.old.secret", "delegation.token.secret.key", "delegation.token.master.key"}

// sensitiveFromBrokers: Kafka returns a password-type config without its
// value, so with source brokers its rule is UNKNOWN or FAIL on every cluster.
func sensitiveFromBrokers(c *ctx) []finding {
	if c.source != "brokers" {
		return nil
	}
	var fs []finding
	for _, key := range configKeys(c) {
		id := childPath("kafka.config", key)
		n := c.node(id)
		if n == nil {
			continue
		}
		for _, s := range sensitiveKeys {
			if key == s || strings.HasSuffix(key, "."+s) {
				fs = append(fs, finding{n, "spec." + id, "with source brokers this key is sensitive, and Kafka never returns its value. " +
					"The rule is always UNKNOWN or FAIL, so the reference can never pass. Drop the key, and keep secrets out of the reference.", []string{id}})
				break
			}
		}
	}
	return fs
}

// nums is the least and greatest number a constraint admits: its values that
// read as numbers, or its min and max, a missing one infinite. ok is false
// when it is absent or admits no number. Rounding to float64 only widens the
// range, so a check built on it fires less, never wrongly.
func nums(n *yaml.Node) (lo, hi float64, ok bool) {
	lo, hi = math.Inf(1), math.Inf(-1)
	num := func(text string) (float64, bool) {
		f, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
		return f, err == nil && !math.IsNaN(f)
	}
	if vals := textsOf(n); vals != nil {
		for _, v := range vals {
			if f, isNum := num(v); isNum {
				lo, hi, ok = min(lo, f), max(hi, f), true
			}
		}
		return lo, hi, ok
	}
	if !present(n) {
		return lo, hi, false
	}
	lo, hi = math.Inf(-1), math.Inf(1)
	if m := get(n, "min"); m != nil {
		lo, ok = num(jsonText(m))
	}
	if m := get(n, "max"); m != nil {
		var okHi bool
		hi, okHi = num(jsonText(m))
		ok = ok || okHi
	}
	return lo, hi, ok
}

// replicationFactors are the rules that set a topic's replicas.
var replicationFactors = []string{
	"kafka.config[default.replication.factor]", "kafka.config[offsets.topic.replication.factor]",
	"kafka.config[transaction.state.log.replication.factor]", "kafka.config[share.coordinator.state.topic.replication.factor]",
	"topics.replicationFactor",
}

// replicationExceedsBrokers: Kafka refuses a topic with more replicas than
// registered brokers, so the topics these factors govern are never created.
func replicationExceedsBrokers(c *ctx) []finding {
	_, most, ok := nums(c.node("brokers.count"))
	if !ok || math.IsInf(most, 1) {
		return nil
	}
	var fs []finding
	for _, id := range replicationFactors {
		n := c.node(id)
		if least, _, ok := nums(n); ok && least > most {
			fs = append(fs, finding{node: n, path: "spec." + id, message: fmt.Sprintf("needs at least %v brokers, but spec.brokers.count allows at most %v: "+
				"Kafka refuses to create a topic with more replicas than brokers.", least, most)})
		}
	}
	return fs
}

// minISRs pair each min ISR rule with the rules that bound the in-sync
// replicas it asks for: the replication factor, then the brokers.
var minISRs = []struct {
	isr    string
	bounds []string
}{
	{"kafka.config[min.insync.replicas]", []string{"kafka.config[default.replication.factor]", "kafka.config[offsets.topic.replication.factor]", "brokers.count"}},
	{"kafka.config[transaction.state.log.min.isr]", []string{"kafka.config[transaction.state.log.replication.factor]", "brokers.count"}},
	{"topics.minInsyncReplicas", []string{"topics.replicationFactor", "brokers.count"}},
}

// minISRNotBelowRF: the ISR never exceeds the replication factor or the
// brokers, so a min ISR above them refuses every acks=all write, and one
// equal to them refuses those writes whenever a replica lags.
func minISRNotBelowRF(c *ctx) []finding {
	var fs []finding
	for _, p := range minISRs {
		n := c.node(p.isr)
		lo, _, ok := nums(n)
		if !ok {
			continue
		}
		least := math.Ceil(lo)
		bound, bhi := "", math.Inf(1)
		for _, b := range p.bounds {
			if _, hi, ok := nums(c.node(b)); ok && hi < bhi {
				bound, bhi = b, hi
			}
		}
		if bound == "" || least < bhi || least == bhi && bhi < 2 {
			continue
		}
		outcome := "always refused"
		if least == bhi {
			outcome = "refused whenever one replica is out of sync, rolling restarts included"
		}
		fs = append(fs, finding{node: n, path: "spec." + p.isr, message: fmt.Sprintf("is at least %v, and spec.%s is at most %v: acks=all writes it governs are %s. Keep it below spec.%s.",
			least, bound, bhi, outcome, bound)})
	}
	return fs
}

// isrLostWithOneZone: some zone holds ceil(R/Z) replicas of a partition
// however they are placed, so if even the most zones and replicas allowed
// leave fewer than the min ISR, a single-zone outage stops acks=all writes.
func isrLostWithOneZone(c *ctx) []finding {
	zn := c.node("brokers.zones")
	zlo, z, ok := nums(zn)
	if !ok || zlo < 2 || math.IsInf(z, 1) {
		return nil
	}
	var fs []finding
	for _, p := range minISRs {
		_, rhi, okR := nums(c.node(p.bounds[0]))
		mlo, _, okM := nums(c.node(p.isr))
		r, m := math.Floor(rhi), math.Max(1, math.Ceil(mlo))
		if !okR || !okM || math.IsInf(r, 1) || r < 2 || m >= r {
			continue
		}
		if left := r - math.Ceil(r/z); left < m {
			fs = append(fs, finding{node: zn, path: "spec.brokers.zones", message: fmt.Sprintf("allows at most %v zones: with spec.%s %v, one zone holds %v replicas of a partition, "+
				"so losing it leaves %v, below spec.%s %v, and a single-zone outage stops acks=all writes. Allow more zones, or a higher replication factor.",
				z, p.bounds[0], r, r-left, left, p.isr, m)})
		}
	}
	return fs
}

// uncleanElection: an out-of-sync leader truncates writes acknowledged
// under acks=all; harmless only on a single broker.
func uncleanElection(c *ctx) []finding {
	id := childPath("kafka.config", unclean)
	n := c.node(id)
	if _, most, ok := nums(c.node("brokers.count")); !only(n, "true") || ok && most < 2 {
		return nil
	}
	return []finding{{node: n, path: "spec." + id, message: "asks for true: an out-of-sync replica can become leader, and writes acknowledged under acks=all can be lost."}}
}
