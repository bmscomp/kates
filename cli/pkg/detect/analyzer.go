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
	b := &verdictBuilder{}
	b.checkPlatform(report)
	b.checkCapacity(report)
	b.check("RBAC permissions", a.hasKafkaNamespaceRBAC(), "kafka namespace")
	b.checkMonitoringAndDNS(report)
	b.checkAZLatency(report)
	b.checkSecretCapability(report)
	b.checkStrimziOperator(report)
	b.checkAdmission(report)
	b.checkExistingInstalls(report)
	b.checkSecurity(report)
	b.checkEcosystem(report)

	b.v.Compatible = b.v.Fails == 0
	report.Verdict = b.v
}

// verdictBuilder accumulates the verdict's rows; each check family below
// appends its rows in the order the report lists them.
type verdictBuilder struct {
	v Verdict
}

func (b *verdictBuilder) check(desc string, pass bool, detail string) {
	if !pass {
		b.v.Fails++
	}
	b.v.Checks = append(b.v.Checks, CheckResult{
		Description: desc,
		Status:      pass,
		Detail:      detail,
	})
}

// warn is a passing row that counts as a warning: every warning has a
// row, so --fail-on-warning never exits 1 over something no report shows.
func (b *verdictBuilder) warn(desc, detail string) {
	b.v.Warns++
	b.v.Checks = append(b.v.Checks, CheckResult{Description: desc, Status: true, Warn: true, Detail: detail})
}

// checkPlatform covers versions, Strimzi CRDs, zones, and storage.
func (b *verdictBuilder) checkPlatform(report *DetectReport) {
	b.check("Kubernetes version ≥ 1.25", report.K8sMinor >= 25, report.K8sVersion)
	b.check("Helm version ≥ 3.12", report.HelmMajor >= 3, report.HelmVersion)

	// Treat absence of Strimzi CRDs as a warning rather than a fatal failure since they are deployed automatically by Kates.
	if !report.Strimzi.CRDsPresent {
		b.warn("Strimzi CRDs installed", "not present (will be installed by Kates)")
	} else {
		b.check("Strimzi CRDs installed", true, "present")
	}

	b.check("≥ 3 availability zones", len(report.Zones) >= 3, fmt.Sprintf("%d zone(s)", len(report.Zones)))

	min1Node := true
	for _, z := range report.Zones {
		if z.Nodes < 1 {
			min1Node = false
		}
	}
	b.check("≥ 1 node per zone", min1Node, fmt.Sprintf("%d nodes across %d zones", len(report.Nodes), len(report.Zones)))
	b.check("StorageClass available", len(report.Storage) > 0, fmt.Sprintf("%d class(es)", len(report.Storage)))
	b.checkDiskIOPS(report)
}

// checkDiskIOPS judges the IOPS of the class the pools will use — the
// default one — and not the best class on the cluster, which may be one
// nothing provisions from. The number is an estimate from the class's name
// unless the live bench measured it, and the row says which.
func (b *verdictBuilder) checkDiskIOPS(report *DetectReport) {
	const minIOPS = 1000
	sc, isDefault := defaultStorageClass(report.Storage)
	if sc == nil {
		return
	}
	basis := "estimated from the provisioner"
	if sc.IOPSMeasured {
		basis = "measured"
	}
	which := "default class"
	if !isDefault {
		which = "no default class; judging"
	}
	b.check("Disk IOPS sufficient (≥ 1000)", sc.ProbedIOPS >= minIOPS,
		fmt.Sprintf("%s %s: %d IOPS %s", which, sc.Name, sc.ProbedIOPS, basis))
}

// checkCapacity covers the resource budget and the replication guarantees
// the zone count allows.
func (b *verdictBuilder) checkCapacity(report *DetectReport) {
	bud := report.Budget
	b.check("Controller resources fit", bud.TotalCPU >= bud.CtrlCPU && bud.TotalMem >= bud.CtrlMem, fmt.Sprintf("%dm needed", bud.CtrlCPU))
	b.check("Broker resources fit (all zones)", bud.TotalCPU >= bud.NeedCPU && bud.TotalMem >= bud.NeedMem, fmt.Sprintf("%dm total needed", bud.NeedCPU))

	b.check("Replication factor 3 achievable", len(report.Zones) >= 3, fmt.Sprintf("%d zones", len(report.Zones)))
	b.check("min.insync.replicas=2 safe", len(report.Zones) >= 3, "can lose 1 zone")
}

// hasKafkaNamespaceRBAC asks the API server whether the caller may create
// every kind of object the chart installs in the kafka namespace.
func (a *Analyzer) hasKafkaNamespaceRBAC() bool {
	for _, res := range []string{"deployments", "statefulsets", "configmaps", "secrets", "services", "persistentvolumeclaims"} {
		if check, _ := a.exec.Exec("kubectl", "auth", "can-i", "create", res, "-n", "kafka"); !strings.Contains(check, "yes") {
			return false
		}
	}
	return true
}

func (b *verdictBuilder) checkMonitoringAndDNS(report *DetectReport) {
	// Treat absence of Prometheus monitoring CRD as a warning rather than a fatal failure since they are deployed automatically by Kates.
	if !report.Monitoring.PodMonitorCRD {
		b.warn("Prometheus monitoring", "PodMonitor CRD not present (will be installed by Kates)")
	} else {
		b.check("Prometheus monitoring", true, "PodMonitor CRD present")
	}

	b.check("DNS resolution", report.Network.CoreDNSRunning > 0, fmt.Sprintf("%d CoreDNS pod(s)", report.Network.CoreDNSRunning))
}

// checkAZLatency judges the worst measured cross-zone average.
func (b *verdictBuilder) checkAZLatency(report *DetectReport) {
	if len(report.Network.LatencyMatrix) == 0 {
		return
	}
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

	const desc = "AZ network latency (cross-zone)"
	switch {
	case hasFailure:
		b.warn(desc, "some zone probes failed")
	case maxCrossAzAvg == 0.0:
		b.check(desc, true, "no cross-zone routes")
	case maxCrossAzAvg > 15.0:
		b.check(desc, false, fmt.Sprintf("incompatible: max %.2fms (>15ms)", maxCrossAzAvg))
	case maxCrossAzAvg > 5.0:
		b.warn(desc, fmt.Sprintf("warning: max %.2fms (5-15ms)", maxCrossAzAvg))
	default:
		b.check(desc, true, fmt.Sprintf("optimal: max %.2fms (<5ms)", maxCrossAzAvg))
	}
}

func (b *verdictBuilder) checkSecretCapability(report *DetectReport) {
	const desc = "Kubernetes Secret capability"
	audit := report.SecretAudit
	switch {
	case audit.NamespaceCreated && audit.SecretCreated:
		b.check(desc, true, "verified (functional)")
	case audit.NamespaceCreated && audit.BlockedByPolicy:
		policyName := audit.PolicyName
		if policyName == "" {
			policyName = "unknown policy"
		}
		b.check(desc, false, fmt.Sprintf("blocked by admission policy: %s", policyName))
	case audit.NamespaceCreated:
		b.check(desc, false, fmt.Sprintf("failed: %s", audit.ErrorMsg))
	case audit.ErrorMsg != "":
		b.check(desc, false, fmt.Sprintf("namespace blocked: %s", audit.ErrorMsg))
	}
}

// checkStrimziOperator covers the operator's health and the capacity
// status it reports.
func (b *verdictBuilder) checkStrimziOperator(report *DetectReport) {
	if report.Strimzi.Running {
		switch report.Strimzi.Health.Status {
		case "Healthy":
			b.check("Strimzi Operator health", true, "healthy")
		case "Degraded":
			b.warn("Strimzi Operator health", "warning: operator degraded")
		default:
			b.warn("Strimzi Operator health", "warning: operator unhealthy")
		}
	}

	switch report.Strimzi.CapacityStatus {
	case "":
	case "Sufficient":
		b.check("Strimzi Kafka capacity", true, "sufficient resources allocated")
	default:
		b.warn("Strimzi Kafka capacity", report.Strimzi.CapacityStatus)
	}
}

// checkAdmission covers admission-controller compatibility.
func (b *verdictBuilder) checkAdmission(report *DetectReport) {
	kyverno := report.Admission.Kyverno
	if !kyverno.Installed {
		return
	}
	enforced := 0
	for _, p := range kyverno.KafkaRelevant {
		if strings.ToLower(p.Action) == "enforce" {
			enforced++
		}
	}
	if kyverno.Constraints.EmptyPodSelectorBlocked {
		// It's a warning, not a fail — the chart has Kyverno-safe selectors
		b.v.Warns++
	}
	b.check("Kyverno NetworkPolicy safe", true, fmt.Sprintf("%d enforced policies, explicit selectors used", enforced))

	if kyverno.Constraints.ResourceLimitsRequired {
		b.warn("Resource limits required", "Kyverno enforces — verify values.yaml limits")
	}
}

// checkExistingInstalls warns about Kafka already on the cluster and about
// Strimzi CRDs nothing reconciles.
func (b *verdictBuilder) checkExistingInstalls(report *DetectReport) {
	if n := report.ExistingKafka.KafkaClusters; n > 0 {
		b.warn("Existing Kafka clusters", fmt.Sprintf("%d Kafka CR(s) already on the cluster — kates deploy adds another", n))
	}
	if !report.Strimzi.Running && report.Strimzi.CRDsPresent {
		b.warn("Strimzi operator running", "CRDs present but no operator reconciles them — a Kafka CR would never become Ready")
	}
}

// checkSecurity covers pod security, sizing, wildcard RBAC, and TLS
// certificate expiry.
func (b *verdictBuilder) checkSecurity(report *DetectReport) {
	psaDetail := "none enforced"
	if report.Security.PSALabelEnforced != "" && report.Security.PSALabelEnforced != "none" {
		psaDetail = fmt.Sprintf("%s enforced (fully supported)", report.Security.PSALabelEnforced)
	}
	b.check("Pod Security Standards compatible", true, psaDetail)

	b.check("Sizing recommendation available", report.Budget.RecommendedProfile != "insufficient",
		fmt.Sprintf("profile: %s", report.Budget.RecommendedProfile))

	rbacPass := !report.Security.HasExcessivePrivileges
	rbacDetail := "no wildcard rules"
	if !rbacPass {
		rbacDetail = "wildcard rules detected"
		b.v.Warns++
	}
	b.check("Wildcard RBAC roles avoided", rbacPass, rbacDetail)

	certPass := true
	certDetail := "all certificates valid"
	if expiringSoon := len(expiringCerts(report.Security.ExpiringCerts)); expiringSoon > 0 {
		certPass = false
		certDetail = fmt.Sprintf("%d certificate(s) expiring in < 30 days", expiringSoon)
		b.v.Warns++
	}
	b.check("TLS certificate validity", certPass, certDetail)
}

// checkEcosystem covers Kafka Connect and the CDC pieces it depends on.
func (b *verdictBuilder) checkEcosystem(report *DetectReport) {
	eco := report.Ecosystem
	if !eco.KafkaConnect.Installed {
		return
	}
	b.checkConnectHeap(eco.KafkaConnect)

	if !eco.KafkaConnect.TLSEnabled {
		b.warn("Connect TLS", "disabled — traffic is plaintext")
	}
	if !eco.KafkaConnect.PDBConfigured {
		b.warn("Connect PDB", "not configured — upgrades may cause downtime")
	}

	if !eco.SchemaRegistry.Installed {
		b.warn("Schema Registry for CDC", "warning: not found (Debezium Avro requires it)")
	} else {
		b.check("Schema Registry for CDC", eco.SchemaRegistry.Available, "available")
	}

	if eco.Database.Installed {
		b.check("Database CDC source", eco.Database.Accessible, "available in "+eco.Database.Namespace)
	} else {
		b.warn("Database CDC source", "warning: PostgreSQL not detected in cluster")
	}
}

// checkConnectHeap judges the Connect JVM's -Xmx against its memory limit.
func (b *verdictBuilder) checkConnectHeap(kc KafkaConnectStatus) {
	if kc.HeapXmx == "" || kc.MemLimitMi <= 0 {
		return
	}
	ratio := float64(parseJVMHeapMiB(kc.HeapXmx)) / float64(kc.MemLimitMi) * 100
	switch {
	case ratio > 75:
		b.check("Connect JVM heap ratio", false, fmt.Sprintf("%.0f%% of limit — risks OOMKill, reduce -Xmx", ratio))
	case ratio > 50:
		b.warn("Connect JVM heap ratio", fmt.Sprintf("%.0f%% of limit — limited off-heap space", ratio))
	default:
		b.check("Connect JVM heap ratio", true, fmt.Sprintf("%.0f%% of limit — healthy", ratio))
	}
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
