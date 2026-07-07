package cmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
)

func TestDeployStatusCommand(t *testing.T) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Reset flags before testing
	deployTopology = "single"
	deployNamespace = "kates-test-ns"
	deployStatusInteractive = false
	deployStatusOutput = "table"

	err := runDeployStatus(deployStatusCmd, []string{})

	w.Close()
	os.Stdout = oldStdout

	if err != nil {
		t.Fatalf("runDeployStatus failed: %v", err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	// Verify the banner is present
	if !strings.Contains(output, "Kates Deployment Status") {
		t.Errorf("Expected output to contain 'Kates Deployment Status', got: \n%s", output)
	}

	// Verify the expected namespace is populated.
	// In clusters where a release name is unique, deploy status resolves and shows the
	// discovered Helm namespace instead of the requested one.
	expectedNamespace := "kates-test-ns"
	if st, ns, ok := lookupHelmRelease(loadHelmReleaseIndex(context.Background()), "kates", expectedNamespace); ok && st == "deployed" {
		expectedNamespace = ns
	}
	if !strings.Contains(output, expectedNamespace) {
		t.Errorf("Expected output to contain namespace %q, got: \n%s", expectedNamespace, output)
	}

	// Verify some component groups are present
	if !strings.Contains(output, "Operators & CRDs") {
		t.Errorf("Expected output to contain 'Operators & CRDs'")
	}
}

func TestParsePodListHealth(t *testing.T) {
	tests := []struct {
		name           string
		jsonInput      string
		expectedHealth string
		expectedDetail string
	}{
		{
			name: "all pods healthy",
			jsonInput: `{
				"items": [
					{
						"metadata": { "name": "pod-1" },
						"status": {
							"phase": "Running",
							"containerStatuses": [
								{ "name": "c1", "ready": true, "restartCount": 0, "state": { "running": {} } }
							]
						}
					}
				]
			}`,
			expectedHealth: "Healthy",
			expectedDetail: "1/1 pods running & ready",
		},
		{
			name: "pod in crashloop",
			jsonInput: `{
				"items": [
					{
						"metadata": { "name": "pod-1" },
						"status": {
							"phase": "Running",
							"containerStatuses": [
								{ 
									"name": "c1", 
									"ready": false, 
									"restartCount": 5, 
									"state": { 
										"waiting": { "reason": "CrashLoopBackOff", "message": "back-off" } 
									} 
								}
							]
						}
					}
				]
			}`,
			expectedHealth: "Degraded",
			expectedDetail: "Pod pod-1: container c1 in CrashLoopBackOff",
		},
		{
			name: "pod container not ready",
			jsonInput: `{
				"items": [
					{
						"metadata": { "name": "pod-1" },
						"status": {
							"phase": "Running",
							"containerStatuses": [
								{ "name": "c1", "ready": false, "restartCount": 0, "state": { "waiting": { "reason": "ContainerCreating" } } }
							]
						}
					}
				]
			}`,
			expectedHealth: "Degraded",
			expectedDetail: "Pod pod-1: container c1 not ready (ContainerCreating)",
		},
		{
			name:           "no pods",
			jsonInput:      `{"items":[]}`,
			expectedHealth: "Degraded",
			expectedDetail: "No pods found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			health, detail, raw := parsePodListHealth([]byte(tt.jsonInput))
			if health != tt.expectedHealth {
				t.Errorf("expected health %q, got %q", tt.expectedHealth, health)
			}
			if !strings.Contains(detail, tt.expectedDetail) {
				t.Errorf("expected detail to contain %q, got %q", tt.expectedDetail, detail)
			}
			if tt.expectedHealth == "Healthy" && len(raw) != 1 {
				t.Errorf("expected 1 pod detail, got %d", len(raw))
			}
		})
	}
}

func TestParseKafkaHealth(t *testing.T) {
	tests := []struct {
		name           string
		jsonInput      string
		expectedHealth string
		expectedDetail string
	}{
		{
			name: "kafka is ready",
			jsonInput: `{
				"status": {
					"conditions": [
						{ "type": "Ready", "status": "True", "reason": "Ready", "message": "Kafka is ready" }
					]
				}
			}`,
			expectedHealth: "Healthy",
			expectedDetail: "kafka CRD is Ready",
		},
		{
			name: "kafka is not ready",
			jsonInput: `{
				"status": {
					"conditions": [
						{ "type": "Ready", "status": "False", "reason": "ReconciliationFailed", "message": "Failed to reconcile Kafka" }
					]
				}
			}`,
			expectedHealth: "Degraded",
			expectedDetail: "Failed to reconcile Kafka",
		},
		{
			name: "ready condition missing",
			jsonInput: `{
				"status": {
					"conditions": []
				}
			}`,
			expectedHealth: "Degraded",
			expectedDetail: "kafka CRD missing Ready condition",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			health, detail, raw := parseKafkaHealth([]byte(tt.jsonInput), "kafka")
			if health != tt.expectedHealth {
				t.Errorf("expected health %q, got %q", tt.expectedHealth, health)
			}
			if !strings.Contains(detail, tt.expectedDetail) {
				t.Errorf("expected detail to contain %q, got %q", tt.expectedDetail, detail)
			}
			if len(raw.Conditions) != len(strings.Split(tt.jsonInput, "type"))-1 {
				// quick estimate validation
				if len(raw.Conditions) == 0 && strings.Contains(tt.jsonInput, "Ready") {
					t.Errorf("expected conditions in raw output")
				}
			}
		})
	}
}

func TestParseWorkloadHealth(t *testing.T) {
	tests := []struct {
		name           string
		jsonInput      string
		expectedHealth string
		expectedDetail string
	}{
		{
			name: "replicas match",
			jsonInput: `{
				"spec": { "replicas": 3 },
				"status": { "replicas": 3, "readyReplicas": 3, "updatedReplicas": 3 }
			}`,
			expectedHealth: "Healthy",
			expectedDetail: "3/3 replicas ready",
		},
		{
			name: "replicas do not match",
			jsonInput: `{
				"spec": { "replicas": 3 },
				"status": { "replicas": 3, "readyReplicas": 2, "updatedReplicas": 3 }
			}`,
			expectedHealth: "Degraded",
			expectedDetail: "2/3 replicas ready",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			health, detail, raw := parseWorkloadHealth([]byte(tt.jsonInput))
			if health != tt.expectedHealth {
				t.Errorf("expected health %q, got %q", tt.expectedHealth, health)
			}
			if !strings.Contains(detail, tt.expectedDetail) {
				t.Errorf("expected detail to contain %q, got %q", tt.expectedDetail, detail)
			}
			if raw.Replicas != 3 {
				t.Errorf("expected spec replicas 3, got status replicas %d", raw.Replicas)
			}
		})
	}
}

func TestLookupHelmRelease(t *testing.T) {
	index := map[string][]helmStatusRelease{
		"monitoring": {
			{Name: "monitoring", Namespace: "monitoring", Status: "deployed"},
		},
		"kafka-ui": {
			{Name: "kafka-ui", Namespace: "kafka-ui", Status: "deployed"},
		},
		"shared": {
			{Name: "shared", Namespace: "ns-a", Status: "deployed"},
			{Name: "shared", Namespace: "ns-b", Status: "deployed"},
		},
	}

	t.Run("exact namespace match", func(t *testing.T) {
		status, ns, ok := lookupHelmRelease(index, "monitoring", "monitoring")
		if !ok {
			t.Fatal("expected exact release lookup to succeed")
		}
		if status != "deployed" || ns != "monitoring" {
			t.Fatalf("unexpected lookup result: status=%q ns=%q", status, ns)
		}
	})

	t.Run("unique release fallback to discovered namespace", func(t *testing.T) {
		status, ns, ok := lookupHelmRelease(index, "kafka-ui", "kafka")
		if !ok {
			t.Fatal("expected unique release fallback lookup to succeed")
		}
		if status != "deployed" || ns != "kafka-ui" {
			t.Fatalf("unexpected lookup result: status=%q ns=%q", status, ns)
		}
	})

	t.Run("ambiguous release without exact namespace", func(t *testing.T) {
		_, ns, ok := lookupHelmRelease(index, "shared", "missing-ns")
		if ok {
			t.Fatal("expected ambiguous release lookup to fail without exact namespace")
		}
		if ns != "missing-ns" {
			t.Fatalf("expected unresolved namespace to remain unchanged, got %q", ns)
		}
	})
}
