package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// waitComponentReadySilent polls pods matching a label selector until all are
// Running/Succeeded or the timeout expires. It does NOT call
// dl.StartComponent/FinishComponent — the caller (deploy.go) is responsible
// for managing the dashboard component lifecycle.
func waitComponentReadySilent(ctx context.Context, namespace, selector string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := runExecOutputFn(ctx, "kubectl", "get", "pods", "-n", namespace, "-l", selector, "-o", "jsonpath={range .items[*]}{.status.phase}{','}{end}")
		if err == nil {
			phases := strings.Split(strings.TrimSpace(string(out)), ",")
			allReady := true
			count := 0
			for _, p := range phases {
				if p != "" {
					count++
					if p != "Running" && p != "Succeeded" {
						allReady = false
					}
				}
			}
			if count > 0 && allReady {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return fmt.Errorf("timeout waiting for pods (selector=%s) to become ready", selector)
}

// waitCustomResourceReadySilent polls custom resources matching a label selector
// until all have Ready=True condition or the timeout expires.
func waitCustomResourceReadySilent(ctx context.Context, kind, namespace, selector string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := runExecOutputFn(ctx, "kubectl", "get", kind, "-n", namespace, "-l", selector, "-o", "jsonpath={range .items[*]}{.status.conditions[?(@.type==\"Ready\")].status}{','}{end}")
		if err == nil {
			statuses := strings.Split(strings.TrimSpace(string(out)), ",")
			allReady := true
			count := 0
			for _, s := range statuses {
				if s != "" {
					count++
					if s != "True" {
						allReady = false
					}
				}
			}
			if count > 0 && allReady {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return fmt.Errorf("timeout waiting for %s to become ready", kind)
}

func waitNamedCustomResourceReadySilent(ctx context.Context, kind, namespace, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := runExecOutputFn(ctx, "kubectl", "get", kind, name, "-n", namespace, "-o", `jsonpath={.status.conditions[?(@.type=="Ready")].status}`)
		if err == nil && strings.Contains(string(out), "True") {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return fmt.Errorf("timeout waiting for %s/%s to become ready", kind, name)
}

func waitKafkaUsersReadySilent(ctx context.Context, namespace string, timeout time.Duration) error {
	return waitCustomResourceReadySilent(ctx, "kafkauser", namespace, "strimzi.io/cluster="+deployKafkaName, timeout)
}

func waitKafkaConnectReadySilent(ctx context.Context, namespace, name string, timeout time.Duration) error {
	return waitNamedCustomResourceReadySilent(ctx, "kafkaconnect", namespace, name, timeout)
}

func waitConnectorReadySilent(ctx context.Context, namespace string, timeout time.Duration) error {
	return waitCustomResourceReadySilent(ctx, "kafkaconnector", namespace, "strimzi.io/cluster=connect-cluster", timeout)
}
