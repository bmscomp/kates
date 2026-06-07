package cmd

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func init() {
	cleanSleepFn = func(time.Duration) {}
}

func TestCleanCommand_SingleTopology(t *testing.T) {
	// Reset/configure flags
	cleanForce = true
	cleanVerbose = false
	cleanTopology = "single"
	cleanNamespace = "kates-single-test"

	var executedCommands []string
	var mu sync.Mutex

	// Mock cleanRunFn to simulate that everything exists
	cleanRunFn = func(ctx context.Context, name string, args ...string) error {
		cmdStr := name + " " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		return nil
	}

	cleanRunOutputFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		cmdStr := name + " " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		if name == "helm" && args[0] == "list" {
			return []byte(`[{"name":"chaos","namespace":"kates-single-test"},{"name":"kates","namespace":"kates-single-test"},{"name":"apicurio","namespace":"kates-single-test"},{"name":"jaeger","namespace":"kates-single-test"},{"name":"krafter","namespace":"kates-single-test"},{"name":"monitoring","namespace":"kates-single-test"},{"name":"postgresql","namespace":"kates-single-test"}]`), nil
		}
		if name == "kubectl" && args[0] == "get" && args[1] == "namespaces" {
			return []byte("kates-single-test"), nil
		}
		if name == "kubectl" && args[0] == "get" && args[1] == "crd" {
			return []byte("kafkas.kafka.strimzi.io"), nil
		}
		if name == "kubectl" && args[0] == "get" && strings.HasPrefix(args[1], "kafkas") {
			// Mock finding one stuck CR name "my-kafka"
			return []byte("my-kafka"), nil
		}
		return []byte(""), nil
	}

	// Restore default runners after test
	defer func() {
		cleanRunFn = cleanRunDefault
		cleanRunOutputFn = cleanRunOutputDefault
	}()

	err := runClean(cleanCmd, []string{})
	if err != nil {
		t.Fatalf("runClean failed: %v", err)
	}

	// Verify executed commands contains single-topology elements
	foundSingleNSDelete := false
	foundIsolatedNSDelete := false
	foundSingleHelmUninstall := false
	foundIsolatedHelmUninstall := false

	mu.Lock()
	cmds := make([]string, len(executedCommands))
	copy(cmds, executedCommands)
	mu.Unlock()

	for _, cmd := range cmds {
		if strings.Contains(cmd, "kubectl delete namespace kates-single-test") {
			foundSingleNSDelete = true
		}
		if strings.Contains(cmd, "kubectl delete namespace kates-isolated-test") {
			foundIsolatedNSDelete = true
		}
		if strings.Contains(cmd, "helm uninstall kates -n kates-single-test") {
			foundSingleHelmUninstall = true
		}
		if strings.Contains(cmd, "helm uninstall kates -n kates-isolated-test") {
			foundIsolatedHelmUninstall = true
		}
	}

	if !foundSingleNSDelete {
		t.Error("Expected kates-single-test namespace to be deleted")
	}
	if foundIsolatedNSDelete {
		t.Error("Did not expect isolated namespaces to be deleted under single topology")
	}
	if !foundSingleHelmUninstall {
		t.Error("Expected kates release in single-topology namespace to be uninstalled")
	}
	if foundIsolatedHelmUninstall {
		t.Error("Did not expect isolated-topology releases to be uninstalled")
	}

	foundCRDDelete := false
	for _, cmd := range cmds {
		if strings.Contains(cmd, "kubectl delete crd") && strings.Contains(cmd, "kafkas.kafka.strimzi.io") {
			foundCRDDelete = true
		}
	}
	if !foundCRDDelete {
		t.Error("Expected kafkas.kafka.strimzi.io CRD to be deleted")
	}
}

func TestCleanCommand_IsolatedTopology(t *testing.T) {
	// Reset/configure flags
	cleanForce = true
	cleanVerbose = false
	cleanTopology = "isolated"
	cleanKafkaNS = "kafka-iso-test"
	cleanAppNS = "app-iso-test"
	cleanChaosNS = "chaos-iso-test"
	cleanMonitoringNS = "monitoring-iso-test"

	var executedCommands []string
	var mu sync.Mutex

	cleanRunFn = func(ctx context.Context, name string, args ...string) error {
		cmdStr := name + " " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		return nil
	}

	cleanRunOutputFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		cmdStr := name + " " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		if name == "helm" && args[0] == "list" {
			return []byte(`[{"name":"chaos","namespace":"chaos-iso-test"},{"name":"kates","namespace":"app-iso-test"},{"name":"apicurio","namespace":"kafka-iso-test"}]`), nil
		}
		if name == "kubectl" && args[0] == "get" && args[1] == "namespaces" {
			return []byte("chaos-iso-test app-iso-test kafka-iso-test"), nil
		}
		if name == "kubectl" && args[0] == "get" && args[1] == "crd" {
			return []byte(""), nil
		}
		return []byte(""), nil
	}

	defer func() {
		cleanRunFn = cleanRunDefault
		cleanRunOutputFn = cleanRunOutputDefault
	}()

	err := runClean(cleanCmd, []string{})
	if err != nil {
		t.Fatalf("runClean failed: %v", err)
	}

	// Verify that the isolated namespaces and releases are uninstalled, but NOT the single namespace
	foundAppNSDelete := false
	foundKafkaNSDelete := false
	foundSingleNSDelete := false
	foundAppHelmUninstall := false

	mu.Lock()
	cmds := make([]string, len(executedCommands))
	copy(cmds, executedCommands)
	mu.Unlock()

	for _, cmd := range cmds {
		if strings.Contains(cmd, "kubectl delete namespace app-iso-test") {
			foundAppNSDelete = true
		}
		if strings.Contains(cmd, "kubectl delete namespace kafka-iso-test") {
			foundKafkaNSDelete = true
		}
		if strings.Contains(cmd, "kubectl delete namespace kates-stack") {
			foundSingleNSDelete = true
		}
		if strings.Contains(cmd, "helm uninstall kates -n app-iso-test") {
			foundAppHelmUninstall = true
		}
	}

	if !foundAppNSDelete {
		t.Error("Expected app-iso-test namespace to be deleted")
	}
	if !foundKafkaNSDelete {
		t.Error("Expected kafka-iso-test namespace to be deleted")
	}
	if foundSingleNSDelete {
		t.Error("Did not expect single namespace to be deleted under isolated topology")
	}
	if !foundAppHelmUninstall {
		t.Error("Expected kates release in isolated-topology app namespace to be uninstalled")
	}
}

func TestCleanCommand_DefaultBothTopology(t *testing.T) {
	// Reset/configure flags
	cleanForce = true
	cleanVerbose = false
	cleanTopology = "" // both
	cleanNamespace = "kates-single-def"
	cleanKafkaNS = "kafka-iso-def"
	cleanAppNS = "app-iso-def"
	cleanChaosNS = "chaos-iso-def"
	cleanMonitoringNS = "monitoring-iso-def"

	var executedCommands []string
	var mu sync.Mutex

	cleanRunFn = func(ctx context.Context, name string, args ...string) error {
		cmdStr := name + " " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		return nil
	}

	cleanRunOutputFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		cmdStr := name + " " + strings.Join(args, " ")
		mu.Lock()
		executedCommands = append(executedCommands, cmdStr)
		mu.Unlock()
		if name == "helm" && args[0] == "list" {
			return []byte(`[{"name":"kates","namespace":"kates-single-def"},{"name":"kates","namespace":"app-iso-def"}]`), nil
		}
		if name == "kubectl" && args[0] == "get" && args[1] == "namespaces" {
			return []byte("kates-single-def app-iso-def"), nil
		}
		if name == "kubectl" && args[0] == "get" && args[1] == "crd" {
			return []byte(""), nil
		}
		return []byte(""), nil
	}

	defer func() {
		cleanRunFn = cleanRunDefault
		cleanRunOutputFn = cleanRunOutputDefault
	}()

	err := runClean(cleanCmd, []string{})
	if err != nil {
		t.Fatalf("runClean failed: %v", err)
	}

	// Verify both single and isolated environments are cleaned
	foundSingleNSDelete := false
	foundIsolatedNSDelete := false

	mu.Lock()
	cmds := make([]string, len(executedCommands))
	copy(cmds, executedCommands)
	mu.Unlock()

	for _, cmd := range cmds {
		if strings.Contains(cmd, "kubectl delete namespace kates-single-def") {
			foundSingleNSDelete = true
		}
		if strings.Contains(cmd, "kubectl delete namespace app-iso-def") {
			foundIsolatedNSDelete = true
		}
	}

	if !foundSingleNSDelete {
		t.Error("Expected single-topology namespace to be deleted when topology is not specified")
	}
	if !foundIsolatedNSDelete {
		t.Error("Expected isolated-topology namespace to be deleted when topology is not specified")
	}
}

func TestCleanCommand_AlreadyClean(t *testing.T) {
	cleanForce = true
	cleanVerbose = false
	cleanTopology = "single"
	cleanNamespace = "already-clean-ns"

	// Mock cleanRunFn to simulate nothing exists (returns error for status/get namespace)
	cleanRunFn = func(ctx context.Context, name string, args ...string) error {
		return fmt.Errorf("not found")
	}

	cleanRunOutputFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return nil, fmt.Errorf("not found")
	}

	defer func() {
		cleanRunFn = cleanRunDefault
		cleanRunOutputFn = cleanRunOutputDefault
	}()

	err := runClean(cleanCmd, []string{})
	if err != nil {
		t.Fatalf("runClean failed: %v", err)
	}
	// The command should exit with nil and print already clean message
}
