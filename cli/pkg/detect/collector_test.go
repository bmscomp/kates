package detect

import (
	"context"
	"fmt"
	"testing"
)

const healthyNodesJSON = `{
  "items": [
    {
      "metadata": {"name": "node-1", "labels": {"topology.kubernetes.io/zone": "us-east-1a", "node-role.kubernetes.io/worker": ""}},
      "status": {
        "allocatable": {"cpu": "4000m", "memory": "16Gi"},
        "nodeInfo": {"containerRuntimeVersion": "containerd://1.7", "kubeletVersion": "v1.31.0", "operatingSystem": "linux"}
      }
    },
    {
      "metadata": {"name": "node-2", "labels": {"topology.kubernetes.io/zone": "us-east-1b", "node-role.kubernetes.io/worker": ""}},
      "status": {
        "allocatable": {"cpu": "4000m", "memory": "16Gi"},
        "nodeInfo": {"containerRuntimeVersion": "containerd://1.7", "kubeletVersion": "v1.31.0", "operatingSystem": "linux"}
      }
    },
    {
      "metadata": {"name": "node-3", "labels": {"topology.kubernetes.io/zone": "us-east-1c", "node-role.kubernetes.io/worker": ""}},
      "status": {
        "allocatable": {"cpu": "4000m", "memory": "16Gi"},
        "nodeInfo": {"containerRuntimeVersion": "containerd://1.7", "kubeletVersion": "v1.31.0", "operatingSystem": "linux"}
      }
    }
  ]
}`

const healthySCJSON = `{
  "items": [
    {
      "metadata": {"name": "gp3", "annotations": {"storageclass.kubernetes.io/is-default-class": "true"}},
      "provisioner": "ebs.csi.aws.com",
      "volumeBindingMode": "WaitForFirstConsumer",
      "reclaimPolicy": "Delete"
    }
  ]
}`

const strimziCRDFound = "kafkas.kafka.strimzi.io"

const strimziDeploymentJSON = `{
  "items": [
    {
      "metadata": {"name": "strimzi-cluster-operator", "namespace": "kafka"},
      "spec": {"template": {"spec": {"containers": [{"image": "quay.io/strimzi/operator:1.0.0"}]}}},
      "status": {"readyReplicas": 1, "replicas": 1}
    }
  ]
}`

const emptyListJSON = `{"items": []}`

func setupHealthyMock() *MockExecutor {
	m := NewMockExecutor()
	m.Set("kind-panda", "kubectl", "config", "current-context")
	m.Set("https://127.0.0.1:6443", "kubectl", "config", "view", "--minify", "-o", "jsonpath={.clusters[0].cluster.server}")
	m.Set("ok", "kubectl", "cluster-info")
	m.Set("v1.31.2", "kubectl", "version", "--short", "--client=false")
	m.Set("v3.15.0", "helm", "version", "--short")
	m.Set(healthyNodesJSON, "kubectl", "get", "nodes", "-o", "json")
	m.Set(healthySCJSON, "kubectl", "get", "sc", "-o", "json")
	m.Set(strimziCRDFound, "kubectl", "get", "crd", "kafkas.kafka.strimzi.io")
	m.Set(strimziDeploymentJSON, "kubectl", "get", "deployment", "-A", "-o", "json")
	m.Set(emptyListJSON, "kubectl", "get", "kafka", "-n", "kafka", "--no-headers")
	m.Set(emptyListJSON, "kubectl", "get", "kafkanodepools", "-n", "kafka", "--no-headers")
	m.Set(emptyListJSON, "kubectl", "get", "kafkatopics", "-n", "kafka", "--no-headers")
	m.Set(emptyListJSON, "kubectl", "get", "kafkausers", "-n", "kafka", "--no-headers")
	m.Set("", "kubectl", "get", "pvc", "-n", "kafka", "--no-headers")
	m.Set("", "helm", "ls", "-n", "kafka", "--short")
	// Monitoring
	m.Set("podmonitors.monitoring.coreos.com", "kubectl", "get", "crd", "podmonitors.monitoring.coreos.com")
	m.Set("prometheusrules.monitoring.coreos.com", "kubectl", "get", "crd", "prometheusrules.monitoring.coreos.com")
	// Network
	m.Set("10.244.0.0/24", "kubectl", "get", "nodes", "-o", "jsonpath={.items[0].spec.podCIDR}")
	// NetPol audit
	m.Set(emptyListJSON, "kubectl", "get", "networkpolicy", "-n", "kafka", "-o", "json")
	// Admission
	m.Set(emptyListJSON, "kubectl", "get", "clusterpolicy", "-o", "json")
	// RBAC
	m.Set("yes", "kubectl", "auth", "can-i", "create", "deployments", "-n", "kafka")
	m.Set("yes", "kubectl", "auth", "can-i", "create", "statefulsets", "-n", "kafka")
	m.Set("yes", "kubectl", "auth", "can-i", "create", "configmaps", "-n", "kafka")
	m.Set("yes", "kubectl", "auth", "can-i", "create", "secrets", "-n", "kafka")
	m.Set("yes", "kubectl", "auth", "can-i", "create", "services", "-n", "kafka")
	m.Set("yes", "kubectl", "auth", "can-i", "create", "persistentvolumeclaims", "-n", "kafka")
	// New Strimzi checks mocks
	m.Set("kafkanodepools.kafka.strimzi.io", "kubectl", "get", "crd", "kafkanodepools.kafka.strimzi.io", "--no-headers")
	m.Set("kafkatopics.kafka.strimzi.io", "kubectl", "get", "crd", "kafkatopics.kafka.strimzi.io", "--no-headers")
	m.Set("kafkausers.kafka.strimzi.io", "kubectl", "get", "crd", "kafkausers.kafka.strimzi.io", "--no-headers")
	m.Set(`{"items": []}`, "kubectl", "get", "pods", "-A", "-l", "name=strimzi-cluster-operator", "-o", "json")
	m.Set(`{"items": []}`, "kubectl", "get", "pods", "-n", "kafka", "-l", "name=strimzi-cluster-operator", "-o", "json")

	// Secrets check mocks
	m.Set("namespace created", "kubectl create ns")
	m.Set("namespace labeled", "kubectl label ns")
	m.Set("namespace deleted", "kubectl delete ns")
	m.Set("secret created", "sh -c")

	// Latency matrix check mocks
	m.Set("pod run", "kubectl run")
	m.Set("pod ready", "kubectl wait")
	m.Set(`{"items": []}`, "kubectl get pods")
	m.Set("ping success", "kubectl exec")

	return m
}

func TestCollect_HealthyCluster(t *testing.T) {
	m := setupHealthyMock()
	c := NewCollector(m)

	report, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	if len(report.Nodes) != 3 {
		t.Errorf("expected 3 nodes, got %d", len(report.Nodes))
	}
	if len(report.Zones) != 3 {
		t.Errorf("expected 3 zones, got %d", len(report.Zones))
	}
	if !report.Strimzi.Running {
		t.Error("expected Strimzi to be running")
	}
	if report.Strimzi.Image != "quay.io/strimzi/operator:1.0.0" {
		t.Errorf("expected strimzi image, got %q", report.Strimzi.Image)
	}
	if len(report.Storage) != 1 {
		t.Errorf("expected 1 storage class, got %d", len(report.Storage))
	}
	if !report.Storage[0].IsDefault {
		t.Error("expected gp3 to be default")
	}
}

func TestCollect_NoStrimzi(t *testing.T) {
	m := setupHealthyMock()
	// Override: Strimzi CRD not found
	m.Set("Error from server (NotFound)", "kubectl", "get", "crd", "kafkas.kafka.strimzi.io")
	// Override: no strimzi in deployments
	m.Set(emptyListJSON, "kubectl", "get", "deployment", "-A", "-o", "json")

	c := NewCollector(m)
	report, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	if report.Strimzi.Running {
		t.Error("expected Strimzi NOT running")
	}
	if report.Strimzi.CRDsPresent {
		t.Error("expected Strimzi CRDs NOT present")
	}
}

func TestCollect_SingleZone(t *testing.T) {
	singleNodeJSON := `{
  "items": [
    {
      "metadata": {"name": "node-1", "labels": {"topology.kubernetes.io/zone": "us-east-1a"}},
      "status": {
        "allocatable": {"cpu": "4000m", "memory": "16Gi"},
        "nodeInfo": {"containerRuntimeVersion": "containerd://1.7", "kubeletVersion": "v1.31.0", "operatingSystem": "linux"}
      }
    }
  ]
}`
	m := setupHealthyMock()
	m.Set(singleNodeJSON, "kubectl", "get", "nodes", "-o", "json")

	c := NewCollector(m)
	report, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	if len(report.Zones) != 1 {
		t.Errorf("expected 1 zone, got %d", len(report.Zones))
	}
}

func TestParsePingOutput(t *testing.T) {
	tests := []struct {
		name       string
		pingOut    string
		wantMin    float64
		wantAvg    float64
		wantMax    float64
		wantJitter float64
		wantOK     bool
	}{
		{
			name: "successful ping with jitter",
			pingOut: `PING 10.244.1.2 (10.244.1.2): 56 data bytes
64 bytes from 10.244.1.2: seq=0 ttl=64 time=0.081 ms
64 bytes from 10.244.1.2: seq=1 ttl=64 time=0.076 ms
64 bytes from 10.244.1.2: seq=2 ttl=64 time=0.065 ms
64 bytes from 10.244.1.2: seq=3 ttl=64 time=0.072 ms
64 bytes from 10.244.1.2: seq=4 ttl=64 time=0.070 ms

--- 10.244.1.2 ping statistics ---
5 packets transmitted, 5 packets received, 0% packet loss
round-trip min/avg/max/stddev = 0.065/0.072/0.081/0.005 ms`,
			wantMin:    0.065,
			wantAvg:    0.072,
			wantMax:    0.081,
			wantJitter: 0.005,
			wantOK:     true,
		},
		{
			name: "successful ping without jitter",
			pingOut: `PING 10.244.1.2 (10.244.1.2): 56 data bytes
64 bytes from 10.244.1.2: seq=0 ttl=64 time=1.20 ms

--- 10.244.1.2 ping statistics ---
1 packets transmitted, 1 packets received, 0% packet loss
round-trip min/avg/max = 1.20/1.20/1.20 ms`,
			wantMin:    1.20,
			wantAvg:    1.20,
			wantMax:    1.20,
			wantJitter: 0,
			wantOK:     true,
		},
		{
			name: "failed ping",
			pingOut: `PING 10.244.1.2 (10.244.1.2): 56 data bytes
Request timeout for icmp_seq 0

--- 10.244.1.2 ping statistics ---
5 packets transmitted, 0 packets received, 100% packet loss`,
			wantMin:    0,
			wantAvg:    0,
			wantMax:    0,
			wantJitter: 0,
			wantOK:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			min, avg, max, jitter, ok := parsePingOutput(tt.pingOut)
			if ok != tt.wantOK {
				t.Errorf("parsePingOutput() ok = %v, want %v", ok, tt.wantOK)
			}
			if min != tt.wantMin || avg != tt.wantAvg || max != tt.wantMax || jitter != tt.wantJitter {
				t.Errorf("parsePingOutput() = %v/%v/%v/%v, want %v/%v/%v/%v", min, avg, max, jitter, tt.wantMin, tt.wantAvg, tt.wantMax, tt.wantJitter)
			}
		})
	}
}

func TestCheckKafkaCapacity(t *testing.T) {
	c := &Collector{}

	tests := []struct {
		name       string
		nodes      []NodeInfo
		workload   WorkloadPressure
		reservePct float64
		want       string
	}{
		{
			name: "Sufficient capacity",
			nodes: []NodeInfo{
				{CPU: 4000, MemoryGi: 16},
				{CPU: 4000, MemoryGi: 16},
				{CPU: 4000, MemoryGi: 16},
			},
			workload: WorkloadPressure{
				TotalCPURequests: 2000,
				TotalMemRequests: 8,
			},
			reservePct: 0.30,
			want:       "Sufficient",
		},
		{
			name: "Insufficient capacity CPU deficit",
			nodes: []NodeInfo{
				{CPU: 2000, MemoryGi: 16},
				{CPU: 2000, MemoryGi: 16},
			},
			workload: WorkloadPressure{
				TotalCPURequests: 2000,
				TotalMemRequests: 4,
			},
			reservePct: 0.30,
			want:       "Insufficient: missing 100m CPU",
		},
		{
			name: "Insufficient capacity Memory deficit",
			nodes: []NodeInfo{
				{CPU: 4000, MemoryGi: 8},
				{CPU: 4000, MemoryGi: 8},
			},
			workload: WorkloadPressure{
				TotalCPURequests: 1000,
				TotalMemRequests: 10,
			},
			reservePct: 0.30,
			want:       "Insufficient: missing 2Gi Memory",
		},
		{
			name: "Insufficient capacity both CPU and Memory deficit",
			nodes: []NodeInfo{
				{CPU: 2000, MemoryGi: 8},
			},
			workload: WorkloadPressure{
				TotalCPURequests: 1000,
				TotalMemRequests: 6,
			},
			reservePct: 0.10,
			want:       "Insufficient: missing 600m CPU, 5Gi Memory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := &DetectReport{
				Nodes:    tt.nodes,
				Workload: tt.workload,
			}
			got := c.checkKafkaCapacity(report, tt.reservePct)
			if got != tt.want {
				t.Errorf("checkKafkaCapacity() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCheckSecretCreation_KyvernoDenial(t *testing.T) {
	m := NewMockExecutor()
	m.Set("namespace created", "kubectl create ns")
	m.Set("namespace labeled", "kubectl label ns")
	m.Set("namespace deleted", "kubectl delete ns")

	// Kyverno denial response output
	kyvernoDenialMsg := `Error from server (Forbidden): admission webhook "validate.kyverno.svc-fail" denied the request: 
policy generic-secret-blocked-by-kyverno.rules.restrict-secrets: Secret creation is blocked in this namespace.`

	m.Set(kyvernoDenialMsg, "sh -c")
	m.SetError(fmt.Errorf("denied by policy"), "sh -c")

	c := NewCollector(m)
	audit := c.checkSecretCreation(context.Background())

	if audit.SecretCreated {
		t.Error("expected secret creation to be blocked")
	}
	if !audit.BlockedByPolicy {
		t.Error("expected BlockedByPolicy to be true")
	}
	if audit.PolicyName != "generic-secret-blocked-by-kyverno" {
		t.Errorf("expected PolicyName 'generic-secret-blocked-by-kyverno', got %q", audit.PolicyName)
	}
}
