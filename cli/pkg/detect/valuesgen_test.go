package detect

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"gopkg.in/yaml.v3"
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

// poolsWithRole returns the generated pools carrying role, in order.
func poolsWithRole(vals *GeneratedValues, role string) []GenPool {
	var out []GenPool
	for _, p := range vals.NodePools.Pools {
		if slices.Contains(p.Roles, role) {
			out = append(out, p)
		}
	}
	return out
}

func TestGenerateValues_3AZ(t *testing.T) {
	r := buildTestReport(3, []string{"local-storage-alpha", "local-storage-gamma", "local-storage-sigma", "standard"})
	gen := NewValuesGenerator(r, "krafter")
	vals := gen.Generate()

	brokers := poolsWithRole(vals, "broker")
	if len(brokers) != 3 {
		t.Fatalf("expected 3 broker pools, got %d", len(brokers))
	}
	// Check zone-named pools
	if brokers[0].Name != "brokers-alpha" {
		t.Errorf("expected brokers-alpha, got %s", brokers[0].Name)
	}
	if brokers[1].Name != "brokers-gamma" {
		t.Errorf("expected brokers-gamma, got %s", brokers[1].Name)
	}
	if brokers[2].Name != "brokers-sigma" {
		t.Errorf("expected brokers-sigma, got %s", brokers[2].Name)
	}
	// Check per-zone SC matching, on the volume the chart reads it from
	if brokers[0].Storage.Volumes[0].Class != "local-storage-alpha" {
		t.Errorf("expected local-storage-alpha for alpha zone, got %s", brokers[0].Storage.Volumes[0].Class)
	}
	if brokers[1].Storage.Volumes[0].Class != "local-storage-gamma" {
		t.Errorf("expected local-storage-gamma for gamma zone, got %s", brokers[1].Storage.Volumes[0].Class)
	}
	if brokers[0].Zone != "alpha" || brokers[0].Storage.Volumes[0].Size != gen.Cap.BrokerStorage {
		t.Errorf("pool %+v is not pinned to its zone with the broker storage size", brokers[0])
	}
}

func TestGenerateValues_2AZ(t *testing.T) {
	r := buildTestReport(2, []string{"standard"})
	gen := NewValuesGenerator(r, "krafter")
	vals := gen.Generate()

	brokers := poolsWithRole(vals, "broker")
	if len(brokers) != 3 {
		t.Fatalf("expected 3 broker pools (2 pinned + 1 float), got %d", len(brokers))
	}
	if brokers[2].Name != "brokers-float" {
		t.Errorf("expected floating pool named brokers-float, got %s", brokers[2].Name)
	}
	if brokers[2].Zone != "" {
		t.Errorf("expected empty zone for floating pool, got %s", brokers[2].Zone)
	}
}

func TestGenerateValues_NoZones(t *testing.T) {
	r := buildTestReport(0, []string{"standard"})
	// Add nodes without zones
	r.Nodes = append(r.Nodes, NodeInfo{CPU: 8000, MemoryGi: 16})
	gen := NewValuesGenerator(r, "krafter")
	vals := gen.Generate()

	brokers := poolsWithRole(vals, "broker")
	if len(brokers) != 1 {
		t.Fatalf("expected 1 broker pool (no zones), got %d", len(brokers))
	}
	if brokers[0].Name != "brokers" {
		t.Errorf("expected pool named 'brokers', got %s", brokers[0].Name)
	}
	if vals.NodePools.Defaults.Scheduling.Spread.Enabled {
		t.Error("expected topology spread disabled with no zones")
	}
	if vals.NodePools.Defaults.Scheduling.ZoneKey != "" {
		t.Errorf("no pool pins a zone, so zoneKey should stay the chart's; got %q", vals.NodePools.Defaults.Scheduling.ZoneKey)
	}
}

// Controller sizing divides the controller share by the number of
// controllers the pools emit — one per zone — not by a replica count the
// pools never used: three zones used to get three single-controller pools
// sized as if they were one.
func TestGenerateValues_ControllerSizingMatchesThePoolsEmitted(t *testing.T) {
	for _, tc := range []struct {
		zones, wantControllers int
	}{{3, 3}, {4, 4}, {1, 3}, {2, -1}, {0, -1}} {
		r := buildTestReport(tc.zones, []string{"standard"})
		if tc.zones == 0 {
			r.Nodes = append(r.Nodes, NodeInfo{CPU: 8000, MemoryGi: 16})
		}
		gen := NewValuesGenerator(r, "krafter")
		if tc.wantControllers < 0 {
			// One pool, sized by the cluster.
			tc.wantControllers = gen.selectControllerReplicas()
		}
		vals := gen.Generate()
		emitted := 0
		for _, p := range poolsWithRole(vals, "controller") {
			emitted += p.Replicas
		}
		if emitted != tc.wantControllers || gen.Cap.ControllerReplicas != tc.wantControllers {
			t.Errorf("%d zones: pools run %d controllers, budget divides by %d, want %d",
				tc.zones, emitted, gen.Cap.ControllerReplicas, tc.wantControllers)
		}
		if tc.zones == 3 {
			want := clamp(int(float64(gen.Cap.KafkaCPU)*controllerCPUShare)/3, minControllerCPU, maxControllerCPU)
			if gen.Cap.ControllerCPU != want {
				t.Errorf("controller CPU = %dm, want the share split three ways, %dm", gen.Cap.ControllerCPU, want)
			}
		}
	}
}

// The 0.4 keys are gone from the output: kafka-cluster 1.x ignores
// strimziOperator, crdUpgrade and kafkaConnect, fails on
// drainCleaner.enabled, and translates the pool keys with a deprecation
// note. Every top-level key emitted is one the chart's schema knows and
// does not mark deprecated.
func TestRenderValues_EmitsOnlyCurrentChartKeys(t *testing.T) {
	r := buildTestReport(3, []string{"standard"})
	r.Strimzi.Running = false
	r.Strimzi.CRDsPresent = false
	r.Ecosystem.KafkaConnect.Installed = true
	var buf bytes.Buffer
	if err := RenderValues(r, "krafter", &buf); err != nil {
		t.Fatalf("RenderValues failed: %v", err)
	}
	var doc map[string]interface{}
	if err := yaml.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("output is not YAML: %v\n%s", err, buf.String())
	}
	for _, legacy := range []string{"strimziOperator", "crdUpgrade", "drainCleaner", "kafkaConnect", "controllerPools", "brokerPools",
		"controllerDefaults", "brokerDefaults", "networkPolicies", "strimzi-kafka-operator", "podMonitors", "dashboards", "zookeeper"} {
		if _, ok := doc[legacy]; ok {
			t.Errorf("the deprecated key %q is emitted:\n%s", legacy, buf.String())
		}
	}
	pools, _ := doc["nodePools"].(map[string]interface{})["pools"].([]interface{})
	if len(pools) != 6 {
		t.Errorf("want 3 controller + 3 broker pools under nodePools.pools, got %d", len(pools))
	}

	schema := loadChartSchema(t)
	props, _ := schema["properties"].(map[string]interface{})
	for key := range doc {
		p, ok := props[key].(map[string]interface{})
		if !ok {
			t.Errorf("top-level key %q is not in the chart's values.schema.json", key)
			continue
		}
		if dep, _ := p["deprecated"].(bool); dep {
			t.Errorf("top-level key %q is deprecated in the chart's schema", key)
		}
	}
}

const kafkaClusterChart = "../../../charts/kafka-cluster"

func loadChartSchema(t *testing.T) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(kafkaClusterChart, "values.schema.json"))
	if err != nil {
		t.Skipf("chart schema not available: %v", err)
	}
	var schema map[string]interface{}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("parse values.schema.json: %v", err)
	}
	return schema
}

// The generated values pass the chart's own schema, the check helm runs
// before rendering: a misspelled or moved key fails here rather than at
// install.
func TestGeneratedValuesMatchChartSchema(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(kafkaClusterChart, "values.schema.json"))
	if err != nil {
		t.Skipf("chart schema not available: %v", err)
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("parse values.schema.json: %v", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve values.schema.json: %v", err)
	}
	for _, tc := range []struct {
		name  string
		zones int
		prov  string
		scs   []string
	}{
		{"three zones on kind", 3, "kind", []string{"local-storage-alpha", "local-storage-gamma", "local-storage-sigma", "standard"}},
		{"two zones on eks", 2, "eks", []string{"gp3"}},
		{"one zone", 1, "generic", []string{"standard"}},
		{"no zones", 0, "aks", []string{"managed-csi"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := buildTestReport(tc.zones, tc.scs)
			r.Provider = tc.prov
			if tc.zones == 0 {
				r.Nodes = append(r.Nodes, NodeInfo{CPU: 8000, MemoryGi: 16})
			}
			var buf bytes.Buffer
			if err := RenderValues(r, "krafter", &buf); err != nil {
				t.Fatal(err)
			}
			var doc map[string]interface{}
			if err := yaml.Unmarshal(buf.Bytes(), &doc); err != nil {
				t.Fatal(err)
			}
			if err := resolved.Validate(doc); err != nil {
				t.Errorf("generated values fail the chart schema: %v\n%s", err, buf.String())
			}
		})
	}
}

// TestGeneratedValuesRenderWithHelm renders the generated values with the
// real chart, the way kates deploy installs them, and checks the pools and
// the keys the chart derived from them. It needs helm on PATH and skips
// without it; it touches no cluster. The chart's seaweedfs dependency comes
// from a remote repository, so the dependency build is skipped, not failed,
// when it cannot be reached.
func TestGeneratedValuesRenderWithHelm(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm not on PATH")
	}
	if _, err := os.Stat(kafkaClusterChart); err != nil {
		t.Skipf("chart not available: %v", err)
	}
	depCtx, depCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer depCancel()
	if out, err := exec.CommandContext(depCtx, helm, "dependency", "build", kafkaClusterChart).CombinedOutput(); err != nil {
		t.Skipf("helm dependency build (the chart's seaweedfs dependency is remote): %v\n%s", err, out)
	}
	r := buildTestReport(3, []string{"local-storage-alpha", "local-storage-gamma", "local-storage-sigma", "standard"})
	var buf bytes.Buffer
	if err := RenderValues(r, "krafter", &buf); err != nil {
		t.Fatal(err)
	}
	values := filepath.Join(t.TempDir(), "generated.yaml")
	if err := os.WriteFile(values, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, helm, "template", "krafter", kafkaClusterChart, "-n", "kafka", "-f", values).CombinedOutput()
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	rendered := string(out)
	for _, want := range []string{
		"kind: KafkaNodePool",
		"name: controllers-alpha",
		"name: brokers-sigma",
		"class: local-storage-gamma",
		"topology.kubernetes.io/zone",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered chart lacks %q", want)
		}
	}
	if strings.Contains(rendered, "DEPRECATED") {
		t.Errorf("the chart reports a deprecated key in the generated values:\n%s", rendered)
	}
}

// A base values file is the user's: the merge keeps every toggle it set
// (cruiseControl, alerts), every list it defined (its own pools), and lets
// the detected facts — the cluster domain, the default storage class — win
// over it, naming each one in the header. Detected values fill only the keys
// the base left unset.
func TestRenderValuesFromBase_BaseWinsExceptDetectedFacts(t *testing.T) {
	r := buildTestReport(3, []string{"local-storage-alpha", "local-storage-gamma", "local-storage-sigma", "standard"})
	r.Network.ClusterDomain = "taz-tls-int-1.local"
	r.Monitoring.PrometheusRuleCRD = false // the generator would say alerts: false
	base := []byte(`
global:
  clusterDomain: cluster.local
cruiseControl:
  enabled: true
alerts:
  enabled: true
kafka:
  listeners:
    - name: plain
      port: 9092
      type: internal
      tls: false
nodePools:
  roleDefaults:
    broker:
      resources:
        requests:
          cpu: 7000m
  pools:
    - name: mine
      roles: [controller, broker]
      replicas: 3
`)
	var buf bytes.Buffer
	if err := RenderValuesFromBase(r, "krafter", base, &buf); err != nil {
		t.Fatalf("RenderValuesFromBase: %v", err)
	}
	out := buf.String()
	var doc map[string]interface{}
	if err := yaml.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("merged output is not YAML: %v\n%s", err, out)
	}
	at := func(path string) interface{} {
		var cur interface{} = doc
		for _, k := range strings.Split(path, ".") {
			m, ok := cur.(map[string]interface{})
			if !ok {
				return nil
			}
			cur = m[k]
		}
		return cur
	}
	if at("cruiseControl.enabled") != true || at("alerts.enabled") != true {
		t.Errorf("the base's toggles were flipped: cruiseControl=%v alerts=%v\n%s", at("cruiseControl.enabled"), at("alerts.enabled"), out)
	}
	if pools, _ := at("nodePools.pools").([]interface{}); len(pools) != 1 {
		t.Errorf("the base's pool list was replaced: %v", at("nodePools.pools"))
	}
	if listeners, _ := at("kafka.listeners").([]interface{}); len(listeners) != 1 {
		t.Errorf("the base's listener list was replaced: %v", at("kafka.listeners"))
	}
	if at("nodePools.roleDefaults.broker.resources.requests.cpu") != "7000m" {
		t.Errorf("the base's broker CPU request was overridden: %v", at("nodePools.roleDefaults.broker.resources.requests.cpu"))
	}
	if at("nodePools.roleDefaults.broker.resources.requests.memory") == nil || at("nodePools.roleDefaults.controller.resources.requests.cpu") == nil {
		t.Error("the sizes the base did not set were not filled in")
	}
	if at("global.clusterDomain") != "taz-tls-int-1.local" || at("global.storageClass") != "standard" {
		t.Errorf("the detected facts did not win: domain=%v class=%v", at("global.clusterDomain"), at("global.storageClass"))
	}
	if !strings.Contains(out, "# Detected facts that replaced a value the base set:") ||
		!strings.Contains(out, "#   global.clusterDomain: cluster.local → taz-tls-int-1.local") {
		t.Errorf("the header does not name the override:\n%s", out)
	}
	if strings.Contains(out, "#   cruiseControl") || strings.Contains(out, "#   alerts") {
		t.Errorf("a toggle is listed as overridden:\n%s", out)
	}
}

// A base still on the 0.4 pool keys keeps them: the chart refuses both
// shapes at once, so no nodePools.pools join the merge.
func TestMergeValuesWithBase_LeavesLegacyPoolsAlone(t *testing.T) {
	base := map[string]interface{}{
		"brokerPools": []interface{}{map[string]interface{}{"name": "brokers", "replicas": 3}},
	}
	gen := map[string]interface{}{
		"nodePools": map[string]interface{}{
			"pools":        []interface{}{map[string]interface{}{"name": "brokers-a"}},
			"roleDefaults": map[string]interface{}{"broker": map[string]interface{}{"resources": map[string]interface{}{}}},
		},
	}
	merged, overrides := MergeValuesWithBase(base, gen)
	np := merged["nodePools"].(map[string]interface{})
	if _, ok := np["pools"]; ok {
		t.Errorf("nodePools.pools were added beside brokerPools: %v", merged)
	}
	if _, ok := np["roleDefaults"]; !ok {
		t.Error("roleDefaults should still be filled in")
	}
	if len(overrides) != 0 {
		t.Errorf("nothing was overridden, got %v", overrides)
	}
	if _, ok := gen["nodePools"].(map[string]interface{})["pools"]; !ok {
		t.Error("the caller's generated map was mutated")
	}
}

func TestGenerateValues_MonitoringFollowsCRDs(t *testing.T) {
	r := buildTestReport(3, []string{"standard"})
	vals := NewValuesGenerator(r, "krafter").Generate()
	if !vals.Monitoring.PodMonitor.Enabled {
		t.Error("expected monitoring.podMonitor.enabled=true when the PodMonitor CRD exists")
	}
	if !vals.Alerts.Enabled {
		t.Error("expected alerts.enabled=true when both operator CRDs exist")
	}

	r.Monitoring.PrometheusRuleCRD = false
	vals = NewValuesGenerator(r, "krafter").Generate()
	if !vals.Monitoring.PodMonitor.Enabled || vals.Alerts.Enabled {
		t.Errorf("expected scrape on and rules off without the PrometheusRule CRD, got podMonitor=%v alerts=%v",
			vals.Monitoring.PodMonitor.Enabled, vals.Alerts.Enabled)
	}

	r.Monitoring.PodMonitorCRD = false
	vals = NewValuesGenerator(r, "krafter").Generate()
	if vals.Monitoring.PodMonitor.Enabled || vals.Alerts.Enabled {
		t.Errorf("expected scrape and rules off without prometheus-operator, got podMonitor=%v alerts=%v",
			vals.Monitoring.PodMonitor.Enabled, vals.Alerts.Enabled)
	}
}

func TestRenderValues_EmitsTheCurrentMonitoringKey(t *testing.T) {
	r := buildTestReport(3, []string{"standard"})
	var buf bytes.Buffer
	if err := RenderValues(r, "krafter", &buf); err != nil {
		t.Fatalf("RenderValues failed: %v", err)
	}
	content := buf.String()
	if !strings.Contains(content, "monitoring:\n    podMonitor:\n        enabled: true") {
		t.Errorf("expected monitoring.podMonitor.enabled in the output, got:\n%s", content)
	}
	// The 0.4 key wins over the current one inside the chart whenever the
	// current one holds the default, so it must not be emitted at all.
	if strings.Contains(content, "podMonitors:") {
		t.Error("the deprecated podMonitors key must not be emitted")
	}
	if strings.Contains(content, "dashboards:") {
		t.Error("dashboards.enabled does nothing on kafka-cluster 1.x and must not be emitted")
	}
}

func TestGenerateValues_KyvernoSafe(t *testing.T) {
	r := buildTestReport(3, []string{"standard"})
	r.Admission.Kyverno.Installed = true
	r.Admission.Kyverno.Constraints.EmptyPodSelectorBlocked = true
	gen := NewValuesGenerator(r, "krafter")
	vals := gen.Generate()

	if !vals.NetworkPolicy.Enabled || !vals.NetworkPolicy.DefaultDeny.Enabled {
		t.Error("expected networkPolicy and its default deny enabled")
	}
	if vals.NetworkPolicy.DefaultDeny.Selector == nil {
		t.Fatal("expected an explicit defaultDeny.selector")
	}
	if vals.NetworkPolicy.DefaultDeny.Selector["app.kubernetes.io/part-of"] != "strimzi-krafter" {
		t.Errorf("expected strimzi-krafter selector, got %v", vals.NetworkPolicy.DefaultDeny.Selector)
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

func TestGenerateValues_CapsKafkaPodsToSchedulableNode(t *testing.T) {
	r := &DetectReport{
		Context:  "remote",
		Provider: "generic",
		Network:  NetworkInfo{ClusterDomain: "cluster.local"},
		Zones: []ZoneInfo{
			{Name: "ext", Nodes: 3, CPUAllocatable: 23100, MemAllocatableGi: 90},
			{Name: "mrs3", Nodes: 3, CPUAllocatable: 23100, MemAllocatableGi: 90},
			{Name: "vil", Nodes: 3, CPUAllocatable: 23100, MemAllocatableGi: 90},
		},
		Nodes: []NodeInfo{
			{Name: "ext-a", Zone: "ext", CPU: 7700, MemoryGi: 30},
			{Name: "ext-b", Zone: "ext", CPU: 7700, MemoryGi: 30},
			{Name: "ext-c", Zone: "ext", CPU: 7700, MemoryGi: 30},
			{Name: "mrs3-a", Zone: "mrs3", CPU: 7700, MemoryGi: 30},
			{Name: "mrs3-b", Zone: "mrs3", CPU: 7700, MemoryGi: 30},
			{Name: "mrs3-c", Zone: "mrs3", CPU: 7700, MemoryGi: 30},
			{Name: "vil-a", Zone: "vil", CPU: 7700, MemoryGi: 30},
			{Name: "vil-b", Zone: "vil", CPU: 7700, MemoryGi: 30},
			{Name: "vil-c", Zone: "vil", CPU: 7700, MemoryGi: 30},
		},
		Workload: WorkloadPressure{
			TotalCPURequests: 24050,
			TotalMemRequests: 4,
			PerNode: []NodePressure{
				{Name: "ext-a", CPURequested: 2980, MemRequested: 4},
				{Name: "ext-b", CPURequested: 4623, MemRequested: 8},
				{Name: "ext-c", CPURequested: 2930, MemRequested: 4},
				{Name: "mrs3-a", CPURequested: 2880, MemRequested: 4},
				{Name: "mrs3-b", CPURequested: 2990, MemRequested: 4},
				{Name: "mrs3-c", CPURequested: 4063, MemRequested: 7},
				{Name: "vil-a", CPURequested: 2680, MemRequested: 3},
				{Name: "vil-b", CPURequested: 4223, MemRequested: 7},
				{Name: "vil-c", CPURequested: 2880, MemRequested: 4},
			},
		},
	}
	r.Workload = hydrateWorkloadPressure(r.Nodes, r.Workload)

	gen := NewValuesGeneratorWithReserve(r, "krafter", 0.30)
	vals := gen.Generate()

	if gen.Cap.WeakestZoneCPU != 8796 {
		t.Fatalf("weakest zone budget = %dm, want 8796m", gen.Cap.WeakestZoneCPU)
	}
	if gen.Cap.BrokerCPU != 3339 {
		t.Fatalf("broker CPU = %dm, want the weakest zone's best-node cap of 3339m", gen.Cap.BrokerCPU)
	}
	if got := vals.NodePools.RoleDefaults.Broker.Resources.Requests.CPU; got != "3339m" {
		t.Fatalf("rendered broker CPU request = %s, want 3339m", got)
	}
	if gen.Cap.BrokerMem != 16 {
		t.Fatalf("broker memory should still use the configured max when node memory permits it, got %dGi", gen.Cap.BrokerMem)
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

}
