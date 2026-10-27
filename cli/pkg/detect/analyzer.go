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

	if reqs.BrokerCPU > 0 {
		b.BrokerCPU = reqs.BrokerCPU
	}
	if reqs.BrokerMem > 0 {
		b.BrokerMem = reqs.BrokerMem
	}
	if reqs.ControllerCPU > 0 {
		b.CtrlCPU = reqs.ControllerCPU
	}
	if reqs.ControllerMem > 0 {
		b.CtrlMem = reqs.ControllerMem
	}
	if reqs.OtherCPU > 0 {
		b.OtherCPU = reqs.OtherCPU
	}
	if reqs.OtherMem > 0 {
		b.OtherMem = reqs.OtherMem
	}

	b.NeedCPU = b.CtrlCPU + b.BrokerCPU*3 + b.OtherCPU
	b.NeedMem = b.CtrlMem + b.BrokerMem*3 + b.OtherMem

	for _, n := range report.Nodes {
		b.TotalCPU += n.CPU
		b.TotalMem += n.MemoryGi
	}

	b.Sufficient = b.TotalCPU >= b.NeedCPU && b.TotalMem >= b.NeedMem

	if b.TotalCPU >= 15000 && b.TotalMem >= 49 {
		b.RecommendedProfile = "production"
	} else if b.TotalCPU >= 8000 && b.TotalMem >= 24 {
		b.RecommendedProfile = "standard"
	} else if b.TotalCPU >= 3000 && b.TotalMem >= 8 {
		b.RecommendedProfile = "minimal"
	} else {
		b.RecommendedProfile = "insufficient"
	}

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
	// addWarn is a passing row that counts as a warning: every warning has a
	// row, so --fail-on-warning never exits 1 over something no report shows.
	addWarn := func(desc, detail string) {
		v.Warns++
		v.Checks = append(v.Checks, CheckResult{Description: desc, Status: true, Warn: true, Detail: detail})
	}

	addCheck("Kubernetes version ≥ 1.25", report.K8sMinor >= 25, report.K8sVersion)
	addCheck("Helm version ≥ 3.12", report.HelmMajor >= 3, report.HelmVersion)

	// Treat absence of Strimzi CRDs as a warning rather than a fatal failure since they are deployed automatically by Kates.
	if !report.Strimzi.CRDsPresent {
		addWarn("Strimzi CRDs installed", "not present (will be installed by Kates)")
	} else {
		addCheck("Strimzi CRDs installed", true, "present")
	}

	addCheck("≥ 3 availability zones", len(report.Zones) >= 3, fmt.Sprintf("%d zone(s)", len(report.Zones)))

	min1Node := true
	for _, z := range report.Zones {
		if z.Nodes < 1 {
			min1Node = false
		}
	}
	addCheck("≥ 1 node per zone", min1Node, fmt.Sprintf("%d nodes across %d zones", len(report.Nodes), len(report.Zones)))
	addCheck("StorageClass available", len(report.Storage) > 0, fmt.Sprintf("%d class(es)", len(report.Storage)))

	// Disk IOPS of the class the pools will use — the default one — and not
	// the best class on the cluster, which may be one nothing provisions
	// from. The number is an estimate from the class's name unless the live
	// bench measured it, and the row says which.
	const minIOPS = 1000
	if sc, isDefault := defaultStorageClass(report.Storage); sc != nil {
		basis := "estimated from the provisioner"
		if sc.IOPSMeasured {
			basis = "measured"
		}
		which := "default class"
		if !isDefault {
			which = "no default class; judging"
		}
		addCheck("Disk IOPS sufficient (≥ 1000)", sc.ProbedIOPS >= minIOPS,
			fmt.Sprintf("%s %s: %d IOPS %s", which, sc.Name, sc.ProbedIOPS, basis))
	}

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

	// Treat absence of Prometheus monitoring CRD as a warning rather than a fatal failure since they are deployed automatically by Kates.
	if !report.Monitoring.PodMonitorCRD {
		addWarn("Prometheus monitoring", "PodMonitor CRD not present (will be installed by Kates)")
	} else {
		addCheck("Prometheus monitoring", true, "PodMonitor CRD present")
	}

	addCheck("DNS resolution", report.Network.CoreDNSRunning > 0, fmt.Sprintf("%d CoreDNS pod(s)", report.Network.CoreDNSRunning))

	// Active AZ network latency check
	if len(report.Network.LatencyMatrix) > 0 {
		maxCrossAzAvg := 0.0
		hasFailure := false
		for _, r := range report.Network.LatencyMatrix {
			if r.SourceZone != r.TargetZone {
				if !r.Success {
					hasFailure = true
				}
				if r.AvgMs > maxCrossAzAvg {
					maxCrossAzAvg = r.AvgMs
				}
			}
		}

		if hasFailure {
			addWarn("AZ network latency (cross-zone)", "some zone probes failed")
		} else if maxCrossAzAvg == 0.0 {
			addCheck("AZ network latency (cross-zone)", true, "no cross-zone routes")
		} else if maxCrossAzAvg > 15.0 {
			addCheck("AZ network latency (cross-zone)", false, fmt.Sprintf("incompatible: max %.2fms (>15ms)", maxCrossAzAvg))
		} else if maxCrossAzAvg > 5.0 {
			addWarn("AZ network latency (cross-zone)", fmt.Sprintf("warning: max %.2fms (5-15ms)", maxCrossAzAvg))
		} else {
			addCheck("AZ network latency (cross-zone)", true, fmt.Sprintf("optimal: max %.2fms (<5ms)", maxCrossAzAvg))
		}
	}

	// Secret creation check
	if report.SecretAudit.NamespaceCreated {
		if report.SecretAudit.SecretCreated {
			addCheck("Kubernetes Secret capability", true, "verified (functional)")
		} else if report.SecretAudit.BlockedByPolicy {
			policyName := report.SecretAudit.PolicyName
			if policyName == "" {
				policyName = "unknown policy"
			}
			addCheck("Kubernetes Secret capability", false, fmt.Sprintf("blocked by admission policy: %s", policyName))
		} else {
			addCheck("Kubernetes Secret capability", false, fmt.Sprintf("failed: %s", report.SecretAudit.ErrorMsg))
		}
	} else if report.SecretAudit.ErrorMsg != "" {
		addCheck("Kubernetes Secret capability", false, fmt.Sprintf("namespace blocked: %s", report.SecretAudit.ErrorMsg))
	}

	// Strimzi Operator health diagnostics
	if report.Strimzi.Running {
		healthStatus := report.Strimzi.Health.Status
		if healthStatus == "Healthy" {
			addCheck("Strimzi Operator health", true, "healthy")
		} else if healthStatus == "Degraded" {
			addWarn("Strimzi Operator health", "warning: operator degraded")
		} else {
			addWarn("Strimzi Operator health", "warning: operator unhealthy")
		}
	}

	// Capacity budget audit
	if report.Strimzi.CapacityStatus != "" && report.Strimzi.CapacityStatus != "Sufficient" {
		addWarn("Strimzi Kafka capacity", report.Strimzi.CapacityStatus)
	} else if report.Strimzi.CapacityStatus == "Sufficient" {
		addCheck("Strimzi Kafka capacity", true, "sufficient resources allocated")
	}

	// Admission controller compatibility
	if report.Admission.Kyverno.Installed {
		enforced := 0
		for _, p := range report.Admission.Kyverno.KafkaRelevant {
			if strings.ToLower(p.Action) == "enforce" {
				enforced++
			}
		}
		kyvernoSafe := !report.Admission.Kyverno.Constraints.EmptyPodSelectorBlocked
		if !kyvernoSafe {
			// It's a warning, not a fail — the chart has Kyverno-safe selectors
			v.Warns++
		}
		addCheck("Kyverno NetworkPolicy safe", true, fmt.Sprintf("%d enforced policies, explicit selectors used", enforced))

		if report.Admission.Kyverno.Constraints.ResourceLimitsRequired {
			addWarn("Resource limits required", "Kyverno enforces — verify values.yaml limits")
		}
	}

	if n := report.ExistingKafka.KafkaClusters; n > 0 {
		addWarn("Existing Kafka clusters", fmt.Sprintf("%d Kafka CR(s) already on the cluster — kates deploy adds another", n))
	}
	if !report.Strimzi.Running && report.Strimzi.CRDsPresent {
		addWarn("Strimzi operator running", "CRDs present but no operator reconciles them — a Kafka CR would never become Ready")
	}

	// New pre-flight security audit check
	psaPass := true
	psaDetail := "none enforced"
	if report.Security.PSALabelEnforced != "" && report.Security.PSALabelEnforced != "none" {
		psaDetail = fmt.Sprintf("%s enforced (fully supported)", report.Security.PSALabelEnforced)
	}
	addCheck("Pod Security Standards compatible", psaPass, psaDetail)

	// New sizing advisor profile check
	sizingPass := report.Budget.RecommendedProfile != "insufficient"
	sizingDetail := fmt.Sprintf("profile: %s", report.Budget.RecommendedProfile)
	addCheck("Sizing recommendation available", sizingPass, sizingDetail)

	// Wildcard RBAC roles audit check
	rbacPass := !report.Security.HasExcessivePrivileges
	rbacDetail := "no wildcard rules"
	if !rbacPass {
		rbacDetail = "wildcard rules detected"
		v.Warns++
	}
	addCheck("Wildcard RBAC roles avoided", rbacPass, rbacDetail)

	// TLS Certificate validity audit check
	certPass := true
	certDetail := "all certificates valid"
	expiringSoon := len(expiringCerts(report.Security.ExpiringCerts))
	if expiringSoon > 0 {
		certPass = false
		certDetail = fmt.Sprintf("%d certificate(s) expiring in < 30 days", expiringSoon)
		v.Warns++
	}
	addCheck("TLS certificate validity", certPass, certDetail)

	// Ecosystem / CDC Checks
	if report.Ecosystem.KafkaConnect.Installed {

		// Connect JVM health check
		if report.Ecosystem.KafkaConnect.HeapXmx != "" && report.Ecosystem.KafkaConnect.MemLimitMi > 0 {
			heapMi := parseJVMHeapMiB(report.Ecosystem.KafkaConnect.HeapXmx)
			limitMi := report.Ecosystem.KafkaConnect.MemLimitMi
			ratio := float64(heapMi) / float64(limitMi) * 100
			if ratio > 75 {
				addCheck("Connect JVM heap ratio", false, fmt.Sprintf("%.0f%% of limit — risks OOMKill, reduce -Xmx", ratio))
			} else if ratio > 50 {
				addWarn("Connect JVM heap ratio", fmt.Sprintf("%.0f%% of limit — limited off-heap space", ratio))
			} else {
				addCheck("Connect JVM heap ratio", true, fmt.Sprintf("%.0f%% of limit — healthy", ratio))
			}
		}

		if !report.Ecosystem.KafkaConnect.TLSEnabled {
			addWarn("Connect TLS", "disabled — traffic is plaintext")
		}

		if !report.Ecosystem.KafkaConnect.PDBConfigured {
			addWarn("Connect PDB", "not configured — upgrades may cause downtime")
		}

		if !report.Ecosystem.SchemaRegistry.Installed {
			addWarn("Schema Registry for CDC", "warning: not found (Debezium Avro requires it)")
		} else {
			addCheck("Schema Registry for CDC", report.Ecosystem.SchemaRegistry.Available, "available")
		}

		if report.Ecosystem.Database.Installed {
			addCheck("Database CDC source", report.Ecosystem.Database.Accessible, "available in "+report.Ecosystem.Database.Namespace)
		} else {
			addWarn("Database CDC source", "warning: PostgreSQL not detected in cluster")
		}
	}

	v.Compatible = v.Fails == 0
	report.Verdict = v
}

// defaultStorageClass is the class marked default, or, when none is, the
// first one (isDefault false): a chart with no class named uses the former,
// and the latter is the closest thing to it the report has.
func defaultStorageClass(classes []SCInfo) (sc *SCInfo, isDefault bool) {
	for i := range classes {
		if classes[i].IsDefault {
			return &classes[i], true
		}
	}
	if len(classes) > 0 {
		return &classes[0], false
	}
	return nil, false
}
