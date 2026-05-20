package detect

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

// Collector fetches raw data from the cluster using the provided executor.
type Collector struct {
	exec         CommandExecutor
	BenchStorage bool
	BenchNetwork bool
	BenchDNS     bool
	OnProgress   func(string)
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
	if _, err := c.exec.LookPath("kubectl"); err != nil {
		return fmt.Errorf("kubectl is not installed or not in PATH")
	}
	if _, err := c.exec.LookPath("helm"); err != nil {
		return fmt.Errorf("helm is not installed or not in PATH")
	}
	if _, err := c.exec.Exec("kubectl", "cluster-info"); err != nil {
		return fmt.Errorf("Kubernetes cluster is unreachable")
	}
	return nil
}

// Collect runs all introspection queries concurrently and returns a raw report state.
func (c *Collector) Collect(ctx context.Context) (*DetectReport, error) {
	report := &DetectReport{}
	g, _ := errgroup.WithContext(ctx)

	// Context & Server (Sequential since they are very fast and needed for provider)
	report.Context = c.getContext()
	report.Server = c.getServer()
	report.Provider = c.getProvider(report.Context)

	// Concurrent fetching
	g.Go(func() error {
		report.K8sVersion, report.K8sMinor = c.getK8sVersion()
		return nil
	})
	g.Go(func() error {
		report.HelmVersion, report.HelmMajor = c.getHelmVersion()
		return nil
	})
	g.Go(func() error {
		nodes, err := c.parseNodes()
		if err != nil {
			return err
		}
		report.Nodes = nodes
		report.Zones = c.groupNodesByZone(nodes)
		return nil
	})
	g.Go(func() error {
		report.Storage = c.getStorageClasses()
		return nil
	})
	g.Go(func() error {
		report.StorageAudit = c.getStorageAudit()
		return nil
	})
	g.Go(func() error {
		report.ExistingKafka = c.getExistingKafkaResources()
		return nil
	})
	g.Go(func() error {
		report.Strimzi = c.getStrimziStatus()
		return nil
	})
	g.Go(func() error {
		report.Monitoring = c.getMonitoringStatus()
		return nil
	})
	g.Go(func() error {
		report.Network = c.getNetworkStatus()
		return nil
	})
	g.Go(func() error {
		report.Admission = c.getAdmissionStatus()
		return nil
	})
	g.Go(func() error {
		report.NetPolAudit = c.getNetworkPolicyAudit()
		return nil
	})
	g.Go(func() error {
		report.Workload = c.getWorkloadPressure()
		return nil
	})

	err := g.Wait()
	if err != nil {
		return nil, err
	}

	// Concurrent execution of Stage 2 probes
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

	report.Strimzi.CapacityStatus = c.checkKafkaCapacity(report, 0.30)

	return report, nil
}

func (c *Collector) getContext() string {
	out, _ := c.exec.Exec("kubectl", "config", "current-context")
	if out == "" {
		return "unknown"
	}
	return out
}

func (c *Collector) getServer() string {
	out, _ := c.exec.Exec("kubectl", "config", "view", "--minify", "-o", "jsonpath={.clusters[0].cluster.server}")
	if out == "" {
		return "unknown"
	}
	return out
}

func (c *Collector) getProvider(ctx string) string {
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

func (c *Collector) getK8sVersion() (string, int) {
	out, err := c.exec.Exec("kubectl", "version", "-o", "json")
	if err != nil {
		return "unknown", 0
	}
	var data struct {
		ServerVersion struct {
			Major string `json:"major"`
			Minor string `json:"minor"`
		} `json:"serverVersion"`
	}
	json.Unmarshal([]byte(out), &data)
	minorStr := strings.TrimRight(data.ServerVersion.Minor, "+")
	minor, _ := strconv.Atoi(minorStr)
	return fmt.Sprintf("%s.%s", data.ServerVersion.Major, data.ServerVersion.Minor), minor
}

func (c *Collector) getHelmVersion() (string, int) {
	out, _ := c.exec.Exec("helm", "version", "--short")
	out = strings.TrimPrefix(out, "v")
	out = strings.Split(out, "+")[0]
	majorStr := strings.Split(out, ".")[0]
	major, _ := strconv.Atoi(majorStr)
	if out == "" {
		return "unknown", 0
	}
	return "v" + out, major
}

func (c *Collector) parseNodes() ([]NodeInfo, error) {
	out, err := c.exec.Exec("kubectl", "get", "nodes", "-o", "json")
	if err != nil {
		return nil, err
	}
	var data struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
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

		cpuStr := n.Status.Allocatable.CPU
		cpu, _ := strconv.Atoi(cpuStr)
		if strings.HasSuffix(cpuStr, "m") {
			cpu, _ = strconv.Atoi(strings.TrimSuffix(cpuStr, "m"))
		} else {
			cpu *= 1000
		}

		memStr := n.Status.Allocatable.Memory
		memKi, _ := strconv.Atoi(strings.TrimSuffix(memStr, "Ki"))

		nodes = append(nodes, NodeInfo{
			Name:     n.Metadata.Name,
			Zone:     zone,
			Roles:    roleStr,
			CPU:      cpu,
			MemoryGi: memKi / 1048576,
			Runtime:  n.Status.NodeInfo.ContainerRuntimeVersion,
			Kubelet:  n.Status.NodeInfo.KubeletVersion,
			OS:       n.Status.NodeInfo.OSImage,
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
	return zones
}

func (c *Collector) getStorageClasses() []SCInfo {
	out, err := c.exec.Exec("kubectl", "get", "sc", "-o", "json")
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
	json.Unmarshal([]byte(out), &data)
	var scs []SCInfo
	for _, sc := range data.Items {
		isDefault := sc.Metadata.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" ||
			sc.Metadata.Annotations["storageclass.beta.kubernetes.io/is-default-class"] == "true"
		allowExpand := false
		if sc.AllowVolumeExpansion != nil {
			allowExpand = *sc.AllowVolumeExpansion
		}
		// Probe the storage class performance
		iops, latency, err := c.ProbeStorageClass(sc.Metadata.Name)
		if err != nil {
			// If probing fails, we just record 0
			iops = 0
			latency = 0.0
		}

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

func (c *Collector) getStorageAudit() StorageAudit {
	audit := StorageAudit{}

	// PV inventory
	pvOut, _ := c.exec.Exec("kubectl", "get", "pv", "-o", "json")
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
			if pv.Status.Phase == "Bound" {
				audit.PVBoundCount++
			} else if pv.Status.Phase == "Available" {
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
	csiOut, _ := c.exec.Exec("kubectl", "get", "csidrivers", "-o", "jsonpath={.items[*].metadata.name}")
	if csiOut != "" {
		audit.CSIDrivers = strings.Fields(csiOut)
	}

	return audit
}

func (c *Collector) getExistingKafkaResources() KafkaResources {
	countLines := func(args ...string) int {
		out, _ := c.exec.Exec(args[0], args[1:]...)
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

	pvcOut, _ := c.exec.Exec("kubectl", "get", "pvc", "-n", "kafka", "--no-headers")
	if pvcOut != "" {
		res.PVCs = len(strings.Split(pvcOut, "\n"))
		res.BoundPVCs = strings.Count(pvcOut, "Bound")
	}

	helmOut, _ := c.exec.Exec("helm", "list", "-n", "kafka", "-o", "json")
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
		kafkaOut, _ := c.exec.Exec("kubectl", "get", "kafka", "-n", "kafka", "-o", "json")
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
				Version:  k.Status.KafkaVersion,
				Replicas: k.Spec.Kafka.Replicas,
			}
			for _, l := range k.Spec.Kafka.Listeners {
				health.Listeners = append(health.Listeners, KafkaListener{
					Name: l.Name, Type: l.Type, Port: l.Port, TLS: l.TLS,
				})
			}
			for _, c := range k.Status.Conditions {
				health.Conditions = append(health.Conditions, KafkaCondition{
					Type: c.Type, Status: c.Status, Reason: c.Reason, Message: c.Message,
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

func (c *Collector) getStrimziStatus() StrimziInfo {
	info := StrimziInfo{}
	
	// Check CRDs
	expectedCRDs := []string{
		"kafkas.kafka.strimzi.io",
		"kafkanodepools.kafka.strimzi.io",
		"kafkatopics.kafka.strimzi.io",
		"kafkausers.kafka.strimzi.io",
	}
	var missingCRDs []string
	for _, crd := range expectedCRDs {
		out, _ := c.exec.Exec("kubectl", "get", "crd", crd, "--no-headers")
		if out == "" || strings.Contains(out, "NotFound") || strings.Contains(out, "error") {
			missingCRDs = append(missingCRDs, crd)
		}
	}
	info.CRDsPresent = len(missingCRDs) == 0
	info.Health.MissingCRDs = missingCRDs

	// Find the deployment for strimzi operator
	depOut, _ := c.exec.Exec("kubectl", "get", "deployment", "-A", "-o", "json")
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

	if json.Unmarshal([]byte(depOut), &depData) == nil {
		for _, dep := range depData.Items {
			if strings.Contains(dep.Metadata.Name, "strimzi-cluster-operator") {
				info.Running = true
				info.Namespace = dep.Metadata.Namespace
				if len(dep.Spec.Template.Spec.Containers) > 0 {
					info.Image = dep.Spec.Template.Spec.Containers[0].Image
				}
				info.ReadyReplicas = dep.Status.ReadyReplicas
				info.TotalReplicas = dep.Status.Replicas
				break
			}
		}
	}

	// Auditing Strimzi Operator pods and warning logs
	if info.Running {
		podOut, _ := c.exec.Exec("kubectl", "get", "pods", "-n", info.Namespace, "-l", "name=strimzi-cluster-operator", "-o", "json")
		var podData struct {
			Items []struct {
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
				Status struct {
					Phase string `json:"phase"`
					ContainerStatuses []struct {
						Ready bool `json:"ready"`
					} `json:"containerStatuses"`
				} `json:"status"`
			} `json:"items"`
		}

		if json.Unmarshal([]byte(podOut), &podData) == nil && len(podData.Items) > 0 {
			pod := podData.Items[0]
			podReady := pod.Status.Phase == "Running"
			for _, cs := range pod.Status.ContainerStatuses {
				if !cs.Ready {
					podReady = false
				}
			}
			info.Health.PodsReady = podReady

			// Set health status based on replicas and CRDs
			if info.ReadyReplicas == info.TotalReplicas && podReady && info.CRDsPresent {
				info.Health.Status = "Healthy"
			} else if info.ReadyReplicas > 0 {
				info.Health.Status = "Degraded"
			} else {
				info.Health.Status = "Unhealthy"
			}

			// Fetch logs
			logs, _ := c.exec.Exec("kubectl", "logs", "-n", info.Namespace, pod.Metadata.Name, "--tail=100")
			var warningLogs []string
			for _, line := range strings.Split(logs, "\n") {
				lineLower := strings.ToLower(line)
				if strings.Contains(lineLower, "warn") || strings.Contains(lineLower, "error") || 
					strings.Contains(lineLower, "exception") || strings.Contains(lineLower, "denied") || 
					strings.Contains(lineLower, "leader election") || strings.Contains(lineLower, "permission") {
					warningLogs = append(warningLogs, line)
					if len(warningLogs) >= 10 {
						break
					}
				}
			}
			info.Health.WarningLogs = warningLogs
		} else {
			info.Health.Status = "Unhealthy"
		}
	} else {
		info.Health.Status = "Unhealthy"
	}

	return info
}

func (c *Collector) checkKafkaCapacity(report *DetectReport, reservePct float64) string {
	var totalCPU, totalMem int
	for _, n := range report.Nodes {
		totalCPU += n.CPU
		totalMem += n.MemoryGi
	}
	usedCPU := report.Workload.TotalCPURequests
	usedMem := report.Workload.TotalMemRequests
	
	availCPU := totalCPU - usedCPU
	availMem := totalMem - usedMem
	if availCPU < 0 { availCPU = 0 }
	if availMem < 0 { availMem = 0 }
	
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

func (c *Collector) checkSecretCreation(ctx context.Context) SecretCreationAudit {
	audit := SecretCreationAudit{}
	
	runID := strconv.FormatInt(time.Now().UnixNano(), 36)
	ns := fmt.Sprintf("kates-detect-secrets-%s", runID)
	
	// Create namespace
	_, err := c.exec.Exec("kubectl", "create", "ns", ns)
	if err != nil {
		audit.NamespaceCreated = false
		audit.SecretCreated = false
		audit.ErrorMsg = fmt.Sprintf("failed to create namespace: %v", err)
		return audit
	}
	audit.NamespaceCreated = true
	
	// Defer cleanup of namespace (double-layer cleanup)
	defer func() {
		c.exec.Exec("kubectl", "delete", "ns", ns, "--wait=false")
	}()
	
	// Label the namespace
	_, _ = c.exec.Exec("kubectl", "label", "ns", ns, "kates-detect-experimental=true", fmt.Sprintf("kates-detect-run=%s", runID))
	
	// Try to create secret and capture combined stdout/stderr using sh -c
	cmdStr := fmt.Sprintf("kubectl create secret generic kates-detect-test-sec --from-literal=test-key=test-val -n %s 2>&1", ns)
	out, err := c.exec.Exec("sh", "-c", cmdStr)
	
	if err != nil {
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
	var results []LatencyResult
	
	// Group nodes by zone
	zoneToNode := make(map[string]string)
	for _, n := range nodes {
		if n.Zone == "" || n.Zone == "-" {
			continue
		}
		// Pick the first node in each zone
		if _, exists := zoneToNode[n.Zone]; !exists {
			zoneToNode[n.Zone] = n.Name
		}
	}
	
	// If zero or only one zone is detected, cross-AZ latency doesn't apply
	if len(zoneToNode) <= 1 {
		return nil
	}
	
	// Session run ID and namespace
	runID := strconv.FormatInt(time.Now().UnixNano(), 36)
	ns := fmt.Sprintf("kates-detect-latency-%s", runID)
	
	c.progress("Auditing inter-AZ network latency: setting up temporary namespace...")
	// Create namespace
	_, err := c.exec.Exec("kubectl", "create", "ns", ns)
	if err != nil {
		return nil
	}
	
	// Defer cleanup of namespace (double-layer cleanup)
	defer func() {
		c.progress("Auditing inter-AZ network latency: cleaning up resources...")
		c.exec.Exec("kubectl", "delete", "ns", ns, "--wait=false")
	}()
	
	// Label the namespace
	_, _ = c.exec.Exec("kubectl", "label", "ns", ns, "kates-detect-experimental=true", fmt.Sprintf("kates-detect-run=%s", runID))
	
	c.progress("Auditing inter-AZ network latency: creating prober pods...")
	
	// Spin up a prober pod in each zone
	g, ctx2 := errgroup.WithContext(ctx)
	for zone, nodeName := range zoneToNode {
		zone := zone
		nodeName := nodeName
		podName := fmt.Sprintf("prober-%s", sanitizeDNSLabel(zone))
		
		g.Go(func() error {
			overrides := fmt.Sprintf(`{"spec":{"nodeName":"%s","containers":[{"name":"busybox","image":"busybox:1.37","command":["sleep","3600"]}]}}`, nodeName)
			_, runErr := c.exec.Exec("kubectl", "run", podName, "--image=busybox:1.37", "-n", ns,
				"--labels", fmt.Sprintf("kates-detect-experimental=true,kates-detect-run=%s", runID),
				"--overrides", overrides, "--restart=Never")
			return runErr
		})
	}
	
	if err := g.Wait(); err != nil {
		// Failed to create one or more pods
		return nil
	}
	
	c.progress("Auditing inter-AZ network latency: waiting for prober pods to be Ready...")
	// Wait for all pods to be ready
	_, waitErr := c.exec.Exec("kubectl", "wait", "--for=condition=Ready", "pod", "-n", ns, "-l", "kates-detect-experimental=true", "--timeout=30s")
	if waitErr != nil {
		// Pods failed to become ready (e.g. image pull backoff)
		return nil
	}
	
	// Fetch Pod IPs
	podListOut, err := c.exec.Exec("kubectl", "get", "pods", "-n", ns, "-l", "kates-detect-experimental=true", "-o", "json")
	if err != nil {
		return nil
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
		return nil
	}
	
	podIPs := make(map[string]string)
	for _, item := range podData.Items {
		podIPs[item.Metadata.Name] = item.Status.PodIP
	}
	
	c.progress("Auditing inter-AZ network latency: running cross-AZ ping sweeps...")
	// Ping sweeps between all pairs of zones (matrix size: len(zoneToNode) x len(zoneToNode))
	resultsChan := make(chan LatencyResult, len(zoneToNode)*len(zoneToNode))
	pingGroup, _ := errgroup.WithContext(ctx2)
	
	for srcZone := range zoneToNode {
		for dstZone := range zoneToNode {
			srcZone := srcZone
			dstZone := dstZone
			srcPod := fmt.Sprintf("prober-%s", sanitizeDNSLabel(srcZone))
			dstPod := fmt.Sprintf("prober-%s", sanitizeDNSLabel(dstZone))
			dstIP := podIPs[dstPod]
			
			if dstIP == "" {
				continue
			}
			
			pingGroup.Go(func() error {
				pingOut, pingErr := c.exec.Exec("kubectl", "exec", "-n", ns, srcPod, "--", "ping", "-c", "5", dstIP)
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

func (c *Collector) getMonitoringStatus() MonitoringInfo {
	info := MonitoringInfo{}
	if out, _ := c.exec.Exec("kubectl", "get", "crd", "podmonitors.monitoring.coreos.com"); !strings.Contains(out, "NotFound") && out != "" {
		info.PodMonitorCRD = true
	}
	if out, _ := c.exec.Exec("kubectl", "get", "crd", "prometheusrules.monitoring.coreos.com"); !strings.Contains(out, "NotFound") && out != "" {
		info.PrometheusRuleCRD = true
	}
	if out, _ := c.exec.Exec("kubectl", "get", "deployment", "-n", "monitoring", "-l", "app.kubernetes.io/name=grafana", "--no-headers"); out != "" {
		info.GrafanaDeployed = true
	}
	label, _ := c.exec.Exec("kubectl", "get", "podmonitors", "-A", "-o", "jsonpath={.items[0].metadata.labels.release}")
	if label != "" {
		info.ReleaseLabel = label
	} else {
		info.ReleaseLabel = "monitoring"
	}
	return info
}

func (c *Collector) getNetworkStatus() NetworkInfo {
	info := NetworkInfo{CNI: "unknown"}
	if out, _ := c.exec.Exec("kubectl", "get", "pods", "-n", "kube-system", "-l", "k8s-app=calico-node", "--no-headers"); out != "" {
		info.CNI = "Calico"
	} else if out, _ := c.exec.Exec("kubectl", "get", "pods", "-n", "kube-system", "-l", "k8s-app=cilium", "--no-headers"); out != "" {
		info.CNI = "Cilium"
	} else if out, _ := c.exec.Exec("kubectl", "get", "pods", "-n", "kube-system", "-l", "app=kindnet", "--no-headers"); out != "" {
		info.CNI = "kindnet"
	} else if out, _ := c.exec.Exec("kubectl", "get", "ds", "-n", "kube-system", "kindnet", "--no-headers"); out != "" {
		info.CNI = "kindnet"
	} else if out, _ := c.exec.Exec("kubectl", "get", "ds", "-n", "kube-system", "-l", "app=flannel", "--no-headers"); out != "" {
		info.CNI = "Flannel"
	}

	depOut, _ := c.exec.Exec("kubectl", "get", "deployment", "-A", "-o", "json")
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

	info.PodCIDR, _ = c.exec.Exec("kubectl", "get", "nodes", "-o", "jsonpath={.items[0].spec.podCIDR}")
	if info.PodCIDR == "" {
		info.PodCIDR = "unknown"
	}

	// Detect cluster DNS domain using robust pod-based extraction
	info.ClusterDomain = "cluster.local"
	
	// Attempt 1: Get from an already running pod
	podOut, _ := c.exec.Exec("sh", "-c", "kubectl get pods --all-namespaces --field-selector=status.phase=Running -o jsonpath='{.items[0].metadata.namespace} {.items[0].metadata.name}' 2>/dev/null || true")
	var resolvContent string
	if podOut != "" {
		parts := strings.Fields(podOut)
		if len(parts) == 2 {
			resolvContent, _ = c.exec.Exec("kubectl", "exec", "-n", parts[0], parts[1], "--", "cat", "/etc/resolv.conf")
		}
	}
	
	// Attempt 2: If no running pods, spin up a temporary pod
	if resolvContent == "" {
		resolvContent, _ = c.exec.Exec("kubectl", "run", "--rm", "-i", "--image=busybox:1.36", "dns-detect", "--restart=Never", "--", "cat", "/etc/resolv.conf")
	}
	
	if resolvContent != "" {
		for _, line := range strings.Split(resolvContent, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(strings.ToLower(line), "search ") {
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
				
				if found != "" && found != "cluster.local" {
					info.ClusterDomain = found
				}
				break
			}
		}
	}

	// For bash pipe commands, we use sh -c 
	svcOut, _ := c.exec.Exec("sh", "-c", "kubectl get pod -n kube-system -l component=kube-apiserver -o jsonpath='{.items[0].spec.containers[0].command}' 2>/dev/null | grep -oE 'service-cluster-ip-range=[^\\\",]+' | cut -d= -f2 | head -1")
	if svcOut == "" {
		svcOut = "unknown"
	}
	info.ServiceCIDR = svcOut

	return info
}

func (c *Collector) getAdmissionStatus() AdmissionInfo {
	info := AdmissionInfo{}

	// Detect Kyverno and Gatekeeper deployments
	depOut, _ := c.exec.Exec("kubectl", "get", "deployment", "-A", "-o", "json")
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
		c.parseKyvernoPolicies(&info)
	}

	// OPA Gatekeeper constraints
	if info.Gatekeeper.Installed {
		c.parseGatekeeperConstraints(&info)
	}

	return info
}

func (c *Collector) parseKyvernoPolicies(info *AdmissionInfo) {
	polOut, _ := c.exec.Exec("kubectl", "get", "clusterpolicy", "-o", "json")
	var polData struct {
		Items []json.RawMessage `json:"items"`
	}
	if json.Unmarshal([]byte(polOut), &polData) != nil {
		return
	}

	for _, raw := range polData.Items {
		var pol struct {
			Metadata struct {
				Name        string            `json:"name"`
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
			Spec struct {
				ValidationFailureAction string `json:"validationFailureAction"`
				Rules []struct {
					Name  string `json:"name"`
					Match struct {
						Any []struct {
							Resources struct {
								Kinds      []string `json:"kinds"`
								Namespaces []string `json:"namespaces"`
							} `json:"resources"`
						} `json:"any"`
						Resources struct {
							Kinds      []string `json:"kinds"`
							Namespaces []string `json:"namespaces"`
						} `json:"resources"`
					} `json:"match"`
				} `json:"rules"`
			} `json:"spec"`
		}
		if json.Unmarshal(raw, &pol) != nil {
			continue
		}

		action := strings.ToLower(pol.Spec.ValidationFailureAction)
		if action == "" {
			action = "audit"
		}

		category := pol.Metadata.Annotations["policies.kyverno.io/category"]
		if category == "" {
			category = pol.Metadata.Annotations["kyverno.io/category"]
		}

		description := pol.Metadata.Annotations["policies.kyverno.io/description"]

		var ruleNames []string
		affectsKafka := false
		for _, r := range pol.Spec.Rules {
			ruleNames = append(ruleNames, r.Name)
			// Check if rule targets kafka namespace or all namespaces
			if len(r.Match.Resources.Namespaces) == 0 && len(r.Match.Any) == 0 {
				// No namespace filter = applies to all namespaces including kafka
				affectsKafka = true
			}
			for _, ns := range r.Match.Resources.Namespaces {
				if strings.ToLower(ns) == "kafka" || ns == "*" {
					affectsKafka = true
				}
			}
			for _, any := range r.Match.Any {
				if len(any.Resources.Namespaces) == 0 {
					affectsKafka = true
				}
				for _, ns := range any.Resources.Namespaces {
					if strings.ToLower(ns) == "kafka" || ns == "*" {
						affectsKafka = true
					}
				}
			}
		}

		policyInfo := KyvernoPolicyInfo{
			Name:         pol.Metadata.Name,
			Action:       action,
			Category:     category,
			AffectsKafka: affectsKafka,
			Rules:        ruleNames,
			Description:  description,
		}

		info.Kyverno.ClusterPolicies = append(info.Kyverno.ClusterPolicies, policyInfo)
		if affectsKafka {
			info.Kyverno.KafkaRelevant = append(info.Kyverno.KafkaRelevant, policyInfo)
		}

		// Detect constraint categories from policy name and rule names
		nameLower := strings.ToLower(pol.Metadata.Name)
		allNames := nameLower
		for _, rn := range ruleNames {
			allNames += " " + strings.ToLower(rn)
		}

		if action == "enforce" {
			if strings.Contains(allNames, "empty") && strings.Contains(allNames, "podselector") {
				info.Kyverno.Constraints.EmptyPodSelectorBlocked = true
			}
			if strings.Contains(allNames, "netpol") && strings.Contains(allNames, "podselector") {
				info.Kyverno.Constraints.EmptyPodSelectorBlocked = true
			}
			if strings.Contains(allNames, "host") && strings.Contains(allNames, "network") {
				info.Kyverno.Constraints.HostNetworkBlocked = true
			}
			if strings.Contains(allNames, "privileged") || strings.Contains(allNames, "privilege") {
				info.Kyverno.Constraints.PrivilegedBlocked = true
			}
			if strings.Contains(allNames, "run-as-root") || strings.Contains(allNames, "runasnonroot") || strings.Contains(allNames, "run-as-non-root") {
				info.Kyverno.Constraints.RunAsRootBlocked = true
			}
			if strings.Contains(allNames, "latest") && strings.Contains(allNames, "tag") {
				info.Kyverno.Constraints.LatestTagBlocked = true
			}
			if strings.Contains(allNames, "resource") && (strings.Contains(allNames, "limit") || strings.Contains(allNames, "request")) {
				info.Kyverno.Constraints.ResourceLimitsRequired = true
			}
		}
	}
}

func (c *Collector) parseGatekeeperConstraints(info *AdmissionInfo) {
	conOut, _ := c.exec.Exec("kubectl", "get", "constraints", "-o", "json")
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

func (c *Collector) getNetworkPolicyAudit() NetworkPolicyAudit {
	audit := NetworkPolicyAudit{}

	out, err := c.exec.Exec("kubectl", "get", "networkpolicy", "-n", "kafka", "-o", "json")
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
			managedBy = rel
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
func (c *Collector) getWorkloadPressure() WorkloadPressure {
	wp := WorkloadPressure{}

	out, err := c.exec.Exec("kubectl", "get", "pods", "-A",
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

	// Per-node accumulation
	nodeCPU := make(map[string]int)
	nodeMem := make(map[string]int)

	for _, pod := range data.Items {
		wp.TotalPods++
		if pod.Metadata.Namespace == "kafka" {
			wp.KafkaNamespacePods++
		}
		for _, c := range pod.Spec.Containers {
			cpuStr := c.Resources.Requests.CPU
			memStr := c.Resources.Requests.Memory

			// Parse CPU
			cpuM := 0
			if strings.HasSuffix(cpuStr, "m") {
				cpuM, _ = strconv.Atoi(strings.TrimSuffix(cpuStr, "m"))
			} else if cpuStr != "" {
				cores, _ := strconv.Atoi(cpuStr)
				cpuM = cores * 1000
			}

			// Parse Memory (to GiB, approximate)
			memGi := 0
			if strings.HasSuffix(memStr, "Gi") {
				memGi, _ = strconv.Atoi(strings.TrimSuffix(memStr, "Gi"))
			} else if strings.HasSuffix(memStr, "Mi") {
				mi, _ := strconv.Atoi(strings.TrimSuffix(memStr, "Mi"))
				memGi = mi / 1024 // rough conversion
			} else if strings.HasSuffix(memStr, "Ki") {
				ki, _ := strconv.Atoi(strings.TrimSuffix(memStr, "Ki"))
				memGi = ki / 1048576
			}

			wp.TotalCPURequests += cpuM
			wp.TotalMemRequests += memGi
			nodeCPU[pod.Spec.NodeName] += cpuM
			nodeMem[pod.Spec.NodeName] += memGi
		}
	}

	return wp
}

type fioOutput struct {
	Jobs []struct {
		Read struct {
			IOPS float64 `json:"iops"`
			Lat  struct {
				Mean float64 `json:"mean"`
			} `json:"lat_ns"`
		} `json:"read"`
		Write struct {
			IOPS float64 `json:"iops"`
			Lat  struct {
				Mean float64 `json:"mean"`
			} `json:"lat_ns"`
		} `json:"write"`
	} `json:"jobs"`
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
	// Create namespace
	_, err := c.exec.Exec("kubectl", "create", "ns", ns)
	if err != nil {
		return
	}

	// Setup double-layer cleanup
	defer func() {
		c.progress("Storage benchmarking: cleaning up ephemeral resources...")
		_, _ = c.exec.Exec("kubectl", "delete", "ns", ns, "--wait=false")
	}()

	// Label the namespace
	_, _ = c.exec.Exec("kubectl", "label", "ns", ns, "kates-detect-experimental=true", fmt.Sprintf("kates-detect-run=%s", runID))

	// Benchmark each StorageClass sequentially to avoid disk self-contention
	for idx, sc := range report.Storage {
		scName := sc.Name
		pvcName := fmt.Sprintf("kates-detect-bench-pvc-%d", idx)
		podName := fmt.Sprintf("kates-detect-bench-pod-%d", idx)

		c.progress(fmt.Sprintf("Storage benchmarking: provisioning ephemeral PVC on StorageClass %q...", scName))
		// 1. Create Dynamic PVC inside temporary namespace
		pvcManifest := fmt.Sprintf(`cat <<EOF | kubectl apply -f -
apiVersion: v1
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
EOF`, pvcName, ns, scName)

		_, pvcErr := c.exec.Exec("sh", "-c", pvcManifest)
		if pvcErr != nil {
			c.progress(fmt.Sprintf("Storage benchmarking: failed to provision PVC on %q, falling back to heuristics...", scName))
			continue // fallback remains unchanged
		}

		c.progress(fmt.Sprintf("Storage benchmarking: launching ephemeral FIO pod for %q...", scName))
		// 2. Create the Benchmark Pod pinned to target node to resolve WaitForFirstConsumer immediately
		podOverrides := fmt.Sprintf(`{"spec":{"nodeName":"%s","volumes":[{"name":"bench-volume","persistentVolumeClaim":{"claimName":"%s"}}],"containers":[{"name":"fio-bench","image":"alpine:3.19","volumeMounts":[{"name":"bench-volume","mountPath":"/data"}],"command":["sh","-c","apk add --no-cache fio && fio --name=bench-test --filename=/data/testfile --size=64M --rw=randrw --rwmixwrite=50 --bs=4k --direct=1 --numjobs=1 --time_based --runtime=15 --group_reporting --output-format=json"]}]}}`, nodeName, pvcName)

		_, runErr := c.exec.Exec("kubectl", "run", podName,
			"--image=alpine:3.19", "-n", ns,
			"--overrides", podOverrides,
			"--restart=Never")
		if runErr != nil {
			c.progress(fmt.Sprintf("Storage benchmarking: failed to launch pod on %q, falling back to heuristics...", scName))
			continue // fallback remains unchanged
		}

		c.progress(fmt.Sprintf("Storage benchmarking: waiting for FIO pod to be ready on %q...", scName))
		// 3. Wait for the pod to become Ready (timeout 60s)
		_, waitErr := c.exec.Exec("kubectl", "wait", "--for=condition=Ready", "pod/"+podName, "-n", ns, "--timeout=60s")
		if waitErr != nil {
			c.progress(fmt.Sprintf("Storage benchmarking: pod failed to become ready on %q, falling back to heuristics...", scName))
			continue // fallback remains unchanged
		}

		c.progress(fmt.Sprintf("Storage benchmarking: executing 15s random I/O benchmark on %q...", scName))
		// 4. Stream and capture logs (kubectl logs -f blocks until pod exits)
		logOut, logErr := c.exec.Exec("kubectl", "logs", "-f", podName, "-n", ns)
		if logErr != nil {
			c.progress(fmt.Sprintf("Storage benchmarking: failed to capture benchmark logs on %q, falling back to heuristics...", scName))
			continue // fallback remains unchanged
		}

		// 5. Parse the FIO JSON block from logs
		firstBrace := strings.Index(logOut, "{")
		if firstBrace == -1 {
			c.progress(fmt.Sprintf("Storage benchmarking: FIO output format unexpected on %q, falling back to heuristics...", scName))
			continue // fallback remains unchanged
		}
		jsonStr := logOut[firstBrace:]

		var fioData fioOutput
		if jsonUnmarshalErr := json.Unmarshal([]byte(jsonStr), &fioData); jsonUnmarshalErr != nil {
			c.progress(fmt.Sprintf("Storage benchmarking: FIO JSON parse error on %q, falling back to heuristics...", scName))
			continue // fallback remains unchanged
		}

		if len(fioData.Jobs) > 0 {
			job := fioData.Jobs[0]
			totalIOPS := int(job.Read.IOPS + job.Write.IOPS)
			
			var meanLat float64
			if job.Read.IOPS > 0 && job.Write.IOPS > 0 {
				meanLat = (job.Read.Lat.Mean + job.Write.Lat.Mean) / 2.0 / 1000000.0
			} else if job.Read.IOPS > 0 {
				meanLat = job.Read.Lat.Mean / 1000000.0
			} else if job.Write.IOPS > 0 {
				meanLat = job.Write.Lat.Mean / 1000000.0
			}

			if totalIOPS > 0 {
				c.progress(fmt.Sprintf("Storage benchmarking: successfully measured %q (%d IOPS, %.2fms latency)!", scName, totalIOPS, meanLat))
				// Overwrite with actual dynamic measurements
				report.Storage[idx].ProbedIOPS = totalIOPS
				report.Storage[idx].ProbeLatencyMs = meanLat
			} else {
				c.progress(fmt.Sprintf("Storage benchmarking: measured 0 IOPS on %q, falling back to heuristics...", scName))
			}
		}
	}
}

type iperfOutput struct {
	End struct {
		SumReceived struct {
			BitsPerSecond float64 `json:"bits_per_second"`
		} `json:"sum_received"`
	} `json:"end"`
	Error string `json:"error"`
}

func (c *Collector) runAZBandwidthBench(ctx context.Context, report *DetectReport) {
	// Group nodes by zone
	zoneToNode := make(map[string]string)
	for _, n := range report.Nodes {
		if n.Zone == "" || n.Zone == "-" {
			continue
		}
		// Pick the first node in each zone
		if _, exists := zoneToNode[n.Zone]; !exists {
			zoneToNode[n.Zone] = n.Name
		}
	}

	// If zero or only one zone is detected, cross-AZ bandwidth doesn't apply
	if len(zoneToNode) <= 1 {
		return
	}

	runID := strconv.FormatInt(time.Now().UnixNano(), 36)
	ns := fmt.Sprintf("kates-detect-bandwidth-%s", runID)

	c.progress("Auditing inter-AZ network bandwidth: setting up temporary namespace...")
	_, err := c.exec.Exec("kubectl", "create", "ns", ns)
	if err != nil {
		return
	}

	defer func() {
		c.progress("Auditing inter-AZ network bandwidth: cleaning up ephemeral resources...")
		_, _ = c.exec.Exec("kubectl", "delete", "ns", ns, "--wait=false")
	}()

	// Label the namespace
	_, _ = c.exec.Exec("kubectl", "label", "ns", ns, "kates-detect-experimental=true", fmt.Sprintf("kates-detect-run=%s", runID))

	c.progress("Auditing inter-AZ network bandwidth: creating iperf3 prober pods...")

	// Spin up a prober pod in each zone
	g, ctx2 := errgroup.WithContext(ctx)
	podNames := make(map[string]string)
	for zone, nodeName := range zoneToNode {
		zone := zone
		nodeName := nodeName
		podName := fmt.Sprintf("prober-%s", sanitizeDNSLabel(zone))
		podNames[zone] = podName

		g.Go(func() error {
			overrides := fmt.Sprintf(`{"spec":{"nodeName":"%s","containers":[{"name":"alpine","image":"alpine:3.19","command":["sleep","3600"]}]}}`, nodeName)
			_, runErr := c.exec.Exec("kubectl", "run", podName, "--image=alpine:3.19", "-n", ns,
				"--labels", fmt.Sprintf("kates-detect-experimental=true,kates-detect-run=%s", runID),
				"--overrides", overrides, "--restart=Never")
			return runErr
		})
	}

	if err := g.Wait(); err != nil {
		c.progress("Auditing inter-AZ network bandwidth: failed to create prober pods, falling back gracefully...")
		return
	}

	c.progress("Auditing inter-AZ network bandwidth: waiting for prober pods to be Ready...")
	_, waitErr := c.exec.Exec("kubectl", "wait", "--for=condition=Ready", "pod", "-n", ns, "-l", "kates-detect-experimental=true", "--timeout=30s")
	if waitErr != nil {
		c.progress("Auditing inter-AZ network bandwidth: pods failed to become ready, falling back gracefully...")
		return
	}

	c.progress("Auditing inter-AZ network bandwidth: installing iperf3 on prober pods...")
	gInstall, _ := errgroup.WithContext(ctx2)
	for _, podName := range podNames {
		podName := podName
		gInstall.Go(func() error {
			_, installErr := c.exec.Exec("kubectl", "exec", "-n", ns, podName, "--", "apk", "add", "--no-cache", "iperf3")
			return installErr
		})
	}
	if err := gInstall.Wait(); err != nil {
		c.progress("Auditing inter-AZ network bandwidth: failed to install iperf3, falling back gracefully...")
		return
	}

	// Fetch Pod IPs
	podListOut, err := c.exec.Exec("kubectl", "get", "pods", "-n", ns, "-l", "kates-detect-experimental=true", "-o", "json")
	if err != nil {
		return
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
		return
	}

	podIPs := make(map[string]string)
	for _, item := range podData.Items {
		podIPs[item.Metadata.Name] = item.Status.PodIP
	}

	// Loop over unique source/target zone combinations sequentially to avoid port conflict or saturation
	var results []BandwidthResult
	for srcZone := range zoneToNode {
		for dstZone := range zoneToNode {
			if srcZone == dstZone {
				continue
			}

			srcPod := podNames[srcZone]
			dstPod := podNames[dstZone]
			dstIP := podIPs[dstPod]

			if dstIP == "" {
				continue
			}

			c.progress(fmt.Sprintf("Auditing inter-AZ network bandwidth: sweeping %s -> %s...", srcZone, dstZone))

			// Start server on destination pod (runs exactly once and terminates using -1)
			_, srvErr := c.exec.Exec("kubectl", "exec", "-n", ns, dstPod, "--", "sh", "-c", "iperf3 -s -p 5201 -1 >/dev/null 2>&1 &")
			if srvErr != nil {
				results = append(results, BandwidthResult{
					SourceZone: srcZone,
					TargetZone: dstZone,
					Success:    false,
				})
				continue
			}

			// Sleep briefly to let server start
			time.Sleep(1000 * time.Millisecond)

			// Run client on source pod (runs 5 seconds)
			clientOut, clientErr := c.exec.Exec("kubectl", "exec", "-n", ns, srcPod, "--", "iperf3", "-c", dstIP, "-p", "5201", "-t", "5", "-J")

			var iperfData iperfOutput
			var success bool
			var bandwidthMbps float64

			if clientErr == nil {
				firstBrace := strings.Index(clientOut, "{")
				if firstBrace != -1 {
					jsonStr := clientOut[firstBrace:]
					if jsonUnmarshalErr := json.Unmarshal([]byte(jsonStr), &iperfData); jsonUnmarshalErr == nil {
						if iperfData.Error == "" && iperfData.End.SumReceived.BitsPerSecond > 0 {
							bandwidthMbps = iperfData.End.SumReceived.BitsPerSecond / 1000000.0
							success = true
						}
					}
				}
			}

			if success {
				c.progress(fmt.Sprintf("Auditing inter-AZ network bandwidth: successfully measured %s -> %s: %.2f Mbps", srcZone, dstZone, bandwidthMbps))
			} else {
				c.progress(fmt.Sprintf("Auditing inter-AZ network bandwidth: failed to sweep %s -> %s, falling back gracefully...", srcZone, dstZone))
			}

			results = append(results, BandwidthResult{
				SourceZone:    srcZone,
				TargetZone:    dstZone,
				BandwidthMbps: bandwidthMbps,
				Success:       success,
			})
		}
	}

	report.Network.BandwidthMatrix = results
}

func (c *Collector) runDNSResolutionProbe(ctx context.Context, report *DetectReport) {
	if len(report.Nodes) == 0 {
		return
	}

	runID := strconv.FormatInt(time.Now().UnixNano(), 36)
	ns := fmt.Sprintf("kates-detect-dns-%s", runID)

	c.progress("Auditing CoreDNS performance: setting up temporary namespace...")
	_, err := c.exec.Exec("kubectl", "create", "ns", ns)
	if err != nil {
		return
	}

	defer func() {
		c.progress("Auditing CoreDNS performance: cleaning up ephemeral resources...")
		_, _ = c.exec.Exec("kubectl", "delete", "ns", ns, "--wait=false")
	}()

	// Label the namespace
	_, _ = c.exec.Exec("kubectl", "label", "ns", ns, "kates-detect-experimental=true", fmt.Sprintf("kates-detect-run=%s", runID))

	c.progress("Auditing CoreDNS performance: creating prober pod...")

	nodeName := report.Nodes[0].Name
	podName := "dns-prober"
	overrides := fmt.Sprintf(`{"spec":{"nodeName":"%s","containers":[{"name":"alpine","image":"alpine:3.19","command":["sleep","3600"]}]}}`, nodeName)
	_, runErr := c.exec.Exec("kubectl", "run", podName, "--image=alpine:3.19", "-n", ns,
		"--labels", fmt.Sprintf("kates-detect-experimental=true,kates-detect-run=%s", runID),
		"--overrides", overrides, "--restart=Never")
	if runErr != nil {
		c.progress("Auditing CoreDNS performance: failed to create prober pod, falling back gracefully...")
		return
	}

	c.progress("Auditing CoreDNS performance: waiting for prober pod to be Ready...")
	_, waitErr := c.exec.Exec("kubectl", "wait", "--for=condition=Ready", "pod/"+podName, "-n", ns, "--timeout=30s")
	if waitErr != nil {
		c.progress("Auditing CoreDNS performance: prober pod failed to become ready, falling back gracefully...")
		return
	}

	c.progress("Auditing CoreDNS performance: installing bind-tools...")
	_, installErr := c.exec.Exec("kubectl", "exec", "-n", ns, podName, "--", "apk", "add", "--no-cache", "bind-tools")
	if installErr != nil {
		c.progress("Auditing CoreDNS performance: failed to install bind-tools, falling back gracefully...")
		return
	}

	c.progress("Auditing CoreDNS performance: running rapid parallel DNS queries...")

	var internalTimes []float64
	var externalTimes []float64

	internalChan := make(chan float64, 20)
	externalChan := make(chan float64, 20)

	g, _ := errgroup.WithContext(ctx)

	// Internal queries
	g.Go(func() error {
		gInt, _ := errgroup.WithContext(ctx)
		for i := 0; i < 20; i++ {
			gInt.Go(func() error {
				out, err := c.exec.Exec("kubectl", "exec", "-n", ns, podName, "--", "dig", "+tries=1", "+timeout=2", "kubernetes.default.svc.cluster.local")
				if err == nil {
					if ms, ok := parseDigOutput(out); ok {
						internalChan <- ms
					}
				}
				return nil
			})
		}
		_ = gInt.Wait()
		close(internalChan)
		return nil
	})

	// External queries
	g.Go(func() error {
		gExt, _ := errgroup.WithContext(ctx)
		for i := 0; i < 20; i++ {
			gExt.Go(func() error {
				out, err := c.exec.Exec("kubectl", "exec", "-n", ns, podName, "--", "dig", "+tries=1", "+timeout=2", "google.com")
				if err == nil {
					if ms, ok := parseDigOutput(out); ok {
						externalChan <- ms
					}
				}
				return nil
			})
		}
		_ = gExt.Wait()
		close(externalChan)
		return nil
	})

	_ = g.Wait()

	for ms := range internalChan {
		internalTimes = append(internalTimes, ms)
	}
	for ms := range externalChan {
		externalTimes = append(externalTimes, ms)
	}

	c.progress("Auditing CoreDNS performance: compiling results...")

	internalResult := computeDNSProbeResult("Internal", internalTimes, 20)
	externalResult := computeDNSProbeResult("External", externalTimes, 20)

	report.Network.DNSResults = []DNSProbeResult{internalResult, externalResult}
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
	var max float64 = -1.0
	for _, t := range times {
		sum += t
		if t > max {
			max = t
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
		MaxLatencyMs: max,
	}
}
