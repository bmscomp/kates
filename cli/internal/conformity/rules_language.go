package conformity

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// The language rules: versions, listeners, monitoring and settings whose
// constraints contradict each other, Strimzi, or what the check can read.

func init() {
	register(
		check{code: "strimzi-1-with-pre-kraft-kafka", severity: Error, twoPass: true, run: strimziWithPreKRaft},
		check{code: "listener-contradicts-every", severity: Error, twoPass: true, run: listenerContradictsEvery},
		check{code: "listener-port-below-9092", severity: Error, twoPass: true, run: listenerPortBelow9092},
		check{code: "podmonitor-without-metrics", severity: Warning, run: podMonitorWithoutMetrics},
		check{code: "authorization-without-authentication", severity: Warning, run: authorizationWithoutAuthentication},
		check{code: "setting-without-effect", severity: Warning, run: settingWithoutEffect},
	)
}

func cmpVersion(a, b string) int {
	x, _ := version(a)
	y, _ := version(b)
	return compareOrdinal(x, y)
}

// strimziWithPreKRaft: Strimzi manages only KRaft clusters on Kafka 3.9 or
// later long before 1.0, so a 1.x operator never records a successful
// reconcile of Kafka 2.x, and one of the two version rules always fails.
// The 3.0 threshold is deliberately loose.
func strimziWithPreKRaft(c *ctx) []finding {
	ids := []string{"versions.strimzi", "versions.kafka"}
	sn, kn := c.node(ids[0]), c.node(ids[1])
	if sn == nil || kn == nil {
		return nil
	}
	least, _ := extremes(sn, cmpVersion)
	_, most := extremes(kn, cmpVersion)
	if least == "" || most == "" || cmpVersion(least, "1.0.0") < 0 || cmpVersion(most, "3.0.0") >= 0 {
		return nil
	}
	return []finding{{sn, "spec." + ids[0], fmt.Sprintf("requires Strimzi %s or later, which never runs Kafka before 3.0, "+
		"but spec.versions.kafka allows at most %s, so no cluster can meet both.", least, most), ids}}
}

// requiredListeners is the rule id of each kafka.listeners.require item,
// once by name, in file order.
func requiredListeners(c *ctx) []string {
	req := lookup(c.spec, "kafka.listeners.require")
	if req == nil || req.Kind != yaml.SequenceNode {
		return nil
	}
	var ids []string
	seen := map[string]bool{}
	for _, l := range req.Content {
		if name := get(l, "name"); name != nil && name.Kind == yaml.ScalarNode && !seen[name.Value] {
			seen[name.Value] = true
			ids = append(ids, "kafka.listeners.require["+name.Value+"]")
		}
	}
	return ids
}

// listing is a constraint's allowed values for a message: one, or one of several.
func listing(vals []string) string {
	if len(vals) == 1 {
		return vals[0]
	}
	return "one of " + strings.Join(vals, ", ")
}

// listenerContradictsEvery: a required listener is one of every listener,
// so when it and kafka.listeners.every share no value for a property, the
// two rules never both pass.
func listenerContradictsEvery(c *ctx) []finding {
	var fs []finding
	for _, item := range requiredListeners(c) {
		l := c.node(item)
		for _, p := range []string{"type", "tls", "auth"} {
			every := "kafka.listeners.every." + p
			mine, all := textsOf(get(l, p)), textsOf(c.node(every))
			if mine == nil || all == nil || shares(mine, all) {
				continue
			}
			fs = append(fs, finding{get(l, p), "spec." + item + "." + p, fmt.Sprintf("allows %s, but spec.%s allows %s: the listener is one of every listener, "+
				"so the two rules can never both pass.", listing(mine), every, listing(all)), []string{item, every}})
		}
	}
	return fs
}

func shares(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if strings.EqualFold(x, y) {
				return true
			}
		}
	}
	return false
}

// listenerPortBelow9092: Strimzi's Kafka CRD refuses a listener port below
// 9092, so the stored CR the check reads never has one.
func listenerPortBelow9092(c *ctx) []finding {
	var fs []finding
	for _, item := range requiredListeners(c) {
		port := get(c.node(item), "port")
		ports := intsOf(port)
		if !present(port) || ports.hi >= 9092 {
			continue
		}
		fs = append(fs, finding{port, "spec." + item + ".port", fmt.Sprintf("allows %s, but Strimzi's Kafka CRD refuses a listener port below 9092, so no cluster can meet it. "+
			"This is the port inside Kafka, not the one clients reach through a route, ingress, load balancer or node port.", ports), []string{item}})
	}
	return fs
}

// podMonitorWithoutMetrics: with no metrics config Strimzi opens no
// Prometheus port on the brokers, so the PodMonitor scrapes nothing, unless
// it is there for the Kafka Exporter.
func podMonitorWithoutMetrics(c *ctx) []finding {
	pm, exporter := c.node("monitoring.podMonitor"), c.node("monitoring.kafkaExporter")
	if !only(pm, "true") || !only(c.node("monitoring.metrics"), "none") || exporter != nil && !only(exporter, "false") {
		return nil
	}
	return []finding{{node: pm, path: "spec.monitoring.podMonitor", message: "requires a PodMonitor on the broker pods, but spec.monitoring.metrics allows only none " +
		"and no Kafka Exporter is required, so the PodMonitor has nothing of this cluster's to scrape."}}
}

// authorizationWithoutAuthentication: with every listener anonymous, each
// client is User:ANONYMOUS, so an authorizer that decides by principal
// cannot tell applications apart. OPA and custom authorizers can still
// decide by listener and address.
func authorizationWithoutAuthentication(c *ctx) []finding {
	n := c.node("kafka.authorization")
	auth := textsOf(n)
	for _, a := range auth {
		if !strings.EqualFold(a, "simple") && !strings.EqualFold(a, "keycloak") {
			return nil
		}
	}
	if auth == nil || !only(c.node("kafka.listeners.every.auth"), "none") || c.tolerated("kafka.authorization") || c.tolerated("kafka.listeners.every.auth") {
		return nil
	}
	return []finding{{node: n, path: "spec.kafka.authorization", message: fmt.Sprintf("requires the %s authorizer, but spec.kafka.listeners.every.auth allows only none: "+
		"every client on the declared listeners is User:ANONYMOUS, so ACLs cannot tell applications apart.", strings.Join(auth, " or "))}}
}

// settingWithoutEffect: zoneLabel and kafka.source are settings, not
// rules; set to something other than their default with no rule that reads
// them, they usually mean the rules they were written for are missing.
func settingWithoutEffect(c *ctx) []finding {
	var fs []finding
	if zl := get(c.spec, "zoneLabel"); zl != nil && zl.Kind == yaml.ScalarNode && zl.Value != "topology.kubernetes.io/zone" &&
		!c.declares("controllers.zones", "controllers.skew", "brokers.zones", "brokers.skew", "kafka.rackAwareness") {
		fs = append(fs, finding{node: zl, path: "spec.zoneLabel", message: fmt.Sprintf("is %s, but no rule reads it: only zones, skew and kafka.rackAwareness use it. "+
			"Add the rules you meant, or remove the setting.", zl.Value)})
	}
	if c.source == "brokers" && len(configKeys(c)) == 0 && !c.declares("kafka.rackAwareness") {
		fs = append(fs, finding{node: lookup(c.spec, "kafka.source"), path: "spec.kafka.source", message: "is brokers, but no rule reads it: only kafka.config and kafka.rackAwareness use it, " +
			"and authorization and listeners are always read from the Kafka CR. Add the rules you meant, or remove the setting."})
	}
	return fs
}

// declares says whether the reference declares any of the rules, tolerated or not.
func (c *ctx) declares(ids ...string) bool {
	for _, id := range ids {
		if c.rules[id] != nil {
			return true
		}
	}
	return false
}
