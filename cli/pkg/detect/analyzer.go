package detect

import (
	"fmt"
	"strings"
)

type Analyzer struct {
	exec CommandExecutor
}

func NewAnalyzer(exec CommandExecutor) *Analyzer {
	return &Analyzer{exec: exec}
}

// Analyze processes the raw report and populates Budget and Verdict
func (a *Analyzer) Analyze(report *DetectReport, reqs ParsedReqs) {
	a.calculateBudget(report, reqs)
	a.calculateVerdict(report)
}

func (a *Analyzer) calculateBudget(report *DetectReport, reqs ParsedReqs) {
	b := BudgetReport{
		CtrlCPU:   1500,
		CtrlMem:   3,
		BrokerCPU: 3000,
		BrokerMem: 12,
		OtherCPU:  500,
		OtherMem:  1,
	}

	if reqs.BrokerCPU > 0 { b.BrokerCPU = reqs.BrokerCPU }
	if reqs.BrokerMem > 0 { b.BrokerMem = reqs.BrokerMem }
	if reqs.ControllerCPU > 0 { b.CtrlCPU = reqs.ControllerCPU }
	if reqs.ControllerMem > 0 { b.CtrlMem = reqs.ControllerMem }
	if reqs.OtherCPU > 0 { b.OtherCPU = reqs.OtherCPU }
	if reqs.OtherMem > 0 { b.OtherMem = reqs.OtherMem }

	b.NeedCPU = b.CtrlCPU + b.BrokerCPU*3 + b.OtherCPU
	b.NeedMem = b.CtrlMem + b.BrokerMem*3 + b.OtherMem

	for _, n := range report.Nodes {
		b.TotalCPU += n.CPU
		b.TotalMem += n.MemoryGi
	}

	b.Sufficient = b.TotalCPU >= b.NeedCPU && b.TotalMem >= b.NeedMem
	report.Budget = b
}

func (a *Analyzer) calculateVerdict(report *DetectReport) {
	v := Verdict{}

	addCheck := func(desc string, pass bool, detail string) {
		if !pass {
			v.Fails++
		}
		v.Checks = append(v.Checks, CheckResult{
			Description: desc,
			Status:      pass,
			Detail:      detail,
		})
	}

	addCheck("Kubernetes version ≥ 1.25", report.K8sMinor >= 25, report.K8sVersion)
	addCheck("Helm version ≥ 3.12", report.HelmMajor >= 3, report.HelmVersion)
	addCheck("Strimzi CRDs installed", report.Strimzi.CRDsPresent, "CRDs presence")
	addCheck("≥ 3 availability zones", len(report.Zones) >= 3, fmt.Sprintf("%d zone(s)", len(report.Zones)))
	
	min1Node := true
	for _, z := range report.Zones {
		if z.Nodes < 1 {
			min1Node = false
		}
	}
	addCheck("≥ 1 node per zone", min1Node, fmt.Sprintf("%d nodes across %d zones", len(report.Nodes), len(report.Zones)))
	addCheck("StorageClass available", len(report.Storage) > 0, fmt.Sprintf("%d class(es)", len(report.Storage)))
	
	addCheck("Controller resources fit", report.Budget.TotalCPU >= report.Budget.CtrlCPU && report.Budget.TotalMem >= report.Budget.CtrlMem, fmt.Sprintf("%dm needed", report.Budget.CtrlCPU))
	addCheck("Broker resources fit (all zones)", report.Budget.TotalCPU >= report.Budget.NeedCPU && report.Budget.TotalMem >= report.Budget.NeedMem, fmt.Sprintf("%dm total needed", report.Budget.NeedCPU))
	
	addCheck("Replication factor 3 achievable", len(report.Zones) >= 3, fmt.Sprintf("%d zones", len(report.Zones)))
	addCheck("min.insync.replicas=2 safe", len(report.Zones) >= 3, "can lose 1 zone")

	hasRbac := true
	for _, res := range []string{"deployments", "statefulsets", "configmaps", "secrets", "services", "persistentvolumeclaims"} {
		if check, _ := a.exec.Exec("kubectl", "auth", "can-i", "create", res, "-n", "kafka"); !strings.Contains(check, "yes") {
			hasRbac = false
			break
		}
	}
	addCheck("RBAC permissions", hasRbac, "kafka namespace")
	addCheck("Prometheus monitoring", report.Monitoring.PodMonitorCRD, "PodMonitor CRD")
	addCheck("DNS resolution", report.Network.CoreDNSRunning > 0, fmt.Sprintf("%d CoreDNS pod(s)", report.Network.CoreDNSRunning))

	if report.ExistingKafka.KafkaClusters > 0 {
		v.Warns++
	}
	if !report.Strimzi.Running && report.Strimzi.CRDsPresent {
		v.Warns++
	}

	v.Compatible = v.Fails == 0
	report.Verdict = v
}
