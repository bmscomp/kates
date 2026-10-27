// Package components is the one table of what kates installs: each
// component's Helm release, the namespace it goes into under each topology,
// its chart, whether it is an operator, and the CRD groups the stack brings
// along. kates deploy builds its plan, its dashboard and its skip checks from
// it; kates clean reads the same table to know what to uninstall, which
// namespaces are the stack's and which CRDs go. Before it, the four copies of
// the CRD lists and the per-command release lists had drifted: clean listed a
// "jaeger" release nothing installs, and a CRD added to one list was missed by
// another.
//
// It is not in pkg/strimzi because most of what it lists is not Strimzi's:
// cert-manager, Kyverno, Litmus and the Prometheus Operator all have rows. It
// imports nothing of the CLI, so every package can read it.
package components

import "slices"

// Group is the order components install in: operators first, then the data
// plane, then what talks to it. The deploy views call them waves.
const (
	GroupOperators = "A"
	GroupCore      = "B"
	GroupApps      = "C"
)

// A NamespaceKey names which of the user's namespace choices a component
// lands in. Operators have a fixed namespace of their own instead.
type NamespaceKey string

const (
	NSKafka      NamespaceKey = "kafka"
	NSConnect    NamespaceKey = "connect"
	NSDatabase   NamespaceKey = "db"
	NSApp        NamespaceKey = "app"
	NSChaos      NamespaceKey = "chaos"
	NSMonitoring NamespaceKey = "monitoring"
	NSKafkaUI    NamespaceKey = "ui"
	NSMM2        NamespaceKey = "mm2"
)

// Placement is where the user put each part of the stack: the --*-ns flags
// under the isolated topology, or one namespace for every key under single.
// KafkaName is the primary Kafka cluster's name, which is also its release.
type Placement struct {
	Kafka, Connect, Database, App, Chaos, Monitoring, KafkaUI, MM2 string
	KafkaName                                                      string
}

// DefaultPlacement is the isolated topology's defaults, the ones the deploy
// and clean flags carry.
func DefaultPlacement() Placement {
	return Placement{
		Kafka: "kafka", Connect: "connect", Database: "database", App: "kates",
		Chaos: "litmus", Monitoring: "monitoring", KafkaUI: "kafka", MM2: "kafka",
		KafkaName: "krafter",
	}
}

// SingleNamespace is the single topology's default namespace.
const SingleNamespace = "kates-stack"

// SinglePlacement puts every namespaced component in namespace. The database
// is placed separately because deploy keeps it on --db-ns under both
// topologies (an existing database is never moved), while clean folds it in.
func SinglePlacement(namespace, database, kafkaName string) Placement {
	return Placement{
		Kafka: namespace, Connect: namespace, Database: database, App: namespace,
		Chaos: namespace, Monitoring: namespace, KafkaUI: namespace, MM2: namespace,
		KafkaName: kafkaName,
	}
}

// Component is one thing kates installs, or one step the deploy dashboard
// tracks (ID set, Release empty).
type Component struct {
	// ID is the dashboard's and the wizard's name for it.
	ID string
	// Name is what the deploy prints.
	Name string
	// Icon is the deploy views' two-cell glyph.
	Icon string
	// Group is the install order (GroupOperators, GroupCore, GroupApps).
	Group string
	// Release is the Helm release name; KafkaRelease stands for the primary
	// Kafka cluster, whose release is named by the user. Empty for a dashboard
	// step that installs nothing of its own.
	Release string
	// Chart is the chart it installs from: a directory of this repository's
	// charts, or alias/name for a third-party chart.
	Chart string
	// FixedNamespace is an operator's own namespace; otherwise Namespace says
	// which of the user's namespaces it goes into.
	FixedNamespace string
	NamespaceKey   NamespaceKey
	// Operator marks a cluster-wide operator: installed once per cluster,
	// kept when something outside the stack still uses its CRDs.
	Operator bool
	// CRDGroup names the CRD group the component brings, if any.
	CRDGroup string
	// Selector is the pod selector the dashboard watches; %s is KafkaName.
	Selector string
}

// KafkaRelease is the Release of the primary Kafka cluster's component: the
// real name comes from Placement.KafkaName.
const KafkaRelease = "$kafka"

// ReleaseName is the component's Helm release under p.
func (c Component) ReleaseName(p Placement) string {
	if c.Release == KafkaRelease {
		return p.KafkaName
	}
	return c.Release
}

// Namespace is where the component goes under p.
func (c Component) Namespace(p Placement) string {
	if c.FixedNamespace != "" {
		return c.FixedNamespace
	}
	switch c.NamespaceKey {
	case NSKafka:
		return p.Kafka
	case NSConnect:
		return p.Connect
	case NSDatabase:
		return p.Database
	case NSApp:
		return p.App
	case NSChaos:
		return p.Chaos
	case NSMonitoring:
		return p.Monitoring
	case NSKafkaUI:
		return p.KafkaUI
	case NSMM2:
		return p.MM2
	}
	return ""
}

// Installs says whether the component is a Helm release of its own.
func (c Component) Installs() bool {
	return c.Release != ""
}

// The CRD group names, as Groups lists them.
const (
	CRDStrimzi     = "Strimzi"
	CRDCertManager = "cert-manager"
	CRDKyverno     = "Kyverno"
	CRDLitmus      = "Litmus"
	CRDPrometheus  = "Prometheus Operator"
)

// table is the stack, in install order.
var table = []Component{
	{ID: "strimzi", Name: "Strimzi Operator", Icon: "🦊", Group: GroupOperators, Release: "strimzi-operator", Chart: "charts/strimzi-operator",
		FixedNamespace: "strimzi-operator", Operator: true, CRDGroup: CRDStrimzi, Selector: "name=strimzi-cluster-operator"},
	{ID: "cert-manager", Name: "Cert-Manager", Icon: "🔐", Group: GroupOperators, Release: "cert-manager", Chart: "jetstack/cert-manager",
		FixedNamespace: "cert-manager", Operator: true, CRDGroup: CRDCertManager, Selector: "app.kubernetes.io/instance=cert-manager"},
	{ID: "kyverno", Name: "Kyverno", Icon: "🚦", Group: GroupOperators, Release: "kyverno", Chart: "kyverno/kyverno",
		FixedNamespace: "kyverno", Operator: true, CRDGroup: CRDKyverno, Selector: "app.kubernetes.io/instance=kyverno"},

	{ID: "kafka", Name: "Kafka Cluster", Icon: "📨", Group: GroupCore, Release: KafkaRelease, Chart: "charts/kafka-cluster",
		NamespaceKey: NSKafka, Selector: "strimzi.io/cluster=%s"},
	{ID: "kafka-users", Name: "Kafka Users", Group: GroupCore, NamespaceKey: NSKafka, Selector: "app.kubernetes.io/name=entity-operator"},
	{ID: "monitoring", Name: "Monitoring Stack", Icon: "📊", Group: GroupCore, Release: "monitoring", Chart: "charts/monitoring",
		NamespaceKey: NSMonitoring, CRDGroup: CRDPrometheus, Selector: "release=monitoring"},
	{ID: "postgres", Name: "PostgreSQL", Icon: "🐘", Group: GroupCore, Release: "postgresql", Chart: "charts/cdc-postgresql",
		NamespaceKey: NSDatabase, Selector: "app.kubernetes.io/instance=postgresql"},
	{ID: "kafka-connect", Name: "Kafka Connect", Icon: "🔗", Group: GroupCore, Release: "connect-cluster", Chart: "charts/connect-cluster",
		NamespaceKey: NSConnect, Selector: "strimzi.io/kind=KafkaConnect"},
	{ID: "kafka-connector", Name: "CDC Connector", Group: GroupCore, NamespaceKey: NSConnect, Selector: "strimzi.io/cluster=connect-cluster"},

	{ID: "apicurio", Name: "Apicurio Registry", Icon: "📋", Group: GroupApps, Release: "apicurio", Chart: "charts/apicurio-registry",
		NamespaceKey: NSKafka, Selector: "app.kubernetes.io/instance=apicurio"},
	{ID: "kates", Name: "Kates Backend", Icon: "📦", Group: GroupApps, Release: "kates", Chart: "charts/kates",
		NamespaceKey: NSApp, Selector: "app.kubernetes.io/instance=kates"},
	{ID: "kafka-ui", Name: "Kafka UI", Icon: "💻", Group: GroupApps, Release: "kafka-ui", Chart: "charts/kafka-ui",
		NamespaceKey: NSKafkaUI, Selector: "app.kubernetes.io/name=kafka-ui"},
	{ID: "mirror-maker2", Name: "MirrorMaker 2", Icon: "🔁", Group: GroupApps, Release: "mm2", Chart: "charts/mirror-maker2",
		NamespaceKey: NSMM2, Selector: "strimzi.io/kind=KafkaMirrorMaker2"},
	{ID: "chaos", Name: "Litmus Chaos", Icon: "🧪", Group: GroupApps, Release: "chaos", Chart: "charts/kates-chaos",
		NamespaceKey: NSChaos, CRDGroup: CRDLitmus, Selector: "app.kubernetes.io/instance=chaos"},
}

// All returns every component, in install order. The slice is a copy.
func All() []Component {
	return slices.Clone(table)
}

// ByID returns the component with that ID.
func ByID(id string) (Component, bool) {
	i := slices.IndexFunc(table, func(c Component) bool { return c.ID == id })
	if i < 0 {
		return Component{}, false
	}
	return table[i], true
}

// ByRelease returns the component installed as release (KafkaRelease for the
// primary Kafka cluster, whatever its name).
func ByRelease(release string) (Component, bool) {
	i := slices.IndexFunc(table, func(c Component) bool { return c.Release == release })
	if i < 0 {
		return Component{}, false
	}
	return table[i], true
}

// Releases returns every release the stack installs under p, in install
// order, as name and namespace.
func Releases(p Placement) []Release {
	var out []Release
	for _, c := range table {
		if c.Installs() {
			out = append(out, Release{Name: c.ReleaseName(p), Namespace: c.Namespace(p)})
		}
	}
	return out
}

// Release is a Helm release's name and namespace.
type Release struct {
	Name, Namespace string
}

// Namespaces returns the namespaces the stack's components go into under p,
// without duplicates and in install order; operators' own namespaces last.
func Namespaces(p Placement) []string {
	var out []string
	add := func(ns string) {
		if ns != "" && !slices.Contains(out, ns) {
			out = append(out, ns)
		}
	}
	for _, c := range table {
		if !c.Operator {
			add(c.Namespace(p))
		}
	}
	for _, c := range table {
		if c.Operator {
			add(c.Namespace(p))
		}
	}
	return out
}

// CRDGroup is a set of CRDs that go together, with the operator release that
// owns them when kates installs that operator as a release of its own.
type CRDGroup struct {
	Name string
	// Owner is the operator component's release, "" when a stack release
	// brings the CRDs.
	Owner *Release
	CRDs  []string
	// UsedBy are the CRDs whose objects, outside the stack, keep the group.
	// Kinds an operator makes by itself on the cluster at large, such as
	// Kyverno's policy reports, would keep it on every cluster.
	UsedBy []string
	// Own are cluster-scoped objects of the group that kates makes without
	// Helm, as kind/name.
	Own []string
	// Namespaced are the kinds that carry finalizers or webhooks, which clean
	// deletes and strips in each namespace it deletes while the operator is
	// still running. Namespaced kinds only: kubectl ignores -n for a
	// cluster-scoped kind, so deleting ClusterIssuers "in" a namespace deleted
	// every ClusterIssuer on the cluster.
	Namespaced []string
}

var (
	strimziCRDs = []string{
		"kafkas.kafka.strimzi.io",
		"kafkaconnects.kafka.strimzi.io",
		"kafkatopics.kafka.strimzi.io",
		"kafkausers.kafka.strimzi.io",
		"kafkanodepools.kafka.strimzi.io",
		"kafkabridges.kafka.strimzi.io",
		"kafkaconnectors.kafka.strimzi.io",
		"kafkamirrormaker2s.kafka.strimzi.io",
		"kafkarebalances.kafka.strimzi.io",
		"strimzipodsets.core.strimzi.io",
	}
	litmusCRDs     = []string{"chaosengines.litmuschaos.io", "chaosexperiments.litmuschaos.io", "chaosresults.litmuschaos.io"}
	prometheusCRDs = []string{
		"alertmanagerconfigs.monitoring.coreos.com",
		"alertmanagers.monitoring.coreos.com",
		"podmonitors.monitoring.coreos.com",
		"probes.monitoring.coreos.com",
		"prometheusagents.monitoring.coreos.com",
		"prometheuses.monitoring.coreos.com",
		"prometheusrules.monitoring.coreos.com",
		"scrapeconfigs.monitoring.coreos.com",
		"servicemonitors.monitoring.coreos.com",
		"thanosrulers.monitoring.coreos.com",
	}
	crdGroups = []CRDGroup{
		{
			Name: CRDStrimzi, Owner: &Release{"strimzi-operator", "strimzi-operator"},
			CRDs: strimziCRDs, UsedBy: strimziCRDs,
			Namespaced: []string{
				"kafkas.kafka.strimzi.io", "kafkatopics.kafka.strimzi.io", "kafkausers.kafka.strimzi.io",
				"kafkaconnects.kafka.strimzi.io", "kafkaconnectors.kafka.strimzi.io",
				"kafkamirrormaker2s.kafka.strimzi.io", "kafkabridges.kafka.strimzi.io",
			},
		},
		{
			Name: CRDCertManager, Owner: &Release{"cert-manager", "cert-manager"},
			CRDs: []string{
				"certificaterequests.cert-manager.io", "certificates.cert-manager.io", "challenges.acme.cert-manager.io",
				"clusterissuers.cert-manager.io", "issuers.cert-manager.io", "orders.acme.cert-manager.io",
			},
			UsedBy: []string{"certificates.cert-manager.io", "issuers.cert-manager.io", "clusterissuers.cert-manager.io"},
			// kates deploy applies it after installing cert-manager.
			Own:        []string{"ClusterIssuer/selfsigned-issuer"},
			Namespaced: []string{"certificates.cert-manager.io", "issuers.cert-manager.io"},
		},
		{
			Name: CRDKyverno, Owner: &Release{"kyverno", "kyverno"},
			CRDs: []string{
				"admissionreports.kyverno.io", "backgroundscanreports.kyverno.io", "clusteradmissionreports.kyverno.io",
				"clusterbackgroundscanreports.kyverno.io", "cleanuppolicies.kyverno.io", "clustercleanuppolicies.kyverno.io",
				"clusterpolicies.kyverno.io", "policies.kyverno.io", "policyexceptions.kyverno.io",
				"updaterequests.kyverno.io",
			},
			UsedBy: []string{
				"clusterpolicies.kyverno.io", "policies.kyverno.io", "cleanuppolicies.kyverno.io",
				"clustercleanuppolicies.kyverno.io", "policyexceptions.kyverno.io",
			},
			Namespaced: []string{"policies.kyverno.io"},
		},
		{Name: CRDLitmus, CRDs: litmusCRDs, UsedBy: litmusCRDs, Namespaced: litmusCRDs},
		{Name: CRDPrometheus, CRDs: prometheusCRDs, UsedBy: prometheusCRDs},
	}
)

// Groups returns the CRD groups the stack brings, in the order clean lists
// them. The groups and their slices are copies.
func Groups() []CRDGroup {
	out := make([]CRDGroup, len(crdGroups))
	for i, g := range crdGroups {
		out[i] = cloneGroup(g)
	}
	return out
}

// Group returns one CRD group by name.
func Group(name string) (CRDGroup, bool) {
	for _, g := range crdGroups {
		if g.Name == name {
			return cloneGroup(g), true
		}
	}
	return CRDGroup{}, false
}

func cloneGroup(g CRDGroup) CRDGroup {
	g.CRDs, g.UsedBy, g.Own, g.Namespaced = slices.Clone(g.CRDs), slices.Clone(g.UsedBy), slices.Clone(g.Own), slices.Clone(g.Namespaced)
	if g.Owner != nil {
		owner := *g.Owner
		g.Owner = &owner
	}
	return g
}

// CRDs returns every CRD of every group, in group order.
func CRDs() []string {
	var out []string
	for _, g := range crdGroups {
		out = append(out, g.CRDs...)
	}
	return out
}

// NamespacedCRDs returns the kinds clean deletes per namespace before it
// uninstalls anything, in group order.
func NamespacedCRDs() []string {
	var out []string
	for _, g := range crdGroups {
		out = append(out, g.Namespaced...)
	}
	return out
}
