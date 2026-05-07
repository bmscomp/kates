package detect

import (
	"bytes"
	"strings"
	"testing"
)

func buildTestReport(zones int, scNames []string) *DetectReport {
	r := &DetectReport{
		Context:    "test-context",
		Provider:   "kind",
		K8sVersion: "1.31",
		Strimzi:    StrimziInfo{Running: true, CRDsPresent: true, Namespace: "kafka"},
		Monitoring: MonitoringInfo{PodMonitorCRD: true, PrometheusRuleCRD: true, GrafanaDeployed: true, ReleaseLabel: "monitoring"},
		Network:    NetworkInfo{CNI: "kindnet", ClusterDomain: "cluster.local"},
	}
	zoneNames := []string{"alpha", "gamma", "sigma", "delta"}
	for i := 0; i < zones && i < len(zoneNames); i++ {
		r.Zones = append(r.Zones, ZoneInfo{Name: zoneNames[i], Nodes: 1, CPUAllocatable: 18000, MemAllocatableGi: 39})
		r.Nodes = append(r.Nodes, NodeInfo{CPU: 18000, MemoryGi: 39})
	}
	for _, name := range scNames {
		isDefault := name == "standard"
		r.Storage = append(r.Storage, SCInfo{Name: name, IsDefault: isDefault, Provisioner: "rancher.io/local-path"})
	}
	return r
}

func TestGenerateValues_3AZ(t *testing.T) {
	r := buildTestReport(3, []string{"local-storage-alpha", "local-storage-gamma", "local-storage-sigma", "standard"})
	gen := NewValuesGenerator(r, "krafter")
	vals := gen.Generate()

	if len(vals.BrokerPools) != 3 {
		t.Fatalf("expected 3 broker pools, got %d", len(vals.BrokerPools))
	}
	// Check zone-named pools
	if vals.BrokerPools[0].Name != "brokers-alpha" {
		t.Errorf("expected brokers-alpha, got %s", vals.BrokerPools[0].Name)
	}
	if vals.BrokerPools[1].Name != "brokers-gamma" {
		t.Errorf("expected brokers-gamma, got %s", vals.BrokerPools[1].Name)
	}
	if vals.BrokerPools[2].Name != "brokers-sigma" {
		t.Errorf("expected brokers-sigma, got %s", vals.BrokerPools[2].Name)
	}
	// Check per-zone SC matching
	if vals.BrokerPools[0].StorageClass != "local-storage-alpha" {
		t.Errorf("expected local-storage-alpha for alpha zone, got %s", vals.BrokerPools[0].StorageClass)
	}
	if vals.BrokerPools[1].StorageClass != "local-storage-gamma" {
		t.Errorf("expected local-storage-gamma for gamma zone, got %s", vals.BrokerPools[1].StorageClass)
	}
}

func TestGenerateValues_2AZ(t *testing.T) {
	r := buildTestReport(2, []string{"standard"})
	gen := NewValuesGenerator(r, "krafter")
	vals := gen.Generate()

	if len(vals.BrokerPools) != 3 {
		t.Fatalf("expected 3 broker pools (2 pinned + 1 float), got %d", len(vals.BrokerPools))
	}
	if vals.BrokerPools[2].Name != "brokers-float" {
		t.Errorf("expected floating pool named brokers-float, got %s", vals.BrokerPools[2].Name)
	}
	if vals.BrokerPools[2].Zone != "" {
		t.Errorf("expected empty zone for floating pool, got %s", vals.BrokerPools[2].Zone)
	}
}

func TestGenerateValues_NoZones(t *testing.T) {
	r := buildTestReport(0, []string{"standard"})
	// Add nodes without zones
	r.Nodes = append(r.Nodes, NodeInfo{CPU: 8000, MemoryGi: 16})
	gen := NewValuesGenerator(r, "krafter")
	vals := gen.Generate()

	if len(vals.BrokerPools) != 1 {
		t.Fatalf("expected 1 broker pool (no zones), got %d", len(vals.BrokerPools))
	}
	if vals.BrokerPools[0].Name != "brokers" {
		t.Errorf("expected pool named 'brokers', got %s", vals.BrokerPools[0].Name)
	}
	if vals.BrokerDefaults.TopologyTSC.Enabled {
		t.Error("expected topology constraints disabled with no zones")
	}
}

func TestGenerateValues_StrimziDeployed(t *testing.T) {
	r := buildTestReport(3, []string{"standard"})
	r.Strimzi.Running = true
	gen := NewValuesGenerator(r, "krafter")
	vals := gen.Generate()

	if vals.StrimziOp.Enabled {
		t.Error("expected strimziOperator.enabled=false when already running")
	}
	if vals.CRDUpgrade.Enabled {
		t.Error("expected crdUpgrade.enabled=false when already running")
	}
}

func TestGenerateValues_StrimziNotInstalled(t *testing.T) {
	r := buildTestReport(3, []string{"standard"})
	r.Strimzi.Running = false
	r.Strimzi.CRDsPresent = false
	gen := NewValuesGenerator(r, "krafter")
	vals := gen.Generate()

	if !vals.StrimziOp.Enabled {
		t.Error("expected strimziOperator.enabled=true when not installed")
	}
	if !vals.CRDUpgrade.Enabled {
		t.Error("expected crdUpgrade.enabled=true when no CRDs")
	}
}

func TestGenerateValues_KyvernoSafe(t *testing.T) {
	r := buildTestReport(3, []string{"standard"})
	r.Admission.Kyverno.Installed = true
	r.Admission.Kyverno.Constraints.EmptyPodSelectorBlocked = true
	gen := NewValuesGenerator(r, "krafter")
	vals := gen.Generate()

	if !vals.NetPolicies.Enabled {
		t.Error("expected networkPolicies enabled")
	}
	if vals.NetPolicies.DefaultSelector == nil {
		t.Fatal("expected explicit defaultDenySelector")
	}
	if vals.NetPolicies.DefaultSelector["app.kubernetes.io/part-of"] != "strimzi-krafter" {
		t.Errorf("expected strimzi-krafter selector, got %v", vals.NetPolicies.DefaultSelector)
	}
}

func TestGenerateValues_EKS(t *testing.T) {
	r := buildTestReport(3, []string{"gp3"})
	r.Provider = "eks"
	gen := NewValuesGenerator(r, "krafter")
	vals := gen.Generate()

	// Should have external listener with NLB annotation
	found := false
	for _, l := range vals.Kafka.Listeners {
		if l.Name == "external" && l.Type == "loadbalancer" {
			found = true
			if l.Configuration == nil || l.Configuration.Bootstrap.Annotations["service.beta.kubernetes.io/aws-load-balancer-type"] != "nlb" {
				t.Error("expected NLB annotation for EKS")
			}
		}
	}
	if !found {
		t.Error("expected external loadbalancer listener for EKS")
	}
}

func TestMatchStorageClass_ZoneSpecific(t *testing.T) {
	r := buildTestReport(3, []string{"local-storage-alpha", "local-storage-gamma", "standard"})
	gen := NewValuesGenerator(r, "krafter")

	sc := gen.matchStorageClass("alpha")
	if sc != "local-storage-alpha" {
		t.Errorf("expected local-storage-alpha, got %s", sc)
	}

	sc = gen.matchStorageClass("gamma")
	if sc != "local-storage-gamma" {
		t.Errorf("expected local-storage-gamma, got %s", sc)
	}

	// Zone with no matching SC should fall back to default
	sc = gen.matchStorageClass("sigma")
	if sc != "standard" {
		t.Errorf("expected standard (default) for unmatched zone, got %s", sc)
	}
}

func TestMatchStorageClass_Default(t *testing.T) {
	r := buildTestReport(3, []string{"standard"})
	gen := NewValuesGenerator(r, "krafter")

	sc := gen.matchStorageClass("alpha")
	if sc != "standard" {
		t.Errorf("expected standard (default), got %s", sc)
	}
}

func TestSizingProfile_Production(t *testing.T) {
	r := buildTestReport(3, []string{"standard"})
	gen := NewValuesGenerator(r, "krafter")

	if gen.Cap.Profile != "production" {
		t.Errorf("expected production profile (54 CPU), got %s", gen.Cap.Profile)
	}
}

func TestSizingProfile_Minimal(t *testing.T) {
	r := &DetectReport{
		Nodes:   []NodeInfo{{CPU: 500, MemoryGi: 1}},
		Storage: []SCInfo{{Name: "standard", IsDefault: true}},
		Network: NetworkInfo{CNI: "kindnet", ClusterDomain: "cluster.local"},
	}
	gen := NewValuesGenerator(r, "krafter")

	if gen.Cap.Profile != "minimal" {
		t.Errorf("expected minimal profile (2 CPU), got %s", gen.Cap.Profile)
	}
}

func TestRenderValues_WritesYAML(t *testing.T) {
	r := buildTestReport(3, []string{"local-storage-alpha", "local-storage-gamma", "local-storage-sigma", "standard"})
	var buf bytes.Buffer
	err := RenderValues(r, "krafter", &buf)
	if err != nil {
		t.Fatalf("RenderValues failed: %v", err)
	}

	content := buf.String()
	// Check comment header
	if !strings.Contains(content, "Auto-generated by kates detect") {
		t.Error("expected header comment")
	}
	// Check broker pool names
	if !strings.Contains(content, "brokers-alpha") {
		t.Error("expected brokers-alpha in output")
	}
	if !strings.Contains(content, "local-storage-alpha") {
		t.Error("expected local-storage-alpha in output")
	}
	// Check YAML is parseable
	if !strings.Contains(content, "clusterName: krafter") {
		t.Error("expected clusterName in YAML")
	}
	// Check global.clusterDomain is present
	if !strings.Contains(content, "clusterDomain: cluster.local") {
		t.Error("expected global.clusterDomain in YAML")
	}
}
