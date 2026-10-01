package cmd

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// TestDeployCommand_KafkaUIFollowsKafkaName checks that Kafka UI is pointed
// at the primary that --kafka-name names. It read kates detect's
// --cluster-name instead, whose default is krafter, so a primary under any
// other name got a Kafka UI whose KafkaUser and bootstrap address named a
// cluster that does not exist.
func TestDeployCommand_KafkaUIFollowsKafkaName(t *testing.T) {
	defer func() {
		deployTopology = "isolated"
		deployKafkaNS = "kafka"
		deployKafkaName = "krafter"
		deployWithSchemaRegistry = "none"
		deployWithChaos = false
		deployWithMonitoring = false
		deployWithCertManager = false
		deployWithKyverno = false
		deployWithStrimzi = false
		deployWithKafkaConnect = false
		deployWithKafkaUI = false
		deployWithMirrorMaker2 = false
	}()

	deployTopology = "isolated"
	deployKafkaNS = "kafka"
	deployKafkaName = "orders"
	deployWithSchemaRegistry = "none"
	deployWithChaos = false
	deployWithMonitoring = false
	deployWithCertManager = false
	deployWithKyverno = false
	deployWithStrimzi = false
	deployWithKafkaConnect = false
	deployWithKafkaUI = true
	deployWithMirrorMaker2 = false

	var helmCommands []string
	var mu sync.Mutex
	runExecFn = func(ctx context.Context, name string, args ...string) error { return nil }
	runExecStdinFn = func(ctx context.Context, name string, args []string, stdinData string) error { return nil }
	runHelmFn = func(ctx context.Context, args ...string) error {
		mu.Lock()
		helmCommands = append(helmCommands, "helm "+strings.Join(args, " "))
		mu.Unlock()
		return nil
	}
	isHelmReleaseDeployedFn = func(ctx context.Context, release, namespace string) bool { return false }
	defaultExecutor = &MockExecutor{}

	if err := runDeploy(deployCmd, []string{}); err != nil {
		t.Fatalf("runDeploy failed: %v", err)
	}

	var ui string
	for _, c := range helmCommands {
		if strings.Contains(c, "helm upgrade --install kafka-ui charts/kafka-ui") {
			ui = c
		}
	}
	if ui == "" {
		t.Fatalf("Kafka UI was not installed; helm calls:\n%s", strings.Join(helmCommands, "\n"))
	}
	if !strings.Contains(ui, "--set kafka.clusterName=orders ") {
		t.Errorf("Kafka UI is not pointed at the primary, orders:\n%s", ui)
	}
}
