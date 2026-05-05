package detect

import (
	"fmt"
	"strconv"

	"github.com/klster/kates-cli/output"
)

func RenderTUI(report *DetectReport) {
	output.Header("Cluster Identity")
	output.KeyValue("Context:", report.Context)
	output.KeyValue("Server:", report.Server)
	output.KeyValue("Provider:", report.Provider)
	output.KeyValue("Kubernetes:", report.K8sVersion)
	output.KeyValue("Helm:", report.HelmVersion)

	output.Header("Node Details")
	if len(report.Nodes) > 0 {
		headers := []string{"NAME", "ZONE", "ROLES", "CPU", "MEMORY", "RUNTIME", "KUBELET"}
		var rows [][]string
		for _, n := range report.Nodes {
			rows = append(rows, []string{
				n.Name, n.Zone, n.Roles, fmt.Sprintf("%dm", n.CPU), fmt.Sprintf("%dGi", n.MemoryGi), n.Runtime, n.Kubelet,
			})
		}
		output.Table(headers, rows)
		output.Success(fmt.Sprintf("Nodes: %d total", len(report.Nodes)))
	} else {
		output.Warn("No nodes found")
	}

	output.Header("Per-Zone Capacity")
	if len(report.Zones) > 0 {
		headers := []string{"ZONE", "NODES", "CPU", "MEMORY"}
		var rows [][]string
		for _, z := range report.Zones {
			rows = append(rows, []string{
				z.Name, strconv.Itoa(z.Nodes), fmt.Sprintf("%dm", z.CPUAllocatable), fmt.Sprintf("%dGi", z.MemAllocatableGi),
			})
		}
		output.Table(headers, rows)
		output.Success(fmt.Sprintf("Zones: %d", len(report.Zones)))
	} else {
		output.Warn("No zone labels found on nodes")
	}

	output.Header("Resource Budget")
	bHeaders := []string{"COMPONENT", "PODS", "CPU", "MEMORY"}
	bRows := [][]string{
		{"Controllers", "3", fmt.Sprintf("%dm", report.Budget.CtrlCPU), fmt.Sprintf("%dGi", report.Budget.CtrlMem)},
		{"Brokers (az1)", "3", fmt.Sprintf("%dm", report.Budget.BrokerCPU), fmt.Sprintf("%dGi", report.Budget.BrokerMem)},
		{"Brokers (az2)", "3", fmt.Sprintf("%dm", report.Budget.BrokerCPU), fmt.Sprintf("%dGi", report.Budget.BrokerMem)},
		{"Brokers (az3)", "3", fmt.Sprintf("%dm", report.Budget.BrokerCPU), fmt.Sprintf("%dGi", report.Budget.BrokerMem)},
		{"Operators + Exporter", "3", fmt.Sprintf("%dm", report.Budget.OtherCPU), fmt.Sprintf("%dGi", report.Budget.OtherMem)},
		{"TOTAL REQUIRED", "15", fmt.Sprintf("%dm", report.Budget.NeedCPU), fmt.Sprintf("%dGi", report.Budget.NeedMem)},
		{"CLUSTER AVAILABLE", strconv.Itoa(len(report.Nodes)), fmt.Sprintf("%dm", report.Budget.TotalCPU), fmt.Sprintf("%dGi", report.Budget.TotalMem)},
	}
	output.Table(bHeaders, bRows)

	if report.Budget.Sufficient {
		output.Success(fmt.Sprintf("Resources sufficient (%dm CPU / %dGi available)", report.Budget.TotalCPU, report.Budget.TotalMem))
	} else {
		output.Error(fmt.Sprintf("Insufficient resources (need %dm CPU, %dGi memory)", report.Budget.NeedCPU, report.Budget.NeedMem))
	}

	output.Header("Storage Compatibility")
	if len(report.Storage) > 0 {
		sHeaders := []string{"NAME", "PROVISIONER", "BINDING", "RECLAIM", "DEFAULT"}
		var sRows [][]string
		for _, sc := range report.Storage {
			def := "✗"
			if sc.IsDefault {
				def = "PASS"
			}
			sRows = append(sRows, []string{
				sc.Name, sc.Provisioner, sc.BindingMode, sc.ReclaimPolicy, def,
			})
		}
		output.Table(sHeaders, sRows)
		output.Success(fmt.Sprintf("StorageClasses: %d available", len(report.Storage)))
	} else {
		output.Error("No StorageClasses found")
	}

	output.Header("Existing Kafka Resources")
	output.KeyValue("Kafka clusters:", strconv.Itoa(report.ExistingKafka.KafkaClusters))
	output.KeyValue("KafkaNodePools:", strconv.Itoa(report.ExistingKafka.KafkaNodePools))
	output.KeyValue("KafkaTopics:", strconv.Itoa(report.ExistingKafka.KafkaTopics))
	output.KeyValue("KafkaUsers:", strconv.Itoa(report.ExistingKafka.KafkaUsers))
	output.KeyValue("PVCs:", fmt.Sprintf("%d (%d bound)", report.ExistingKafka.PVCs, report.ExistingKafka.BoundPVCs))
	output.KeyValue("Helm release:", report.ExistingKafka.HelmRelease)

	if report.ExistingKafka.KafkaClusters > 0 {
		output.Warn("Existing Kafka deployment detected — upgrade mode recommended")
	} else {
		output.Success("No existing Kafka deployment — clean install")
	}

	output.Header("Strimzi Operator")
	if report.Strimzi.Running {
		output.KeyValue("Namespace:", report.Strimzi.Namespace)
		output.KeyValue("Image:", report.Strimzi.Image)
		output.KeyValue("Replicas:", fmt.Sprintf("%d/%d ready", report.Strimzi.ReadyReplicas, report.Strimzi.TotalReplicas))
		output.Success("Strimzi operator: running")
	} else {
		if report.Strimzi.CRDsPresent {
			output.Warn("Strimzi CRDs present but operator not running")
		} else {
			output.Warn("Strimzi not installed — chart will install operator subchart")
		}
	}

	output.Header("Monitoring Stack")
	if report.Monitoring.PodMonitorCRD {
		output.Success("PodMonitor CRD: present")
	} else {
		output.Warn("PodMonitor CRD: not found")
	}
	if report.Monitoring.PrometheusRuleCRD {
		output.Success("PrometheusRule CRD: present")
	} else {
		output.Warn("PrometheusRule CRD: not found")
	}
	if report.Monitoring.GrafanaDeployed {
		output.Success("Grafana: deployed in monitoring")
	} else {
		output.Warn("Grafana: not found")
	}
	output.Success(fmt.Sprintf("Release label: %s", report.Monitoring.ReleaseLabel))

	output.Header("Network & Connectivity")
	output.Success(fmt.Sprintf("CNI: %s", report.Network.CNI))
	if report.Network.CoreDNSRunning > 0 {
		output.Success(fmt.Sprintf("CoreDNS: %d replica(s) running", report.Network.CoreDNSRunning))
	} else {
		output.Warn("CoreDNS: not detected")
	}
	output.Success(fmt.Sprintf("Pod CIDR: %s", report.Network.PodCIDR))
	output.Success(fmt.Sprintf("Service CIDR: %s", report.Network.ServiceCIDR))

	output.Header("3-AZ Kafka Compatibility Verdict")
	var cRows [][]string
	for _, c := range report.Verdict.Checks {
		status := "PASS"
		if !c.Status {
			status = "FAIL"
		}
		cRows = append(cRows, []string{c.Description, status, c.Detail})
	}
	output.Table([]string{"CHECK", "STATUS", "DETAIL"}, cRows)

	if report.Verdict.Fails == 0 && report.Verdict.Warns == 0 {
		output.Banner("RESULT: COMPATIBLE", "Cluster can run a 3-AZ Kafka deployment")
	} else if report.Verdict.Fails == 0 {
		output.Banner("RESULT: PARTIAL", fmt.Sprintf("Compatible with %d warning(s)", report.Verdict.Warns))
	} else {
		output.Banner("RESULT: INCOMPATIBLE", fmt.Sprintf("%d check(s) failed", report.Verdict.Fails))
	}
}
