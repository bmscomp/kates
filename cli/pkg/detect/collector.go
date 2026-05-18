package detect

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/sync/errgroup"
)

// Collector fetches raw data from the cluster using the provided executor.
type Collector struct {
	exec CommandExecutor
}

func NewCollector(exec CommandExecutor) *Collector {
	return &Collector{exec: exec}
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
	return report, err
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
	crdOut, _ := c.exec.Exec("kubectl", "get", "crd", "kafkas.kafka.strimzi.io")
	info.CRDsPresent = !strings.Contains(crdOut, "NotFound")

	depOut, _ := c.exec.Exec("kubectl", "get", "deployment", "-A", "-o", "json")
	var data struct {
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
	
	if json.Unmarshal([]byte(depOut), &data) == nil {
		for _, dep := range data.Items {
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
	return info
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

	// Detect cluster DNS domain from CoreDNS Corefile
	corefileOut, _ := c.exec.Exec("kubectl", "get", "configmap", "coredns", "-n", "kube-system", "-o", "jsonpath={.data.Corefile}")
	info.ClusterDomain = "cluster.local"
	if corefileOut != "" {
		// Look for "kubernetes <domain>" directive in Corefile
		for _, line := range strings.Split(corefileOut, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "kubernetes ") {
				parts := strings.Fields(trimmed)
				if len(parts) >= 2 {
					domain := parts[1]
					// Filter out placeholder values
					if domain != "{" && !strings.HasPrefix(domain, "{") && domain != "" {
						info.ClusterDomain = domain
					}
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
