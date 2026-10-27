package detect

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
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
	m.Set("namespace created", "kubectl create -f")
	m.Set("namespace created", "kubectl create ns")
	m.Set("namespace labeled", "kubectl label ns")
	m.Set("namespace deleted", "kubectl delete ns")
	m.Set("secret created", "kubectl create secret")

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

// The zones came out of a map, so two runs of detect listed them in two
// orders; the report, the values file and every renderer read them in order.
func TestGroupNodesByZone_SortsZones(t *testing.T) {
	nodes := []NodeInfo{
		{Name: "n1", Zone: "us-east-1d", CPU: 1000, MemoryGi: 4},
		{Name: "n2", Zone: "us-east-1b", CPU: 1000, MemoryGi: 4},
		{Name: "n3", Zone: "us-east-1a", CPU: 1000, MemoryGi: 4},
		{Name: "n4", Zone: "us-east-1c", CPU: 1000, MemoryGi: 4},
		{Name: "n5", Zone: "us-east-1a", CPU: 1000, MemoryGi: 4},
		{Name: "n6", Zone: "-", CPU: 1000, MemoryGi: 4},
	}
	want := []string{"us-east-1a", "us-east-1b", "us-east-1c", "us-east-1d"}
	// A few runs: map order is random, so one lucky pass proves nothing.
	for run := 0; run < 5; run++ {
		zones := NewCollector(NewMockExecutor()).groupNodesByZone(nodes)
		var got []string
		for _, z := range zones {
			got = append(got, z.Name)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("zones = %v, want %v", got, want)
		}
		if zones[0].Nodes != 2 || zones[0].CPUAllocatable != 2000 || zones[0].MemAllocatableGi != 8 {
			t.Fatalf("us-east-1a = %+v, want 2 nodes, 2000m, 8Gi", zones[0])
		}
	}
}

// Allocatable memory arrives as Ki from most kubelets, but a node can report
// Mi or Gi; parseNodes read everything as Ki and made the others 0.
func TestParseNodes_MemoryUnits(t *testing.T) {
	m := NewMockExecutor()
	m.Set(`{"items": [
	  {"metadata": {"name": "ki"}, "status": {"allocatable": {"cpu": "4", "memory": "16777216Ki"}}},
	  {"metadata": {"name": "mi"}, "status": {"allocatable": {"cpu": "4", "memory": "16384Mi"}}},
	  {"metadata": {"name": "gi"}, "status": {"allocatable": {"cpu": "4", "memory": "16Gi"}}},
	  {"metadata": {"name": "plain"}, "status": {"allocatable": {"cpu": "4", "memory": "17179869184"}}}
	]}`, "kubectl", "get", "nodes", "-o", "json")

	nodes, err := NewCollector(m).parseNodes()
	if err != nil {
		t.Fatalf("parseNodes: %v", err)
	}
	if len(nodes) != 4 {
		t.Fatalf("got %d nodes, want 4", len(nodes))
	}
	for _, n := range nodes {
		if n.MemoryGi != 16 {
			t.Errorf("node %s: MemoryGi = %d, want 16", n.Name, n.MemoryGi)
		}
		if n.CPU != 4000 {
			t.Errorf("node %s: CPU = %d, want 4000", n.Name, n.CPU)
		}
	}
}

// The internal DNS probe digs the cluster's own domain, the one stage 1 read
// from CoreDNS, not cluster.local on every cluster.
func TestDNSResolutionProbe_UsesTheClusterDomain(t *testing.T) {
	m := NewMockExecutor()
	m.Responses["kubectl create -f"] = ""
	m.Responses["kubectl label ns"] = ""
	m.Responses["kubectl delete ns"] = ""
	m.Responses["kubectl run dns-prober"] = ""
	m.Responses["kubectl wait --for=condition=Ready pod/dns-prober"] = "pod condition met"
	m.Responses["dns-prober -- apk add"] = "installed bind-tools"
	m.Responses["dns-prober -- dig"] = ";; Query time: 2 msec"

	collector := NewCollector(m)
	collector.BenchDNS = true
	report := &DetectReport{
		Nodes:   []NodeInfo{{Name: "node-1", Zone: "zone-a"}},
		Network: NetworkInfo{ClusterDomain: "taz-tls-int-1.local"},
	}
	collector.runDNSResolutionProbe(context.Background(), report)

	digs := 0
	for _, call := range m.Calls() {
		if !strings.Contains(call, "dig ") {
			continue
		}
		if strings.Contains(call, "kubernetes.default.svc.") {
			digs++
			if !strings.HasSuffix(call, "kubernetes.default.svc.taz-tls-int-1.local") {
				t.Fatalf("internal dig %q, want the cluster domain taz-tls-int-1.local", call)
			}
		}
	}
	if digs != 20 {
		t.Errorf("%d internal digs, want 20", digs)
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

// The lookup read any "error" in the JSON of every deployment as a failed
// kubectl, so one --log-level=error argument anywhere sent it to namespace
// kafka, and an operator in strimzi-system went unseen.
func TestGetStrimziStatus_FindsTheOperatorBesideAnErrorLogLevel(t *testing.T) {
	m := NewMockExecutor()
	m.Set(`{"items":[
	  {"metadata":{"name":"ingress-nginx-controller","namespace":"ingress"},
	   "spec":{"template":{"spec":{"containers":[{"image":"ingress-nginx:1.12","args":["--log-level=error"]}]}}}},
	  {"metadata":{"name":"strimzi-cluster-operator","namespace":"strimzi-system"},
	   "spec":{"template":{"spec":{"containers":[{"image":"quay.io/strimzi/operator:0.48.0"}]}}},
	   "status":{"readyReplicas":1,"replicas":1}}]}`,
		"kubectl", "get", "deployment", "-A", "-o", "json")

	info := NewCollector(m).getStrimziStatus()
	if !info.Running || info.Namespace != "strimzi-system" || info.Image != "quay.io/strimzi/operator:0.48.0" {
		t.Errorf("Running %v in %q with image %q, want the operator in strimzi-system", info.Running, info.Namespace, info.Image)
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
	m.Set("namespace created", "kubectl create -f")
	m.Set("namespace deleted", "kubectl delete ns")

	// Kyverno denial response output
	kyvernoDenialMsg := `Error from server (Forbidden): admission webhook "validate.kyverno.svc-fail" denied the request: 
policy generic-secret-blocked-by-kyverno.rules.restrict-secrets: Secret creation is blocked in this namespace.`

	// The denial is on kubectl's stderr, which OSExecutor wraps in the error
	// it returns.
	m.Set("", "kubectl create secret")
	m.SetError(fmt.Errorf("%w: %s", errors.New("exit status 1"), kyvernoDenialMsg), "kubectl create secret")

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
	if audit.ErrorMsg != kyvernoDenialMsg {
		t.Errorf("ErrorMsg = %q, want kubectl's message without the exit status: %q", audit.ErrorMsg, kyvernoDenialMsg)
	}
}

// A collector pinned to a kube context reports that context, and the provider
// it implies, rather than the kubeconfig's current one: `kubectl config
// current-context` ignores --context.
func TestCollect_ReportsTheContextItIsPinnedTo(t *testing.T) {
	m := setupHealthyMock()
	m.Set("prod-eu", "kubectl", "config", "current-context")

	c := NewCollector(m)
	c.KubeContext = "kind-panda"
	report, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if report.Context != "kind-panda" || report.Provider != "kind" {
		t.Errorf("report names context %q (provider %q), want kind-panda (kind)", report.Context, report.Provider)
	}
}

func TestGetNetworkStatus_ServiceCIDRFromTheAPIServerCommand(t *testing.T) {
	for _, tt := range []struct {
		name, command, want string
	}{
		{"kubeadm", `["kube-apiserver","--advertise-address=172.18.0.3","--service-cluster-ip-range=10.96.0.0/16","--tls-cert-file=/etc/kubernetes/pki/apiserver.crt"]`, "10.96.0.0/16"},
		{"dual stack keeps the first", `["kube-apiserver","--service-cluster-ip-range=10.96.0.0/16,fd00:10:96::/112"]`, "10.96.0.0/16"},
		{"last flag", `["kube-apiserver","--service-cluster-ip-range=10.43.0.0/16"]`, "10.43.0.0/16"},
		{"no flag", `["kube-apiserver","--secure-port=6443"]`, "unknown"},
		{"no API server pod", "", "unknown"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := NewMockExecutor()
			m.Set(tt.command, "kubectl", "get", "pod", "-n", "kube-system", "-l", "component=kube-apiserver",
				"-o", "jsonpath={.items[0].spec.containers[0].command}")
			if got := NewCollector(m).getNetworkStatus().ServiceCIDR; got != tt.want {
				t.Errorf("ServiceCIDR = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseCoreDNSClusterDomain(t *testing.T) {
	corefile := `. {
    errors
    kubernetes taz-tls-int-1.local in-addr.arpa ip6.arpa {
        pods insecure
    }
}`
	if got := parseCoreDNSClusterDomain(corefile); got != "taz-tls-int-1.local" {
		t.Fatalf("parseCoreDNSClusterDomain = %q, want taz-tls-int-1.local", got)
	}
}

func TestGetNetworkStatus_PrefersCoreDNSClusterDomain(t *testing.T) {
	m := NewMockExecutor()
	m.Set(`.:53 {
    kubernetes taz-tls-int-1.local in-addr.arpa ip6.arpa {
        pods insecure
    }
}`, "kubectl", "get", "configmap", "-n", "kube-system", "coredns", "-o", "jsonpath={.data.Corefile}")
	m.Set("default misleading-pod", "kubectl", "get", "pods", "--all-namespaces", "--field-selector=status.phase=Running",
		"-o", "jsonpath={.items[0].metadata.namespace} {.items[0].metadata.name}")
	m.Set("search default.svc.cluster.local svc.cluster.local cluster.local", "kubectl", "exec", "-n", "default", "misleading-pod", "--", "cat", "/etc/resolv.conf")

	got := NewCollector(m).getNetworkStatus().ClusterDomain
	if got != "taz-tls-int-1.local" {
		t.Fatalf("ClusterDomain = %q, want taz-tls-int-1.local", got)
	}
}

func TestParseResolvConfClusterDomain(t *testing.T) {
	resolv := `search kafka.svc.taz-tls-int-1.local svc.taz-tls-int-1.local taz-tls-int-1.local
nameserver 100.64.224.10`
	if got := parseResolvConfClusterDomain(resolv); got != "taz-tls-int-1.local" {
		t.Fatalf("parseResolvConfClusterDomain = %q, want taz-tls-int-1.local", got)
	}
}

// mutatingVerbs are the kubectl verbs that create, change or delete an object
// in the cluster. `kubectl auth can-i create` is a question, and its verb is
// auth.
var mutatingVerbs = map[string]bool{
	"create": true, "run": true, "label": true, "annotate": true,
	"delete": true, "apply": true, "patch": true, "replace": true,
	"scale": true, "set": true, "expose": true, "taint": true,
	"cordon": true, "drain": true, "edit": true,
}

// kubectlVerb finds the verb of every kubectl in a command, including the
// ones inside an `sh -c` script, which is how the storage bench applies its
// PVC.
var kubectlVerb = regexp.MustCompile(`\bkubectl\s+([a-z-]+)`)

// mutatingCalls returns the calls that would write to the cluster.
func mutatingCalls(calls []string) []string {
	var out []string
	for _, c := range calls {
		for _, m := range kubectlVerb.FindAllStringSubmatch(c, -1) {
			if mutatingVerbs[m[1]] {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// setupWriteProbeMock is the healthy three-zone cluster, with every write
// probe reachable: three zones for the latency matrix, and no running pod to
// read resolv.conf from, so the cluster-domain lookup falls back to its
// dns-detect pod. The healthy mock's "kubectl get pods" pattern would
// otherwise answer the running-pod query.
func setupWriteProbeMock() *MockExecutor {
	m := setupHealthyMock()
	delete(m.Responses, "kubectl get pods")
	return m
}

func containsCall(calls []string, parts ...string) bool {
	for _, c := range calls {
		found := true
		for _, p := range parts {
			if !strings.Contains(c, p) {
				found = false
				break
			}
		}
		if found {
			return true
		}
	}
	return false
}

// The control for the read-only test below: on the same mock, an ordinary
// collection does issue every write the read-only one must not, so that
// test's "no writes" cannot pass by never reaching the probes.
func TestCollect_WriteProbesCreateThings(t *testing.T) {
	m := setupWriteProbeMock()
	c := NewCollector(m)
	c.BenchStorage, c.BenchNetwork, c.BenchDNS = true, true, true

	if _, err := c.Collect(context.Background()); err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	writes := mutatingCalls(m.Calls())
	for _, want := range []struct {
		probe string
		parts []string
	}{
		{"secret audit namespace", []string{"kubectl delete ns kates-detect-secrets-"}},
		{"secret audit Secret", []string{"kubectl create secret generic kates-detect-test-sec"}},
		{"latency namespace", []string{"kubectl delete ns kates-detect-latency-"}},
		{"probe namespace manifests", []string{"kubectl create -f ", "kates-detect-namespace-"}},
		{"latency prober pods", []string{"kubectl run prober-"}},
		{"dns-detect fallback pod", []string{"kubectl run", "dns-detect"}},
		{"namespace cleanup", []string{"kubectl delete ns"}},
	} {
		if !containsCall(writes, want.parts...) {
			t.Errorf("%s: no call containing %q among the writes:\n  %s",
				want.probe, want.parts, strings.Join(writes, "\n  "))
		}
	}
	// Every probe creates its namespace from a manifest: secret, latency and
	// the three benches.
	var namespaces int
	for _, w := range writes {
		if strings.HasPrefix(w, "kubectl create -f ") {
			namespaces++
		}
	}
	if namespaces != 5 {
		t.Errorf("created %d namespaces, want 5 (secret, latency and three benches)", namespaces)
	}
}

// manifestExecutor answers as its MockExecutor does, and keeps every file
// `kubectl create -f` is given, read before the probe removes it.
type manifestExecutor struct {
	*MockExecutor
	lock      sync.Mutex
	manifests map[string]string
}

func (e *manifestExecutor) Exec(name string, args ...string) (string, error) {
	if name == "kubectl" && len(args) == 3 && args[0] == "create" && args[1] == "-f" {
		data, err := os.ReadFile(args[2])
		if err != nil {
			return "", err
		}
		e.lock.Lock()
		e.manifests[args[2]] = string(data)
		e.lock.Unlock()
	}
	return e.MockExecutor.Exec(name, args...)
}

// A policy that requires labels on every namespace refuses a bare one, so
// the secret and latency probes create theirs in one call that already
// carries the user's labels, under the probe's own, which win a clash.
func TestCollect_ProbeNamespacesCarryTheUserLabels(t *testing.T) {
	m := &manifestExecutor{MockExecutor: setupWriteProbeMock(), manifests: map[string]string{}}
	c := NewCollector(m)
	c.BenchStorage, c.BenchNetwork, c.BenchDNS = true, true, true
	c.NamespaceLabels = map[string]string{
		"gov.example.com/product-id":         "KATES1",
		"kates-detect-experimental":          "no",
		"pod-security.kubernetes.io/enforce": "restricted",
	}
	if _, err := c.Collect(context.Background()); err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	if len(m.manifests) != 5 {
		t.Fatalf("want one manifest per probe namespace, got %d: %v", len(m.manifests), m.manifests)
	}
	for _, prefix := range []string{"kates-detect-secrets-", "kates-detect-latency-", "kates-detect-bench-", "kates-detect-bandwidth-", "kates-detect-dns-"} {
		var manifest string
		for _, mf := range m.manifests {
			if strings.Contains(mf, "name: "+prefix) {
				manifest = mf
			}
		}
		if manifest == "" {
			t.Errorf("no manifest creates a %s namespace: %v", prefix, m.manifests)
			continue
		}
		for _, want := range []string{
			"kind: Namespace",
			`"gov.example.com/product-id": "KATES1"`,
			`"kates-detect-experimental": "true"`,
			`"kates-detect-run": "`,
		} {
			if !strings.Contains(manifest, want) {
				t.Errorf("the %s manifest lacks %q:\n%s", prefix, want, manifest)
			}
		}
		// The probes' pods are not restricted-ready; a Pod Security label
		// would have them refused.
		if strings.Contains(manifest, "pod-security.kubernetes.io") {
			t.Errorf("the %s manifest carries a Pod Security label:\n%s", prefix, manifest)
		}
	}
	for _, call := range m.Calls() {
		if strings.HasPrefix(call, "kubectl label ns kates-detect-") || strings.HasPrefix(call, "kubectl create ns ") {
			t.Errorf("a probe namespace was created bare or labelled after its creation: %s", call)
		}
	}
	for path := range m.manifests {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the manifest %s was left behind: %v", path, err)
		}
	}
}

// kates deploy --dry-run collects read-only: whatever it previews, it must
// not create, label or delete anything to do it, benches included.
func TestCollect_ReadOnlyIssuesNoWrites(t *testing.T) {
	m := setupWriteProbeMock()
	c := NewCollector(m)
	c.ReadOnly = true
	c.BenchStorage, c.BenchNetwork, c.BenchDNS = true, true, true

	report, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect failed: %v", err)
	}

	if writes := mutatingCalls(m.Calls()); len(writes) > 0 {
		t.Errorf("a read-only collection wrote to the cluster:\n  %s", strings.Join(writes, "\n  "))
	}

	// The reads still ran...
	if len(report.Nodes) != 3 || !report.Strimzi.Running {
		t.Errorf("reads are missing: %d nodes, Strimzi running %v", len(report.Nodes), report.Strimzi.Running)
	}
	// ...and what only a write probe could fill stays empty.
	if report.SecretAudit != (SecretCreationAudit{}) {
		t.Errorf("SecretAudit = %+v, want it empty", report.SecretAudit)
	}
	if report.Network.LatencyMatrix != nil || report.Network.BandwidthMatrix != nil || report.Network.DNSResults != nil {
		t.Errorf("network probes filled the report: latency %v, bandwidth %v, dns %v",
			report.Network.LatencyMatrix, report.Network.BandwidthMatrix, report.Network.DNSResults)
	}
	if report.Network.ClusterDomain != "cluster.local" {
		t.Errorf("ClusterDomain = %q, want the cluster.local default", report.Network.ClusterDomain)
	}
}

// Each container's request was cut to whole GiB, so 256Mi counted as 0, and
// 100 pods of two 256Mi containers requested nothing: the capacity check said
// Sufficient, and broker sizing took that memory as free.
func TestGetWorkloadPressure_SumsMemoryBeforeRounding(t *testing.T) {
	pod := func(node string) string {
		return `{"metadata":{"namespace":"apps"},"spec":{"nodeName":"` + node + `","containers":[` +
			`{"resources":{"requests":{"cpu":"100m","memory":"256Mi"}}},` +
			`{"resources":{"requests":{"cpu":"100m","memory":"256Mi"}}}]}}`
	}
	pods := make([]string, 100)
	for i := range pods {
		pods[i] = pod(fmt.Sprintf("node-%d", i%2))
	}
	m := NewMockExecutor()
	m.Set(`{"items":[`+strings.Join(pods, ",")+`]}`,
		"kubectl", "get", "pods", "-A", "-o", "json", "--field-selector=status.phase=Running")

	wp := NewCollector(m).getWorkloadPressure()
	if wp.TotalPods != 100 || wp.TotalMemRequests != 50 {
		t.Errorf("%d pods requesting %d GiB, want 100 pods requesting 50 GiB", wp.TotalPods, wp.TotalMemRequests)
	}
	for _, node := range wp.PerNode {
		if node.MemRequested != 25 {
			t.Errorf("%s requests %d GiB, want 25", node.Name, node.MemRequested)
		}
	}
}

func TestMemoryMiB(t *testing.T) {
	for quantity, want := range map[string]float64{
		"256Mi":     256,
		"2Gi":       2048,
		"0.5Gi":     512,
		"524288Ki":  512,
		"268435456": 256,
		"1Ti":       1 << 20,
		"1G":        1e9 / (1 << 20),
		"500M":      500e6 / (1 << 20),
		"":          0,
		"lots":      0,
	} {
		if got := memoryMiB(quantity); got != want {
			t.Errorf("memoryMiB(%q) = %v, want %v", quantity, got, want)
		}
	}
}

func TestPlatformOf(t *testing.T) {
	aks := "azure:///subscriptions/s/resourceGroups/MC_rg_c_westeurope/providers/Microsoft.Compute/virtualMachineScaleSets/aks-nodepool1-123-vmss/virtualMachines/0"
	cases := []struct {
		name, gitVersion string
		apis             []string
		providerID, ctx  string
		want, basis      string
	}{
		{"openshift wins over its cloud", "v1.29.6+abc", []string{"v1", "config.openshift.io/v1"}, "aws:///eu-west-1a/i-1", "prod", "openshift", "API group config.openshift.io"},
		{"eks from the version on AWS nodes", "v1.30.4-eks-a737599", nil, "aws:///eu-west-1a/i-1", "prod", "eks", "server version v1.30.4-eks-a737599 on aws:// nodes"},
		{"gke from the version on GCE nodes", "v1.30.3-gke.1639000", nil, "gce://p/europe-west1-b/gke-c-pool-1", "", "gke", "server version v1.30.3-gke.1639000 on gce:// nodes"},
		{"EKS Anywhere off AWS is not eks", "v1.28.15-eks-1-28-47", nil, "vsphere://4201", "mgmt-admin@mgmt", "generic", "no platform signal"},
		{"Google Distributed Cloud on bare metal is not gke", "v1.30.3-gke.1", nil, "", "admin", "generic", "no platform signal"},
		{"k3s from the version", "v1.29.4+k3s1", nil, "", "default", "k3s", "server version v1.29.4+k3s1"},
		{"rke2 from the version", "v1.28.9+rke2r1", nil, "", "default", "rke2", "server version v1.28.9+rke2r1"},
		{"aks from its scale set", "v1.29.4", nil, aks, "prod", "aks", "node providerID, an AKS scale set"},
		{"kind from the node, whatever the context", "v1.31.0", nil, "kind://docker/panda/panda-control-plane", "lab", "kind", "node providerID kind://"},
		{"a self-built AWS cluster is not eks", "v1.30.1", nil, "aws:///eu-west-1a/i-1", "prod", "generic", "no platform signal"},
		{"context name last", "v1.30.1", nil, "", "kind-panda", "kind", "context name, guessed"},
		{"nothing at all", "", nil, "", "", "generic", "no platform signal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, basis := platformOf(tc.gitVersion, tc.apis, []NodeInfo{{ProviderID: tc.providerID}}, tc.ctx)
			if got != tc.want || basis != tc.basis {
				t.Errorf("platformOf = %q (%q), want %q (%q)", got, basis, tc.want, tc.basis)
			}
		})
	}
}

func TestPlatform_ReadsOnlyWhatItNeeds(t *testing.T) {
	m := NewMockExecutor()
	m.Set(`{"serverVersion":{"major":"1","minor":"30","gitVersion":"v1.30.4-eks-a737599"}}`, "kubectl", "version", "-o", "json")
	m.Set("v1\napps/v1", "kubectl", "api-versions")
	m.Set(`{"items":[{"metadata":{"name":"ip-10-0-1-1"},"spec":{"providerID":"aws:///eu-west-1a/i-1"}}]}`, "kubectl", "get", "nodes", "-o", "json")
	if p, basis, v := Platform(m, "prod"); p != "eks" || basis != "server version v1.30.4-eks-a737599 on aws:// nodes" || v != "v1.30.4-eks-a737599" {
		t.Errorf("Platform = %q, %q, %q", p, basis, v)
	}

	// An admin-less account may not list nodes: the version and the context still count.
	m.Errors["kubectl get nodes"] = errors.New("forbidden")
	if p, basis, _ := Platform(m, "kind-panda"); p != "kind" || basis != "context name, guessed" {
		t.Errorf("without nodes: Platform = %q, %q", p, basis)
	}
}
