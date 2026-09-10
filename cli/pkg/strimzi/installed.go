package strimzi

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/bmscomp/kates/cli/pkg/kafkaversion"
)

const (
	// EnvNamespace is the Cluster Operator env that lists the namespaces it
	// watches: "*" for every namespace, else a comma-separated list.
	EnvNamespace = "STRIMZI_NAMESPACE"
	// OperatorPodSelector selects Cluster Operator pods. The upstream
	// Deployment carries strimzi.io/kind=cluster-operator on its pod
	// template and selector only, not on its own metadata, so discovery
	// goes through the pods and their owning Deployment.
	OperatorPodSelector = "strimzi.io/kind=cluster-operator"
	// WrapperChartPrefix is the "chart" column helm list shows for releases
	// of the kates wrapper chart (strimzi-operator-<chart version>).
	WrapperChartPrefix = "strimzi-operator-"
	// CRDLabelSelector selects the Strimzi CRDs.
	CRDLabelSelector = "app=strimzi"

	// ScopeCluster is an operator watching every namespace.
	ScopeCluster = "cluster"
	// ScopeNamespaces is an operator watching a list of namespaces.
	ScopeNamespaces = "namespaces"
	// WatchAll is the Watches entry of a cluster-scoped operator.
	WatchAll = "*"
)

// Operator is a Cluster Operator running on the cluster, as its Deployment
// and (when kates installed it) its Helm release describe it.
type Operator struct {
	// Namespace and Name locate the Deployment.
	Namespace, Name string
	// Version is the operator image's tag.
	Version string
	// Image is the operator container image.
	Image string
	// Scope is ScopeCluster when STRIMZI_NAMESPACE is "*", else
	// ScopeNamespaces.
	Scope string
	// Watches is the namespaces the operator reconciles: {"*"} for a
	// cluster-scoped operator, otherwise the STRIMZI_NAMESPACE list, or the
	// operator's own namespace when the env comes from the pod's
	// metadata.namespace.
	Watches []string
	// Window is the Kafka versions the operator supports (STRIMZI_KAFKA_IMAGES).
	Window kafkaversion.Window
	// Release and AppVersion come from the wrapper chart's Helm release in
	// the same namespace; both are empty for a foreign operator that kates
	// did not install.
	Release, AppVersion string
}

// IsForeign reports whether no kates wrapper release owns the operator.
func (o Operator) IsForeign() bool {
	return o.Release == ""
}

// InstalledOperators discovers every Cluster Operator on the cluster: pods
// selected by strimzi.io/kind=cluster-operator resolve to their Deployments,
// each Deployment is read for image, scope, watched namespaces and Kafka
// window, and helm list attaches the wrapper release in the same namespace.
// The result is sorted by namespace, then name.
func InstalledOperators(ctx context.Context, r Runner) ([]Operator, error) {
	podsJSON, err := r.Run(ctx, "kubectl", "get", "pods", "-A", "-l", OperatorPodSelector, "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("list operator pods: %w", err)
	}
	owners, err := parseOperatorPodOwners([]byte(podsJSON))
	if err != nil {
		return nil, err
	}
	ops := make([]Operator, 0, len(owners))
	for _, o := range owners {
		deployJSON, err := r.Run(ctx, "kubectl", "get", "deployment", "-n", o.namespace, o.name, "-o", "json")
		if err != nil {
			return nil, fmt.Errorf("read operator Deployment %s/%s: %w", o.namespace, o.name, err)
		}
		op, err := ParseOperatorDeployment([]byte(deployJSON))
		if err != nil {
			return nil, fmt.Errorf("operator Deployment %s/%s: %w", o.namespace, o.name, err)
		}
		ops = append(ops, op)
	}
	if len(ops) > 0 {
		listJSON, err := r.Run(ctx, "helm", "list", "-A", "-o", "json")
		if err != nil {
			return nil, fmt.Errorf("list helm releases: %w", err)
		}
		if err := attachReleases(ops, []byte(listJSON)); err != nil {
			return nil, err
		}
	}
	slices.SortFunc(ops, func(a, b Operator) int {
		if c := strings.Compare(a.Namespace, b.Namespace); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return ops, nil
}

// deploymentRef locates a Deployment.
type deploymentRef struct {
	namespace, name string
}

// parseOperatorPodOwners maps the operator pods to their owning Deployments:
// a pod's ReplicaSet is named <deployment>-<pod-template-hash>. Pods without
// a ReplicaSet owner are not Deployment-managed and are ignored.
func parseOperatorPodOwners(podsJSON []byte) ([]deploymentRef, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Namespace       string            `json:"namespace"`
				Labels          map[string]string `json:"labels"`
				OwnerReferences []struct {
					Kind string `json:"kind"`
					Name string `json:"name"`
				} `json:"ownerReferences"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(podsJSON, &list); err != nil {
		return nil, fmt.Errorf("parse pod list: %w", err)
	}
	var refs []deploymentRef
	for _, pod := range list.Items {
		for _, owner := range pod.Metadata.OwnerReferences {
			if owner.Kind != "ReplicaSet" {
				continue
			}
			name := owner.Name
			if hash := pod.Metadata.Labels["pod-template-hash"]; hash != "" {
				name = strings.TrimSuffix(name, "-"+hash)
			} else if i := strings.LastIndexByte(name, '-'); i > 0 {
				name = name[:i]
			}
			ref := deploymentRef{namespace: pod.Metadata.Namespace, name: name}
			if !slices.Contains(refs, ref) {
				refs = append(refs, ref)
			}
		}
	}
	return refs, nil
}

// ParseOperatorDeployment reads a Cluster Operator Deployment (kubectl get
// deployment -o json) into an Operator: version from the image tag, scope
// and watched namespaces from STRIMZI_NAMESPACE, window from
// STRIMZI_KAFKA_IMAGES. Release and AppVersion are left empty.
func ParseOperatorDeployment(deploymentJSON []byte) (Operator, error) {
	var d struct {
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Spec struct {
			Template struct {
				Spec struct {
					Containers []container `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(deploymentJSON, &d); err != nil {
		return Operator{}, fmt.Errorf("parse Deployment: %w", err)
	}
	c, ok := operatorContainer(d.Spec.Template.Spec.Containers)
	if !ok {
		return Operator{}, fmt.Errorf("no %s container", kafkaversion.OperatorContainerName)
	}
	op := Operator{
		Namespace: d.Metadata.Namespace,
		Name:      d.Metadata.Name,
		Image:     c.Image,
		Version:   kafkaversion.ImageTag(c.Image),
	}
	op.Scope, op.Watches = watchScope(c, d.Metadata.Namespace)
	if images := c.env(kafkaversion.EnvKafkaImages); images != nil && images.Value != "" {
		w, err := kafkaversion.ParseImageMap(images.Value)
		if err != nil {
			return Operator{}, fmt.Errorf("%s: %w", kafkaversion.EnvKafkaImages, err)
		}
		op.Window = w
	}
	return op, nil
}

type container struct {
	Name  string   `json:"name"`
	Image string   `json:"image"`
	Env   []envVar `json:"env"`
}

type envVar struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	ValueFrom *struct {
		FieldRef *struct {
			FieldPath string `json:"fieldPath"`
		} `json:"fieldRef"`
	} `json:"valueFrom"`
}

func (c container) env(name string) *envVar {
	for i := range c.Env {
		if c.Env[i].Name == name {
			return &c.Env[i]
		}
	}
	return nil
}

// operatorContainer picks the Cluster Operator container: the one named
// strimzi-cluster-operator, else the one carrying STRIMZI_KAFKA_IMAGES.
func operatorContainer(containers []container) (container, bool) {
	for _, c := range containers {
		if c.Name == kafkaversion.OperatorContainerName {
			return c, true
		}
	}
	for _, c := range containers {
		if c.env(kafkaversion.EnvKafkaImages) != nil {
			return c, true
		}
	}
	return container{}, false
}

// watchScope reads STRIMZI_NAMESPACE. A missing env or one taken from the
// pod's metadata.namespace (the chart's default when neither
// watchAnyNamespace nor watchNamespaces is set) means the operator's own
// namespace.
func watchScope(c container, ownNamespace string) (scope string, watches []string) {
	e := c.env(EnvNamespace)
	if e == nil || (e.Value == "" && e.ValueFrom != nil) {
		return ScopeNamespaces, []string{ownNamespace}
	}
	if strings.TrimSpace(e.Value) == WatchAll {
		return ScopeCluster, []string{WatchAll}
	}
	for _, ns := range strings.Split(e.Value, ",") {
		if ns = strings.TrimSpace(ns); ns != "" {
			watches = append(watches, ns)
		}
	}
	if len(watches) == 0 {
		watches = []string{ownNamespace}
	}
	return ScopeNamespaces, watches
}

// attachReleases sets Release and AppVersion from helm list output for the
// wrapper release in each operator's namespace.
func attachReleases(ops []Operator, listJSON []byte) error {
	var releases []struct {
		Name       string `json:"name"`
		Namespace  string `json:"namespace"`
		Chart      string `json:"chart"`
		AppVersion string `json:"app_version"`
	}
	if err := json.Unmarshal(listJSON, &releases); err != nil {
		return fmt.Errorf("parse helm list output: %w", err)
	}
	for i := range ops {
		for _, rel := range releases {
			if rel.Namespace == ops[i].Namespace && strings.HasPrefix(rel.Chart, WrapperChartPrefix) {
				ops[i].Release, ops[i].AppVersion = rel.Name, rel.AppVersion
				break
			}
		}
	}
	return nil
}

// ScopeShape is the shape of the operators installed on a cluster.
type ScopeShape string

const (
	// ShapeNone: no operator.
	ShapeNone ScopeShape = "none"
	// ShapeCluster: only cluster-scoped operators (one, normally).
	ShapeCluster ScopeShape = "cluster"
	// ShapeNamespaces: only namespace-scoped operators.
	ShapeNamespaces ScopeShape = "namespaces"
	// ShapeMixed: a cluster-scoped operator beside namespace-scoped ones —
	// the shape deploy refuses and doctor flags.
	ShapeMixed ScopeShape = "mixed"
)

// Shape classifies the installed operators by scope.
func Shape(ops []Operator) ScopeShape {
	cluster, namespaced := false, false
	for _, op := range ops {
		if op.Scope == ScopeCluster {
			cluster = true
		} else {
			namespaced = true
		}
	}
	switch {
	case cluster && namespaced:
		return ShapeMixed
	case cluster:
		return ShapeCluster
	case namespaced:
		return ShapeNamespaces
	default:
		return ShapeNone
	}
}

// Results of UpgradeKind.
const (
	UpgradeSame      = "same"
	UpgradeUpgrade   = "upgrade"
	UpgradeDowngrade = "downgrade"
)

// UpgradeKind compares an installed operator version with a requested one:
// "same", "upgrade" (requested is newer) or "downgrade" (requested is older,
// which Strimzi does not support).
func UpgradeKind(installed, requested string) (string, error) {
	iv, err := kafkaversion.Parse(installed)
	if err != nil {
		return "", fmt.Errorf("installed operator version: %w", err)
	}
	rv, err := kafkaversion.Parse(requested)
	if err != nil {
		return "", fmt.Errorf("requested operator version: %w", err)
	}
	switch kafkaversion.Compare(iv, rv) {
	case 0:
		return UpgradeSame, nil
	case -1:
		return UpgradeUpgrade, nil
	default:
		return UpgradeDowngrade, nil
	}
}

// StoredVersions returns, for every Strimzi CRD on the cluster, the API
// versions it has stored objects in (status.storedVersions) — the installed
// CRD generation. CRDs are selected by app=strimzi, falling back to every
// CRD in a strimzi.io group when none carries the label.
func StoredVersions(ctx context.Context, r Runner) (map[string][]string, error) {
	out, err := r.Run(ctx, "kubectl", "get", "crd", "-l", CRDLabelSelector, "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("list Strimzi CRDs: %w", err)
	}
	stored, err := parseStoredVersions([]byte(out))
	if err != nil {
		return nil, err
	}
	if len(stored) > 0 {
		return stored, nil
	}
	out, err = r.Run(ctx, "kubectl", "get", "crd", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("list CRDs: %w", err)
	}
	return parseStoredVersions([]byte(out))
}

func parseStoredVersions(crdListJSON []byte) (map[string][]string, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				StoredVersions []string `json:"storedVersions"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(crdListJSON, &list); err != nil {
		return nil, fmt.Errorf("parse CRD list: %w", err)
	}
	stored := map[string][]string{}
	for _, item := range list.Items {
		if !strings.HasSuffix(item.Metadata.Name, ".strimzi.io") {
			continue
		}
		stored[item.Metadata.Name] = slices.Clone(item.Status.StoredVersions)
	}
	return stored, nil
}
