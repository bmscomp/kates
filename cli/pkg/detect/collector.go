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
			Provisioner       string `json:"provisioner"`
			VolumeBindingMode string `json:"volumeBindingMode"`
			ReclaimPolicy     string `json:"reclaimPolicy"`
		} `json:"items"`
	}
	json.Unmarshal([]byte(out), &data)
	var scs []SCInfo
	for _, sc := range data.Items {
		isDefault := sc.Metadata.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" ||
			sc.Metadata.Annotations["storageclass.beta.kubernetes.io/is-default-class"] == "true"
		scs = append(scs, SCInfo{
			Name:          sc.Metadata.Name,
			Provisioner:   sc.Provisioner,
			BindingMode:   sc.VolumeBindingMode,
			ReclaimPolicy: sc.ReclaimPolicy,
			IsDefault:     isDefault,
		})
	}
	return scs
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

	return res
}

func (c *Collector) getStrimziStatus() StrimziInfo {
	info := StrimziInfo{}
	crdOut, _ := c.exec.Exec("kubectl", "get", "crd", "kafkas.kafka.strimzi.io")
	info.CRDsPresent = !strings.Contains(crdOut, "NotFound")

	depOut, _ := c.exec.Exec("kubectl", "get", "deployment", "-A", "-l", "app.kubernetes.io/name=strimzi-cluster-operator", "-o", "json")
	var data struct {
		Items []struct {
			Metadata struct {
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
	if json.Unmarshal([]byte(depOut), &data) == nil && len(data.Items) > 0 {
		info.Running = true
		info.Namespace = data.Items[0].Metadata.Namespace
		if len(data.Items[0].Spec.Template.Spec.Containers) > 0 {
			info.Image = data.Items[0].Spec.Template.Spec.Containers[0].Image
		}
		info.ReadyReplicas = data.Items[0].Status.ReadyReplicas
		info.TotalReplicas = data.Items[0].Status.Replicas
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

	dnsOut, _ := c.exec.Exec("kubectl", "get", "pods", "-n", "kube-system", "-l", "k8s-app=kube-dns", "--no-headers")
	info.CoreDNSRunning = strings.Count(dnsOut, "Running")

	info.PodCIDR, _ = c.exec.Exec("kubectl", "get", "nodes", "-o", "jsonpath={.items[0].spec.podCIDR}")
	if info.PodCIDR == "" {
		info.PodCIDR = "unknown"
	}

	// For bash pipe commands, we use sh -c 
	svcOut, _ := c.exec.Exec("sh", "-c", "kubectl get pod -n kube-system -l component=kube-apiserver -o jsonpath='{.items[0].spec.containers[0].command}' 2>/dev/null | grep -oE 'service-cluster-ip-range=[^\\\",]+' | cut -d= -f2 | head -1")
	if svcOut == "" {
		svcOut = "unknown"
	}
	info.ServiceCIDR = svcOut

	return info
}
