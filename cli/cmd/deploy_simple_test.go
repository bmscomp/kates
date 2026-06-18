package cmd

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// resetSimpleDeployFlags resets all simple deploy flags to their defaults
// before each test so tests are independent. Call the returned cleanup
// function (or use t.Cleanup) to restore non-simple defaults after the test.
func resetSimpleDeployFlags(t *testing.T) {
	deploySimple = true
	deploySimpleDev = false
	deploySimplePgUser = "debezium"
	deploySimplePgPassword = "debezium"
	deploySimpleUpgrade = false
	deploySimpleWithConnectors = true
	deploySimpleWithBackend = false
	deployHA = false
	deployDryRun = false
	deployPortForward = false
	deployWithSchemaRegistry = "apicurio"
	deployNamespace = "test-ns"
	deployTopology = "single"
	deployWithStrimzi = false
	deployWithCertManager = false
	deployWithKyverno = false
	deployWithChaos = false
	deployWithMonitoring = false
	deployWithKafkaConnect = true

	// Restore non-simple defaults after the test so existing tests aren't polluted
	t.Cleanup(func() {
		deploySimple = false
		deploySimpleDev = false
		deploySimplePgUser = "debezium"
		deploySimplePgPassword = "debezium"
		deploySimpleUpgrade = false
		deploySimpleWithConnectors = true
		deploySimpleWithBackend = false
		deployHA = true
		deployDryRun = false
		deployPortForward = false
		deployWithSchemaRegistry = "apicurio"
		deployTopology = "isolated"
		deployWithStrimzi = true
		deployWithCertManager = true
		deployWithKyverno = false
		deployWithChaos = true
		deployWithMonitoring = true
		deployWithKafkaConnect = false
	})
}

// setupSimpleMocks configures mock function variables for simple deploy tests.
// Returns pointers to captured helm command strings, stdin YAML strings, and
// the mutex used for synchronization.
func setupSimpleMocks() (helmCmds *[]string, stdinYAMLs *[]string, mu *sync.Mutex) {
	var hCmds []string
	var sYAMLs []string
	var m sync.Mutex

	runHelmFn = func(ctx context.Context, args ...string) error {
		cmdStr := "helm " + strings.Join(args, " ")
		m.Lock()
		hCmds = append(hCmds, cmdStr)
		m.Unlock()
		return nil
	}

	runExecStdinFn = func(ctx context.Context, name string, args []string, stdinData string) error {
		m.Lock()
		sYAMLs = append(sYAMLs, stdinData)
		m.Unlock()
		return nil
	}

	runExecFn = func(ctx context.Context, name string, args ...string) error {
		return nil
	}

	isHelmReleaseDeployedFn = func(ctx context.Context, release, namespace string) bool {
		return false
	}

	defaultExecutor = &MockExecutor{}

	return &hCmds, &sYAMLs, &m
}

// findYAML searches captured stdin YAMLs for one containing the given substring.
func findYAML(yamls []string, substr string) (string, bool) {
	for _, y := range yamls {
		if strings.Contains(y, substr) {
			return y, true
		}
	}
	return "", false
}

// Test 1: Default simple deploy creates all core components (PostgreSQL, Kafka, Connect, connectors, Apicurio)
func TestSimpleDeploy_DefaultMode(t *testing.T) {
	resetSimpleDeployFlags(t)
	helmCmds, stdinYAMLs, _ := setupSimpleMocks()

	err := runSimpleDeploy(deployCmd, "test-ns")
	if err != nil {
		t.Fatalf("runSimpleDeploy failed: %v", err)
	}

	// Verify PostgreSQL helm install
	foundPG := false
	for _, cmd := range *helmCmds {
		if strings.Contains(cmd, "upgrade --install postgresql bitnami/postgresql") && strings.Contains(cmd, "-n test-ns") {
			foundPG = true
		}
	}
	if !foundPG {
		t.Error("PostgreSQL helm install not found")
	}

	// Verify Kafka CR applied
	if _, found := findYAML(*stdinYAMLs, "kind: Kafka"); !found {
		t.Error("Kafka CR not applied")
	}

	// Verify KafkaConnect CR applied
	if _, found := findYAML(*stdinYAMLs, "kind: KafkaConnect"); !found {
		t.Error("KafkaConnect CR not applied")
	}

	// Verify Debezium connector applied (connectors ON by default)
	if _, found := findYAML(*stdinYAMLs, "debezium-postgres-source"); !found {
		t.Error("Debezium connector not applied")
	}

	// Verify JDBC Sink connector applied
	if _, found := findYAML(*stdinYAMLs, "jdbc-sink-connector"); !found {
		t.Error("JDBC Sink connector not applied")
	}

	// Verify Apicurio (schema registry defaults to "apicurio")
	foundApicurio := false
	for _, cmd := range *helmCmds {
		if strings.Contains(cmd, "upgrade --install apicurio") && strings.Contains(cmd, "-n test-ns") {
			foundApicurio = true
		}
	}
	if !foundApicurio {
		t.Error("Apicurio helm install not found")
	}

	// Verify PG credentials secret applied
	if _, found := findYAML(*stdinYAMLs, "connect-pg-credentials"); !found {
		t.Error("PG credentials secret not applied")
	}
}

// Test 2: HA mode sets correct replicas and replication factors
func TestSimpleDeploy_HAMode(t *testing.T) {
	resetSimpleDeployFlags(t)
	deployHA = true
	_, stdinYAMLs, _ := setupSimpleMocks()

	err := runSimpleDeploy(deployCmd, "test-ns")
	if err != nil {
		t.Fatalf("runSimpleDeploy failed: %v", err)
	}

	// Verify Kafka CR has correct HA config
	kafkaYAML, found := findYAML(*stdinYAMLs, "kind: Kafka")
	if !found {
		t.Fatal("Kafka CR not applied")
	}
	if !strings.Contains(kafkaYAML, "min.insync.replicas: 2") {
		t.Error("HA mode should have min.insync.replicas: 2")
	}
	if !strings.Contains(kafkaYAML, "default.replication.factor: 3") {
		t.Error("HA mode should have replication factor 3")
	}
	if !strings.Contains(kafkaYAML, "replicas: 3") {
		t.Error("HA mode should have 3 replicas on Kafka CR")
	}

	// Verify KafkaConnect CR has 3 replicas in HA mode
	connectYAML, found := findYAML(*stdinYAMLs, "kind: KafkaConnect")
	if !found {
		t.Fatal("KafkaConnect CR not applied")
	}
	if !strings.Contains(connectYAML, "replicas: 3") {
		t.Error("HA mode should have 3 replicas on KafkaConnect CR")
	}
}

// Test 3: Non-HA mode sets single replicas and replication factor 1
func TestSimpleDeploy_NonHAMode(t *testing.T) {
	resetSimpleDeployFlags(t)
	deployHA = false
	_, stdinYAMLs, _ := setupSimpleMocks()

	err := runSimpleDeploy(deployCmd, "test-ns")
	if err != nil {
		t.Fatalf("runSimpleDeploy failed: %v", err)
	}

	kafkaYAML, found := findYAML(*stdinYAMLs, "kind: Kafka")
	if !found {
		t.Fatal("Kafka CR not applied")
	}
	if !strings.Contains(kafkaYAML, "min.insync.replicas: 1") {
		t.Error("non-HA mode should have min.insync.replicas: 1")
	}
	if !strings.Contains(kafkaYAML, "default.replication.factor: 1") {
		t.Error("non-HA mode should have replication factor 1")
	}
	if !strings.Contains(kafkaYAML, "replicas: 1") {
		t.Error("non-HA mode should have 1 replica on Kafka CR")
	}

	connectYAML, found := findYAML(*stdinYAMLs, "kind: KafkaConnect")
	if !found {
		t.Fatal("KafkaConnect CR not applied")
	}
	if !strings.Contains(connectYAML, "replicas: 1") {
		t.Error("non-HA mode should have 1 replica on KafkaConnect CR")
	}
}

// Test 4: Without connectors flag skips Debezium and JDBC Sink
func TestSimpleDeploy_WithoutConnectors(t *testing.T) {
	resetSimpleDeployFlags(t)
	deploySimpleWithConnectors = false
	_, stdinYAMLs, _ := setupSimpleMocks()

	err := runSimpleDeploy(deployCmd, "test-ns")
	if err != nil {
		t.Fatalf("runSimpleDeploy failed: %v", err)
	}

	// Verify no Debezium connector
	if _, found := findYAML(*stdinYAMLs, "debezium-postgres-source"); found {
		t.Error("Debezium connector should NOT be applied when --with-connectors=false")
	}

	// Verify no JDBC Sink connector
	if _, found := findYAML(*stdinYAMLs, "jdbc-sink-connector"); found {
		t.Error("JDBC Sink connector should NOT be applied when --with-connectors=false")
	}

	// Verify Kafka + Connect + PG are still deployed
	if _, found := findYAML(*stdinYAMLs, "kind: Kafka"); !found {
		t.Error("Kafka CR should still be applied")
	}
	if _, found := findYAML(*stdinYAMLs, "kind: KafkaConnect"); !found {
		t.Error("KafkaConnect CR should still be applied")
	}
}

// Test 5: Connector YAML content validation
func TestSimpleDeploy_ConnectorContent(t *testing.T) {
	resetSimpleDeployFlags(t)
	_, stdinYAMLs, _ := setupSimpleMocks()

	err := runSimpleDeploy(deployCmd, "test-ns")
	if err != nil {
		t.Fatalf("runSimpleDeploy failed: %v", err)
	}

	// Validate Debezium connector content
	debeziumYAML, found := findYAML(*stdinYAMLs, "debezium-postgres-source")
	if !found {
		t.Fatal("Debezium connector not found")
	}

	debeziumChecks := []struct {
		name   string
		substr string
	}{
		{"kind", "kind: KafkaConnector"},
		{"name", "name: debezium-postgres-source"},
		{"cluster label", "strimzi.io/cluster: connect-cluster"},
		{"connector class", "PostgresConnector"},
		{"database host", "postgresql.test-ns.svc"},
		{"database name", "database.dbname: orders"},
		{"topic prefix", "topic.prefix: cdc"},
		{"plugin name", "plugin.name: pgoutput"},
		{"snapshot mode", "snapshot.mode: initial"},
		{"auto restart", "autoRestart"},
	}
	for _, c := range debeziumChecks {
		if !strings.Contains(debeziumYAML, c.substr) {
			t.Errorf("Debezium connector missing %s: expected substring %q", c.name, c.substr)
		}
	}

	// Validate JDBC Sink connector content
	sinkYAML, found := findYAML(*stdinYAMLs, "jdbc-sink-connector")
	if !found {
		t.Fatal("JDBC Sink connector not found")
	}

	sinkChecks := []struct {
		name   string
		substr string
	}{
		{"kind", "kind: KafkaConnector"},
		{"name", "name: jdbc-sink-connector"},
		{"cluster label", "strimzi.io/cluster: connect-cluster"},
		{"connector class", "JdbcSinkConnector"},
		{"connection URL", "postgresql.test-ns.svc"},
		{"auto create", "auto.create"},
		{"insert mode", "insert.mode"},
		{"auto evolve", "auto.evolve"},
	}
	for _, c := range sinkChecks {
		if !strings.Contains(sinkYAML, c.substr) {
			t.Errorf("JDBC Sink connector missing %s: expected substring %q", c.name, c.substr)
		}
	}
}

// Test 6: Custom PG credentials propagate to Helm command and PG secret
func TestSimpleDeploy_CustomCredentials(t *testing.T) {
	resetSimpleDeployFlags(t)
	deploySimplePgUser = "myuser"
	deploySimplePgPassword = "mysecret"
	helmCmds, stdinYAMLs, _ := setupSimpleMocks()

	err := runSimpleDeploy(deployCmd, "test-ns")
	if err != nil {
		t.Fatalf("runSimpleDeploy failed: %v", err)
	}

	// Verify PostgreSQL Helm uses custom credentials
	foundCustomPG := false
	for _, cmd := range *helmCmds {
		if strings.Contains(cmd, "auth.username=myuser") && strings.Contains(cmd, "auth.password=mysecret") {
			foundCustomPG = true
		}
	}
	if !foundCustomPG {
		t.Error("PostgreSQL helm should use custom credentials")
	}

	// Verify PG credentials secret uses custom values
	secretYAML, found := findYAML(*stdinYAMLs, "connect-pg-credentials")
	if !found {
		t.Fatal("PG credentials secret not applied")
	}
	if !strings.Contains(secretYAML, "password: mysecret") {
		t.Error("PG secret should contain custom password")
	}
	if !strings.Contains(secretYAML, "username: myuser") {
		t.Error("PG secret should contain custom username")
	}
}

// Test 7: Dry-run shows preview without executing any helm installs or kubectl applies
func TestSimpleDeploy_DryRun(t *testing.T) {
	resetSimpleDeployFlags(t)
	deployDryRun = true
	helmCmds, stdinYAMLs, _ := setupSimpleMocks()

	err := runSimpleDeploy(deployCmd, "test-ns")
	if err != nil {
		t.Fatalf("runSimpleDeploy failed: %v", err)
	}

	// Dry-run should NOT execute any helm upgrade --install commands
	for _, cmd := range *helmCmds {
		if strings.Contains(cmd, "upgrade --install") {
			t.Errorf("dry-run should not execute helm install: %s", cmd)
		}
	}

	// Dry-run should NOT apply any CRs via kubectl
	for _, yaml := range *stdinYAMLs {
		if strings.Contains(yaml, "kind: Kafka") || strings.Contains(yaml, "kind: KafkaConnect") {
			t.Errorf("dry-run should not apply CRs via kubectl")
		}
	}
}

// Test 8: Upgrade mode re-deploys even when PostgreSQL is already deployed
func TestSimpleDeploy_Upgrade(t *testing.T) {
	resetSimpleDeployFlags(t)
	deploySimpleUpgrade = true

	helmCmds, stdinYAMLs, _ := setupSimpleMocks()
	// Override isHelmReleaseDeployedFn AFTER setupSimpleMocks (which resets it)
	isHelmReleaseDeployedFn = func(ctx context.Context, release, namespace string) bool {
		return true
	}

	err := runSimpleDeploy(deployCmd, "test-ns")
	if err != nil {
		t.Fatalf("runSimpleDeploy failed: %v", err)
	}

	// Even though PG is "deployed", upgrade mode should still run helm upgrade
	foundPGUpgrade := false
	for _, cmd := range *helmCmds {
		if strings.Contains(cmd, "upgrade --install postgresql") {
			foundPGUpgrade = true
		}
	}
	if !foundPGUpgrade {
		t.Error("upgrade mode should run helm upgrade even when PG is already deployed")
	}

	// Kafka CR should still be applied (upgrade mode)
	if _, found := findYAML(*stdinYAMLs, "kind: Kafka"); !found {
		t.Error("upgrade mode should apply Kafka CR even when already deployed")
	}

	// Apicurio should also be upgraded
	foundApicurio := false
	for _, cmd := range *helmCmds {
		if strings.Contains(cmd, "upgrade --install apicurio") {
			foundApicurio = true
		}
	}
	if !foundApicurio {
		t.Error("upgrade mode should run helm upgrade for Apicurio even when already deployed")
	}
}

// Test 9: Kates backend deployment with correct helm parameters
func TestSimpleDeploy_WithKatesBackend(t *testing.T) {
	resetSimpleDeployFlags(t)
	deploySimpleWithBackend = true
	helmCmds, _, _ := setupSimpleMocks()

	err := runSimpleDeploy(deployCmd, "test-ns")
	if err != nil {
		t.Fatalf("runSimpleDeploy failed: %v", err)
	}

	// Verify kates helm install
	foundKates := false
	foundSimpleValues := false
	foundMonitoringOff := false
	for _, cmd := range *helmCmds {
		if strings.Contains(cmd, "upgrade --install kates charts/kates") {
			foundKates = true
			if strings.Contains(cmd, "values-simple.yaml") {
				foundSimpleValues = true
			}
			if strings.Contains(cmd, "monitoring.enabled=false") {
				foundMonitoringOff = true
			}
		}
	}
	if !foundKates {
		t.Error("Kates backend helm install not found")
	}
	if !foundSimpleValues {
		t.Error("Kates backend should use values-simple.yaml")
	}
	if !foundMonitoringOff {
		t.Error("Kates backend should disable monitoring")
	}
}

// Test 10: Idempotency — PostgreSQL skipped when already deployed and not upgrading
func TestSimpleDeploy_Idempotency(t *testing.T) {
	resetSimpleDeployFlags(t)
	deploySimpleUpgrade = false

	helmCmds, _, _ := setupSimpleMocks()
	// Override after setupSimpleMocks resets it: PostgreSQL + Apicurio already deployed
	isHelmReleaseDeployedFn = func(ctx context.Context, release, namespace string) bool {
		return true
	}

	err := runSimpleDeploy(deployCmd, "test-ns")
	if err != nil {
		t.Fatalf("runSimpleDeploy failed: %v", err)
	}

	// PostgreSQL should be skipped (no helm upgrade --install postgresql)
	for _, cmd := range *helmCmds {
		if strings.Contains(cmd, "upgrade --install postgresql") {
			t.Error("PostgreSQL should be skipped when already deployed and no --upgrade flag")
		}
	}

	// Apicurio should also be skipped
	for _, cmd := range *helmCmds {
		if strings.Contains(cmd, "upgrade --install apicurio") {
			t.Error("Apicurio should be skipped when already deployed and no --upgrade flag")
		}
	}

	// Note: Kafka and KafkaConnect CR idempotency uses isSimpleComponentDeployed
	// which calls kubectl directly. In test env, kubectl is unavailable so
	// isSimpleComponentDeployed always returns false, meaning Kafka and Connect CRs
	// will still be applied. Full idempotency testing for CRs requires converting
	// isSimpleComponentDeployed to a function variable.
}

// Test 11: Dev mode uses minimal resources on KafkaNodePools, Connect, and PostgreSQL
func TestSimpleDeploy_DevMode(t *testing.T) {
	resetSimpleDeployFlags(t)
	deploySimpleDev = true
	helmCmds, stdinYAMLs, _ := setupSimpleMocks()

	err := runSimpleDeploy(deployCmd, "test-ns")
	if err != nil {
		t.Fatalf("runSimpleDeploy failed: %v", err)
	}

	// Verify KafkaNodePool controllers with dev resources
	if ctrlYAML, found := findYAML(*stdinYAMLs, "name: controllers"); !found {
		t.Fatal("KafkaNodePool controllers not applied")
	} else {
		if !strings.Contains(ctrlYAML, "kind: KafkaNodePool") {
			t.Error("controllers CR should be KafkaNodePool kind")
		}
		if !strings.Contains(ctrlYAML, "memory: 512Mi") {
			t.Error("dev mode controllers should have 512Mi memory")
		}
		if !strings.Contains(ctrlYAML, "cpu: 100m") {
			t.Error("dev mode controllers should have 100m CPU request")
		}
		if !strings.Contains(ctrlYAML, "-Xms: 256m") {
			t.Error("dev mode controllers should have -Xms 256m")
		}
		if !strings.Contains(ctrlYAML, "size: 1Gi") {
			t.Error("dev mode controllers should have 1Gi storage")
		}
		if !strings.Contains(ctrlYAML, "replicas: 1") {
			t.Error("dev mode controllers should have 1 replica")
		}
	}

	// Verify KafkaNodePool brokers with dev resources
	if brokerYAML, found := findYAML(*stdinYAMLs, "name: brokers"); !found {
		t.Fatal("KafkaNodePool brokers not applied")
	} else {
		if !strings.Contains(brokerYAML, "kind: KafkaNodePool") {
			t.Error("brokers CR should be KafkaNodePool kind")
		}
		if !strings.Contains(brokerYAML, "memory: 1Gi") {
			t.Error("dev mode brokers should have 1Gi memory")
		}
		if !strings.Contains(brokerYAML, "cpu: 250m") {
			t.Error("dev mode brokers should have 250m CPU request")
		}
		if !strings.Contains(brokerYAML, "-Xms: 512m") {
			t.Error("dev mode brokers should have -Xms 512m")
		}
		if !strings.Contains(brokerYAML, "size: 5Gi") {
			t.Error("dev mode brokers should have 5Gi storage")
		}
		if !strings.Contains(brokerYAML, "replicas: 1") {
			t.Error("dev mode brokers should have 1 replica")
		}
	}

	// Verify KafkaConnect dev resources
	if connectYAML, found := findYAML(*stdinYAMLs, "kind: KafkaConnect"); !found {
		t.Fatal("KafkaConnect CR not applied")
	} else {
		if !strings.Contains(connectYAML, "memory: 512Mi") {
			t.Error("dev mode connect should have 512Mi memory request")
		}
		if !strings.Contains(connectYAML, "cpu: 250m") {
			t.Error("dev mode connect should have 250m CPU request")
		}
		if !strings.Contains(connectYAML, "-Xms: 256m") {
			t.Error("dev mode connect should have -Xms 256m")
		}
	}

	// Verify PostgreSQL dev resources
	foundPGDev := false
	for _, cmd := range *helmCmds {
		if strings.Contains(cmd, "postgresql") &&
			strings.Contains(cmd, "primary.resources.requests.memory=128Mi") &&
			strings.Contains(cmd, "primary.persistence.size=1Gi") {
			foundPGDev = true
		}
	}
	if !foundPGDev {
		t.Error("PostgreSQL dev resources not set")
	}
}

// Test 12: Dev mode + HA uses 3 replicas with dev resources
func TestSimpleDeploy_DevHA(t *testing.T) {
	resetSimpleDeployFlags(t)
	deploySimpleDev = true
	deployHA = true
	_, stdinYAMLs, _ := setupSimpleMocks()

	err := runSimpleDeploy(deployCmd, "test-ns")
	if err != nil {
		t.Fatalf("runSimpleDeploy failed: %v", err)
	}

	// Verify broker replicas = 3 with dev resources
	if brokerYAML, found := findYAML(*stdinYAMLs, "name: brokers"); !found {
		t.Fatal("KafkaNodePool brokers not applied")
	} else {
		if !strings.Contains(brokerYAML, "replicas: 3") {
			t.Error("dev HA mode brokers should have 3 replicas")
		}
		// Still dev resources
		if !strings.Contains(brokerYAML, "memory: 1Gi") {
			t.Error("dev HA brokers should still have 1Gi memory")
		}
	}

	// Verify controller replicas = 3 with dev resources
	if ctrlYAML, found := findYAML(*stdinYAMLs, "name: controllers"); !found {
		t.Fatal("KafkaNodePool controllers not applied")
	} else {
		if !strings.Contains(ctrlYAML, "replicas: 3") {
			t.Error("dev HA mode controllers should have 3 replicas")
		}
		if !strings.Contains(ctrlYAML, "memory: 512Mi") {
			t.Error("dev HA controllers should still have 512Mi memory")
		}
	}

	// Verify Kafka CR has HA config
	if kafkaYAML, found := findYAML(*stdinYAMLs, "kind: Kafka"); !found {
		t.Fatal("Kafka CR not applied")
	} else {
		if !strings.Contains(kafkaYAML, "min.insync.replicas: 2") {
			t.Error("dev HA should have min.insync.replicas: 2")
		}
	}
}
