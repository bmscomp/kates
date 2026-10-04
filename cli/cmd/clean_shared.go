package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// What kates clean leaves for others.
//
// kates clean deleted the Strimzi, cert-manager, Kyverno, Litmus and
// Prometheus Operator CRDs, uninstalled the cert-manager, Kyverno and Strimzi
// operators, and deleted namespaces such as kafka and monitoring, all by
// name. kates deploy reuses a cert-manager or Kyverno release that is already
// there, so clean removed operators it never installed. Deleting a CRD
// deletes every object of its kind in every namespace: every Certificate,
// every ServiceMonitor, every Kafka on the cluster.
//
// It now keeps what something outside the stack still uses: an operator, its
// namespace and its CRDs while objects of its kinds exist outside the
// namespaces clean deletes; a namespace holding a Helm release that kates
// does not install; a ClusterRole another release owns. On a cluster that
// holds only the stack, nothing is kept and clean removes what it always did.
// When it cannot tell, it keeps.

// crdGroup is a set of CRDs that go together, with the operator release that
// owns them when kates installs that operator as a release of its own.
type crdGroup struct {
	Name     string
	Operator *helmRelease // nil when a stack release brings the CRDs
	CRDs     []string
	// UsedBy are the CRDs whose objects, outside the stack, keep the group.
	// Kinds an operator makes by itself on the cluster at large, such as
	// Kyverno's policy reports, would keep it on every cluster.
	UsedBy []string
	// Own are cluster-scoped objects of the group that kates makes without
	// Helm, as kind/name.
	Own []string
}

func cleanCRDGroups() []crdGroup {
	strimzi := []string{
		"kafkabridges.kafka.strimzi.io", "kafkaconnectors.kafka.strimzi.io", "kafkaconnects.kafka.strimzi.io",
		"kafkamirrormaker2s.kafka.strimzi.io", "kafkanodepools.kafka.strimzi.io", "kafkarebalances.kafka.strimzi.io",
		"kafkas.kafka.strimzi.io", "kafkatopics.kafka.strimzi.io", "kafkausers.kafka.strimzi.io",
		"strimzipodsets.core.strimzi.io",
	}
	monitoring := []string{
		"alertmanagerconfigs.monitoring.coreos.com", "alertmanagers.monitoring.coreos.com",
		"podmonitors.monitoring.coreos.com", "probes.monitoring.coreos.com",
		"prometheusagents.monitoring.coreos.com", "prometheuses.monitoring.coreos.com",
		"prometheusrules.monitoring.coreos.com", "scrapeconfigs.monitoring.coreos.com",
		"servicemonitors.monitoring.coreos.com", "thanosrulers.monitoring.coreos.com",
	}
	litmus := []string{"chaosengines.litmuschaos.io", "chaosexperiments.litmuschaos.io", "chaosresults.litmuschaos.io"}
	return []crdGroup{
		{
			Name: "Strimzi", Operator: &helmRelease{"strimzi-operator", "strimzi-operator"},
			CRDs: strimzi, UsedBy: strimzi,
		},
		{
			Name: "cert-manager", Operator: &helmRelease{"cert-manager", "cert-manager"},
			CRDs: []string{
				"certificaterequests.cert-manager.io", "certificates.cert-manager.io", "challenges.acme.cert-manager.io",
				"clusterissuers.cert-manager.io", "issuers.cert-manager.io", "orders.acme.cert-manager.io",
			},
			UsedBy: []string{"certificates.cert-manager.io", "issuers.cert-manager.io", "clusterissuers.cert-manager.io"},
			// kates deploy applies it after installing cert-manager.
			Own: []string{"ClusterIssuer/selfsigned-issuer"},
		},
		{
			Name: "Kyverno", Operator: &helmRelease{"kyverno", "kyverno"},
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
		},
		{Name: "Litmus", CRDs: litmus, UsedBy: litmus},
		{Name: "Prometheus Operator", CRDs: monitoring, UsedBy: monitoring},
	}
}

// k8sObject is the part of a Kubernetes object the checks read.
type k8sObject struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Name        string            `json:"name"`
		Namespace   string            `json:"namespace"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
}

// helmReleaseNamespace is the annotation Helm puts on every object it
// installs.
const helmReleaseNamespace = "meta.helm.sh/release-namespace"

// keptItem is something kates clean leaves, and why.
type keptItem struct {
	What, Why string
}

// groupInUse returns why group must stay, or "" when nothing outside the
// stack uses it. present holds the CRDs on the cluster; deleting, the
// namespaces clean deletes. An object counts as the stack's when it lives in
// one of those namespaces or the group operator's own, or, cluster-scoped,
// when Helm installed it from one of them or it is one the group lists as
// kates' own.
func groupInUse(ctx context.Context, g crdGroup, present, deleting map[string]bool) string {
	var kinds []string
	for _, crd := range g.UsedBy {
		if present[crd] {
			kinds = append(kinds, crd)
		}
	}
	if len(kinds) == 0 {
		return ""
	}
	ours := func(ns string) bool { return deleting[ns] || (g.Operator != nil && ns == g.Operator.Namespace) }
	own := map[string]bool{}
	for _, o := range g.Own {
		own[o] = true
	}

	lCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := cleanRunOutput(lCtx, "kubectl", "get", strings.Join(kinds, ","), "-A", "-o", "json")
	var list struct {
		Items []k8sObject `json:"items"`
	}
	if err == nil {
		err = json.Unmarshal(out, &list)
	}
	if err != nil {
		return "could not list its objects to check, so it is kept"
	}

	elsewhere := map[objectsAt]int{}
	for _, o := range list.Items {
		ns := o.Metadata.Namespace
		switch {
		case ns != "" && ours(ns):
			continue
		case ns == "" && (ours(o.Metadata.Annotations[helmReleaseNamespace]) || own[o.Kind+"/"+o.Metadata.Name]):
			continue
		}
		elsewhere[objectsAt{o.Kind, ns}]++
	}
	if len(elsewhere) == 0 {
		return ""
	}
	return "in use outside the stack: " + describeCounts(elsewhere)
}

// objectsAt is a kind of object in a namespace, "" for cluster-scoped.
type objectsAt struct{ Kind, Namespace string }

// describeCounts turns {Certificate in web: 2} into "2 Certificates in web",
// sorted, at most three entries and a count of the rest.
func describeCounts(counts map[objectsAt]int) string {
	keys := make([]objectsAt, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Namespace != keys[j].Namespace {
			return keys[i].Namespace < keys[j].Namespace
		}
		return keys[i].Kind < keys[j].Kind
	})
	parts := make([]string, 0, 4)
	for i, k := range keys {
		if i == 3 {
			parts = append(parts, fmt.Sprintf("and %d more", len(keys)-3))
			break
		}
		where := "cluster-wide"
		if k.Namespace != "" {
			where = "in " + k.Namespace
		}
		parts = append(parts, fmt.Sprintf("%d %s %s", counts[k], pluralKind(k.Kind, counts[k]), where))
	}
	return strings.Join(parts, ", ")
}

// pluralKind is a Kubernetes kind for n objects: ClusterPolicy, ClusterPolicies.
func pluralKind(kind string, n int) string {
	switch {
	case n == 1:
		return kind
	case strings.HasSuffix(kind, "y"):
		return strings.TrimSuffix(kind, "y") + "ies"
	case strings.HasSuffix(kind, "s"):
		return kind + "es"
	default:
		return kind + "s"
	}
}

// foreignReleases returns the Helm releases in namespace ns that are not
// among the stack's releases, as name/namespace.
func foreignReleases(list []helmReleaseJSON, ns string, stack []helmRelease) []string {
	ours := map[helmRelease]bool{}
	for _, r := range stack {
		ours[r] = true
	}
	var foreign []string
	for _, l := range list {
		if l.Namespace == ns && !ours[helmRelease(l)] {
			foreign = append(foreign, l.Name)
		}
	}
	sort.Strings(foreign)
	return foreign
}

// clusterObjectOwner returns the namespace of the Helm release that installed
// the cluster-scoped object kind/name, "" when no release did, and whether
// the object exists. An object it cannot read is reported as an error, and
// the caller keeps it.
func clusterObjectOwner(ctx context.Context, kind, name string) (owner string, exists bool, err error) {
	gCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := cleanRunOutput(gCtx, "kubectl", "get", kind, name, "-o", "json", "--ignore-not-found")
	if err != nil {
		return "", true, err
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		return "", false, nil
	}
	var o k8sObject
	if err := json.Unmarshal(out, &o); err != nil {
		return "", true, err
	}
	return o.Metadata.Annotations[helmReleaseNamespace], true, nil
}
