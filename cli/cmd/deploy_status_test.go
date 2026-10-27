package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/bmscomp/kates/cli/internal/helm"
	"github.com/bmscomp/kates/cli/internal/proc"
)

// statusCluster answers deploy status's reads for a cluster where the kates
// release failed, a kafka-ui release lives in another namespace, and the
// Strimzi operator is healthy. Everything else is not found.
func statusCluster(t *testing.T) *proc.Fake {
	t.Helper()
	return stubRunner(t).
		On("helm list -A -a -o json", `[{"name":"kates","namespace":"kates-test-ns","status":"failed"},
			{"name":"kafka-ui","namespace":"other-ui","status":"deployed"},
			{"name":"strimzi-operator","namespace":"strimzi-operator","status":"deployed"}]`).
		Handle("kubectl get pods", func(c proc.Call) ([]byte, error) {
			if slices.Contains(c.Args, "strimzi-operator") {
				return []byte(`{"items":[{"metadata":{"name":"op-1"},"status":{"phase":"Running","containerStatuses":[{"name":"c","ready":true,"state":{"running":{}}}]}}]}`), nil
			}
			return nil, &proc.Error{Name: c.Name, Args: c.Args, Stderr: "Error from server (NotFound): not found", Err: errNotFoundExit}
		}).
		Fail("helm", "Error: release: not found").
		Fail("kubectl", "Error from server (NotFound): not found")
}

// errNotFoundExit stands in for kubectl's exit status in a Handle reply.
var errNotFoundExit = errors.New("exit status 1")

func TestDeployStatusCommand(t *testing.T) {
	fake := statusCluster(t)
	prevTopology, prevNS, prevMM2 := deployTopology, deployNamespace, deployMM2NS
	t.Cleanup(func() { deployTopology, deployNamespace, deployMM2NS = prevTopology, prevNS, prevMM2 })
	deployTopology = "single"
	deployNamespace = "kates-test-ns"
	deployMM2NS = "mm2-flag-value"
	deployStatusInteractive = false
	deployStatusOutput = "table"

	var err error
	output := captureStdout(t, func() { err = runDeployStatus(deployStatusCmd, []string{}) })
	if err != nil {
		t.Fatalf("runDeployStatus failed: %v", err)
	}

	if !strings.Contains(output, "Kates Deployment Status") || !strings.Contains(output, "Operators & CRDs") {
		t.Errorf("banner or groups missing:\n%s", output)
	}
	rows := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		for _, name := range []string{"Kates Backend", "Kafka UI", "Strimzi Operator", "MirrorMaker 2"} {
			if strings.Contains(line, name) {
				rows[name] = line
			}
		}
	}
	// The real status, not "deployed" because helm status exited 0.
	if !strings.Contains(rows["Kates Backend"], "failed") || !strings.Contains(rows["Kates Backend"], "Release is failed") {
		t.Errorf("a failed release must say so: %q", rows["Kates Backend"])
	}
	// A same-named release elsewhere is reported, not adopted: the row keeps
	// this deploy's namespace.
	if !strings.Contains(rows["Kafka UI"], "kates-test-ns") || !strings.Contains(rows["Kafka UI"], "elsewhere (other-ui)") || !strings.Contains(rows["Kafka UI"], "is in other-ui") {
		t.Errorf("a release in another namespace must be listed as elsewhere: %q", rows["Kafka UI"])
	}
	if !strings.Contains(rows["Strimzi Operator"], "deployed") || !strings.Contains(rows["Strimzi Operator"], "Healthy") {
		t.Errorf("the healthy operator: %q", rows["Strimzi Operator"])
	}
	// The single namespace is computed, not written into deploy's --mm2-ns.
	if !strings.Contains(rows["MirrorMaker 2"], "kates-test-ns") || deployMM2NS != "mm2-flag-value" {
		t.Errorf("deploy status must not mutate deployMM2NS (now %q): %q", deployMM2NS, rows["MirrorMaker 2"])
	}
	// The index is read once, first; a release it lacks is asked for alone,
	// in its namespace; only the deployed operator has its health checked.
	lines := fake.Lines()
	if len(lines) == 0 || lines[0] != "helm list -A -a -o json" {
		t.Errorf("the release index was not read first: %q", lines)
	}
	if !slices.Contains(lines, "helm -n kyverno status kyverno -o json") {
		t.Errorf("a release the index lacks was not asked for alone: %q", lines)
	}
	var kubectlCalls []string
	for _, l := range lines {
		if strings.HasPrefix(l, "kubectl ") {
			kubectlCalls = append(kubectlCalls, l)
		}
	}
	if want := []string{"kubectl get pods -l name=strimzi-cluster-operator -o json -n strimzi-operator"}; !slices.Equal(kubectlCalls, want) {
		t.Errorf("kubectl calls %q, want %q only", kubectlCalls, want)
	}

	// The JSON view runs the same lookups.
	deployStatusOutput = "json"
	t.Cleanup(func() { deployStatusOutput = "table" })
	var statuses []ComponentStatus
	data := captureStdout(t, func() { err = runDeployStatus(deployStatusCmd, []string{}) })
	if err != nil {
		t.Fatalf("json status: %v", err)
	}
	if err := json.Unmarshal([]byte(data), &statuses); err != nil {
		t.Fatalf("json status output: %v\n%s", err, data)
	}
	got := map[string]ComponentStatus{}
	for _, s := range statuses {
		got[s.Release] = s
	}
	if got["kates"].HelmStatus != "failed" || got["kafka-ui"].HelmStatus != "elsewhere (other-ui)" || got["kafka-ui"].Namespace != "kates-test-ns" || got["kyverno"].HelmStatus != "missing" {
		t.Errorf("json statuses: kates=%+v kafka-ui=%+v kyverno=%+v", got["kates"], got["kafka-ui"], got["kyverno"])
	}
}

// helm status exits 0 for a failed release; the status comes from its JSON.
func TestGetHelmStatus_ReadsTheReleaseStatus(t *testing.T) {
	fake := stubRunner(t).
		On("helm -n ns status broken", `{"name":"broken","info":{"status":"failed"}}`).
		On("helm -n ns status half", `{"name":"half","info":{"status":"pending-upgrade"}}`).
		On("helm -n ns status garbled", `<html>`).
		Fail("helm -n ns status unreachable", "Error: Kubernetes cluster unreachable").
		Fail("helm", "Error: release: not found")
	for release, want := range map[string]string{
		"broken": "failed", "half": "pending-upgrade", "none": "missing",
		"unreachable": "missing", "garbled": "unknown",
	} {
		if got := getHelmStatus(context.Background(), release, "ns"); got != want {
			t.Errorf("%s: status %q, want %q", release, got, want)
		}
	}
	for _, c := range fake.Lines() {
		if !strings.HasPrefix(c, "helm -n ns status ") || !strings.HasSuffix(c, " -o json") {
			t.Errorf("unexpected helm call %q", c)
		}
	}
}

// The health check says what kubectl said: a Strimzi resource that is absent
// reads "NotFound", not a bare exit status.
func TestGetHealthStatusV2ReportsKubectlsWords(t *testing.T) {
	stubRunner(t).
		Handle("kubectl get kafkas.kafka.strimzi.io krafter", func(c proc.Call) ([]byte, error) {
			out := `Error from server (NotFound): kafkas.kafka.strimzi.io "krafter" not found`
			return []byte(out + "\n"), &proc.Error{Name: c.Name, Args: c.Args, Stderr: out, Err: errNotFoundExit}
		}).
		On("kubectl get kafkaconnects.kafka.strimzi.io", `{"status":{"conditions":[{"type":"Ready","status":"True"}]}}`).
		On("kubectl get statefulset postgresql", `{"spec":{"replicas":1},"status":{"replicas":1,"readyReplicas":0}}`).
		Fail("kubectl", "Unable to connect to the server")
	ctx := context.Background()

	health, details, _ := getHealthStatusV2(ctx, "kafkas.kafka.strimzi.io", "krafter", "kafka")
	if health != "Unknown" || details != `Error from server (NotFound): kafkas.kafka.strimzi.io "krafter" not found` {
		t.Errorf("absent Kafka = %q, %q", health, details)
	}
	if health, details, _ := getHealthStatusV2(ctx, "kafkaconnects.kafka.strimzi.io", "connect-cluster", "connect"); health != "Healthy" {
		t.Errorf("ready Connect = %q, %q", health, details)
	}
	if health, details, _ := getHealthStatusV2(ctx, "statefulset", "postgresql", "database"); health != "Degraded" || details != "0/1 replicas ready" {
		t.Errorf("statefulset = %q, %q", health, details)
	}
	if health, details, _ := getHealthStatusV2(ctx, "pod", "-l app=x", "kafka"); health != "Unknown" || !strings.Contains(details, "Unable to connect") {
		t.Errorf("unreachable pods = %q, %q", health, details)
	}
	if health, details, _ := getHealthStatusV2(ctx, "service", "x", "kafka"); health != "Unknown" || details != "no health check for kind service" {
		t.Errorf("unknown kind = %q, %q", health, details)
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

// The bug this guards: the health check named the Strimzi kinds it knew
// ("kafkas...", "kafkaconnects..."), MirrorMaker 2 was added to the component
// table with a kind that was not on the list, and it reported a blank
// "Unknown" in every status output. Nothing failed, because nothing checked
// that the table and the health check agreed.
func TestEveryStatusComponentCanBeChecked(t *testing.T) {
	for _, c := range statusComponents("kafka", "connect", "kates", "litmus", "monitoring", "database", "kafka", "kafka") {
		if !healthCheckKinds(c.Kind) {
			t.Errorf("%s is listed with kind %q, which no health check handles — it would report a blank Unknown", c.Name, c.Kind)
		}
	}
}

// Strimzi CRs are recognised by their API group, not by a list of names that
// has to be maintained alongside the component table.
func TestStrimziCRRecognisesTheWholeGroup(t *testing.T) {
	for _, kind := range []string{
		"kafkas.kafka.strimzi.io",
		"kafkaconnects.kafka.strimzi.io",
		"kafkamirrormaker2s.kafka.strimzi.io",
		"kafkatopics.kafka.strimzi.io",
		"kafkanodepools.kafka.strimzi.io",
	} {
		if !strimziCR(kind) {
			t.Errorf("%q should be recognised as a Strimzi custom resource", kind)
		}
	}
	for _, kind := range []string{"pod", "statefulset", "deployment", "", "kafkas.example.com"} {
		if strimziCR(kind) {
			t.Errorf("%q is not a Strimzi custom resource", kind)
		}
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
	index := map[string][]helm.Release{
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

	// A unique release in another namespace used to be adopted, so the table
	// showed a second tenant's install under this deploy's namespace.
	t.Run("unique release elsewhere is reported, not adopted", func(t *testing.T) {
		status, ns, ok := lookupHelmRelease(index, "kafka-ui", "kafka")
		if !ok || status != "elsewhere (kafka-ui)" || ns != "kafka" {
			t.Fatalf("unexpected lookup result: status=%q ns=%q ok=%v", status, ns, ok)
		}
	})

	t.Run("ambiguous release without exact namespace", func(t *testing.T) {
		status, ns, ok := lookupHelmRelease(index, "shared", "missing-ns")
		if !ok || status != "elsewhere (ns-a, ns-b)" || ns != "missing-ns" {
			t.Fatalf("unexpected lookup result: status=%q ns=%q ok=%v", status, ns, ok)
		}
	})

	t.Run("unknown release", func(t *testing.T) {
		if _, _, ok := lookupHelmRelease(index, "nothing", "ns"); ok {
			t.Fatal("a release helm does not list is not found")
		}
	})
}
