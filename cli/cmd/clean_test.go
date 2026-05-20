package cmd

import (
	"context"
	"fmt"
	"strings"
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

	// Mock cleanRunFn to simulate that everything exists
	cleanRunFn = func(ctx context.Context, name string, args ...string) error {
		cmdStr := name + " " + strings.Join(args, " ")
		executedCommands = append(executedCommands, cmdStr)
		// Return nil for helm status and kubectl get namespace to simulate presence
		if (name == "helm" && args[0] == "status") || (name == "kubectl" && args[0] == "get" && args[1] == "namespace") {
			return nil
		}
		return nil
	}

	cleanRunOutputFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		cmdStr := name + " " + strings.Join(args, " ")
		executedCommands = append(executedCommands, cmdStr)
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

	for _, cmd := range executedCommands {
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
	for _, cmd := range executedCommands {
		if strings.Contains(cmd, "kubectl delete crd kafkas.kafka.strimzi.io") {
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

	cleanRunFn = func(ctx context.Context, name string, args ...string) error {
		cmdStr := name + " " + strings.Join(args, " ")
		executedCommands = append(executedCommands, cmdStr)
		if (name == "helm" && args[0] == "status") || (name == "kubectl" && args[0] == "get" && args[1] == "namespace") {
			return nil
		}
		return nil
	}

	cleanRunOutputFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		cmdStr := name + " " + strings.Join(args, " ")
		executedCommands = append(executedCommands, cmdStr)
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

	for _, cmd := range executedCommands {
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

	cleanRunFn = func(ctx context.Context, name string, args ...string) error {
		cmdStr := name + " " + strings.Join(args, " ")
		executedCommands = append(executedCommands, cmdStr)
		if (name == "helm" && args[0] == "status") || (name == "kubectl" && args[0] == "get" && args[1] == "namespace") {
			return nil
		}
		return nil
	}

	cleanRunOutputFn = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		cmdStr := name + " " + strings.Join(args, " ")
		executedCommands = append(executedCommands, cmdStr)
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

	for _, cmd := range executedCommands {
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
