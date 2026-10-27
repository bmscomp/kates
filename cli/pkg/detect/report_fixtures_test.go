package detect

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files under testdata")

// fixedTime stands in for time.Now in golden output.
var fixedTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// healthyReport is a 3-zone cluster that passes every check: every probe
// ran, the latency and bandwidth matrices are measured, Connect is healthy.
func healthyReport() *DetectReport {
	r := &DetectReport{
		Context: "prod-eu", Server: "https://api.prod-eu.example:6443",
		Provider: "eks", ProviderBasis: "server version v1.31.2-eks-1234",
		K8sVersion: "v1.31.2", K8sMinor: 31, HelmVersion: "v3.15.4", HelmMajor: 3,
		Storage: []SCInfo{
			{Name: "gp3", Provisioner: "ebs.csi.aws.com", BindingMode: "WaitForFirstConsumer", ReclaimPolicy: "Delete", IsDefault: true, AllowExpansion: true, ProbedIOPS: 3000, IOPSMeasured: true, ProbeLatencyMs: 0.42},
			{Name: "io2", Provisioner: "ebs.csi.aws.com", BindingMode: "WaitForFirstConsumer", ReclaimPolicy: "Retain", AllowExpansion: true},
		},
		StorageAudit: StorageAudit{PVCount: 6, PVBoundCount: 6, PVTotalCapacity: "600Gi", CSIDrivers: []string{"ebs.csi.aws.com"}},
		Strimzi: StrimziInfo{CRDsPresent: true, Running: true, Namespace: "strimzi-operator", Image: "quay.io/strimzi/operator:0.45.0",
			ReadyReplicas: 1, TotalReplicas: 1, Health: StrimziHealth{Status: "Healthy", PodsReady: true}, CapacityStatus: "Sufficient"},
		Monitoring: MonitoringInfo{PodMonitorCRD: true, PrometheusRuleCRD: true, GrafanaDeployed: true, ReleaseLabel: "kube-prometheus"},
		Network: NetworkInfo{CNI: "cilium", CoreDNSRunning: 2, ClusterDomain: "cluster.local", PodCIDR: "10.0.0.0/16", ServiceCIDR: "172.20.0.0/16",
			DNSResults: []DNSProbeResult{{QueryType: "Internal", QueriesRun: 20, SuccessCount: 20, SuccessRate: 100, AvgLatencyMs: 1.5, MaxLatencyMs: 3}}},
		Ecosystem: EcosystemInfo{
			KafkaConnect: KafkaConnectStatus{Installed: true, Name: "connect", Namespace: "kafka", ReadyReplicas: 2, TotalReplicas: 2,
				Image: "quay.io/strimzi/kafka:0.45.0-kafka-3.9.0", HeapXms: "1g", HeapXmx: "1g", MemLimitMi: 4096, TLSEnabled: true, PDBConfigured: true,
				Connectors: []ConnectorStatus{{Name: "pg-source", Class: "io.debezium.connector.postgresql.PostgresConnector", TasksMax: 1, Status: "Running"}}},
			SchemaRegistry: SchemaRegistryStatus{Installed: true, Available: true, Name: "apicurio", Namespace: "registry"},
			Database:       DatabaseCDCStatus{Installed: true, Accessible: true, Name: "pg", Namespace: "db", Port: 5432},
		},
		Admission: AdmissionInfo{Kyverno: KyvernoInfo{Installed: true, Namespace: "kyverno", Version: "1.12.0",
			ClusterPolicies: []KyvernoPolicyInfo{{Name: "require-labels"}, {Name: "disallow-latest-tag"}},
			KafkaRelevant:   []KyvernoPolicyInfo{{Name: "disallow-latest-tag", Action: "enforce", Category: "Best Practices", Rules: []string{"validate-image-tag"}}},
			Constraints:     PolicyConstraints{LatestTagBlocked: true}}},
		NetPolAudit: NetworkPolicyAudit{TotalCount: 1, HasDefaultDeny: true, Existing: []ExistingNetPol{
			{Name: "kafka-default-deny", PodSelector: "{}", PolicyTypes: []string{"Ingress", "Egress"}, ManagedBy: "kates"}}},
		Budget: BudgetReport{CtrlCPU: 1500, CtrlMem: 3, BrokerCPU: 3000, BrokerMem: 12, OtherCPU: 500, OtherMem: 1,
			NeedCPU: 11000, NeedMem: 40, TotalCPU: 48000, TotalMem: 192, Sufficient: true, RecommendedProfile: "production"},
		SecretAudit: SecretCreationAudit{NamespaceCreated: true, SecretCreated: true},
		Security:    SecurityAudit{PSALabelEnforced: "baseline", PermissionsOk: true},
	}
	for i, z := range []string{"eu-west-1a", "eu-west-1b", "eu-west-1c"} {
		r.Nodes = append(r.Nodes, NodeInfo{Name: "ip-10-0-" + string(rune('1'+i)) + "-10", Zone: z, Roles: "worker", CPU: 16000, MemoryGi: 64,
			Runtime: "containerd://1.7.22", Kubelet: "v1.31.2-eks-1234"})
		r.Zones = append(r.Zones, ZoneInfo{Name: z, Nodes: 1, CPUAllocatable: 16000, MemAllocatableGi: 64})
	}
	// Listed out of order, as the probe's goroutines finish.
	for _, src := range []string{"eu-west-1c", "eu-west-1a", "eu-west-1b"} {
		for _, dst := range []string{"eu-west-1b", "eu-west-1c", "eu-west-1a"} {
			avg := 0.05
			if src != dst {
				avg = 0.9
			}
			r.Network.LatencyMatrix = append(r.Network.LatencyMatrix, LatencyResult{SourceZone: src, TargetZone: dst, AvgMs: avg, Success: true})
			if src != dst {
				r.Network.BandwidthMatrix = append(r.Network.BandwidthMatrix, BandwidthResult{SourceZone: src, TargetZone: dst, BandwidthMbps: 4800, Success: true})
			}
		}
	}
	r.Verdict = Verdict{Compatible: true, Checks: []CheckResult{
		{Description: "Kubernetes version ≥ 1.25", Status: true, Detail: "v1.31.2"},
		{Description: "≥ 3 availability zones", Status: true, Detail: "3 zone(s)"},
		{Description: "StorageClass available", Status: true, Detail: "2 class(es)"},
		{Description: "AZ network latency (cross-zone)", Status: true, Detail: "optimal: max 0.90ms (<5ms)"},
	}}
	return r
}

// degradedReport is a single-zone read-only run that fails: no latency
// matrix, no StorageClass, a node named <script>, an expiring certificate,
// a Secret blocked by policy and a hand-made NetworkPolicy.
func degradedReport() *DetectReport {
	r := &DetectReport{
		Context: `kind-<script>alert("ctx")</script>`, Server: "https://127.0.0.1:6443",
		K8sVersion: "v1.24.0", K8sMinor: 24, HelmVersion: "v3.11.0", HelmMajor: 3,
		Nodes: []NodeInfo{{Name: "<script>alert(1)</script>", Zone: "zone-a", Roles: "control-plane", CPU: 2000, MemoryGi: 4}},
		Zones: []ZoneInfo{{Name: "zone-a", Nodes: 1, CPUAllocatable: 2000, MemAllocatableGi: 4}},
		Strimzi: StrimziInfo{CRDsPresent: true, Running: true, Namespace: "kafka", Image: "quay.io/strimzi/operator:0.38.0", TotalReplicas: 1,
			Health: StrimziHealth{Status: "Unhealthy", WarningLogs: []string{"WARN Reconciliation #12 failed: <timeout> & retry"}, MissingCRDs: []string{"kafkarebalances.kafka.strimzi.io"}}},
		ExistingKafka: KafkaResources{KafkaClusters: 1, KafkaNodePools: 2, PVCs: 3, BoundPVCs: 2, HelmRelease: "krafter",
			Health: KafkaClusterHealth{Name: "krafter", Version: "3.9.0", Replicas: 3, ReadyReplicas: 1,
				Conditions: []KafkaCondition{{Type: "Ready", Status: "False", Reason: "NotReady"}},
				Listeners:  []KafkaListener{{Name: "plain", Type: "internal", Port: 9092}}}},
		Network: NetworkInfo{CNI: "kindnet", ClusterDomain: "cluster.local"},
		Admission: AdmissionInfo{Gatekeeper: GatekeeperInfo{Installed: true, Namespace: "gatekeeper-system",
			Constraints: []GatekeeperConstraint{{Name: "require-team", Kind: "K8sRequiredLabels"}}}},
		NetPolAudit: NetworkPolicyAudit{TotalCount: 1, Existing: []ExistingNetPol{
			{Name: "legacy|allow", PodSelector: "app=kafka", PolicyTypes: []string{"Ingress"}, IngressRules: 1, ManagedBy: "manual"}}},
		Budget: BudgetReport{CtrlCPU: 1500, CtrlMem: 3, BrokerCPU: 3000, BrokerMem: 12, OtherCPU: 500, OtherMem: 1,
			NeedCPU: 11000, NeedMem: 40, TotalCPU: 2000, TotalMem: 4, RecommendedProfile: "insufficient"},
		SecretAudit: SecretCreationAudit{NamespaceCreated: true, BlockedByPolicy: true, PolicyName: "restrict-secrets"},
		Security: SecurityAudit{HasExcessivePrivileges: true,
			ExpiringCerts: []CertExpirationInfo{{SecretName: "krafter-cluster-ca-cert", Subject: "CN=cluster-ca", ExpiryDate: "2026-01-20", DaysLeft: 18}}},
	}
	r.Verdict = Verdict{Fails: 3, Warns: 2, Checks: []CheckResult{
		{Description: "Kubernetes version ≥ 1.25", Status: false, Detail: "v1.24.0"},
		{Description: "≥ 3 availability zones", Status: false, Detail: "1 zone(s)"},
		{Description: "StorageClass available", Status: false, Detail: "0 class(es)"},
		{Description: "node <b>bold</b>", Status: true, Detail: "a & b <i>"},
		{Description: "Existing Kafka clusters", Status: true, Warn: true, Detail: "1 Kafka CR(s) already on the cluster"},
	}}
	return r
}

// fixtureDoc is BuildReportDoc at the fixed time golden files are written
// at.
func fixtureDoc(r *DetectReport) *ReportDoc {
	doc := BuildReportDoc(r)
	doc.GeneratedAt = fixedTime
	return doc
}

// checkGolden compares got with testdata/name, or rewrites it under
// -update.
func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -run %s -update to write it)", err, t.Name())
	}
	if string(got) != string(want) {
		t.Errorf("%s differs from the golden file; rerun with -update and review the diff\n--- got ---\n%s", path, got)
	}
}
