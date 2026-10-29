package detect

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bmscomp/kates/cli/output"
	"golang.org/x/sync/errgroup"
)

// Collector fetches raw data from the cluster using the provided executor.
type Collector struct {
	exec CommandExecutor
	// KubeContext is the kube context the executor is pinned to, and the one
	// the report names. Left empty, the report names what `kubectl config
	// current-context` prints, and that command ignores --context.
	KubeContext  string
	BenchStorage bool
	BenchNetwork bool
	BenchDNS     bool
	// ReadOnly skips every probe that creates something in the cluster to
	// measure it: the Secret-creation audit, the inter-AZ latency matrix, the
	// dns-detect pod the cluster-domain lookup falls back to, and the Bench*
	// probes whatever their flags say. Their parts of the report stay empty.
	// kates deploy --dry-run sets it: a plan preview must not write.
	ReadOnly bool
	// NamespaceLabels go onto the namespaces the Secret-creation audit and
	// the latency matrix create, under the probes' own labels, so a policy
	// that requires labels on every namespace admits them. kates deploy and
	// kates auto set them to the user's namespace labels.
	NamespaceLabels map[string]string
	OnProgress      func(string)
	// CallTimeout bounds each command the collector runs; 0 means
	// DefaultCallTimeout. The probes that wait on pods give their own,
	// longer, bounds.
	CallTimeout time.Duration

	// depFetch is `kubectl get deployment -A -o json`, fetched once per
	// Collect: the Strimzi, network and admission probes all read it.
	depMu    sync.Mutex
	depFetch func() (string, error)
}

// run runs a command under ctx and the collector's per-call timeout.
func (c *Collector) run(ctx context.Context, name string, args ...string) (string, error) {
	timeout := c.CallTimeout
	if timeout <= 0 {
		timeout = DefaultCallTimeout
	}
	return execContext(ctx, c.exec, timeout, name, args...)
}

// runFor runs a command under ctx for at most timeout, for the calls that
// wait on a pod (kubectl wait, logs -f, an image pull) and need longer than
// the per-call default.
func (c *Collector) runFor(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	return execContext(ctx, c.exec, timeout, name, args...)
}

// cleanup deletes a probe namespace. It runs even when ctx was cancelled
// (Ctrl-C is exactly when the namespace must still go), bounded by the
// per-call timeout.
func (c *Collector) cleanup(ctx context.Context, ns string) {
	_, _ = c.run(context.WithoutCancel(ctx), "kubectl", "delete", "ns", ns, "--wait=false")
}

// deployments is every Deployment in the cluster as JSON, fetched on first
// use and shared by every probe of the same Collect.
func (c *Collector) deployments(ctx context.Context) (string, error) {
	c.depMu.Lock()
	if c.depFetch == nil {
		c.depFetch = sync.OnceValues(func() (string, error) {
			return c.run(ctx, "kubectl", "get", "deployment", "-A", "-o", "json")
		})
	}
	fetch := c.depFetch
	c.depMu.Unlock()
	return fetch()
}

func NewCollector(exec CommandExecutor) *Collector {
	return &Collector{exec: exec}
}

func (c *Collector) progress(msg string) {
	if c.OnProgress != nil {
		c.OnProgress(msg)
	}
}

// Preflight checks if required binaries are installed and cluster is reachable.
func (c *Collector) Preflight() error {
	return c.PreflightContext(context.Background())
}

// PreflightContext is Preflight under ctx.
func (c *Collector) PreflightContext(ctx context.Context) error {
	if _, err := c.exec.LookPath("kubectl"); err != nil {
		return fmt.Errorf("kubectl is not installed or not in PATH")
	}
	if _, err := c.exec.LookPath("helm"); err != nil {
		return fmt.Errorf("helm is not installed or not in PATH")
	}
	if _, err := c.run(ctx, "kubectl", "cluster-info"); err != nil {
		return fmt.Errorf("the Kubernetes cluster is unreachable")
	}
	return nil
}

// Collect runs all introspection queries concurrently and returns a raw report state.
func (c *Collector) Collect(ctx context.Context) (*DetectReport, error) {
	report := &DetectReport{}
	// gctx ends when ctx does, or when a probe the report cannot do without
	// (the node list) fails: the other probes' kubectl calls stop with it.
	g, gctx := errgroup.WithContext(ctx)
	c.depMu.Lock()
	c.depFetch = nil
	c.depMu.Unlock()

	// Context & Server (Sequential since they are very fast and needed for provider)
	report.Context = c.getContext(ctx)
	report.Server = c.getServer(ctx)

	// Concurrent fetching
	var gitVersion string
	var apiVersions []string
	g.Go(func() error {
		report.K8sVersion, report.K8sMinor, gitVersion = c.getK8sVersion(gctx)
		return nil
	})
	g.Go(func() error {
		out, _ := c.run(gctx, "kubectl", "api-versions")
		apiVersions = strings.Fields(out)
		return nil
	})
	g.Go(func() error {
		report.HelmVersion, report.HelmMajor = c.getHelmVersion(gctx)
		return nil
	})
	g.Go(func() error {
		nodes, err := c.parseNodes(gctx)
		if err != nil {
			return err
		}
		report.Nodes = nodes
		report.Zones = c.groupNodesByZone(nodes)
		return nil
	})
	g.Go(func() error {
		report.Storage = c.getStorageClasses(gctx)
		return nil
	})
	g.Go(func() error {
		report.StorageAudit = c.getStorageAudit(gctx)
		return nil
	})
	g.Go(func() error {
		report.ExistingKafka = c.getExistingKafkaResources(gctx)
		return nil
	})
	g.Go(func() error {
		report.Strimzi = c.getStrimziStatus(gctx)
		return nil
	})
	g.Go(func() error {
		report.Monitoring = c.getMonitoringStatus(gctx)
		return nil
	})
	g.Go(func() error {
		report.Network = c.getNetworkStatus(gctx)
		return nil
	})
	g.Go(func() error {
		report.Admission = c.getAdmissionStatus(gctx)
		return nil
	})
	g.Go(func() error {
		report.NetPolAudit = c.getNetworkPolicyAudit(gctx)
		return nil
	})
	g.Go(func() error {
		report.Workload = c.getWorkloadPressure(gctx)
		return nil
	})
	g.Go(func() error {
		report.Ecosystem = c.getEcosystemStatus(gctx)
		return nil
	})

	if err := g.Wait(); err != nil {
		return nil, err
	}
	report.Provider, report.ProviderBasis = platformOf(gitVersion, apiVersions, report.Nodes, report.Context)
	return c.finishCollect(ctx, report)
}

// finishCollect is the stages that need the first one's results: the write
// probes, the capacity check and the security audit. Interrupted (ctx
// ended), every call returns early and the report is hollow, so it says so
// rather than hand it on to be graded.
func (c *Collector) finishCollect(ctx context.Context, report *DetectReport) (*DetectReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	report.Workload = hydrateWorkloadPressure(report.Nodes, report.Workload)
	if !c.ReadOnly {
		c.runWriteProbes(ctx, report)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	report.Strimzi.CapacityStatus = c.checkKafkaCapacity(ctx, report, 0.30)
	report.Security = c.getSecurityAudit(ctx, report.Admission)
	return report, nil
}

// runWriteProbes is stage 2: the probes that learn something by creating it —
// a namespace, a Secret in it, prober pods — and deleting it afterwards.
// Collect skips it when the collector is ReadOnly.
func (c *Collector) runWriteProbes(ctx context.Context, report *DetectReport) {
	g2, ctx2 := errgroup.WithContext(ctx)
	g2.Go(func() error {
		report.SecretAudit = c.checkSecretCreation(ctx2)
		return nil
	})
	g2.Go(func() error {
		report.Network.LatencyMatrix = c.getAZLatency(ctx2, report.Nodes)
		return nil
	})
	if c.BenchStorage && len(report.Nodes) > 0 {
		g2.Go(func() error {
			c.runLiveStorageBench(ctx2, report)
			return nil
		})
	}
	if c.BenchNetwork && len(report.Nodes) > 0 {
		g2.Go(func() error {
			c.runAZBandwidthBench(ctx2, report)
			return nil
		})
	}
	if c.BenchDNS && len(report.Nodes) > 0 {
		g2.Go(func() error {
			c.runDNSResolutionProbe(ctx2, report)
			return nil
		})
	}
	_ = g2.Wait()
}

func (c *Collector) getContext(ctx context.Context) string {
	if c.KubeContext != "" {
		return c.KubeContext
	}
	out, _ := c.run(ctx, "kubectl", "config", "current-context")
	if out == "" {
		return "unknown"
	}
	return out
}

func (c *Collector) getServer(ctx context.Context) string {
	out, _ := c.run(ctx, "kubectl", "config", "view", "--minify", "-o", "jsonpath={.clusters[0].cluster.server}")
	if out == "" {
		return "unknown"
	}
	return out
}

// platformOf names the platform from the strongest signal there is, and says
// which: the OpenShift API group, the server version's suffix, a node's
// providerID, and last the context name, which is only a guess. Neither
// signal alone names EKS or GKE: a cloud's providerID (aws://, gce://) is
// also a self-built cluster's, and EKS Anywhere and Google Distributed Cloud
// run the -eks- and -gke. builds off that cloud, so it takes both.
func platformOf(gitVersion string, apiVersions []string, nodes []NodeInfo, kubeContext string) (provider, basis string) {
	for _, v := range apiVersions {
		if strings.HasPrefix(v, "config.openshift.io/") {
			return "openshift", "API group config.openshift.io"
		}
	}
	for _, s := range []struct{ mark, provider, node string }{{"-eks-", "eks", "aws://"}, {"-gke.", "gke", "gce://"}, {"+k3s", "k3s", ""}, {"+rke2", "rke2", ""}} {
		if !strings.Contains(gitVersion, s.mark) {
			continue
		}
		if s.node == "" {
			return s.provider, "server version " + gitVersion
		}
		if slices.ContainsFunc(nodes, func(n NodeInfo) bool { return strings.HasPrefix(n.ProviderID, s.node) }) {
			return s.provider, "server version " + gitVersion + " on " + s.node + " nodes"
		}
	}
	for _, n := range nodes {
		id := strings.ToLower(n.ProviderID)
		switch {
		case strings.HasPrefix(id, "kind://"):
			return "kind", "node providerID kind://"
		case strings.HasPrefix(id, "k3s://"):
			return "k3s", "node providerID k3s://"
		case strings.HasPrefix(id, "azure://") && strings.Contains(id, "/virtualmachinescalesets/aks-"):
			return "aks", "node providerID, an AKS scale set"
		}
	}
	if p := providerFromContext(kubeContext); p != "generic" {
		return p, "context name, guessed"
	}
	return "generic", "no platform signal"
}

// Platform reads only what platformOf needs: the server version, the API
// groups and the nodes' providerIDs, which an admin-less account may not
// read. version is the server's gitVersion.
func Platform(exec CommandExecutor, kubeContext string) (provider, basis, version string) {
	c := &Collector{exec: exec}
	ctx := context.Background()
	_, _, version = c.getK8sVersion(ctx)
	out, _ := c.run(ctx, "kubectl", "api-versions")
	nodes, _ := c.parseNodes(ctx)
	provider, basis = platformOf(version, strings.Fields(out), nodes, kubeContext)
	return provider, basis, version
}

func providerFromContext(ctx string) string {
	lowerCtx := strings.ToLower(ctx)
	if strings.Contains(lowerCtx, "kind") {
		return "kind"
	}
	if strings.Contains(lowerCtx, "eks") || strings.Contains(lowerCtx, "arn:aws") {
		return "eks"
	}
	if strings.Contains(lowerCtx, "gke") {
		return "gke"
	}
	if strings.Contains(lowerCtx, "aks") || strings.Contains(lowerCtx, "azure") {
		return "aks"
	}
	return "generic"
}

func (c *Collector) getK8sVersion(ctx context.Context) (version string, minor int, gitVersion string) {
	out, err := c.run(ctx, "kubectl", "version", "-o", "json")
	if err != nil {
		return "unknown", 0, ""
	}
	var data struct {
		ServerVersion struct {
			Major      string `json:"major"`
			Minor      string `json:"minor"`
			GitVersion string `json:"gitVersion"`
		} `json:"serverVersion"`
	}
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		return "unknown", 0, ""
	}
	minorStr := strings.TrimRight(data.ServerVersion.Minor, "+")
	minor, _ = strconv.Atoi(minorStr)
	return fmt.Sprintf("%s.%s", data.ServerVersion.Major, data.ServerVersion.Minor), minor, data.ServerVersion.GitVersion
}

func (c *Collector) getHelmVersion(ctx context.Context) (string, int) {
	out, _ := c.run(ctx, "helm", "version", "--short")
	out = strings.TrimPrefix(out, "v")
	out = strings.Split(out, "+")[0]
	majorStr := strings.Split(out, ".")[0]
	major, _ := strconv.Atoi(majorStr)
	if out == "" {
		return "unknown", 0
	}
	return "v" + out, major
}

func (c *Collector) parseNodes(ctx context.Context) ([]NodeInfo, error) {
	out, err := c.run(ctx, "kubectl", "get", "nodes", "-o", "json")
	if err != nil {
		return nil, err
	}
	var data struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				ProviderID string `json:"providerID"`
			} `json:"spec"`
			Status struct {
				Allocatable struct {
					CPU    string `json:"cpu"`
					Memory string `json:"memory"`
				} `json:"allocatable"`
				NodeInfo struct {
					ContainerRuntimeVersion string `json:"containerRuntimeVersion"`
					KubeletVersion          string `json:"kubeletVersion"`
					OSImage                 string `json:"osImage"`
				} `json:"nodeInfo"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		return nil, err
	}

	var nodes []NodeInfo
	for _, n := range data.Items {
		zone := n.Metadata.Labels["topology.kubernetes.io/zone"]
		if zone == "" {
			zone = n.Metadata.Labels["failure-domain.beta.kubernetes.io/zone"]
		}
		if zone == "" {
			zone = "-"
		}

		var roles []string
		for k := range n.Metadata.Labels {
			if strings.Contains(k, "node-role") {
				parts := strings.Split(k, "/")
				roles = append(roles, parts[len(parts)-1])
			}
		}
		roleStr := "worker"
		if len(roles) > 0 {
			roleStr = strings.Join(roles, ",")
		}

		cpu := parseCPUMilli(n.Status.Allocatable.CPU)

		// Allocatable memory comes in whatever unit the kubelet reports (Ki on
		// most nodes, Mi or Gi on some); parseMemoryMiB reads them all.
		memGi := int(parseMemoryMiB(n.Status.Allocatable.Memory) / 1024)

		nodes = append(nodes, NodeInfo{
			Name:     n.Metadata.Name,
			Zone:     zone,
			Roles:    roleStr,
			CPU:      cpu,
			MemoryGi: memGi,
			Runtime:  n.Status.NodeInfo.ContainerRuntimeVersion,
			Kubelet:  n.Status.NodeInfo.KubeletVersion,
			OS:       n.Status.NodeInfo.OSImage,

			ProviderID: n.Spec.ProviderID,
		})
	}
	return nodes, nil
}

func (c *Collector) groupNodesByZone(nodes []NodeInfo) []ZoneInfo {
	zoneMap := make(map[string]*ZoneInfo)
	for _, n := range nodes {
		if n.Zone == "-" {
			continue
		}
		if _, ok := zoneMap[n.Zone]; !ok {
			zoneMap[n.Zone] = &ZoneInfo{Name: n.Zone}
		}
		zoneMap[n.Zone].Nodes++
		zoneMap[n.Zone].CPUAllocatable += n.CPU
		zoneMap[n.Zone].MemAllocatableGi += n.MemoryGi
	}
	var zones []ZoneInfo
	for _, z := range zoneMap {
		zones = append(zones, *z)
	}
	// Map order is random, and the zones head every report and values file.
	sort.Slice(zones, func(i, j int) bool { return zones[i].Name < zones[j].Name })
	return zones
}

func (c *Collector) getStorageClasses(ctx context.Context) []SCInfo {
	out, err := c.run(ctx, "kubectl", "get", "sc", "-o", "json")
	if err != nil {
		return nil
	}
	var data struct {
		Items []struct {
			Metadata struct {
				Name        string            `json:"name"`
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
			Provisioner          string `json:"provisioner"`
			VolumeBindingMode    string `json:"volumeBindingMode"`
			ReclaimPolicy        string `json:"reclaimPolicy"`
			AllowVolumeExpansion *bool  `json:"allowVolumeExpansion"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		return nil
	}
	var scs []SCInfo
	for _, sc := range data.Items {
		isDefault := sc.Metadata.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" ||
			sc.Metadata.Annotations["storageclass.beta.kubernetes.io/is-default-class"] == "true"
		allowExpand := false
		if sc.AllowVolumeExpansion != nil {
			allowExpand = *sc.AllowVolumeExpansion
		}
		// Estimated from the provisioner this list already holds; the
		// --bench-storage probe replaces it with a measurement.
		iops, latency := estimateIOPS(sc.Provisioner, sc.Metadata.Name)

		scs = append(scs, SCInfo{
			Name:           sc.Metadata.Name,
			Provisioner:    sc.Provisioner,
			BindingMode:    sc.VolumeBindingMode,
			ReclaimPolicy:  sc.ReclaimPolicy,
			IsDefault:      isDefault,
			AllowExpansion: allowExpand,
			ProbedIOPS:     iops,
			ProbeLatencyMs: latency,
		})
	}
	return scs
}

func (c *Collector) getStorageAudit(ctx context.Context) StorageAudit {
	audit := StorageAudit{}

	// PV inventory
	pvOut, _ := c.run(ctx, "kubectl", "get", "pv", "-o", "json")
	var pvData struct {
		Items []struct {
			Spec struct {
				Capacity struct {
					Storage string `json:"storage"`
				} `json:"capacity"`
			} `json:"spec"`
			Status struct {
				Phase string `json:"phase"`
			} `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal([]byte(pvOut), &pvData) == nil {
		audit.PVCount = len(pvData.Items)
		totalGi := 0
		for _, pv := range pvData.Items {
			switch pv.Status.Phase {
			case "Bound":
				audit.PVBoundCount++
			case "Available":
				audit.PVAvailable++
			}
			cap := pv.Spec.Capacity.Storage
			if strings.HasSuffix(cap, "Gi") {
				val, _ := strconv.Atoi(strings.TrimSuffix(cap, "Gi"))
				totalGi += val
			}
		}
		audit.PVTotalCapacity = fmt.Sprintf("%dGi", totalGi)
	}

	// CSI drivers
	csiOut, _ := c.run(ctx, "kubectl", "get", "csidrivers", "-o", "jsonpath={.items[*].metadata.name}")
	if csiOut != "" {
		audit.CSIDrivers = strings.Fields(csiOut)
	}

	return audit
}

func (c *Collector) getExistingKafkaResources(ctx context.Context) KafkaResources {
	countLines := func(args ...string) int {
		out, _ := c.run(ctx, args[0], args[1:]...)
		if out == "" {
			return 0
		}
		return len(strings.Split(out, "\n"))
	}
	res := KafkaResources{
		KafkaClusters:  countLines("kubectl", "get", "kafka", "-n", "kafka", "--no-headers"),
		KafkaNodePools: countLines("kubectl", "get", "kafkanodepools", "-n", "kafka", "--no-headers"),
		KafkaTopics:    countLines("kubectl", "get", "kafkatopics", "-n", "kafka", "--no-headers"),
		KafkaUsers:     countLines("kubectl", "get", "kafkausers", "-n", "kafka", "--no-headers"),
	}

	pvcOut, _ := c.run(ctx, "kubectl", "get", "pvc", "-n", "kafka", "--no-headers")
	if pvcOut != "" {
		res.PVCs = len(strings.Split(pvcOut, "\n"))
		res.BoundPVCs = strings.Count(pvcOut, "Bound")
	}

	helmOut, _ := c.run(ctx, "helm", "list", "-n", "kafka", "-o", "json")
	var helmData []struct {
		Name     string `json:"name"`
		Revision string `json:"revision"`
		Status   string `json:"status"`
	}
	if json.Unmarshal([]byte(helmOut), &helmData) == nil && len(helmData) > 0 {
		res.HelmRelease = fmt.Sprintf("%s (rev %s, %s)", helmData[0].Name, helmData[0].Revision, helmData[0].Status)
	} else {
		res.HelmRelease = "none"
	}

	// Kafka cluster health introspection
	if res.KafkaClusters > 0 {
		kafkaOut, _ := c.run(ctx, "kubectl", "get", "kafka", "-n", "kafka", "-o", "json")
		var kData struct {
			Items []struct {
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
				Spec struct {
					Kafka struct {
						Replicas  int `json:"replicas"`
						Listeners []struct {
							Name string `json:"name"`
							Type string `json:"type"`
							Port int    `json:"port"`
							TLS  bool   `json:"tls"`
						} `json:"listeners"`
					} `json:"kafka"`
				} `json:"spec"`
				Status struct {
					KafkaVersion string `json:"kafkaVersion"`
					Conditions   []struct {
						Type    string `json:"type"`
						Status  string `json:"status"`
						Reason  string `json:"reason"`
						Message string `json:"message"`
					} `json:"conditions"`
				} `json:"status"`
			} `json:"items"`
		}
		if json.Unmarshal([]byte(kafkaOut), &kData) == nil && len(kData.Items) > 0 {
			k := kData.Items[0]
			health := KafkaClusterHealth{
				Name:     k.Metadata.Name,
				Version:  output.Printable(k.Status.KafkaVersion), // printed in the Cluster fact; CRD status is free text
				Replicas: k.Spec.Kafka.Replicas,
			}
			for _, l := range k.Spec.Kafka.Listeners {
				health.Listeners = append(health.Listeners, KafkaListener{
					Name: l.Name, Type: l.Type, Port: l.Port, TLS: l.TLS,
				})
			}
			for _, c := range k.Status.Conditions {
				health.Conditions = append(health.Conditions, KafkaCondition{
					// Status and reason are printed; a CRD status is free text.
					Type: c.Type, Status: output.Printable(c.Status), Reason: output.Printable(c.Reason), Message: c.Message,
				})
				if c.Type == "Ready" && c.Status == "True" {
					health.ReadyReplicas = health.Replicas
				}
			}
			res.Health = health
		}
	}

	return res
}

func (c *Collector) getStrimziStatus(ctx context.Context) StrimziInfo {
	info := StrimziInfo{}

	missingCRDs := c.missingStrimziCRDs(ctx)
	info.CRDsPresent = len(missingCRDs) == 0
	info.Health.MissingCRDs = missingCRDs

	c.findStrimziOperator(ctx, &info)

	// Auditing Strimzi Operator pods and warning logs
	if info.Running {
		c.auditStrimziOperatorPod(ctx, &info)
	} else {
		info.Health.Status = "Unhealthy"
	}

	return info
}

// missingStrimziCRDs lists the required Strimzi CRDs the cluster lacks.
func (c *Collector) missingStrimziCRDs(ctx context.Context) []string {
	// Check CRDs - split into required core CRDs and optional helper CRDs
	requiredCRDs := []string{
		"kafkas.kafka.strimzi.io",
		"kafkatopics.kafka.strimzi.io",
		"kafkausers.kafka.strimzi.io",
	}
	optionalCRDs := []string{
		"kafkanodepools.kafka.strimzi.io",
	}

	var missingCRDs []string
	for _, crd := range requiredCRDs {
		out, _ := c.run(ctx, "kubectl", "get", "crd", crd, "--no-headers")
		if out == "" || strings.Contains(out, "NotFound") || strings.Contains(out, "error") {
			missingCRDs = append(missingCRDs, crd)
		}
	}

	// We still check optional CRDs to be thorough, but their absence does not count as a failure
	for _, crd := range optionalCRDs {
		_, _ = c.run(ctx, "kubectl", "get", "crd", crd, "--no-headers")
	}
	return missingCRDs
}

// findStrimziOperator fills the operator's deployment fields of info from
// the first strimzi-cluster-operator deployment found.
func (c *Collector) findStrimziOperator(ctx context.Context, info *StrimziInfo) {
	// Find the deployment for strimzi operator (with permission-friendly namespace fallback).
	// A refusal is an error from kubectl; the JSON of every deployment in the
	// cluster may say "error" anywhere, a --log-level=error argument included.
	depOut, err := c.deployments(ctx)
	if err != nil || depOut == "" {
		// Fallback: check in the kafka namespace
		depOut, _ = c.run(ctx, "kubectl", "get", "deployment", "-n", "kafka", "-o", "json")
	}

	var depData struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Spec struct {
				Template struct {
					Spec struct {
						Containers []struct {
							Image string `json:"image"`
						} `json:"containers"`
					} `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
			Status struct {
				ReadyReplicas int `json:"readyReplicas"`
				Replicas      int `json:"replicas"`
			} `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal([]byte(depOut), &depData) != nil {
		return
	}
	for _, dep := range depData.Items {
		if strings.Contains(dep.Metadata.Name, "strimzi-cluster-operator") {
			info.Running = true
			info.Namespace = dep.Metadata.Namespace
			if len(dep.Spec.Template.Spec.Containers) > 0 {
				info.Image = dep.Spec.Template.Spec.Containers[0].Image
			}
			info.ReadyReplicas = dep.Status.ReadyReplicas
			info.TotalReplicas = dep.Status.Replicas
			return
		}
	}
}

// auditStrimziOperatorPod sets the operator's health from its first pod
// and collects the pod's recent warning log lines.
func (c *Collector) auditStrimziOperatorPod(ctx context.Context, info *StrimziInfo) {
	podOut, _ := c.run(ctx, "kubectl", "get", "pods", "-n", info.Namespace, "-l", "name=strimzi-cluster-operator", "-o", "json")
	var podData struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				Phase             string `json:"phase"`
				ContainerStatuses []struct {
					Ready bool `json:"ready"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal([]byte(podOut), &podData) != nil || len(podData.Items) == 0 {
		info.Health.Status = "Unhealthy"
		return
	}

	pod := podData.Items[0]
	podReady := pod.Status.Phase == "Running"
	for _, cs := range pod.Status.ContainerStatuses {
		if !cs.Ready {
			podReady = false
		}
	}
	info.Health.PodsReady = podReady

	// Set health status based on replicas and CRDs
	switch {
	case info.ReadyReplicas == info.TotalReplicas && podReady && info.CRDsPresent:
		info.Health.Status = "Healthy"
	case info.ReadyReplicas > 0:
		info.Health.Status = "Degraded"
	default:
		info.Health.Status = "Unhealthy"
	}

	logs, _ := c.run(ctx, "kubectl", "logs", "-n", info.Namespace, pod.Metadata.Name, "--tail=100", "--since=15m")
	info.Health.WarningLogs = strimziWarningLogs(logs)
}

// strimziWarningLogs picks at most ten lines of operator log that look like
// trouble, leaving out the warnings Strimzi logs on every healthy start.
func strimziWarningLogs(logs string) []string {
	var warningLogs []string
	for _, line := range strings.Split(logs, "\n") {
		lineLower := strings.ToLower(line)
		if !containsAny(lineLower, "warn", "error", "exception", "denied", "leader election", "permission") {
			continue
		}
		// Ignore known harmless Strimzi warnings
		if containsAny(lineLower,
			"platformfeaturesavailability",
			"meterregistry",
			"route.openshift.io",
			"build.openshift.io",
			"image.openshift.io",
			"the gauge registration will be ignored") {
			continue
		}

		warningLogs = append(warningLogs, line)
		if len(warningLogs) >= 10 {
			break
		}
	}
	return warningLogs
}

// containsAny reports whether s contains any of subs.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func (c *Collector) getEcosystemStatus(ctx context.Context) EcosystemInfo {
	return EcosystemInfo{
		KafkaConnect:   c.getKafkaConnectStatus(ctx),
		SchemaRegistry: c.getSchemaRegistryStatus(ctx),
		Database:       c.getDatabaseCDCStatus(ctx),
	}
}

// kafkaConnectItem is one KafkaConnect resource as kubectl prints it.
type kafkaConnectItem struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Spec struct {
		Replicas int    `json:"replicas"`
		Image    string `json:"image"`
		Build    *struct {
			Output struct {
				Image string `json:"image"`
				Type  string `json:"type"`
			} `json:"output"`
		} `json:"build"`
		Rack *struct {
			TopologyKey string `json:"topologyKey"`
		} `json:"rack"`
		Tracing *struct {
			Type string `json:"type"`
		} `json:"tracing"`
		JVMOptions *struct {
			Xms string `json:"-Xms"`
			Xmx string `json:"-Xmx"`
		} `json:"jvmOptions"`
		Resources *struct {
			Requests *struct {
				CPU    string `json:"cpu"`
				Memory string `json:"memory"`
			} `json:"requests"`
			Limits *struct {
				CPU    string `json:"cpu"`
				Memory string `json:"memory"`
			} `json:"limits"`
		} `json:"resources"`
		TLS *struct {
			TrustedCertificates []struct {
				SecretName string `json:"secretName"`
			} `json:"trustedCertificates"`
		} `json:"tls"`
		Template *struct {
			PDB *struct {
				MaxUnavailable int `json:"maxUnavailable"`
			} `json:"podDisruptionBudget"`
		} `json:"template"`
	} `json:"spec"`
	Status struct {
		Conditions []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"conditions"`
		LabelSelector    string `json:"labelSelector"`
		ConnectorPlugins []struct {
			Class   string `json:"class"`
			Type    string `json:"type"`
			Version string `json:"version"`
		} `json:"connectorPlugins"`
	} `json:"status"`
}

// getKafkaConnectStatus describes the first KafkaConnect on the cluster,
// its deployment's ready replicas, and the connectors in its namespace.
func (c *Collector) getKafkaConnectStatus(ctx context.Context) KafkaConnectStatus {
	kcOut, _ := c.run(ctx, "kubectl", "get", "kafkaconnect", "-A", "-o", "json")
	var kcData struct {
		Items []kafkaConnectItem `json:"items"`
	}
	if json.Unmarshal([]byte(kcOut), &kcData) != nil || len(kcData.Items) == 0 {
		return KafkaConnectStatus{}
	}
	kc := kafkaConnectFromSpec(kcData.Items[0])

	// Check deployment for ready replicas
	depOut, _ := c.run(ctx, "kubectl", "get", "deployment", fmt.Sprintf("%s-connect", kc.Name), "-n", kc.Namespace, "-o", "jsonpath={.status.readyReplicas}")
	if depOut != "" {
		kc.ReadyReplicas, _ = strconv.Atoi(depOut)
	}

	kc.Connectors = c.getKafkaConnectors(ctx, kc.Namespace)
	return kc
}

// kafkaConnectFromSpec reads what the KafkaConnect resource itself says.
func kafkaConnectFromSpec(item kafkaConnectItem) KafkaConnectStatus {
	spec := item.Spec
	kc := KafkaConnectStatus{
		Installed:      true,
		Name:           item.Metadata.Name,
		Namespace:      item.Metadata.Namespace,
		TotalReplicas:  spec.Replicas,
		Image:          spec.Image,
		HasBuild:       spec.Build != nil,
		RackAware:      spec.Rack != nil,
		TracingEnabled: spec.Tracing != nil,
		TLSEnabled:     spec.TLS != nil,
		PDBConfigured:  spec.Template != nil && spec.Template.PDB != nil,
	}
	if spec.JVMOptions != nil {
		kc.HeapXms = spec.JVMOptions.Xms
		kc.HeapXmx = spec.JVMOptions.Xmx
	}
	if spec.Resources != nil {
		if spec.Resources.Limits != nil {
			kc.MemLimitMi = int(parseMemoryMiB(spec.Resources.Limits.Memory))
			kc.CPULimitM = parseCPUMilli(spec.Resources.Limits.CPU)
		}
		if spec.Resources.Requests != nil {
			kc.CPURequestM = parseCPUMilli(spec.Resources.Requests.CPU)
		}
	}
	// Installed plugins from status
	for _, p := range item.Status.ConnectorPlugins {
		kc.Plugins = append(kc.Plugins, p.Class)
	}
	return kc
}

// getKafkaConnectors lists the KafkaConnectors in ns with their readiness.
func (c *Collector) getKafkaConnectors(ctx context.Context, ns string) []ConnectorStatus {
	kctorOut, _ := c.run(ctx, "kubectl", "get", "kafkaconnectors", "-n", ns, "-o", "json")
	var kctorData struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Class    string `json:"class"`
				TasksMax int    `json:"tasksMax"`
			} `json:"spec"`
			Status struct {
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal([]byte(kctorOut), &kctorData) != nil {
		return nil
	}
	var connectors []ConnectorStatus
	for _, kctor := range kctorData.Items {
		status := "Unknown"
		for _, cond := range kctor.Status.Conditions {
			if cond.Type == "Ready" {
				if cond.Status == "True" {
					status = "Ready"
				} else {
					status = "NotReady"
				}
			}
		}
		connectors = append(connectors, ConnectorStatus{
			Name:     kctor.Metadata.Name,
			Class:    kctor.Spec.Class,
			TasksMax: kctor.Spec.TasksMax,
			Status:   status,
		})
	}
	return connectors
}

// namedReadyItems is the part of a deployment or statefulset list the
// schema-registry and database probes read.
type namedReadyItems struct {
	Items []struct {
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Status struct {
			ReadyReplicas int `json:"readyReplicas"`
		} `json:"status"`
	} `json:"items"`
}

// getSchemaRegistryStatus looks for an Apicurio registry deployment.
func (c *Collector) getSchemaRegistryStatus(ctx context.Context) SchemaRegistryStatus {
	srOut, _ := c.run(ctx, "kubectl", "get", "deployment", "-A", "-l", "app.kubernetes.io/name=apicurio-registry", "-o", "json")
	var srData namedReadyItems
	if json.Unmarshal([]byte(srOut), &srData) != nil || len(srData.Items) == 0 {
		return SchemaRegistryStatus{}
	}
	first := srData.Items[0]
	return SchemaRegistryStatus{
		Installed: true,
		Name:      first.Metadata.Name,
		Namespace: first.Metadata.Namespace,
		Available: first.Status.ReadyReplicas > 0,
	}
}

// getDatabaseCDCStatus looks for a PostgreSQL statefulset or deployment.
func (c *Collector) getDatabaseCDCStatus(ctx context.Context) DatabaseCDCStatus {
	dbOut, _ := c.run(ctx, "kubectl", "get", "statefulset,deployment", "-A", "-l", "app.kubernetes.io/name=postgresql", "-o", "json")
	var dbData namedReadyItems
	if json.Unmarshal([]byte(dbOut), &dbData) != nil || len(dbData.Items) == 0 {
		return DatabaseCDCStatus{}
	}
	first := dbData.Items[0]
	return DatabaseCDCStatus{
		Installed:  true,
		Name:       first.Metadata.Name,
		Namespace:  first.Metadata.Namespace,
		Port:       5432,
		Accessible: first.Status.ReadyReplicas > 0,
	}
}

func (c *Collector) checkKafkaCapacity(ctx context.Context, report *DetectReport, reservePct float64) string {
	var totalCPU, totalMem int
	for _, n := range report.Nodes {
		totalCPU += n.CPU
		totalMem += n.MemoryGi
	}
	usedCPU := report.Workload.TotalCPURequests
	usedMem := report.Workload.TotalMemRequests

	availCPU := totalCPU - usedCPU
	availMem := totalMem - usedMem
	if availCPU < 0 {
		availCPU = 0
	}
	if availMem < 0 {
		availMem = 0
	}

	usable := 1.0 - reservePct
	kafkaCPU := int(float64(availCPU) * usable)
	kafkaMem := int(float64(availMem) * usable)

	// A 3-broker, 3-controller standard cluster requires minimum of:
	// 3 * 250m = 750m CPU for brokers, 3 * 250m = 750m CPU for controllers = 1500m CPU
	// 3 * 1Gi = 3Gi Mem for brokers, 3 * 1Gi = 3Gi Mem for controllers = 6Gi Mem
	reqCPU := 1500
	reqMem := 6

	if kafkaCPU >= reqCPU && kafkaMem >= reqMem {
		return "Sufficient"
	}

	var missingCPU, missingMem int
	if kafkaCPU < reqCPU {
		missingCPU = reqCPU - kafkaCPU
	}
	if kafkaMem < reqMem {
		missingMem = reqMem - kafkaMem
	}

	var deficits []string
	if missingCPU > 0 {
		deficits = append(deficits, fmt.Sprintf("%dm CPU", missingCPU))
	}
	if missingMem > 0 {
		deficits = append(deficits, fmt.Sprintf("%dGi Memory", missingMem))
	}

	return "Insufficient: missing " + strings.Join(deficits, ", ")
}

const (
	// resolvConfNamespace is where the cluster-domain lookup finds a pod to
	// read /etc/resolv.conf from: the deploy's own namespace, never an
	// arbitrary workload's.
	resolvConfNamespace = "kates"
	// resolvConfTimeout bounds that lookup's API calls (kubectl's
	// --request-timeout), so a slow exec cannot hang the collection.
	resolvConfTimeout = "10s"
)

// createProbeNamespace creates a probe's namespace in one call, already
// carrying NamespaceLabels and the probe's labels (which win a clash): a
// policy that requires labels refuses a bare namespace before any label call.
// Pod Security labels stay off: the probes' pods are plain busybox, fio and
// iperf3 pods that a restricted namespace refuses.
// The executor has no stdin, so the manifest goes through a file.
func (c *Collector) createProbeNamespace(ctx context.Context, ns, runID string) error {
	labels := map[string]string{}
	for key, value := range c.NamespaceLabels {
		if !strings.HasPrefix(key, "pod-security.kubernetes.io/") {
			labels[key] = value
		}
	}
	labels["kates-detect-experimental"] = "true"
	labels["kates-detect-run"] = runID
	path, err := writeProbeFile("kates-detect-namespace-*.yaml", NamespaceManifest(ns, labels, nil))
	if err != nil {
		return err
	}
	defer os.Remove(path)
	_, err = c.run(ctx, "kubectl", "create", "-f", path)
	return err
}

// writeProbeFile writes content to a new temp file named after pattern and
// returns its path; the caller removes it. The executor has no stdin, so
// everything a probe hands kubectl as a document goes through a file — never
// through a shell and a heredoc.
func writeProbeFile(pattern, content string) (string, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	_, err = f.WriteString(content)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// NamespaceManifest renders a Namespace for kubectl create -f, every label
// and annotation key and value quoted.
func NamespaceManifest(name string, labels, annotations map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: %s\n", name)
	for _, field := range []struct {
		name   string
		values map[string]string
	}{{"annotations", annotations}, {"labels", labels}} {
		if len(field.values) == 0 {
			continue
		}
		fmt.Fprintf(&b, "  %s:\n", field.name)
		for _, key := range slices.Sorted(maps.Keys(field.values)) {
			fmt.Fprintf(&b, "    %s: %s\n", strconv.Quote(key), strconv.Quote(field.values[key]))
		}
	}
	return b.String()
}

func (c *Collector) checkSecretCreation(ctx context.Context) SecretCreationAudit {
	audit := SecretCreationAudit{}

	runID := strconv.FormatInt(time.Now().UnixNano(), 36)
	ns := fmt.Sprintf("kates-detect-secrets-%s", runID)

	if err := c.createProbeNamespace(ctx, ns, runID); err != nil {
		audit.NamespaceCreated = false
		audit.SecretCreated = false
		audit.ErrorMsg = fmt.Sprintf("failed to create namespace: %v", err)
		return audit
	}
	audit.NamespaceCreated = true

	// Defer cleanup of namespace (double-layer cleanup)
	defer func() {
		c.cleanup(ctx, ns)
	}()

	// Try to create the secret. A refusal (an admission webhook's included)
	// is on kubectl's stderr, which the executor returns in the error.
	_, err := c.run(ctx, "kubectl", "create", "secret", "generic", "kates-detect-test-sec",
		"--from-literal=test-key=test-val", "-n", ns)

	if err != nil {
		// Report kubectl's words, not the exit status wrapped around them.
		out := err.Error()
		if inner := errors.Unwrap(err); inner != nil {
			out = strings.TrimPrefix(out, inner.Error()+": ")
		}
		audit.SecretCreated = false
		audit.ErrorMsg = out

		// Parse blockage reason
		outLower := strings.ToLower(out)
		if strings.Contains(outLower, "kyverno") || strings.Contains(outLower, "policy") {
			audit.BlockedByPolicy = true
			if idx := strings.Index(outLower, "policy "); idx != -1 {
				pName := out[idx+7:]
				if spaceIdx := strings.IndexAny(pName, " .:\n\t"); spaceIdx != -1 {
					audit.PolicyName = pName[:spaceIdx]
				} else {
					audit.PolicyName = pName
				}
			} else if idx := strings.Index(outLower, "clusterpolicy "); idx != -1 {
				pName := out[idx+14:]
				if spaceIdx := strings.IndexAny(pName, " .:\n\t"); spaceIdx != -1 {
					audit.PolicyName = pName[:spaceIdx]
				} else {
					audit.PolicyName = pName
				}
			}
		} else if strings.Contains(outLower, "gatekeeper") || strings.Contains(outLower, "denied by") {
			audit.BlockedByPolicy = true
			if idx := strings.Index(outLower, "denied by "); idx != -1 {
				pName := out[idx+10:]
				if endIdx := strings.IndexAny(pName, " ]\n\t"); endIdx != -1 {
					audit.PolicyName = pName[:endIdx]
				} else {
					audit.PolicyName = pName
				}
			}
		}
	} else {
		audit.SecretCreated = true
	}

	return audit
}

func (c *Collector) getAZLatency(ctx context.Context, nodes []NodeInfo) []LatencyResult {
	zoneToNode := firstNodePerZone(nodes)

	// If zero or only one zone is detected, cross-AZ latency doesn't apply
	if len(zoneToNode) <= 1 {
		return nil
	}

	// Session run ID and namespace
	runID := strconv.FormatInt(time.Now().UnixNano(), 36)
	ns := fmt.Sprintf("kates-detect-latency-%s", runID)

	c.progress("Auditing inter-AZ network latency: setting up temporary namespace...")
	if err := c.createProbeNamespace(ctx, ns, runID); err != nil {
		return nil
	}

	// Defer cleanup of namespace (double-layer cleanup)
	defer func() {
		c.progress("Auditing inter-AZ network latency: cleaning up resources...")
		c.cleanup(ctx, ns)
	}()

	podIPs, ok := c.startLatencyProbers(ctx, ns, runID, zoneToNode)
	if !ok {
		return nil
	}

	c.progress("Auditing inter-AZ network latency: running cross-AZ ping sweeps...")
	return c.pingZonePairs(ctx, ns, zoneToNode, podIPs)
}

// firstNodePerZone maps each labelled zone to the first of its nodes.
func firstNodePerZone(nodes []NodeInfo) map[string]string {
	zoneToNode := make(map[string]string)
	for _, n := range nodes {
		if n.Zone == "" || n.Zone == "-" {
			continue
		}
		if _, exists := zoneToNode[n.Zone]; !exists {
			zoneToNode[n.Zone] = n.Name
		}
	}
	return zoneToNode
}

// zoneProberPod names the prober pod pinned in zone.
func zoneProberPod(zone string) string {
	return fmt.Sprintf("prober-%s", sanitizeDNSLabel(zone))
}

// startLatencyProbers runs one busybox pod per zone in ns, waits for them
// to be Ready, and returns their IPs by pod name; ok is false when any
// step fails.
func (c *Collector) startLatencyProbers(ctx context.Context, ns, runID string, zoneToNode map[string]string) (podIPs map[string]string, ok bool) {
	c.progress("Auditing inter-AZ network latency: creating prober pods...")
	if err := c.createZoneProbers(ctx, ns, runID, probeImage("busybox:1.37"), "busybox", zoneToNode); err != nil {
		// Failed to create one or more pods
		return nil, false
	}

	c.progress("Auditing inter-AZ network latency: waiting for prober pods to be Ready...")
	if err := c.waitZoneProbers(ctx, ns); err != nil {
		// Pods failed to become ready (e.g. image pull backoff)
		return nil, false
	}

	return c.zoneProberIPs(ctx, ns)
}

// pingZonePairs pings from every zone's prober to every zone's prober
// (itself included) and returns one result per pair whose target has an IP.
func (c *Collector) pingZonePairs(ctx context.Context, ns string, zoneToNode, podIPs map[string]string) []LatencyResult {
	resultsChan := make(chan LatencyResult, len(zoneToNode)*len(zoneToNode))
	var pingGroup errgroup.Group

	for srcZone := range zoneToNode {
		for dstZone := range zoneToNode {
			srcPod := zoneProberPod(srcZone)
			dstIP := podIPs[zoneProberPod(dstZone)]
			if dstIP == "" {
				continue
			}

			pingGroup.Go(func() error {
				pingOut, pingErr := c.run(ctx, "kubectl", "exec", "-n", ns, srcPod, "--", "ping", "-c", "5", dstIP)
				minMs, avgMs, maxMs, jitterMs, ok := parsePingOutput(pingOut)

				resultsChan <- LatencyResult{
					SourceZone: srcZone,
					TargetZone: dstZone,
					MinMs:      minMs,
					MaxMs:      maxMs,
					AvgMs:      avgMs,
					JitterMs:   jitterMs,
					Success:    ok && pingErr == nil,
				}
				return nil
			})
		}
	}

	_ = pingGroup.Wait()
	close(resultsChan)

	var results []LatencyResult
	for res := range resultsChan {
		results = append(results, res)
	}
	return results
}

func sanitizeDNSLabel(s string) string {
	s = strings.ToLower(s)
	var sb strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('-')
		}
	}
	res := sb.String()
	for strings.Contains(res, "--") {
		res = strings.ReplaceAll(res, "--", "-")
	}
	return strings.Trim(res, "-")
}

func parsePingOutput(pingOut string) (min, avg, max, jitter float64, success bool) {
	for _, line := range strings.Split(pingOut, "\n") {
		if strings.Contains(line, "min/avg/max") {
			parts := strings.Split(line, "=")
			if len(parts) >= 2 {
				valPart := strings.TrimSpace(parts[1])
				valPart = strings.TrimSuffix(valPart, " ms")
				subParts := strings.Split(valPart, "/")
				if len(subParts) >= 3 {
					min, _ = strconv.ParseFloat(subParts[0], 64)
					avg, _ = strconv.ParseFloat(subParts[1], 64)
					max, _ = strconv.ParseFloat(subParts[2], 64)
					success = true
					if len(subParts) >= 4 {
						jitter, _ = strconv.ParseFloat(subParts[3], 64)
					}
				}
			}
		}
	}
	return
}

func (c *Collector) getMonitoringStatus(ctx context.Context) MonitoringInfo {
	info := MonitoringInfo{}
	if out, _ := c.run(ctx, "kubectl", "get", "crd", "podmonitors.monitoring.coreos.com"); !strings.Contains(out, "NotFound") && out != "" {
		info.PodMonitorCRD = true
	}
	if out, _ := c.run(ctx, "kubectl", "get", "crd", "prometheusrules.monitoring.coreos.com"); !strings.Contains(out, "NotFound") && out != "" {
		info.PrometheusRuleCRD = true
	}
	if out, _ := c.run(ctx, "kubectl", "get", "deployment", "-n", "monitoring", "-l", "app.kubernetes.io/name=grafana", "--no-headers"); out != "" {
		info.GrafanaDeployed = true
	}
	label, _ := c.run(ctx, "kubectl", "get", "podmonitors", "-A", "-o", "jsonpath={.items[0].metadata.labels.release}")
	if label != "" {
		info.ReleaseLabel = label
	} else {
		info.ReleaseLabel = "monitoring"
	}
	return info
}

// serviceCIDRFlag finds the API server's --service-cluster-ip-range value in
// its command line, as the jsonpath prints it (a JSON array): the first CIDR,
// up to the quote or comma that ends it.
var serviceCIDRFlag = regexp.MustCompile(`service-cluster-ip-range=([^",\\]+)`)
var coreDNSKubernetesDomainLine = regexp.MustCompile(`(?m)^\s*kubernetes\s+([^\s{]+)`)

func parseCoreDNSClusterDomain(corefile string) string {
	for _, match := range coreDNSKubernetesDomainLine.FindAllStringSubmatch(corefile, -1) {
		if len(match) < 2 {
			continue
		}
		domain := strings.Trim(strings.TrimSpace(match[1]), ".")
		switch domain {
		case "", "in-addr.arpa", "ip6.arpa":
			continue
		default:
			return domain
		}
	}
	return ""
}

func parseResolvConfClusterDomain(resolvContent string) string {
	for _, line := range strings.Split(resolvContent, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToLower(line), "search ") {
			continue
		}
		fields := strings.Fields(line)[1:]

		// Priority: find entry starting exactly with svc.
		var found string
		for _, f := range fields {
			if strings.HasPrefix(f, "svc.") {
				found = strings.TrimPrefix(f, "svc.")
				break
			}
		}

		// Fallback: strip namespace.svc. or svc.
		if found == "" {
			for _, f := range fields {
				clean := f
				idx := strings.Index(clean, ".svc.")
				if idx != -1 {
					clean = clean[idx+5:]
				}
				clean = strings.TrimPrefix(clean, "svc.")
				if clean != "" {
					found = clean
					break
				}
			}
		}

		if found != "" {
			return found
		}
		break
	}
	return ""
}

func (c *Collector) getNetworkStatus(ctx context.Context) NetworkInfo {
	info := NetworkInfo{CNI: "unknown"}
	if out, _ := c.run(ctx, "kubectl", "get", "pods", "-n", "kube-system", "-l", "k8s-app=calico-node", "--no-headers"); out != "" {
		info.CNI = "Calico"
	} else if out, _ := c.run(ctx, "kubectl", "get", "pods", "-n", "kube-system", "-l", "k8s-app=cilium", "--no-headers"); out != "" {
		info.CNI = "Cilium"
	} else if out, _ := c.run(ctx, "kubectl", "get", "pods", "-n", "kube-system", "-l", "app=kindnet", "--no-headers"); out != "" {
		info.CNI = "kindnet"
	} else if out, _ := c.run(ctx, "kubectl", "get", "ds", "-n", "kube-system", "kindnet", "--no-headers"); out != "" {
		info.CNI = "kindnet"
	} else if out, _ := c.run(ctx, "kubectl", "get", "ds", "-n", "kube-system", "-l", "app=flannel", "--no-headers"); out != "" {
		info.CNI = "Flannel"
	}

	depOut, _ := c.deployments(ctx)
	var depData struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				ReadyReplicas int `json:"readyReplicas"`
			} `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal([]byte(depOut), &depData) == nil {
		for _, dep := range depData.Items {
			name := strings.ToLower(dep.Metadata.Name)
			if strings.Contains(name, "coredns") || strings.Contains(name, "kube-dns") {
				info.CoreDNSRunning += dep.Status.ReadyReplicas
			}
		}
	}

	info.PodCIDR, _ = c.run(ctx, "kubectl", "get", "nodes", "-o", "jsonpath={.items[0].spec.podCIDR}")
	if info.PodCIDR == "" {
		info.PodCIDR = "unknown"
	}

	// Detect cluster DNS domain using robust pod-based extraction
	info.ClusterDomain = "cluster.local"
	if corefile, _ := c.run(ctx, "kubectl", "get", "configmap", "-n", "kube-system", "coredns", "-o", "jsonpath={.data.Corefile}"); corefile != "" {
		if domain := parseCoreDNSClusterDomain(corefile); domain != "" {
			info.ClusterDomain = domain
		}
	}

	// Without a Corefile to read, a pod's resolv.conf names the domain. The
	// pod is one of the deploy's own (the kates namespace), never the first
	// Running pod on the cluster, and the exec is bounded. A read-only
	// collection execs into nothing and keeps the cluster.local default.
	if info.ClusterDomain == "cluster.local" && !c.ReadOnly {
		podOut, _ := c.run(ctx, "kubectl", "get", "pods", "-n", resolvConfNamespace, "--field-selector=status.phase=Running",
			"--request-timeout="+resolvConfTimeout, "-o", "jsonpath={.items[0].metadata.name}")
		var resolvContent string
		if pod := strings.TrimSpace(podOut); pod != "" {
			resolvContent, _ = c.run(ctx, "kubectl", "exec", "--request-timeout="+resolvConfTimeout, "-n", resolvConfNamespace, pod, "--", "cat", "/etc/resolv.conf")
		}

		// No pod of ours yet (a first deploy): a temporary one.
		if resolvContent == "" {
			resolvContent, _ = c.runFor(ctx, 90*time.Second, "kubectl", "run", "--rm", "-i", "--image="+probeImage("busybox:1.36"), "dns-detect", "--restart=Never", "--", "cat", "/etc/resolv.conf")
		}

		if domain := parseResolvConfClusterDomain(resolvContent); domain != "" {
			info.ClusterDomain = domain
		}
	}

	// The service CIDR is a flag on the API server's command line, where the
	// API server runs as a pod (kubeadm, kind).
	apiserverCmd, _ := c.run(ctx, "kubectl", "get", "pod", "-n", "kube-system", "-l", "component=kube-apiserver",
		"-o", "jsonpath={.items[0].spec.containers[0].command}")
	info.ServiceCIDR = "unknown"
	if m := serviceCIDRFlag.FindStringSubmatch(apiserverCmd); m != nil {
		info.ServiceCIDR = m[1]
	}

	return info
}

func (c *Collector) getAdmissionStatus(ctx context.Context) AdmissionInfo {
	info := AdmissionInfo{}

	// Detect Kyverno and Gatekeeper deployments
	depOut, _ := c.deployments(ctx)
	var depData struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Spec struct {
				Template struct {
					Spec struct {
						Containers []struct {
							Image string `json:"image"`
						} `json:"containers"`
					} `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
		} `json:"items"`
	}
	if json.Unmarshal([]byte(depOut), &depData) == nil {
		for _, dep := range depData.Items {
			name := strings.ToLower(dep.Metadata.Name)
			if strings.Contains(name, "kyverno") && !info.Kyverno.Installed {
				info.Kyverno.Installed = true
				info.Kyverno.Namespace = dep.Metadata.Namespace
				if len(dep.Spec.Template.Spec.Containers) > 0 {
					img := dep.Spec.Template.Spec.Containers[0].Image
					if parts := strings.Split(img, ":"); len(parts) > 1 {
						info.Kyverno.Version = parts[len(parts)-1]
					}
				}
			}
			if strings.Contains(name, "gatekeeper") && !info.Gatekeeper.Installed {
				info.Gatekeeper.Installed = true
				info.Gatekeeper.Namespace = dep.Metadata.Namespace
			}
		}
	}

	// Deep Kyverno ClusterPolicy parsing
	if info.Kyverno.Installed {
		c.parseKyvernoPolicies(ctx, &info)
	}

	// OPA Gatekeeper constraints
	if info.Gatekeeper.Installed {
		c.parseGatekeeperConstraints(ctx, &info)
	}

	return info
}

func (c *Collector) parseKyvernoPolicies(ctx context.Context, info *AdmissionInfo) {
	polOut, _ := c.run(ctx, "kubectl", "get", "clusterpolicy", "-o", "json")
	var polData struct {
		Items []json.RawMessage `json:"items"`
	}
	if json.Unmarshal([]byte(polOut), &polData) != nil {
		return
	}

	for _, raw := range polData.Items {
		policyInfo, ok := kyvernoPolicyFromJSON(raw)
		if !ok {
			continue
		}

		info.Kyverno.ClusterPolicies = append(info.Kyverno.ClusterPolicies, policyInfo)
		if policyInfo.AffectsKafka {
			info.Kyverno.KafkaRelevant = append(info.Kyverno.KafkaRelevant, policyInfo)
		}

		if policyInfo.Action == "enforce" {
			applyKyvernoConstraints(&info.Kyverno.Constraints, policyInfo)
		}
	}
}

// kyvernoMatchResources is the resources block of a Kyverno rule's match.
type kyvernoMatchResources struct {
	Kinds      []string `json:"kinds"`
	Namespaces []string `json:"namespaces"`
}

// kyvernoRule is the part of a Kyverno rule the admission audit reads.
type kyvernoRule struct {
	Name  string `json:"name"`
	Match struct {
		Any []struct {
			Resources kyvernoMatchResources `json:"resources"`
		} `json:"any"`
		Resources kyvernoMatchResources `json:"resources"`
	} `json:"match"`
}

// kyvernoPolicyFromJSON reads one ClusterPolicy; ok is false when raw is
// not one.
func kyvernoPolicyFromJSON(raw json.RawMessage) (KyvernoPolicyInfo, bool) {
	var pol struct {
		Metadata struct {
			Name        string            `json:"name"`
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
		Spec struct {
			ValidationFailureAction string        `json:"validationFailureAction"`
			Rules                   []kyvernoRule `json:"rules"`
		} `json:"spec"`
	}
	if json.Unmarshal(raw, &pol) != nil {
		return KyvernoPolicyInfo{}, false
	}

	action := strings.ToLower(pol.Spec.ValidationFailureAction)
	if action == "" {
		action = "audit"
	}

	category := pol.Metadata.Annotations["policies.kyverno.io/category"]
	if category == "" {
		category = pol.Metadata.Annotations["kyverno.io/category"]
	}
	// An annotation can carry newlines and escapes; the category is printed.
	category = output.Printable(category)

	var ruleNames []string
	affectsKafka := false
	for _, r := range pol.Spec.Rules {
		ruleNames = append(ruleNames, r.Name)
		if kyvernoRuleAffectsKafka(r) {
			affectsKafka = true
		}
	}

	return KyvernoPolicyInfo{
		Name:         pol.Metadata.Name,
		Action:       action,
		Category:     category,
		AffectsKafka: affectsKafka,
		Rules:        ruleNames,
		Description:  pol.Metadata.Annotations["policies.kyverno.io/description"],
	}, true
}

// kyvernoRuleAffectsKafka reports whether a rule matches the kafka
// namespace: no namespace filter at all, or a filter naming kafka or *.
func kyvernoRuleAffectsKafka(r kyvernoRule) bool {
	if len(r.Match.Resources.Namespaces) == 0 && len(r.Match.Any) == 0 {
		// No namespace filter = applies to all namespaces including kafka
		return true
	}
	if namespacesIncludeKafka(r.Match.Resources.Namespaces) {
		return true
	}
	for _, anyMatch := range r.Match.Any {
		if len(anyMatch.Resources.Namespaces) == 0 || namespacesIncludeKafka(anyMatch.Resources.Namespaces) {
			return true
		}
	}
	return false
}

func namespacesIncludeKafka(namespaces []string) bool {
	for _, ns := range namespaces {
		if strings.ToLower(ns) == "kafka" || ns == "*" {
			return true
		}
	}
	return false
}

// applyKyvernoConstraints marks the constraint categories an enforced
// policy's name and rule names suggest.
func applyKyvernoConstraints(con *PolicyConstraints, pol KyvernoPolicyInfo) {
	allNames := strings.ToLower(pol.Name)
	for _, rn := range pol.Rules {
		allNames += " " + strings.ToLower(rn)
	}
	has := func(sub string) bool { return strings.Contains(allNames, sub) }

	if has("empty") && has("podselector") {
		con.EmptyPodSelectorBlocked = true
	}
	if has("netpol") && has("podselector") {
		con.EmptyPodSelectorBlocked = true
	}
	if has("host") && has("network") {
		con.HostNetworkBlocked = true
	}
	if has("privileged") || has("privilege") {
		con.PrivilegedBlocked = true
	}
	if has("run-as-root") || has("runasnonroot") || has("run-as-non-root") {
		con.RunAsRootBlocked = true
	}
	if has("latest") && has("tag") {
		con.LatestTagBlocked = true
	}
	if has("resource") && (has("limit") || has("request")) {
		con.ResourceLimitsRequired = true
	}
}

func (c *Collector) parseGatekeeperConstraints(ctx context.Context, info *AdmissionInfo) {
	conOut, _ := c.run(ctx, "kubectl", "get", "constraints", "-o", "json")
	var conData struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Kind string `json:"kind"`
			Spec struct {
				EnforcementAction string `json:"enforcementAction"`
			} `json:"spec"`
		} `json:"items"`
	}
	if json.Unmarshal([]byte(conOut), &conData) == nil {
		for _, con := range conData.Items {
			action := con.Spec.EnforcementAction
			if action == "" {
				action = "deny"
			}
			info.Gatekeeper.Constraints = append(info.Gatekeeper.Constraints, GatekeeperConstraint{
				Name:   con.Metadata.Name,
				Kind:   con.Kind,
				Action: action,
			})
		}
	}
}

func (c *Collector) getNetworkPolicyAudit(ctx context.Context) NetworkPolicyAudit {
	audit := NetworkPolicyAudit{}

	out, err := c.run(ctx, "kubectl", "get", "networkpolicy", "-n", "kafka", "-o", "json")
	if err != nil {
		return audit
	}

	var data struct {
		Items []struct {
			Metadata struct {
				Name        string            `json:"name"`
				Namespace   string            `json:"namespace"`
				Annotations map[string]string `json:"annotations"`
				Labels      map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				PodSelector struct {
					MatchLabels map[string]string `json:"matchLabels"`
				} `json:"podSelector"`
				PolicyTypes []string          `json:"policyTypes"`
				Ingress     []json.RawMessage `json:"ingress"`
				Egress      []json.RawMessage `json:"egress"`
			} `json:"spec"`
		} `json:"items"`
	}

	if json.Unmarshal([]byte(out), &data) != nil {
		return audit
	}

	for _, np := range data.Items {
		// Build human-readable selector
		selector := "{}"
		if len(np.Spec.PodSelector.MatchLabels) > 0 {
			var parts []string
			for k, v := range np.Spec.PodSelector.MatchLabels {
				parts = append(parts, k+"="+v)
			}
			selector = strings.Join(parts, ",")
		}

		// Determine who manages this policy
		managedBy := "manual"
		if rel, ok := np.Metadata.Annotations["meta.helm.sh/release-name"]; ok {
			managedBy = output.Printable(rel) // an annotation, printed in the policy table
		} else if np.Metadata.Labels["app.kubernetes.io/managed-by"] == "strimzi-cluster-operator" || np.Metadata.Labels["strimzi.io/cluster"] != "" || np.Metadata.Labels["strimzi.io/kind"] == "Kafka" {
			managedBy = "strimzi"
		}

		npInfo := ExistingNetPol{
			Name:         np.Metadata.Name,
			Namespace:    np.Metadata.Namespace,
			PodSelector:  selector,
			PolicyTypes:  np.Spec.PolicyTypes,
			IngressRules: len(np.Spec.Ingress),
			EgressRules:  len(np.Spec.Egress),
			ManagedBy:    managedBy,
		}

		audit.Existing = append(audit.Existing, npInfo)

		nameLower := strings.ToLower(np.Metadata.Name)
		if strings.Contains(nameLower, "default-deny") || strings.Contains(nameLower, "deny-all") {
			audit.HasDefaultDeny = true
		}
		if strings.Contains(nameLower, "allow-dns") || strings.Contains(nameLower, "dns") {
			audit.HasDNSAllow = true
		}
	}

	audit.TotalCount = len(data.Items)
	return audit
}

// getWorkloadPressure collects resource consumption from all running pods.
func (c *Collector) getWorkloadPressure(ctx context.Context) WorkloadPressure {
	wp := WorkloadPressure{}

	out, err := c.run(ctx, "kubectl", "get", "pods", "-A",
		"-o", "json", "--field-selector=status.phase=Running")
	if err != nil {
		return wp
	}

	var data struct {
		Items []struct {
			Metadata struct {
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Spec struct {
				NodeName   string `json:"nodeName"`
				Containers []struct {
					Resources struct {
						Requests struct {
							CPU    string `json:"cpu"`
							Memory string `json:"memory"`
						} `json:"requests"`
					} `json:"resources"`
				} `json:"containers"`
			} `json:"spec"`
		} `json:"items"`
	}
	if json.Unmarshal([]byte(out), &data) != nil {
		return wp
	}

	// Per-node accumulation. Memory adds up in MiB and becomes GiB once:
	// converted per container, every request under 1Gi counted as nothing.
	nodeCPU := make(map[string]int)
	nodeMemMiB := make(map[string]float64)
	var totalMemMiB float64

	for _, pod := range data.Items {
		wp.TotalPods++
		if pod.Metadata.Namespace == "kafka" {
			wp.KafkaNamespacePods++
		}
		for _, c := range pod.Spec.Containers {
			cpuM := parseCPUMilli(c.Resources.Requests.CPU)
			memMiB := parseMemoryMiB(c.Resources.Requests.Memory)

			wp.TotalCPURequests += cpuM
			totalMemMiB += memMiB
			nodeCPU[pod.Spec.NodeName] += cpuM
			nodeMemMiB[pod.Spec.NodeName] += memMiB
		}
	}
	wp.TotalMemRequests = int(totalMemMiB / 1024)

	for node, cpu := range nodeCPU {
		wp.PerNode = append(wp.PerNode, NodePressure{
			Name:         node,
			CPURequested: cpu,
			MemRequested: int(nodeMemMiB[node] / 1024),
		})
	}
	sort.Slice(wp.PerNode, func(i, j int) bool {
		return wp.PerNode[i].Name < wp.PerNode[j].Name
	})

	return wp
}

func hydrateWorkloadPressure(nodes []NodeInfo, wp WorkloadPressure) WorkloadPressure {
	requests := map[string]NodePressure{}
	for _, node := range wp.PerNode {
		requests[node.Name] = node
	}

	type zoneTotals struct {
		pressure           ZonePressure
		cpuAlloc, memAlloc int
	}
	zones := map[string]*zoneTotals{}
	wp.PerNode = nil
	wp.PerZone = nil

	for _, node := range nodes {
		req := requests[node.Name]
		cpuAvailable := node.CPU - req.CPURequested
		if cpuAvailable < 0 {
			cpuAvailable = 0
		}
		memAvailable := node.MemoryGi - req.MemRequested
		if memAvailable < 0 {
			memAvailable = 0
		}
		wp.PerNode = append(wp.PerNode, NodePressure{
			Name:           node.Name,
			Zone:           node.Zone,
			CPUAllocatable: node.CPU,
			CPURequested:   req.CPURequested,
			CPUAvailable:   cpuAvailable,
			MemAllocatable: node.MemoryGi,
			MemRequested:   req.MemRequested,
			MemAvailable:   memAvailable,
		})

		if node.Zone == "" || node.Zone == "-" {
			continue
		}
		zt := zones[node.Zone]
		if zt == nil {
			zt = &zoneTotals{pressure: ZonePressure{Name: node.Zone}}
			zones[node.Zone] = zt
		}
		zt.pressure.Nodes++
		zt.pressure.CPUAvailable += cpuAvailable
		zt.pressure.MemAvailable += memAvailable
		zt.cpuAlloc += node.CPU
		zt.memAlloc += node.MemoryGi
	}

	sort.Slice(wp.PerNode, func(i, j int) bool {
		return wp.PerNode[i].Name < wp.PerNode[j].Name
	})
	for _, zt := range zones {
		if zt.cpuAlloc > 0 {
			zt.pressure.Utilization = float64(zt.cpuAlloc-zt.pressure.CPUAvailable) / float64(zt.cpuAlloc)
		}
		wp.PerZone = append(wp.PerZone, zt.pressure)
	}
	sort.Slice(wp.PerZone, func(i, j int) bool {
		return wp.PerZone[i].Name < wp.PerZone[j].Name
	})

	return wp
}

type fioOutput struct {
	Jobs []fioJob `json:"jobs"`
}

// fioJob is one job of fio's JSON output.
type fioJob struct {
	Read  fioDirection `json:"read"`
	Write fioDirection `json:"write"`
}

// fioDirection is a fio job's read or write side; latency is in ns.
type fioDirection struct {
	IOPS float64 `json:"iops"`
	Lat  struct {
		Mean float64 `json:"mean"`
	} `json:"lat_ns"`
}

const (
	// storageBenchScriptName is the ConfigMap the bench pod mounts its
	// script from.
	storageBenchScriptName = "kates-detect-bench-script"
	// storageBenchTimeout bounds the wait for the bench pod to finish: a
	// 15 s fio run, the fio package install, and slack for a slow image pull.
	storageBenchTimeout = "120s"
	// storageBenchScript is what the bench pod runs. It lives in a ConfigMap,
	// never on a `sh -c` command line.
	storageBenchScript = `#!/bin/sh
set -e
apk add --no-cache fio >&2
exec fio --name=bench-test --filename=/data/testfile --size=64M --rw=randrw --rwmixwrite=50 --bs=4k --direct=1 --numjobs=1 --time_based --runtime=15 --group_reporting --output-format=json
`
)

// storageBenchPVCManifest renders the 1Gi claim the bench pod mounts, for
// kubectl apply -f.
func storageBenchPVCManifest(name, namespace, storageClass string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: %s
  namespace: %s
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: %s
  resources:
    requests:
      storage: 1Gi
`, strconv.Quote(name), strconv.Quote(namespace), strconv.Quote(storageClass))
}

func (c *Collector) runLiveStorageBench(ctx context.Context, report *DetectReport) {
	if len(report.Nodes) == 0 || len(report.Storage) == 0 {
		return
	}

	// Pick target node (first node)
	nodeName := report.Nodes[0].Name

	runID := strconv.FormatInt(time.Now().UnixNano(), 36)
	ns := fmt.Sprintf("kates-detect-bench-%s", runID)

	c.progress("Storage benchmarking: setting up temporary namespace...")
	if err := c.createProbeNamespace(ctx, ns, runID); err != nil {
		return
	}

	// Setup double-layer cleanup
	defer func() {
		c.progress("Storage benchmarking: cleaning up ephemeral resources...")
		c.cleanup(ctx, ns)
	}()

	// The bench script reaches the pod through a ConfigMap, not a shell
	// program on the command line: the pod runs `sh /scripts/bench.sh`.
	scriptPath, err := writeProbeFile("kates-detect-bench-*.sh", storageBenchScript)
	if err != nil {
		return
	}
	defer os.Remove(scriptPath)
	if _, err := c.run(ctx, "kubectl", "create", "configmap", storageBenchScriptName, "-n", ns, "--from-file=bench.sh="+scriptPath); err != nil {
		c.progress("Storage benchmarking: failed to create the bench script ConfigMap, falling back to heuristics...")
		return
	}

	// Benchmark each StorageClass sequentially to avoid disk self-contention
	for idx := range report.Storage {
		if !c.benchStorageClass(ctx, ns, nodeName, idx, &report.Storage[idx]) {
			return
		}
	}
}

// benchStorageClass runs the fio pod against a fresh PVC of class sc and,
// when it measures any IOPS, overwrites the estimate in sc. A step that
// fails leaves the estimate alone; it returns false only when the bench
// cannot go on to the next class either.
func (c *Collector) benchStorageClass(ctx context.Context, ns, nodeName string, idx int, sc *SCInfo) bool {
	scName := sc.Name
	logOut, ok, cont := c.runStorageBenchPod(ctx, ns, nodeName, idx, scName)
	if !ok {
		return cont
	}

	// Parse the FIO JSON block from logs
	firstBrace := strings.Index(logOut, "{")
	if firstBrace == -1 {
		c.progress(fmt.Sprintf("Storage benchmarking: FIO output format unexpected on %q, falling back to heuristics...", scName))
		return true
	}
	var fioData fioOutput
	if err := json.Unmarshal([]byte(logOut[firstBrace:]), &fioData); err != nil {
		c.progress(fmt.Sprintf("Storage benchmarking: FIO JSON parse error on %q, falling back to heuristics...", scName))
		return true
	}
	if len(fioData.Jobs) == 0 {
		return true
	}

	totalIOPS, meanLat := fioJobMeasurement(fioData.Jobs[0])
	if totalIOPS > 0 {
		c.progress(fmt.Sprintf("Storage benchmarking: successfully measured %q (%d IOPS, %.2fms latency)!", scName, totalIOPS, meanLat))
		// Overwrite with actual dynamic measurements
		sc.ProbedIOPS = totalIOPS
		sc.ProbeLatencyMs = meanLat
		sc.IOPSMeasured = true
	} else {
		c.progress(fmt.Sprintf("Storage benchmarking: measured 0 IOPS on %q, falling back to heuristics...", scName))
	}
	return true
}

// fioJobMeasurement is a fio job's total IOPS and its mean latency in
// milliseconds, averaged over the directions that did any I/O.
func fioJobMeasurement(job fioJob) (iops int, meanLatMs float64) {
	iops = int(job.Read.IOPS + job.Write.IOPS)
	switch {
	case job.Read.IOPS > 0 && job.Write.IOPS > 0:
		meanLatMs = (job.Read.Lat.Mean + job.Write.Lat.Mean) / 2.0 / 1000000.0
	case job.Read.IOPS > 0:
		meanLatMs = job.Read.Lat.Mean / 1000000.0
	case job.Write.IOPS > 0:
		meanLatMs = job.Write.Lat.Mean / 1000000.0
	}
	return iops, meanLatMs
}

// runStorageBenchPod provisions the PVC, runs the bench pod pinned to
// nodeName so WaitForFirstConsumer binds at once, waits for it to finish,
// and returns its log. When ok is false the step that failed has said so;
// cont then says whether the next class may still be tried.
func (c *Collector) runStorageBenchPod(ctx context.Context, ns, nodeName string, idx int, scName string) (logOut string, ok, cont bool) {
	pvcName := fmt.Sprintf("kates-detect-bench-pvc-%d", idx)
	podName := fmt.Sprintf("kates-detect-bench-pod-%d", idx)
	fallback := func(format string) (string, bool, bool) {
		c.progress(fmt.Sprintf(format, scName))
		return "", false, true // fallback remains unchanged
	}

	c.progress(fmt.Sprintf("Storage benchmarking: provisioning ephemeral PVC on StorageClass %q...", scName))
	pvcPath, err := writeProbeFile("kates-detect-bench-pvc-*.yaml", storageBenchPVCManifest(pvcName, ns, scName))
	if err != nil {
		return "", false, false
	}
	_, pvcErr := c.run(ctx, "kubectl", "apply", "-f", pvcPath)
	os.Remove(pvcPath)
	if pvcErr != nil {
		return fallback("Storage benchmarking: failed to provision PVC on %q, falling back to heuristics...")
	}

	c.progress(fmt.Sprintf("Storage benchmarking: launching ephemeral FIO pod for %q...", scName))
	podOverrides := fmt.Sprintf(`{"spec":{"nodeName":%q,"volumes":[{"name":"bench-volume","persistentVolumeClaim":{"claimName":%q}},{"name":"bench-script","configMap":{"name":%q}}],"containers":[{"name":"fio-bench","image":%q,"volumeMounts":[{"name":"bench-volume","mountPath":"/data"},{"name":"bench-script","mountPath":"/scripts"}],"command":["sh","/scripts/bench.sh"]}]}}`,
		nodeName, pvcName, storageBenchScriptName, probeImage("alpine:3.19"))
	if _, err := c.run(ctx, "kubectl", "run", podName,
		"--image="+probeImage("alpine:3.19"), "-n", ns,
		"--overrides", podOverrides,
		"--restart=Never"); err != nil {
		return fallback("Storage benchmarking: failed to launch pod on %q, falling back to heuristics...")
	}

	c.progress(fmt.Sprintf("Storage benchmarking: waiting for FIO pod to be ready on %q...", scName))
	if _, err := c.runFor(ctx, 90*time.Second, "kubectl", "wait", "--for=condition=Ready", "pod/"+podName, "-n", ns, "--timeout=60s"); err != nil {
		return fallback("Storage benchmarking: pod failed to become ready on %q, falling back to heuristics...")
	}

	c.progress(fmt.Sprintf("Storage benchmarking: executing 15s random I/O benchmark on %q...", scName))
	// Wait for the run to end, bounded (the fio run is 15 s plus the
	// package install), then read the logs once: `logs -f` had no bound.
	if _, err := c.runFor(ctx, 150*time.Second, "kubectl", "wait", "--for=jsonpath={.status.phase}=Succeeded", "pod/"+podName, "-n", ns, "--timeout="+storageBenchTimeout); err != nil {
		c.progress(fmt.Sprintf("Storage benchmarking: benchmark did not finish within %s on %q, falling back to heuristics...", storageBenchTimeout, scName))
		return "", false, true
	}
	logOut, err = c.run(ctx, "kubectl", "logs", podName, "-n", ns)
	if err != nil {
		return fallback("Storage benchmarking: failed to capture benchmark logs on %q, falling back to heuristics...")
	}
	return logOut, true, true
}

type iperfOutput struct {
	End struct {
		SumReceived struct {
			BitsPerSecond float64 `json:"bits_per_second"`
		} `json:"sum_received"`
	} `json:"end"`
	Error string `json:"error"`
}

// createZoneProbers runs one sleeping pod of image per zone, pinned to the
// zone's node and named by zoneProberPod.
func (c *Collector) createZoneProbers(ctx context.Context, ns, runID, image, container string, zoneToNode map[string]string) error {
	var g errgroup.Group
	for zone, nodeName := range zoneToNode {
		podName := zoneProberPod(zone)
		g.Go(func() error {
			overrides := fmt.Sprintf(`{"spec":{"nodeName":"%s","containers":[{"name":"`+container+`","image":"`+image+`","command":["sleep","3600"]}]}}`, nodeName)
			_, runErr := c.run(ctx, "kubectl", "run", podName, "--image="+image, "-n", ns,
				"--labels", fmt.Sprintf("kates-detect-experimental=true,kates-detect-run=%s", runID),
				"--overrides", overrides, "--restart=Never")
			return runErr
		})
	}
	return g.Wait()
}

// waitZoneProbers waits for every prober pod in ns to be Ready.
func (c *Collector) waitZoneProbers(ctx context.Context, ns string) error {
	_, err := c.runFor(ctx, 45*time.Second, "kubectl", "wait", "--for=condition=Ready", "pod", "-n", ns, "-l", "kates-detect-experimental=true", "--timeout=30s")
	return err
}

// zoneProberIPs returns the prober pods' IPs by pod name; ok is false when
// the list cannot be read.
func (c *Collector) zoneProberIPs(ctx context.Context, ns string) (podIPs map[string]string, ok bool) {
	podListOut, err := c.run(ctx, "kubectl", "get", "pods", "-n", ns, "-l", "kates-detect-experimental=true", "-o", "json")
	if err != nil {
		return nil, false
	}
	var podData struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				PodIP string `json:"podIP"`
			} `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal([]byte(podListOut), &podData) != nil {
		return nil, false
	}
	podIPs = make(map[string]string)
	for _, item := range podData.Items {
		podIPs[item.Metadata.Name] = item.Status.PodIP
	}
	return podIPs, true
}

func (c *Collector) runAZBandwidthBench(ctx context.Context, report *DetectReport) {
	zoneToNode := firstNodePerZone(report.Nodes)

	// If zero or only one zone is detected, cross-AZ bandwidth doesn't apply
	if len(zoneToNode) <= 1 {
		return
	}

	runID := strconv.FormatInt(time.Now().UnixNano(), 36)
	ns := fmt.Sprintf("kates-detect-bandwidth-%s", runID)

	c.progress("Auditing inter-AZ network bandwidth: setting up temporary namespace...")
	if err := c.createProbeNamespace(ctx, ns, runID); err != nil {
		return
	}

	defer func() {
		c.progress("Auditing inter-AZ network bandwidth: cleaning up ephemeral resources...")
		c.cleanup(ctx, ns)
	}()

	podIPs, ok := c.startBandwidthProbers(ctx, ns, runID, zoneToNode)
	if !ok {
		return
	}

	// Loop over unique source/target zone combinations sequentially to avoid port conflict or saturation
	var results []BandwidthResult
	for srcZone := range zoneToNode {
		for dstZone := range zoneToNode {
			if srcZone == dstZone {
				continue
			}
			dstIP := podIPs[zoneProberPod(dstZone)]
			if dstIP == "" {
				continue
			}
			results = append(results, c.sweepBandwidth(ctx, ns, srcZone, dstZone, dstIP))
		}
	}

	report.Network.BandwidthMatrix = results
}

// startBandwidthProbers runs an alpine pod with iperf3 in each zone and
// returns their IPs by pod name; ok is false when any step fails.
func (c *Collector) startBandwidthProbers(ctx context.Context, ns, runID string, zoneToNode map[string]string) (podIPs map[string]string, ok bool) {
	c.progress("Auditing inter-AZ network bandwidth: creating iperf3 prober pods...")
	if err := c.createZoneProbers(ctx, ns, runID, probeImage("alpine:3.19"), "alpine", zoneToNode); err != nil {
		c.progress("Auditing inter-AZ network bandwidth: failed to create prober pods, falling back gracefully...")
		return nil, false
	}

	c.progress("Auditing inter-AZ network bandwidth: waiting for prober pods to be Ready...")
	if err := c.waitZoneProbers(ctx, ns); err != nil {
		c.progress("Auditing inter-AZ network bandwidth: pods failed to become ready, falling back gracefully...")
		return nil, false
	}

	c.progress("Auditing inter-AZ network bandwidth: installing iperf3 on prober pods...")
	var gInstall errgroup.Group
	for zone := range zoneToNode {
		podName := zoneProberPod(zone)
		gInstall.Go(func() error {
			_, installErr := c.runFor(ctx, 90*time.Second, "kubectl", "exec", "-n", ns, podName, "--", "apk", "add", "--no-cache", "iperf3")
			return installErr
		})
	}
	if err := gInstall.Wait(); err != nil {
		c.progress("Auditing inter-AZ network bandwidth: failed to install iperf3, falling back gracefully...")
		return nil, false
	}

	return c.zoneProberIPs(ctx, ns)
}

// sweepBandwidth measures srcZone -> dstZone with a one-shot iperf3 server
// on the target's prober and a 5-second client on the source's.
func (c *Collector) sweepBandwidth(ctx context.Context, ns, srcZone, dstZone, dstIP string) BandwidthResult {
	srcPod := zoneProberPod(srcZone)
	dstPod := zoneProberPod(dstZone)

	c.progress(fmt.Sprintf("Auditing inter-AZ network bandwidth: sweeping %s -> %s...", srcZone, dstZone))

	// Start server on destination pod (runs exactly once and terminates using -1)
	if _, srvErr := c.run(ctx, "kubectl", "exec", "-n", ns, dstPod, "--", "sh", "-c", "iperf3 -s -p 5201 -1 >/dev/null 2>&1 &"); srvErr != nil {
		return BandwidthResult{SourceZone: srcZone, TargetZone: dstZone, Success: false}
	}

	// Sleep briefly to let server start
	time.Sleep(1000 * time.Millisecond)

	// Run client on source pod (runs 5 seconds)
	clientOut, clientErr := c.run(ctx, "kubectl", "exec", "-n", ns, srcPod, "--", "iperf3", "-c", dstIP, "-p", "5201", "-t", "5", "-J")
	var bandwidthMbps float64
	var success bool
	if clientErr == nil {
		bandwidthMbps, success = parseIperfMbps(clientOut)
	}

	if success {
		c.progress(fmt.Sprintf("Auditing inter-AZ network bandwidth: successfully measured %s -> %s: %.2f Mbps", srcZone, dstZone, bandwidthMbps))
	} else {
		c.progress(fmt.Sprintf("Auditing inter-AZ network bandwidth: failed to sweep %s -> %s, falling back gracefully...", srcZone, dstZone))
	}

	return BandwidthResult{
		SourceZone:    srcZone,
		TargetZone:    dstZone,
		BandwidthMbps: bandwidthMbps,
		Success:       success,
	}
}

// parseIperfMbps reads the received rate from iperf3 -J output (anything
// before the JSON is skipped); ok is false when there is no positive rate.
func parseIperfMbps(out string) (mbps float64, ok bool) {
	firstBrace := strings.Index(out, "{")
	if firstBrace == -1 {
		return 0, false
	}
	var iperfData iperfOutput
	if json.Unmarshal([]byte(out[firstBrace:]), &iperfData) != nil {
		return 0, false
	}
	if iperfData.Error != "" || iperfData.End.SumReceived.BitsPerSecond <= 0 {
		return 0, false
	}
	return iperfData.End.SumReceived.BitsPerSecond / 1000000.0, true
}

func (c *Collector) runDNSResolutionProbe(ctx context.Context, report *DetectReport) {
	if len(report.Nodes) == 0 {
		return
	}

	runID := strconv.FormatInt(time.Now().UnixNano(), 36)
	ns := fmt.Sprintf("kates-detect-dns-%s", runID)

	c.progress("Auditing CoreDNS performance: setting up temporary namespace...")
	if err := c.createProbeNamespace(ctx, ns, runID); err != nil {
		return
	}

	defer func() {
		c.progress("Auditing CoreDNS performance: cleaning up ephemeral resources...")
		c.cleanup(ctx, ns)
	}()

	const podName = "dns-prober"
	if !c.startDNSProber(ctx, ns, runID, podName, report.Nodes[0].Name) {
		return
	}

	c.progress("Auditing CoreDNS performance: running rapid parallel DNS queries...")

	// Stage 1 read the cluster domain from CoreDNS; a cluster that is not
	// cluster.local would otherwise fail every internal query.
	clusterDomain := report.Network.ClusterDomain
	if clusterDomain == "" {
		clusterDomain = "cluster.local"
	}
	internalName := "kubernetes.default.svc." + clusterDomain

	const queries = 20
	var internalTimes, externalTimes []float64
	var g errgroup.Group
	g.Go(func() error {
		internalTimes = c.digTimes(ctx, ns, podName, internalName, queries)
		return nil
	})
	g.Go(func() error {
		externalTimes = c.digTimes(ctx, ns, podName, "google.com", queries)
		return nil
	})
	_ = g.Wait()

	c.progress("Auditing CoreDNS performance: compiling results...")

	internalResult := computeDNSProbeResult("Internal", internalTimes, queries)
	externalResult := computeDNSProbeResult("External", externalTimes, queries)

	report.Network.DNSResults = []DNSProbeResult{internalResult, externalResult}
}

// startDNSProber runs an alpine pod with dig on nodeName; it reports
// false, having said why, when the pod cannot be made ready.
func (c *Collector) startDNSProber(ctx context.Context, ns, runID, podName, nodeName string) bool {
	c.progress("Auditing CoreDNS performance: creating prober pod...")
	overrides := fmt.Sprintf(`{"spec":{"nodeName":"%s","containers":[{"name":"alpine","image":"`+probeImage("alpine:3.19")+`","command":["sleep","3600"]}]}}`, nodeName)
	if _, err := c.run(ctx, "kubectl", "run", podName, "--image="+probeImage("alpine:3.19"), "-n", ns,
		"--labels", fmt.Sprintf("kates-detect-experimental=true,kates-detect-run=%s", runID),
		"--overrides", overrides, "--restart=Never"); err != nil {
		c.progress("Auditing CoreDNS performance: failed to create prober pod, falling back gracefully...")
		return false
	}

	c.progress("Auditing CoreDNS performance: waiting for prober pod to be Ready...")
	if _, err := c.runFor(ctx, 45*time.Second, "kubectl", "wait", "--for=condition=Ready", "pod/"+podName, "-n", ns, "--timeout=30s"); err != nil {
		c.progress("Auditing CoreDNS performance: prober pod failed to become ready, falling back gracefully...")
		return false
	}

	c.progress("Auditing CoreDNS performance: installing bind-tools...")
	if _, err := c.runFor(ctx, 90*time.Second, "kubectl", "exec", "-n", ns, podName, "--", "apk", "add", "--no-cache", "bind-tools"); err != nil {
		c.progress("Auditing CoreDNS performance: failed to install bind-tools, falling back gracefully...")
		return false
	}
	return true
}

// digTimes resolves name n times in parallel from the prober pod and
// returns the query times of the lookups that answered.
func (c *Collector) digTimes(ctx context.Context, ns, podName, name string, n int) []float64 {
	timesChan := make(chan float64, n)
	var g errgroup.Group
	for range n {
		g.Go(func() error {
			out, err := c.run(ctx, "kubectl", "exec", "-n", ns, podName, "--", "dig", "+tries=1", "+timeout=2", name)
			if err == nil {
				if ms, ok := parseDigOutput(out); ok {
					timesChan <- ms
				}
			}
			return nil
		})
	}
	_ = g.Wait()
	close(timesChan)

	var times []float64
	for ms := range timesChan {
		times = append(times, ms)
	}
	return times
}

func parseDigOutput(out string) (float64, bool) {
	idx := strings.Index(out, ";; Query time:")
	if idx == -1 {
		return 0, false
	}
	sub := out[idx+len(";; Query time:"):]
	end := strings.Index(sub, "msec")
	if end == -1 {
		return 0, false
	}
	valStr := strings.TrimSpace(sub[:end])
	val, err := strconv.ParseFloat(valStr, 64)
	if err != nil {
		return 0, false
	}
	return val, true
}

func computeDNSProbeResult(queryType string, times []float64, totalRun int) DNSProbeResult {
	if len(times) == 0 {
		return DNSProbeResult{
			QueryType:    queryType,
			QueriesRun:   totalRun,
			SuccessCount: 0,
			SuccessRate:  0.0,
			AvgLatencyMs: 0.0,
			MaxLatencyMs: 0.0,
		}
	}
	var sum float64
	maxMs := -1.0
	for _, t := range times {
		sum += t
		if t > maxMs {
			maxMs = t
		}
	}
	avg := sum / float64(len(times))
	successRate := (float64(len(times)) / float64(totalRun)) * 100.0
	return DNSProbeResult{
		QueryType:    queryType,
		QueriesRun:   totalRun,
		SuccessCount: len(times),
		SuccessRate:  successRate,
		AvgLatencyMs: avg,
		MaxLatencyMs: maxMs,
	}
}

func (c *Collector) getSecurityAudit(ctx context.Context, adm AdmissionInfo) SecurityAudit {
	audit := SecurityAudit{
		PSALabelEnforced: "none",
		KyvernoEnforced:  adm.Kyverno.Installed,
	}

	// 1. The kafka namespace's enforced Pod Security level
	if nsOut, err := c.run(ctx, "kubectl", "get", "ns", "kafka", "-o", "json"); err == nil {
		if level, ok := psaEnforceLabel(nsOut); ok {
			audit.PSALabelEnforced = level
		}
	}

	// 2. Check RBAC permissions (permissionsOk)
	audit.PermissionsOk = c.canCreateInKafkaNamespace(ctx)

	// 3. Scan for TLS secrets and verify expiration
	if secOut, err := c.run(ctx, "kubectl", "get", "secrets", "-n", "kafka", "-o", "json"); err == nil {
		audit.ExpiringCerts = tlsSecretExpiries(secOut, time.Now())
	}

	// 4. Audit roles inside target namespace for excessive privileges
	if rolesOut, err := c.run(ctx, "kubectl", "get", "roles", "-n", "kafka", "-o", "json"); err == nil {
		audit.HasExcessivePrivileges = rolesHaveWildcardRule(rolesOut)
	}

	return audit
}

// psaEnforceLabel reads the pod-security enforce label from a namespace's
// JSON; ok is false when the namespace has none.
func psaEnforceLabel(nsJSON string) (level string, ok bool) {
	var nsData struct {
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	if json.Unmarshal([]byte(nsJSON), &nsData) != nil {
		return "", false
	}
	level, ok = nsData.Metadata.Labels["pod-security.kubernetes.io/enforce"]
	return level, ok
}

// canCreateInKafkaNamespace asks whether the caller may create every kind
// of object the chart installs in the kafka namespace.
func (c *Collector) canCreateInKafkaNamespace(ctx context.Context) bool {
	for _, res := range []string{"deployments", "statefulsets", "configmaps", "secrets", "services", "persistentvolumeclaims"} {
		if check, _ := c.run(ctx, "kubectl", "auth", "can-i", "create", res, "-n", "kafka"); !strings.Contains(check, "yes") {
			return false
		}
	}
	return true
}

// tlsSecretExpiries lists the expiry of each kubernetes.io/tls secret in a
// secret list whose tls.crt parses, days counted from now.
func tlsSecretExpiries(secretsJSON string, now time.Time) []CertExpirationInfo {
	var secData struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Type string            `json:"type"`
			Data map[string]string `json:"data"`
		} `json:"items"`
	}
	if json.Unmarshal([]byte(secretsJSON), &secData) != nil {
		return nil
	}
	var certs []CertExpirationInfo
	for _, item := range secData.Items {
		if item.Type != "kubernetes.io/tls" {
			continue
		}
		cert := parseTLSCertB64(item.Data["tls.crt"])
		if cert == nil {
			continue
		}
		certs = append(certs, CertExpirationInfo{
			SecretName: item.Metadata.Name,
			// The CN is printed in the fix list's commands: a newline would add a line.
			Subject:    output.Printable(cert.Subject.CommonName),
			ExpiryDate: cert.NotAfter.Format("2006-01-02"),
			DaysLeft:   int(cert.NotAfter.Sub(now).Hours() / 24),
		})
	}
	return certs
}

// parseTLSCertB64 decodes the first certificate of a base64 PEM bundle, or
// returns nil.
func parseTLSCertB64(certB64 string) *x509.Certificate {
	if certB64 == "" {
		return nil
	}
	certBytes, err := base64.StdEncoding.DecodeString(certB64)
	if err != nil {
		return nil
	}
	block, _ := pem.Decode(certBytes)
	if block == nil {
		return nil
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil
	}
	return cert
}

// rolesHaveWildcardRule reports whether any rule of any Role in a role
// list grants verb * on resource *.
func rolesHaveWildcardRule(rolesJSON string) bool {
	var rolesData struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Rules []struct {
				APIGroups []string `json:"apiGroups"`
				Resources []string `json:"resources"`
				Verbs     []string `json:"verbs"`
			} `json:"rules"`
		} `json:"items"`
	}
	if json.Unmarshal([]byte(rolesJSON), &rolesData) != nil {
		return false
	}
	for _, role := range rolesData.Items {
		for _, rule := range role.Rules {
			if slices.Contains(rule.Verbs, "*") && slices.Contains(rule.Resources, "*") {
				return true
			}
		}
	}
	return false
}
