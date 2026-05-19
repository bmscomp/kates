package cmd

import (
	"context"
	"strings"
	"testing"
)

type MockExecutor struct{}

func (m *MockExecutor) LookPath(file string) (string, error) {
	return "/usr/local/bin/" + file, nil
}

func (m *MockExecutor) Exec(name string, args ...string) (string, error) {
	// Return mock JSON for get nodes so detection doesn't fail
	if name == "kubectl" && len(args) > 1 && args[0] == "get" && args[1] == "nodes" {
		return `{"items":[{"metadata":{"name":"node1"},"status":{"capacity":{"cpu":"4","memory":"8192Mi"}}}]}`, nil
	}
	if name == "kubectl" && len(args) > 0 && args[0] == "cluster-info" {
		return "Kubernetes control plane is running at https://127.0.0.1:6443", nil
	}
	return "", nil
}

func TestDeployCommand_SingleTopology(t *testing.T) {
	// Reset flags before testing
	deployTopology = "single"
	deployNamespace = "kates-test"
	deployWithSchemaRegistry = "none"
	deployWithChaos = false
	deployWithMonitoring = false
	deployWithCertManager = false
	deployWithKyverno = false

	var executedCommands []string
	
	// Mock functions
	runExecFn = func(ctx context.Context, name string, args ...string) error {
		cmdStr := name + " " + strings.Join(args, " ")
		executedCommands = append(executedCommands, cmdStr)
		return nil
	}
	runExecStdinFn = func(ctx context.Context, name string, args []string, stdinData string) error {
		return nil
	}
	runHelmFn = func(ctx context.Context, args ...string) error {
		cmdStr := "helm " + strings.Join(args, " ")
		executedCommands = append(executedCommands, cmdStr)
		return nil
	}
	isHelmReleaseDeployedFn = func(ctx context.Context, release, namespace string) bool {
		return false
	}
	
	defaultExecutor = &MockExecutor{}
	
	// Mock the cluster detection dependencies by overriding valuesFile creation or ensuring it succeeds
	// In runDeploy it creates .build/values-detected.yaml. We just run it and let it create the file.

	err := runDeploy(deployCmd, []string{})
	if err != nil {
		t.Fatalf("runDeploy failed: %v", err)
	}

	// Verify that the commands executed successfully and used the correct namespace
	foundKafka := false
	foundKates := false
	
	for _, cmd := range executedCommands {
		if strings.Contains(cmd, "helm upgrade --install krafter") {
			foundKafka = true
			if !strings.Contains(cmd, "-n kates-test") {
				t.Errorf("Expected Kafka to be deployed in kates-test namespace, got: %s", cmd)
			}
		}
		if strings.Contains(cmd, "helm upgrade --install kates") {
			foundKates = true
			if !strings.Contains(cmd, "-n kates-test") {
				t.Errorf("Expected Kates to be deployed in kates-test namespace, got: %s", cmd)
			}
		}
	}
	
	if !foundKafka {
		t.Error("Kafka deployment command was not executed")
	}
	if !foundKates {
		t.Error("Kates deployment command was not executed")
	}
}

func TestDeployCommand_IsolatedTopology(t *testing.T) {
	deployTopology = "isolated"
	deployKafkaNS = "kafka-sys"
	deployAppNS = "app-sys"
	deployChaosNS = "chaos-sys"
	deployWithSchemaRegistry = "apicurio"
	deployWithChaos = true
	deployWithMonitoring = false
	deployWithCertManager = false
	deployWithKyverno = false

	var executedCommands []string
	
	runExecFn = func(ctx context.Context, name string, args ...string) error {
		cmdStr := name + " " + strings.Join(args, " ")
		executedCommands = append(executedCommands, cmdStr)
		return nil
	}
	runExecStdinFn = func(ctx context.Context, name string, args []string, stdinData string) error {
		return nil
	}
	runHelmFn = func(ctx context.Context, args ...string) error {
		cmdStr := "helm " + strings.Join(args, " ")
		executedCommands = append(executedCommands, cmdStr)
		return nil
	}
	isHelmReleaseDeployedFn = func(ctx context.Context, release, namespace string) bool {
		return false
	}

	err := runDeploy(deployCmd, []string{})
	if err != nil {
		t.Fatalf("runDeploy failed: %v", err)
	}

	foundKafka, foundKates, foundChaos, foundSchema := false, false, false, false
	
	for _, cmd := range executedCommands {
		if strings.Contains(cmd, "helm upgrade --install krafter") {
			foundKafka = true
			if !strings.Contains(cmd, "-n kafka-sys") {
				t.Errorf("Expected Kafka to be deployed in kafka-sys namespace, got: %s", cmd)
			}
		}
		if strings.Contains(cmd, "helm upgrade --install kates") {
			foundKates = true
			if !strings.Contains(cmd, "-n app-sys") {
				t.Errorf("Expected Kates to be deployed in app-sys namespace, got: %s", cmd)
			}
		}
		if strings.Contains(cmd, "helm upgrade --install chaos") {
			foundChaos = true
			if !strings.Contains(cmd, "-n chaos-sys") {
				t.Errorf("Expected Chaos to be deployed in chaos-sys namespace, got: %s", cmd)
			}
		}
		if strings.Contains(cmd, "helm upgrade --install apicurio") {
			foundSchema = true
			if !strings.Contains(cmd, "-n kafka-sys") {
				t.Errorf("Expected Apicurio to be deployed in kafka-sys namespace, got: %s", cmd)
			}
		}
	}
	
	if !foundKafka || !foundKates || !foundChaos || !foundSchema {
		t.Errorf("Missing expected commands. Kafka: %v, Kates: %v, Chaos: %v, Schema: %v", foundKafka, foundKates, foundChaos, foundSchema)
	}
}

func TestDeployCommand_Idempotency(t *testing.T) {
	deployTopology = "single"
	deployNamespace = "test-ns"
	deployWithSchemaRegistry = "apicurio"
	deployWithChaos = true
	deployWithMonitoring = true
	deployWithCertManager = true
	deployWithKyverno = true

	var executedCommands []string
	
	runExecFn = func(ctx context.Context, name string, args ...string) error {
		cmdStr := name + " " + strings.Join(args, " ")
		executedCommands = append(executedCommands, cmdStr)
		return nil
	}
	runExecStdinFn = func(ctx context.Context, name string, args []string, stdinData string) error {
		return nil
	}
	runHelmFn = func(ctx context.Context, args ...string) error {
		cmdStr := "helm " + strings.Join(args, " ")
		executedCommands = append(executedCommands, cmdStr)
		return nil
	}
	isHelmReleaseDeployedFn = func(ctx context.Context, release, namespace string) bool {
		// Mock everything as already deployed
		return true
	}

	defaultExecutor = &MockExecutor{}

	err := runDeploy(deployCmd, []string{})
	if err != nil {
		t.Fatalf("runDeploy failed: %v", err)
	}

	// Because everything is already deployed, no `helm upgrade` should be called.
	for _, cmd := range executedCommands {
		if strings.Contains(cmd, "helm upgrade") {
			t.Errorf("Expected no helm upgrade commands to run due to idempotency, got: %s", cmd)
		}
	}
}
