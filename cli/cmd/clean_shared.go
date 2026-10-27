package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/pkg/components"
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

// cleanCRDGroups are the CRD groups the stack brings, from the component
// table, in the order clean lists them.
func cleanCRDGroups() []components.CRDGroup {
	return components.Groups()
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
func groupInUse(ctx context.Context, g components.CRDGroup, present, deleting map[string]bool) string {
	var kinds []string
	for _, crd := range g.UsedBy {
		if present[crd] {
			kinds = append(kinds, crd)
		}
	}
	if len(kinds) == 0 {
		return ""
	}
	ours := func(ns string) bool { return deleting[ns] || (g.Owner != nil && ns == g.Owner.Namespace) }
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
func foreignReleases(list []helmRelease, ns string, stack []components.Release) []string {
	ours := map[components.Release]bool{}
	for _, r := range stack {
		ours[r] = true
	}
	var foreign []string
	for _, l := range list {
		if l.Namespace == ns && !ours[components.Release{Name: l.Name, Namespace: l.Namespace}] {
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
