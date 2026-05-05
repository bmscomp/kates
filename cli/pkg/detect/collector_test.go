package detect

import (
	"context"
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
