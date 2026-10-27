package components

import (
	"slices"
	"strings"
	"testing"
)

// Every row is complete: a component either installs a release from a chart
// into a namespace, or is a dashboard step with neither.
func TestTableIsConsistent(t *testing.T) {
	p := DefaultPlacement()
	seen := map[string]bool{}
	for _, c := range All() {
		if c.ID == "" || c.Name == "" || c.Group == "" || c.Selector == "" {
			t.Errorf("%+v: missing an ID, name, group or selector", c)
		}
		if seen[c.ID] {
			t.Errorf("%s listed twice", c.ID)
		}
		seen[c.ID] = true
		if c.Namespace(p) == "" {
			t.Errorf("%s: no namespace under the default placement", c.ID)
		}
		if c.Installs() != (c.Chart != "") || c.Installs() != (c.Icon != "") {
			t.Errorf("%s: a release needs a chart and an icon, a step neither (release %q chart %q icon %q)", c.ID, c.Release, c.Chart, c.Icon)
		}
		if c.Operator && c.FixedNamespace == "" {
			t.Errorf("%s: an operator has a namespace of its own", c.ID)
		}
		if c.CRDGroup != "" {
			if _, ok := Group(c.CRDGroup); !ok {
				t.Errorf("%s: CRD group %q is not listed", c.ID, c.CRDGroup)
			}
		}
	}
}

func TestPlacementDecidesNamespaceAndRelease(t *testing.T) {
	kafka, _ := ByID("kafka")
	strimzi, _ := ByID("strimzi")
	pg, _ := ByID("postgres")

	iso := DefaultPlacement()
	iso.KafkaName = "primary"
	if got := kafka.ReleaseName(iso); got != "primary" {
		t.Errorf("Kafka release = %q, want the placement's name", got)
	}
	if got := kafka.Namespace(iso); got != "kafka" {
		t.Errorf("Kafka namespace = %q", got)
	}
	if got := strimzi.Namespace(iso); got != "strimzi-operator" {
		t.Errorf("operator namespace = %q, want its fixed one", got)
	}

	single := SinglePlacement("team", "team-db", "krafter")
	if got := kafka.Namespace(single); got != "team" {
		t.Errorf("single: Kafka namespace = %q", got)
	}
	if got := pg.Namespace(single); got != "team-db" {
		t.Errorf("single: the database stays where it was placed, got %q", got)
	}
	if got := strimzi.Namespace(single); got != "strimzi-operator" {
		t.Errorf("single: operator namespace = %q", got)
	}
}

// What clean reads: every release and namespace of both topologies, with no
// release nothing installs (the old lists carried "jaeger").
func TestReleasesAndNamespaces(t *testing.T) {
	p := DefaultPlacement()
	rels := Releases(p)
	names := make([]string, len(rels))
	for i, r := range rels {
		names[i] = r.Name + "/" + r.Namespace
	}
	for _, want := range []string{
		"strimzi-operator/strimzi-operator", "cert-manager/cert-manager", "kyverno/kyverno",
		"krafter/kafka", "monitoring/monitoring", "postgresql/database", "connect-cluster/connect",
		"apicurio/kafka", "kates/kates", "kafka-ui/kafka", "mm2/kafka", "chaos/litmus",
	} {
		if !slices.Contains(names, want) {
			t.Errorf("Releases lacks %s: %v", want, names)
		}
	}
	if len(names) != 12 {
		t.Errorf("Releases = %v, want exactly the 12 releases", names)
	}
	for _, n := range names {
		if strings.HasPrefix(n, "jaeger/") {
			t.Errorf("a jaeger release is listed; nothing installs one")
		}
	}

	ns := Namespaces(p)
	want := []string{"kafka", "monitoring", "database", "connect", "kates", "litmus", "strimzi-operator", "cert-manager", "kyverno"}
	if !slices.Equal(ns, want) {
		t.Errorf("Namespaces = %v, want %v", ns, want)
	}
}

func TestCRDGroups(t *testing.T) {
	all := CRDs()
	if len(all) != 39 {
		t.Errorf("CRDs() lists %d, want 39", len(all))
	}
	for _, g := range Groups() {
		for _, crd := range g.UsedBy {
			if !slices.Contains(g.CRDs, crd) {
				t.Errorf("%s: UsedBy %s is not one of its CRDs", g.Name, crd)
			}
		}
		for _, crd := range g.Namespaced {
			if !slices.Contains(g.CRDs, crd) {
				t.Errorf("%s: Namespaced %s is not one of its CRDs", g.Name, crd)
			}
		}
		if g.Owner != nil {
			c, ok := ByRelease(g.Owner.Name)
			if !ok || !c.Operator || c.CRDGroup != g.Name {
				t.Errorf("%s: owner %s is not an operator component of this group", g.Name, g.Owner.Name)
			}
		}
	}
	if slices.Contains(NamespacedCRDs(), "clusterissuers.cert-manager.io") || slices.Contains(NamespacedCRDs(), "clusterpolicies.kyverno.io") {
		t.Error("a cluster-scoped kind is listed as namespaced")
	}

	// The copies protect the table.
	g, _ := Group(CRDStrimzi)
	g.CRDs[0] = "changed"
	if fresh, _ := Group(CRDStrimzi); fresh.CRDs[0] == "changed" {
		t.Error("Group returned the table's own slice")
	}
}
