package detect

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderPDF(t *testing.T) {
	// Create temporary directory for output report
	tmpDir, err := os.MkdirTemp("", "kates-pdf-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	pdfPath := filepath.Join(tmpDir, "test-report.pdf")

	// Build a fully fleshed-out report to exercise all sections of RenderPDF
	report := buildReport(3, true, true, 2, 2)
	
	// Add context and basic fields
	report.Context = "test-context"
	report.Provider = "kind"
	report.K8sVersion = "v1.31.0"
	report.HelmVersion = "v3.15.0"
	
	// Add custom storage class audit details
	report.StorageAudit.PVCount = 5
	report.StorageAudit.PVBoundCount = 3
	report.StorageAudit.PVTotalCapacity = "150Gi"
	report.StorageAudit.CSIDrivers = []string{"pd.csi.storage.gke.io", "filestore.csi.storage.gke.io"}

	// Strimzi info
	report.Strimzi.Image = "quay.io/strimzi/operator:0.38.0"
	report.Strimzi.ReadyReplicas = 1
	report.Strimzi.TotalReplicas = 1
	report.Strimzi.Health.WarningLogs = []string{
		"Warning: Kafka cluster reconciliation is slow",
		"Another warning: resource limits not configured correctly for pod A",
	}
	report.Strimzi.Health.MissingCRDs = []string{"kafkatops.kafka.strimzi.io"}
	report.Strimzi.CapacityStatus = "Supports up to 5 brokers"

	// Network Latency Matrix
	report.Network.LatencyMatrix = []LatencyResult{
		{SourceZone: "zone-a", TargetZone: "zone-a", AvgMs: 0.05, Success: true},
		{SourceZone: "zone-a", TargetZone: "zone-b", AvgMs: 1.25, Success: true},
		{SourceZone: "zone-a", TargetZone: "zone-c", AvgMs: 1.88, Success: true},
		{SourceZone: "zone-b", TargetZone: "zone-a", AvgMs: 1.22, Success: true},
		{SourceZone: "zone-b", TargetZone: "zone-b", AvgMs: 0.04, Success: true},
		{SourceZone: "zone-b", TargetZone: "zone-c", AvgMs: 2.10, Success: true},
		{SourceZone: "zone-c", TargetZone: "zone-a", AvgMs: 1.95, Success: true},
		{SourceZone: "zone-c", TargetZone: "zone-b", AvgMs: 2.05, Success: true},
		{SourceZone: "zone-c", TargetZone: "zone-c", AvgMs: 0.06, Success: true},
	}

	// Admission Controllers
	report.Admission.Kyverno.Installed = true
	report.Admission.Kyverno.Namespace = "kyverno"
	report.Admission.Kyverno.Version = "1.11.0"
	report.Admission.Kyverno.ClusterPolicies = []KyvernoPolicyInfo{{Name: "policy-1"}, {Name: "policy-2"}}
	report.Admission.Kyverno.KafkaRelevant = []KyvernoPolicyInfo{{Name: "restrict-empty-podselector", Action: "enforce"}}

	report.Admission.Gatekeeper.Installed = true
	report.Admission.Gatekeeper.Namespace = "gatekeeper-system"
	report.Admission.Gatekeeper.Constraints = []GatekeeperConstraint{{Name: "constraint-1", Kind: "K8sRequiredLabels"}}

	// Secrets Audit
	report.SecretAudit.NamespaceCreated = true
	report.SecretAudit.SecretCreated = false
	report.SecretAudit.BlockedByPolicy = true
	report.SecretAudit.PolicyName = "restrict-empty-podselector"

	// Network policies audit
	report.NetPolAudit.TotalCount = 2
	report.NetPolAudit.Existing = []ExistingNetPol{
		{
			Name:         "allow-kafka-traffic",
			PodSelector:  "app.kubernetes.io/name=kafka",
			PolicyTypes:  []string{"Ingress", "Egress"},
			IngressRules: 2,
			EgressRules:  1,
			ManagedBy:    "strimzi",
		},
	}

	// Resource Budget details
	report.Budget.CtrlCPU = 600
	report.Budget.CtrlMem = 2
	report.Budget.BrokerCPU = 1000
	report.Budget.BrokerMem = 4
	report.Budget.OtherCPU = 200
	report.Budget.OtherMem = 1
	report.Budget.NeedCPU = 3800
	report.Budget.NeedMem = 15
	report.Budget.TotalCPU = 24000
	report.Budget.TotalMem = 96
	report.Budget.Sufficient = true

	// Compatibility checks
	report.Verdict.Checks = []CheckResult{
		{Description: "Strimzi CRDs installed", Status: true, Detail: "All required CRDs present"},
		{Description: "≥ 3 availability zones", Status: true, Detail: "3 zones detected: zone-a, zone-b, zone-c"},
		{Description: "Resource budget fits", Status: true, Detail: "Sufficient CPU & Memory allocation"},
	}

	// Render the PDF
	err = RenderPDF(report, pdfPath)
	if err != nil {
		t.Fatalf("RenderPDF failed: %v", err)
	}

	// Verify file was created and is not empty
	info, err := os.Stat(pdfPath)
	if err != nil {
		t.Fatalf("PDF file was not created: %v", err)
	}
	if info.Size() == 0 {
		t.Error("PDF file is empty")
	}
}

func TestRenderMarkdownAndJSON(t *testing.T) {
	report := buildReport(3, true, true, 2, 2)
	report.Context = "test-context"
	report.Provider = "kind"
	report.K8sVersion = "v1.31.0"
	report.HelmVersion = "v3.15.0"

	var buf bytes.Buffer
	RenderMarkdown(report, &buf)
	if buf.Len() == 0 {
		t.Error("RenderMarkdown wrote no bytes")
	}

	buf.Reset()
	RenderJSONTo(report, &buf)
	if buf.Len() == 0 {
		t.Error("RenderJSONTo wrote no bytes")
	}
}

func TestRenderHTML(t *testing.T) {
	report := buildReport(3, true, true, 2, 2)
	report.Context = "test-context"
	report.Provider = "kind"
	report.K8sVersion = "v1.31.0"
	report.HelmVersion = "v3.15.0"

	// Mock some security audit and bandwidth matrix
	report.Security.HasExcessivePrivileges = true
	report.Security.ExpiringCerts = []CertExpirationInfo{
		{SecretName: "test-cert", Subject: "CN=test", ExpiryDate: "2026-06-15", DaysLeft: 25},
	}
	report.Budget.RecommendedProfile = "production"

	// Trigger matrix generation logic by having zones in Nodes
	report.Nodes[0].Zone = "zone-a"
	report.Nodes[1].Zone = "zone-b"

	var buf bytes.Buffer
	err := RenderHTML(report, &buf)
	if err != nil {
		t.Fatalf("RenderHTML failed: %v", err)
	}

	if buf.Len() == 0 {
		t.Error("RenderHTML wrote no bytes")
	}

	htmlStr := buf.String()
	if !strings.Contains(htmlStr, "Kates Preflight Audit Report") {
		t.Error("expected html to contain title")
	}
	if !strings.Contains(htmlStr, "Wildcard RBAC Audit") {
		t.Error("expected html to contain Wildcard RBAC Audit section")
	}
	if !strings.Contains(htmlStr, "TLS Secret Expiration") {
		t.Error("expected html to contain TLS Secret Expiration section")
	}
	if !strings.Contains(htmlStr, "zone-a") {
		t.Error("expected html to contain zone-a in matrix")
	}
}


func TestLiveStorageBench_Success(t *testing.T) {
	m := NewMockExecutor()
	m.Responses["kubectl create ns"] = ""
	m.Responses["kubectl label ns"] = ""
	m.Responses["kubectl delete ns"] = ""
	m.Responses["sh -c cat <<EOF | kubectl apply -f -"] = ""
	m.Responses["kubectl run kates-detect-bench-pod-0"] = ""
	m.Responses["kubectl wait --for=condition=Ready pod/kates-detect-bench-pod-0"] = "pod/kates-detect-bench-pod-0 condition met"

	// Mock FIO logs
	fioJSON := `{
  "fio version": "fio-3.36",
  "jobs": [
    {
      "read": {
        "iops": 5000.5,
        "lat_ns": {
          "mean": 450000.0
        }
      },
      "write": {
        "iops": 4999.5,
        "lat_ns": {
          "mean": 550000.0
        }
      }
    }
  ]
}`
	m.Responses["kubectl logs -f kates-detect-bench-pod-0"] = "fetch https://dl-cdn.alpinelinux.org/alpine/v3.19/main\nInstalling fio...\n" + fioJSON

	collector := NewCollector(m)
	collector.BenchStorage = true

	var progressMessages []string
	collector.OnProgress = func(msg string) {
		progressMessages = append(progressMessages, msg)
	}

	report := &DetectReport{
		Nodes: []NodeInfo{{Name: "node-1"}},
		Storage: []SCInfo{
			{Name: "gp3", Provisioner: "ebs.csi.aws.com"},
		},
	}

	collector.runLiveStorageBench(context.Background(), report)

	if report.Storage[0].ProbedIOPS != 10000 {
		t.Errorf("Expected 10000 IOPS, got %d", report.Storage[0].ProbedIOPS)
	}
	if report.Storage[0].ProbeLatencyMs != 0.5 {
		t.Errorf("Expected 0.5ms latency, got %f", report.Storage[0].ProbeLatencyMs)
	}

	if len(progressMessages) == 0 {
		t.Error("Expected progress messages to be sent, but got none")
	}
	hasSettingUp := false
	hasProvisioning := false
	hasSuccess := false
	for _, msg := range progressMessages {
		if len(msg) > 0 {
			if strings.Contains(msg, "setting up temporary namespace") {
				hasSettingUp = true
			}
			if strings.Contains(msg, "provisioning ephemeral PVC") {
				hasProvisioning = true
			}
			if strings.Contains(msg, "successfully measured") {
				hasSuccess = true
			}
		}
	}
	if !hasSettingUp || !hasProvisioning || !hasSuccess {
		t.Errorf("Progress messages missing expected milestones. Got: %v", progressMessages)
	}
}

func TestLiveStorageBench_Fallback(t *testing.T) {
	m := NewMockExecutor()
	m.Responses["kubectl create ns"] = ""
	m.Responses["kubectl label ns"] = ""
	m.Responses["kubectl delete ns"] = ""
	m.Responses["sh -c cat <<EOF | kubectl apply -f -"] = ""
	m.Responses["kubectl run kates-detect-bench-pod-0"] = ""
	m.Errors["kubectl wait --for=condition=Ready pod/kates-detect-bench-pod-0"] = fmt.Errorf("timeout waiting for pod")

	collector := NewCollector(m)
	collector.BenchStorage = true

	report := &DetectReport{
		Nodes: []NodeInfo{{Name: "node-1"}},
		Storage: []SCInfo{
			{Name: "gp3", Provisioner: "ebs.csi.aws.com", ProbedIOPS: 3000, ProbeLatencyMs: 0.5},
		},
	}

	collector.runLiveStorageBench(context.Background(), report)

	// Since live benchmarking failed/timed out, the values should remain untouched (not set to 0 or panic)
	if report.Storage[0].ProbedIOPS != 3000 {
		t.Errorf("Expected fallback to keep 3000 IOPS, got %d", report.Storage[0].ProbedIOPS)
	}
	if report.Storage[0].ProbeLatencyMs != 0.5 {
		t.Errorf("Expected fallback to keep 0.5ms latency, got %f", report.Storage[0].ProbeLatencyMs)
	}
}

func TestAZBandwidthBench_Success(t *testing.T) {
	m := NewMockExecutor()
	m.Responses["kubectl create ns"] = ""
	m.Responses["kubectl label ns"] = ""
	m.Responses["kubectl delete ns"] = ""
	m.Responses["kubectl run prober-"] = ""
	m.Responses["kubectl wait --for=condition=Ready pod"] = "pod condition met"
	m.Responses["apk add"] = "installed iperf3"
	m.Responses["iperf3 -s"] = ""

	// Mock Pod list JSON
	podListJSON := `{
  "items": [
    {
      "metadata": {
        "name": "prober-zone-a"
      },
      "status": {
        "podIP": "10.244.1.2"
      }
    },
    {
      "metadata": {
        "name": "prober-zone-b"
      },
      "status": {
        "podIP": "10.244.2.3"
      }
    }
  ]
}`
	m.Responses["get pods"] = podListJSON

	// Mock iperf3 JSON outputs
	iperfJSON_ab := `{
  "end": {
    "sum_received": {
      "bits_per_second": 945000000.0
    }
  }
}`
	iperfJSON_ba := `{
  "end": {
    "sum_received": {
      "bits_per_second": 880000000.0
    }
  }
}`
	m.Responses["iperf3 -c 10.244.2.3"] = iperfJSON_ab
	m.Responses["iperf3 -c 10.244.1.2"] = iperfJSON_ba

	collector := NewCollector(m)
	collector.BenchNetwork = true

	var progressMessages []string
	collector.OnProgress = func(msg string) {
		progressMessages = append(progressMessages, msg)
	}

	report := &DetectReport{
		Nodes: []NodeInfo{
			{Name: "node-1", Zone: "zone-a"},
			{Name: "node-2", Zone: "zone-b"},
		},
	}

	collector.runAZBandwidthBench(context.Background(), report)

	var res1, res2 *BandwidthResult
	for i := range report.Network.BandwidthMatrix {
		r := &report.Network.BandwidthMatrix[i]
		if r.SourceZone == "zone-a" && r.TargetZone == "zone-b" {
			res1 = r
		} else if r.SourceZone == "zone-b" && r.TargetZone == "zone-a" {
			res2 = r
		}
	}

	if res1 == nil {
		t.Errorf("Could not find bandwidth result for zone-a -> zone-b")
	} else if !res1.Success || res1.BandwidthMbps != 945.0 {
		t.Errorf("Unexpected result for zone-a -> zone-b: %+v", *res1)
	}

	if res2 == nil {
		t.Errorf("Could not find bandwidth result for zone-b -> zone-a")
	} else if !res2.Success || res2.BandwidthMbps != 880.0 {
		t.Errorf("Unexpected result for zone-b -> zone-a: %+v", *res2)
	}

	if len(progressMessages) == 0 {
		t.Error("Expected progress messages to be sent, but got none")
	}
}

func TestDNSResolutionProbe_Success(t *testing.T) {
	m := NewMockExecutor()
	m.Responses["kubectl create ns"] = ""
	m.Responses["kubectl label ns"] = ""
	m.Responses["kubectl delete ns"] = ""
	m.Responses["kubectl run dns-prober"] = ""
	m.Responses["kubectl wait --for=condition=Ready pod/dns-prober"] = "pod condition met"
	m.Responses["dns-prober -- apk add"] = "installed bind-tools"

	// Mock dig outputs
	m.Responses["dns-prober -- dig +tries=1 +timeout=2 kubernetes.default.svc.cluster.local"] = `
;; Got answer:
;; ->>HEADER<<- opcode: QUERY, status: NOERROR, id: 12345
;; flags: qr aa rd ra; QUERY: 1, ANSWER: 1, AUTHORITY: 0, ADDITIONAL: 0

;; QUESTION SECTION:
;kubernetes.default.svc.cluster.local. IN A

;; ANSWER SECTION:
kubernetes.default.svc.cluster.local. 30 IN A 10.96.0.1

;; Query time: 2 msec
;; SERVER: 10.96.0.10#53(10.96.0.10)
`
	m.Responses["dns-prober -- dig +tries=1 +timeout=2 google.com"] = `
;; Got answer:
;; ->>HEADER<<- opcode: QUERY, status: NOERROR, id: 54321
;; flags: qr rd ra; QUERY: 1, ANSWER: 1, AUTHORITY: 0, ADDITIONAL: 0

;; QUESTION SECTION:
;google.com.			IN	A

;; ANSWER SECTION:
google.com.		258	IN	A	142.250.74.46

;; Query time: 15 msec
;; SERVER: 10.96.0.10#53(10.96.0.10)
`

	collector := NewCollector(m)
	collector.BenchDNS = true

	var progressMessages []string
	collector.OnProgress = func(msg string) {
		progressMessages = append(progressMessages, msg)
	}

	report := &DetectReport{
		Nodes: []NodeInfo{
			{Name: "node-1", Zone: "zone-a"},
		},
	}

	collector.runDNSResolutionProbe(context.Background(), report)

	if len(report.Network.DNSResults) != 2 {
		t.Fatalf("Expected 2 DNS probe results, got %d", len(report.Network.DNSResults))
	}

	res1 := report.Network.DNSResults[0]
	if res1.QueryType != "Internal" || res1.QueriesRun != 20 || res1.SuccessCount != 20 || res1.SuccessRate != 100.0 || res1.AvgLatencyMs != 2.0 || res1.MaxLatencyMs != 2.0 {
		t.Errorf("Unexpected DNS internal result: %+v", res1)
	}

	res2 := report.Network.DNSResults[1]
	if res2.QueryType != "External" || res2.QueriesRun != 20 || res2.SuccessCount != 20 || res2.SuccessRate != 100.0 || res2.AvgLatencyMs != 15.0 || res2.MaxLatencyMs != 15.0 {
		t.Errorf("Unexpected DNS external result: %+v", res2)
	}

	if len(progressMessages) == 0 {
		t.Error("Expected progress messages to be sent, but got none")
	}
}
