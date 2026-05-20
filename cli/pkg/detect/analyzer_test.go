package detect

import (
	"testing"
)

func buildReport(zones int, strimziCRDs bool, strimziRunning bool, scCount int, coreDNS int) *DetectReport {
	r := &DetectReport{
		K8sVersion:  "1.31",
		K8sMinor:    31,
		HelmVersion: "v3.15.0",
		HelmMajor:   3,
		Strimzi: StrimziInfo{
			CRDsPresent: strimziCRDs,
			Running:     strimziRunning,
			Namespace:   "kafka",
		},
		Network: NetworkInfo{
			CNI:            "calico",
			CoreDNSRunning: coreDNS,
		},
		Monitoring: MonitoringInfo{
			PodMonitorCRD: true,
		},
	}

	for i := 0; i < zones; i++ {
		r.Zones = append(r.Zones, ZoneInfo{
			Name:             "zone-" + string(rune('a'+i)),
			Nodes:            1,
			CPUAllocatable:   8000,
			MemAllocatableGi: 32,
		})
		r.Nodes = append(r.Nodes, NodeInfo{
			CPU:      8000,
			MemoryGi: 32,
		})
	}

	for i := 0; i < scCount; i++ {
		r.Storage = append(r.Storage, SCInfo{
			Name:       "sc-" + string(rune('a'+i)),
			ProbedIOPS: 2000,
		})
	}

	return r
}

func TestVerdict_FullyCompatible(t *testing.T) {
	m := setupHealthyMock()
	a := NewAnalyzer(m)
	r := buildReport(3, true, true, 1, 2)
	a.Analyze(r, ParsedReqs{})

	if !r.Verdict.Compatible {
		t.Error("expected compatible verdict")
	}
	if r.Verdict.Fails != 0 {
		t.Errorf("expected 0 fails, got %d", r.Verdict.Fails)
	}
}

func TestVerdict_InsufficientResources(t *testing.T) {
	m := setupHealthyMock()
	a := NewAnalyzer(m)
	// 3 zones, 1 node each, but only 1000m CPU each
	r := buildReport(3, true, true, 1, 2)
	for i := range r.Nodes {
		r.Nodes[i].CPU = 1000
		r.Nodes[i].MemoryGi = 1
	}
	a.Analyze(r, ParsedReqs{})

	if r.Budget.Sufficient {
		t.Error("expected budget to be insufficient")
	}

	// Broker resources check should fail
	found := false
	for _, c := range r.Verdict.Checks {
		if c.Description == "Broker resources fit (all zones)" && !c.Status {
			found = true
		}
	}
	if !found {
		t.Error("expected broker resources check to fail")
	}
}

func TestVerdict_MissingZones(t *testing.T) {
	m := setupHealthyMock()
	a := NewAnalyzer(m)
	r := buildReport(1, true, true, 1, 2) // only 1 zone
	a.Analyze(r, ParsedReqs{})

	if r.Verdict.Compatible {
		t.Error("expected incompatible verdict with 1 zone")
	}

	found := false
	for _, c := range r.Verdict.Checks {
		if c.Description == "≥ 3 availability zones" && !c.Status {
			found = true
		}
	}
	if !found {
		t.Error("expected zone check to fail")
	}
}

func TestVerdict_KyvernoWarnings(t *testing.T) {
	m := setupHealthyMock()
	a := NewAnalyzer(m)
	r := buildReport(3, true, true, 1, 2)
	r.Admission.Kyverno.Installed = true
	r.Admission.Kyverno.Constraints.EmptyPodSelectorBlocked = true
	r.Admission.Kyverno.KafkaRelevant = []KyvernoPolicyInfo{
		{Name: "restrict-empty-podselector", Action: "enforce"},
	}
	a.Analyze(r, ParsedReqs{})

	if !r.Verdict.Compatible {
		t.Error("expected compatible (Kyverno is warning, not fail)")
	}
	if r.Verdict.Warns == 0 {
		t.Error("expected at least 1 warning for Kyverno empty-selector")
	}
}

func TestBudget_CustomValues(t *testing.T) {
	m := setupHealthyMock()
	a := NewAnalyzer(m)
	r := buildReport(3, true, true, 1, 2)
	reqs := ParsedReqs{
		BrokerCPU:     500,
		BrokerMem:     2,
		ControllerCPU: 250,
		ControllerMem: 1,
	}
	a.Analyze(r, reqs)

	if r.Budget.BrokerCPU != 500 {
		t.Errorf("expected broker CPU 500, got %d", r.Budget.BrokerCPU)
	}
	if r.Budget.CtrlMem != 1 {
		t.Errorf("expected controller mem 1, got %d", r.Budget.CtrlMem)
	}
}

func TestRemediation_NoStrimzi(t *testing.T) {
	r := buildReport(3, false, false, 1, 2)
	r.Verdict.Checks = []CheckResult{{Description: "Strimzi CRDs installed", Status: false}}
	hints := GenerateRemediation(r)

	found := false
	for _, h := range hints {
		if h.Check == "Strimzi CRDs installed" && h.Severity == "critical" {
			found = true
			if len(h.Commands) == 0 {
				t.Error("expected remediation commands")
			}
		}
	}
	if !found {
		t.Error("expected Strimzi CRD remediation hint")
	}
}

func TestRemediation_HealthyCluster(t *testing.T) {
	r := buildReport(3, true, true, 1, 2)
	r.Monitoring.PodMonitorCRD = true
	r.Budget.Sufficient = true
	r.Network.CoreDNSRunning = 2
	hints := GenerateRemediation(r)

	for _, h := range hints {
		if h.Severity == "critical" {
			t.Errorf("did not expect critical remediation on healthy cluster: %s", h.Check)
		}
	}
}

func TestRemediation_StrimziOperator(t *testing.T) {
	// Case 1: Strimzi CRDs present but operator not running, and Namespace is blank (missing deployment)
	r1 := buildReport(3, true, false, 1, 2)
	r1.Strimzi.Namespace = ""
	hints1 := GenerateRemediation(r1)

	found1 := false
	for _, h := range hints1 {
		if h.Check == "Strimzi operator running" {
			found1 = true
			if len(h.Commands) != 2 || h.Commands[0] != "kates deploy          # deploy the operator" {
				t.Errorf("expected kates deploy hint command, got: %v", h.Commands)
			}
		}
	}
	if !found1 {
		t.Error("expected Strimzi operator running remediation hint")
	}

	// Case 2: Strimzi CRDs present but operator not running, and Namespace is set (running/degraded deployment exists)
	r2 := buildReport(3, true, false, 1, 2)
	r2.Strimzi.Namespace = "my-kafka-namespace"
	hints2 := GenerateRemediation(r2)

	found2 := false
	for _, h := range hints2 {
		if h.Check == "Strimzi operator running" {
			found2 = true
			if len(h.Commands) != 2 || h.Commands[1] != "kubectl rollout restart deployment/strimzi-cluster-operator -n my-kafka-namespace" {
				t.Errorf("expected kubectl rollout restart hint command, got: %v", h.Commands)
			}
		}
	}
	if !found2 {
		t.Error("expected Strimzi operator running remediation hint")
	}
}

func TestVerdict_SecurityAndSizing(t *testing.T) {
	m := setupHealthyMock()
	a := NewAnalyzer(m)

	// Case 1: Insufficient resources for minimal profile (total 2CPU, 4Gi)
	r1 := buildReport(3, true, true, 1, 2)
	r1.Nodes[0].CPU = 1000
	r1.Nodes[0].MemoryGi = 2
	r1.Nodes[1].CPU = 500
	r1.Nodes[1].MemoryGi = 1
	r1.Nodes[2].CPU = 500
	r1.Nodes[2].MemoryGi = 1
	r1.Security.PSALabelEnforced = "restricted"
	r1.Security.KyvernoEnforced = true
	r1.Security.PermissionsOk = true

	a.Analyze(r1, ParsedReqs{})

	if r1.Budget.RecommendedProfile != "insufficient" {
		t.Errorf("expected recommended profile to be 'insufficient', got %q", r1.Budget.RecommendedProfile)
	}

	// Sizing check should fail
	sizingFound := false
	psaFound := false
	for _, c := range r1.Verdict.Checks {
		if c.Description == "Sizing recommendation available" {
			sizingFound = true
			if c.Status {
				t.Error("expected sizing recommendation check to fail under insufficient resources")
			}
		}
		if c.Description == "Pod Security Standards compatible" {
			psaFound = true
			if !c.Status {
				t.Error("expected PSA check to pass")
			}
			if c.Detail != "restricted enforced (fully supported)" {
				t.Errorf("expected restricted enforced detail, got %q", c.Detail)
			}
		}
	}
	if !sizingFound {
		t.Error("expected 'Sizing recommendation available' check")
	}
	if !psaFound {
		t.Error("expected 'Pod Security Standards compatible' check")
	}

	// Case 2: Minimal profile (3CPU, 8Gi)
	r2 := buildReport(3, true, true, 1, 2)
	r2.Nodes[0].CPU = 1000
	r2.Nodes[0].MemoryGi = 3
	r2.Nodes[1].CPU = 1000
	r2.Nodes[1].MemoryGi = 3
	r2.Nodes[2].CPU = 1000
	r2.Nodes[2].MemoryGi = 2

	a.Analyze(r2, ParsedReqs{})
	if r2.Budget.RecommendedProfile != "minimal" {
		t.Errorf("expected recommended profile to be 'minimal', got %q", r2.Budget.RecommendedProfile)
	}

	// Case 3: Standard profile (8CPU, 24Gi)
	r3 := buildReport(3, true, true, 1, 2)
	r3.Nodes[0].CPU = 3000
	r3.Nodes[0].MemoryGi = 8
	r3.Nodes[1].CPU = 3000
	r3.Nodes[1].MemoryGi = 8
	r3.Nodes[2].CPU = 2000
	r3.Nodes[2].MemoryGi = 8

	a.Analyze(r3, ParsedReqs{})
	if r3.Budget.RecommendedProfile != "standard" {
		t.Errorf("expected recommended profile to be 'standard', got %q", r3.Budget.RecommendedProfile)
	}

	// Case 4: Production profile (15CPU, 49Gi)
	r4 := buildReport(3, true, true, 1, 2)
	r4.Nodes[0].CPU = 5000
	r4.Nodes[0].MemoryGi = 17
	r4.Nodes[1].CPU = 5000
	r4.Nodes[1].MemoryGi = 17
	r4.Nodes[2].CPU = 5000
	r4.Nodes[2].MemoryGi = 15

	a.Analyze(r4, ParsedReqs{})
	if r4.Budget.RecommendedProfile != "production" {
		t.Errorf("expected recommended profile to be 'production', got %q", r4.Budget.RecommendedProfile)
	}
}
