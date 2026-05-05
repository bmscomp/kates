package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/klster/kates-cli/output"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	"gopkg.in/yaml.v3"
)

var detectCmd = &cobra.Command{
	Use:     "detect",
	Aliases: []string{"preflight-cluster", "cluster-check"},
	Short:   "Deep cluster compatibility report for 3-AZ Kafka",
	Example: "  kates detect\n  kates detect --output json",
	RunE:    runDetect,
}

var detectValuesFile string

func init() {
	detectCmd.Flags().StringVarP(&detectValuesFile, "values", "f", "", "Path to custom values.yaml for dynamic resource budgeting")
	rootCmd.AddCommand(detectCmd)
}

func runDetect(cmd *cobra.Command, args []string) error {
	if _, err := exec.LookPath("kubectl"); err != nil {
		output.Error("kubectl is not installed or not in PATH")
		return nil
	}
	if _, err := exec.LookPath("helm"); err != nil {
		output.Error("helm is not installed or not in PATH")
		return nil
	}
	if _, err := execCmd("kubectl", "cluster-info"); err != nil {
		output.Error("Kubernetes cluster is unreachable")
		return nil
	}

	if outputMode != "json" {
		fmt.Println()
		output.Hint("Introspecting cluster state...")
	}

	ctxStr := getContext()
	server := getServer()
	provider := getProvider(ctxStr)
	
	var k8sVer string
	var k8sMinor int
	var helmVer string
	var helmMajor int
	var nodes []NodeInfo
	var scs []SCInfo
	var existingKafka KafkaResources
	var strimzi StrimziInfo
	var monitoring MonitoringInfo
	var network NetworkInfo
	var parseErr error

	// Run concurrent data collection
	g, _ := errgroup.WithContext(context.Background())

	g.Go(func() error {
		k8sVer, k8sMinor = getK8sVersion()
		return nil
	})
	g.Go(func() error {
		helmVer, helmMajor = getHelmVersion()
		return nil
	})
	g.Go(func() error {
		nodes, parseErr = parseNodes()
		return parseErr
	})
	g.Go(func() error {
		scs = getStorageClasses()
		return nil
	})
	g.Go(func() error {
		existingKafka = getExistingKafkaResources()
		return nil
	})
	g.Go(func() error {
		strimzi = getStrimziStatus()
		return nil
	})
	g.Go(func() error {
		monitoring = getMonitoringStatus()
		return nil
	})
	g.Go(func() error {
		network = getNetworkStatus()
		return nil
	})

	// Run a spinner while collecting data (if not JSON)
	if outputMode != "json" {
		p := tea.NewProgram(spinnerModel{spinner: spinner.New(spinner.WithSpinner(spinner.Dot))})
		go func() {
			g.Wait()
			p.Quit()
		}()
		p.Run()
	} else {
		g.Wait()
	}

	if parseErr != nil {
		output.Error(fmt.Sprintf("Failed to get nodes: %v", parseErr))
		return nil
	}

	zones := groupNodesByZone(nodes)

	if outputMode == "json" {
		report := map[string]interface{}{
			"identity": map[string]string{
				"context":    ctxStr,
				"server":     server,
				"provider":   provider,
				"kubernetes": k8sVer,
				"helm":       helmVer,
			},
			"nodes":         nodes,
			"zones":         zones,
			"storage":       scs,
			"existingKafka": existingKafka,
			"strimzi":       strimzi,
			"monitoring":    monitoring,
			"network":       network,
		}
		output.JSON(report)
		return nil
	}

	// TUI Rendering

	output.Header("Cluster Identity")
	output.KeyValue("Context:", ctxStr)
	output.KeyValue("Server:", server)
	output.KeyValue("Provider:", provider)
	output.KeyValue("Kubernetes:", k8sVer)
	output.KeyValue("Helm:", helmVer)

	output.Header("Node Details")
	if len(nodes) > 0 {
		headers := []string{"NAME", "ZONE", "ROLES", "CPU", "MEMORY", "RUNTIME", "KUBELET"}
		var rows [][]string
		for _, n := range nodes {
			rows = append(rows, []string{
				n.Name, n.Zone, n.Roles, fmt.Sprintf("%dm", n.CPU), fmt.Sprintf("%dGi", n.MemoryGi), n.Runtime, n.Kubelet,
			})
		}
		output.Table(headers, rows)
		output.Success(fmt.Sprintf("Nodes: %d total", len(nodes)))
	} else {
		output.Warn("No nodes found")
	}

	output.Header("Per-Zone Capacity")
	if len(zones) > 0 {
		headers := []string{"ZONE", "NODES", "CPU", "MEMORY"}
		var rows [][]string
		for _, z := range zones {
			rows = append(rows, []string{
				z.Name, strconv.Itoa(z.Nodes), fmt.Sprintf("%dm", z.CPUAllocatable), fmt.Sprintf("%dGi", z.MemAllocatableGi),
			})
		}
		output.Table(headers, rows)
		output.Success(fmt.Sprintf("Zones: %d", len(zones)))
	} else {
		output.Warn("No zone labels found on nodes")
	}

	output.Header("Resource Budget")
	ctrlCPU, ctrlMem := 1500, 3
	brokerCPU, brokerMem := 3000, 12
	otherCPU, otherMem := 500, 1

	if detectValuesFile != "" {
		data, err := os.ReadFile(detectValuesFile)
		if err == nil {
			reqs := parseResourcesFromYAML(data)
			if reqs.BrokerCPU > 0 { brokerCPU = reqs.BrokerCPU }
			if reqs.BrokerMem > 0 { brokerMem = reqs.BrokerMem }
			if reqs.ControllerCPU > 0 { ctrlCPU = reqs.ControllerCPU }
			if reqs.ControllerMem > 0 { ctrlMem = reqs.ControllerMem }
			if reqs.OtherCPU > 0 { otherCPU = reqs.OtherCPU }
			if reqs.OtherMem > 0 { otherMem = reqs.OtherMem }
		}
	}

	needCPU := ctrlCPU + brokerCPU*3 + otherCPU
	needMem := ctrlMem + brokerMem*3 + otherMem

	var totalCPU, totalMem int
	for _, n := range nodes {
		totalCPU += n.CPU
		totalMem += n.MemoryGi
	}

	bHeaders := []string{"COMPONENT", "PODS", "CPU", "MEMORY"}
	bRows := [][]string{
		{"Controllers", "3", "1500m", "3Gi"},
		{"Brokers (az1)", "3", "3000m", "12Gi"},
		{"Brokers (az2)", "3", "3000m", "12Gi"},
		{"Brokers (az3)", "3", "3000m", "12Gi"},
		{"Operators + Exporter", "3", "500m", "1Gi"},
		{"TOTAL REQUIRED", "15", fmt.Sprintf("%dm", needCPU), fmt.Sprintf("%dGi", needMem)},
		{"CLUSTER AVAILABLE", strconv.Itoa(len(nodes)), fmt.Sprintf("%dm", totalCPU), fmt.Sprintf("%dGi", totalMem)},
	}
	output.Table(bHeaders, bRows)

	if totalCPU >= needCPU && totalMem >= needMem {
		output.Success(fmt.Sprintf("Resources sufficient (%dm CPU / %dGi available)", totalCPU, totalMem))
	} else {
		output.Error(fmt.Sprintf("Insufficient resources (need %dm CPU, %dGi memory)", needCPU, needMem))
	}

	output.Header("Storage Compatibility")
	if len(scs) > 0 {
		sHeaders := []string{"NAME", "PROVISIONER", "BINDING", "RECLAIM", "DEFAULT"}
		var sRows [][]string
		for _, sc := range scs {
			def := "✗"
			if sc.IsDefault {
				def = "PASS" // output.Table auto-colors PASS green
			}
			sRows = append(sRows, []string{
				sc.Name, sc.Provisioner, sc.BindingMode, sc.ReclaimPolicy, def,
			})
		}
		output.Table(sHeaders, sRows)
		output.Success(fmt.Sprintf("StorageClasses: %d available", len(scs)))
	} else {
		output.Error("No StorageClasses found")
	}

	output.Header("Existing Kafka Resources")
	warns := 0
	output.KeyValue("Kafka clusters:", strconv.Itoa(existingKafka.KafkaClusters))
	output.KeyValue("KafkaNodePools:", strconv.Itoa(existingKafka.KafkaNodePools))
	output.KeyValue("KafkaTopics:", strconv.Itoa(existingKafka.KafkaTopics))
	output.KeyValue("KafkaUsers:", strconv.Itoa(existingKafka.KafkaUsers))
	output.KeyValue("PVCs:", fmt.Sprintf("%d (%d bound)", existingKafka.PVCs, existingKafka.BoundPVCs))
	output.KeyValue("Helm release:", existingKafka.HelmRelease)

	if existingKafka.KafkaClusters > 0 {
		output.Warn("Existing Kafka deployment detected — upgrade mode recommended")
		warns++
	} else {
		output.Success("No existing Kafka deployment — clean install")
	}

	output.Header("Strimzi Operator")
	if strimzi.Running {
		output.KeyValue("Namespace:", strimzi.Namespace)
		output.KeyValue("Image:", strimzi.Image)
		output.KeyValue("Replicas:", fmt.Sprintf("%d/%d ready", strimzi.ReadyReplicas, strimzi.TotalReplicas))
		output.Success("Strimzi operator: running")
	} else {
		if strimzi.CRDsPresent {
			output.Warn("Strimzi CRDs present but operator not running")
			warns++
		} else {
			output.Warn("Strimzi not installed — chart will install operator subchart")
		}
	}

	output.Header("Monitoring Stack")
	if monitoring.PodMonitorCRD {
		output.Success("PodMonitor CRD: present")
	} else {
		output.Warn("PodMonitor CRD: not found")
	}
	if monitoring.PrometheusRuleCRD {
		output.Success("PrometheusRule CRD: present")
	} else {
		output.Warn("PrometheusRule CRD: not found")
	}
	if monitoring.GrafanaDeployed {
		output.Success("Grafana: deployed in monitoring")
	} else {
		output.Warn("Grafana: not found")
	}
	output.Success(fmt.Sprintf("Release label: %s", monitoring.ReleaseLabel))

	output.Header("Network & Connectivity")
	output.Success(fmt.Sprintf("CNI: %s", network.CNI))
	if network.CoreDNSRunning > 0 {
		output.Success(fmt.Sprintf("CoreDNS: %d replica(s) running", network.CoreDNSRunning))
	} else {
		output.Warn("CoreDNS: not detected")
	}
	output.Success(fmt.Sprintf("Pod CIDR: %s", network.PodCIDR))
	output.Success(fmt.Sprintf("Service CIDR: %s", network.ServiceCIDR))

	output.Header("3-AZ Kafka Compatibility Verdict")
	fails := 0

	var cRows [][]string
	addCheck := func(desc string, pass bool, detail string) {
		status := "PASS"
		if !pass {
			status = "FAIL"
			fails++
		}
		cRows = append(cRows, []string{desc, status, detail})
	}

	addCheck("Kubernetes version ≥ 1.25", k8sMinor >= 25, k8sVer)
	addCheck("Helm version ≥ 3.12", helmMajor >= 3, helmVer)
	addCheck("Strimzi CRDs installed", strimzi.CRDsPresent, "CRDs presence")
	addCheck("≥ 3 availability zones", len(zones) >= 3, fmt.Sprintf("%d zone(s)", len(zones)))
	min1Node := true
	for _, z := range zones {
		if z.Nodes < 1 {
			min1Node = false
		}
	}
	addCheck("≥ 1 node per zone", min1Node, fmt.Sprintf("%d nodes across %d zones", len(nodes), len(zones)))
	addCheck("StorageClass available", len(scs) > 0, fmt.Sprintf("%d class(es)", len(scs)))
	addCheck("Controller resources fit", totalCPU >= ctrlCPU && totalMem >= ctrlMem, fmt.Sprintf("%dm needed", ctrlCPU))
	addCheck("Broker resources fit (all zones)", totalCPU >= needCPU && totalMem >= needMem, fmt.Sprintf("%dm total needed", needCPU))
	addCheck("Replication factor 3 achievable", len(zones) >= 3, fmt.Sprintf("%d zones", len(zones)))
	addCheck("min.insync.replicas=2 safe", len(zones) >= 3, "can lose 1 zone")
	hasRbac := true
	for _, res := range []string{"deployments", "statefulsets", "configmaps", "secrets", "services", "persistentvolumeclaims"} {
		if check, _ := execCmd("kubectl", "auth", "can-i", "create", res, "-n", "kafka"); !strings.Contains(check, "yes") {
			hasRbac = false
			break
		}
	}
	addCheck("RBAC permissions", hasRbac, "kafka namespace")
	addCheck("Prometheus monitoring", monitoring.PodMonitorCRD, "PodMonitor CRD")
	addCheck("DNS resolution", network.CoreDNSRunning > 0, fmt.Sprintf("%d CoreDNS pod(s)", network.CoreDNSRunning))

	output.Table([]string{"CHECK", "STATUS", "DETAIL"}, cRows)

	if fails == 0 && warns == 0 {
		output.Banner("RESULT: COMPATIBLE", "Cluster can run a 3-AZ Kafka deployment")
	} else if fails == 0 {
		output.Banner("RESULT: PARTIAL", fmt.Sprintf("Compatible with %d warning(s)", warns))
	} else {
		output.Banner("RESULT: INCOMPATIBLE", fmt.Sprintf("%d check(s) failed", fails))
	}

	return nil
}

// Data structures and helper functions

type NodeInfo struct {
	Name     string
	Zone     string
	Roles    string
	CPU      int
	MemoryGi int
	Runtime  string
	Kubelet  string
	OS       string
}

type ZoneInfo struct {
	Name             string
	Nodes            int
	CPUAllocatable   int
	MemAllocatableGi int
}

type SCInfo struct {
	Name          string
	Provisioner   string
	BindingMode   string
	ReclaimPolicy string
	IsDefault     bool
}

type KafkaResources struct {
	KafkaClusters  int
	KafkaNodePools int
	KafkaTopics    int
	KafkaUsers     int
	PVCs           int
	BoundPVCs      int
	HelmRelease    string
}

type StrimziInfo struct {
	CRDsPresent   bool
	Running       bool
	Namespace     string
	Image         string
	ReadyReplicas int
	TotalReplicas int
}

type MonitoringInfo struct {
	PodMonitorCRD     bool
	PrometheusRuleCRD bool
	GrafanaDeployed   bool
	ReleaseLabel      string
}

type NetworkInfo struct {
	CNI            string
	CoreDNSRunning int
	PodCIDR        string
	ServiceCIDR    string
}

func execCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

func getContext() string {
	out, _ := execCmd("kubectl", "config", "current-context")
	if out == "" {
		return "unknown"
	}
	return out
}

func getServer() string {
	out, _ := execCmd("kubectl", "config", "view", "--minify", "-o", "jsonpath={.clusters[0].cluster.server}")
	if out == "" {
		return "unknown"
	}
	return out
}

func getProvider(ctx string) string {
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

func getK8sVersion() (string, int) {
	out, err := execCmd("kubectl", "version", "-o", "json")
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

func getHelmVersion() (string, int) {
	out, _ := execCmd("helm", "version", "--short")
	out = strings.TrimPrefix(out, "v")
	out = strings.Split(out, "+")[0]
	majorStr := strings.Split(out, ".")[0]
	major, _ := strconv.Atoi(majorStr)
	if out == "" {
		return "unknown", 0
	}
	return "v" + out, major
}

func parseNodes() ([]NodeInfo, error) {
	out, err := execCmd("kubectl", "get", "nodes", "-o", "json")
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
				Capacity struct {
					CPU    string `json:"cpu"`
					Memory string `json:"memory"`
				} `json:"capacity"`
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

func groupNodesByZone(nodes []NodeInfo) []ZoneInfo {
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

func getStorageClasses() []SCInfo {
	out, err := execCmd("kubectl", "get", "sc", "-o", "json")
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

func getExistingKafkaResources() KafkaResources {
	countLines := func(args ...string) int {
		out, _ := execCmd(args[0], args[1:]...)
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

	pvcOut, _ := execCmd("kubectl", "get", "pvc", "-n", "kafka", "--no-headers")
	if pvcOut != "" {
		res.PVCs = len(strings.Split(pvcOut, "\n"))
		res.BoundPVCs = strings.Count(pvcOut, "Bound")
	}

	helmOut, _ := execCmd("helm", "list", "-n", "kafka", "-o", "json")
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

func getStrimziStatus() StrimziInfo {
	info := StrimziInfo{}
	crdOut, _ := execCmd("kubectl", "get", "crd", "kafkas.kafka.strimzi.io")
	info.CRDsPresent = !strings.Contains(crdOut, "NotFound")

	depOut, _ := execCmd("kubectl", "get", "deployment", "-A", "-l", "app.kubernetes.io/name=strimzi-cluster-operator", "-o", "json")
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

func getMonitoringStatus() MonitoringInfo {
	info := MonitoringInfo{}
	if out, _ := execCmd("kubectl", "get", "crd", "podmonitors.monitoring.coreos.com"); !strings.Contains(out, "NotFound") && out != "" {
		info.PodMonitorCRD = true
	}
	if out, _ := execCmd("kubectl", "get", "crd", "prometheusrules.monitoring.coreos.com"); !strings.Contains(out, "NotFound") && out != "" {
		info.PrometheusRuleCRD = true
	}
	if out, _ := execCmd("kubectl", "get", "deployment", "-n", "monitoring", "-l", "app.kubernetes.io/name=grafana", "--no-headers"); out != "" {
		info.GrafanaDeployed = true
	}
	label, _ := execCmd("kubectl", "get", "podmonitors", "-A", "-o", "jsonpath={.items[0].metadata.labels.release}")
	if label != "" {
		info.ReleaseLabel = label
	} else {
		info.ReleaseLabel = "monitoring"
	}
	return info
}

func getNetworkStatus() NetworkInfo {
	info := NetworkInfo{CNI: "unknown"}
	if out, _ := execCmd("kubectl", "get", "pods", "-n", "kube-system", "-l", "k8s-app=calico-node", "--no-headers"); out != "" {
		info.CNI = "Calico"
	} else if out, _ := execCmd("kubectl", "get", "pods", "-n", "kube-system", "-l", "k8s-app=cilium", "--no-headers"); out != "" {
		info.CNI = "Cilium"
	} else if out, _ := execCmd("kubectl", "get", "pods", "-n", "kube-system", "-l", "app=kindnet", "--no-headers"); out != "" {
		info.CNI = "kindnet"
	} else if out, _ := execCmd("kubectl", "get", "ds", "-n", "kube-system", "kindnet", "--no-headers"); out != "" {
		info.CNI = "kindnet"
	} else if out, _ := execCmd("kubectl", "get", "ds", "-n", "kube-system", "-l", "app=flannel", "--no-headers"); out != "" {
		info.CNI = "Flannel"
	}

	dnsOut, _ := execCmd("kubectl", "get", "pods", "-n", "kube-system", "-l", "k8s-app=kube-dns", "--no-headers")
	info.CoreDNSRunning = strings.Count(dnsOut, "Running")

	info.PodCIDR, _ = execCmd("kubectl", "get", "nodes", "-o", "jsonpath={.items[0].spec.podCIDR}")
	if info.PodCIDR == "" {
		info.PodCIDR = "unknown"
	}

	svcOut, _ := execCmd("bash", "-c", "kubectl get pod -n kube-system -l component=kube-apiserver -o jsonpath='{.items[0].spec.containers[0].command}' 2>/dev/null | grep -oE 'service-cluster-ip-range=[^\",]+' | cut -d= -f2 | head -1")
	if svcOut == "" {
		svcOut = "unknown"
	}
	info.ServiceCIDR = svcOut
	return info
}

type spinnerModel struct {
	spinner  spinner.Model
	quitting bool
}

func (m spinnerModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m spinnerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m, tea.Quit
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	default:
		return m, nil
	}
}

func (m spinnerModel) View() string {
	if m.quitting {
		return ""
	}
	return fmt.Sprintf("\n  %s Introspecting cluster state...\n", lipgloss.NewStyle().Foreground(output.Cyan).Render(m.spinner.View()))
}

type ParsedReqs struct {
	BrokerCPU     int
	BrokerMem     int
	ControllerCPU int
	ControllerMem int
	OtherCPU      int
	OtherMem      int
}

func parseResourcesFromYAML(data []byte) ParsedReqs {
	var root map[string]interface{}
	yaml.Unmarshal(data, &root)

	reqs := ParsedReqs{}

	parseMem := func(s string) int {
		s = strings.TrimSuffix(s, "i")
		if strings.HasSuffix(s, "G") {
			v, _ := strconv.Atoi(strings.TrimSuffix(s, "G"))
			return v
		}
		if strings.HasSuffix(s, "M") {
			v, _ := strconv.Atoi(strings.TrimSuffix(s, "M"))
			return v / 1024
		}
		return 0
	}
	parseCPU := func(s string) int {
		if strings.HasSuffix(s, "m") {
			v, _ := strconv.Atoi(strings.TrimSuffix(s, "m"))
			return v
		}
		v, _ := strconv.Atoi(s)
		return v * 1000
	}

	extract := func(path ...string) (string, string) {
		cur := root
		for i, p := range path {
			val, ok := cur[p]
			if !ok {
				return "", ""
			}
			if i == len(path)-1 {
				m, _ := val.(map[string]interface{})
				if m == nil {
					return "", ""
				}
				cpu, _ := m["cpu"].(string)
				mem, _ := m["memory"].(string)
				if cpu == "" && mem == "" {
					if cpuInt, ok := m["cpu"].(int); ok {
						cpu = strconv.Itoa(cpuInt)
					}
					if memInt, ok := m["memory"].(int); ok {
						mem = strconv.Itoa(memInt)
					}
				}
				return cpu, mem
			}
			cur, _ = val.(map[string]interface{})
			if cur == nil {
				return "", ""
			}
		}
		return "", ""
	}

	if c, m := extract("kafka", "resources", "requests"); c != "" || m != "" {
		reqs.BrokerCPU = parseCPU(c)
		reqs.BrokerMem = parseMem(m)
	}
	if c, m := extract("zookeeper", "resources", "requests"); c != "" || m != "" {
		reqs.ControllerCPU = parseCPU(c)
		reqs.ControllerMem = parseMem(m)
	}
	
	// approximate other stuff (cruise control, entity operator, exporter)
	oCpu, oMem := 0, 0
	if c, m := extract("cruiseControl", "resources", "requests"); c != "" || m != "" {
		oCpu += parseCPU(c)
		oMem += parseMem(m)
	}
	if c, m := extract("kafkaExporter", "resources", "requests"); c != "" || m != "" {
		oCpu += parseCPU(c)
		oMem += parseMem(m)
	}
	if oCpu > 0 { reqs.OtherCPU = oCpu }
	if oMem > 0 { reqs.OtherMem = oMem }

	return reqs
}
