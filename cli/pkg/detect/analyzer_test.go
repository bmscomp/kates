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
		r.Storage = append(r.Storage, SCInfo{Name: "sc-" + string(rune('a'+i))})
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
