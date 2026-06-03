package cmd

import (
	"context"
	"strings"
	"sync"
	"testing"
)

func init() {
	isTesting = true
}

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
	deployWithStrimzi = false
	deployWithKafkaConnect = false

	var executedCommands []string
	var mu sync.Mutex

	// Mock functions
	runExecFn = func(ctx context.Context, name string, args ...string) error {
		cmdStr := name + " " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		return nil
	}
	runExecStdinFn = func(ctx context.Context, name string, args []string, stdinData string) error {
		return nil
	}
	runHelmFn = func(ctx context.Context, args ...string) error {
		cmdStr := "helm " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		return nil
	}
	isHelmReleaseDeployedFn = func(ctx context.Context, release, namespace string) bool {
		return false
	}

	defaultExecutor = &MockExecutor{}

	// Mock the cluster detection dependencies by overriding valuesFile creation or ensuring it succeeds
	// In runDeploy it creates .build/values-detected.yaml. We just run it and let it create the file.

	_ = deployCmd.Flags().Set("topology", deployTopology)
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
	deployWithStrimzi = false
	deployWithKafkaConnect = true

	var executedCommands []string
	var mu sync.Mutex

	runExecFn = func(ctx context.Context, name string, args ...string) error {
		cmdStr := name + " " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		return nil
	}
	runExecStdinFn = func(ctx context.Context, name string, args []string, stdinData string) error {
		return nil
	}
	runHelmFn = func(ctx context.Context, args ...string) error {
		cmdStr := "helm " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		return nil
	}
	isHelmReleaseDeployedFn = func(ctx context.Context, release, namespace string) bool {
		return false
	}

	_ = deployCmd.Flags().Set("topology", deployTopology)
	err := runDeploy(deployCmd, []string{})
	if err != nil {
		t.Fatalf("runDeploy failed: %v", err)
	}

	foundKafka, foundKates, foundChaos, foundSchema, foundPostgres := false, false, false, false, false

	for _, cmd := range executedCommands {
		if strings.Contains(cmd, "helm upgrade --install krafter") {
			foundKafka = true
			if !strings.Contains(cmd, "-n kafka-sys") {
				t.Errorf("Expected Kafka to be deployed in kafka-sys namespace, got: %s", cmd)
			}
			if !strings.Contains(cmd, "kafkaConnect.enabled=true") {
				t.Errorf("Expected Kafka Connect to be enabled, got: %s", cmd)
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
		if strings.Contains(cmd, "helm upgrade --install postgresql bitnami/postgresql") {
			foundPostgres = true
			if !strings.Contains(cmd, "-n database") {
				t.Errorf("Expected Postgres to be deployed in database namespace, got: %s", cmd)
			}
		}
	}

	if !foundKafka || !foundKates || !foundChaos || !foundSchema || !foundPostgres {
		t.Errorf("Missing expected commands. Kafka: %v, Kates: %v, Chaos: %v, Schema: %v, Postgres: %v", foundKafka, foundKates, foundChaos, foundSchema, foundPostgres)
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
	deployWithStrimzi = true
	deployWithKafkaConnect = false

	var executedCommands []string
	var mu sync.Mutex

	runExecFn = func(ctx context.Context, name string, args ...string) error {
		cmdStr := name + " " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		return nil
	}
	runExecStdinFn = func(ctx context.Context, name string, args []string, stdinData string) error {
		return nil
	}
	runHelmFn = func(ctx context.Context, args ...string) error {
		cmdStr := "helm " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		return nil
	}
	isHelmReleaseDeployedFn = func(ctx context.Context, release, namespace string) bool {
		// Mock everything as already deployed
		return true
	}

	defaultExecutor = &MockExecutor{}

	_ = deployCmd.Flags().Set("topology", deployTopology)
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

func TestDeployCommand_KafkaConnectParameters(t *testing.T) {
	// Save and restore global flags
	defer func() {
		deployTopology = "isolated"
		deployNamespace = "kates-stack"
		deployKafkaNS = "kafka"
		deployDbNS = "database"
		deployAppNS = "kates"
		deployChaosNS = "litmus"
		deployWithSchemaRegistry = "none"
		deployWithChaos = false
		deployWithMonitoring = false
		deployWithCertManager = false
		deployWithKyverno = false
		deployWithStrimzi = false
		deployWithKafkaConnect = false
	}()

	deployTopology = "isolated"
	deployKafkaNS = "kafka"
	deployDbNS = "database"
	deployAppNS = "kates"
	deployChaosNS = "litmus"
	deployWithSchemaRegistry = "none"
	deployWithChaos = false
	deployWithMonitoring = false
	deployWithCertManager = false
	deployWithKyverno = false
	deployWithStrimzi = false
	deployWithKafkaConnect = true

	var executedCommands []string
	var stdinCommands []string
	var mu sync.Mutex

	runExecFn = func(ctx context.Context, name string, args ...string) error {
		cmdStr := name + " " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		return nil
	}
	runExecStdinFn = func(ctx context.Context, name string, args []string, stdinData string) error {
		cmdStr := name + " " + strings.Join(args, " ")
		mu.Lock()
		stdinCommands = append(stdinCommands, cmdStr+"|"+stdinData)
		mu.Unlock()
		return nil
	}
	runHelmFn = func(ctx context.Context, args ...string) error {
		cmdStr := "helm " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		return nil
	}
	isHelmReleaseDeployedFn = func(ctx context.Context, release, namespace string) bool {
		return false
	}

	defaultExecutor = &MockExecutor{}

	_ = deployCmd.Flags().Set("topology", deployTopology)
	err := runDeploy(deployCmd, []string{})
	if err != nil {
		t.Fatalf("runDeploy failed: %v", err)
	}

	// 1. Verify Kafka helm command includes ALL connect-specific sets
	foundKafkaConnect := false
	for _, cmd := range executedCommands {
		if !strings.Contains(cmd, "helm upgrade --install krafter") {
			continue
		}
		foundKafkaConnect = true

		connectSets := []string{
			"kafkaConnect.enabled=true",
			"kafkaConnect.schemaRegistry.enabled=true",
			"kafkaConnect.databaseEgress[0].namespace=database",
			"kafkaConnect.externalConfiguration.volumes[0].name=pg-credentials",
		}
		for _, expected := range connectSets {
			if !strings.Contains(cmd, expected) {
				t.Errorf("Expected Kafka helm command to include %q, got: %s", expected, cmd)
			}
		}
	}
	if !foundKafkaConnect {
		t.Error("Kafka deployment command (krafter) was not executed")
	}

	// 2. Verify PostgreSQL is deployed with the correct namespace (database)
	foundPostgres := false
	for _, cmd := range executedCommands {
		if strings.Contains(cmd, "helm upgrade --install postgresql bitnami/postgresql") {
			foundPostgres = true
			if !strings.Contains(cmd, "-n database") {
				t.Errorf("Expected PostgreSQL to be deployed in 'database' namespace, got: %s", cmd)
			}
		}
	}
	if !foundPostgres {
		t.Error("PostgreSQL deployment command was not executed")
	}

	// 3. Verify the connect-pg-credentials Secret creation kubectl command is invoked
	foundPgSecret := false
	for _, cmd := range stdinCommands {
		if strings.Contains(cmd, "kubectl") && strings.Contains(cmd, "connect-pg-credentials") {
			foundPgSecret = true
		}
	}
	if !foundPgSecret {
		t.Error("connect-pg-credentials Secret creation command was not invoked")
	}
}

func TestDeployCommand_KafkaConnectIdempotent(t *testing.T) {
	// Save and restore global flags
	defer func() {
		deployTopology = "isolated"
		deployNamespace = "kates-stack"
		deployKafkaNS = "kafka"
		deployDbNS = "database"
		deployAppNS = "kates"
		deployChaosNS = "litmus"
		deployWithSchemaRegistry = "none"
		deployWithChaos = false
		deployWithMonitoring = false
		deployWithCertManager = false
		deployWithKyverno = false
		deployWithStrimzi = false
		deployWithKafkaConnect = false
	}()

	deployTopology = "isolated"
	deployKafkaNS = "kafka"
	deployDbNS = "database"
	deployAppNS = "kates"
	deployChaosNS = "litmus"
	deployWithSchemaRegistry = "none"
	deployWithChaos = false
	deployWithMonitoring = false
	deployWithCertManager = false
	deployWithKyverno = false
	deployWithStrimzi = false
	deployWithKafkaConnect = true

	var executedCommands []string
	var stdinCommands []string
	var mu sync.Mutex

	runExecFn = func(ctx context.Context, name string, args ...string) error {
		cmdStr := name + " " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		return nil
	}
	runExecStdinFn = func(ctx context.Context, name string, args []string, stdinData string) error {
		cmdStr := name + " " + strings.Join(args, " ")
		mu.Lock()
		stdinCommands = append(stdinCommands, cmdStr+"|"+stdinData)
		mu.Unlock()
		return nil
	}
	runHelmFn = func(ctx context.Context, args ...string) error {
		cmdStr := "helm " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		return nil
	}
	isHelmReleaseDeployedFn = func(ctx context.Context, release, namespace string) bool {
		// All releases are already deployed
		return true
	}

	defaultExecutor = &MockExecutor{}

	_ = deployCmd.Flags().Set("topology", deployTopology)
	err := runDeploy(deployCmd, []string{})
	if err != nil {
		t.Fatalf("runDeploy failed: %v", err)
	}

	// When everything is already deployed, no helm upgrade should be called
	for _, cmd := range executedCommands {
		if strings.Contains(cmd, "helm upgrade") {
			t.Errorf("Expected no helm upgrade commands due to idempotency, got: %s", cmd)
		}
	}

	// Specifically, PostgreSQL should NOT be deployed
	for _, cmd := range executedCommands {
		if strings.Contains(cmd, "helm upgrade --install postgresql") {
			t.Error("PostgreSQL should not be redeployed when already deployed (idempotency)")
		}
	}

	// The connect-pg-credentials secret should still be created (it's applied idempotently via kubectl apply)
	// but no helm upgrade for kafka/postgresql should run
	for _, cmd := range executedCommands {
		if strings.Contains(cmd, "helm upgrade --install krafter") {
			t.Error("Kafka chart should not be redeployed when already deployed (idempotency)")
		}
	}
}
