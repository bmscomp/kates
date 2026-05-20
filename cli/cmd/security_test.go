package cmd

import (
	"fmt"
	"strings"
	"testing"
)

func TestSecurityNetpolCmd_DefaultNamespaces(t *testing.T) {
	// Mock runKubectlFn to simulate that get secrets returns empty (no custom Helm namespaces found)
	// and get networkpolicies returns some mock policies for the default namespaces.
	var executedArgs [][]string

	runKubectlFn = func(args ...string) (string, error) {
		executedArgs = append(executedArgs, args)

		cmdStr := strings.Join(args, " ")
		if strings.Contains(cmdStr, "get secrets") {
			return "", nil
		}
		if strings.Contains(cmdStr, "get networkpolicies") && strings.Contains(cmdStr, "-n kafka ") {
			return "kafka-allow-all:app=kafka:ingress,egress", nil
		}
		if strings.Contains(cmdStr, "get networkpolicies") && strings.Contains(cmdStr, "-n kates ") {
			return "kates-allow-kafka:app=kates:ingress", nil
		}
		if strings.Contains(cmdStr, "get networkpolicies") && strings.Contains(cmdStr, "-n strimzi-system ") {
			return "", nil // No policies
		}
		if strings.Contains(cmdStr, "get networkpolicies -A -o") {
			return "kafka/kafka-allow-all\nkates/kates-allow-kafka", nil
		}
		return "", fmt.Errorf("not found")
	}

	defer func() {
		runKubectlFn = runKubectlDefault
	}()

	err := securityNetpolCmd.RunE(securityNetpolCmd, nil)
	if err != nil {
		t.Fatalf("securityNetpolCmd failed: %v", err)
	}

	// Verify that the default namespaces were scanned
	scannedKafka := false
	scannedKates := false
	scannedStrimzi := false

	for _, args := range executedArgs {
		cmdStr := strings.Join(args, " ")
		if strings.Contains(cmdStr, "get networkpolicies") && strings.Contains(cmdStr, "-n kafka") {
			scannedKafka = true
		}
		if strings.Contains(cmdStr, "get networkpolicies") && strings.Contains(cmdStr, "-n kates") {
			scannedKates = true
		}
		if strings.Contains(cmdStr, "get networkpolicies") && strings.Contains(cmdStr, "-n strimzi-system") {
			scannedStrimzi = true
		}
	}

	if !scannedKafka || !scannedKates || !scannedStrimzi {
		t.Errorf("Expected all default namespaces to be scanned. Kafka: %t, Kates: %t, Strimzi: %t", scannedKafka, scannedKates, scannedStrimzi)
	}
}

func TestSecurityNetpolCmd_DynamicNamespaces(t *testing.T) {
	// Mock runKubectlFn to return custom namespaces from get secrets
	var executedNamespaces []string

	runKubectlFn = func(args ...string) (string, error) {
		cmdStr := strings.Join(args, " ")
		if strings.Contains(cmdStr, "get secrets") {
			return "custom-namespace-b custom-namespace-a kates", nil
		}
		if strings.Contains(cmdStr, "get networkpolicies") {
			// Extract namespace scanned
			for i, arg := range args {
				if arg == "-n" && i+1 < len(args) {
					executedNamespaces = append(executedNamespaces, args[i+1])
				}
			}
			return "some-policy:app=test:ingress", nil
		}
		return "", nil
	}

	defer func() {
		runKubectlFn = runKubectlDefault
	}()

	err := securityNetpolCmd.RunE(securityNetpolCmd, nil)
	if err != nil {
		t.Fatalf("securityNetpolCmd failed: %v", err)
	}

	// Namespaces should contain defaults + custom ones, sorted alphabetically:
	// custom-namespace-a, custom-namespace-b, kafka, kates, strimzi-system
	expectedOrder := []string{"custom-namespace-a", "custom-namespace-b", "kafka", "kates", "strimzi-system"}

	// Let's filter executedNamespaces to only keep the unique scanned ones in the order scanned
	var uniqueScanned []string
	seen := make(map[string]bool)
	for _, ns := range executedNamespaces {
		if !seen[ns] {
			seen[ns] = true
			uniqueScanned = append(uniqueScanned, ns)
		}
	}

	if len(uniqueScanned) != len(expectedOrder) {
		t.Fatalf("Expected %d unique scanned namespaces, got %d: %v", len(expectedOrder), len(uniqueScanned), uniqueScanned)
	}

	for i, ns := range expectedOrder {
		if uniqueScanned[i] != ns {
			t.Errorf("Expected namespace at index %d to be %s, got %s", i, ns, uniqueScanned[i])
		}
	}
}

// setupTest is defined in audit_test.go/testutil_test.go, so we can use standard helpers if needed,
// but we just run netpol directly and mock runKubectlFn.
